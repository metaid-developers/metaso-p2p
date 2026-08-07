package socialcontent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cockroachdb/pebble"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

const defaultBackfillPageSize = 100

var defaultBackfillPaths = []string{PathSimpleBuzz, PathPayLike, PathPayComment}

type BackfillOptions struct {
	Context  context.Context
	Client   *BackfillClient
	Paths    []string
	Since    time.Time
	PageSize int
}

type BackfillClient struct {
	baseURL    string
	httpClient *http.Client
}

type BackfillPage struct {
	Pins       []BackfillPin
	NextCursor string
}

type BackfillPin struct {
	ID             string       `json:"id"`
	Path           string       `json:"path"`
	OriginalPath   string       `json:"originalPath"`
	Operation      string       `json:"operation"`
	ContentType    string       `json:"contentType"`
	ContentBody    backfillBody `json:"contentBody"`
	ContentSummary string       `json:"contentSummary"`
	MetaId         string       `json:"metaId"`
	GlobalMetaId   string       `json:"globalMetaId"`
	Address        string       `json:"address"`
	CreateMetaId   string       `json:"createMetaId"`
	CreateAddress  string       `json:"createAddress"`
	ChainName      string       `json:"chainName"`
	Timestamp      int64        `json:"timestamp"`
	GenesisHeight  int64        `json:"genesisHeight"`
	OriginalId     string       `json:"originalId"`
}

type backfillMeta struct {
	ID            string
	Path          string
	Operation     string
	TargetID      string
	OriginalID    string
	ChainName     string
	Timestamp     int64
	GenesisHeight int64
}

func DefaultBackfillPaths() []string {
	return append([]string(nil), defaultBackfillPaths...)
}

func NewBackfillClient(baseURL string, httpClient *http.Client) *BackfillClient {
	if httpClient == nil {
		httpClient = newBackfillHTTPClient()
	}
	return &BackfillClient{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		httpClient: httpClient,
	}
}

// newBackfillHTTPClient returns a client with an overall request timeout.
// It keeps the standard transport (HTTP/2 capable) because MANAPI's nginx
// accepts those handshakes reliably, and the timeout prevents a stale stream
// from blocking the backfill forever.
func newBackfillHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   60 * time.Second,
		Transport: http.DefaultTransport,
	}
}

