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

	for _, path := range paths {
		cursor := ""
		seenCursors := make(map[string]struct{})
		pins := make([]*aggregator.PinInscription, 0, pageSize)
		for {
			if _, seen := seenCursors[cursor]; seen {
				return fmt.Errorf("repeated MANAPI cursor %q for path %s", cursor, path)
			}
			seenCursors[cursor] = struct{}{}
			page, err := opts.Client.ListPath(ctx, path, cursor, pageSize)
			if err != nil {
				return err
			}
			if len(page.Pins) == 0 {
				break
			}
			allOlder := true
			for _, pin := range page.Pins {
				if opts.Since.IsZero() || pin.Timestamp >= opts.Since.Unix() {
					allOlder = false
					pins = append(pins, pin.toAggregatorPin())
				}
			}
			if allOlder || page.NextCursor == "" || len(page.Pins) < pageSize {
				break
			}
			cursor = page.NextCursor
		}
		for i := len(pins) - 1; i >= 0; i-- {
			if _, err := a.HandleBlockPin(pins[i]); err != nil {
				return fmt.Errorf("replay %s pin %s: %w", path, pins[i].Id, err)
			}
		}
	}
	return nil
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
