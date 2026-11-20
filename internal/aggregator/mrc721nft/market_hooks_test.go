package mrc721nft

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

type stubAddrResolver struct {
	byGID map[string][]string
}

func (r *stubAddrResolver) ResolveAddressesByGlobalMetaId(globalMetaId string) []string {
	return r.byGID[globalMetaId]
}

type stubItemEnricher struct{}

func (e *stubItemEnricher) EnrichItemDetail(itemPinId, ownerAddress string) (*OwnerProfileDTO, any) {
	return &OwnerProfileDTO{
			Address:      ownerAddress,
			GlobalMetaId: "idq1owner0000000000000000000000000000000000",
			Name:         "OwnerName",
			IsOnline:     true,
		}, map[string]any{
			"itemPinId": itemPinId,
			"active":    true,
		}
}

func TestGlobalMetaIdAddressQueriesAndTransferHookNoDeadlock(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := storage.NewPebbleStore(t.TempDir())
	t.Cleanup(func() { _ = store.Close() })

	agg := &Aggregator{}
	if err := agg.Init(store, nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	agg.SetZeroConfGates(true, true)

	const addr = "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa"
	const addr2 = "1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2"
	const gid = "idq1owner0000000000000000000000000000000000"
	const colPin = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccci0"
	const itemPin = "1111111111111111111111111111111111111111111111111111111111111111i0"

	agg.SetAddressResolver(&stubAddrResolver{byGID: map[string][]string{gid: {addr}}})
	agg.SetItemDetailEnricher(&stubItemEnricher{})

	var hookCalls atomic.Int32
	agg.SetTransferEventHook(func(ev TransferEvent) {
		hookCalls.Add(1)
		// Calling read methods inside the transfer hook must NEVER deadlock.
		owner, _, burned, found := agg.ItemOwnership(ev.ItemPinId, true)
		if !found || burned || owner != ev.ToAddress {
			t.Fatalf("unexpected ownership inside hook: owner=%s burned=%v found=%v", owner, burned, found)
		}
	})

	_, _ = agg.HandleBlockPin(&aggregator.PinInscription{
		Id:            colPin,
		Operation:     "create",
		Path:          "/nft/mrc721/testcol/collection_desc",
		Address:       addr,
		CreateAddress: addr,
		GenesisHeight: 10,
		Timestamp:     1760000000,
		ContentBody:   []byte(`{"name":"TestCol","totalSupply":10}`),
		ChainName:     "mvc",
		IsOpReturn:    true,
		Output:        "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:0",
	})
	_, _ = agg.HandleBlockPin(&aggregator.PinInscription{
		Id:                 itemPin,
		Operation:          "create",
		Path:               "/nft/mrc721/testcol",
		Address:            addr,
		CreateAddress:      addr,
		GenesisHeight:      11,
		Timestamp:          1760000010,
		ContentBody:        []byte(`{"name":"TestItem #1"}`),
		ChainName:          "mvc",
		IsOpReturn:         true,
		GenesisTransaction: "1111111111111111111111111111111111111111111111111111111111111111",
		Output:             "1111111111111111111111111111111111111111111111111111111111111111:0",
		OutputValue:        1000,
	})

	r := gin.New()
	agg.RegisterRoutes(r.Group("/api"))

	// Query /address/collection?globalMetaId=...
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/mrc721/address/collection?globalMetaId="+gid, nil))
	var colResp struct {
		Code int `json:"code"`
		Data struct {
			Total int `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &colResp); err != nil {
		t.Fatalf("decode colResp: %v", err)
	}
	if colResp.Code != 1 || colResp.Data.Total != 1 {
		t.Fatalf("expected total=1 for globalMetaId collection query, got %+v", colResp)
	}

	// Query /address/item?globalMetaId=...
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/api/mrc721/address/item?globalMetaId="+gid, nil))
	var itemListResp struct {
		Code int `json:"code"`
		Data struct {
			Total int `json:"total"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec2.Body.Bytes(), &itemListResp)
	if itemListResp.Code != 1 || itemListResp.Data.Total != 1 {
		t.Fatalf("expected total=1 for globalMetaId item query, got %+v", itemListResp)
	}

	// Query /item/info enriched with ownerProfile and activeListing (and absent on confirmedOnly=1).
	rec3 := httptest.NewRecorder()
	r.ServeHTTP(rec3, httptest.NewRequest(http.MethodGet, "/api/mrc721/item/info?pinId="+itemPin, nil))
	var infoResp struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	}
	_ = json.Unmarshal(rec3.Body.Bytes(), &infoResp)
	if infoResp.Data["ownerProfile"] == nil || infoResp.Data["activeListing"] == nil {
		t.Fatalf("expected ownerProfile and activeListing on /item/info, got %+v", infoResp.Data)
	}

	// Confirmed transfer triggers hook cleanly without deadlock.
	agg.HandleBlockTransfers("mvc", 20, map[string]aggregator.TransferNotice{
		"1111111111111111111111111111111111111111111111111111111111111111:0": {
			FromAddress: addr,
			ToAddress:   addr2,
			Output:      "2222222222222222222222222222222222222222222222222222222222222222:0",
			OutputValue: 1000,
			Location:    "2222222222222222222222222222222222222222222222222222222222222222:0:0",
		},
	})
	if hookCalls.Load() != 1 {
		t.Fatalf("expected 1 transfer hook call, got %d", hookCalls.Load())
	}
}
