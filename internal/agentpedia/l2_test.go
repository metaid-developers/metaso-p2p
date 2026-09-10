package agentpedia

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func fixtureEvents() []Event {
	const hexA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const hexB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	const hexC = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	return []Event{
		{
			Pin: "con", Path: PathConstitution, Sender: "idq1d5m392ahkhp79wsy9ur79e3vhak7tg729dwdr5", Height: 1, TxIndex: 0,
			Payload: map[string]any{
				"v": 1.0, "revision": 0.0, "prevConstitution": nil, "proposalPin": nil,
				"founders":     []any{"idq1d5m392ahkhp79wsy9ur79e3vhak7tg729dwdr5", "idq14hmv23j5fnlx4ccnmvlyldjd38xjsechzwg9xz"},
				"params":       paramsToAny(defaultsForTest()),
				"algoVersions": map[string]any{"adoption": "adoption-algo-v1", "reputation": "reputation-algo-v1", "arbiterDraw": "arbiter-draw-v1"},
			},
		},
		{Pin: "t1", Path: PathRev, Sender: "idq1d5m392ahkhp79wsy9ur79e3vhak7tg729dwdr5", Height: 10, TxIndex: 0, Payload: map[string]any{
			"v": 1.0, "type": "create", "lang": "zh", "slug": "metaid", "title": "MetaID", "content": "MetaID 是 [[agentpedia]] 的身份层。", "contentHash": hexA,
		}},
		{Pin: "t2", Path: PathRev, Sender: "idq1d5m392ahkhp79wsy9ur79e3vhak7tg729dwdr5", Height: 11, TxIndex: 0, Payload: map[string]any{
			"v": 1.0, "type": "create", "lang": "zh", "slug": "agentpedia", "title": "Agentpedia", "content": "链上维基。", "contentHash": hexB,
		}},
		{Pin: "t3", Path: PathRev, Sender: "idq14hmv23j5fnlx4ccnmvlyldjd38xjsechzwg9xz", Height: 12, TxIndex: 0, Payload: map[string]any{
			"v": 1.0, "type": "edit", "lang": "zh", "slug": "metaid", "title": "MetaID", "content": "MetaID 是 [[agentpedia]] 与 [[metaso]] 的身份层。", "contentHash": hexC, "parentRev": "t1", "basedOn": "t1",
		}},
		{Pin: "rv", Path: PathReview, Sender: "idq14hmv23j5fnlx4ccnmvlyldjd38xjsechzwg9xz", Height: 13, TxIndex: 0, Payload: map[string]any{
			"v": 1.0, "targetRev": "t1", "score": 4.0, "dimensions": map[string]any{"accuracy": 4.0, "citation": 4.0, "neutrality": 4.0},
		}},
	}
}

// TestCrossPathMergeIncrementalEqualsFull locks the G8 merge contract
// (loop blind-spot warning pin://281098079ee3b95382f986f09ed9eae47f8ccdd0bdd
// fc1ee7f92c43611d749bei0): events pulled per-path in separate incremental
// batches must yield the same view as a one-shot full replay, even when pull
// order contradicts chain order (the challenge arrives in a later batch than
// the ruling that references it).
func TestCrossPathMergeIncrementalEqualsFull(t *testing.T) {
	founders := []string{"idq1d5m392ahkhp79wsy9ur79e3vhak7tg729dwdr5", "idq14hmv23j5fnlx4ccnmvlyldjd38xjsechzwg9xz"}
	const hexA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	con := Event{Pin: "con", Path: PathConstitution, Sender: founders[0], Height: 1, Payload: map[string]any{
		"v": 1.0, "revision": 0.0, "prevConstitution": nil, "proposalPin": nil,
		"founders":     founders,
		"params":       paramsToAny(defaultsForTest()),
		"algoVersions": map[string]any{"adoption": "adoption-algo-v1", "reputation": "reputation-algo-v1", "arbiterDraw": "arbiter-draw-v1"},
	}}
	t1 := Event{Pin: "t1", Path: PathRev, Sender: founders[0], Height: 10, Payload: map[string]any{
		"v": 1.0, "type": "create", "lang": "zh", "slug": "a", "title": "A", "content": "x", "contentHash": hexA,
	}}
	c1 := Event{Pin: "c1", Path: PathChallenge, Sender: founders[1], Height: 11, Payload: map[string]any{
		"v": 1.0, "targetRev": "t1", "reason": "factual", "detail": "interleaved merge fixture dispute",
	}}
	p1 := Event{Pin: "p1", Path: PathRuling, Sender: founders[0], Height: 12, Payload: map[string]any{
		"v": 1.0, "action": "proposal", "challengePin": "c1", "seed": "c1", "outcome": "dismiss", "baselineRev": nil, "params": nil,
	}}

	// one-shot full replay (pull order == chain order)
	oneShot := Replay([]Event{con, t1, c1, p1}, Options{})

	// incremental: batch1 pulls the rev path (t1 + p1) BEFORE batch2 pulls the
	// challenge path (c1) — pull order contradicts chain order for p1's dependency.
	l := NewL2("http://manapi.invalid")
	l.Append([]Event{con, t1, p1})
	mid := l.View()
	if len(mid.Graveyard) != 1 || mid.Graveyard[0].Pin != "p1" {
		t.Fatalf("mid state: expected p1 graveyarded (challenge not yet pulled), got %v", mid.Graveyard)
	}
	l.Append([]Event{c1})
	final := l.View()
	if final.Proposals["p1"].State != "open" {
		t.Fatalf("final p1 state = %v, want open", final.Proposals["p1"].State)
	}

	// incremental view must equal the one-shot full-replay view
	aj, _ := json.Marshal(final)
	bj, _ := json.Marshal(oneShot)
	if string(aj) != string(bj) {
		t.Fatalf("incremental view != full-replay view:\ninc:  %s\nfull: %s", aj, bj)
	}
}

