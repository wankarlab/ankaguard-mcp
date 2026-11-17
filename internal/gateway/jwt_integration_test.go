package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestGatewayJWTIntegration(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	jwtSecret := "test-secret-key-for-jwt-validation"

	// Create gateway config with JWT enabled
	config := &GatewayConfig{
		Port:           9090,
		MCPServerURL:   "http://localhost:10600",
		ScalpingAPIURL: "http://localhost:8081",
		EnableCORS:     true,
		EnableJWTAuth:  true,
		JWTSecret:      jwtSecret,
	}

	gateway, err := NewGateway(config, logger)
	assert.NoError(t, err)
	assert.NotNil(t, gateway)

	t.Run("Protected route without token returns 401", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/mcp/test", nil)
		w := httptest.NewRecorder()

		gateway.router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("Protected route with invalid token returns 401", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/mcp/test", nil)
		req.Header.Set("Authorization", "Bearer invalid-token")
		w := httptest.NewRecorder()

		gateway.router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("Protected route with valid token passes middleware", func(t *testing.T) {
		// Create valid JWT token
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"user_id":    "test-user-123",
			"level":      2.0,
			"tenant_id":  "test-tenant",
			"company_id": "test-company",
			"exp":        time.Now().Add(time.Hour).Unix(),
		})

		tokenString, err := token.SignedString([]byte(jwtSecret))
		assert.NoError(t, err)

		req := httptest.NewRequest("GET", "/api/mcp/test", nil)
		req.Header.Set("Authorization", "Bearer "+tokenString)
		w := httptest.NewRecorder()

		gateway.router.ServeHTTP(w, req)

		// Should not be 401 (will be 502 because backend is not running, but that's OK)
		assert.NotEqual(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("Protected route with insufficient level returns 403", func(t *testing.T) {
		// Create JWT token with level 1 (Basic)
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"user_id":    "test-user-123",
			"level":      1.0, // Basic level
			"tenant_id":  "test-tenant",
			"company_id": "test-company",
			"exp":        time.Now().Add(time.Hour).Unix(),
		})

		tokenString, err := token.SignedString([]byte(jwtSecret))
		assert.NoError(t, err)

		// Try to access orders endpoint (requires level 3)
		req := httptest.NewRequest("GET", "/api/mcp/orders", nil)
		req.Header.Set("Authorization", "Bearer "+tokenString)
		w := httptest.NewRecorder()

		gateway.router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("Health endpoint is public (no JWT required)", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/health", nil)
		w := httptest.NewRecorder()

		gateway.router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("Gateway info, build info and metrics require a JWT (task 6.8)", func(t *testing.T) {
		for _, path := range []string{"/api/gateway/info", "/info", "/api/info", "/metrics"} {
			req := httptest.NewRequest("GET", path, nil)
			w := httptest.NewRecorder()

			gateway.router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusUnauthorized, w.Code, path)
		}
	})

	t.Run("WebSocket upgrade without a token is rejected", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/ws", nil)
		w := httptest.NewRecorder()

		gateway.router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("WebSocket upgrade with a garbage token is rejected", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/ws?token=not-a-jwt", nil)
		w := httptest.NewRecorder()

		gateway.router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

func TestGatewayJWTDisabled(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	// Create gateway config with JWT disabled
	config := &GatewayConfig{
		Port:           9090,
		MCPServerURL:   "http://localhost:10600",
		ScalpingAPIURL: "http://localhost:8081",
		EnableCORS:     true,
		EnableJWTAuth:  false, // JWT disabled
		JWTSecret:      "",
	}

	gateway, err := NewGateway(config, logger)
	assert.NoError(t, err)
	assert.NotNil(t, gateway)

	t.Run("Protected route without token passes when JWT disabled", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/mcp/test", nil)
		w := httptest.NewRecorder()

		gateway.router.ServeHTTP(w, req)

		// Should not be 401 (will be 502 because backend is not running, but that's OK)
		assert.NotEqual(t, http.StatusUnauthorized, w.Code)
	})
}
