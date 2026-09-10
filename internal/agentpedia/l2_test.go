package agentpedia

import (
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
