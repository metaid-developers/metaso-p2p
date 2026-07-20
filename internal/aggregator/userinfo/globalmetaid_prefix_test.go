package userinfo

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
	"github.com/metaid-developers/metaso-p2p/pkg/idaddress"
)

func TestNormaliseGlobalMetaIDPrefixRequiresAtLeastEightCharacters(t *testing.T) {
	for _, test := range []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "seven", input: "idq1w8y", wantErr: true},
		{name: "eight", input: "idq1w8ye", want: "idq1w8ye"},
		{name: "longer uppercase", input: " IDQ1W8YEQ ", want: "idq1w8yeq"},
		{name: "valid p2tr header", input: "idt1w8ye", want: "idt1w8ye"},
		{name: "invalid version", input: "idx1w8ye", wantErr: true},
		{name: "invalid separator", input: "idq0w8ye", wantErr: true},
		{name: "invalid charset", input: "idq1w8yb", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := normaliseGlobalMetaIDPrefix(test.input)
			if test.wantErr {
				if err == nil {
					t.Fatalf("normaliseGlobalMetaIDPrefix(%q) returned no error", test.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("normaliseGlobalMetaIDPrefix(%q): %v", test.input, err)
			}
			if got != test.want {
				t.Fatalf("normaliseGlobalMetaIDPrefix(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestHandleGlobalMetaIDPrefixRequiresReadyIndex(t *testing.T) {
	_, store, router := setupTestAggregator(t)
	defer store.Close()

	w := performRequest(t, router, http.MethodGet, "/api/info/globalmetaid?prefix=idq1w8ye")
	assertPrefixResponseCode(t, w.Body.Bytes(), globalMetaIDPrefixNotReadyCode)
}

func TestHandleGlobalMetaIDPrefixRejectsSevenCharacters(t *testing.T) {
	agg, store, router := setupTestAggregator(t)
	defer store.Close()
	markGlobalMetaIDPrefixIndexReady(t, agg)

	w := performRequest(t, router, http.MethodGet, "/api/info/globalmetaid?prefix=idq1w8y")
	assertPrefixResponseCode(t, w.Body.Bytes(), 40000)
}

func TestHandleGlobalMetaIDPrefixReturnsEarliestCreationNotLexicalFirst(t *testing.T) {
	agg, store, router := setupTestAggregator(t)
	defer store.Close()
	markGlobalMetaIDPrefixIndexReady(t, agg)

	laterLexicalFirst := globalMetaIDCreationRecord{
		GlobalMetaID: "idq1w8yeqqqq",
		MetaID:       "later",
		CreatedAt:    2000,
		ChainName:    "mvc",
		PinID:        "later:i0",
	}
	earlierLexicalLast := globalMetaIDCreationRecord{
		GlobalMetaID: "idq1w8yezzzz",
		MetaID:       "earlier",
		CreatedAt:    1000,
		ChainName:    "mvc",
		PinID:        "earlier:i0",
	}
	if _, err := agg.upsertGlobalMetaIDCreationRecords([]globalMetaIDCreationRecord{
		laterLexicalFirst,
		earlierLexicalLast,
	}); err != nil {
		t.Fatalf("upsertGlobalMetaIDCreationRecords: %v", err)
	}

	w := performRequest(t, router, http.MethodGet, "/api/info/globalmetaid?prefix=idq1w8ye")
	if w.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200", w.Code)
	}
	var response struct {
		Code int `json:"code"`
		Data struct {
			GlobalMetaID string `json:"globalMetaId"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Code != 1 {
		t.Fatalf("code = %d, want 1", response.Code)
	}
	if response.Data.GlobalMetaID != earlierLexicalLast.GlobalMetaID {
		t.Fatalf("globalMetaId = %q, want earliest %q", response.Data.GlobalMetaID, earlierLexicalLast.GlobalMetaID)
	}

	// A longer prefix stays inside the same eight-character bucket and skips
	// chronologically earlier records that do not match the complete input.
	w = performRequest(t, router, http.MethodGet, "/api/info/globalmetaid?prefix=idq1w8yeq")
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode longer-prefix response: %v", err)
	}
	if response.Data.GlobalMetaID != laterLexicalFirst.GlobalMetaID {
		t.Fatalf("longer-prefix globalMetaId = %q, want %q", response.Data.GlobalMetaID, laterLexicalFirst.GlobalMetaID)
	}
}

func TestGlobalMetaIDPrefixRouteHasMetafileIndexerCompatibilityMount(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()
	markGlobalMetaIDPrefixIndexReady(t, agg)
	if _, err := agg.upsertGlobalMetaIDCreationRecords([]globalMetaIDCreationRecord{{
		GlobalMetaID: "idq1w8yeqqqq",
		MetaID:       "meta",
		CreatedAt:    1000,
		ChainName:    "mvc",
		PinID:        "root:i0",
	}}); err != nil {
		t.Fatalf("upsertGlobalMetaIDCreationRecords: %v", err)
	}

	router := gin.New()
	agg.RegisterRoutes(router.Group("/api"))
	agg.RegisterRoutes(router.Group("/metafile-indexer/api"))
	for _, path := range []string{
		"/api/info/globalmetaid?prefix=idq1w8ye",
		"/metafile-indexer/api/info/globalmetaid?prefix=idq1w8ye",
	} {
		w := performRequest(t, router, http.MethodGet, path)
		var response struct {
			Code int `json:"code"`
			Data struct {
				GlobalMetaID string `json:"globalMetaId"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if response.Code != 1 || response.Data.GlobalMetaID != "idq1w8yeqqqq" {
			t.Fatalf("response for %s = %+v", path, response)
		}
	}
}

func TestEarlierReplayAtomicallyReplacesCreationIndex(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	globalMetaID := "idq1w8yeqqqq"
	later := globalMetaIDCreationRecord{
		GlobalMetaID: globalMetaID,
		MetaID:       "meta",
		CreatedAt:    2000,
		ChainName:    "mvc",
		PinID:        "later:i0",
	}
	earlier := later
	earlier.CreatedAt = 1000
	earlier.PinID = "earlier:i0"

	result, err := agg.upsertGlobalMetaIDCreationRecords([]globalMetaIDCreationRecord{later})
	if err != nil {
		t.Fatalf("initial upsert: %v", err)
	}
	if result.Inserted != 1 {
		t.Fatalf("initial result = %+v, want one insert", result)
	}
	result, err = agg.upsertGlobalMetaIDCreationRecords([]globalMetaIDCreationRecord{earlier})
	if err != nil {
		t.Fatalf("earlier replay: %v", err)
	}
	if result.Replaced != 1 {
		t.Fatalf("replacement result = %+v, want one replacement", result)
	}

	var keys []string
	if err := store.ScanPrefix(namespace, globalMetaIDPrefixBucketKey(globalMetaID[:8]), func(key, _ []byte) error {
		keys = append(keys, string(key))
		return nil
	}); err != nil {
		t.Fatalf("scan prefix keys: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("prefix key count = %d, want 1; keys=%v", len(keys), keys)
	}
	if !strings.Contains(keys[0], "00000000000003e8") || strings.Contains(keys[0], "00000000000007d0") {
		t.Fatalf("remaining key does not contain earlier timestamp: %q", keys[0])
	}
}

func TestConfirmedRootIsIndexedButMempoolRootIsExcluded(t *testing.T) {
	agg, store, router := setupTestAggregator(t)
	defer store.Close()
	markGlobalMetaIDPrefixIndexReady(t, agg)

	globalMetaID, err := idaddress.EncodeIDAddress(idaddress.VersionP2PKH, []byte{
		1, 2, 3, 4, 5, 6, 7, 8, 9, 10,
		11, 12, 13, 14, 15, 16, 17, 18, 19, 20,
	})
	if err != nil {
		t.Fatalf("EncodeIDAddress: %v", err)
	}
	pin := &aggregator.PinInscription{
		Id:           "root:i0",
		Path:         "/",
		Operation:    "create",
		MetaId:       "meta-root",
		GlobalMetaId: globalMetaID,
		Address:      "address-root",
		ChainName:    "mvc",
		Timestamp:    1700000000,
	}
	if _, err := agg.HandleMempoolPin(pin); err != nil {
		t.Fatalf("HandleMempoolPin: %v", err)
	}

	prefix := globalMetaID[:globalMetaIDMinimumPrefixLength]
	w := performRequest(t, router, http.MethodGet, "/api/info/globalmetaid?prefix="+prefix)
	assertPrefixResponseCode(t, w.Body.Bytes(), 40400)

	if _, err := agg.HandleBlockPin(pin); err != nil {
		t.Fatalf("HandleBlockPin: %v", err)
	}
	w = performRequest(t, router, http.MethodGet, "/api/info/globalmetaid?prefix="+prefix)
	var response struct {
		Code int `json:"code"`
		Data struct {
			GlobalMetaID string `json:"globalMetaId"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode confirmed response: %v", err)
	}
	if response.Code != 1 || response.Data.GlobalMetaID != globalMetaID {
		t.Fatalf("confirmed response = %+v, want code=1 globalMetaId=%q", response, globalMetaID)
	}
}

func markGlobalMetaIDPrefixIndexReady(t *testing.T, agg *Aggregator) {
	t.Helper()
	if err := agg.saveGlobalMetaIDPrefixIndexState(globalMetaIDPrefixIndexState{
		Status: globalMetaIDPrefixStateReady,
	}); err != nil {
		t.Fatalf("save ready prefix state: %v", err)
	}
}

func assertPrefixResponseCode(t *testing.T, raw []byte, want int) {
	t.Helper()
	var response struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Code != want {
		t.Fatalf("code = %d, want %d; body=%s", response.Code, want, raw)
	}
}

func BenchmarkLookupGlobalMetaIDPrefix(b *testing.B) {
	for _, size := range []int{100_000, 1_000_000} {
		b.Run(fmt.Sprintf("records=%d", size), func(b *testing.B) {
			store := storage.NewPebbleStore(b.TempDir())
			b.Cleanup(func() { _ = store.Close() })
			agg := &Aggregator{}
			if err := agg.Init(store, cache.New(store)); err != nil {
				b.Fatalf("init aggregator: %v", err)
			}

			const seedBatchSize = 10_000
			for start := 0; start < size; start += seedBatchSize {
				end := min(start+seedBatchSize, size)
				records := make([]globalMetaIDCreationRecord, 0, end-start)
				for i := start; i < end; i++ {
					suffix := fmtBase32TestValue(i)
					records = append(records, globalMetaIDCreationRecord{
						GlobalMetaID: "idq1w8ye" + suffix,
						MetaID:       suffix,
						CreatedAt:    int64(i + 1),
						ChainName:    "mvc",
						PinID:        suffix + ":i0",
					})
				}
				if _, err := agg.upsertGlobalMetaIDCreationRecords(records); err != nil {
					b.Fatalf("seed prefix index: %v", err)
				}
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := agg.lookupGlobalMetaIDPrefix("idq1w8ye"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func fmtBase32TestValue(value int) string {
	const alphabet = idaddress.Charset
	buf := make([]byte, 8)
	for i := len(buf) - 1; i >= 0; i-- {
		buf[i] = alphabet[value%len(alphabet)]
		value /= len(alphabet)
	}
	return string(buf)
}
