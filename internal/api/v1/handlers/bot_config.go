// Package handlers — BotConfigHandler exposes per-user bot control endpoints (MU-5.2).
package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/rantipay/trading-mcp/internal/services"
	"github.com/rantipay/trading-mcp/internal/storage"
	"github.com/rantipay/trading-mcp/internal/subscription"
	"github.com/rantipay/trading-mcp/internal/trading/middleware"
	"github.com/sirupsen/logrus"
)

// BotConfigHandler exposes per-user bot configuration and lifecycle endpoints.
// It owns exactly the user's own bot — ownership is inherently enforced because
// all operations derive userID from the validated JWT claim.
type BotConfigHandler struct {
	botManager      *services.BotManager
	cfgRepo         *storage.UserBotConfigRepository
	logger          *logrus.Logger
	subscriptionSvc *subscription.Service
}

// NewBotConfigHandler creates a BotConfigHandler.
// botManager and cfgRepo must be non-nil for the handler to function.
func NewBotConfigHandler(
	botManager *services.BotManager,
	cfgRepo *storage.UserBotConfigRepository,
	logger *logrus.Logger,
) *BotConfigHandler {
	if logger == nil {
		logger = logrus.New()
	}
	return &BotConfigHandler{
		botManager: botManager,
		cfgRepo:    cfgRepo,
		logger:     logger,
	}
}

// SetSubscriptionService wires the subscription service used by BOT-06's
// bot-quota check (ValidateMaxBotsQuota). Optional: nil skips the quota
// check (only the real-trading-tier check still applies), logged as a warning
// so a missing wiring is visible rather than silently permissive.
func (h *BotConfigHandler) SetSubscriptionService(svc *subscription.Service) {
	h.subscriptionSvc = svc
}

// GetConfig handles GET /api/v1/bot/config
// Returns the persisted bot configuration for the requesting user.
func (h *BotConfigHandler) GetConfig(c *gin.Context) {
	userID := userIDFromContext(c)
	exchange := c.DefaultQuery("exchange", "kucoin")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	cfg, err := h.cfgRepo.Get(ctx, userID, exchange)
	if err != nil {
		h.logger.WithError(err).Error("Failed to get bot config")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponseWithDetails(
			"BOT_CONFIG_ERROR", "Failed to retrieve bot configuration", err.Error(),
		))
		return
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"user_id":              cfg.UserID,
		"exchange":             cfg.Exchange,
		"enabled":              cfg.Enabled,
		"trade_size_usd":       cfg.TradeSizeUSD,
		"leverage":             cfg.Leverage,
		"market_type":          cfg.MarketType,
		"mode_selection":       cfg.ModeSelection,
		"confidence_threshold": cfg.ConfidenceThreshold,
		"max_daily_loss_usd":   cfg.MaxDailyLossUSD,
		"max_open_positions":   cfg.MaxOpenPositions,
		"dry_run":              cfg.DryRun,
		"updated_at":           cfg.UpdatedAt,
	}))
}

// updateBotConfigRequest is the request body for PUT /api/v1/bot/config.
type updateBotConfigRequest struct {
	Exchange            string  `json:"exchange"`
	Enabled             *bool   `json:"enabled"`
	TradeSizeUSD        float64 `json:"trade_size_usd"`
	Leverage            int     `json:"leverage"`
	MarketType          string  `json:"market_type"`
	ModeSelection       string  `json:"mode_selection"`
	ConfidenceThreshold float64 `json:"confidence_threshold"`
	MaxDailyLossUSD     float64 `json:"max_daily_loss_usd"`
	MaxOpenPositions    int     `json:"max_open_positions"`
	DryRun              *bool   `json:"dry_run"`
}

