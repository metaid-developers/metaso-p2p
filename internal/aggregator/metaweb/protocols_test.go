package metaweb

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
)

// mapFetcher answers FetchPin from a static map (registry detail tests).
type mapFetcher struct {
	pins map[string]*RemotePin
}

func (f *mapFetcher) FetchPin(pinId string) (*RemotePin, error) {
	if pin, ok := f.pins[pinId]; ok {
		return pin, nil
	}
	return nil, ErrRemotePinNotFound
}

// protocolPin builds a metaprotocol pin with explicit timestamp/identity so
// the fold order and owner checks are deterministic.
func protocolPin(pinId, path, operation, originalId string, ts int64, globalMetaId, metaId, address, jsonBody string) *aggregator.PinInscription {
	return &aggregator.PinInscription{
		Id:           pinId,
		Path:         path,
		Operation:    operation,
		OriginalId:   originalId,
		ContentType:  "application/json",
		ContentBody:  []byte(jsonBody),
		ChainName:    "mvc",
		Timestamp:    ts,
		GlobalMetaId: globalMetaId,
		MetaId:       metaId,
		Address:      address,
	}
}

type protocolsEnvelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Items []struct {
			ProtocolPath   string `json:"protocolPath"`
			Title          string `json:"title"`
			ProtocolName   string `json:"protocolName"`
			Intro          string `json:"intro"`
			Version        string `json:"version"`
			ChainName      string `json:"chainName"`
			PinId          string `json:"pinId"`
			CurrentPinId   string `json:"currentPinId"`
			CreatedAt      int64  `json:"createdAt"`
			UpdatedAt      int64  `json:"updatedAt"`
			Confirmed      bool   `json:"confirmed"`
			Author         struct {
				Address      string `json:"address"`
				MetaId       string `json:"metaid"`
				GlobalMetaId string `json:"globalMetaId"`
				Name         string `json:"name"`
			} `json:"author"`
			ConflictsCount int `json:"conflictsCount"`
			Conflicts      []struct {
				PinId string `json:"pinId"`
			} `json:"conflicts"`
		} `json:"items"`
		Rejected []struct {
			PinId  string `json:"pinId"`
			Reason string `json:"reason"`
		} `json:"rejected"`
		HasMore    bool    `json:"hasMore"`
		NextCursor *string `json:"nextCursor"`
	} `json:"data"`
}

func doProtocols(t *testing.T, router *gin.Engine, query string) protocolsEnvelope {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/metaweb/protocols?"+query, nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200", recorder.Code)
	}
	var envelope protocolsEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return envelope
}

func registryRouter(t *testing.T, published *publishedcontent.Aggregator) *gin.Engine {
	t.Helper()
	agg := newTestAggregator(t)
	agg.SetProtocolRegistry(published)
	agg.SetPinLookups(published, nil)
	return newTestRouter(agg)
}

