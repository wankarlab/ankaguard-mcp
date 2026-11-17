package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

// Property Test 2: Validación de Acceso por Kong Auth Level
// Validates: Requirements 2.3 - Kong Auth Service level-based access control
func TestProperty_KongAuthLevelValidation(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	gin.SetMode(gin.TestMode)

	middleware := NewKongAuthMiddleware(logger)

	testCases := []struct {
		name           string
		userLevel      int
		requiredLevel  int
		expectedStatus int
		description    string
	}{
		{
			name:           "Level1_CanAccessLevel1",
			userLevel:      1,
			requiredLevel:  1,
			expectedStatus: http.StatusOK,
			description:    "Level 1 users should access Level 1 endpoints",
		},
		{
			name:           "Level1_CannotAccessLevel2",
			userLevel:      1,
			requiredLevel:  2,
			expectedStatus: http.StatusForbidden,
			description:    "Level 1 users should not access Level 2 endpoints",
		},
		{
			name:           "Level2_CanAccessLevel1",
			userLevel:      2,
			requiredLevel:  1,
			expectedStatus: http.StatusOK,
			description:    "Level 2 users should access Level 1 endpoints",
		},
		{
			name:           "Level2_CanAccessLevel2",
			userLevel:      2,
			requiredLevel:  2,
			expectedStatus: http.StatusOK,
			description:    "Level 2 users should access Level 2 endpoints",
		},
		{
			name:           "Level2_CannotAccessLevel3",
			userLevel:      2,
			requiredLevel:  3,
			expectedStatus: http.StatusForbidden,
			description:    "Level 2 users should not access Level 3 endpoints",
		},
		{
			name:           "Level3_CanAccessLevel3",
			userLevel:      3,
			requiredLevel:  3,
			expectedStatus: http.StatusOK,
			description:    "Level 3 users should access Level 3 endpoints",
		},
		{
			name:           "Level3_CannotAccessLevel4",
			userLevel:      3,
			requiredLevel:  4,
			expectedStatus: http.StatusForbidden,
			description:    "Level 3 users should not access Level 4 endpoints",
		},
		{
			name:           "Level4_CanAccessLevel4",
			userLevel:      4,
			requiredLevel:  4,
			expectedStatus: http.StatusOK,
			description:    "Level 4 users should access Level 4 endpoints",
		},
		{
			name:           "Level4_CanAccessAllLevels",
			userLevel:      4,
			requiredLevel:  1,
			expectedStatus: http.StatusOK,
			description:    "Level 4 users should access all lower level endpoints",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()

			// Add middleware to set user level before the auth middleware
			router.Use(func(c *gin.Context) {
				c.Set("user_level", tc.userLevel)
				c.Set("user_id", uuid.New().String())
				c.Set("tenant_id", uuid.New().String())
				c.Next()
			})

			// Add test endpoint with required level
			router.GET("/test", middleware.RequireLevel(tc.requiredLevel), func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{"success": true})
			})

			// Create request
			req := httptest.NewRequest("GET", "/test", nil)
			w := httptest.NewRecorder()

			// Execute request
			router.ServeHTTP(w, req)

			assert.Equal(t, tc.expectedStatus, w.Code, tc.description)
		})
	}
}

