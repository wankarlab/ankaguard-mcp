package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestKongJWTMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := logrus.New()
	secret := "test-secret-key"

	t.Run("Missing Authorization header", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)

		middleware := KongJWTMiddleware(secret, "", logger)
		middleware(c)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("Invalid Authorization format", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)
		c.Request.Header.Set("Authorization", "InvalidFormat")

		middleware := KongJWTMiddleware(secret, "", logger)
		middleware(c)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("Valid JWT token", func(t *testing.T) {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, &KongJWTClaims{
			UserID:   "test-user-123",
			Level:    2,
			TenantID: "tenant-123",
		})
		tokenString, _ := token.SignedString([]byte(secret))

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)
		c.Request.Header.Set("Authorization", "Bearer "+tokenString)

		middleware := KongJWTMiddleware(secret, "", logger)
		middleware(c)

		userID, exists := GetUserIDFromContext(c)
		assert.True(t, exists)
		assert.Equal(t, "test-user-123", userID)

		level, exists := GetKongLevelFromContext(c)
		assert.True(t, exists)
		assert.Equal(t, 2, level)
	})

	t.Run("Insufficient permissions", func(t *testing.T) {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, &KongJWTClaims{
			UserID: "test-user-123",
			Level:  1,
		})
		tokenString, _ := token.SignedString([]byte(secret))

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/orders/create", nil)
		c.Request.Header.Set("Authorization", "Bearer "+tokenString)

		middleware := KongJWTMiddleware(secret, "", logger)
		middleware(c)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})
}

// TestKongJWTMiddleware_GatewayToken is the regression test for the hardcoded
// "internal-gateway-token" bypass (identity spoof via forged JWT). The
// internal-gateway trust path may skip JWT signature verification, but only when
// the X-Gateway-Token matches the CONFIGURED secret, and never when no token is
// configured (fail closed).
func TestKongJWTMiddleware_GatewayToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := logrus.New()
	const serverSecret = "server-secret"
	const gatewaySecret = "s3cr3t-internal-gateway-token"

	// A token signed with a DIFFERENT secret (as the MATP auth service would, or
	// as an attacker forging admin claims). validateJWT must reject it; the
	// gateway trust path accepts it only when the gateway token matches.
	forged := jwt.NewWithClaims(jwt.SigningMethodHS256, &KongJWTClaims{
		UserID:    "spoofed-admin",
		UserLevel: 4,
	})
	forgedString, _ := forged.SignedString([]byte("some-other-secret"))

	t.Run("trusted gateway token accepts cross-signed JWT", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)
		c.Request.Header.Set("Authorization", "Bearer "+forgedString)
		c.Request.Header.Set("X-Gateway-Token", gatewaySecret)

		KongJWTMiddleware(serverSecret, gatewaySecret, logger)(c)

		userID, exists := GetUserIDFromContext(c)
		assert.True(t, exists)
		assert.Equal(t, "spoofed-admin", userID)
	})

	t.Run("old literal token is rejected", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)
		c.Request.Header.Set("Authorization", "Bearer "+forgedString)
		c.Request.Header.Set("X-Gateway-Token", "internal-gateway-token") // the OLD git literal

		KongJWTMiddleware(serverSecret, gatewaySecret, logger)(c)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("fail closed: no gateway token configured ignores header", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)
		c.Request.Header.Set("Authorization", "Bearer "+forgedString)
		c.Request.Header.Set("X-Gateway-Token", "internal-gateway-token")

		KongJWTMiddleware(serverSecret, "", logger)(c) // empty configured token

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}
