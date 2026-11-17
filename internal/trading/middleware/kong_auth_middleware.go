package middleware

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/trading/plans"
	"github.com/rantipay/trading-mcp/pkg/auth"
)

// RateLimiter interface for pluggable rate limiting backends
type RateLimiter interface {
	// Allow checks if a request is allowed and returns remaining requests
	Allow(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, remaining int, resetAt time.Time, err error)
}

// InMemoryRateLimiter provides a simple in-memory rate limiter
// NOTE: In production with multiple instances, use Valkey-based rate limiter
type InMemoryRateLimiter struct {
	mu      sync.RWMutex
	buckets map[string]*rateBucket
}

type rateBucket struct {
	count     int
	resetAt   time.Time
	lastClean time.Time
}

// NewInMemoryRateLimiter creates a new in-memory rate limiter
func NewInMemoryRateLimiter() *InMemoryRateLimiter {
	rl := &InMemoryRateLimiter{
		buckets: make(map[string]*rateBucket),
	}
	// Start cleanup goroutine
	go rl.cleanupLoop()
	return rl
}

func (rl *InMemoryRateLimiter) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		rl.cleanup()
	}
}

func (rl *InMemoryRateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	for key, bucket := range rl.buckets {
		if now.After(bucket.resetAt) {
			delete(rl.buckets, key)
		}
	}
}

func (rl *InMemoryRateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, remaining int, resetAt time.Time, err error) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	bucket, exists := rl.buckets[key]
	if !exists || now.After(bucket.resetAt) {
		// Create new bucket or reset expired one
		bucket = &rateBucket{
			count:     0,
			resetAt:   now.Add(window),
			lastClean: now,
		}
		rl.buckets[key] = bucket
	}

	remaining = limit - bucket.count
	resetAt = bucket.resetAt

	if bucket.count >= limit {
		return false, 0, resetAt, nil
	}

	bucket.count++
	remaining = limit - bucket.count

	return true, remaining, resetAt, nil
}

// KongAuthMiddleware handles Kong Auth Service level-based authorization
type KongAuthMiddleware struct {
	logger      *logrus.Logger
	rateLimiter RateLimiter
}

// NewKongAuthMiddleware creates a new Kong Auth middleware instance
func NewKongAuthMiddleware(logger *logrus.Logger) *KongAuthMiddleware {
	return &KongAuthMiddleware{
		logger:      logger,
		rateLimiter: NewInMemoryRateLimiter(),
	}
}

// NewKongAuthMiddlewareWithRateLimiter creates a new middleware with custom rate limiter
// Use this to inject a Valkey-based rate limiter in production
func NewKongAuthMiddlewareWithRateLimiter(logger *logrus.Logger, rateLimiter RateLimiter) *KongAuthMiddleware {
	return &KongAuthMiddleware{
		logger:      logger,
		rateLimiter: rateLimiter,
	}
}

// RequireLevel returns a middleware that requires a minimum Kong Auth Service level
func (m *KongAuthMiddleware) RequireLevel(requiredLevel int) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get user level from JWT claims (set by Kong JWT middleware)
		userLevel, exists := c.Get("user_level")
		if !exists {
			m.respondInsufficientLevel(c, requiredLevel, 0, "User level not found in token")
			return
		}

		currentLevel, ok := userLevel.(int)
		if !ok {
			m.respondInsufficientLevel(c, requiredLevel, 0, "Invalid user level format")
			return
		}

		// Check if user has sufficient level
		if currentLevel < requiredLevel {
			m.logAccessDenied(c, requiredLevel, currentLevel)
			m.respondInsufficientLevel(c, requiredLevel, currentLevel, "Insufficient subscription level")
			return
		}

		// Log successful authorization
		m.logAccessGranted(c, requiredLevel, currentLevel)
		c.Next()
	}
}

// RequireAuthLevel returns a middleware that requires a minimum auth.AuthLevel
func (m *KongAuthMiddleware) RequireAuthLevel(requiredAuthLevel auth.AuthLevel) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check if user has minimum auth level using existing auth package
		if !auth.HasMinimumAuthLevel(c.Request.Context(), requiredAuthLevel) {
			userLevel, _ := c.Get("user_level")
			currentLevel, _ := userLevel.(int)

			m.logAccessDenied(c, int(m.authLevelToInt(requiredAuthLevel)), currentLevel)
			m.respondInsufficientAuthLevel(c, requiredAuthLevel, "Insufficient authentication level")
			return
		}

		c.Next()
	}
}

