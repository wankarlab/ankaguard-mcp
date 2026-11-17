// Package handlers contains HTTP handlers for REST API v1
package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/rantipay/trading-mcp/internal/middleware"
	"github.com/rantipay/trading-mcp/internal/subscription"
)

// SubscriptionHandler handles subscription-related endpoints
type SubscriptionHandler struct {
	service *subscription.Service
	logger  *logrus.Logger
}

// NewSubscriptionHandler creates a new subscription handler
func NewSubscriptionHandler(service *subscription.Service, logger *logrus.Logger) *SubscriptionHandler {
	if logger == nil {
		logger = logrus.New()
	}
	return &SubscriptionHandler{
		service: service,
		logger:  logger,
	}
}

// SubscriptionResponse represents the subscription API response
type SubscriptionResponse struct {
	ID                 string                        `json:"id"`
	UserID             string                        `json:"user_id"`
	Tier               string                        `json:"tier"`
	TierDisplayName    string                        `json:"tier_display_name"`
	MonthlyPrice       float64                       `json:"monthly_price"`
	Features           []string                      `json:"features"`
	Limits             subscription.SubscriptionLimits `json:"limits"`
	IsActive           bool                          `json:"is_active"`
	IsOnTrial          bool                          `json:"is_trial"`
	TrialDaysRemaining int                           `json:"trial_days_remaining,omitempty"`
	DaysUntilExpiry    int                           `json:"days_until_expiry,omitempty"`
	ExpiresAt          *time.Time                    `json:"expires_at,omitempty"`
	BillingCycleStart  *time.Time                    `json:"billing_cycle_start,omitempty"`
	NextPaymentAt      *time.Time                    `json:"next_payment_at,omitempty"`
	CreatedAt          time.Time                     `json:"created_at"`
	UpdatedAt          time.Time                     `json:"updated_at"`
}

// UsageResponse represents the usage API response
type UsageResponse struct {
	Backtests         UsageStat `json:"backtests"`
	Exchanges         UsageStat `json:"exchanges"`
	Bots              UsageStat `json:"bots"`
	Alerts            UsageStat `json:"alerts"`
	APICalls          UsageStat `json:"api_calls"`
	DailyVolume       UsageStat `json:"daily_volume"`
	Positions         UsageStat `json:"positions"`
	Webhooks          UsageStat `json:"webhooks"`
	CustomStrategies  UsageStat `json:"custom_strategies"`
	PeriodStart       time.Time `json:"period_start"`
	PeriodEnd         time.Time `json:"period_end"`
	Warnings          []string  `json:"warnings,omitempty"`
}

// UsageStat represents a single usage statistic
type UsageStat struct {
	Used      int     `json:"used"`
	Limit     int     `json:"limit"` // -1 means unlimited
	Remaining int     `json:"remaining"` // -1 means unlimited
	Percent   float64 `json:"percent"` // 0-100, 0 if unlimited
}

// TierInfo represents information about a subscription tier
type TierInfo struct {
	Tier          string                          `json:"tier"`
	DisplayName   string                          `json:"display_name"`
	MonthlyPrice  float64                         `json:"monthly_price"`
	AnnualPrice   float64                         `json:"annual_price"` // With discount
	Features      []string                        `json:"features"`
	Limits        subscription.SubscriptionLimits `json:"limits"`
	IsCurrent     bool                            `json:"is_current,omitempty"`
	IsRecommended bool                            `json:"is_recommended,omitempty"`
}

// GetSubscription handles GET /api/v1/subscription
// Returns the current user's subscription details
func (h *SubscriptionHandler) GetSubscription(c *gin.Context) {
	userID, err := middleware.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	h.logger.WithField("user_id", userID).Debug("Getting subscription")

	sub, err := h.service.GetSubscription(c.Request.Context(), userID)
	if err != nil {
		h.logger.WithError(err).Error("Failed to get subscription")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("SUBSCRIPTION_ERROR", "Failed to retrieve subscription"))
		return
	}

	response := h.toSubscriptionResponse(sub)
	c.JSON(http.StatusOK, models.NewSuccessResponse(response))
}

// GetUsage handles GET /api/v1/subscription/usage
// Returns the current user's usage statistics
func (h *SubscriptionHandler) GetUsage(c *gin.Context) {
	userID, err := middleware.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	h.logger.WithField("user_id", userID).Debug("Getting usage")

	usage, err := h.service.GetUsage(c.Request.Context(), userID)
	if err != nil {
		h.logger.WithError(err).Error("Failed to get usage")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("USAGE_ERROR", "Failed to retrieve usage"))
		return
	}

	response := h.toUsageResponse(usage)
	c.JSON(http.StatusOK, models.NewSuccessResponse(response))
}