func TestHandleProtocols_V2FoldAndRejected(t *testing.T) {
	published := setupPublishedContent(t)

	earlier := hexPinId('a', 0)
	later := hexPinId('b', 0)
	poisoned := hexPinId('c', 0)
	noPayload := hexPinId('d', 0)
	for _, pin := range []*aggregator.PinInscription{
		// Later registration arrives first; the earlier one must still win
		// the authority. Payload path case differs — the fold normalizes.
		protocolPin(later, publishedcontent.PathMetaProtocol, "create", "", 1755000200,
			"gid-late", "meta-late", "addr-late",
			`{"title":"Game Protocol","path":"/protocols/gamescorerecording","protocolName":"gamescorerecording","intro":"records scores","version":"1.0.1"}`),
		protocolPin(earlier, publishedcontent.PathMetaProtocol, "create", "", 1755000100,
			"gid-early", "meta-early", "addr-early",
			`{"title":"Game Protocol","path":"/protocols/GameScoreRecording","protocolName":"gamescorerecording","version":"1.0.0"}`),
		protocolPin(poisoned, publishedcontent.PathMetaProtocol, "create", "", 1755000300,
			"gid-p", "meta-p", "addr-p",
			`{"title":"x","path":"/protocols/when requiretype is mrc721, a value is required."}`),
		protocolPin(noPayload, publishedcontent.PathMetaProtocol, "create", "", 1755000400,
			"gid-n", "meta-n", "addr-n", `plain text, no json`),
	} {
		if _, err := published.HandleBlockPin(pin); err != nil {
			t.Fatalf("HandleBlockPin(%s): %v", pin.Id, err)
		}
	}

	router := registryRouter(t, published)
	envelope := doProtocols(t, router, "size=10")
	if envelope.Code != 0 {
		t.Fatalf("code = %d message = %q", envelope.Code, envelope.Message)
	}
	if len(envelope.Data.Items) != 1 {
		t.Fatalf("items = %+v, want exactly 1 folded entry", envelope.Data.Items)
	}
	item := envelope.Data.Items[0]
	if item.PinId != earlier {
		t.Fatalf("authoritative = %s, want earliest %s", item.PinId, earlier)
	}
	if item.ProtocolPath != "/protocols/gamescorerecording" {
		t.Fatalf("protocolPath = %q, want normalized lowercase", item.ProtocolPath)
	}
	if item.ConflictsCount != 1 || item.Version != "1.0.0" || !item.Confirmed ||
		item.Author.MetaId != "meta-early" || item.CreatedAt != 1755000100 {
		t.Fatalf("item = %+v", item)
	}
	if len(item.Conflicts) != 0 {
		t.Fatalf("conflicts must be omitted without includeConflicts: %+v", item.Conflicts)
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

	// includeConflicts=true attaches the conflict detail.
	envelope = doProtocols(t, router, "size=10&includeConflicts=true")
	if len(envelope.Data.Items) != 1 || len(envelope.Data.Items[0].Conflicts) != 1 ||
		envelope.Data.Items[0].Conflicts[0].PinId != later {
		t.Fatalf("includeConflicts items = %+v", envelope.Data.Items)
	}
}

func TestHandleProtocols_Filters(t *testing.T) {
	published := setupPublishedContent(t)
	game := hexPinId('a', 0)
	note := hexPinId('b', 0)
	for _, pin := range []*aggregator.PinInscription{
		protocolPin(game, publishedcontent.PathMetaProtocol, "create", "", 1755000100,
			"gid-game", "meta-game", "addr-game",
			`{"title":"Game Score Protocol","path":"/protocols/game","protocolName":"game","version":"1.0.0"}`),
		protocolPin(note, publishedcontent.PathMetaProtocol, "create", "", 1755000200,
			"gid-note", "meta-note", "addr-note",
			`{"title":"Note Taking Protocol","path":"/protocols/note","protocolName":"note","version":"0.1.0"}`),
	} {
		if _, err := published.HandleBlockPin(pin); err != nil {
			t.Fatalf("HandleBlockPin(%s): %v", pin.Id, err)
		}
	}
	router := registryRouter(t, published)

	envelope := doProtocols(t, router, "q=score")
	if len(envelope.Data.Items) != 1 || envelope.Data.Items[0].PinId != game {
		t.Fatalf("q filter items = %+v", envelope.Data.Items)
	}
	envelope = doProtocols(t, router, "q=note")
	if len(envelope.Data.Items) != 1 || envelope.Data.Items[0].PinId != note {
		t.Fatalf("q protocolName/path filter items = %+v", envelope.Data.Items)
	}
	envelope = doProtocols(t, router, "publisher=META-GAME")
	if len(envelope.Data.Items) != 1 || envelope.Data.Items[0].PinId != game {
		t.Fatalf("publisher metaid filter items = %+v", envelope.Data.Items)
	}
	envelope = doProtocols(t, router, "publisher=addr-note")
	if len(envelope.Data.Items) != 1 || envelope.Data.Items[0].PinId != note {
		t.Fatalf("publisher address filter items = %+v", envelope.Data.Items)
	}
	envelope = doProtocols(t, router, "path=/protocols/GAME")
	if len(envelope.Data.Items) != 1 || envelope.Data.Items[0].PinId != game {
		t.Fatalf("path filter items = %+v", envelope.Data.Items)
	}
	envelope = doProtocols(t, router, "path=/protocols/unknown")
	if len(envelope.Data.Items) != 0 {
		t.Fatalf("unknown path filter items = %+v", envelope.Data.Items)
	}
	envelope = doProtocols(t, router, "path=not-a-protocol-path")
	if envelope.Code != codeInvalidParam {
		t.Fatalf("malformed path filter: code = %d, want 40000", envelope.Code)
	}
}

func TestHandleProtocols_Pagination(t *testing.T) {
	published := setupPublishedContent(t)
	for i := 0; i < 3; i++ {
		pin := protocolPin(hexPinId(byte('a'+i), 0), publishedcontent.PathMetaProtocol, "create", "",
			1755000100+int64(i), "gid", "meta", "addr",
			fmt.Sprintf(`{"title":"Protocol %d","path":"/protocols/p%d","protocolName":"p%d"}`, i, i, i))
		if _, err := published.HandleBlockPin(pin); err != nil {
			t.Fatalf("HandleBlockPin: %v", err)
		}
	}
	router := registryRouter(t, published)

	envelope := doProtocols(t, router, "size=2")
	if len(envelope.Data.Items) != 2 || !envelope.Data.HasMore || envelope.Data.NextCursor == nil {
		t.Fatalf("page 1 = %+v", envelope.Data)
	}
	// Newest registration first.
	if envelope.Data.Items[0].ProtocolPath != "/protocols/p2" || envelope.Data.Items[1].ProtocolPath != "/protocols/p1" {
		t.Fatalf("page 1 order = %s, %s", envelope.Data.Items[0].ProtocolPath, envelope.Data.Items[1].ProtocolPath)
	}
	envelope = doProtocols(t, router, "size=2&cursor="+*envelope.Data.NextCursor)
	if len(envelope.Data.Items) != 1 || envelope.Data.HasMore || envelope.Data.NextCursor != nil {
		t.Fatalf("page 2 = %+v", envelope.Data)
	}
	if envelope.Data.Items[0].ProtocolPath != "/protocols/p0" {
		t.Fatalf("page 2 item = %s", envelope.Data.Items[0].ProtocolPath)
	}
	envelope = doProtocols(t, router, "cursor=!!!")
	if envelope.Code != codeInvalidParam {
		t.Fatalf("invalid cursor: code = %d, want 40000", envelope.Code)
	}
}

type checkEnvelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Path      string `json:"path"`
		Available bool   `json:"available"`
		Existing  *struct {
			PinId        string `json:"pinId"`
			CurrentPinId string `json:"currentPinId"`
			Title        string `json:"title"`
			ProtocolName string `json:"protocolName"`
			Version      string `json:"version"`
			CreatedAt    int64  `json:"createdAt"`
			Confirmed    bool   `json:"confirmed"`
			Author       struct {
				MetaId string `json:"metaid"`
			} `json:"author"`
		} `json:"existing"`
	} `json:"data"`
}