// Property Test: Real Trading Access Validation
// Validates: Requirements 3.1, 3.2 - Real trading access control
func TestProperty_RealTradingAccessValidation(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	gin.SetMode(gin.TestMode)

	middleware := NewKongAuthMiddleware(logger)

	testCases := []struct {
		name           string
		userLevel      int
		expectedStatus int
		description    string
	}{
		{
			name:           "Level1_CannotAccessRealTrading",
			userLevel:      1,
			expectedStatus: http.StatusForbidden,
			description:    "Level 1 users should not access real trading",
		},
		{
			name:           "Level2_CannotAccessRealTrading",
			userLevel:      2,
			expectedStatus: http.StatusForbidden,
			description:    "Level 2 users should not access real trading",
		},
		{
			name:           "Level3_CanAccessRealTrading",
			userLevel:      3,
			expectedStatus: http.StatusOK,
			description:    "Level 3 users should access real trading",
		},
		{
			name:           "Level4_CanAccessRealTrading",
			userLevel:      4,
			expectedStatus: http.StatusOK,
			description:    "Level 4 users should access real trading",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()

			// Add middleware to set user level before the auth middleware
			router.Use(func(c *gin.Context) {
				c.Set("user_level", tc.userLevel)
				c.Set("user_id", uuid.New().String())
				c.Set("tenant_id", uuid.New().String())
				c.Next()
			})

			// Add test endpoint with real trading validation
			router.POST("/positions", middleware.ValidateRealTradingAccess(), func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{"success": true})
			})

			// Create request
			req := httptest.NewRequest("POST", "/positions", nil)
			w := httptest.NewRecorder()

			// Execute request
			router.ServeHTTP(w, req)

			assert.Equal(t, tc.expectedStatus, w.Code, tc.description)
		})
	}
}

// Property Test: Admin Access Validation
// Validates: Requirements 3.4 - Admin access control
func TestProperty_AdminAccessValidation(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	gin.SetMode(gin.TestMode)

	middleware := NewKongAuthMiddleware(logger)

	testCases := []struct {
		name           string
		userLevel      int
		expectedStatus int
		description    string
	}{
		{
			name:           "Level1_CannotAccessAdmin",
			userLevel:      1,
			expectedStatus: http.StatusForbidden,
			description:    "Level 1 users should not access admin features",
		},
		{
			name:           "Level2_CannotAccessAdmin",
			userLevel:      2,
			expectedStatus: http.StatusForbidden,
			description:    "Level 2 users should not access admin features",
		},
		{
			name:           "Level3_CannotAccessAdmin",
			userLevel:      3,
			expectedStatus: http.StatusForbidden,
			description:    "Level 3 users should not access admin features",
		},
		{
			name:           "Level4_CanAccessAdmin",
			userLevel:      4,
			expectedStatus: http.StatusOK,
			description:    "Level 4 users should access admin features",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()

			// Add middleware to set user level before the auth middleware
			router.Use(func(c *gin.Context) {
				c.Set("user_level", tc.userLevel)
				c.Set("user_id", uuid.New().String())
				c.Set("tenant_id", uuid.New().String())
				c.Next()
			})

			// Add test endpoint with admin validation
			router.GET("/admin/users", middleware.ValidateAdminAccess(), func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{"success": true})
			})

			// Create request
			req := httptest.NewRequest("GET", "/admin/users", nil)
			w := httptest.NewRecorder()

			// Execute request
			router.ServeHTTP(w, req)

			assert.Equal(t, tc.expectedStatus, w.Code, tc.description)
		})
	}
}

