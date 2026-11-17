package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rantipay/trading-mcp/internal/database"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// createTenantTestToken creates a valid JWT token for testing
func createTenantTestToken(secret string, claims TenantJWTClaims) string {
	claims.RegisteredClaims = jwt.RegisteredClaims{
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedToken, _ := token.SignedString([]byte(secret))
	return signedToken
}

func TestTenantMiddleware_Handler_ValidToken(t *testing.T) {
	logger := logrus.New()
	secret := "test-secret-key"

	tenantID := uuid.New()
	userID := uuid.New()

	config := &TenantConfig{
		JWTSecret:     secret,
		RequireTenant: true,
		Logger:        logger,
	}

	middleware := NewTenantMiddleware(config, nil)

	router := gin.New()
	router.Use(middleware.Handler())
	router.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"tenant_id":  c.GetString("tenant_id"),
			"user_id":    c.GetString("user_id"),
			"user_level": c.GetInt("user_level"),
		})
	})

	// Create a valid token
	token := createTenantTestToken(secret, TenantJWTClaims{
		TenantID: tenantID.String(),
		UserID:   userID.String(),
		Level:    3,
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestTenantMiddleware_Handler_MissingToken(t *testing.T) {
	logger := logrus.New()

	config := &TenantConfig{
		JWTSecret:     "test-secret",
		RequireTenant: true,
		Logger:        logger,
	}

	middleware := NewTenantMiddleware(config, nil)

	router := gin.New()
	router.Use(middleware.Handler())
	router.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestTenantMiddleware_Handler_InvalidToken(t *testing.T) {
	logger := logrus.New()

	config := &TenantConfig{
		JWTSecret:     "test-secret",
		RequireTenant: true,
		Logger:        logger,
	}

	middleware := NewTenantMiddleware(config, nil)

	router := gin.New()
	router.Use(middleware.Handler())
	router.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestTenantMiddleware_Handler_SkipPaths(t *testing.T) {
	logger := logrus.New()

	config := &TenantConfig{
		JWTSecret:     "test-secret",
		RequireTenant: true,
		SkipPaths:     []string{"/health", "/metrics"},
		Logger:        logger,
	}

	middleware := NewTenantMiddleware(config, nil)

	router := gin.New()
	router.Use(middleware.Handler())
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestTenantMiddleware_Handler_HeaderOverride(t *testing.T) {
	logger := logrus.New()
	secret := "test-secret-key"

	jwtTenantID := uuid.New()
	overrideTenantID := uuid.New()
	userID := uuid.New()

	config := &TenantConfig{
		JWTSecret:           secret,
		RequireTenant:       true,
		AllowHeaderOverride: true,
		Logger:              logger,
	}

	middleware := NewTenantMiddleware(config, nil)

	router := gin.New()
	router.Use(middleware.Handler())
	router.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"tenant_id": c.GetString("tenant_id"),
		})
	})

	// Create a valid token with one tenant ID
	token := createTenantTestToken(secret, TenantJWTClaims{
		TenantID: jwtTenantID.String(),
		UserID:   userID.String(),
		Level:    2,
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Tenant-ID", overrideTenantID.String())

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	// When header override is enabled, X-Tenant-ID should take precedence
	assert.Contains(t, w.Body.String(), overrideTenantID.String())
}

func TestRequireTenant(t *testing.T) {
	logger := logrus.New()

	t.Run("with tenant", func(t *testing.T) {
		router := gin.New()
		router.Use(func(c *gin.Context) {
			c.Set("tenant_id", uuid.New().String())
			c.Next()
		})
		router.Use(RequireTenant(logger))
		router.GET("/test", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"status": "ok"})
		})

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("without tenant", func(t *testing.T) {
		router := gin.New()
		router.Use(RequireTenant(logger))
		router.GET("/test", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"status": "ok"})
		})

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})
}

