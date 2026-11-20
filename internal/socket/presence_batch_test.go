package socket

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHandlePresenceBatch(t *testing.T) {
	srv, r := newTestRouter(t)

	now := time.Now()
	const onlineGID = "idq1onlinebot0000000000000000000000000000000"
	srv.manager.mu.Lock()
	srv.manager.connections[onlineGID] = []*TrackedConnection{{
		MetaId:      onlineGID,
		ConnType:    ConnTypePC,
		ConnectedAt: now.Add(-10 * time.Second),
		LastPing:    now.Add(-4 * time.Second),
	}}
	srv.manager.mu.Unlock()

	body, _ := json.Marshal(map[string]any{
		"globalMetaIds": []string{
			"idq1onlinebot0000000000000000000000000000000",
			"idq1offlinebot000000000000000000000000000000",
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/presence/batch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Map map[string]struct {
				Online             bool   `json:"online"`
				LastSeenAgoSeconds *int64 `json:"lastSeenAgoSeconds"`
			} `json:"map"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code=0, got %+v", resp)
	}
	on := resp.Data.Map["idq1onlinebot0000000000000000000000000000000"]
	off := resp.Data.Map["idq1offlinebot000000000000000000000000000000"]
	if !on.Online || on.LastSeenAgoSeconds == nil {
		t.Fatalf("expected online entry with lastSeenAgoSeconds, got %+v", on)
	}
	if off.Online {
		t.Fatalf("expected offline entry, got %+v", off)
	}

	// Reject > 100 entries
	tooMany := make([]string, 101)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("idq1bot%03d", i)
	}
	badBody, _ := json.Marshal(map[string]any{"globalMetaIds": tooMany})
	badRec := httptest.NewRecorder()
	badReq := httptest.NewRequest(http.MethodPost, "/api/presence/batch", bytes.NewReader(badBody))
	badReq.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(badRec, badReq)
	var badResp struct {
		Code int `json:"code"`
	}
	_ = json.Unmarshal(badRec.Body.Bytes(), &badResp)
	if badResp.Code != 40000 {
		t.Fatalf("expected code=40000 for >100 items, got %d", badResp.Code)
	}
}
