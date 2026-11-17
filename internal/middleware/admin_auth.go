// Package middleware provides HTTP middleware for authentication and authorization.
package middleware

import (
	"crypto/subtle"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/edition"
)

// AdminAuthRequired returns a Gin middleware that validates the X-Admin-Token header
// against the configured adminToken using constant-time comparison to prevent timing attacks.
//
// Empty adminToken behavior:
//   - Product edition (build tag product): always reject with 401 (fail-closed).
//   - Standard build, non-production (RANTIPAY_ENV != "production"): allow all
//     requests with a WARN log (dev fallback).
//   - Standard build, production: reject all requests with 401 (fail-closed).
func AdminAuthRequired(adminToken string, log *logrus.Logger) gin.HandlerFunc {
	return adminAuthRequired(adminToken, edition.IsProduct(), log)
}

// adminAuthRequired is AdminAuthRequired with the edition made explicit, so
// the product rule can be tested under any build tag.
func adminAuthRequired(adminToken string, product bool, log *logrus.Logger) gin.HandlerFunc {
	warnedOnce := &sync.Once{}

	return func(c *gin.Context) {
		if adminToken == "" {
			if !product && os.Getenv("RANTIPAY_ENV") != "production" {
				warnedOnce.Do(func() {
					log.Warn("ADMIN_API_TOKEN is not set — admin endpoints are open (dev fallback). Set RANTIPAY_ENV=production to enforce auth.")
				})
				c.Next()
				return
			}
			// Production with no token configured: fail-closed.
			log.WithFields(logrus.Fields{
				"ip": c.ClientIP(),
				"ua": c.Request.UserAgent(),
			}).Warn("admin auth rejected: ADMIN_API_TOKEN not configured (production or product edition)")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}

		token := c.GetHeader("X-Admin-Token")
		if subtle.ConstantTimeCompare([]byte(token), []byte(adminToken)) != 1 {
			log.WithFields(logrus.Fields{
				"ip": c.ClientIP(),
				"ua": c.Request.UserAgent(),
			}).Warn("admin auth rejected: invalid X-Admin-Token")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}

		c.Next()
	}
}

// tokenBucket tracks request count and the last reset time for a single key.
type tokenBucket struct {
	count   int
	resetAt time.Time
}

// rateLimiter holds per-key buckets and cleans stale entries on each reset cycle.
type rateLimiter struct {
	mu        sync.Mutex
	buckets   map[string]*tokenBucket
	perMinute int
	log       *logrus.Logger
}

func newRateLimiter(perMinute int, log *logrus.Logger) *rateLimiter {
	return &rateLimiter{
		buckets:   make(map[string]*tokenBucket),
		perMinute: perMinute,
		log:       log,
	}
}

// allow returns (allowed bool, retryAfterSeconds int).
func (r *rateLimiter) allow(key string) (bool, int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	bucket, ok := r.buckets[key]
	if !ok || now.After(bucket.resetAt) {
		// Purge stale entries on a reset to prevent unbounded map growth.
		if len(r.buckets) > 10000 {
			for k, b := range r.buckets {
				if now.After(b.resetAt) {
					delete(r.buckets, k)
				}
			}
		}
		r.buckets[key] = &tokenBucket{count: 1, resetAt: now.Add(time.Minute)}
		return true, 0
	}

	bucket.count++
	if bucket.count > r.perMinute {
		retryAfter := int(time.Until(bucket.resetAt).Seconds()) + 1
		return false, retryAfter
	}
	return true, 0
}

// RateLimitByToken returns a Gin middleware that enforces a per-minute rate limit.
// The rate-limit key is the X-Admin-Token header value; falls back to client IP when
// the header is absent.
func RateLimitByToken(perMinute int, log *logrus.Logger) gin.HandlerFunc {
	rl := newRateLimiter(perMinute, log)

	return func(c *gin.Context) {
		key := c.GetHeader("X-Admin-Token")
		if key == "" {
			key = c.ClientIP()
		}

		allowed, retryAfter := rl.allow(key)
		if !allowed {
			log.WithFields(logrus.Fields{
				"key":                 key,
				"retry_after_seconds": retryAfter,
			}).Warn("rate limit exceeded on admin endpoint")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":               "rate_limit_exceeded",
				"retry_after_seconds": retryAfter,
			})
			return
		}

		c.Next()
	}
}

// RateLimitConfig returns the rate-per-minute for a given HTTP method and path combination.
// Used to build per-route rate limits on admin/config endpoints.
//
// Rules:
//   - GET  any path         → 0 (no limit)
//   - PUT  any path         → 60 req/min
//   - POST /bulk            → 10 req/min
//   - POST /presets/*/apply → 5  req/min
//   - Default               → 30 req/min
func RateLimitConfig(method, path string) int {
	switch method {
	case http.MethodGet:
		return 0
	case http.MethodPut:
		return 60
	case http.MethodPost:
		// Check for bulk operations.
		if len(path) >= 5 && path[len(path)-5:] == "/bulk" {
			return 10
		}
		// Check for preset apply (e.g. /presets/{name}/apply).
		if len(path) >= 6 && path[len(path)-6:] == "/apply" {
			return 5
		}
		return 30
	default:
		return 30
	}
}
