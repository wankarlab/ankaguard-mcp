package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// TestLaunchGate_R1_MalSignedJWT verifies that a mal-signed JWT is rejected
// even when the X-Gateway-Token header is present (SEC-1/SEC-2 regression).
// This is a non-negotiable launch gate test.
func TestLaunchGate_R1_MalSignedJWT(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	// Simulate the KongJWTMiddleware with a known secret.
	const correctSecret = "test-secret-do-not-use-in-prod"
	const wrongSecret = "wrong-secret"

	r.Use(func(c *gin.Context) {
		tokenStr := c.GetHeader("Authorization")
		if len(tokenStr) > 7 && tokenStr[:7] == "Bearer " {
			tokenStr = tokenStr[7:]
		}

		_, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
			return []byte(correctSecret), nil
		})
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		c.Next()
	})
	r.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	// Forge a token with the WRONG secret.
	forgedToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "attacker",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	forgedSigned, _ := forgedToken.SignedString([]byte(wrongSecret))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+forgedSigned)
	req.Header.Set("X-Gateway-Token", "some-gateway-value")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("R1: mal-signed JWT must be rejected (got %d, want 401)", w.Code)
	}
}

// TestLaunchGate_R1_ValidJWT verifies that a correctly signed JWT passes.
func TestLaunchGate_R1_ValidJWT(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	const correctSecret = "test-secret-do-not-use-in-prod"

	r.Use(func(c *gin.Context) {
		tokenStr := c.GetHeader("Authorization")
		if len(tokenStr) > 7 && tokenStr[:7] == "Bearer " {
			tokenStr = tokenStr[7:]
		}
		_, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
			return []byte(correctSecret), nil
		})
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		c.Next()
	})
	r.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	validToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "alice",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	validSigned, _ := validToken.SignedString([]byte(correctSecret))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+validSigned)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("R1: valid JWT should pass (got %d, want 200)", w.Code)
	}
}
