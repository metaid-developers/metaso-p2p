package federation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetaletClientMVCAddressUTXOsRequestsExpectedPathAndMapsResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method: want GET got %s", r.Method)
		}
		if r.URL.Path != "/wallet-api/v4/mvc/address/utxo-list" {
			t.Fatalf("path: want /wallet-api/v4/mvc/address/utxo-list got %s", r.URL.Path)
		}

		query := r.URL.Query()
		if query.Get("net") != "livenet" {
			t.Fatalf("net query: want livenet got %q", query.Get("net"))
		}
		if query.Get("address") != "mvc-address" {
			t.Fatalf("address query: want mvc-address got %q", query.Get("address"))
		}
		if query.Get("flag") != "confirmed" {
			t.Fatalf("flag query: want confirmed got %q", query.Get("flag"))
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":           0,
			"message":        "success",
			"processingTime": 2,
			"data": map[string]any{
				"list": []map[string]any{
					{
						"txid":     "tx-1",
						"outIndex": 2,
						"value":    1234567890123,
						"address":  "mvc-address",
						"height":   345,
						"flag":     "confirmed",
					},
				},
			},
		})
	}))
	t.Cleanup(server.Close)

	client, err := NewMetaletClient(server.URL)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	got, err := client.MVCAddressUTXOs(context.Background(), "livenet", "mvc-address", "confirmed")
	if err != nil {
		t.Fatalf("mvc address utxos: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("utxos: want 1 got %d", len(got))
	}
	want := MVCUTXO{
		TxID:     "tx-1",
		OutIndex: 2,
		Value:    1234567890123,
		Address:  "mvc-address",
		Height:   345,
		Flag:     "confirmed",
	}
	if got[0] != want {
		t.Fatalf("utxo mapping:\nwant %#v\n got %#v", want, got[0])
	}
}

func TestMetaletClientMVCAddressUTXOsOmitsEmptyFlagAndNormalizesWalletAPIBase(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wallet-api/v4/mvc/address/utxo-list" {
			t.Fatalf("path should not double-prefix wallet-api, got %s", r.URL.Path)
		}
		if _, ok := r.URL.Query()["flag"]; ok {
			t.Fatalf("flag query should be omitted when empty, got %q", r.URL.Query().Get("flag"))
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"success","processingTime":2,"data":{"list":[]}}`))
	}))
	t.Cleanup(server.Close)

	client, err := NewMetaletClient(server.URL + "/wallet-api")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	got, err := client.MVCAddressUTXOs(context.Background(), "livenet", "mvc-address", "")
	if err != nil {
		t.Fatalf("mvc address utxos: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("utxos: want empty got %d", len(got))
	}
}

func TestMetaletClientBroadcastMVCPostsExpectedJSONBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method: want POST got %s", r.Method)
		}
		if r.URL.Path != "/wallet-api/v4/mvc/tx/broadcast" {
			t.Fatalf("path: want /wallet-api/v4/mvc/tx/broadcast got %s", r.URL.Path)
		}
		if contentType := r.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
			t.Fatalf("content-type: want application/json got %q", contentType)
		}

		var body MVCBroadcastRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		want := MVCBroadcastRequest{
			Chain:     "mvc",
			Net:       "livenet",
			PublicKey: "public-key",
			RawTx:     "raw-tx",
		}
		if body != want {
			t.Fatalf("broadcast body:\nwant %#v\n got %#v", want, body)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"success","processingTime":2,"data":"broadcast-txid"}`))
	}))
	t.Cleanup(server.Close)

	client, err := NewMetaletClient(server.URL)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	got, err := client.BroadcastMVC(context.Background(), MVCBroadcastRequest{
		Chain:     "mvc",
		Net:       "livenet",
		PublicKey: "public-key",
		RawTx:     "raw-tx",
	})
	if err != nil {
		t.Fatalf("broadcast mvc: %v", err)
	}
	if got != "broadcast-txid" {
		t.Fatalf("broadcast result: want broadcast-txid got %q", got)
	}
}

func TestMetaletClientEnvelopeErrorReturnsTypedError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":1,"message":"rpc error: TX decode failed","processingTime":2,"data":null}`))
	}))
	t.Cleanup(server.Close)

	client, err := NewMetaletClient(server.URL)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.BroadcastMVC(context.Background(), MVCBroadcastRequest{
		Chain:     "mvc",
		Net:       "livenet",
		PublicKey: "public-key",
		RawTx:     "invalid-raw-tx",
	})
	if err == nil {
		t.Fatal("broadcast mvc should return an error for nonzero API code")
	}

	var metaletErr *MetaletError
	if !errors.As(err, &metaletErr) {
		t.Fatalf("error should be MetaletError, got %T %[1]v", err)
	}
	if metaletErr.StatusCode != http.StatusOK {
		t.Fatalf("status code: want %d got %d", http.StatusOK, metaletErr.StatusCode)
	}
	if metaletErr.Code != 1 {
		t.Fatalf("api code: want 1 got %d", metaletErr.Code)
	}
	if !strings.Contains(metaletErr.Message, "TX decode failed") {
		t.Fatalf("api message should include TX decode failure, got %q", metaletErr.Message)
	}
	if !strings.Contains(metaletErr.Body, "TX decode failed") {
		t.Fatalf("body should include response body, got %q", metaletErr.Body)
	}
}

func TestMetaletClientNon2xxReturnsTypedError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "wallet rejected", http.StatusTeapot)
	}))
	t.Cleanup(server.Close)

	client, err := NewMetaletClient(server.URL)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.MVCAddressUTXOs(context.Background(), "livenet", "mvc-address", "")
	if err == nil {
		t.Fatal("mvc address utxos should return an error for non-2xx response")
	}

	var metaletErr *MetaletError
	if !errors.As(err, &metaletErr) {
		t.Fatalf("error should be MetaletError, got %T %[1]v", err)
	}
	if metaletErr.StatusCode != http.StatusTeapot {
		t.Fatalf("status code: want %d got %d", http.StatusTeapot, metaletErr.StatusCode)
	}
	if !strings.Contains(metaletErr.Body, "wallet rejected") {
		t.Fatalf("body should include response body, got %q", metaletErr.Body)
	}
}

func TestMetaletClientRequestTimeoutIsHonored(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(time.Second):
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewMetaletClient(server.URL, WithMetaletTimeout(20*time.Millisecond))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	started := time.Now()
	_, err = client.MVCAddressUTXOs(context.Background(), "livenet", "mvc-address", "")
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("mvc address utxos should time out")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("timeout was not honored quickly enough, elapsed %s", elapsed)
	}
}
