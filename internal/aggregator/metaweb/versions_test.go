package metaweb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
)

// historyFetcher answers FetchPin per pin id, falling back to a default
// result; it records requested ids for assertions.
type historyFetcher struct {
	pins      map[string]*RemotePin
	def       *RemotePin
	defErr    error
	wantErr   error
	requested []string
}

func (f *historyFetcher) FetchPin(pinId string) (*RemotePin, error) {
	f.requested = append(f.requested, pinId)
	if f.wantErr != nil && strings.Contains(f.wantErr.Error(), pinId) {
		return nil, f.wantErr
	}
	if pin, ok := f.pins[pinId]; ok {
		return pin, nil
	}
	if f.def != nil {
		return f.def, nil
	}
	return nil, f.defErr
}

func TestHandlePinVersions_LocalMultiVersionChain(t *testing.T) {
	published := setupPublishedContent(t)
	source := hexPinId('a', 0)
	version2 := hexPinId('b', 1)

	if _, err := published.HandleBlockPin(pinInscription(source, publishedcontent.PathSimpleNote, "create", "", `{"title":"Doc","content":"v1"}`)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := published.HandleBlockPin(pinInscription(version2, publishedcontent.PathSimpleNote+"@"+source, "modify", source, `{"title":"Doc","content":"v2"}`)); err != nil {
		t.Fatalf("modify: %v", err)
	}

	// The chain projection lists every version on the current pin's record.
	fetcher := &historyFetcher{
		pins: map[string]*RemotePin{
			version2: {PinId: version2, ModifyHistory: []string{source, version2}},
		},
		defErr: ErrRemotePinNotFound,
	}
	agg := newTestAggregator(t)
	agg.SetPinLookups(published, nil)
	agg.SetRemotePinFetcher(fetcher)
	router := newTestRouter(agg)

	req := httptest.NewRequest(http.MethodGet, "/api/metaweb/pin/"+version2+"/versions", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d", recorder.Code)
	}
	var envelope struct {
		Code int    `json:"code"`
		Msg  string `json:"message"`
		Data struct {
			PinId    string `json:"pinId"`
			Latest   string `json:"latest"`
			Versions []struct {
				PinId     string `json:"pinId"`
				Version   int    `json:"version"`
				CreatedAt int64  `json:"createdAt"`
				Operation string `json:"operation"`
				Author    struct {
					MetaId  string `json:"metaid"`
					Address string `json:"address"`
				} `json:"author"`
			} `json:"versions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Code != 0 {
		t.Fatalf("code = %d (%s)", envelope.Code, envelope.Msg)
	}
	if envelope.Data.Latest != version2 {
		t.Errorf("latest = %s, want %s", envelope.Data.Latest, version2)
	}
	if len(envelope.Data.Versions) != 2 {
		t.Fatalf("versions = %d, want 2", len(envelope.Data.Versions))
	}
	first, second := envelope.Data.Versions[0], envelope.Data.Versions[1]
	if first.PinId != source || first.Version != 1 || first.Operation != "create" {
		t.Errorf("v1 = %+v, want %s / 1 / create", first, source)
	}
	if second.PinId != version2 || second.Version != 2 || second.Operation != "modify" {
		t.Errorf("v2 = %+v, want %s / 2 / modify", second, version2)
	}
	if first.CreatedAt != second.CreatedAt && second.CreatedAt < first.CreatedAt {
		t.Errorf("chain not oldest→newest: %d then %d", first.CreatedAt, second.CreatedAt)
	}
	if second.Author.MetaId != "meta-user" || second.Author.Address != "addr-user" {
		t.Errorf("v2 author = %+v, want metadata-user/addr-user", second.Author)
	}
	// No metadata fetches: source and current both come from the local record.
	if len(fetcher.requested) != 1 {
		t.Errorf("remote fetches = %v, want only the current pin", fetcher.requested)
	}
}

func TestHandlePinVersions_SingleVersionLocalNoRemote(t *testing.T) {
	published := setupPublishedContent(t)
	source := hexPinId('c', 0)
	if _, err := published.HandleBlockPin(pinInscription(source, publishedcontent.PathSimpleBuzz, "create", "", `{"content":"one version"}`)); err != nil {
		t.Fatalf("create: %v", err)
	}

	fetcher := &historyFetcher{defErr: fmt.Errorf("must not be called")}
	agg := newTestAggregator(t)
	agg.SetPinLookups(published, nil)
	agg.SetRemotePinFetcher(fetcher)
	router := newTestRouter(agg)

	req := httptest.NewRequest(http.MethodGet, "/api/metaweb/pin/"+source+"/versions", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	var envelope struct {
		Code int `json:"code"`
		Data struct {
			Latest   string `json:"latest"`
			Versions []struct {
				PinId     string `json:"pinId"`
				Operation string `json:"operation"`
			} `json:"versions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Code != 0 || len(envelope.Data.Versions) != 1 || envelope.Data.Versions[0].PinId != source {
		t.Fatalf("single-version chain = %+v, want [%s]", envelope.Data, source)
	}
	if len(fetcher.requested) != 0 {
		t.Errorf("remote fetches = %v, want none (single-version local fast path)", fetcher.requested)
	}
}

func TestHandlePinVersions_RemoteOnlyPin(t *testing.T) {
	requested := hexPinId('d', 0)
	middle := hexPinId('e', 1)
	current := hexPinId('f', 2)

	fetcher := &historyFetcher{
		pins: map[string]*RemotePin{
			requested: {PinId: requested, Operation: "create", Timestamp: 1755000000, ModifyHistory: []string{requested, middle, current}},
			middle:    {PinId: middle, Operation: "modify", Timestamp: 1755000100},
			// `current` is intentionally absent: metadata fetch for it fails.
		},
		defErr: ErrRemotePinNotFound,
	}
	agg := newTestAggregator(t)
	agg.SetRemotePinFetcher(fetcher)
	router := newTestRouter(agg)

	req := httptest.NewRequest(http.MethodGet, "/api/metaweb/pin/"+requested+"/versions", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	var envelope struct {
		Code int    `json:"code"`
		Msg  string `json:"message"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// A chain member whose metadata cannot be fetched fails the request
	// (evidence-grade ordering must not serve silently missing metadata).
	if envelope.Code != codeUnavailable {
		t.Fatalf("code = %d (%s), want 50000 when chain metadata is unattributable", envelope.Code, envelope.Msg)
	}
}

func TestHandlePinVersions_ErrorPaths(t *testing.T) {
	agg := newTestAggregator(t)
	agg.SetRemotePinFetcher(&historyFetcher{defErr: ErrRemotePinNotFound})
	router := newTestRouter(agg)

	do := func(path string) (int, int) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		var envelope struct {
			Code int `json:"code"`
		}
		_ = json.Unmarshal(recorder.Body.Bytes(), &envelope)
		return recorder.Code, envelope.Code
	}
	if _, code := do("/api/metaweb/pin/nope/versions"); code != codeInvalidParam {
		t.Errorf("malformed pinId: code = %d, want 40000", code)
	}
	if _, code := do("/api/metaweb/pin/" + hexPinId('a', 0) + "/versions"); code != codeNotFound {
		t.Errorf("unknown pin: code = %d, want 40400", code)
	}
}

func TestHandlePinBatch(t *testing.T) {
	published := setupPublishedContent(t)
	good := hexPinId('a', 0)
	version2 := hexPinId('b', 1)
	if _, err := published.HandleBlockPin(pinInscription(good, publishedcontent.PathSimpleNote, "create", "", `{"title":"Doc","content":"body"}`)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := published.HandleBlockPin(pinInscription(version2, publishedcontent.PathSimpleNote+"@"+good, "modify", good, `{"title":"Doc","content":"v2"}`)); err != nil {
		t.Fatalf("modify: %v", err)
	}

	fetcher := &historyFetcher{
		pins: map[string]*RemotePin{
			version2: {PinId: version2, ModifyHistory: []string{good, version2}},
		},
		defErr: ErrRemotePinNotFound,
	}
	agg := newTestAggregator(t)
	agg.SetPinLookups(published, nil)
	agg.SetRemotePinFetcher(fetcher)
	router := newTestRouter(agg)

	body := fmt.Sprintf(`{"pinIds":[%q,%q,%q,%q]}`, good, version2, hexPinId('d', 9), "not-a-pin")
	req := httptest.NewRequest(http.MethodPost, "/api/metaweb/pins:batch", bytes.NewBufferString(body))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d", recorder.Code)
	}
	var envelope struct {
		Code int    `json:"code"`
		Msg  string `json:"message"`
		Data struct {
			Pins map[string]struct {
				Error        string  `json:"error"`
				PinId        string  `json:"pinId"`
				CurrentPinId string  `json:"currentPinId"`
				Text         *string `json:"text"`
				Truncated    *bool   `json:"truncated"`
				TotalLength  *int    `json:"totalLength"`
				Version      struct {
					Latest string `json:"latest"`
					Count  *int   `json:"count"`
				} `json:"version"`
			} `json:"pins"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Code != 0 {
		t.Fatalf("code = %d (%s)", envelope.Code, envelope.Msg)
	}
	if len(envelope.Data.Pins) != 4 {
		t.Fatalf("pins = %d entries, want 4", len(envelope.Data.Pins))
	}

	goodEntry := envelope.Data.Pins[good]
	if goodEntry.Error != "" || goodEntry.PinId != good {
		t.Errorf("good entry = %+v, want full pin object", goodEntry)
	}
	if goodEntry.Text == nil || *goodEntry.Text == "" {
		t.Errorf("good entry text missing")
	}
	if goodEntry.Truncated == nil || *goodEntry.Truncated {
		t.Errorf("good entry truncated = %+v, want false (explicit)", goodEntry.Truncated)
	}
	if goodEntry.TotalLength == nil || *goodEntry.TotalLength == 0 {
		t.Errorf("good entry totalLength = %+v, want > 0", goodEntry.TotalLength)
	}
	// The record for `good` was modified (version2 targets it), so its pin
	// read carries the current chain: count 2, latest version2.
	if goodEntry.Version.Count == nil || *goodEntry.Version.Count != 2 {
		t.Errorf("good entry version.count = %+v, want 2", goodEntry.Version.Count)
	}
	if goodEntry.Version.Latest != version2 {
		t.Errorf("good entry version.latest = %s, want %s", goodEntry.Version.Latest, version2)
	}

	modifiedEntry := envelope.Data.Pins[version2]
	if modifiedEntry.Error != "" {
		t.Fatalf("modified entry error: %s", modifiedEntry.Error)
	}
	if modifiedEntry.Version.Count == nil || *modifiedEntry.Version.Count != 2 {
		t.Errorf("modified entry version.count = %+v, want 2 (chain-attributed)", modifiedEntry.Version.Count)
	}
	if modifiedEntry.Version.Latest != version2 {
		t.Errorf("modified entry version.latest = %s, want %s", modifiedEntry.Version.Latest, version2)
	}

	if envelope.Data.Pins[hexPinId('d', 9)].Error != "pin not found" {
		t.Errorf("unknown pin entry = %+v, want error pin not found", envelope.Data.Pins[hexPinId('d', 9)])
	}
	if envelope.Data.Pins["not-a-pin"].Error != "malformed pinId" {
		t.Errorf("malformed entry = %+v, want error malformed pinId", envelope.Data.Pins["not-a-pin"])
	}
}

func TestHandlePinBatch_BadRequests(t *testing.T) {
	agg := newTestAggregator(t)
	router := newTestRouter(agg)

	do := func(body string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/metaweb/pins:batch", bytes.NewBufferString(body))
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		var envelope struct {
			Code int `json:"code"`
		}
		_ = json.Unmarshal(recorder.Body.Bytes(), &envelope)
		return envelope.Code
	}
	if code := do(`not json`); code != codeInvalidParam {
		t.Errorf("bad body: code = %d, want 40000", code)
	}
	if code := do(`{}`); code != codeInvalidParam {
		t.Errorf("empty pinIds: code = %d, want 40000", code)
	}
	if code := do(`{"pinIds":["  "]}`); code != codeInvalidParam {
		t.Errorf("blank pinIds: code = %d, want 40000", code)
	}
	ids := make([]string, 51)
	for i := range ids {
		ids[i] = hexPinId(byte('a'+i%26), i)
	}
	raw, _ := json.Marshal(map[string][]string{"pinIds": ids})
	if code := do(string(raw)); code != codeInvalidParam {
		t.Errorf("51 ids: code = %d, want 40000", code)
	}
}
