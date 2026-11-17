package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestAuthMiddleware_Handler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	secret := "test-secret-key"

	tests := []struct {
		name           string
		config         *AuthConfig
		setupRequest   func(*http.Request)
		expectedStatus int
		expectedBody   string
	}{
		{
			name: "auth disabled - should pass",
			config: &AuthConfig{
				Enabled:   false,
				JWTSecret: secret,
				Logger:    logger,
			},
			setupRequest:   func(r *http.Request) {},
			expectedStatus: http.StatusOK,
		},
		{
			name: "skip path - should pass",
			config: &AuthConfig{
				Enabled:   true,
				JWTSecret: secret,
				SkipPaths: []string{"/health", "/skip"},
				Logger:    logger,
			},
			setupRequest:   func(r *http.Request) {},
			expectedStatus: http.StatusOK,
		},
		{
			name: "missing auth header - should fail",
			config: &AuthConfig{
				Enabled:   true,
				JWTSecret: secret,
				SkipPaths: []string{},
				Logger:    logger,
			},
			setupRequest:   func(r *http.Request) {},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name: "invalid auth format - should fail",
			config: &AuthConfig{
				Enabled:   true,
				JWTSecret: secret,
				SkipPaths: []string{},
				Logger:    logger,
			},
			setupRequest: func(r *http.Request) {
				r.Header.Set("Authorization", "InvalidFormat")
			},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name: "valid token - should pass",
			config: &AuthConfig{
				Enabled:   true,
				JWTSecret: secret,
				SkipPaths: []string{},
				Logger:    logger,
			},
			setupRequest: func(r *http.Request) {
				token := createTestToken(secret, "user123", "tenant456", 2)
				r.Header.Set("Authorization", "Bearer "+token)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "internal gateway bypass - should pass",
			config: &AuthConfig{
				Enabled:              true,
				JWTSecret:            secret,
				SkipPaths:            []string{},
				AllowInternalGateway: true,
				InternalGatewayToken: "gateway-token",
				Logger:               logger,
			},
			setupRequest: func(r *http.Request) {
				r.Header.Set("X-Internal-Gateway", "true")
				r.Header.Set("X-Gateway-Token", "gateway-token")
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "insufficient level - should fail",
			config: &AuthConfig{
				Enabled:      true,
				JWTSecret:    secret,
				SkipPaths:    []string{},
				RequireLevel: 3,
				Logger:       logger,
			},
			setupRequest: func(r *http.Request) {
				token := createTestToken(secret, "user123", "tenant456", 2)
				r.Header.Set("Authorization", "Bearer "+token)
			},
			expectedStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := gin.New()
			middleware := NewAuthMiddleware(tt.config)
			router.Use(middleware.Handler())
			router.GET("/health", func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{"status": "ok"})
			})
			router.GET("/skip", func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{"status": "ok"})
			})
			router.GET("/test", func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{"status": "ok"})
			})

			w := httptest.NewRecorder()
			// Use /skip for skip path test, /test for others
			path := "/test"
			if tt.name == "skip path - should pass" {
				path = "/skip"
			}
			req, _ := http.NewRequest("GET", path, nil)
			tt.setupRequest(req)

			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
		})
	}
}

func TestAuthMiddleware_RequireLevel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	secret := "test-secret-key"

	tests := []struct {
		name           string
		userLevel      int
		requiredLevel  int
		expectedStatus int
	}{
		{
			name:           "level 3 user accessing level 2 endpoint - should pass",
			userLevel:      3,
			requiredLevel:  2,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "level 2 user accessing level 3 endpoint - should fail",
			userLevel:      2,
			requiredLevel:  3,
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "level 4 user accessing level 4 endpoint - should pass",
			userLevel:      4,
			requiredLevel:  4,
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &AuthConfig{
				Enabled:   true,
				JWTSecret: secret,
				SkipPaths: []string{},
				Logger:    logger,
			}

			router := gin.New()
			middleware := NewAuthMiddleware(config)
			router.Use(middleware.Handler())
			router.GET("/protected", middleware.RequireLevel(tt.requiredLevel), func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{"status": "ok"})
			})

			w := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", "/protected", nil)
			token := createTestToken(secret, "user123", "tenant456", tt.userLevel)
			req.Header.Set("Authorization", "Bearer "+token)

			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
		})
	}
}

func TestAuthMiddleware_OptionalHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	secret := "test-secret-key"

	config := &AuthConfig{
		Enabled:   true,
		JWTSecret: secret,
		Logger:    logger,
	}

	middleware := NewAuthMiddleware(config)

	t.Run("with valid token - should set context", func(t *testing.T) {
		router := gin.New()
		router.Use(middleware.OptionalHandler())
		router.GET("/test", func(c *gin.Context) {
			userID, exists := AuthGetUserID(c)
			if exists {
				c.JSON(http.StatusOK, gin.H{"user_id": userID})
			} else {
				c.JSON(http.StatusOK, gin.H{"user_id": nil})
			}
		})

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test", nil)
		token := createTestToken(secret, "user123", "tenant456", 2)
		req.Header.Set("Authorization", "Bearer "+token)

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "user123")
	})

	t.Run("without token - should pass without context", func(t *testing.T) {
		router := gin.New()
		router.Use(middleware.OptionalHandler())
		router.GET("/test", func(c *gin.Context) {
			_, exists := AuthGetUserID(c)
			c.JSON(http.StatusOK, gin.H{"authenticated": exists})
		})

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test", nil)

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "false")
	})
}

