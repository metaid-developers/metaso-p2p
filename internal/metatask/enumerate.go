package metatask

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Termination values for a path walk. Only the first is a complete
// enumeration; every other value is surfaced as UNKNOWN and blocks the
// "complete" flag, because a short delivery must never be accepted silently.
const (
	TermEmptyPageCursorExhausted = "empty_page_cursor_exhausted"
	TermFullPageNoCursor         = "full_page_no_cursor"
	TermEmptyPageWithCursor      = "empty_page_with_cursor"
	TermListNullWithCursor       = "list_null_with_cursor"
	TermListNullEmptyCursor      = "list_null_empty_cursor"
	TermListFieldMissing         = "list_field_missing"
	TermMaxPages                 = "max_pages_reached"
	TermFetchError               = "fetch_error"
)

// MaxPages bounds a walk so a server that never exhausts its cursor cannot spin
// forever; hitting it is reported as an incomplete enumeration.
const MaxPages = 400

// Page is one raw list response.
type Page struct {
	Pins []*Pin

	URL           string
	HTTPStatus    int
	Raw           []byte
	RawSHA256     string
	FetchedAt     time.Time
	CursorIn      string
	NextCursor    string
	ListPresent   bool
	ListNull      bool
	CursorPresent bool
	Total         int64
}

// Lister fetches one page of a protocol path.
type Lister interface {
	ListPath(ctx context.Context, protocolPath, cursor string, size int) (Page, error)
}

// PayloadSource fetches the byte-exact payload of one pin.
type PayloadSource interface {
	FetchPayload(ctx context.Context, pin *Pin) (raw []byte, channel string, err error)
}

// Enumerate walks one protocol path with cursor paging until an explicit empty
// page is reached. Every stop condition is recorded.
func Enumerate(ctx context.Context, lister Lister, protocolPath string, size int) ([]*Pin, PageEnumeration, error) {
	if size <= 0 {
		size = 50
	}
	report := PageEnumeration{ProtocolPath: protocolPath, Termination: TermEmptyPageCursorExhausted}
	seen := map[string]bool{}
	var pins []*Pin
	cursor := ""
	for page := 0; page < MaxPages; page++ {
		p, err := lister.ListPath(ctx, protocolPath, cursor, size)
		if err != nil {
			report.Termination = TermFetchError
			report.Complete = false
			return pins, report, err
		}
		report.Pages++
		report.PageHashes = append(report.PageHashes, p.RawSHA256)
		report.PageSizes = append(report.PageSizes, len(p.Pins))
		report.Totals = append(report.Totals, p.Total)
		if report.FetchedAt == "" || p.FetchedAt.After(parseRFC3339(report.FetchedAt)) {
			report.FetchedAt = nowRFC3339(p.FetchedAt)
		}
		if !p.ListPresent {
			report.Termination = TermListFieldMissing
			return pins, report, nil
		}
		for _, pin := range p.Pins {
			report.Records++
			if seen[pin.ID] {
				report.Duplicates++
				continue
			}
			seen[pin.ID] = true
			pins = append(pins, pin)
		}
		switch {
		case p.ListNull && strings.TrimSpace(p.NextCursor) == "":
			report.Termination = TermListNullEmptyCursor
			report.CursorExhausted = true
			report.Complete = true
			report.Unique = len(pins)
			return pins, report, nil
		case p.ListNull:
			// The server handed back a null page while still advertising a
			// next cursor: the walk is incomplete and MUST NOT be read as an
			// exhausted pool (the classic empty-list false negative).
			report.Termination = TermListNullWithCursor
			report.Unique = len(pins)
			return pins, report, nil
		case len(p.Pins) == 0 && strings.TrimSpace(p.NextCursor) == "":
			report.Termination = TermEmptyPageCursorExhausted
			report.CursorExhausted = true
			report.Complete = true
			report.Unique = len(pins)
			return pins, report, nil
		case len(p.Pins) == 0:
			report.Termination = TermEmptyPageWithCursor
			report.Unique = len(pins)
			return pins, report, nil
		case strings.TrimSpace(p.NextCursor) == "":
			report.Termination = TermFullPageNoCursor
			report.Unique = len(pins)
			return pins, report, nil
		}
		cursor = p.NextCursor
	}
	report.Termination = TermMaxPages
	report.Unique = len(pins)
	return pins, report, nil
}