func defaultsForTest() map[string]float64 {
	raw, err := osReadVectorsDefaults()
	if err != nil {
		panic(err)
	}
	return raw
}

func TestL2Endpoints(t *testing.T) {
	l := NewL2("http://manapi.invalid") // no network in tests; Seed supplies the stream
	l.Seed(fixtureEvents())
	mux := l.ServeMux()

	// entry detail: head/status/history/editorBreakdown present, zero write paths
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/agentpedia/entry?lang=zh&slug=metaid", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("entry status %d: %s", rec.Code, rec.Body.String())
	}
	var detail map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode entry: %v", err)
	}
	if detail["status"] != "normal" {
		t.Fatalf("status = %v", detail["status"])
	}
	head := detail["head"].(map[string]any)
	if head["pin"] != "t3" || head["author"] != "idq14hmv23j5fnlx4ccnmvlyldjd38xjsechzwg9xz" {
		t.Fatalf("head = %v", head)
	}
	if len(detail["history"].([]any)) != 2 {
		t.Fatalf("history = %v", detail["history"])
	}
	if detail["lastReviewedRevId"] != "t1" {
		t.Fatalf("lastReviewedRevId = %v (Tier B: last reviewed rev, chain-ordered)", detail["lastReviewedRevId"])
	}
	if len(detail["backlinks"].([]any)) != 0 {
		t.Fatalf("metaid should have no backlinks, got %v", detail["backlinks"])
	}

	// backlinks: both zh:metaid revisions (t1 and t3) reference [[agentpedia]]
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/agentpedia/backlinks?lang=zh&slug=agentpedia", nil))
	var bl map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &bl); err != nil {
		t.Fatalf("decode backlinks: %v", err)
	}
	links := bl["backlinks"].([]any)
	if len(links) != 2 {
		t.Fatalf("expected 2 backlinks (t1+t3), got %v", bl)
	}
	first := links[0].(map[string]any)
	if first["fromEntry"] != "zh:metaid" {
		t.Fatalf("backlink = %v", first)
	}
	fromRevs := map[string]bool{links[0].(map[string]any)["fromRev"].(string): true, links[1].(map[string]any)["fromRev"].(string): true}
	if !fromRevs["t1"] || !fromRevs["t3"] {
		t.Fatalf("backlink revs = %v", fromRevs)
	}

	// entry_list with lang filter + pagination cursor
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/agentpedia/entry_list?lang=zh&limit=1", nil))
	var list map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list["total"].(float64) != 2 {
		t.Fatalf("total = %v", list["total"])
	}
	if list["cursor"] == "" {
		t.Fatalf("expected a next cursor, got %v", list["cursor"])
	}

	// 404 on unknown entry
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/agentpedia/entry?lang=zh&slug=missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing entry status %d", rec.Code)
	}

	// sync endpoint: no-network error is reported honestly, lastCursor still returned
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/agentpedia/sync", nil))
	var sync map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &sync); err != nil {
		t.Fatalf("decode sync: %v", err)
	}
	if _, ok := sync["lastCursor"].(map[string]any); !ok {
		t.Fatalf("sync lastCursor missing: %v", sync)
	}
}

