package agentpedia

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// L2 is the application-layer aggregator (second deliverable §4.1): it consumes
// the L0 chain primitive (manapi pins_by_path), replays through the local L1
// engine, and serves the read-side queries. Zero write paths by design — the
// human-facing reader contract.
type L2 struct {
	mu       sync.Mutex
	view     *View
	events   []Event
	pinMeta  map[string]pinMeta // pinId -> chain position/author
	contents map[string]string  // rev pinId -> inline content (backlinks)
	cursors  map[string]string  // path -> manapi nextCursor (lastCursor)
	seenPins map[string]bool
	manapi   string
	client   *http.Client
}

type pinMeta struct {
	height  int64
	txIndex int
	sender  string
	path    string
	title   string
}

// NewL2 builds an aggregator against a manapi base URL (e.g. https://manapi.metaid.io).
func NewL2(manapiBase string) *L2 {
	return &L2{
		pinMeta:  map[string]pinMeta{},
		contents: map[string]string{},
		cursors:  map[string]string{},
		seenPins: map[string]bool{},
		manapi:   strings.TrimRight(manapiBase, "/"),
		client:   &http.Client{Timeout: 20 * time.Second},
	}
}

// Seed injects a fixture/demo stream and rebuilds the view (tests and cold demos).
func (l *L2) Seed(events []Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append([]Event(nil), events...)
	l.rebuildLocked()
}

// Append injects an incremental batch and rebuilds. This is the cross-path
// merge contract made explicit (loop blind-spot warning
// pin://281098079ee3b95382f986f09ed9eae47f8ccdd0bddfc1ee7f92c43611d749bei0):
// batches arrive in per-path pull order, but the rebuild re-sorts the ENTIRE
// pooled stream by (genesisHeight, txIndex) before replay, so pull order never
// leaks into the view — incremental views equal full-replay views.
func (l *L2) Append(events []Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, events...)
	l.rebuildLocked()
}

// View returns the current replay view (shared struct; treat as read-only).
func (l *L2) View() *View {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.view
}

func (l *L2) rebuildLocked() {
	opts := Options{BlocksPerHour: 6, BlocksPerDay: 144}
	l.view = Replay(l.events, opts)
	l.pinMeta = map[string]pinMeta{}
	l.contents = map[string]string{}
	for _, ev := range l.events {
		meta := pinMeta{height: ev.Height, txIndex: ev.TxIndex, sender: ev.Sender, path: ev.Path}
		if ev.Path == PathRev {
			if title, ok := ev.Payload["title"].(string); ok {
				meta.title = title
			}
			if c, ok := ev.Payload["content"].(string); ok && c != "" {
				l.contents[ev.Pin] = c
			}
		}
		l.pinMeta[ev.Pin] = meta
	}
}

// ---- manapi incremental sync (L0 primitive: pins_by_path with cursor) ----

var syncPaths = []string{
	PathConstitution, PathRev, PathChallenge, PathRuling, PathReview, PathEditor, PathParamProposal,
}

type manapiPage struct {
	Code int `json:"code"`
	Data struct {
		List []struct {
			ID            string `json:"id"`
			GenesisHeight int64  `json:"genesisHeight"`
			TxIndex       int    `json:"txIndex"`
			GlobalMetaID  string `json:"globalMetaId"`
			ContentBody   any    `json:"contentBody"`
		} `json:"list"`
		NextCursor string `json:"nextCursor"`
		Total      int    `json:"total"`
	} `json:"data"`
}

// SyncOnce pulls new pins for every agentpedia path via manapi paging, converts
// them to replay events, and rebuilds the view. Returns the per-path lastCursor
// map (the incremental-sync cursor contract, acceptance item 4).
func (l *L2) SyncOnce(ctx context.Context) (map[string]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	applied := 0
	for _, path := range syncPaths {
		cursor := l.cursors[path]
		for {
			page, err := l.fetchPage(ctx, path, cursor)
			if err != nil {
				return l.snapshotCursorsLocked(), err
			}
			for _, pin := range page.Data.List {
				if l.seenPins[pin.ID] {
					continue
				}
				l.seenPins[pin.ID] = true
				if pin.GenesisHeight < 0 {
					continue // mempool: visible but not ordered (F6)
				}
				payload := map[string]any{}
				if body, ok := pin.ContentBody.(string); ok && body != "" {
					if raw, err := base64.StdEncoding.DecodeString(body); err == nil {
						_ = json.Unmarshal(raw, &payload)
					}
				}
				l.events = append(l.events, Event{
					Pin: pin.ID, Path: path, Height: pin.GenesisHeight,
					TxIndex: pin.TxIndex, Sender: pin.GlobalMetaID, Payload: payload,
				})
				applied++
			}
			if page.Data.NextCursor == "" || len(page.Data.List) == 0 {
				l.cursors[path] = page.Data.NextCursor
				break
			}
			cursor = page.Data.NextCursor
			l.cursors[path] = cursor
		}
	}
	if applied > 0 {
		l.rebuildLocked()
	}
	return l.snapshotCursorsLocked(), nil
}

