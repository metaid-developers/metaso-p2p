package mrc721

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestParseSellIntentPayloadValid(t *testing.T) {
	body := `{
		"active": true,
		"itemPinId": "` + validItemPinId + `",
		"collectionName": "pixel-cats",
		"collectionPinId": "0000000000000000000000000000000000000000000000000000000000000000i1",
		"itemOutpoint": {"txid": "` + hexOf("outpoint") + `", "vout": 0, "satoshis": 1},
		"sellerAddress": "1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2",
		"sellerMetaId": "` + hexOf("seller") + `",
		"priceHintSats": 30000,
		"acceptedCurrencies": ["space", "btc", "fiat:CNY"],
		"settlement": ["atomic-swap", "escrow"],
		"note": "free text",
		"createdAt": 1793868000,
		"expiresAt": 1794472800
	}`
	payload, err := parseSellIntentPayload([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if payload.ItemPinId != validItemPinId || !payload.Active {
		t.Fatalf("unexpected core fields: %+v", payload)
	}
	if payload.SellerAddress != "1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2" {
		t.Fatalf("seller address = %q", payload.SellerAddress)
	}
	if payload.PriceHintSats != 30000 || payload.CreatedAt != 1793868000 || payload.ExpiresAt != 1794472800 {
		t.Fatalf("numeric fields: %+v", payload)
	}
	if payload.ItemOutpoint == nil || payload.ItemOutpoint.Vout != 0 || payload.ItemOutpoint.Satoshis != 1 {
		t.Fatalf("outpoint: %+v", payload.ItemOutpoint)
	}
	if len(payload.AcceptedCurrencies) != 3 || len(payload.Settlement) != 2 {
		t.Fatalf("lists: %+v", payload)
	}
}

// The host-side writer uses camelCase; the parser must also accept the
// snake_case / lowercase duplicates seen from other writers.
func TestParseSellIntentPayloadSnakeCaseTolerance(t *testing.T) {
	body := `{
		"active": false,
		"itempinid": "` + validItemPinId + `",
		"selleraddress": "1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2",
		"collection_name": "pixel-cats",
		"pricehintsats": 1200
	}`
	payload, err := parseSellIntentPayload([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if payload.ItemPinId != validItemPinId || payload.Active {
		t.Fatalf("core fields: %+v", payload)
	}
	if payload.CollectionName != "pixel-cats" || payload.PriceHintSats != 1200 {
		t.Fatalf("tolerated fields: %+v", payload)
	}
}

func TestParseSellIntentPayloadBase64Body(t *testing.T) {
	inner := []byte(`{"active":true,"itemPinId":"` + validItemPinId + `","sellerAddress":"1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2"}`)
	encoded := base64.StdEncoding.EncodeToString(inner)
	payload, err := parseSellIntentPayload([]byte(encoded))
	if err != nil {
		t.Fatalf("parse base64 body: %v", err)
	}
	if payload.ItemPinId != validItemPinId || !payload.Active {
		t.Fatalf("payload: %+v", payload)
	}
}

func TestParseSellIntentPayloadInvalid(t *testing.T) {
	validAddress := "1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2"
	cases := []struct {
		name string
		body string
	}{
		{"empty", ``},
		{"not an object", `"hello"`},
		{"missing itemPinId", `{"active":true,"sellerAddress":"` + validAddress + `"}`},
		{"bad itemPinId", `{"active":true,"itemPinId":"nothex","sellerAddress":"` + validAddress + `"}`},
		{"missing sellerAddress", `{"active":true,"itemPinId":"` + validItemPinId + `"}`},
		{"bad sellerAddress", `{"active":true,"itemPinId":"` + validItemPinId + `","sellerAddress":"!!not-base58!!"}`},
		{"missing active", `{"itemPinId":"` + validItemPinId + `","sellerAddress":"` + validAddress + `"}`},
		{"non-boolean active", `{"active":"maybe","itemPinId":"` + validItemPinId + `","sellerAddress":"` + validAddress + `"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseSellIntentPayload([]byte(tc.body)); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

func TestParseSellIntentPayloadMillisecondTimestamps(t *testing.T) {
	body := `{"active":true,"itemPinId":"` + validItemPinId + `","sellerAddress":"1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2","createdAt":1793868000000,"expiresAt":1794472800000}`
	payload, err := parseSellIntentPayload([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if payload.CreatedAt != 1793868000 || payload.ExpiresAt != 1794472800 {
		t.Fatalf("expected second-granularity timestamps, got %d/%d", payload.CreatedAt, payload.ExpiresAt)
	}
}

func TestPayloadBoolStringTolerance(t *testing.T) {
	ok, valid := payloadBool(json.RawMessage(`"TRUE"`))
	if !valid || !ok {
		t.Fatalf("string true not tolerated: %v %v", ok, valid)
	}
	ok, valid = payloadBool(json.RawMessage(`"false"`))
	if !valid || ok {
		t.Fatalf("string false not tolerated: %v %v", ok, valid)
	}
	if _, valid = payloadBool(json.RawMessage(`"maybe"`)); valid {
		t.Fatal("garbage string accepted as bool")
	}
}