func TestEditorEndpointAndContractDeltas(t *testing.T) {
	l := NewL2("http://manapi.invalid")
	l.Seed(fixtureEvents())
	handler := l.Handler() // CORS-wrapped handler
	mux := l.ServeMux()

	// editor endpoint: registry view + derived recentRevs/endorsedBy
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/agentpedia/editor?metaid=idq1d5m392ahkhp79wsy9ur79e3vhak7tg729dwdr5", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("editor status %d: %s", rec.Code, rec.Body.String())
	}
	var ed map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &ed); err != nil {
		t.Fatalf("decode editor: %v", err)
	}
	if ed["status"] != "active" || ed["tier"] != "T2" {
		t.Fatalf("editor = %v", ed)
	}
	if len(ed["recentRevs"].([]any)) != 2 {
		t.Fatalf("recentRevs = %v", ed["recentRevs"])
	}

	// 404 for unknown editor
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/agentpedia/editor?metaid=idq1missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown editor status %d", rec.Code)
	}

	// contract deltas: history nodes carry parentRev/txIndex; entry_list carries title/featured
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/agentpedia/entry?lang=zh&slug=metaid", nil))
	var detail map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	history := detail["history"].([]any)
	last := history[len(history)-1].(map[string]any)
	if last["parentRev"] != "t1" || last["basedOn"] != "t1" {
		t.Fatalf("history node edges = %v", last)
	}
	if _, ok := last["txIndex"]; !ok {
		t.Fatalf("history node missing txIndex: %v", last)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/agentpedia/entry_list?lang=zh", nil))
	var list map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	for _, raw := range list["items"].([]any) {
		item := raw.(map[string]any)
		if _, ok := item["title"]; !ok {
			t.Fatalf("entry_list item missing title: %v", item)
		}
		if _, ok := item["featured"]; !ok {
			t.Fatalf("entry_list item missing featured: %v", item)
		}
	}

	// CORS: Handler() sets Access-Control-Allow-Origin on every endpoint
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("CORS header missing: %v", rec.Header())
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodOptions, "/api/agentpedia/entry", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("CORS preflight status %d", rec.Code)
	}
}

// TestSyncRecoversEmptyContentBodyFromPinRead locks the real-pull fallback
// (reader v0.2.2 report): the manapi list endpoint omits contentBody, so
// SyncOnce must recover each payload via the per-pin envelope read; empty
// payloads must never enter the replay stream.
func TestSyncRecoversEmptyContentBodyFromPinRead(t *testing.T) {
	founders := []string{"idq1d5m392ahkhp79wsy9ur79e3vhak7tg729dwdr5", "idq14hmv23j5fnlx4ccnmvlyldjd38xjsechzwg9xz"}
	const hexAB = "abababababababababababababababababababababababababababababababab"
	genesisPayload := map[string]any{
		"v": 1.0, "revision": 0.0, "prevConstitution": nil, "proposalPin": nil,
		"founders": founders, "params": paramsToAny(defaultsForTest()),
		"algoVersions": map[string]any{"adoption": "adoption-algo-v1", "reputation": "reputation-algo-v1", "arbiterDraw": "arbiter-draw-v1"},
	}
	b64 := mustB64(genesisPayload)

	mux := http.NewServeMux()
	mux.HandleFunc("/pin/path/list", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		list := []any{}
		if path == PathConstitution {
			list = append(list, map[string]any{
				"id": "gpin", "genesisHeight": 10, "txIndex": 0,
				"globalMetaId": founders[0], "contentBody": "", // the real-pull shape: body omitted
			})
		}
		if path == PathRev {
			revPayload := map[string]any{
				"v": 1.0, "type": "create", "lang": "zh", "slug": "metaid", "title": "MetaID",
				"content": "body", "contentHash": hexAB,
			}
			list = append(list, map[string]any{
				"id": "rpin", "genesisHeight": 11, "txIndex": 0,
				"globalMetaId": founders[0], "contentBody": mustB64(revPayload),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"code": 1, "data": map[string]any{"list": list, "nextCursor": "", "total": len(list)}})
	})
	mux.HandleFunc("/pin/gpin", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"code": 1, "data": map[string]any{"contentBody": b64}})
	})
	fake := httptest.NewServer(mux)
	defer fake.Close()

	l := NewL2(fake.URL)
	cursors, err := l.SyncOnce(context.Background())
	if err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if cursors[PathConstitution] != "" {
		t.Fatalf("cursor = %q", cursors[PathConstitution])
	}
	view := l.View()
	if len(view.Founders) != 2 {
		t.Fatalf("genesis not applied: founders = %v", view.Founders)
	}
	if view.Params["stakeAmountSat"] != 100000 {
		t.Fatalf("genesis params missing: %v", view.Params["stakeAmountSat"])
	}
	en, ok := view.Entries["zh:metaid"]
	if !ok || en.Head != "rpin" {
		t.Fatalf("entry not built from recovered payload: %+v", view.Entries)
	}
	if len(l.lastSyncSkipped) != 0 {
		t.Fatalf("unexpected skipped pins: %v", l.lastSyncSkipped)
	}
}