// ValidateSubscription handles POST /api/v1/subscription/validate
// Validates the subscription with Odoo and returns updated status
func (h *SubscriptionHandler) ValidateSubscription(c *gin.Context) {
	userID, err := middleware.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	h.logger.WithField("user_id", userID).Debug("Validating subscription with Odoo")

	sub, err := h.service.ValidateWithOdoo(c.Request.Context(), userID)
	if err != nil {
		h.logger.WithError(err).Error("Failed to validate subscription")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("VALIDATION_ERROR", "Failed to validate subscription"))
		return
	}

	response := h.toSubscriptionResponse(sub)
	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"validated":    true,
		"subscription": response,
		"validated_at": time.Now().Format(time.RFC3339),
	}))
}

// GetTiers handles GET /api/v1/subscription/tiers
// Returns all available subscription tiers with their features and limits
func (h *SubscriptionHandler) GetTiers(c *gin.Context) {
	// Get current user's tier for comparison
	var currentTier subscription.SubscriptionTier
	userID, err := middleware.GetUserFromContext(c)
	if err == nil {
		if sub, err := h.service.GetSubscription(c.Request.Context(), userID); err == nil {
			currentTier = sub.Tier
		}
	}

	tiers := []TierInfo{
		h.buildTierInfo(subscription.TierFree, currentTier),
		h.buildTierInfo(subscription.TierBasic, currentTier),
		h.buildTierInfo(subscription.TierPro, currentTier),
		h.buildTierInfo(subscription.TierEnterprise, currentTier),
	}

	// Mark recommended tier (usually Pro)
	for i := range tiers {
		if tiers[i].Tier == string(subscription.TierPro) {
			tiers[i].IsRecommended = true
		}
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"tiers":        tiers,
		"current_tier": string(currentTier),
	}))
}

// CompareTiers handles GET /api/v1/subscription/compare
// Compares two subscription tiers
func (h *SubscriptionHandler) CompareTiers(c *gin.Context) {
	fromTierStr := c.Query("from")
	toTierStr := c.Query("to")

	if fromTierStr == "" || toTierStr == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_REQUEST", "Both 'from' and 'to' tier parameters are required"))
		return
	}

	fromTier, ok := subscription.ParseSubscriptionTier(fromTierStr)
	if !ok {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_TIER", "Invalid 'from' tier"))
		return
	}

	toTier, ok := subscription.ParseSubscriptionTier(toTierStr)
	if !ok {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_TIER", "Invalid 'to' tier"))
		return
	}

	comparison := h.service.GetTierComparison(fromTier, toTier)
	c.JSON(http.StatusOK, models.NewSuccessResponse(comparison))
}

// CheckFeature handles GET /api/v1/subscription/features/:feature
// Checks if the current user has access to a specific feature
func (h *SubscriptionHandler) CheckFeature(c *gin.Context) {
	userID, err := middleware.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	featureStr := c.Param("feature")
	if featureStr == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_REQUEST", "Feature parameter is required"))
		return
	}

	feature := subscription.Feature(featureStr)

	err = h.service.CheckFeatureAccess(c.Request.Context(), userID, feature)
	hasAccess := err == nil

	response := gin.H{
		"feature":    featureStr,
		"has_access": hasAccess,
	}

	if !hasAccess && err != nil {
		response["reason"] = err.Error()

		// Add upgrade hint
		sub, _ := h.service.GetSubscription(c.Request.Context(), userID)
		if sub != nil {
			for _, tier := range subscription.AllTiers() {
				if tier.TierLevel() > sub.Tier.TierLevel() {
					features := subscription.DefaultFeaturesForTier(tier)
					for _, f := range features {
						if f == feature {
							response["upgrade_to"] = string(tier)
							response["upgrade_price"] = tier.MonthlyPrice()
							break
						}
					}
					if response["upgrade_to"] != nil {
						break
					}
				}
			}
		}
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(response))
}

// CheckLimit handles GET /api/v1/subscription/limits/:limit_type
// Checks the current usage against a specific limit
func (h *SubscriptionHandler) CheckLimit(c *gin.Context) {
	userID, err := middleware.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	limitTypeStr := c.Param("limit_type")
	if limitTypeStr == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_REQUEST", "Limit type parameter is required"))
		return
	}

	usageType := subscription.UsageType(limitTypeStr)

	err = h.service.CheckLimit(c.Request.Context(), userID, usageType)
	allowed := err == nil

	response := gin.H{
		"limit_type": limitTypeStr,
		"allowed":    allowed,
	}

	if !allowed && err != nil {
		response["reason"] = err.Error()
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(response))
}