// Enumeration is the cross-path walk report.
func EnumerateAll(ctx context.Context, lister Lister, paths []string, size int) (map[string][]*Pin, Enumeration, error) {
	enum := Enumeration{Size: size, Paths: map[string]PageEnumeration{}, Complete: true}
	events := map[string][]*Pin{}
	ids := []string{}
	for _, path := range paths {
		pins, rep, err := Enumerate(ctx, lister, path, size)
		if err != nil {
			return events, enum, err
		}
		events[path] = pins
		enum.Paths[path] = rep
		if !rep.Complete {
			enum.Complete = false
			enum.Unresolved = append(enum.Unresolved, path)
		}
		for _, p := range pins {
			ids = append(ids, p.ID)
		}
	}
	sort.Strings(ids)
	sum := sha256.Sum256([]byte(strings.Join(ids, "\n")))
	enum.PinSetSHA256 = hex.EncodeToString(sum[:])
	return events, enum, nil
}

// LoadPayloads fills each pin's byte-exact payload from the payload source,
// applying the §〇 acceptance rules and recording the verdict on the pin.
func LoadPayloads(ctx context.Context, pins []*Pin, source PayloadSource) {
	for _, p := range pins {
		raw, channel, err := source.FetchPayload(ctx, p)
		if err != nil {
			p.PayloadState = PayloadEmpty
			p.PayloadError = err.Error()
			continue
		}
		p.Payload = raw
		p.PayloadChannel = channel
		v := acceptPayload(raw, p.ContentLength, channel)
		p.PayloadState = v.State
		if !v.Accepted() {
			p.PayloadError = v.Reason
		}
	}
}

// ---- HTTP lister (manapi) ----

// HTTPLister walks a protocol path through the indexer's paginated list
// endpoint. It performs no retries: a failed page surfaces as an incomplete
// enumeration rather than a silently shorter walk.
type HTTPLister struct {
	BaseURL string
	Client  *http.Client
}

// NewHTTPLister builds a lister for an indexer base URL (e.g.
// https://manapi.metaid.io).
func NewHTTPLister(baseURL string) *HTTPLister {
	return &HTTPLister{BaseURL: strings.TrimRight(baseURL, "/"), Client: &http.Client{Timeout: 60 * time.Second}}
}

// ListPath fetches one page.
func (h *HTTPLister) ListPath(ctx context.Context, protocolPath, cursor string, size int) (Page, error) {
	q := url.Values{}
	q.Set("path", protocolPath)
	q.Set("size", strconv.Itoa(size))
	if cursor != "" {
		// The cursor is a composite "<hex>&<ts>&<chain>&<height>&<pinId>"
		// string; it must be query-encoded, never concatenated raw, or the
		// server answers with a null list that looks like an exhausted pool.
		q.Set("cursor", cursor)
	}
	full := h.BaseURL + "/api/pin/path/list?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return Page{}, err
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		return Page{}, fmt.Errorf("list %s: %w", protocolPath, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Page{}, fmt.Errorf("read list %s: %w", protocolPath, err)
	}
	page, err := decodePage(body)
	if err != nil {
		return Page{}, err
	}
	page.URL = full
	page.HTTPStatus = resp.StatusCode
	page.FetchedAt = time.Now().UTC()
	page.CursorIn = cursor
	return page, nil
}

// FetchPayload reads the pin content endpoint. The endpoint accepts the bare
// pinId only — an extension suffix is rejected by the server.
func (h *HTTPLister) FetchPayload(ctx context.Context, pin *Pin) ([]byte, string, error) {
	if len(pin.Payload) > 0 {
		return pin.Payload, pin.PayloadChannel, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.BaseURL+"/content/"+pin.ID, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("fetch content %s: %w", pin.ID, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read content %s: %w", pin.ID, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "content", fmt.Errorf("content %s returned HTTP %d", pin.ID, resp.StatusCode)
	}
	return body, "content", nil
}

func decodePage(raw []byte) (Page, error) {
	page := Page{Raw: raw}
	sum := sha256.Sum256(raw)
	page.RawSHA256 = hex.EncodeToString(sum[:])
	var env struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    *struct {
			List       []map[string]any `json:"list"`
			NextCursor *string          `json:"nextCursor"`
			Total      int64            `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return page, fmt.Errorf("decode list response: %w", err)
	}
	if env.Data == nil {
		return page, fmt.Errorf("list response has no data object")
	}
	page.ListPresent = true
	page.Total = env.Data.Total
	if env.Data.List == nil {
		page.ListNull = true
	} else {
		for _, item := range env.Data.List {
			page.Pins = append(page.Pins, pinFromMap(item))
		}
	}
	if env.Data.NextCursor != nil {
		page.CursorPresent = true
		page.NextCursor = *env.Data.NextCursor
	}
	return page, nil
}

func pinFromMap(m map[string]any) *Pin {
	p := &Pin{}
	for k, v := range m {
		switch k {
		case "id":
			p.ID = asString(v)
		case "path":
			p.Path = asString(v)
		case "operation":
			p.Operation = asString(v)
		case "globalMetaId":
			p.GlobalMetaID = asString(v)
		case "metaId":
			p.MetaID = asString(v)
		case "address":
			p.Address = asString(v)
		case "genesisHeight":
			p.GenesisHeight = asInt(v, -1)
		case "txIndex":
			p.TxIndex = asInt(v, 0)
		case "timestamp":
			p.Timestamp = asInt(v, 0)
		case "seenTime":
			p.SeenTime = asInt(v, 0)
		case "genesisTransaction":
			p.GenesisTransaction = asString(v)
		case "genesisFee":
			p.GenesisFee = asInt(v, 0)
		case "outputValue":
			p.OutputValue = asInt(v, 0)
		case "number":
			p.Number = asInt(v, -1)
		case "version":
			p.Version = asInt(v, 0)
		case "contentLength":
			p.ContentLength = asInt(v, 0)
		case "contentSummary":
			p.ContentSummary = asString(v)
		case "contentBody":
			if raw, err := DecodeBase64Payload(asString(v)); err == nil && len(raw) > 0 {
				p.Payload = raw
				p.PayloadChannel = "contentBody"
			}
		}
	}
	return p
}

func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return strings.Trim(string(b), `"`)
	}
}

func asInt(v any, def int64) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case int64:
		return t
	case int:
		return int64(t)
	case json.Number:
		n, err := t.Int64()
		if err != nil {
			return def
		}
		return n
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		if err != nil {
			return def
		}
		return n
	}
	return def
}