func (l *L2) snapshotCursorsLocked() map[string]string {
	out := make(map[string]string, len(l.cursors))
	for k, v := range l.cursors {
		out[k] = v
	}
	return out
}

func (l *L2) fetchPage(ctx context.Context, path, cursor string) (*manapiPage, error) {
	u, err := url.Parse(l.manapi + "/pin/path/list")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("path", path)
	q.Set("size", "100")
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := l.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	var page manapiPage
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("manapi %s: decode %q: %w", path, truncate(string(body), 120), err)
	}
	if page.Code != 1 {
		return nil, fmt.Errorf("manapi %s: code %d", path, page.Code)
	}
	return &page, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ---- read-side queries ----

type headInfo struct {
	Pin        string `json:"pin"`
	Title      string `json:"title"`
	Author     string `json:"author"`
	Content    string `json:"content,omitempty"`
	ContentRef string `json:"contentRef,omitempty"`
	Height     int64  `json:"height"`
}

type historyItem struct {
	Pin       string `json:"pin"`
	Author    string `json:"author"`
	Height    int64  `json:"height"`
	TxIndex   int    `json:"txIndex"`
	Type      string `json:"type"`
	ParentRev string `json:"parentRev,omitempty"` // AC-F1: history is a DAG — parent edges are load-bearing
	BasedOn   string `json:"basedOn,omitempty"`
	Summary   string `json:"summary,omitempty"`
}

type editorBreakdownItem struct {
	Editor    string `json:"editor"`
	Revisions int    `json:"revisions"`
}

type backlinkItem struct {
	FromEntry string `json:"fromEntry"`
	FromRev   string `json:"fromRev"`
}

func (l *L2) entryDetail(lang, slug string) (map[string]any, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entryKey := lang + ":" + slug
	en, ok := l.view.Entries[entryKey]
	if !ok {
		return nil, false
	}
	head := map[string]any{"pin": en.Head}
	if v, ok := en.Versions[en.Head]; ok {
		head["author"] = v.Author
		if meta, ok := l.pinMeta[en.Head]; ok {
			head["height"] = meta.height
		}
		head["content"] = l.contents[en.Head]
		if rev := payloadOf(l.events, en.Head); rev != nil {
			if ref, ok := rev["contentRef"].(string); ok {
				head["contentRef"] = ref
			}
			if title, ok := rev["title"].(string); ok {
				head["title"] = title
			}
		}
	}
	history := make([]historyItem, 0, len(en.History))
	breakdown := map[string]int{}
	for _, pin := range en.History {
		author := ""
		ver := en.Versions[pin]
		if v, ok := en.Versions[pin]; ok {
			author = v.Author
		}
		height := int64(0)
		txIndex := 0
		if meta, ok := l.pinMeta[pin]; ok {
			height = meta.height
			txIndex = meta.txIndex
		}
		history = append(history, historyItem{
			Pin: pin, Author: author, Height: height, TxIndex: txIndex,
			Type: revType(l.events, pin), ParentRev: ver.ParentRev, BasedOn: ver.BasedOn, Summary: ver.Summary,
		})
		if author != "" {
			breakdown[author]++
		}
	}
	breakdownList := make([]editorBreakdownItem, 0, len(breakdown))
	for editor, revisions := range breakdown {
		breakdownList = append(breakdownList, editorBreakdownItem{Editor: editor, Revisions: revisions})
	}
	sort.Slice(breakdownList, func(i, j int) bool { return breakdownList[i].Editor < breakdownList[j].Editor })
	// Tier B (AC-F2): lastReviewedRevId — the highest-chain-order rev carrying at
	// least one recorded review. Deterministically derived from the replay stream
	// (review pins sorted by chain position); no external queries (E3-5a).
	type reviewAt struct {
		rev    string
		height int64
		tx     int
	}
	var reviews []reviewAt
	for _, ev := range l.events {
		if ev.Path != PathReview {
			continue
		}
		rev := str(ev.Payload, "targetRev")
		if _, exists := en.Versions[rev]; exists {
			reviews = append(reviews, reviewAt{rev: rev, height: ev.Height, tx: ev.TxIndex})
		}
	}
	sort.Slice(reviews, func(i, j int) bool {
		if reviews[i].height != reviews[j].height {
			return reviews[i].height < reviews[j].height
		}
		return reviews[i].tx < reviews[j].tx
	})
	out := map[string]any{
		"entryKey":        entryKey,
		"head":            head,
		"status":          en.Status,
		"disputed":        en.Disputed,
		"featured":        false, // featured ladder is consumption-side; MVP view reports the flag
		"history":         history,
		"backlinks":       l.backlinksLocked(lang, slug),
		"editorBreakdown": breakdownList,
		"contests":        en.Contests,
		"redirect":        en.Redirect,
	}
	if len(reviews) > 0 {
		out["lastReviewedRevId"] = reviews[len(reviews)-1].rev // Tier B: omitted when no review exists
	}
	return out, true
}