// RequireFeature returns a middleware that requires access to a specific feature
func (m *KongAuthMiddleware) RequireFeature(feature string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userLevel, exists := c.Get("user_level")
		if !exists {
			m.respondInsufficientLevel(c, 1, 0, "User level not found")
			return
		}

		currentLevel, ok := userLevel.(int)
		if !ok {
			m.respondInsufficientLevel(c, 1, 0, "Invalid user level format")
			return
		}

		// Check if user's Kong level has access to the feature
		if !plans.HasFeature(currentLevel, feature) {
			m.logger.WithFields(logrus.Fields{
				"user_level": currentLevel,
				"feature":    feature,
				"path":       c.Request.URL.Path,
				"user_id":    c.GetString("user_id"),
				"tenant_id":  c.GetString("tenant_id"),
			}).Warn("Feature access denied - insufficient Kong Auth level")

			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"error": gin.H{
					"code":             "FEATURE_ACCESS_DENIED",
					"message":          "Your subscription plan does not include access to this feature",
					"feature":          feature,
					"required_upgrade": m.getRequiredUpgradeForFeature(feature),
				},
				"meta": gin.H{
					"timestamp": time.Now().Format(time.RFC3339),
				},
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// CheckRateLimit returns a middleware that enforces Kong level-based rate limiting
func (m *KongAuthMiddleware) CheckRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		userLevel, exists := c.Get("user_level")
		if !exists {
			c.Next() // Skip rate limiting if no level found
			return
		}

		currentLevel, ok := userLevel.(int)
		if !ok {
			c.Next() // Skip rate limiting if invalid level
			return
		}

		// Get rate limit for user's Kong level
		rateLimit := plans.GetAPIRateLimit(currentLevel)

		// If unlimited (0), skip rate limiting
		if rateLimit == 0 {
			c.Header("X-RateLimit-Limit", "unlimited")
			c.Next()
			return
		}

		// Build rate limit key: user_id + tenant_id + path prefix
		userID := c.GetString("user_id")
		tenantID := c.GetString("tenant_id")
		if userID == "" {
			userID = c.ClientIP() // Fallback to IP for anonymous users
		}
		rateLimitKey := fmt.Sprintf("ratelimit:%s:%s:%s", tenantID, userID, c.Request.URL.Path)

		// Check rate limit using the rate limiter
		// Rate limit is per minute
		allowed, remaining, resetAt, err := m.rateLimiter.Allow(
			c.Request.Context(),
			rateLimitKey,
			rateLimit,
			time.Minute,
		)

		// Always add rate limit headers
		c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", rateLimit))
		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
		c.Header("X-RateLimit-Reset", fmt.Sprintf("%d", resetAt.Unix()))

		if err != nil {
			// Log error but allow request (fail open for rate limiting)
			m.logger.WithError(err).WithFields(logrus.Fields{
				"user_id":   userID,
				"tenant_id": tenantID,
				"path":      c.Request.URL.Path,
			}).Error("Rate limiter error - allowing request")
			c.Next()
			return
		}

		if !allowed {
			m.logger.WithFields(logrus.Fields{
				"user_level": currentLevel,
				"rate_limit": rateLimit,
				"path":       c.Request.URL.Path,
				"user_id":    userID,
				"tenant_id":  tenantID,
				"reset_at":   resetAt.Format(time.RFC3339),
			}).Warn("Rate limit exceeded")

			c.Header("Retry-After", fmt.Sprintf("%d", int(time.Until(resetAt).Seconds())))
			c.JSON(http.StatusTooManyRequests, gin.H{
				"success": false,
				"error": gin.H{
					"code":       "RATE_LIMIT_EXCEEDED",
					"message":    "Rate limit exceeded. Please wait before making more requests.",
					"limit":      rateLimit,
					"reset_at":   resetAt.Format(time.RFC3339),
					"retry_after": int(time.Until(resetAt).Seconds()),
				},
				"meta": gin.H{
					"timestamp": time.Now().Format(time.RFC3339),
				},
			})
			c.Abort()
			return
		}

		m.logger.WithFields(logrus.Fields{
			"user_level": currentLevel,
			"rate_limit": rateLimit,
			"remaining":  remaining,
			"path":       c.Request.URL.Path,
			"user_id":    userID,
			"tenant_id":  tenantID,
		}).Debug("Rate limit check passed")

		c.Next()
	}
}