// PutConfig handles PUT /api/v1/bot/config
// Validates and persists bot configuration; hot-reloads into running bot if active.
func (h *BotConfigHandler) PutConfig(c *gin.Context) {
	userID := userIDFromContext(c)

	var req updateBotConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_BODY", err.Error()))
		return
	}

	exchange := req.Exchange
	if exchange == "" {
		exchange = "kucoin"
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	// Load existing config as base so unset fields remain unchanged.
	existing, err := h.cfgRepo.Get(ctx, userID, exchange)
	if err != nil {
		h.logger.WithError(err).Error("Failed to load existing bot config")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponseWithDetails(
			"BOT_CONFIG_ERROR", "Failed to load existing bot configuration", err.Error(),
		))
		return
	}

	// Apply only non-zero / explicitly set fields from the request.
	if req.TradeSizeUSD > 0 {
		existing.TradeSizeUSD = req.TradeSizeUSD
	}
	if req.Leverage > 0 {
		existing.Leverage = req.Leverage
	}
	if req.MarketType != "" {
		existing.MarketType = req.MarketType
	}
	if req.ModeSelection != "" {
		existing.ModeSelection = req.ModeSelection
	}
	if req.ConfidenceThreshold > 0 {
		existing.ConfidenceThreshold = req.ConfidenceThreshold
	}
	if req.MaxDailyLossUSD > 0 {
		existing.MaxDailyLossUSD = req.MaxDailyLossUSD
	}
	if req.MaxOpenPositions > 0 {
		existing.MaxOpenPositions = req.MaxOpenPositions
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	if req.DryRun != nil {
		existing.DryRun = *req.DryRun
	}

	// BOT-06 fix: reject turning off dry_run (enabling real-money trading) for
	// accounts whose subscription tier doesn't allow it. ValidateRealTradingAccess
	// returns nil immediately when dryRun=true, so this is a no-op for paper
	// configs.
	kongLevel, _ := middleware.GetKongLevelFromContext(c)
	if err := services.ValidateRealTradingAccess(kongLevel, existing.DryRun); err != nil {
		c.JSON(http.StatusPaymentRequired, models.NewErrorResponseWithDetails(
			"TIER_UPGRADE_REQUIRED", "Real-money trading is not available on your subscription tier", err.Error(),
		))
		return
	}

	// Build the services.UserBotCfg that UpdateConfig expects.
	svcCfg := &services.UserBotCfg{
		UserID:              userID,
		Exchange:            existing.Exchange,
		Enabled:             existing.Enabled,
		TradeSizeUSD:        existing.TradeSizeUSD,
		Leverage:            existing.Leverage,
		MarketType:          existing.MarketType,
		ModeSelection:       existing.ModeSelection,
		ConfidenceThreshold: existing.ConfidenceThreshold,
		MaxDailyLossUSD:     existing.MaxDailyLossUSD,
		MaxOpenPositions:    existing.MaxOpenPositions,
		DryRun:              existing.DryRun,
		ConfigJSON:          existing.ConfigJSON,
	}

	if err := h.botManager.UpdateConfig(ctx, userID, svcCfg); err != nil {
		h.logger.WithError(err).Error("Failed to update bot config")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponseWithDetails(
			"BOT_CONFIG_ERROR", "Failed to update bot configuration", err.Error(),
		))
		return
	}

	h.logger.WithFields(logrus.Fields{
		"user_id":  userID,
		"exchange": existing.Exchange,
	}).Info("Bot configuration updated")

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"user_id":              userID,
		"exchange":             existing.Exchange,
		"trade_size_usd":       existing.TradeSizeUSD,
		"leverage":             existing.Leverage,
		"market_type":          existing.MarketType,
		"mode_selection":       existing.ModeSelection,
		"confidence_threshold": existing.ConfidenceThreshold,
		"max_daily_loss_usd":   existing.MaxDailyLossUSD,
		"max_open_positions":   existing.MaxOpenPositions,
		"dry_run":              existing.DryRun,
		"note":                 "config persisted and hot-reloaded if bot is running",
	}))
}

