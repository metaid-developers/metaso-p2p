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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
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
	ChainName     string
	Timestamp     int64
	GenesisHeight int64
}

func DefaultBackfillPaths() []string {
	return append([]string(nil), defaultBackfillPaths...)
}

func NewBackfillClient(baseURL string, httpClient *http.Client) *BackfillClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &BackfillClient{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		httpClient: httpClient,
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

	// MANAPI pagination is not globally ordered by timestamp. First scan all
	// pages and retain only lightweight metadata, then compute the dependency
	// closure needed to fold recent interactions onto their canonical posts.
	metas := make(map[string]backfillMeta)
	for _, path := range paths {
		if err := opts.Client.scanPath(ctx, path, pageSize, func(page BackfillPage) error {
			for _, pin := range page.Pins {
				meta := backfillMetaFromPin(pin)
				if meta.ID != "" {
					metas[meta.ID] = meta
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	selected := selectBackfillPins(metas, opts.Since)
	if len(selected) == 0 {
		return nil
	}

	// Scan a second time so the first pass does not retain large content bodies.
	pins := make(map[string]*aggregator.PinInscription, len(selected))
	for _, path := range paths {
		if err := opts.Client.scanPath(ctx, path, pageSize, func(page BackfillPage) error {
			for _, pin := range page.Pins {
				if _, ok := selected[pin.ID]; ok {
					pins[pin.ID] = pin.toAggregatorPin()
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	ordered := make([]*aggregator.PinInscription, 0, len(pins))
	for _, pin := range pins {
		ordered = append(ordered, pin)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := backfillPathRank(ordered[i].Path), backfillPathRank(ordered[j].Path)
		if left != right {
			return left < right
		}
		if ordered[i].Timestamp != ordered[j].Timestamp {
			return ordered[i].Timestamp < ordered[j].Timestamp
		}
		if ordered[i].GenesisHeight != ordered[j].GenesisHeight {
			return ordered[i].GenesisHeight < ordered[j].GenesisHeight
		}
		return ordered[i].Id < ordered[j].Id
	})
	for _, pin := range ordered {
		if _, err := a.HandleBlockPin(pin); err != nil {
			return fmt.Errorf("replay social pin %s: %w", pin.Id, err)
		}
	}
	return nil
}

func (c *BackfillClient) scanPath(ctx context.Context, path string, size int, visit func(BackfillPage) error) error {
	cursor := ""
	seenCursors := make(map[string]struct{})
	for {
		if _, seen := seenCursors[cursor]; seen {
			return fmt.Errorf("repeated MANAPI cursor %q for path %s", cursor, path)
		}
		seenCursors[cursor] = struct{}{}
		page, err := c.ListPath(ctx, path, cursor, size)
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

func backfillMetaFromPin(pin BackfillPin) backfillMeta {
	converted := pin.toAggregatorPin()
	meta := backfillMeta{
		ID:            strings.TrimSpace(pin.ID),
		Path:          protocolPathFromPinPath(pin.Path),
		Operation:     strings.ToLower(strings.TrimSpace(pin.Operation)),
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

func selectBackfillPins(metas map[string]backfillMeta, since time.Time) map[string]struct{} {
	selected := make(map[string]struct{})
	for id, meta := range metas {
		if since.IsZero() || meta.Timestamp >= since.Unix() {
			selected[id] = struct{}{}
		}
	}
	resolveSource := func(id string) string {
		seen := make(map[string]struct{})
		for id != "" {
			if _, ok := seen[id]; ok {
				return id
			}
			seen[id] = struct{}{}
			meta, ok := metas[id]
			if !ok || meta.Path != PathSimpleBuzz || meta.Operation == OperationCreate || meta.TargetID == "" {
				return id
			}
			id = meta.TargetID
		}
		return id
	}
	for changed := true; changed; {
		changed = false
		sources := make(map[string]struct{})
		for id := range selected {
			meta, ok := metas[id]
			if !ok {
				continue
			}
			target := meta.TargetID
			if meta.Path == PathSimpleBuzz {
				target = id
			}
			if source := resolveSource(target); source != "" {
				sources[source] = struct{}{}
			}
		}
		for id, meta := range metas {
			_, sourceSelected := sources[resolveSource(id)]
			if meta.Path == PathSimpleBuzz && sourceSelected {
				if _, ok := selected[id]; !ok {
					selected[id] = struct{}{}
					changed = true
				}
			}
			_, targetSelected := sources[resolveSource(meta.TargetID)]
			if (meta.Path == PathPayLike || meta.Path == PathPayComment) && targetSelected {
				if _, ok := selected[id]; !ok {
					selected[id] = struct{}{}
					changed = true
				}
			}
		}
	}
	return selected
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
