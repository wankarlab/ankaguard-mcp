// Package middleware provides HTTP middleware for authentication, rate limiting, and more.
package middleware

// RateLimiter implements per-user token-bucket rate limiting.
// It uses Valkey as primary backend (atomic Lua script) with an
// in-memory fallback (golang.org/x/time/rate) when Valkey is unavailable.

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"crypto/sha256"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"github.com/valkey-io/valkey-go"
	"golang.org/x/time/rate"
)

// Quota defines the token-bucket parameters for a single user/endpoint combination.
type Quota struct {
	RequestsPerMinute int
	BurstSize         int
}

// defaultQuota is used when no QuotaResolver is configured.
var defaultQuota = Quota{
	RequestsPerMinute: 30,
	BurstSize:         5,
}

// QuotaResolver resolves the applicable quota for a given user.
type QuotaResolver interface {
	GetQuota(userID string) Quota
}

// RateLimiter is the top-level interface for per-user rate limiting.
type RateLimiter interface {
	// Allow checks whether the request is within quota.
	// Returns (true, 0, nil) on success.
	// Returns (false, retryAfter, nil) when the bucket is empty.
	Allow(ctx context.Context, userID, endpoint string) (allowed bool, retryAfter time.Duration, err error)
}

// =============================================================================
// Valkey-backed rate limiter
// =============================================================================

// valkeyRateLimiter uses a Lua atomic token-bucket script stored in Valkey.
type valkeyRateLimiter struct {
	client valkey.Client
	logger *logrus.Logger
}

// NewValkeyRateLimiter creates a rate limiter backed by Valkey.
func NewValkeyRateLimiter(client valkey.Client, logger *logrus.Logger) RateLimiter {
	if logger == nil {
		logger = logrus.New()
	}
	return &valkeyRateLimiter{client: client, logger: logger}
}

// tokenBucketLua is an atomic Lua script that implements a token-bucket in Valkey.
// KEYS[1] = bucket key
// ARGV[1] = capacity (burst size)
// ARGV[2] = refill rate  (tokens per second, float)
// ARGV[3] = current unix timestamp (float seconds)
// Returns {allowed(0|1), retry_after_ms}
const tokenBucketLua = `
local key        = KEYS[1]
local capacity   = tonumber(ARGV[1])
local rate_per_s = tonumber(ARGV[2])
local now        = tonumber(ARGV[3])

local data = redis.call("HMGET", key, "tokens", "ts")
local tokens = tonumber(data[1]) or capacity
local ts     = tonumber(data[2]) or now

-- refill since last call
local elapsed = math.max(0, now - ts)
tokens = math.min(capacity, tokens + elapsed * rate_per_s)

if tokens >= 1 then
    tokens = tokens - 1
    redis.call("HMSET", key, "tokens", tokens, "ts", now)
    redis.call("PEXPIRE", key, math.ceil(capacity / rate_per_s * 1000) + 5000)
    return {1, 0}
else
    local retry_ms = math.ceil((1 - tokens) / rate_per_s * 1000)
    redis.call("HMSET", key, "tokens", tokens, "ts", now)
    redis.call("PEXPIRE", key, math.ceil(capacity / rate_per_s * 1000) + 5000)
    return {0, retry_ms}
end
`

func (v *valkeyRateLimiter) Allow(ctx context.Context, userID, endpoint string) (bool, time.Duration, error) {
	key := fmt.Sprintf("rl:%s:%s", endpoint, userID)
	capacity := defaultQuota.BurstSize
	ratePerSec := float64(defaultQuota.RequestsPerMinute) / 60.0
	now := float64(time.Now().UnixNano()) / 1e9

	cmd := v.client.B().Eval().
		Script(tokenBucketLua).
		Numkeys(1).
		Key(key).
		Arg(
			fmt.Sprintf("%d", capacity),
			fmt.Sprintf("%.6f", ratePerSec),
			fmt.Sprintf("%.6f", now),
		).Build()

	resp := v.client.Do(ctx, cmd)
	if err := resp.Error(); err != nil {
		return false, 0, fmt.Errorf("valkey eval error: %w", err)
	}

	arr, err := resp.ToArray()
	if err != nil || len(arr) < 2 {
		return false, 0, fmt.Errorf("unexpected valkey response")
	}

	allowed, err := arr[0].AsInt64()
	if err != nil {
		return false, 0, err
	}
	retryMs, err := arr[1].AsInt64()
	if err != nil {
		return false, 0, err
	}

	if allowed == 1 {
		return true, 0, nil
	}
	return false, time.Duration(retryMs) * time.Millisecond, nil
}

// =============================================================================
// In-memory rate limiter (golang.org/x/time/rate)
// =============================================================================

// inMemoryEntry holds a limiter and a last-seen timestamp for GC.
type inMemoryEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// InMemoryRateLimiter uses a sync.Map of *rate.Limiter instances per key,
// with a background goroutine that prunes stale entries.
type InMemoryRateLimiter struct {
	mu      sync.Mutex
	entries map[string]*inMemoryEntry
	quota   Quota
	stopGC  chan struct{}
}

// NewInMemoryRateLimiter creates an in-memory rate limiter with the given quota.
// A GC goroutine prunes entries that have been idle for more than 5× the bucket
// refill window.
func NewInMemoryRateLimiter(quota Quota) *InMemoryRateLimiter {
	rl := &InMemoryRateLimiter{
		entries: make(map[string]*inMemoryEntry),
		quota:   quota,
		stopGC:  make(chan struct{}),
	}
	go rl.gcLoop()
	return rl
}