func doCheck(t *testing.T, router *gin.Engine, query string) checkEnvelope {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/metaweb/protocols/check?"+query, nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200", recorder.Code)
	}
	var envelope checkEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return envelope
}

func TestHandleProtocolCheck(t *testing.T) {
	published := setupPublishedContent(t)
	router := registryRouter(t, published)

	// Param validation: missing and malformed paths are 40000.
	for _, query := range []string{"", "path=%20%20", "path=/other/x", "path=/protocols/UPPER%20case"} {
		if envelope := doCheck(t, router, query); envelope.Code != codeInvalidParam {
			t.Fatalf("query %q: code = %d, want 40000 (%q)", query, envelope.Code, envelope.Message)
		}
	}

	envelope := doCheck(t, router, "path=/protocols/fresh")
	if envelope.Code != 0 || !envelope.Data.Available || envelope.Data.Existing != nil {
		t.Fatalf("fresh path: %+v", envelope)
	}

	// A confirmed registration occupies the path; the query normalizes case.
	if _, err := published.HandleBlockPin(protocolPin(hexPinId('a', 0), publishedcontent.PathMetaProtocol, "create", "",
		1755000100, "gid-a", "meta-a", "addr-a",
		`{"title":"Foo","path":"/protocols/foo","protocolName":"foo","version":"1.0.0"}`)); err != nil {
		t.Fatalf("HandleBlockPin: %v", err)
	}
	envelope = doCheck(t, router, "path=/protocols/FOO")
	if envelope.Code != 0 || envelope.Data.Available || envelope.Data.Existing == nil {
		t.Fatalf("occupied path: %+v", envelope)
	}
	if envelope.Data.Path != "/protocols/foo" {
		t.Fatalf("echo path = %q, want normalized", envelope.Data.Path)
	}
	existing := envelope.Data.Existing
	if existing.PinId != hexPinId('a', 0) || existing.ProtocolName != "foo" || existing.Version != "1.0.0" ||
		!existing.Confirmed || existing.Author.MetaId != "meta-a" || existing.CreatedAt != 1755000100 {
		t.Fatalf("existing = %+v", existing)
	}

	// A mempool registration also occupies the path (anti-squatting), flagged
	// unconfirmed.
	if _, err := published.HandleMempoolPin(protocolPin(hexPinId('b', 0), publishedcontent.PathMetaProtocol, "create", "",
		1755000200, "gid-b", "meta-b", "addr-b",
		`{"title":"Pending","path":"/protocols/pending","protocolName":"pending"}`)); err != nil {
		t.Fatalf("HandleMempoolPin: %v", err)
	}
	envelope = doCheck(t, router, "path=/protocols/pending")
	if envelope.Data.Available || envelope.Data.Existing == nil || envelope.Data.Existing.Confirmed {
		t.Fatalf("mempool-occupied path: %+v", envelope.Data)
	}
}

type detailEnvelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Record struct {
			ProtocolPath string         `json:"protocolPath"`
			PinId        string         `json:"pinId"`
			CurrentPinId string         `json:"currentPinId"`
			Version      string         `json:"version"`
			Payload      map[string]any `json:"payload"`
		} `json:"record"`
		Versions []struct {
			PinId       string `json:"pinId"`
			Version     string `json:"version"`
			Timestamp   int64  `json:"timestamp"`
			Attribution string `json:"attribution"`
			Author      struct {
				MetaId string `json:"metaid"`
			} `json:"author"`
		} `json:"versions"`
		Conflicts []struct {
			PinId string `json:"pinId"`
		} `json:"conflicts"`
		InvalidModifies []struct {
			PinId            string `json:"pinId"`
			TargetSourcePinId string `json:"targetSourcePinId"`
			Reason           string `json:"reason"`
			Timestamp        int64  `json:"timestamp"`
			ModifierIdentity struct {
				GlobalMetaId string `json:"globalMetaId"`
			} `json:"modifierIdentity"`
		} `json:"invalidModifies"`
	} `json:"data"`
}

func doDetail(t *testing.T, router *gin.Engine, query string) detailEnvelope {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/metaweb/protocols/detail?"+query, nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200", recorder.Code)
	}
	var envelope detailEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return envelope
}