// ValidateRealTradingAccess checks if user can access real trading features
func (m *KongAuthMiddleware) ValidateRealTradingAccess() gin.HandlerFunc {
	return func(c *gin.Context) {
		userLevel, exists := c.Get("user_level")
		if !exists {
			m.respondInsufficientLevel(c, 3, 0, "User level required for trading")
			return
		}

		currentLevel, ok := userLevel.(int)
		if !ok {
			m.respondInsufficientLevel(c, 3, 0, "Invalid user level")
			return
		}

		// Check if user can access real trading (Level 3+)
		if !plans.CanAccessRealTrading(currentLevel) {
			m.logger.WithFields(logrus.Fields{
				"user_level": currentLevel,
				"path":       c.Request.URL.Path,
				"user_id":    c.GetString("user_id"),
				"tenant_id":  c.GetString("tenant_id"),
			}).Warn("Real trading access denied - insufficient Kong Auth level")

			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"error": gin.H{
					"code":            "REAL_TRADING_ACCESS_DENIED",
					"message":         "Real trading requires Premium subscription (Level 3) or higher",
					"current_level":   currentLevel,
					"required_level":  3,
					"upgrade_options": []string{"Premium Plan", "Enterprise Plan"},
				},
				"meta": gin.H{
					"timestamp": time.Now().Format(time.RFC3339),
				},
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// ValidateAdminAccess checks if user can access admin features
func (m *KongAuthMiddleware) ValidateAdminAccess() gin.HandlerFunc {
	return func(c *gin.Context) {
		userLevel, exists := c.Get("user_level")
		if !exists {
			m.respondInsufficientLevel(c, 4, 0, "User level required for admin access")
			return
		}

		currentLevel, ok := userLevel.(int)
		if !ok {
			m.respondInsufficientLevel(c, 4, 0, "Invalid user level")
			return
		}

		// Check if user can access admin features (Level 4 only)
		if !plans.CanAccessAdminFeatures(currentLevel) {
			m.logger.WithFields(logrus.Fields{
				"user_level": currentLevel,
				"path":       c.Request.URL.Path,
				"user_id":    c.GetString("user_id"),
				"tenant_id":  c.GetString("tenant_id"),
			}).Warn("Admin access denied - insufficient Kong Auth level")

			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"error": gin.H{
					"code":            "ADMIN_ACCESS_DENIED",
					"message":         "Admin features require Enterprise subscription (Level 4)",
					"current_level":   currentLevel,
					"required_level":  4,
					"upgrade_options": []string{"Enterprise Plan"},
				},
				"meta": gin.H{
					"timestamp": time.Now().Format(time.RFC3339),
				},
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// Helper methods

func (m *KongAuthMiddleware) logAccessGranted(c *gin.Context, requiredLevel, currentLevel int) {
	m.logger.WithFields(logrus.Fields{
		"required_level": requiredLevel,
		"current_level":  currentLevel,
		"path":           c.Request.URL.Path,
		"user_id":        c.GetString("user_id"),
		"tenant_id":      c.GetString("tenant_id"),
		"status":         "access_granted",
	}).Info("Kong Auth level validation successful")
}

func (m *KongAuthMiddleware) logAccessDenied(c *gin.Context, requiredLevel, currentLevel int) {
	m.logger.WithFields(logrus.Fields{
		"required_level": requiredLevel,
		"current_level":  currentLevel,
		"path":           c.Request.URL.Path,
		"user_id":        c.GetString("user_id"),
		"tenant_id":      c.GetString("tenant_id"),
		"status":         "access_denied",
	}).Warn("Kong Auth level validation failed")
}

func (m *KongAuthMiddleware) respondInsufficientLevel(c *gin.Context, requiredLevel, currentLevel int, message string) {
	planName := "Unknown"
	if plan := plans.GetPlanByKongLevel(requiredLevel); plan != nil {
		planName = plan.Name
	}

	c.JSON(http.StatusForbidden, gin.H{
		"success": false,
		"error": gin.H{
			"code":           "INSUFFICIENT_KONG_LEVEL",
			"message":        message,
			"current_level":  currentLevel,
			"required_level": requiredLevel,
			"required_plan":  planName,
			"upgrade_url":    "/api/v1/auth/progressive/upgrade/initiate", // Kong Gateway endpoint
		},
		"meta": gin.H{
			"timestamp": time.Now().Format(time.RFC3339),
		},
	})
	c.Abort()
}

func (m *KongAuthMiddleware) respondInsufficientAuthLevel(c *gin.Context, requiredAuthLevel auth.AuthLevel, message string) {
	c.JSON(http.StatusForbidden, gin.H{
		"success": false,
		"error": gin.H{
			"code":                "INSUFFICIENT_AUTH_LEVEL",
			"message":             message,
			"required_auth_level": requiredAuthLevel,
		},
		"meta": gin.H{
			"timestamp": time.Now().Format(time.RFC3339),
		},
	})
	c.Abort()
}

func (m *KongAuthMiddleware) authLevelToInt(level auth.AuthLevel) int {
	switch level {
	case auth.BASIC:
		return 1
	case auth.VERIFIED:
		return 2
	case auth.PREMIUM:
		return 3
	case auth.ENTERPRISE:
		return 4
	default:
		return 1
	}
}

func (m *KongAuthMiddleware) getRequiredUpgradeForFeature(feature string) string {
	// Map features to required plans
	featureToLevel := map[string]string{
		"signals_generation":   "Standard Plan (Level 2)",
		"backtesting":          "Standard Plan (Level 2)",
		"real_trading":         "Premium Plan (Level 3)",
		"risk_management":      "Premium Plan (Level 3)",
		"admin_apis":           "Enterprise Plan (Level 4)",
		"bulk_operations":      "Enterprise Plan (Level 4)",
		"unlimited_everything": "Enterprise Plan (Level 4)",
	}

	if upgrade, exists := featureToLevel[feature]; exists {
		return upgrade
	}
	return "Higher subscription plan required"
}

// GetUserPlanInfo returns detailed plan information for the current user
func (m *KongAuthMiddleware) GetUserPlanInfo() gin.HandlerFunc {
	return func(c *gin.Context) {
		userLevel, exists := c.Get("user_level")
		if !exists {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"error": gin.H{
					"code":    "USER_LEVEL_NOT_FOUND",
					"message": "User level not found in token",
				},
			})
			return
		}

		currentLevel, ok := userLevel.(int)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"error": gin.H{
					"code":    "INVALID_USER_LEVEL",
					"message": "Invalid user level format",
				},
			})
			return
		}

		// Get plan information
		plan := plans.GetPlanByKongLevel(currentLevel)
		if plan == nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"error": gin.H{
					"code":    "PLAN_NOT_FOUND",
					"message": "Plan configuration not found",
				},
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"current_plan":       plan,
				"usage_limits":       plan.Limits,
				"available_features": plan.Features,
				"upgrade_options":    m.getUpgradeOptions(currentLevel),
			},
			"meta": gin.H{
				"timestamp": time.Now().Format(time.RFC3339),
				"user_id":   c.GetString("user_id"),
				"tenant_id": c.GetString("tenant_id"),
			},
		})
	}
}

func (m *KongAuthMiddleware) getUpgradeOptions(currentLevel int) []map[string]interface{} {
	allPlans := plans.GetAllPlans()
	upgradeOptions := []map[string]interface{}{}

	for _, plan := range allPlans {
		if plan.Level > currentLevel {
			upgradeOptions = append(upgradeOptions, map[string]interface{}{
				"level":       plan.Level,
				"name":        plan.Name,
				"description": plan.Description,
				"features":    plan.Features,
				"limits":      plan.Limits,
			})
		}
	}

	return upgradeOptions
}
