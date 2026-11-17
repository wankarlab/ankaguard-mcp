package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

// Property Test 4: Kong Level Validation Property Test
// Validates: Requirements 2.3 - Kong Auth Service level-based access control
// This property test ensures that Kong Auth level validation works correctly
// across all possible combinations of user levels and required levels
func TestProperty_KongLevelValidationProperty(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	gin.SetMode(gin.TestMode)

	middleware := NewKongAuthMiddleware(logger)

	// Property: For any user level U and required level R:
	// - If U >= R, access should be granted (200)
	// - If U < R, access should be denied (403)
	// - Invalid levels should be handled gracefully

	testCases := []struct {
		name           string
		userLevel      interface{} // Can be int, string, or nil to test type safety
		requiredLevel  int
		expectedStatus int
		description    string
	}{
		// Valid level combinations
		{
			name:           "Property_Level1_RequiresLevel1_ShouldPass",
			userLevel:      1,
			requiredLevel:  1,
			expectedStatus: http.StatusOK,
			description:    "Level 1 user accessing Level 1 endpoint should succeed",
		},
		{
			name:           "Property_Level2_RequiresLevel1_ShouldPass",
			userLevel:      2,
			requiredLevel:  1,
			expectedStatus: http.StatusOK,
			description:    "Level 2 user accessing Level 1 endpoint should succeed",
		},
		{
			name:           "Property_Level3_RequiresLevel2_ShouldPass",
			userLevel:      3,
			requiredLevel:  2,
			expectedStatus: http.StatusOK,
			description:    "Level 3 user accessing Level 2 endpoint should succeed",
		},
		{
			name:           "Property_Level4_RequiresLevel3_ShouldPass",
			userLevel:      4,
			requiredLevel:  3,
			expectedStatus: http.StatusOK,
			description:    "Level 4 user accessing Level 3 endpoint should succeed",
		},
		{
			name:           "Property_Level1_RequiresLevel2_ShouldFail",
			userLevel:      1,
			requiredLevel:  2,
			expectedStatus: http.StatusForbidden,
			description:    "Level 1 user accessing Level 2 endpoint should fail",
		},
		{
			name:           "Property_Level2_RequiresLevel3_ShouldFail",
			userLevel:      2,
			requiredLevel:  3,
			expectedStatus: http.StatusForbidden,
			description:    "Level 2 user accessing Level 3 endpoint should fail",
		},
		{
			name:           "Property_Level3_RequiresLevel4_ShouldFail",
			userLevel:      3,
			requiredLevel:  4,
			expectedStatus: http.StatusForbidden,
			description:    "Level 3 user accessing Level 4 endpoint should fail",
		},
		// Edge cases and error handling
		{
			name:           "Property_NoUserLevel_ShouldFail",
			userLevel:      nil,
			requiredLevel:  1,
			expectedStatus: http.StatusForbidden,
			description:    "Missing user level should fail gracefully",
		},
		{
			name:           "Property_InvalidUserLevelType_ShouldFail",
			userLevel:      "invalid",
			requiredLevel:  1,
			expectedStatus: http.StatusForbidden,
			description:    "Invalid user level type should fail gracefully",
		},
		{
			name:           "Property_ZeroUserLevel_ShouldFail",
			userLevel:      0,
			requiredLevel:  1,
			expectedStatus: http.StatusForbidden,
			description:    "Zero user level should fail",
		},
		{
			name:           "Property_NegativeUserLevel_ShouldFail",
			userLevel:      -1,
			requiredLevel:  1,
			expectedStatus: http.StatusForbidden,
			description:    "Negative user level should fail",
		},
		// Boundary conditions
		{
			name:           "Property_MaxLevel_RequiresMax_ShouldPass",
			userLevel:      4,
			requiredLevel:  4,
			expectedStatus: http.StatusOK,
			description:    "Max level user accessing max level endpoint should succeed",
		},
		{
			name:           "Property_HighUserLevel_RequiresLow_ShouldPass",
			userLevel:      4,
			requiredLevel:  1,
			expectedStatus: http.StatusOK,
			description:    "High level user accessing low level endpoint should succeed",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()

			// Add middleware to set user level (or not set it for nil cases)
			router.Use(func(c *gin.Context) {
				if tc.userLevel != nil {
					c.Set("user_level", tc.userLevel)
				}
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

			// Validate response
			assert.Equal(t, tc.expectedStatus, w.Code, tc.description)

			// Additional validation for error responses
			if tc.expectedStatus == http.StatusForbidden {
				assert.Contains(t, w.Header().Get("Content-Type"), "application/json", "Error response should be JSON")
			}
		})
	}
}

// Property Test: Kong Level Inheritance Property
// Validates that higher levels always have access to lower level features
func TestProperty_KongLevelInheritanceProperty(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	gin.SetMode(gin.TestMode)

	middleware := NewKongAuthMiddleware(logger)

	// Property: For any user level U and required level R where U > R,
	// the user should always have access (inheritance property)

	userLevels := []int{1, 2, 3, 4}
	requiredLevels := []int{1, 2, 3, 4}

	for _, userLevel := range userLevels {
		for _, requiredLevel := range requiredLevels {
			testName := fmt.Sprintf("Property_UserLevel%d_RequiredLevel%d", userLevel, requiredLevel)

			t.Run(testName, func(t *testing.T) {
				router := gin.New()

				// Add middleware to set user level
				router.Use(func(c *gin.Context) {
					c.Set("user_level", userLevel)
					c.Set("user_id", uuid.New().String())
					c.Set("tenant_id", uuid.New().String())
					c.Next()
				})

				// Add test endpoint with required level
				router.GET("/test", middleware.RequireLevel(requiredLevel), func(c *gin.Context) {
					c.JSON(http.StatusOK, gin.H{"success": true})
				})

				// Create request
				req := httptest.NewRequest("GET", "/test", nil)
				w := httptest.NewRecorder()

				// Execute request
				router.ServeHTTP(w, req)

				// Validate inheritance property
				expectedStatus := http.StatusForbidden
				if userLevel >= requiredLevel {
					expectedStatus = http.StatusOK
				}

				assert.Equal(t, expectedStatus, w.Code,
					fmt.Sprintf("User level %d should %s access to required level %d",
						userLevel,
						map[bool]string{true: "have", false: "not have"}[userLevel >= requiredLevel],
						requiredLevel))
			})
		}
	}
}

// Property Test: Kong Level Consistency Property
// Validates that the same user level always produces the same result
func TestProperty_KongLevelConsistencyProperty(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	gin.SetMode(gin.TestMode)

	middleware := NewKongAuthMiddleware(logger)

	// Property: For any user level U and required level R,
	// multiple requests should always produce the same result (consistency)

	testCases := []struct {
		userLevel     int
		requiredLevel int
	}{
		{1, 1}, {1, 2}, {2, 1}, {2, 2}, {2, 3}, {3, 2}, {3, 3}, {3, 4}, {4, 1}, {4, 4},
	}

	for _, tc := range testCases {
		testName := fmt.Sprintf("Property_Consistency_UserLevel%d_RequiredLevel%d", tc.userLevel, tc.requiredLevel)

		t.Run(testName, func(t *testing.T) {
			var results []int

			// Execute the same request multiple times
			for i := 0; i < 5; i++ {
				router := gin.New()

				// Add middleware to set user level
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

				results = append(results, w.Code)
			}

			// Validate consistency - all results should be the same
			firstResult := results[0]
			for i, result := range results {
				assert.Equal(t, firstResult, result,
					fmt.Sprintf("Request %d should have same result as first request for user level %d, required level %d",
						i, tc.userLevel, tc.requiredLevel))
			}
		})
	}
}

// Benchmark: Kong Level Validation Performance
func BenchmarkProperty_KongLevelValidationPerformance(b *testing.B) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	gin.SetMode(gin.TestMode)

	middleware := NewKongAuthMiddleware(logger)

	router := gin.New()

	// Add middleware to set user level
	router.Use(func(c *gin.Context) {
		c.Set("user_level", 3)
		c.Set("user_id", uuid.New().String())
		c.Set("tenant_id", uuid.New().String())
		c.Next()
	})

	router.GET("/test", middleware.RequireLevel(2), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	b.ResetTimer()
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
