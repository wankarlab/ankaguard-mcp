package middleware

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/subscription"
)

// SubscriptionMiddleware provides subscription-based access control
type SubscriptionMiddleware struct {
	service *subscription.Service
	logger  *logrus.Logger
}

// NewSubscriptionMiddleware creates a new subscription middleware
func NewSubscriptionMiddleware(service *subscription.Service, logger *logrus.Logger) *SubscriptionMiddleware {
	if logger == nil {
		logger = logrus.New()
	}
	return &SubscriptionMiddleware{
		service: service,
		logger:  logger,
	}
}

// Context keys for subscription information
const (
	SubscriptionKey     = "subscription"
	SubscriptionTierKey = "subscription_tier"
	SubscriptionIDKey   = "subscription_id"
)

// RequireSubscription creates a middleware that requires a minimum subscription tier
func (m *SubscriptionMiddleware) RequireSubscription(minTier subscription.SubscriptionTier) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := GetUserFromContext(c)
		if err != nil {
			m.logger.WithError(err).WithFields(logrus.Fields{
				"path":   c.Request.URL.Path,
				"method": c.Request.Method,
			}).Warn("User ID not found in context for subscription check")

			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error": gin.H{
					"code":    "USER_REQUIRED",
					"message": "Authentication required to access this resource",
				},
				"meta": gin.H{
					"timestamp": time.Now().Format(time.RFC3339),
				},
			})
			c.Abort()
			return
		}

		// Get subscription
		sub, err := m.service.GetSubscription(c.Request.Context(), userID)
		if err != nil {
			m.logger.WithError(err).WithField("user_id", userID).Error("Failed to get subscription")

			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"error": gin.H{
					"code":    "SUBSCRIPTION_ERROR",
					"message": "Failed to verify subscription status",
				},
				"meta": gin.H{
					"timestamp": time.Now().Format(time.RFC3339),
				},
			})
			c.Abort()
			return
		}

		// Check if subscription is active
		if !sub.CanAccess() {
			errorCode := "SUBSCRIPTION_INACTIVE"
			errorMessage := "Your subscription is not active"

			if sub.IsExpired() {
				errorCode = "SUBSCRIPTION_EXPIRED"
				errorMessage = "Your subscription has expired"
			}

			m.logger.WithFields(logrus.Fields{
				"user_id":   userID,
				"tier":      sub.Tier,
				"is_active": sub.IsActive,
				"expired":   sub.IsExpired(),
			}).Warn("Subscription access denied")

			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"error": gin.H{
					"code":         errorCode,
					"message":      errorMessage,
					"current_tier": string(sub.Tier),
					"expires_at":   sub.ExpiresAt,
				},
				"meta": gin.H{
					"timestamp":    time.Now().Format(time.RFC3339),
					"upgrade_info": m.getUpgradeInfo(sub.Tier, minTier),
				},
			})
			c.Abort()
			return
		}

		// Check tier requirement
		if !sub.Tier.IsAtLeast(minTier) {
			m.logger.WithFields(logrus.Fields{
				"user_id":       userID,
				"current_tier":  sub.Tier,
				"required_tier": minTier,
				"path":          c.Request.URL.Path,
			}).Warn("Insufficient subscription tier")

			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"error": gin.H{
					"code":          "SUBSCRIPTION_TIER_REQUIRED",
					"message":       fmt.Sprintf("This feature requires %s tier or higher", minTier.DisplayName()),
					"current_tier":  string(sub.Tier),
					"required_tier": string(minTier),
				},
				"meta": gin.H{
					"timestamp":    time.Now().Format(time.RFC3339),
					"upgrade_info": m.getUpgradeInfo(sub.Tier, minTier),
				},
			})
			c.Abort()
			return
		}

		// Set subscription context
		c.Set(SubscriptionKey, sub)
		c.Set(SubscriptionTierKey, string(sub.Tier))
		c.Set(SubscriptionIDKey, sub.ID.String())

		// Add subscription to request context for downstream use
		ctx := context.WithValue(c.Request.Context(), SubscriptionKey, sub)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}

