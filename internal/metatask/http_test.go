package metatask

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildServeFixture writes a synthetic frozen-crawl directory (the pool-line
// snapshot format: pages/<pool>/<n>.json with the manapi envelope) with one
// small verified task, then returns the SnapshotLister over it.
func buildServeFixture(t *testing.T) *SnapshotLister {
	t.Helper()
	dir := t.TempDir()
	pin := func(id, path string, h int64, tx int64, ts int64, author string, payload string) map[string]any {
		return map[string]any{
			"id": id, "path": path, "globalMetaId": author,
			"genesisHeight": h, "txIndex": tx, "timestamp": ts,
			"contentSummary": payload,
		}
	}
	page := func(pins []map[string]any, cursor string) map[string]any {
		var list any
		if pins == nil {
			list = nil
		} else {
			list = pins
		}
		total := 0
		if pins != nil {
			total = len(pins)
		}
		return map[string]any{"code": 0, "message": "ok", "data": map[string]any{
			"list": list, "nextCursor": cursor, "total": total,
		}}
	}
	P := "/protocols/metatask/"
	root, b1, b2, b3 := "idq1root", "idq1bot1", "idq1bot2", "idq1bot3"
	const t0 = int64(1789400000)
	pools := map[string][]map[string]any{
		"task": {pin("task1", P+"task", 100, 0, t0, root,
			`{"title":"tv","treeid":"tree1","specid":"","policy":{"claim_ttl_hours":48,"verify_quorum":2,"verify_window_hours":24,"reward_sat":0}}`)},
		"tree": {pin("tree1", P+"tree", 100, 1, t0, root,
			`{"root":"n1","nodes":[{"id":"n1","parent":null,"title":"r","kind":"aggregate","specid":null,"params":{"k":1},"deps":[]}]}`)},
		"spec":    []map[string]any{},
		"release": nil,
		"amend":   nil,
		"claim":   {pin("claim1", P+"claim", 101, 0, t0+60, b1, `{"taskid":"task1","node":"n1"}`)},
		"submission": {pin("sub1", P+"submission", 102, 0, t0+120, b1,
			`{"taskid":"task1","node":"n1","claimid":"claim1","result":{"x":1},"hash":"h","contentType":"application/json"}`)},
		"verify": {
			pin("v1", P+"verify", 190001, 0, t0+180, b2, `{"targetid":"sub1","verdict":"pass","method":"r","semantic_check":"ok"}`),
			pin("v2", P+"verify", 190002, 0, t0+240, b3, `{"targetid":"sub1","verdict":"pass","method":"r","semantic_check":"ok"}`),
		},
		"challenge": nil,
	}
	for pool, pins := range pools {
		d := filepath.Join(dir, "pages", pool)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		writePage := func(name string, body map[string]any) {
			b, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(d, name), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if pins == nil { // empty pool: single null page, no cursor
			writePage("1.json", page(nil, ""))
			continue
		}
		if len(pins) == 0 { // empty list page terminates too
			writePage("1.json", page([]map[string]any{}, ""))
			continue
		}
		writePage("1.json", page(pins, "p2"))
		writePage("2.json", page([]map[string]any{}, ""))
	}
	return NewSnapshotLister(dir)
}

func TestServeEndpoints(t *testing.T) {
	lister := buildServeFixture(t)
	srv := &Server{Lister: lister, Payloads: lister, Size: 50, Version: "test-build"}
	if err := srv.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	srv.ChainHead = func() int64 { return 190009 }
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	get := func(path string) (int, http.Header, string) {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		buf := new(strings.Builder)
		_, _ = fmt.Fprint(buf, "")
		body := make([]byte, 0, 1<<16)
		tmp := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(tmp)
			body = append(body, tmp[:n]...)
			if err != nil {
				break
			}
		}
		return resp.StatusCode, resp.Header, string(body)
	}

	// tasks index: headers + body shape.
	code, hdr, body := get("/api/metatask/tasks")
	if code != http.StatusOK {
		t.Fatalf("tasks: %d %s", code, body)
	}
	if hdr.Get("X-Metatask-Evaluated-Block") != "190002" || hdr.Get("X-Metatask-Chain-Head") != "190009" || hdr.Get("X-Metatask-Indexer-Build") != "test-build" {
		t.Fatalf("headers: %+v", hdr)
	}
	var idx struct {
		Stats struct {
			TaskTotal int `json:"taskTotal"`
		} `json:"stats"`
		Rows []struct {
			RootPinID string `json:"rootPinId"`
			TaskState string `json:"taskState"`
			LastWrite string `json:"lastWriteEventPinId"`
			NodeID    string `json:"nodeId"`
		} `json:"rows"`
		ReplayMeta json.RawMessage `json:"replayMeta"`
	}
	if err := json.Unmarshal([]byte(body), &idx); err != nil {
		t.Fatalf("decode index: %v", err)
	}
	if idx.Stats.TaskTotal != 1 || len(idx.Rows) != 1 || idx.Rows[0].TaskState != "done" {
		t.Fatalf("index body: %s", body)
	}
	if strings.Contains(body, "canonId") {
		t.Fatal("canonId must not appear in any projection body (v1.2 lock)")
	}

	// panorama: lowercase treeid/specid keys, node id key, null-free contract.
	code, _, body = get("/api/metatask/tasks/task1")
	if code != http.StatusOK {
		t.Fatalf("panorama: %d %s", code, body)
	}
	var pano struct {
		TreeID string `json:"treeid"`
		Nodes  []struct {
			ID           string         `json:"id"`
			Parent       *string        `json:"parent"`
			Params       map[string]any `json:"params"`
			MachineState string         `json:"machineState"`
		} `json:"nodes"`
		ReplayMeta json.RawMessage `json:"replayMeta"`
	}
	if err := json.Unmarshal([]byte(body), &pano); err != nil {
		t.Fatalf("decode panorama: %v", err)
	}
	if pano.TreeID != "tree1" || len(pano.Nodes) != 1 || pano.Nodes[0].ID != "n1" {
		t.Fatalf("panorama shape: %s", body)
	}
	if pano.Nodes[0].Parent != nil || pano.Nodes[0].Params["k"] != float64(1) {
		t.Fatalf("node parent/params: %s", body)
	}

	// node events.
	code, _, body = get("/api/metatask/tasks/task1/nodes/n1")
	if code != http.StatusOK || !strings.Contains(body, `"eventPinId":"v2"`) {
		t.Fatalf("node events: %d %s", code, body)
	}

	// replay dump: echoes evaluatedAtBlock at top level.
	code, _, body = get("/api/metatask/replay/task1")
	if code != http.StatusOK || !strings.Contains(body, `"evaluatedAtBlock":190002`) {
		t.Fatalf("replay: %d %s", code, body)
	}

	// unknown root → 404 with coverage note.
	code, _, body = get("/api/metatask/tasks/unknownroot")
	if code != http.StatusNotFound || !strings.Contains(body, "coverage") {
		t.Fatalf("unknown root: %d %s", code, body)
	}

	// to_block rewind: at block 101 the node is only claimed.
	code, hdr, body = get("/api/metatask/tasks?to_block=101")
	if code != http.StatusOK || hdr.Get("X-Metatask-Evaluated-Block") != "101" {
		t.Fatalf("to_block: %d %s", code, body)
	}
	if !strings.Contains(body, `"claimed"`) || strings.Contains(body, `"verified":1`) {
		t.Fatalf("to_block replay boundary not applied: %s", body)
	}

	// invalid to_block → 400.
	if code, _, _ = get("/api/metatask/tasks?to_block=abc"); code != http.StatusBadRequest {
		t.Fatalf("invalid to_block: %d", code)
	}
}