func payloadOf(events []Event, pin string) map[string]any {
	for _, ev := range events {
		if ev.Pin == pin {
			return ev.Payload
		}
	}
	return nil
}

func revType(events []Event, pin string) string {
	if p := payloadOf(events, pin); p != nil {
		t, _ := p["type"].(string)
		return t
	}
	if eq := strings.Split(pin, "::"); len(eq) == 2 {
		return "revert-equivalent"
	}
	return ""
}

var wikilink = regexp.MustCompile(`\[\[([^\]]+)\]\]`)

func (l *L2) backlinksLocked(lang, slug string) []backlinkItem {
	target := lang + ":" + slug
	short := "[" + slug + "]"
	out := []backlinkItem{}
	seen := map[string]bool{}
	for _, ev := range l.events {
		if ev.Path != PathRev {
			continue
		}
		content, ok := l.contents[ev.Pin]
		if !ok {
			continue
		}
		lang2 := str(ev.Payload, "lang")
		slug2 := str(ev.Payload, "slug")
		fromEntry := lang2 + ":" + slug2
		if fromEntry == target {
			continue
		}
		for _, ref := range wikilink.FindAllStringSubmatch(content, -1) {
			refKey := ref[1]
			if refKey == target || (refKey == slug && lang2 == lang) || refKey == short {
				key := fromEntry + "|" + ev.Pin
				if !seen[key] {
					seen[key] = true
					out = append(out, backlinkItem{FromEntry: fromEntry, FromRev: ev.Pin})
				}
				break
			}
		}
	}
	return out
}

// ServeMux returns the L2 HTTP handlers (zero write paths).
func (l *L2) ServeMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/agentpedia/entry", l.handleEntry)
	mux.HandleFunc("/api/agentpedia/entry_list", l.handleEntryList)
	mux.HandleFunc("/api/agentpedia/backlinks", l.handleBacklinks)
	mux.HandleFunc("/api/agentpedia/editor", l.handleEditor)
	mux.HandleFunc("/api/agentpedia/sync", l.handleSync)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	return mux
}

