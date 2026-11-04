package mrc721

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/api"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

const (
	validItemPinId = "1111111111111111111111111111111111111111111111111111111111111111i0"
	testSeller1    = "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa"
	testSeller2    = "1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2"
	testCollection = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccci0"
)

func hexOf(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

// testSellPinId derives a protocol-shaped sell pin id from a seed; the
// optional index makes several pins share one tx (same height/time ties).
func testSellPinId(seed string, index int) string {
	return hexOf(seed) + "i" + strconv.Itoa(index)
}

func newTestAggregator(t *testing.T) *Aggregator {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store := storage.NewPebbleStore(t.TempDir())
	agg := &Aggregator{}
	if err := agg.Init(store, cache.New(store)); err != nil {
		t.Fatalf("init aggregator: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return agg
}

func newTestRouter(agg *Aggregator) *gin.Engine {
	router := gin.New()
	router.Use(api.RequestTimingMiddleware())
	agg.RegisterRoutes(router.Group("/api"))
	return router
}

func doMrc721Request(t *testing.T, router *gin.Engine, path string) (int, envelope) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	router.ServeHTTP(rec, req)
	var env envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode response %s: %v\nraw: %s", path, err, rec.Body.String())
	}
	return rec.Code, env
}

// envelope mirrors the mrc721 wire envelope (loose data payload).
type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// mrc721SellPin builds a confirmed-chain pin inscription for the sell path.
func mrc721SellPin(id string, height int64, timestamp int64, contentBody []byte, operation string) *aggregator.PinInscription {
	if operation == "" {
		operation = "create"
	}
	return &aggregator.PinInscription{
		Id:            id,
		Path:          PathSell,
		Operation:     operation,
		ContentBody:   contentBody,
		ChainName:     "mvc",
		GenesisHeight: height,
		Timestamp:     timestamp,
		CreateAddress: testSeller1,
	}
}

func sellContent(t *testing.T, fields map[string]any) []byte {
	t.Helper()
	if _, ok := fields["itemPinId"]; !ok {
		fields["itemPinId"] = validItemPinId
	}
	if _, ok := fields["sellerAddress"]; !ok {
		fields["sellerAddress"] = testSeller1
	}
	if _, ok := fields["active"]; !ok {
		fields["active"] = true
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal content: %v", err)
	}
	return raw
}