func (a *Aggregator) Backfill(opts BackfillOptions) error {
	if a == nil || a.store == nil {
		return errors.New("socialcontent backfill aggregator is required")
	}
	if opts.Client == nil {
		return errors.New("socialcontent backfill client is required")
	}
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = defaultBackfillPageSize
	}
	paths := normaliseBackfillPaths(opts.Paths)

	spool, err := newPinSpool()
	if err != nil {
		return err
	}
	defer spool.Close()

	// Reference and selection sets live in a temporary Pebble store so the
	// backfill memory stays bounded no matter how large the lookback window is.
	refsStore := storage.NewPebbleStore(filepath.Join(spool.dir, "sets"))
	defer refsStore.Close()
	if _, err := refsStore.OpenDB(refsNamespace); err != nil {
		return fmt.Errorf("open backfill refs set: %w", err)
	}
	if _, err := refsStore.OpenDB(selectedNamespace); err != nil {
		return fmt.Errorf("open backfill selected set: %w", err)
	}

	// MANAPI pagination is not globally ordered by timestamp, so every page is
	// scanned. In-window pins are spooled to disk and their IDs, targets, and
	// originals are indexed in the on-disk refs set; RAM stays independent of
	// the historical size.
	lookbackUnix := int64(0)
	if !opts.Since.IsZero() {
		lookbackUnix = opts.Since.Unix()
	}
	for _, path := range paths {
		if err := opts.Client.scanPath(ctx, path, pageSize, func(page BackfillPage) error {
			var refs []storage.KeyValue
			var inWindow []BackfillPin
			for _, pin := range page.Pins {
				meta := backfillMetaFromPin(pin)
				if meta.ID == "" || (lookbackUnix != 0 && meta.Timestamp < lookbackUnix) {
					continue
				}
				inWindow = append(inWindow, pin)
				refs = append(refs, storage.KeyValue{Key: []byte(meta.ID)})
				if meta.TargetID != "" {
					refs = append(refs, storage.KeyValue{Key: []byte(meta.TargetID)})
				}
				if meta.OriginalID != "" {
					refs = append(refs, storage.KeyValue{Key: []byte(meta.OriginalID)})
				}
			}
			if len(refs) > 0 {
				if err := refsStore.SetBatchNoSync(refsNamespace, refs); err != nil {
					return fmt.Errorf("index backfill refs for %s: %w", path, err)
				}
			}
			if len(inWindow) > 0 {
				if err := spool.appendPins(spoolName(path), inWindow); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return fmt.Errorf("scan %s: %w", path, err)
		}
	}

	// Resolve every simplebuzz version referenced by the in-window set,
	// including older modify/revoke chains, and spool the selected posts.
	if containsPath(paths, PathSimpleBuzz) {
		if err := opts.Client.scanPath(ctx, PathSimpleBuzz, pageSize, func(page BackfillPage) error {
			pageSelected := make(map[string]struct{}, len(page.Pins))
			changed := true
			for changed {
				changed = false
				var refs []storage.KeyValue
				var selected []storage.KeyValue
				var out []BackfillPin
				for _, pin := range page.Pins {
					meta := backfillMetaFromPin(pin)
					if meta.ID == "" {
						continue
					}
					if _, ok := pageSelected[meta.ID]; ok {
						continue
					}
					referenced, err := refsStore.Get(refsNamespace, []byte(meta.ID))
					if err != nil && !errors.Is(err, pebble.ErrNotFound) {
						return err
					}
					if referenced == nil {
						if meta.OriginalID == "" {
							continue
						}
						original, err := refsStore.Get(refsNamespace, []byte(meta.OriginalID))
						if err != nil && !errors.Is(err, pebble.ErrNotFound) {
							return err
						}
						if original == nil {
							continue
						}
					}
					pageSelected[meta.ID] = struct{}{}
					out = append(out, pin)
					selected = append(selected, storage.KeyValue{Key: []byte(meta.ID)})
					if meta.OriginalID != "" {
						original, err := refsStore.Get(refsNamespace, []byte(meta.OriginalID))
						if err != nil && !errors.Is(err, pebble.ErrNotFound) {
							return err
						}
						if original == nil {
							refs = append(refs, storage.KeyValue{Key: []byte(meta.OriginalID)})
							changed = true
						}
					}
				}
				if len(refs) > 0 {
					if err := refsStore.SetBatchNoSync(refsNamespace, refs); err != nil {
						return fmt.Errorf("extend backfill refs: %w", err)
					}
				}
				if len(selected) > 0 {
					if err := refsStore.SetBatchNoSync(selectedNamespace, selected); err != nil {
						return fmt.Errorf("index backfill selected posts: %w", err)
					}
				}
				if len(out) > 0 {
					if err := spool.appendPins(spoolName(PathSimpleBuzz)+".selected", out); err != nil {
						return err
					}
				}
			}
			return nil
		}); err != nil {
			return fmt.Errorf("scan %s: %w", PathSimpleBuzz, err)
		}
	}

	// Keep only interactions whose target post was selected for replay.
	for _, path := range paths {
		if path == PathSimpleBuzz {
			continue
		}
		if err := spool.filterPins(spoolName(path), spoolName(path)+".selected", func(pin BackfillPin) (bool, error) {
			meta := backfillMetaFromPin(pin)
			if meta.TargetID == "" {
				return false, nil
			}
			target, err := refsStore.Get(selectedNamespace, []byte(meta.TargetID))
			if err != nil {
				if errors.Is(err, pebble.ErrNotFound) {
					return false, nil
				}
				return false, err
			}
			return target != nil, nil
		}); err != nil {
			return err
		}
	}

	// Replay posts, likes, and comments in deterministic order. Each path is
	// externally sorted on disk, so replay memory is bounded by one chunk.
	replay := func(pin *aggregator.PinInscription) error {
		if err := a.HandleBlockPinReplay(pin); err != nil {
			return fmt.Errorf("replay social pin %s: %w", pin.Id, err)
		}
		return nil
	}
	for _, path := range orderedBackfillPaths(paths) {
		if err := spool.sortAndReplay(spoolName(path)+".selected", replay); err != nil {
			return err
		}
	}
	return nil
}

const (
	refsNamespace     = "refs"
	selectedNamespace = "selected"
)

func spoolName(path string) string {
	return strings.TrimPrefix(protocolPathFromPinPath(path), "/protocols/")
}

func containsPath(paths []string, target string) bool {
	for _, path := range paths {
		if protocolPathFromPinPath(path) == target {
			return true
		}
	}
	return false
}

func orderedBackfillPaths(paths []string) []string {
	ordered := append([]string(nil), paths...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return backfillPathRank(ordered[i]) < backfillPathRank(ordered[j])
	})
	return ordered
}