// Handler wraps the mux with permissive CORS so the on-chain reader MetaApp
// (browser origin) can fetch the L2 endpoints directly — the reader is
// read-only, so "*" exposes nothing.
func (l *L2) Handler() http.Handler {
	mux := l.ServeMux()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// editorDetail assembles the editor profile view (second deliverable §4.1
// "GET editor"): registry state from the replay view, recent revs and
// endorsement list derived deterministically from the pooled stream.
func (l *L2) editorDetail(metaid string) (map[string]any, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	ed, ok := l.view.Editors[metaid]
	if !ok {
		return nil, false
	}
	type revItem struct {
		Pin    string `json:"pin"`
		Height int64  `json:"height"`
		Type   string `json:"type"`
	}
	recent := []revItem{}
	endorsedBy := []string{}
	for _, ev := range l.events {
		if ev.Sender == metaid && ev.Path == PathRev {
			evt, _ := ev.Payload["type"].(string)
			recent = append(recent, revItem{Pin: ev.Pin, Height: ev.Height, Type: evt})
		}
		if ev.Path == PathEditor {
			if action, _ := ev.Payload["action"].(string); action == "endorse" && str(ev.Payload, "editor") == metaid {
				endorsedBy = append(endorsedBy, ev.Sender)
			}
		}
	}
	sort.Slice(recent, func(i, j int) bool {
		if recent[i].Height != recent[j].Height {
			return recent[i].Height > recent[j].Height
		}
		return recent[i].Pin < recent[j].Pin
	})
	if len(recent) > 10 {
		recent = recent[:10]
	}
	out := map[string]any{
		"metaid":       metaid,
		"status":       ed.Status,
		"tier":         ed.Tier,
		"reputation":   ed.Reputation,
		"validRevs":    ed.ValidRevs,
		"registeredAt": ed.RegisteredAt,
		"revoked":      ed.Revoked,
		"banned":       ed.Banned,
		"recentRevs":   recent,
		"endorsedBy":   endorsedBy,
	}
	return out, true
}

func (l *L2) handleEditor(w http.ResponseWriter, r *http.Request) {
	metaid := r.URL.Query().Get("metaid")
	if metaid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "metaid is required"})
		return
	}
	detail, ok := l.editorDetail(metaid)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "editor not found", "metaid": metaid})
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (l *L2) handleEntry(w http.ResponseWriter, r *http.Request) {
	lang := r.URL.Query().Get("lang")
	slug := r.URL.Query().Get("slug")
	if lang == "" || slug == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "lang and slug are required"})
		return
	}
	detail, ok := l.entryDetail(lang, slug)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "entry not found", "entryKey": lang + ":" + slug})
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (l *L2) handleEntryList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	langFilter := q.Get("lang")
	sortBy := q.Get("sort")
	if sortBy == "" {
		sortBy = "updated"
	}
	l.mu.Lock()
	type item struct {
		Lang      string `json:"lang"`
		Slug      string `json:"slug"`
		EntryKey  string `json:"entryKey"`
		Head      string `json:"head"`
		Status    string `json:"status"`
		Title     string `json:"title,omitempty"`
		Featured  bool   `json:"featured"`
		UpdatedAt int64  `json:"updatedAt"`
	}
	items := []item{}
	for key, en := range l.view.Entries {
		parts := strings.SplitN(key, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if langFilter != "" && parts[0] != langFilter {
			continue
		}
		updatedAt := int64(0)
		title := ""
		if meta, ok := l.pinMeta[en.Head]; ok {
			updatedAt = meta.height
			title = meta.title
		}
		items = append(items, item{Lang: parts[0], Slug: parts[1], EntryKey: key, Head: en.Head, Status: en.Status, Title: title, Featured: false, UpdatedAt: updatedAt})
	}
	l.mu.Unlock()
	if sortBy == "updated" {
		sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt > items[j].UpdatedAt })
	} else {
		sort.Slice(items, func(i, j int) bool { return items[i].EntryKey < items[j].EntryKey })
	}
	// offset-based cursor (opaque "cursor" = next offset) — MVP pagination contract
	offset := 0
	if c := q.Get("cursor"); c != "" {
		offset, _ = strconv.Atoi(c)
	}
	limit := 50
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	end := offset + limit
	nextCursor := ""
	if end < len(items) {
		nextCursor = strconv.Itoa(end)
	} else {
		end = len(items)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total": len(items), "items": items[offset:end], "cursor": nextCursor, "sort": sortBy,
	})
}

func (l *L2) handleBacklinks(w http.ResponseWriter, r *http.Request) {
	lang := r.URL.Query().Get("lang")
	slug := r.URL.Query().Get("slug")
	if lang == "" || slug == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "lang and slug are required"})
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"entryKey":  lang + ":" + slug,
		"backlinks": l.backlinksLocked(lang, slug),
	})
}

func (l *L2) handleSync(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := l.SyncOnce(ctx); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"lastCursor": l.Cursors(), "applied": false, "error": err.Error(),
		})
		return
	}
	l.mu.Lock()
	total := len(l.events)
	l.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"lastCursor": l.Cursors(), "applied": true, "events": total,
	})
}

// Cursors returns the per-path lastCursor map (incremental-sync contract).
func (l *L2) Cursors() map[string]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.snapshotCursorsLocked()
}