// Stop shuts down the background GC goroutine.
func (im *InMemoryRateLimiter) Stop() {
	close(im.stopGC)
}

func (im *InMemoryRateLimiter) gcLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-im.stopGC:
			return
		case <-ticker.C:
			im.pruneStale()
		}
	}
}

func (im *InMemoryRateLimiter) pruneStale() {
	threshold := time.Now().Add(-5 * time.Minute)
	im.mu.Lock()
	defer im.mu.Unlock()
	for k, e := range im.entries {
		if e.lastSeen.Before(threshold) {
			delete(im.entries, k)
		}
	}
}

func (im *InMemoryRateLimiter) getLimiter(key string) *rate.Limiter {
	im.mu.Lock()
	defer im.mu.Unlock()
	e, ok := im.entries[key]
	if !ok {
		rps := rate.Limit(float64(im.quota.RequestsPerMinute) / 60.0)
		e = &inMemoryEntry{
			limiter: rate.NewLimiter(rps, im.quota.BurstSize),
		}
		im.entries[key] = e
	}
	e.lastSeen = time.Now()
	return e.limiter
}

// Allow implements RateLimiter using in-memory token buckets.
// It does not block; it simply checks if a token is available right now.
func (im *InMemoryRateLimiter) Allow(_ context.Context, userID, endpoint string) (bool, time.Duration, error) {
	key := fmt.Sprintf("rl:%s:%s", endpoint, userID)
	lim := im.getLimiter(key)

	// Reserve without blocking to get an honest answer.
	r := lim.Reserve()
	delay := r.Delay()
	if delay == 0 {
		// Token available immediately
		return true, 0, nil
	}
	// No token available right now — cancel the reservation and return denied.
	r.Cancel()
	return false, delay, nil
}

// =============================================================================
// Fallback rate limiter: tries Valkey first, falls back to in-memory
// =============================================================================

// FallbackRateLimiter tries Valkey first. On any Valkey error it transparently
// falls back to the in-memory implementation.
type FallbackRateLimiter struct {
	primary  RateLimiter
	fallback *InMemoryRateLimiter
	logger   *logrus.Logger
}

// NewFallbackRateLimiter creates a FallbackRateLimiter.
func NewFallbackRateLimiter(primary RateLimiter, fallback *InMemoryRateLimiter, logger *logrus.Logger) *FallbackRateLimiter {
	if logger == nil {
		logger = logrus.New()
	}
	return &FallbackRateLimiter{
		primary:  primary,
		fallback: fallback,
		logger:   logger,
	}
}

// Allow tries the primary (Valkey) limiter; on error it uses the in-memory fallback.
func (f *FallbackRateLimiter) Allow(ctx context.Context, userID, endpoint string) (bool, time.Duration, error) {
	allowed, retryAfter, err := f.primary.Allow(ctx, userID, endpoint)
	if err != nil {
		f.logger.WithError(err).Warn("Valkey rate limiter error — using in-memory fallback")
		return f.fallback.Allow(ctx, userID, endpoint)
	}
	return allowed, retryAfter, nil
}

// =============================================================================
// Prometheus counter for rate-limit hits (H0.1.6)
// =============================================================================

// rateLimitHitTotal is incremented each time a request is denied.
// Declared here so the middleware and the counter are co-located.
// The promauto registration happens in metrics_middleware_additions.go to keep
// the counter in the same package while avoiding a re-registration panic.

// =============================================================================
// Gin middleware
// =============================================================================

// RateLimitMiddleware returns a Gin handler that enforces per-user rate limits.
//
//   - userID is read from the "user_id" key set by AuthMiddleware / KongJWTMiddleware.
//   - If the limiter returns allowed=false the handler responds 429 with a
//     Retry-After header and aborts the chain.
//   - When userID cannot be resolved (unauthenticated pass-through) the request
//     is allowed without rate-checking.
func RateLimitMiddleware(rl RateLimiter, qr QuotaResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, exists := c.Get("user_id")
		if !exists || userID == "" {
			// Cannot identify user — let auth middleware decide fate
			c.Next()
			return
		}

		uid, ok := userID.(string)
		if !ok || uid == "" {
			c.Next()
			return
		}

		endpoint := c.FullPath()
		if endpoint == "" {
			endpoint = c.Request.URL.Path
		}

		allowed, retryAfter, err := rl.Allow(c.Request.Context(), uid, endpoint)
		if err != nil {
			// On limiter error, log and allow the request (fail-open)
			logrus.WithError(err).Warn("rate limiter error — failing open")
			c.Next()
			return
		}

		if !allowed {
			// Record Prometheus metric
			hash := fmt.Sprintf("%x", sha256.Sum256([]byte(uid)))[:16]
			recordRateLimitHit(hash, endpoint)

			retrySeconds := int(retryAfter.Seconds())
			if retrySeconds < 1 {
				retrySeconds = 1
			}
			c.Header("Retry-After", fmt.Sprintf("%d", retrySeconds))
			c.JSON(http.StatusTooManyRequests, gin.H{
				"success": false,
				"error": gin.H{
					"code":        "RATE_LIMIT_EXCEEDED",
					"message":     "Too many requests — please slow down",
					"retry_after": retrySeconds,
				},
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// recordRateLimitHit increments the rate_limit_hit_total counter.
// The actual counter variable is defined in metrics_middleware_additions.go.
func recordRateLimitHit(userIDHash, endpoint string) {
	rateLimitHitTotal.WithLabelValues(userIDHash, endpoint).Inc()
}