func TestHandleProtocolDetail_VersionsConflictsAudit(t *testing.T) {
	published := setupPublishedContent(t)
	source := hexPinId('a', 0)
	version2 := hexPinId('b', 1)
	conflict := hexPinId('c', 0)
	forged := hexPinId('d', 2)

	if _, err := published.HandleBlockPin(protocolPin(source, publishedcontent.PathMetaProtocol, "create", "",
		1755000100, "gid-owner", "meta-owner", "addr-owner",
		`{"title":"Owned","path":"/protocols/owned","protocolName":"owned","version":"1.0.0","protocolContent":"{ foo: 'bar' }","protocolContentType":"application/json"}`)); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Same-key later registration → conflict.
	if _, err := published.HandleBlockPin(protocolPin(conflict, publishedcontent.PathMetaProtocol, "create", "",
		1755000200, "gid-c", "meta-c", "addr-c",
		`{"title":"Owned copy","path":"/protocols/owned","protocolName":"owned","version":"1.0.0"}`)); err != nil {
		t.Fatalf("conflict create: %v", err)
	}
	// Owner modify advances the chain.
	if _, err := published.HandleBlockPin(protocolPin(version2, publishedcontent.PathMetaProtocol+"@"+source, "modify", "",
		1755000300, "gid-owner", "meta-owner", "addr-owner",
		`{"title":"Owned","path":"/protocols/owned","protocolName":"owned","version":"1.0.1","protocolContent":"{ foo: 'bar' }","protocolContentType":"application/json"}`)); err != nil {
		t.Fatalf("owner modify: %v", err)
	}
	// Foreign modify is audited, not chained.
	if _, err := published.HandleBlockPin(protocolPin(forged, publishedcontent.PathMetaProtocol+"@"+source, "modify", "",
		1755000400, "gid-forger", "meta-forger", "addr-forger",
		`{"title":"Hijacked","path":"/protocols/owned","version":"9.9.9"}`)); err != nil {
		t.Fatalf("forged modify: %v", err)
	}

	agg := newTestAggregator(t)
	agg.SetProtocolRegistry(published)
	agg.SetPinLookups(published, nil)
	// The chain projection is emulated: modify_history on the current pin,
	// and the original create payload for the version backfill.
	agg.SetRemotePinFetcher(&mapFetcher{pins: map[string]*RemotePin{
		version2: {PinId: version2, Path: publishedcontent.PathMetaProtocol, Operation: "modify", ChainName: "mvc",
			Timestamp: 1755000300, MetaId: "meta-owner", Address: "addr-owner",
			ContentBody:   []byte(`{"title":"Owned","path":"/protocols/owned","version":"1.0.1"}`),
			ModifyHistory: []string{source, version2}},
		source: {PinId: source, Path: publishedcontent.PathMetaProtocol, Operation: "create", ChainName: "mvc",
			Timestamp: 1755000100, MetaId: "meta-owner", Address: "addr-owner",
			ContentBody: []byte(`{"title":"Owned","path":"/protocols/owned","version":"1.0.0"}`)},
	}})
	router := newTestRouter(agg)

	envelope := doDetail(t, router, "path=/protocols/OWNED")
	if envelope.Code != 0 {
		t.Fatalf("code = %d message = %q", envelope.Code, envelope.Message)
	}
	record := envelope.Data.Record
	if record.PinId != source || record.CurrentPinId != version2 || record.Version != "1.0.1" ||
		record.ProtocolPath != "/protocols/owned" {
		t.Fatalf("record = %+v", record)
	}
	// Payload passthrough: protocolContent stays an unparsed string.
	if record.Payload["protocolContent"] != "{ foo: 'bar' }" || record.Payload["version"] != "1.0.1" {
		t.Fatalf("payload = %+v", record.Payload)
	}

	if len(envelope.Data.Versions) != 2 {
		t.Fatalf("versions = %+v, want 2", envelope.Data.Versions)
	}
	v := envelope.Data.Versions
	if v[0].PinId != source || v[0].Version != "1.0.0" || v[0].Timestamp != 1755000100 ||
		v[1].PinId != version2 || v[1].Version != "1.0.1" || v[1].Timestamp != 1755000300 {
		t.Fatalf("versions = %+v", v)
	}
	if v[0].Attribution != "chain" || v[1].Attribution != "chain" {
		t.Fatalf("attribution = %q/%q, want chain", v[0].Attribution, v[1].Attribution)
	}

	if len(envelope.Data.Conflicts) != 1 || envelope.Data.Conflicts[0].PinId != conflict {
		t.Fatalf("conflicts = %+v", envelope.Data.Conflicts)
	}
	if len(envelope.Data.InvalidModifies) != 1 {
		t.Fatalf("invalidModifies = %+v", envelope.Data.InvalidModifies)
	}
	audit := envelope.Data.InvalidModifies[0]
	if audit.PinId != forged || audit.TargetSourcePinId != source ||
		audit.Reason != "publisher_mismatch" || audit.ModifierIdentity.GlobalMetaId != "gid-forger" {
		t.Fatalf("audit = %+v", audit)
	}

	// pinId lookup resolves any chain member; path wins when both are given.
	byPin := doDetail(t, router, "pinId="+version2)
	if byPin.Code != 0 || byPin.Data.Record.PinId != source {
		t.Fatalf("detail by version pinId = %+v", byPin.Data.Record)
	}
	both := doDetail(t, router, "path=/protocols/owned&pinId="+conflict)
	if both.Data.Record.PinId != source {
		t.Fatalf("path must win over pinId: %+v", both.Data.Record)
	}
	// A conflict registration is itself detail-addressable, with the
	// authoritative entry listed as its conflict.
	byConflict := doDetail(t, router, "pinId="+conflict)
	if byConflict.Code != 0 || byConflict.Data.Record.PinId != conflict ||
		len(byConflict.Data.Conflicts) != 1 || byConflict.Data.Conflicts[0].PinId != source {
		t.Fatalf("detail by conflict pinId = %+v", byConflict.Data)
	}
}

