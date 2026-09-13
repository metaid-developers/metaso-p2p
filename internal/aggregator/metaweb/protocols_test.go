package metaweb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
)

func decodeJSON(recorder *httptest.ResponseRecorder, target any) error {
	return json.Unmarshal(recorder.Body.Bytes(), target)
}

func TestHandleProtocols_ValidatesAndLists(t *testing.T) {
	published := setupPublishedContent(t)

	good := hexPinId('a', 0)
	poisoned := hexPinId('b', 0)
	noPayload := hexPinId('c', 0)
	if _, err := published.HandleBlockPin(pinInscription(good, publishedcontent.PathMetaProtocol, "create", "", `{"title":"Game Protocol","path":"/protocols/gamescorerecording","protocolName":"GameScoreRecording","intro":"records scores","version":"1.0.1"}`)); err != nil {
		t.Fatalf("good: %v", err)
	}
	// The production-observed poisoned payload: upstream error text in path.
	if _, err := published.HandleBlockPin(pinInscription(poisoned, publishedcontent.PathMetaProtocol, "create", "", `{"title":"/protocols/when requiretype is mrc721, a value is required.","path":"/protocols/when requiretype is mrc721, a value is required."}`)); err != nil {
		t.Fatalf("poisoned: %v", err)
	}
	if _, err := published.HandleBlockPin(pinInscription(noPayload, publishedcontent.PathMetaProtocol, "create", "", "plain text, no json")); err != nil {
		t.Fatalf("no payload: %v", err)
	}

	agg := newTestAggregator(t)
	agg.SetFreshLookup(published)
	router := newTestRouter(agg)

	req := httptest.NewRequest(http.MethodGet, "/api/metaweb/protocols?size=10", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	var envelope struct {
		Code int `json:"code"`
		Data struct {
			Items []struct {
				PinId        string `json:"pinId"`
				Path         string `json:"path"`
				ProtocolName string `json:"protocolName"`
				Version      string `json:"version"`
			} `json:"items"`
			Rejected []struct {
				PinId  string `json:"pinId"`
				Reason string `json:"reason"`
			} `json:"rejected"`
			HasMore bool `json:"hasMore"`
		} `json:"data"`
	}
	if err := decodeJSON(recorder, &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Code != 0 {
		t.Fatalf("code = %d", envelope.Code)
	}
	if len(envelope.Data.Items) != 1 || envelope.Data.Items[0].PinId != good {
		t.Fatalf("items = %+v, want only %s", envelope.Data.Items, good)
	}
	if envelope.Data.Items[0].Path != "/protocols/gamescorerecording" || envelope.Data.Items[0].ProtocolName != "GameScoreRecording" {
		t.Errorf("item fields = %+v", envelope.Data.Items[0])
	}
	if len(envelope.Data.Rejected) != 2 {
		t.Fatalf("rejected = %+v, want 2 entries", envelope.Data.Rejected)
	}
	reasons := map[string]string{}
	for _, r := range envelope.Data.Rejected {
		reasons[r.PinId] = r.Reason
	}
	if !strings.HasPrefix(reasons[poisoned], "invalid path:") {
		t.Errorf("poisoned reason = %q", reasons[poisoned])
	}
	if reasons[noPayload] != "missing or non-JSON payload" {
		t.Errorf("no-payload reason = %q", reasons[noPayload])
	}
}

func TestValidateProtocolRecord(t *testing.T) {
	valid := func(path string) *publishedcontent.Record {
		return &publishedcontent.Record{PayloadExposed: true, PayloadJSON: map[string]any{"path": path}}
	}
	for _, path := range []string{"/protocols/game", "/protocols/game_score/recording2"} {
		if reason := validateProtocolRecord(valid(path)); reason != "" {
			t.Errorf("path %q rejected: %s", path, reason)
		}
	}
	for _, path := range []string{
		"/protocols/when requiretype is mrc721, a value is required.",
		"/protocols/UPPER",
		"/protocols/",
		"protocols/game",
		"/other/game",
		"/protocols/game/",
	} {
		if reason := validateProtocolRecord(valid(path)); reason == "" {
			t.Errorf("path %q should be rejected", path)
		}
	}
	if reason := validateProtocolRecord(&publishedcontent.Record{PayloadExposed: true, PayloadJSON: map[string]any{}}); reason != "missing path" {
		t.Errorf("missing path reason = %q", reason)
	}
}