func TestContextHelpers(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("AuthGetUserID", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Set("user_id", "user123")

		userID, exists := AuthGetUserID(c)
		assert.True(t, exists)
		assert.Equal(t, "user123", userID)
	})

	t.Run("AuthGetUserLevel", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Set("user_level", 3)

		level, exists := AuthGetUserLevel(c)
		assert.True(t, exists)
		assert.Equal(t, 3, level)
	})

	t.Run("AuthIsAuthenticated", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		assert.False(t, AuthIsAuthenticated(c))

		c.Set("user_id", "user123")
		assert.True(t, AuthIsAuthenticated(c))
	})

	t.Run("AuthHasMinimumLevel", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Set("user_level", 3)

		assert.True(t, AuthHasMinimumLevel(c, 2))
		assert.True(t, AuthHasMinimumLevel(c, 3))
		assert.False(t, AuthHasMinimumLevel(c, 4))
	})
}

// Helper function to create test JWT tokens
func createTestToken(secret, userID, tenantID string, level int) string {
	claims := &JWTClaims{
		UserID:   userID,
		TenantID: tenantID,
		Level:    float64(level),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, _ := token.SignedString([]byte(secret))
	return tokenString
}

// =============================================================================
// OAuth Tests
// =============================================================================

func TestOAuthService_GetSupportedProviders(t *testing.T) {
	config := &OAuthConfig{
		AuthServiceURL: "http://localhost:10101",
		SupportedProviders: []OAuthProvider{
			OAuthProviderGoogle,
			OAuthProviderTikTok,
		},
	}

	service := NewOAuthService(config)
	providers := service.GetSupportedProviders()

	assert.Len(t, providers, 2)
	assert.Contains(t, providers, OAuthProviderGoogle)
	assert.Contains(t, providers, OAuthProviderTikTok)
}

func TestOAuthService_IsProviderSupported(t *testing.T) {
	config := &OAuthConfig{
		AuthServiceURL: "http://localhost:10101",
		SupportedProviders: []OAuthProvider{
			OAuthProviderGoogle,
		},
	}

	service := NewOAuthService(config)

	assert.True(t, service.isProviderSupported(OAuthProviderGoogle))
	assert.False(t, service.isProviderSupported(OAuthProviderTikTok))
	assert.False(t, service.isProviderSupported(OAuthProviderApple))
}

func TestDefaultOAuthConfig(t *testing.T) {
	config := DefaultOAuthConfig()

	assert.Equal(t, "http://localhost:10100", config.AuthServiceURL)
	assert.Len(t, config.SupportedProviders, 1)
	assert.Contains(t, config.SupportedProviders, OAuthProviderGoogle)
}

func TestOAuthHandlers_HandleGetProviders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	config := &OAuthConfig{
		AuthServiceURL: "http://localhost:10101",
		SupportedProviders: []OAuthProvider{
			OAuthProviderGoogle,
		},
	}

	service := NewOAuthService(config)
	handlers := NewOAuthHandlers(service)

	router := gin.New()
	handlers.RegisterOAuthRoutes(router.Group("/api/v1"))

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/auth/providers", nil)

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.True(t, response["success"].(bool))

	providers := response["providers"].([]interface{})
	assert.Len(t, providers, 1)

	googleProvider := providers[0].(map[string]interface{})
	assert.Equal(t, "google", googleProvider["id"])
	assert.Equal(t, "Google", googleProvider["name"])
}

func TestGetProviderDisplayName(t *testing.T) {
	tests := []struct {
		provider OAuthProvider
		expected string
	}{
		{OAuthProviderGoogle, "Google"},
		{OAuthProviderTikTok, "TikTok"},
		{OAuthProviderApple, "Apple"},
		{OAuthProvider("unknown"), "unknown"},
	}

	for _, tt := range tests {
		t.Run(string(tt.provider), func(t *testing.T) {
			result := getProviderDisplayName(tt.provider)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetProviderIcon(t *testing.T) {
	tests := []struct {
		provider OAuthProvider
		expected string
	}{
		{OAuthProviderGoogle, "google"},
		{OAuthProviderTikTok, "tiktok"},
		{OAuthProviderApple, "apple"},
		{OAuthProvider("unknown"), "default"},
	}

	for _, tt := range tests {
		t.Run(string(tt.provider), func(t *testing.T) {
			result := getProviderIcon(tt.provider)
			assert.Equal(t, tt.expected, result)
		})
	}
}