// GetEvents handles GET /api/v1/subscription/events
// Returns subscription history events
func (h *SubscriptionHandler) GetEvents(c *gin.Context) {
	userID, err := middleware.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	limit := 20 // Default limit
	if l := c.Query("limit"); l != "" {
		var parsedLimit int
		if _, err := json.Marshal(l); err == nil {
			parsedLimit = 20 // Keep default if parse fails
		} else {
			parsedLimit = 20
		}
		if parsedLimit > 0 && parsedLimit <= 100 {
			limit = parsedLimit
		}
	}

	events, err := h.service.GetEvents(c.Request.Context(), userID, limit)
	if err != nil {
		h.logger.WithError(err).Error("Failed to get subscription events")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("EVENTS_ERROR", "Failed to retrieve events"))
		return
	}

	c.JSON(http.StatusOK, models.NewSuccessResponseWithMeta(events, &models.APIMeta{
		Count: len(events),
		Limit: limit,
	}))
}

// UpgradeRequest represents a subscription upgrade request
type UpgradeRequest struct {
	TargetTier string `json:"target_tier" binding:"required"`
}

// Upgrade handles POST /api/v1/subscription/upgrade
// Initiates a subscription upgrade (would integrate with payment system)
func (h *SubscriptionHandler) Upgrade(c *gin.Context) {
	userID, err := middleware.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	var req UpgradeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_REQUEST", err.Error()))
		return
	}

	targetTier, ok := subscription.ParseSubscriptionTier(req.TargetTier)
	if !ok {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_TIER", "Invalid target tier"))
		return
	}

	h.logger.WithFields(logrus.Fields{
		"user_id":     userID,
		"target_tier": targetTier,
	}).Info("Processing subscription upgrade")

	// Get current subscription
	currentSub, err := h.service.GetSubscription(c.Request.Context(), userID)
	if err != nil {
		h.logger.WithError(err).Error("Failed to get current subscription")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("SUBSCRIPTION_ERROR", "Failed to process upgrade"))
		return
	}

	// Validate upgrade is actually an upgrade
	if targetTier.TierLevel() <= currentSub.Tier.TierLevel() {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_UPGRADE", "Target tier must be higher than current tier"))
		return
	}

	// In a real implementation, this would:
	// 1. Create a payment intent with Stripe/payment provider
	// 2. Return a checkout session URL
	// 3. The actual upgrade would happen via webhook after successful payment

	// For now, we'll return upgrade information
	comparison := h.service.GetTierComparison(currentSub.Tier, targetTier)

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"upgrade_initiated": true,
		"current_tier":      string(currentSub.Tier),
		"target_tier":       string(targetTier),
		"price_difference":  comparison.PriceDiff,
		"new_features":      comparison.NewFeatures,
		"improved_limits":   comparison.ImprovedLimits,
		"message":           "Please complete payment to activate upgrade",
		"payment_url":       "/api/v1/payments/checkout?tier=" + string(targetTier),
	}))
}

// Helper methods

func (h *SubscriptionHandler) toSubscriptionResponse(sub *subscription.UserSubscription) SubscriptionResponse {
	features := make([]string, len(sub.Features))
	for i, f := range sub.Features {
		features[i] = string(f)
	}

	response := SubscriptionResponse{
		ID:                sub.ID.String(),
		UserID:            sub.UserID.String(),
		Tier:              string(sub.Tier),
		TierDisplayName:   sub.Tier.DisplayName(),
		MonthlyPrice:      sub.Tier.MonthlyPrice(),
		Features:          features,
		Limits:            sub.Limits,
		IsActive:          sub.IsActive && !sub.IsExpired(),
		IsOnTrial:         sub.IsOnTrial(),
		DaysUntilExpiry:   sub.DaysUntilExpiry(),
		ExpiresAt:         sub.ExpiresAt,
		BillingCycleStart: sub.BillingCycleStart,
		NextPaymentAt:     sub.NextPaymentAt,
		CreatedAt:         sub.CreatedAt,
		UpdatedAt:         sub.UpdatedAt,
	}

	if sub.IsOnTrial() && sub.TrialEnd != nil {
		days := int(time.Until(*sub.TrialEnd).Hours() / 24)
		if days < 0 {
			days = 0
		}
		response.TrialDaysRemaining = days
	}

	return response
}