func TestHandleProtocolDetail_Errors(t *testing.T) {
	published := setupPublishedContent(t)
	router := registryRouter(t, published)

	if envelope := doDetail(t, router, ""); envelope.Code != codeInvalidParam {
		t.Fatalf("no params: code = %d, want 40000", envelope.Code)
	}
	if envelope := doDetail(t, router, "path=bad"); envelope.Code != codeInvalidParam {
		t.Fatalf("bad path: code = %d, want 40000", envelope.Code)
	}
	if envelope := doDetail(t, router, "pinId=not-a-pin"); envelope.Code != codeInvalidParam {
		t.Fatalf("bad pinId: code = %d, want 40000", envelope.Code)
	}
	if envelope := doDetail(t, router, "path=/protocols/unknown"); envelope.Code != codeNotFound {
		t.Fatalf("unknown path: code = %d, want 40400", envelope.Code)
	}
	if envelope := doDetail(t, router, "pinId="+hexPinId('f', 0)); envelope.Code != codeNotFound {
		t.Fatalf("unknown pinId: code = %d, want 40400", envelope.Code)
	}

	// A non-metaprotocol record is not a protocol detail.
	if _, err := published.HandleBlockPin(pinInscription(hexPinId('e', 0), publishedcontent.PathSimpleNote, "create", "", `{"title":"note"}`)); err != nil {
		t.Fatalf("seed note: %v", err)
	}
	if envelope := doDetail(t, router, "pinId="+hexPinId('e', 0)); envelope.Code != codeNotFound {
		t.Fatalf("non-protocol pin: code = %d, want 40400", envelope.Code)
	}
}

func TestHandleProtocols_RegistryUnavailable(t *testing.T) {
	agg := newTestAggregator(t)
	router := newTestRouter(agg)
	for _, path := range []string{"/api/metaweb/protocols", "/api/metaweb/protocols/check?path=/protocols/x", "/api/metaweb/protocols/detail?path=/protocols/x"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		var envelope struct {
			Code int `json:"code"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if envelope.Code != codeUnavailable {
			t.Fatalf("%s: code = %d, want 50000", path, envelope.Code)
		}
	}
}