func TestRequireUser(t *testing.T) {
	logger := logrus.New()

	t.Run("with user", func(t *testing.T) {
		router := gin.New()
		router.Use(func(c *gin.Context) {
			c.Set("user_id", uuid.New().String())
			c.Next()
		})
		router.Use(RequireUser(logger))
		router.GET("/test", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"status": "ok"})
		})

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("without user", func(t *testing.T) {
		router := gin.New()
		router.Use(RequireUser(logger))
		router.GET("/test", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"status": "ok"})
		})

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})
}

func TestRequireLevel(t *testing.T) {
	logger := logrus.New()

	testCases := []struct {
		name           string
		userLevel      int
		requiredLevel  int
		expectedStatus int
	}{
		{
			name:           "level meets requirement",
			userLevel:      3,
			requiredLevel:  2,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "level equals requirement",
			userLevel:      2,
			requiredLevel:  2,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "level below requirement",
			userLevel:      1,
			requiredLevel:  3,
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "admin level required",
			userLevel:      4,
			requiredLevel:  4,
			expectedStatus: http.StatusOK,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set("user_level", tc.userLevel)
				c.Next()
			})
			router.Use(RequireLevel(tc.requiredLevel, logger))
			router.GET("/test", func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{"status": "ok"})
			})

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.Equal(t, tc.expectedStatus, w.Code)
		})
	}
}

func TestGetTenantFromContext(t *testing.T) {
	expectedTenantID := uuid.New()

	router := gin.New()
	router.GET("/test", func(c *gin.Context) {
		c.Set("tenant_id", expectedTenantID.String())

		tenantID, err := GetTenantFromContext(c)
		require.NoError(t, err)
		assert.Equal(t, expectedTenantID, tenantID)

		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetUserFromContext(t *testing.T) {
	expectedUserID := uuid.New()

	router := gin.New()
	router.GET("/test", func(c *gin.Context) {
		c.Set("user_id", expectedUserID.String())

		userID, err := GetUserFromContext(c)
		require.NoError(t, err)
		assert.Equal(t, expectedUserID, userID)

		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetUserLevelFromContext(t *testing.T) {
	expectedLevel := 3

	router := gin.New()
	router.GET("/test", func(c *gin.Context) {
		c.Set("user_level", expectedLevel)

		level := GetUserLevelFromContext(c)
		assert.Equal(t, expectedLevel, level)

		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestIsAdminFromContext(t *testing.T) {
	t.Run("admin user", func(t *testing.T) {
		router := gin.New()
		router.GET("/test", func(c *gin.Context) {
			c.Set("is_admin", true)

			isAdmin := IsAdminFromContext(c)
			assert.True(t, isAdmin)

			c.JSON(http.StatusOK, gin.H{"status": "ok"})
		})

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("non-admin user", func(t *testing.T) {
		router := gin.New()
		router.GET("/test", func(c *gin.Context) {
			c.Set("is_admin", false)

			isAdmin := IsAdminFromContext(c)
			assert.False(t, isAdmin)

			c.JSON(http.StatusOK, gin.H{"status": "ok"})
		})

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestTenantContext_Validate(t *testing.T) {
	t.Run("valid context", func(t *testing.T) {
		tc := &database.TenantContext{
			TenantID:  uuid.New(),
			UserID:    uuid.New(),
			UserLevel: 2,
		}
		err := tc.Validate()
		assert.NoError(t, err)
	})

	t.Run("missing tenant_id", func(t *testing.T) {
		tc := &database.TenantContext{
			UserID:    uuid.New(),
			UserLevel: 2,
		}
		err := tc.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "tenant_id")
	})

	t.Run("missing user_id", func(t *testing.T) {
		tc := &database.TenantContext{
			TenantID:  uuid.New(),
			UserLevel: 2,
		}
		err := tc.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "user_id")
	})
}

func TestTenantAuditMiddleware(t *testing.T) {
	logger := logrus.New()

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("tenant_id", uuid.New().String())
		c.Set("user_id", uuid.New().String())
		c.Set("user_level", 2)
		c.Next()
	})
	router.Use(TenantAuditMiddleware(logger))
	router.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}