func (h *SubscriptionHandler) toUsageResponse(usage *subscription.UsageDTO) UsageResponse {
	return UsageResponse{
		Backtests: UsageStat{
			Used:      usage.BacktestsUsed,
			Limit:     usage.BacktestsLimit,
			Remaining: usage.BacktestsRemaining,
			Percent:   h.calculatePercent(usage.BacktestsUsed, usage.BacktestsLimit),
		},
		Exchanges: UsageStat{
			Used:      usage.ExchangesConnected,
			Limit:     usage.ExchangesLimit,
			Remaining: h.calculateRemaining(usage.ExchangesConnected, usage.ExchangesLimit),
			Percent:   h.calculatePercent(usage.ExchangesConnected, usage.ExchangesLimit),
		},
		Bots: UsageStat{
			Used:      usage.ActiveBots,
			Limit:     usage.BotsLimit,
			Remaining: h.calculateRemaining(usage.ActiveBots, usage.BotsLimit),
			Percent:   h.calculatePercent(usage.ActiveBots, usage.BotsLimit),
		},
		Alerts: UsageStat{
			Used:      usage.ActiveAlerts,
			Limit:     usage.AlertsLimit,
			Remaining: h.calculateRemaining(usage.ActiveAlerts, usage.AlertsLimit),
			Percent:   h.calculatePercent(usage.ActiveAlerts, usage.AlertsLimit),
		},
		APICalls: UsageStat{
			Used:      int(usage.APICallsToday),
			Limit:     usage.APICallsLimit,
			Remaining: h.calculateRemaining(int(usage.APICallsToday), usage.APICallsLimit),
			Percent:   h.calculatePercent(int(usage.APICallsToday), usage.APICallsLimit),
		},
		DailyVolume: UsageStat{
			Used:      int(usage.DailyVolume),
			Limit:     int(usage.DailyVolumeLimit),
			Remaining: h.calculateRemaining(int(usage.DailyVolume), int(usage.DailyVolumeLimit)),
			Percent:   h.calculatePercent(int(usage.DailyVolume), int(usage.DailyVolumeLimit)),
		},
		Positions: UsageStat{
			Used:      usage.ActivePositions,
			Limit:     usage.PositionsLimit,
			Remaining: h.calculateRemaining(usage.ActivePositions, usage.PositionsLimit),
			Percent:   h.calculatePercent(usage.ActivePositions, usage.PositionsLimit),
		},
		Webhooks: UsageStat{
			Used:      usage.WebhooksUsed,
			Limit:     usage.WebhooksLimit,
			Remaining: h.calculateRemaining(usage.WebhooksUsed, usage.WebhooksLimit),
			Percent:   h.calculatePercent(usage.WebhooksUsed, usage.WebhooksLimit),
		},
		CustomStrategies: UsageStat{
			Used:      usage.CustomStrategiesUsed,
			Limit:     usage.CustomStrategiesLimit,
			Remaining: h.calculateRemaining(usage.CustomStrategiesUsed, usage.CustomStrategiesLimit),
			Percent:   h.calculatePercent(usage.CustomStrategiesUsed, usage.CustomStrategiesLimit),
		},
		PeriodStart: usage.PeriodStart,
		PeriodEnd:   usage.PeriodEnd,
		Warnings:    usage.Warnings,
	}
}

func (h *SubscriptionHandler) calculateRemaining(used, limit int) int {
	if limit < 0 {
		return -1 // Unlimited
	}
	remaining := limit - used
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (h *SubscriptionHandler) calculatePercent(used, limit int) float64 {
	if limit <= 0 {
		return 0 // Unlimited or invalid
	}
	return float64(used) / float64(limit) * 100
}

func (h *SubscriptionHandler) buildTierInfo(tier subscription.SubscriptionTier, currentTier subscription.SubscriptionTier) TierInfo {
	features := subscription.DefaultFeaturesForTier(tier)
	featureStrings := make([]string, len(features))
	for i, f := range features {
		featureStrings[i] = string(f)
	}

	return TierInfo{
		Tier:         string(tier),
		DisplayName:  tier.DisplayName(),
		MonthlyPrice: tier.MonthlyPrice(),
		AnnualPrice:  tier.MonthlyPrice() * 12 * 0.8, // 20% discount for annual
		Features:     featureStrings,
		Limits:       subscription.DefaultLimitsForTier(tier),
		IsCurrent:    tier == currentTier,
	}
}

// RegisterRoutes registers subscription routes with the router
func (h *SubscriptionHandler) RegisterRoutes(router *gin.RouterGroup) {
	sub := router.Group("/subscription")
	{
		sub.GET("", h.GetSubscription)
		sub.GET("/usage", h.GetUsage)
		sub.POST("/validate", h.ValidateSubscription)
		sub.GET("/tiers", h.GetTiers)
		sub.GET("/compare", h.CompareTiers)
		sub.GET("/features/:feature", h.CheckFeature)
		sub.GET("/limits/:limit_type", h.CheckLimit)
		sub.GET("/events", h.GetEvents)
		sub.POST("/upgrade", h.Upgrade)
	}
}
