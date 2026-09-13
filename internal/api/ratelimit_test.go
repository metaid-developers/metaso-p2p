package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRateLimiter_AllowRefills(t *testing.T) {
	l := NewRateLimiter(2, 2) // 2 rps, burst 2
	clock := 0.0
	l.now = func() float64 { return clock }

	for i := 0; i < 2; i++ {
		if ok, _ := l.Allow("k"); !ok {
			t.Fatalf("burst token %d denied", i)
		}
	}
	if ok, wait := l.Allow("k"); ok {
		t.Fatal("third immediate request allowed over burst")
	} else if wait <= 0 || wait > 0.5 {
		t.Fatalf("retryAfter = %f, want (0, 0.5]", wait)
	}

	// Half a second refills one token.
	clock += 0.5
	if ok, _ := l.Allow("k"); !ok {
		t.Fatal("refilled token denied")
	}
	if ok, _ := l.Allow("k"); ok {
		t.Fatal("second refill arrived too early")
	}
}

func TestRateLimiter_IdentitiesShareBuckets(t *testing.T) {
	l := NewRateLimiter(0.001, 1)
	clock := 0.0
	l.now = func() float64 { return clock }

	if ok, _ := l.Allow("key:idbots-fleet"); !ok {
		t.Fatal("first keyed request denied")
	}
	if ok, _ := l.Allow("key:idbots-fleet"); ok {
		t.Fatal("second keyed request allowed — buckets must be shared per key name")
	}
	if ok, _ := l.Allow("ip:1.2.3.4"); !ok {
		t.Fatal("different identity must have its own bucket")
	}
}

func TestRateLimiter_Middleware429(t *testing.T) {
	l := NewRateLimiter(0.001, 1)
	clock := 0.0
	l.now = func() float64 { return clock }
	keys := map[string]string{"idbots-fleet": "secret-token"}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/ping", l.Middleware(keys), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"code": 0})
	})

	do := func(token string) (*httptest.ResponseRecorder, map[string]any) {
		req := httptest.NewRequest(http.MethodGet, "/api/ping", nil)
		if token != "" {
			req.Header.Set("X-API-KEY", token)
		}
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		var body map[string]any
		_ = json.Unmarshal(recorder.Body.Bytes(), &body)
		return recorder, body
	}

	// Keyed request consumes the shared bucket; the second is limited with
	// Retry-After and a 429 envelope.
	if recorder, _ := do("secret-token"); recorder.Code != http.StatusOK {
		t.Fatalf("first keyed request = %d, want 200", recorder.Code)
	}
	recorder, body := do("secret-token")
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("limited request = %d, want 429", recorder.Code)
	}
	if body["code"] != float64(RateLimitErrorCode) {
		t.Fatalf("429 body code = %v, want %d", body["code"], RateLimitErrorCode)
	}
	if recorder.Header().Get("Retry-After") == "" {
		t.Fatal("429 response missing Retry-After header")
	}

	// Unknown tokens fall back to the per-IP bucket, which is distinct from
	// the keyed bucket — so the unknown-token request succeeds.
	if recorder, _ := do("wrong-token"); recorder.Code != http.StatusOK {
		t.Fatalf("unknown token = %d, want 200 (per-IP bucket)", recorder.Code)
	}
	// An un-keyed request from the same test IP shares that per-IP bucket.
	if recorder, _ := do(""); recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("second per-IP request = %d, want 429", recorder.Code)
	}
}

func TestIdentityFor(t *testing.T) {
	keys := map[string]string{"fleet": "tok1"}
	if got := IdentityFor(keys, "1.2.3.4:5678", "tok1"); got != "key:fleet" {
		t.Fatalf("identity = %q, want key:fleet", got)
	}
	if got := IdentityFor(keys, "1.2.3.4:5678", ""); got != "ip:1.2.3.4:5678" {
		t.Fatalf("identity = %q, want ip:1.2.3.4:5678", got)
	}
	if got := IdentityFor(keys, "", "bad"); got != "ip:unknown" {
		t.Fatalf("identity = %q, want ip:unknown", got)
	}
}