// RequireFeature creates a middleware that requires a specific feature
func (m *SubscriptionMiddleware) RequireFeature(feature subscription.Feature) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := GetUserFromContext(c)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error": gin.H{
					"code":    "USER_REQUIRED",
					"message": "Authentication required to access this resource",
				},
				"meta": gin.H{
					"timestamp": time.Now().Format(time.RFC3339),
				},
			})
			c.Abort()
			return
		}

		// Check feature access
		if err := m.service.CheckFeatureAccess(c.Request.Context(), userID, feature); err != nil {
			m.logger.WithFields(logrus.Fields{
				"user_id": userID,
				"feature": feature,
				"error":   err.Error(),
			}).Warn("Feature access denied")

			errorCode := "FEATURE_NOT_AVAILABLE"
			errorMessage := fmt.Sprintf("The '%s' feature is not available in your subscription", feature)

			if err == subscription.ErrSubscriptionExpired {
				errorCode = "SUBSCRIPTION_EXPIRED"
				errorMessage = "Your subscription has expired"
			} else if err == subscription.ErrSubscriptionInactive {
				errorCode = "SUBSCRIPTION_INACTIVE"
				errorMessage = "Your subscription is not active"
			}

			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"error": gin.H{
					"code":             errorCode,
					"message":          errorMessage,
					"required_feature": string(feature),
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

// RequireLimit creates a middleware that checks a usage limit before allowing the request
func (m *SubscriptionMiddleware) RequireLimit(usageType subscription.UsageType) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := GetUserFromContext(c)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error": gin.H{
					"code":    "USER_REQUIRED",
					"message": "Authentication required to access this resource",
				},
				"meta": gin.H{
					"timestamp": time.Now().Format(time.RFC3339),
				},
			})
			c.Abort()
			return
		}

		// Check limit
		if err := m.service.CheckLimit(c.Request.Context(), userID, usageType); err != nil {
			m.logger.WithFields(logrus.Fields{
				"user_id":    userID,
				"usage_type": usageType,
				"error":      err.Error(),
			}).Warn("Usage limit exceeded")

			c.JSON(http.StatusTooManyRequests, gin.H{
				"success": false,
				"error": gin.H{
					"code":       "LIMIT_EXCEEDED",
					"message":    err.Error(),
					"limit_type": string(usageType),
				},
				"meta": gin.H{
					"timestamp":  time.Now().Format(time.RFC3339),
					"retry_after": m.getRetryAfter(usageType),
				},
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// IncrementUsage creates a middleware that increments usage after successful request
func (m *SubscriptionMiddleware) IncrementUsage(usageType subscription.UsageType) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Process request first
		c.Next()

		// Only increment on successful responses
		if c.Writer.Status() >= 200 && c.Writer.Status() < 300 {
			userID, err := GetUserFromContext(c)
			if err != nil {
				return
			}

			if err := m.service.IncrementUsage(c.Request.Context(), userID, usageType, 1); err != nil {
				m.logger.WithError(err).WithFields(logrus.Fields{
					"user_id":    userID,
					"usage_type": usageType,
				}).Warn("Failed to increment usage")
			}
		}
	}
}

// LoadSubscription loads the subscription into context without enforcing any requirements
func (m *SubscriptionMiddleware) LoadSubscription() gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := GetUserFromContext(c)
		if err != nil {
			c.Next()
			return
		}

		sub, err := m.service.GetSubscription(c.Request.Context(), userID)
		if err != nil {
			m.logger.WithError(err).WithField("user_id", userID).Debug("Failed to load subscription")
			c.Next()
			return
		}

		c.Set(SubscriptionKey, sub)
		c.Set(SubscriptionTierKey, string(sub.Tier))
		c.Set(SubscriptionIDKey, sub.ID.String())

		ctx := context.WithValue(c.Request.Context(), SubscriptionKey, sub)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}

// SubscriptionAudit creates a middleware that logs subscription-related access
func (m *SubscriptionMiddleware) SubscriptionAudit() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		c.Next()

		// Log after request completes
		tier := c.GetString(SubscriptionTierKey)
		subID := c.GetString(SubscriptionIDKey)

		if tier != "" {
			m.logger.WithFields(logrus.Fields{
				"subscription_id": subID,
				"tier":            tier,
				"user_id":         c.GetString("user_id"),
				"path":            c.Request.URL.Path,
				"method":          c.Request.Method,
				"status":          c.Writer.Status(),
				"duration_ms":     time.Since(start).Milliseconds(),
			}).Debug("Subscription-scoped request processed")
		}
	}
}

// Helper methods

func (m *SubscriptionMiddleware) getUpgradeInfo(currentTier, requiredTier subscription.SubscriptionTier) map[string]interface{} {
	return map[string]interface{}{
		"current_tier":       string(currentTier),
		"current_price":      currentTier.MonthlyPrice(),
		"required_tier":      string(requiredTier),
		"required_price":     requiredTier.MonthlyPrice(),
		"price_difference":   requiredTier.MonthlyPrice() - currentTier.MonthlyPrice(),
		"upgrade_url":        "/api/v1/subscription/upgrade",
		"comparison_url":     fmt.Sprintf("/api/v1/subscription/compare?from=%s&to=%s", currentTier, requiredTier),
	}
}