func mustB64(v any) string {
	raw, _ := json.Marshal(v)
	return base64.StdEncoding.EncodeToString(raw)
}

// TestSyncIncrementalFrontPull locks real-run defect #2: manapi pages
// newest-first, so each sweep must pull from the front and dedupe — a newly
// confirmed challenge on its OWN path is collected on the next sweep even
// though its list body is empty (envelope fallback) and any stored
// continuation cursor would point at older pages.
func TestSyncIncrementalFrontPull(t *testing.T) {
	founders := []string{"idq1d5m392ahkhp79wsy9ur79e3vhak7tg729dwdr5", "idq14hmv23j5fnlx4ccnmvlyldjd38xjsechzwg9xz"}
	const hexAB = "abababababababababababababababababababababababababababababababab"
	genesisPayload := map[string]any{
		"v": 1.0, "revision": 0.0, "prevConstitution": nil, "proposalPin": nil,
		"founders": founders, "params": paramsToAny(defaultsForTest()),
		"algoVersions": map[string]any{"adoption": "adoption-algo-v1", "reputation": "reputation-algo-v1", "arbiterDraw": "arbiter-draw-v1"},
	}
	revPayload := map[string]any{
		"v": 1.0, "type": "create", "lang": "zh", "slug": "metaid", "title": "MetaID",
		"content": "body", "contentHash": hexAB,
	}
	challengePayload := map[string]any{
		"v": 1.0, "targetRev": "rpin", "reason": "factual", "detail": "incremental front-pull fixture dispute",
	}
	revFront := []map[string]any{
		{"id": "rpin", "genesisHeight": 11, "txIndex": 0, "globalMetaId": founders[0], "contentBody": mustB64(revPayload)},
	}
	challengeFront := []map[string]any{}
	mux := http.NewServeMux()
	mux.HandleFunc("/pin/path/list", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		var list []any
		switch path {
		case PathConstitution:
			list = []any{map[string]any{"id": "gpin", "genesisHeight": 10, "txIndex": 0, "globalMetaId": founders[0], "contentBody": mustB64(genesisPayload)}}
		case PathRev:
			list = []any{}
			for _, pin := range revFront {
				list = append(list, pin)
			}
		case PathChallenge:
			list = []any{}
			for _, pin := range challengeFront {
				list = append(list, pin)
			}
		default:
			list = []any{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"code": 1, "data": map[string]any{"list": list, "nextCursor": "", "total": len(list)}})
	})
	mux.HandleFunc("/pin/cpin", func(w http.ResponseWriter, _ *http.Request) {
		// the challenge rides its own path list with an empty body — the
		// per-pin envelope read is the recovery channel under test
		writeJSON(w, http.StatusOK, map[string]any{"code": 1, "data": map[string]any{"contentBody": mustB64(challengePayload)}})
	})
	fake := httptest.NewServer(mux)
	defer fake.Close()

	l := NewL2(fake.URL)
	if _, err := l.SyncOnce(context.Background()); err != nil {
		t.Fatalf("sync #1: %v", err)
	}
	if got := len(l.View().Entries); got != 1 {
		t.Fatalf("after sync #1 entries = %d, want 1", got)
	}

	// a new challenge confirms on its own path with an empty list body
	challengeFront = append(challengeFront, map[string]any{
		"id": "cpin", "genesisHeight": 12, "txIndex": 0, "globalMetaId": founders[1], "contentBody": "",
	})
	if _, err := l.SyncOnce(context.Background()); err != nil {
		t.Fatalf("sync #2: %v", err)
	}

	view := l.View()
	en, ok := view.Entries["zh:metaid"]
	if !ok {
		t.Fatalf("entry missing after sync #2")
	}
	if len(en.Disputed) != 1 || en.Disputed[0] != "rpin" {
		t.Fatalf("challenge from incremental sweep not applied: disputed = %v", en.Disputed)
	}
}

