// Package middleware provides HTTP middleware for authentication and authorization.
package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"

	"github.com/rantipay/trading-mcp/internal/edition"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

func newTestLogger() *logrus.Logger {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	return log
}

// okHandler is a trivial Gin handler that returns 200 OK.
func okHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// buildAuthRouter creates a gin router with AdminAuthRequired applied and a single GET /test route.
func buildAuthRouter(adminToken string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AdminAuthRequired(adminToken, newTestLogger()))
	r.GET("/test", okHandler)
	return r
}

// buildRateLimitRouter creates a gin router with RateLimitByToken(perMinute) applied.
func buildRateLimitRouter(perMinute int) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RateLimitByToken(perMinute, newTestLogger()))
	r.GET("/test", okHandler)
	return r
}

// ─── AdminAuth tests ─────────────────────────────────────────────────────────

// TestAdminAuth_NoToken_ReturnsUnauthorized verifies that a request without
// X-Admin-Token is rejected with 401 when a token is configured.
func TestAdminAuth_NoToken_ReturnsUnauthorized(t *testing.T) {
	t.Setenv("RANTIPAY_ENV", "production")

	router := buildAuthRouter("super-secret-token")

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	// Intentionally omitting the X-Admin-Token header.
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestAdminAuth_InvalidToken_ReturnsUnauthorized verifies that an incorrect
// token value is rejected with 401.
func TestAdminAuth_InvalidToken_ReturnsUnauthorized(t *testing.T) {
	t.Setenv("RANTIPAY_ENV", "production")

	router := buildAuthRouter("super-secret-token")

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Admin-Token", "wrong-token")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestAdminAuth_ValidToken_PassesThrough verifies that the correct token allows
// the request to reach the handler.
func TestAdminAuth_ValidToken_PassesThrough(t *testing.T) {
	t.Setenv("RANTIPAY_ENV", "production")

	const token = "super-secret-token"
	router := buildAuthRouter(token)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Admin-Token", token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// ─── RateLimit tests ─────────────────────────────────────────────────────────

// TestRateLimit_BelowLimit_Passes verifies that requests within the limit pass through.
func TestRateLimit_BelowLimit_Passes(t *testing.T) {
	// Allow 5 requests per minute.
	router := buildRateLimitRouter(5)

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("X-Admin-Token", "same-key")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "request %d should pass", i+1)
	}
}

// TestRateLimit_AboveLimit_Returns429 verifies that exceeding the per-minute limit
// returns 429 Too Many Requests.
func TestRateLimit_AboveLimit_Returns429(t *testing.T) {
	// Allow only 3 requests per minute.
	router := buildRateLimitRouter(3)

	const key = "limited-key"
	// First 3 should pass.
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("X-Admin-Token", key)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "request %d should pass", i+1)
	}

	// 4th request must be throttled.
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Admin-Token", key)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
}

// M1: in the product edition an empty ADMIN_API_TOKEN fails closed even
// outside RANTIPAY_ENV=production; the standard build keeps the dev fallback.
func TestAdminAuth_EmptyToken_ProductFailsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("RANTIPAY_ENV", "development")
	for _, tc := range []struct {
		product bool
		want    int
	}{{product: true, want: http.StatusUnauthorized}, {product: false, want: http.StatusOK}} {
		r := gin.New()
		r.Use(adminAuthRequired("", tc.product, newTestLogger()))
		r.GET("/test", okHandler)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/test", nil))
		assert.Equal(t, tc.want, w.Code, "product=%v", tc.product)
	}
}

// The exported constructor follows the compiled edition.
func TestAdminAuth_EmptyToken_FollowsEdition(t *testing.T) {
	t.Setenv("RANTIPAY_ENV", "development")
	w := httptest.NewRecorder()
	buildAuthRouter("").ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/test", nil))
	want := http.StatusOK
	if edition.IsProduct() {
		want = http.StatusUnauthorized
	}
	assert.Equal(t, want, w.Code)
}
