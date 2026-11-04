package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// /api/tx/status contract tests: man-p2p envelope parity — parameter error
// (code 404) for missing params/unknown chains, found:false/unknown on RPC
// errors, and the confirmed/unconfirmed fact set.

type fakeTxStatusResolver struct {
	ok         bool
	confirmed  bool
	confirmats int64
	blockHash  string
	height     int64
	err        error
}

func (f *fakeTxStatusResolver) TransactionStatus(chainName, txid string) (bool, bool, int64, string, int64, error) {
	if chainName == "ghostchain" {
		return false, false, 0, "", 0, nil
	}
	return f.ok, f.confirmed, f.confirmats, f.blockHash, f.height, f.err
}

func doTxStatus(t *testing.T, resolver TxStatusResolver, query string) (int, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/tx/status", txStatusHandler(resolver))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/tx/status?"+query, nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v\nraw: %s", err, rec.Body.String())
	}
	return rec.Code, body
}

func TestTxStatusConfirmed(t *testing.T) {
	resolver := &fakeTxStatusResolver{ok: true, confirmed: true, confirmats: 6, blockHash: "00000000abc", height: 900}
	code, body := doTxStatus(t, resolver, "chainName=mvc&txid=abc123")
	if code != http.StatusOK || body["code"].(float64) != 1 || body["message"] != "ok" {
		t.Fatalf("envelope wrong: %v", body)
	}
	data := body["data"].(map[string]any)
	if data["found"] != true || data["txStatus"] != "confirmed" || data["confirmations"].(float64) != 6 ||
		data["blockHash"] != "00000000abc" || data["height"].(float64) != 900 || data["chainName"] != "mvc" || data["txid"] != "abc123" {
		t.Fatalf("confirmed facts wrong: %v", data)
	}
}

func TestTxStatusUnconfirmed(t *testing.T) {
	resolver := &fakeTxStatusResolver{ok: true, confirmed: false, confirmats: 0}
	_, body := doTxStatus(t, resolver, "chainName=mvc&txid=abc123")
	data := body["data"].(map[string]any)
	if data["found"] != true || data["txStatus"] != "unconfirmed" || data["confirmations"].(float64) != 0 {
		t.Fatalf("unconfirmed facts wrong: %v", data)
	}
}

func TestTxStatusRPCErrorMeansUnknown(t *testing.T) {
	resolver := &fakeTxStatusResolver{ok: true, err: errors.New("rpc down")}
	_, body := doTxStatus(t, resolver, "chainName=mvc&txid=abc123")
	if body["code"].(float64) != 1 {
		t.Fatalf("rpc errors still answer ok envelope: %v", body)
	}
	data := body["data"].(map[string]any)
	if data["found"] != false || data["txStatus"] != "unknown" {
		t.Fatalf("rpc error shape wrong: %v", data)
	}
}

func TestTxStatusParameterErrors(t *testing.T) {
	// Missing params.
	_, body := doTxStatus(t, &fakeTxStatusResolver{}, "chainName=mvc")
	if body["code"].(float64) != 404 || body["message"] != "Parameter error." {
		t.Fatalf("missing params wrong: %v", body)
	}
	// Unknown chain.
	_, body = doTxStatus(t, &fakeTxStatusResolver{}, "chainName=ghostchain&txid=abc")
	if body["code"].(float64) != 404 {
		t.Fatalf("unknown chain wrong: %v", body)
	}
	// Nil resolver (no engine wired): unknown-chain behavior.
	_, body = doTxStatus(t, nil, "chainName=mvc&txid=abc")
	if body["code"].(float64) != 404 {
		t.Fatalf("nil resolver wrong: %v", body)
	}
}
