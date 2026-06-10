package publishedcontent

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

const defaultBackfillPageSize = 100

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
	client := opts.Client
	if client == nil {
		return errors.New("publishedcontent backfill client is required")
	}
	paths := normaliseBackfillPaths(opts.Paths)
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = defaultBackfillPageSize
	}
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}

	for _, path := range paths {
		cursor := ""
		seenCursors := make(map[string]struct{})
		for {
			seenCursors[cursor] = struct{}{}
			page, err := client.ListPath(ctx, path, cursor, pageSize)
			if err != nil {
				return err
			}
			if len(page.Pins) == 0 {
				break
			}

			allOlder := true
			for _, pin := range page.Pins {
				if manapiTimestampBefore(pin.Timestamp, opts.Since) {
					continue
				}
				allOlder = false
				inscription := pin.toAggregatorPin()
				if err := a.processPin(inscription, false); err != nil {
					return err
				}
			}
			if allOlder {
				break
			}
			if page.NextCursor == "" || len(page.Pins) < pageSize {
				break
			}
			if _, seen := seenCursors[page.NextCursor]; seen {
				return fmt.Errorf("repeated MANAPI cursor %q for path %s", page.NextCursor, path)
			}
			cursor = page.NextCursor
		}
	}
	return nil
}

func normaliseBackfillPaths(paths []string) []string {
	if len(paths) == 0 {
		return []string{PathSimpleBuzz, PathMetaApp, PathMetaBotSkill}
	}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if normalised := protocolPathFromPinPath(path); normalised != "" {
			out = append(out, normalised)
		}
	}
	return out
}

type backfillPage struct {
	Pins       []manapiPin
	NextCursor string
}

func (c *BackfillClient) ListPath(ctx context.Context, path, cursor string, size int) (backfillPage, error) {
	if c == nil || strings.TrimSpace(c.baseURL) == "" {
		return backfillPage{}, errors.New("MANAPI base URL is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	requestURL, err := c.listURL(path, cursor, size)
	if err != nil {
		return backfillPage{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return backfillPage{}, fmt.Errorf("create MANAPI backfill request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return backfillPage{}, fmt.Errorf("fetch MANAPI backfill pins: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return backfillPage{}, fmt.Errorf("MANAPI backfill returned HTTP %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return backfillPage{}, fmt.Errorf("read MANAPI backfill response: %w", err)
	}
	page, err := decodeBackfillPage(raw)
	if err != nil {
		return backfillPage{}, err
	}
	return page, nil
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
	q := parsed.Query()
	q.Set("cursor", cursor)
	q.Set("size", strconv.Itoa(size))
	q.Set("path", path)
	parsed.RawQuery = q.Encode()
	return parsed.String(), nil
}

func decodeBackfillPage(raw []byte) (backfillPage, error) {
	var envelope struct {
		Code       int         `json:"code"`
		Message    string      `json:"message"`
		List       []manapiPin `json:"list"`
		NextCursor string      `json:"nextCursor"`
		Cursor     string      `json:"cursor"`
		Data       struct {
			List       []manapiPin `json:"list"`
			NextCursor string      `json:"nextCursor"`
			Cursor     string      `json:"cursor"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return backfillPage{}, fmt.Errorf("decode MANAPI backfill response: %w", err)
	}
	if envelope.Code != 0 && envelope.Code != 1 {
		return backfillPage{}, fmt.Errorf("MANAPI backfill failed: code=%d message=%s", envelope.Code, envelope.Message)
	}
	page := backfillPage{
		Pins:       envelope.Data.List,
		NextCursor: firstNonEmpty(envelope.Data.NextCursor, envelope.Data.Cursor, envelope.NextCursor, envelope.Cursor),
	}
	if page.Pins == nil {
		page.Pins = envelope.List
	}
	return page, nil
}

type manapiPin struct {
	ID             string             `json:"id"`
	Path           string             `json:"path"`
	OriginalPath   string             `json:"originalPath"`
	Operation      string             `json:"operation"`
	ContentType    string             `json:"contentType"`
	ContentBody    manapiContentBytes `json:"contentBody"`
	ContentSummary string             `json:"contentSummary"`
	MetaId         string             `json:"metaId"`
	GlobalMetaId   string             `json:"globalMetaId"`
	Address        string             `json:"address"`
	CreateMetaId   string             `json:"createMetaId"`
	CreateAddress  string             `json:"createAddress"`
	ChainName      string             `json:"chainName"`
	Timestamp      int64              `json:"timestamp"`
	GenesisHeight  int64              `json:"genesisHeight"`
	OriginalId     string             `json:"originalId"`
}

func (p manapiPin) toAggregatorPin() *aggregator.PinInscription {
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

type manapiContentBytes []byte

func (b *manapiContentBytes) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*b = nil
		return nil
	}

	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		text = strings.TrimSpace(text)
		if text == "" {
			*b = nil
			return nil
		}
		if decoded, ok := maybeDecodeBase64Content(text); ok {
			*b = decoded
			return nil
		}
		*b = []byte(text)
		return nil
	}

	var raw json.RawMessage
	if err := json.Unmarshal(trimmed, &raw); err == nil {
		*b = append((*b)[:0], raw...)
		return nil
	}
	*b = append((*b)[:0], trimmed...)
	return nil
}

func (b manapiContentBytes) Bytes() []byte {
	return append([]byte(nil), b...)
}

func maybeDecodeBase64Content(text string) ([]byte, bool) {
	if strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") {
		return nil, false
	}
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	for _, encoding := range encodings {
		decoded, err := encoding.DecodeString(text)
		if err != nil || len(decoded) == 0 {
			continue
		}
		if json.Valid(decoded) {
			return decoded, true
		}
	}
	return nil, false
}

func manapiTimestampBefore(timestamp int64, since time.Time) bool {
	if since.IsZero() || timestamp <= 0 {
		return false
	}
	cutoffMillis := since.UnixMilli()
	if timestamp > 100000000000 {
		return timestamp < cutoffMillis
	}
	return timestamp < since.Unix()
}
