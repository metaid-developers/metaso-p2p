package api

// Token-bucket rate limiter for the aggregation API (R7 of
// docs/specs/2026-09-13-metaweb-surf-reads-api.md): per-identity limits so a
// fleet of bots sharing one egress IP can be limited bot-aware via a shared
// API key, with standard 429 + Retry-After semantics. Disabled unless
// configured (config.RateLimitConfig.Enabled).

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// RateLimitErrorCode is the envelope business code carried by 429 bodies.
const RateLimitErrorCode = 42900

const rateLimitAPIKeyHeader = "X-API-KEY"

type rateBucket struct {
	tokens float64
	last   float64 // seconds, monotonic via the limiter clock
}

// RateLimiter is a shared token-bucket keyed by client identity.
type RateLimiter struct {
	mu      sync.Mutex
	rps     float64
	burst   float64
	buckets map[string]*rateBucket
	now     func() float64 // unix seconds; test hook
}

// NewRateLimiter builds a limiter with the given refill rate and burst.
func NewRateLimiter(requestsPerSecond float64, burst int) *RateLimiter {
	if requestsPerSecond <= 0 {
		requestsPerSecond = 1
	}
	if burst < 1 {
		burst = 1
	}
	return &RateLimiter{
		rps:     requestsPerSecond,
		burst:   float64(burst),
		buckets: map[string]*rateBucket{},
		now:     func() float64 { return float64(time.Now().UnixNano()) / 1e9 },
	}
}

// Allow consumes one token for the identity; when denied it returns the
// recommended retry delay in seconds.
func (l *RateLimiter) Allow(identity string) (bool, float64) {
	if l == nil {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	b, ok := l.buckets[identity]
	if !ok {
		l.buckets[identity] = &rateBucket{tokens: l.burst - 1, last: now}
		return true, 0
	}
	b.tokens += (now - b.last) * l.rps
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := (1 - b.tokens) / l.rps
	return false, wait
}

// IdentityFor resolves the bucket identity of a request: the configured key
// name matching X-API-KEY (fleet bots share one bucket), else the client IP.
func IdentityFor(keys map[string]string, remoteAddr string, apiKeyHeader string) string {
	if apiKeyHeader != "" {
		for name, token := range keys {
			if token != "" && token == apiKeyHeader {
				return "key:" + name
			}
		}
	}
	host := remoteAddr
	if host == "" {
		host = "unknown"
	}
	return "ip:" + host
}

// Middleware returns the Gin middleware enforcing the limiter.
func (l *RateLimiter) Middleware(keys map[string]string) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity := IdentityFor(keys, c.ClientIP(), c.GetHeader(rateLimitAPIKeyHeader))
		ok, wait := l.Allow(identity)
		if ok {
			c.Next()
			return
		}
		retryAfter := int(math.Ceil(wait))
		if retryAfter < 1 {
			retryAfter = 1
		}
		c.Header("Retry-After", strconv.Itoa(retryAfter))
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
			"code":    RateLimitErrorCode,
			"message": fmt.Sprintf("rate limit exceeded, retry after %ds", retryAfter),
			"data":    nil,
		})
	}
}