// Regression (2026-09-11 production incident): read endpoints hit before the
// first sync must answer with well-formed responses — never panic, and never
// leave the mutex locked (the old entry_list unlocked without defer, so a
// nil-view panic deadlocked every later request for minutes).
func TestReadEndpointsBeforeFirstSyncAreSafe(t *testing.T) {
	l2 := NewL2("https://manapi.metaid.io")
	srv := httptest.NewServer(l2.Handler())
	defer srv.Close()
	client := srv.Client()

	getJSON := func(path string, out any) int {
		resp, err := client.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		if out != nil {
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				t.Fatalf("GET %s: decode: %v", path, err)
			}
		}
		return resp.StatusCode
	}

	// Pre-sync: an empty view, not a nil one. entry_list twice proves the
	// mutex survived call one (the old bug deadlocked call two).
	var list struct {
		Total  int              `json:"total"`
		Items  []map[string]any `json:"items"`
		Cursor string           `json:"cursor"`
	}
	for i := 0; i < 2; i++ {
		if code := getJSON("/api/agentpedia/entry_list?lang=zh", &list); code != http.StatusOK {
			t.Fatalf("entry_list call %d: got %d, want 200", i+1, code)
		}
		if list.Total != 0 || len(list.Items) != 0 || list.Cursor != "" {
			t.Fatalf("entry_list call %d: want empty view, got total=%d items=%d cursor=%q", i+1, list.Total, len(list.Items), list.Cursor)
		}
	}
	var entry map[string]any
	if code := getJSON("/api/agentpedia/entry?lang=zh&slug=nope", &entry); code != http.StatusNotFound {
		t.Fatalf("entry pre-sync: got %d, want 404", code)
	}
	if code := getJSON("/api/agentpedia/backlinks?lang=zh&slug=nope", &map[string]any{}); code != http.StatusOK {
		t.Fatalf("backlinks pre-sync: got %d, want 200", code)
	}
	if code := getJSON("/api/agentpedia/editor?metaid=idq1d5m392ahkhp79wsy9ur79e3vhak7tg729dwdr5", &map[string]any{}); code != http.StatusNotFound {
		t.Fatalf("editor pre-sync: got %d, want 404", code)
	}

	// After a rebuild the same paths serve real data (view invariant intact).
	l2.Seed(fixtureEvents())
	if code := getJSON("/api/agentpedia/entry_list?lang=zh", &list); code != http.StatusOK {
		t.Fatalf("entry_list post-seed: got %d, want 200", code)
	}
	if list.Total == 0 {
		t.Fatalf("entry_list post-seed: want non-empty, got total=0")
	}
}

// The entry_list contract exposes a graveyard summary so replay-rejected
// events (e.g. a whole batch rejected t0-no-rev during a new editor's
// cold-start window) are visible to clients instead of showing up only as a
// mysteriously small total (2026-09-11 production verification round).
func TestEntryListExposesGraveyardSummary(t *testing.T) {
	l2 := NewL2("https://manapi.metaid.io")
	evts := append(fixtureEvents(), Event{
		Pin: "ghost", Path: PathRev, Sender: "idq1ghost0000000000000000000000000000000000", Height: 50, TxIndex: 0,
		Payload: map[string]any{
			"v": 1.0, "type": "create", "lang": "zh", "slug": "ghost", "title": "Ghost",
			"content": "x", "contentHash": "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		},
	})
	l2.Seed(evts) // the ghost rev from an unregistered sender is replay-rejected

	srv := httptest.NewServer(l2.Handler())
	defer srv.Close()
	resp, err := srv.Client().Get(srv.URL + "/api/agentpedia/entry_list?lang=zh")
	if err != nil {
		t.Fatalf("GET entry_list: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("entry_list: got %d, want 200", resp.StatusCode)
	}
	var body struct {
		Total     int            `json:"total"`
		Graveyard map[string]any `json:"graveyard"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Total == 0 {
		t.Fatalf("want fixture entries in the list, got total=0")
	}
	gy, _ := body.Graveyard["total"].(float64)
	if gy != 1 {
		t.Fatalf("graveyard total = %v, want 1", body.Graveyard["total"])
	}
	reasons, _ := body.Graveyard["reasons"].(map[string]any)
	if reasons["unregistered"].(float64) != 1 {
		t.Fatalf("graveyard reasons = %v, want unregistered=1", reasons)
	}
}