// Property Test: Feature Access Validation
// Validates: Requirements 2.1, 2.2 - Feature-based access control
func TestProperty_FeatureAccessValidation(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	gin.SetMode(gin.TestMode)

	middleware := NewKongAuthMiddleware(logger)

	testCases := []struct {
		name           string
		userLevel      int
		feature        string
		expectedStatus int
		description    string
	}{
		{
			name:           "Level1_CanAccessBasicFeatures",
			userLevel:      1,
			feature:        "view_data",
			expectedStatus: http.StatusOK,
			description:    "Level 1 users should access basic features",
		},
		{
			name:           "Level1_CannotAccessSignals",
			userLevel:      1,
			feature:        "signals_generation",
			expectedStatus: http.StatusForbidden,
			description:    "Level 1 users should not access signal generation",
		},
		{
			name:           "Level2_CanAccessSignals",
			userLevel:      2,
			feature:        "signals_generation",
			expectedStatus: http.StatusOK,
			description:    "Level 2 users should access signal generation",
		},
		{
			name:           "Level2_CannotAccessRealTrading",
			userLevel:      2,
			feature:        "real_trading",
			expectedStatus: http.StatusForbidden,
			description:    "Level 2 users should not access real trading",
		},
		{
			name:           "Level3_CanAccessRealTrading",
			userLevel:      3,
			feature:        "real_trading",
			expectedStatus: http.StatusOK,
			description:    "Level 3 users should access real trading",
		},
		{
			name:           "Level3_CannotAccessAdminAPIs",
			userLevel:      3,
			feature:        "admin_apis",
			expectedStatus: http.StatusForbidden,
			description:    "Level 3 users should not access admin APIs",
		},
		{
			name:           "Level4_CanAccessAdminAPIs",
			userLevel:      4,
			feature:        "admin_apis",
			expectedStatus: http.StatusOK,
			description:    "Level 4 users should access admin APIs",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()

			// Add middleware to set user level before the auth middleware
			router.Use(func(c *gin.Context) {
				c.Set("user_level", tc.userLevel)
				c.Set("user_id", uuid.New().String())
				c.Set("tenant_id", uuid.New().String())
				c.Next()
			})

			// Add test endpoint with feature validation
			router.GET("/test", middleware.RequireFeature(tc.feature), func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{"success": true})
			})

			// Create request
			req := httptest.NewRequest("GET", "/test", nil)
			w := httptest.NewRecorder()

			// Execute request
			router.ServeHTTP(w, req)

			assert.Equal(t, tc.expectedStatus, w.Code, tc.description)
		})
	}
}

// Property Test: Rate Limiting Headers
// Validates: Rate limiting information is properly set
func TestProperty_RateLimitingHeaders(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	gin.SetMode(gin.TestMode)

	middleware := NewKongAuthMiddleware(logger)

	testCases := []struct {
		name        string
		userLevel   int
		hasHeaders  bool
		description string
	}{
		{
			name:        "Level1_HasRateLimitHeaders",
			userLevel:   1,
			hasHeaders:  true,
			description: "Level 1 users should have rate limit headers",
		},
		{
			name:        "Level2_HasRateLimitHeaders",
			userLevel:   2,
			hasHeaders:  true,
			description: "Level 2 users should have rate limit headers",
		},
		{
			name:        "Level3_HasRateLimitHeaders",
			userLevel:   3,
			hasHeaders:  true,
			description: "Level 3 users should have rate limit headers",
		},
		{
			name:        "Level4_HasUnlimitedHeader",
			userLevel:   4,
			hasHeaders:  true, // level-4 gets "unlimited" header
			description: "Level 4 users get X-RateLimit-Limit: unlimited header",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()

			// Add middleware to set user level before the auth middleware
			router.Use(func(c *gin.Context) {
				c.Set("user_level", tc.userLevel)
				c.Set("user_id", uuid.New().String())
				c.Set("tenant_id", uuid.New().String())
				c.Next()
			})

			// Add test endpoint with rate limiting
			router.GET("/test", middleware.CheckRateLimit(), func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{"success": true})
			})

			// Create request
			req := httptest.NewRequest("GET", "/test", nil)
			w := httptest.NewRecorder()

			// Execute request
			router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code, "Request should succeed")

			if tc.hasHeaders {
				assert.NotEmpty(t, w.Header().Get("X-RateLimit-Limit"), "Should have rate limit header")
			} else {
				assert.Empty(t, w.Header().Get("X-RateLimit-Limit"), "Should not have rate limit header for unlimited users")
			}
		})
	}
}

// Benchmark Test: Kong Auth Middleware Performance
func BenchmarkKongAuthMiddleware_RequireLevel(b *testing.B) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	gin.SetMode(gin.TestMode)

	middleware := NewKongAuthMiddleware(logger)

	router := gin.New()
	router.GET("/test", middleware.RequireLevel(2), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	b.ResetTimer()
	// Add middleware to set user level before the auth middleware
	router.Use(func(c *gin.Context) {
		c.Set("user_level", 2)
		c.Set("user_id", uuid.New().String())
		c.Set("tenant_id", uuid.New().String())
		c.Next()
	})

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest("GET", "/test", nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				b.Errorf("Expected 200, got %d", w.Code)
			}
		}
	})
}