func parseRFC3339(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// ---- frozen snapshot lister ----

// SnapshotLister replays a frozen crawl: it serves the recorded page for each
// cursor and the recorded payload bytes for each pin, so an aggregation run is
// reproducible without touching the network.
//
// Layout under Dir (as written by the crawl tool):
//
//	pages/<pool>/<n>.json       raw list responses in fetch order
//	content/<pinId>.bin         byte-exact payloads
type SnapshotLister struct {
	Dir string

	pages   map[string]map[string][]byte // path -> cursorIn -> raw page
	ordered map[string][]string
	loaded  bool
}

// NewSnapshotLister opens a frozen crawl directory.
func NewSnapshotLister(dir string) *SnapshotLister {
	return &SnapshotLister{Dir: dir, pages: map[string]map[string][]byte{}, ordered: map[string][]string{}}
}

func (l *SnapshotLister) load() error {
	if l.loaded {
		return nil
	}
	for _, path := range EventPaths {
		pool := PoolName(path)
		dir := filepath.Join(l.Dir, "pages", pool)
		entries, err := filepath.Glob(filepath.Join(dir, "*.json"))
		if err != nil {
			return err
		}
		nums := make([]int, 0, len(entries))
		byNum := map[int]string{}
		for _, e := range entries {
			n, err := strconv.Atoi(strings.TrimSuffix(filepath.Base(e), ".json"))
			if err != nil {
				continue
			}
			nums = append(nums, n)
			byNum[n] = e
		}
		sort.Ints(nums)
		l.pages[path] = map[string][]byte{}
		cursorIn := ""
		for _, n := range nums {
			raw, err := os.ReadFile(byNum[n])
			if err != nil {
				return err
			}
			l.pages[path][cursorIn] = raw
			l.ordered[path] = append(l.ordered[path], cursorIn)
			page, err := decodePage(raw)
			if err != nil {
				return fmt.Errorf("snapshot page %s: %w", byNum[n], err)
			}
			cursorIn = page.NextCursor
		}
	}
	l.loaded = true
	return nil
}

// ListPath serves the recorded page for the given cursor.
func (l *SnapshotLister) ListPath(_ context.Context, protocolPath, cursor string, _ int) (Page, error) {
	if err := l.load(); err != nil {
		return Page{}, err
	}
	raw, ok := l.pages[protocolPath][cursor]
	if !ok {
		return Page{}, fmt.Errorf("snapshot %s has no recorded page for cursor %q", protocolPath, cursor)
	}
	page, err := decodePage(raw)
	if err != nil {
		return Page{}, err
	}
	page.URL = "snapshot:" + protocolPath
	page.HTTPStatus = 200
	page.CursorIn = cursor
	if fi, err := os.Stat(filepath.Join(l.Dir, "pages", PoolName(protocolPath))); err == nil {
		page.FetchedAt = fi.ModTime().UTC()
	}
	return page, nil
}

// FetchPayload serves the recorded payload bytes for a pin.
func (l *SnapshotLister) FetchPayload(_ context.Context, pin *Pin) ([]byte, string, error) {
	if len(pin.Payload) > 0 {
		return pin.Payload, pin.PayloadChannel, nil
	}
	raw, err := os.ReadFile(filepath.Join(l.Dir, "content", pin.ID+".bin"))
	if err != nil {
		return nil, "content", err
	}
	return raw, "content", nil
}