func (m *SubscriptionMiddleware) getRetryAfter(usageType subscription.UsageType) string {
	switch usageType {
	case subscription.UsageBacktests:
		return "24h" // Daily limit
	case subscription.UsageAPICalls:
		return "1m" // Per minute limit
	case subscription.UsageDailyVolume:
		return "24h" // Daily limit
	default:
		return "1h"
	}
}

// GetSubscriptionFromContext retrieves the subscription from Gin context
func GetSubscriptionFromContext(c *gin.Context) (*subscription.UserSubscription, bool) {
	sub, exists := c.Get(SubscriptionKey)
	if !exists {
		return nil, false
	}

	userSub, ok := sub.(*subscription.UserSubscription)
	if !ok {
		return nil, false
	}

	return userSub, true
}

// GetSubscriptionFromRequestContext retrieves subscription from request context
func GetSubscriptionFromRequestContext(ctx context.Context) (*subscription.UserSubscription, bool) {
	sub := ctx.Value(SubscriptionKey)
	if sub == nil {
		return nil, false
	}

	userSub, ok := sub.(*subscription.UserSubscription)
	if !ok {
		return nil, false
	}

	return userSub, true
}

// GetSubscriptionTierFromContext retrieves the subscription tier from context
func GetSubscriptionTierFromContext(c *gin.Context) (subscription.SubscriptionTier, bool) {
	tierStr := c.GetString(SubscriptionTierKey)
	if tierStr == "" {
		return subscription.TierFree, false
	}

	tier, ok := subscription.ParseSubscriptionTier(tierStr)
	return tier, ok
}

// Convenience middleware functions for common tier requirements

// RequireFree is a shorthand for RequireSubscription(TierFree)
func (m *SubscriptionMiddleware) RequireFree() gin.HandlerFunc {
	return m.RequireSubscription(subscription.TierFree)
}

// RequireBasic is a shorthand for RequireSubscription(TierBasic)
func (m *SubscriptionMiddleware) RequireBasic() gin.HandlerFunc {
	return m.RequireSubscription(subscription.TierBasic)
}

// RequirePro is a shorthand for RequireSubscription(TierPro)
func (m *SubscriptionMiddleware) RequirePro() gin.HandlerFunc {
	return m.RequireSubscription(subscription.TierPro)
}

// RequireEnterprise is a shorthand for RequireSubscription(TierEnterprise)
func (m *SubscriptionMiddleware) RequireEnterprise() gin.HandlerFunc {
	return m.RequireSubscription(subscription.TierEnterprise)
}

// Convenience feature middleware functions

// RequireRealTrading requires the real trading feature
func (m *SubscriptionMiddleware) RequireRealTrading() gin.HandlerFunc {
	return m.RequireFeature(subscription.FeatureRealTrading)
}

// RequireBacktesting requires the backtesting feature
func (m *SubscriptionMiddleware) RequireBacktesting() gin.HandlerFunc {
	return m.RequireFeature(subscription.FeatureBacktesting)
}

// RequireAIBot requires the AI bot feature
func (m *SubscriptionMiddleware) RequireAIBot() gin.HandlerFunc {
	return m.RequireFeature(subscription.FeatureAIBot)
}

// RequireWebhooks requires the webhooks feature
func (m *SubscriptionMiddleware) RequireWebhooks() gin.HandlerFunc {
	return m.RequireFeature(subscription.FeatureWebhooks)
}

// ChainedMiddleware combines multiple subscription checks
type ChainedMiddleware struct {
	middlewares []gin.HandlerFunc
}

// Chain creates a new chained middleware
func (m *SubscriptionMiddleware) Chain(handlers ...gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		for _, handler := range handlers {
			handler(c)
			if c.IsAborted() {
				return
			}
		}
	}
}

// WithFeatureAndLimit combines feature and limit checks
func (m *SubscriptionMiddleware) WithFeatureAndLimit(feature subscription.Feature, usageType subscription.UsageType) gin.HandlerFunc {
	return m.Chain(
		m.RequireFeature(feature),
		m.RequireLimit(usageType),
	)
}

// WithTierAndFeature combines tier and feature checks
func (m *SubscriptionMiddleware) WithTierAndFeature(minTier subscription.SubscriptionTier, feature subscription.Feature) gin.HandlerFunc {
	return m.Chain(
		m.RequireSubscription(minTier),
		m.RequireFeature(feature),
	)
}

// GetSubscriptionID retrieves the subscription ID from Gin context
func GetSubscriptionID(c *gin.Context) (uuid.UUID, error) {
	subIDStr := c.GetString(SubscriptionIDKey)
	if subIDStr == "" {
		return uuid.Nil, fmt.Errorf("subscription_id not found in context")
	}
	return uuid.Parse(subIDStr)
}