func (c *BackfillClient) scanPath(ctx context.Context, path string, size int, visit func(BackfillPage) error) error {
	cursor := ""
	seenCursors := make(map[string]struct{})
	for {
		if _, seen := seenCursors[cursor]; seen {
			return fmt.Errorf("repeated MANAPI cursor %q for path %s", cursor, path)
		}
		seenCursors[cursor] = struct{}{}
		page, err := c.listPathWithRetry(ctx, path, cursor, size)
		if err != nil {
			return err
		}
		if len(page.Pins) == 0 {
			return nil
		}
		if err := visit(page); err != nil {
			return err
		}
		if page.NextCursor == "" {
			return nil
		}
		cursor = page.NextCursor
	}
}

const backfillMaxRetries = 3

// listPathWithRetry retries transient MANAPI failures (timeouts, resets, EOF)
// with a short backoff so a long historical crawl survives a bad page.
func (c *BackfillClient) listPathWithRetry(ctx context.Context, path, cursor string, size int) (BackfillPage, error) {
	var lastErr error
	for attempt := 0; attempt <= backfillMaxRetries; attempt++ {
		page, err := c.ListPath(ctx, path, cursor, size)
		if err == nil {
			return page, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return BackfillPage{}, ctx.Err()
		}
		if attempt < backfillMaxRetries {
			time.Sleep(time.Duration(1<<attempt) * time.Second)
		}
	}
	return BackfillPage{}, lastErr
}

func backfillMetaFromPin(pin BackfillPin) backfillMeta {
	converted := pin.toAggregatorPin()
	meta := backfillMeta{
		ID:            strings.TrimSpace(pin.ID),
		Path:          protocolPathFromPinPath(pin.Path),
		Operation:     strings.ToLower(strings.TrimSpace(pin.Operation)),
		OriginalID:    strings.TrimSpace(pin.OriginalId),
		ChainName:     strings.ToLower(strings.TrimSpace(pin.ChainName)),
		Timestamp:     pin.Timestamp,
		GenesisHeight: pin.GenesisHeight,
	}
	if converted == nil {
		return meta
	}
	meta.TargetID = targetPinID(converted)
	if meta.TargetID == "" {
		switch meta.Path {
		case PathPayLike:
			if event, err := parseLike(converted); err == nil {
				meta.TargetID = event.TargetPinId
			}
		case PathPayComment:
			if comment, err := parseComment(converted); err == nil {
				meta.TargetID = comment.TargetPinId
			}
		}
	}
	return meta
}

func backfillPathRank(path string) int {
	switch protocolPathFromPinPath(path) {
	case PathSimpleBuzz:
		return 0
	case PathPayLike:
		return 1
	case PathPayComment:
		return 2
	default:
		return 3
	}
}