// StartBot handles POST /api/v1/bot/start
// Starts a trading bot for the requesting user.
func (h *BotConfigHandler) StartBot(c *gin.Context) {
	userID := userIDFromContext(c)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	// BOT-06 fix: ValidateRealTradingAccess/ValidateMaxBotsQuota existed as
	// dead code (no caller) — subscription-tier gating for real-money trading
	// and bot quota was never actually enforced before a bot could start.
	kongLevel, _ := middleware.GetKongLevelFromContext(c)
	exchange := c.DefaultQuery("exchange", "kucoin")
	ubCfg, err := h.cfgRepo.Get(ctx, userID, exchange)
	if err != nil {
		h.logger.WithError(err).WithField("user_id", userID).Error("StartBot: failed to load config for tier check")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponseWithDetails(
			"BOT_CONFIG_ERROR", "Failed to load bot configuration", err.Error(),
		))
		return
	}
	if err := services.ValidateRealTradingAccess(kongLevel, ubCfg.DryRun); err != nil {
		c.JSON(http.StatusPaymentRequired, models.NewErrorResponseWithDetails(
			"TIER_UPGRADE_REQUIRED", "Real-money trading is not available on your subscription tier", err.Error(),
		))
		return
	}
	if h.subscriptionSvc != nil {
		if userUUID, uuidErr := uuid.Parse(userID); uuidErr == nil {
			usage, usageErr := h.subscriptionSvc.GetUsage(ctx, userUUID)
			if usageErr != nil {
				h.logger.WithError(usageErr).WithField("user_id", userID).Warn("StartBot: failed to load subscription usage for quota check — allowing (fail-open on this specific lookup, not the tier gate above)")
			} else if err := services.ValidateMaxBotsQuota(usage.ActiveBots, usage.BotsLimit); err != nil {
				c.JSON(http.StatusPaymentRequired, models.NewErrorResponseWithDetails(
					"BOT_QUOTA_EXCEEDED", "Bot quota exceeded for your subscription tier", err.Error(),
				))
				return
			}
		}
	} else {
		h.logger.Warn("StartBot: subscriptionSvc not wired — bot quota is NOT enforced (only the real-trading tier gate is active)")
	}

	if err := h.botManager.StartBot(ctx, userID); err != nil {
		h.logger.WithError(err).WithField("user_id", userID).Error("Failed to start bot")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponseWithDetails(
			"BOT_START_ERROR", "Failed to start trading bot", err.Error(),
		))
		return
	}

	h.logger.WithField("user_id", userID).Info("Bot started via REST")
	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"user_id": userID,
		"status":  "started",
		"message": "Trading bot started successfully",
	}))
}

// StopBot handles POST /api/v1/bot/stop
// Stops the trading bot for the requesting user.
func (h *BotConfigHandler) StopBot(c *gin.Context) {
	userID := userIDFromContext(c)

	if err := h.botManager.StopBot(userID); err != nil {
		h.logger.WithError(err).WithField("user_id", userID).Error("Failed to stop bot")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponseWithDetails(
			"BOT_STOP_ERROR", "Failed to stop trading bot", err.Error(),
		))
		return
	}

	h.logger.WithField("user_id", userID).Info("Bot stopped via REST")
	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"user_id": userID,
		"status":  "stopped",
		"message": "Trading bot stopped successfully",
	}))
}

// GetStatus handles GET /api/v1/bot/status
// Returns the runtime status of the requesting user's bot.
func (h *BotConfigHandler) GetStatus(c *gin.Context) {
	userID := userIDFromContext(c)

	status := h.botManager.GetStatus(userID)
	if status == nil {
		c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
			"user_id": userID,
			"running": false,
			"message": "No bot running for this user",
		}))
		return
	}

	status["running"] = true
	c.JSON(http.StatusOK, models.NewSuccessResponse(status))
}

// EmergencyStop handles POST /api/v1/bot/emergency-stop
// Immediately closes all of the user's open positions (through their isolated
// exchange session) and then stops the bot. This is the hard-stop for
// real-money safety, distinct from /bot/stop which leaves positions open.
func (h *BotConfigHandler) EmergencyStop(c *gin.Context) {
	userID := userIDFromContext(c)

	if err := h.botManager.EmergencyStop(c.Request.Context(), userID); err != nil {
		h.logger.WithError(err).WithField("user_id", userID).Error("Emergency stop failed")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponseWithDetails(
			"EMERGENCY_STOP_ERROR", "Failed to emergency-stop trading bot", err.Error(),
		))
		return
	}

	h.logger.WithField("user_id", userID).Warn("Bot emergency-stopped via REST (positions closed)")
	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"user_id": userID,
		"status":  "emergency_stopped",
		"message": "Bot stopped and all open positions closed.",
	}))
}