func normaliseBackfillPaths(paths []string) []string {
	allowed := make(map[string]struct{}, len(defaultBackfillPaths))
	for _, path := range defaultBackfillPaths {
		allowed[path] = struct{}{}
	}
	if len(paths) == 0 {
		return DefaultBackfillPaths()
	}
	out := make([]string, 0, len(paths))
	seen := make(map[string]struct{})
	for _, path := range paths {
		path = protocolPathFromPinPath(path)
		if _, ok := allowed[path]; !ok {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	if len(out) == 0 {
		return DefaultBackfillPaths()
	}
	return out
}

func (c *BackfillClient) ListPath(ctx context.Context, path, cursor string, size int) (BackfillPage, error) {
	if c == nil || c.baseURL == "" {
		return BackfillPage{}, errors.New("MANAPI base URL is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	requestURL, err := c.listURL(path, cursor, size)
	if err != nil {
		return BackfillPage{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return BackfillPage{}, fmt.Errorf("create MANAPI backfill request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return BackfillPage{}, fmt.Errorf("fetch MANAPI backfill pins: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return BackfillPage{}, fmt.Errorf("MANAPI backfill returned HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return BackfillPage{}, fmt.Errorf("read MANAPI backfill response: %w", err)
	}
	return decodeBackfillPage(raw)
}

func (c *BackfillClient) listURL(path, cursor string, size int) (string, error) {
	parsed, err := url.Parse(c.baseURL)
	if err != nil {
		return "", fmt.Errorf("parse MANAPI base URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("MANAPI base URL requires scheme and host")
	}
	if parsed.Path == "" || parsed.Path == "/" {
		parsed.Path = "/pin/path/list"
	}
	query := parsed.Query()
	query.Set("path", path)
	query.Set("cursor", cursor)
	query.Set("size", strconv.Itoa(size))
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func decodeBackfillPage(raw []byte) (BackfillPage, error) {
	var envelope struct {
		Code       int           `json:"code"`
		Message    string        `json:"message"`
		List       []BackfillPin `json:"list"`
		NextCursor string        `json:"nextCursor"`
		Cursor     string        `json:"cursor"`
		Data       struct {
			List       []BackfillPin `json:"list"`
			NextCursor string        `json:"nextCursor"`
			Cursor     string        `json:"cursor"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return BackfillPage{}, fmt.Errorf("decode MANAPI backfill response: %w", err)
	}
	if envelope.Code != 0 && envelope.Code != 1 {
		return BackfillPage{}, fmt.Errorf("MANAPI backfill failed: code=%d message=%s", envelope.Code, envelope.Message)
	}
	page := BackfillPage{
		Pins:       envelope.Data.List,
		NextCursor: firstNonEmpty(envelope.Data.NextCursor, envelope.Data.Cursor, envelope.NextCursor, envelope.Cursor),
	}
	if page.Pins == nil {
		page.Pins = envelope.List
	}
	return page, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (p BackfillPin) toAggregatorPin() *aggregator.PinInscription {
	return &aggregator.PinInscription{
		Id:             strings.TrimSpace(p.ID),
		Path:           strings.TrimSpace(p.Path),
		OriginalPath:   strings.TrimSpace(p.OriginalPath),
		Operation:      strings.TrimSpace(p.Operation),
		ContentType:    strings.TrimSpace(p.ContentType),
		ContentBody:    p.ContentBody.Bytes(),
		ContentSummary: strings.TrimSpace(p.ContentSummary),
		MetaId:         strings.TrimSpace(p.MetaId),
		GlobalMetaId:   strings.TrimSpace(p.GlobalMetaId),
		Address:        strings.TrimSpace(p.Address),
		CreateMetaId:   strings.TrimSpace(p.CreateMetaId),
		CreateAddress:  strings.TrimSpace(p.CreateAddress),
		ChainName:      strings.TrimSpace(p.ChainName),
		Timestamp:      p.Timestamp,
		GenesisHeight:  p.GenesisHeight,
		OriginalId:     strings.TrimSpace(p.OriginalId),
	}
}

type backfillBody []byte

func (b *backfillBody) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*b = nil
		return nil
	}
	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		text = strings.TrimSpace(text)
		if decoded, ok := maybeDecodeBase64Content(text); ok {
			*b = decoded
		} else {
			*b = []byte(text)
		}
		return nil
	}
	*b = append((*b)[:0], trimmed...)
	return nil
}

func (b backfillBody) Bytes() []byte { return append([]byte(nil), b...) }

func maybeDecodeBase64Content(text string) ([]byte, bool) {
	if strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") {
		return nil, false
	}
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		decoded, err := encoding.DecodeString(text)
		if err == nil && json.Valid(decoded) {
			return decoded, true
		}
	}
	return nil, false
}
