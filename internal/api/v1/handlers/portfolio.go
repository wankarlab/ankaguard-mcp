// Package handlers contains HTTP handlers for REST API v1
package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/rantipay/trading-mcp/internal/config/dynamic"
	configdomain "github.com/rantipay/trading-mcp/internal/config/dynamic/domain"
	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	tradingModels "github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/rantipay/trading-mcp/internal/trading/middleware"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// PortfolioHandler handles portfolio-related endpoints
type PortfolioHandler struct {
	gctClient   *gctclient.Client
	sessionMgr  *gct.SessionManager  // optional; nil disables per-user session path
	configStore *dynamic.ConfigStore // optional; nil falls back to viper/YAML
	logger      *logrus.Logger
}

// NewPortfolioHandler creates a new portfolio handler
func NewPortfolioHandler(gctClient *gctclient.Client, logger *logrus.Logger) *PortfolioHandler {
	return &PortfolioHandler{
		gctClient: gctClient,
		logger:    logger,
	}
}

// WithSessionManager returns a copy of the handler with the given SessionManager wired in.
// Call this after NewPortfolioHandler when per-user session support is desired.
func (h *PortfolioHandler) WithSessionManager(sm *gct.SessionManager) *PortfolioHandler {
	h.sessionMgr = sm
	return h
}

// SetConfigStore wires the dynamic config store so the portfolio overview reads
// live bot parameters (trade size, leverage, daily-loss cap, max open positions,
// confidence threshold) instead of the static viper/YAML snapshot. When nil the
// handler falls back to viper with the same hardcoded defaults as before (audit P1).
func (h *PortfolioHandler) SetConfigStore(s *dynamic.ConfigStore) {
	h.configStore = s
}

// floatPref resolves a numeric config value with the precedence:
//  1. dynamic config snapshot (manifest override/default) when the key is registered,
//  2. viper (YAML/env) when the key is set there,
//  3. the supplied hardcoded fallback (logged at WARN so the silent default is visible).
//
// Keys that are not in the manifest (e.g. bot.trade_size_usd / bot.leverage /
// bot.max_daily_loss_usd, which remain YAML-only) transparently fall through to
// viper, preserving the previous behaviour while letting registered keys be tuned live.
func (h *PortfolioHandler) floatPref(snap *dynamic.ScopedSnapshot, key string, fallback float64) float64 {
	if snap != nil {
		if v, ok := snap.FloatOk(key); ok {
			return v
		}
	}
	if viper.IsSet(key) {
		if v := viper.GetFloat64(key); v != 0 {
			return v
		}
	}
	h.logger.WithFields(logrus.Fields{
		"key":      key,
		"fallback": fallback,
	}).Warn("portfolio overview: config key not set in dynamic store or viper — using hardcoded default")
	return fallback
}

// GetPortfolio handles GET /api/v1/portfolio
// Returns the user's complete portfolio summary.
// Falls back to the global GCT client (single-operator / legacy mode) when no
// per-user session can be resolved — ensures existing single-operator usage
// is fully backward-compatible.
func (h *PortfolioHandler) GetPortfolio(c *gin.Context) {
	userID, exists := middleware.GetUserIDFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	h.logger.WithField("user_id", userID).Debug("Getting portfolio")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	// Global-client path (covers legacy single-operator and "default" userID).
	// Per-user portfolio aggregation is not yet implemented in gctclient (no
	// GetPortfolioForUser method); when added, wire via sessionMgr here.
	portfolio, err := h.gctClient.GetPortfolio(ctx)
	if err != nil {
		h.logger.WithError(err).Error("Failed to get portfolio")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("PORTFOLIO_ERROR", "Failed to retrieve portfolio"))
		return
	}

	response := convertPortfolioToResponse(portfolio)
	c.JSON(http.StatusOK, models.NewSuccessResponse(response))
}

// GetBalances handles GET /api/v1/portfolio/balances
// Returns balances for all or specific exchanges/currencies.
// When a SessionManager is configured and the requesting user has stored credentials,
// a specific currency query uses the per-user session (GetBalanceForUser). Falls back
// to the global GCT client so existing single-operator usage is unchanged.
func (h *PortfolioHandler) GetBalances(c *gin.Context) {
	userID, exists := middleware.GetUserIDFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	var req models.GetBalancesRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_REQUEST", err.Error()))
		return
	}

	h.logger.WithFields(logrus.Fields{
		"user_id":  userID,
		"exchange": req.Exchange,
		"currency": req.Currency,
	}).Debug("Getting balances")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	exchange := req.Exchange
	if exchange == "" {
		exchange = "kucoin"
	}

	// Per-user path: specific-currency balance via isolated session.
	if req.Currency != "" && h.sessionMgr != nil && userID != "default" {
		session, sessionErr := h.sessionMgr.GetSession(ctx, userID, exchange)
		if sessionErr == nil {
			balance, err := h.gctClient.GetBalanceForUser(ctx, session, req.Currency)
			if err != nil {
				h.logger.WithError(err).Warn("GetBalanceForUser failed, falling back to global client")
				// Fall through to global-client path.
			} else {
				response := models.BalanceResponse{
					Exchange:  balance.Exchange,
					Currency:  balance.Currency,
					Available: balance.Available,
					Locked:    balance.Locked,
					Total:     balance.Total,
				}
				c.JSON(http.StatusOK, models.NewSuccessResponse(response))
				return
			}
		} else {
			h.logger.WithError(sessionErr).WithField("user_id", userID).
				Debug("No session for user, using global client for balance")
		}
	}

	// Global-client fallback: specific exchange + currency.
	if req.Exchange != "" && req.Currency != "" {
		balance, err := h.gctClient.GetBalance(ctx, req.Exchange, req.Currency)
		if err != nil {
			h.logger.WithError(err).Error("Failed to get balance")
			c.JSON(http.StatusInternalServerError, models.NewErrorResponse("BALANCE_ERROR", "Failed to retrieve balance"))
			return
		}

		response := models.BalanceResponse{
			Exchange:  balance.Exchange,
			Currency:  balance.Currency,
			Available: balance.Available,
			Locked:    balance.Locked,
			Total:     balance.Total,
		}
		c.JSON(http.StatusOK, models.NewSuccessResponse(response))
		return
	}

	// Get portfolio for all balances (global client, no per-user session needed here).
	portfolio, err := h.gctClient.GetPortfolio(ctx)
	if err != nil {
		h.logger.WithError(err).Error("Failed to get portfolio")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("BALANCE_ERROR", "Failed to retrieve balances"))
		return
	}

	balances := make([]models.BalanceResponse, 0, len(portfolio.Holdings))
	totalValue := 0.0

	for _, holding := range portfolio.Holdings {
		balance := models.BalanceResponse{
			Exchange:  req.Exchange, // Will be empty if not specified
			Currency:  holding.Currency,
			Available: holding.Amount,
			Total:     holding.Amount,
			ValueUSD:  holding.ValueUSD,
		}
		balances = append(balances, balance)
		totalValue += holding.ValueUSD
	}

	response := models.BalancesListResponse{
		Balances:      balances,
		TotalValueUSD: totalValue,
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(response))
}

// GetPositions handles GET /api/v1/positions
// Returns all open positions.
// When a SessionManager is configured and the requesting user has stored credentials,
// positions are fetched via the per-user session (GetFuturesPositionsForUser). Falls
// back to the global GCT client so existing single-operator usage is unchanged.
func (h *PortfolioHandler) GetPositions(c *gin.Context) {
	userID, exists := middleware.GetUserIDFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	var req models.GetPositionsRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_REQUEST", err.Error()))
		return
	}

	h.logger.WithFields(logrus.Fields{
		"user_id":  userID,
		"exchange": req.Exchange,
		"pair":     req.Pair,
	}).Debug("Getting positions")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	exchange := req.Exchange
	if exchange == "" {
		exchange = "kucoin" // Default exchange
	}

	// Per-user path: fetch positions via isolated session.
	var gctPositions []*gct.Position
	if h.sessionMgr != nil && userID != "default" {
		session, sessionErr := h.sessionMgr.GetSession(ctx, userID, exchange)
		if sessionErr == nil {
			userPositions, err := h.gctClient.GetFuturesPositionsForUser(ctx, session)
			if err != nil {
				h.logger.WithError(err).Warn("GetFuturesPositionsForUser failed, falling back to global client")
				// Fall through to global-client path.
			} else {
				gctPositions = userPositions
			}
		} else {
			h.logger.WithError(sessionErr).WithField("user_id", userID).
				Debug("No session for user, using global client for positions")
		}
	}

	// Global-client fallback when per-user path was not taken.
	if gctPositions == nil {
		var err error
		gctPositions, err = h.gctClient.GetFuturesPositions(ctx, exchange)
		if err != nil {
			h.logger.WithError(err).Warn("Failed to get futures positions, returning empty list")
			// Return empty list instead of error for better UX.
			response := models.PositionsListResponse{
				Positions:          []models.PositionResponse{},
				TotalUnrealizedPnL: 0,
				TotalRealizedPnL:   0,
			}
			c.JSON(http.StatusOK, models.NewSuccessResponse(response))
			return
		}
	}

	positions := make([]models.PositionResponse, 0, len(gctPositions))
	totalUnrealizedPnL := 0.0
	totalRealizedPnL := 0.0

	for i, pos := range gctPositions {
		// Filter by pair if specified
		if req.Pair != "" && pos.Symbol != req.Pair {
			continue
		}

		position := models.PositionResponse{
			ID:               fmt.Sprintf("%s-%d", exchange, i), // Generate ID from exchange and index
			Exchange:         exchange,
			Pair:             pos.Symbol,
			Side:             pos.Side,
			Size:             pos.Size,
			EntryPrice:       pos.EntryPrice,
			CurrentPrice:     pos.CurrentPrice,
			MarkPrice:        pos.CurrentPrice, // Use CurrentPrice as MarkPrice if not available
			Leverage:         float64(pos.Leverage),
			Margin:           pos.Margin,
			UnrealizedPnL:    pos.UnrealizedPnL,
			RealizedPnL:      pos.RealizedPnL,
			LiquidationPrice: pos.LiquidationPrice,
			CreatedAt:        pos.UpdatedAt, // Use UpdatedAt as CreatedAt
			UpdatedAt:        pos.UpdatedAt,
		}
		positions = append(positions, position)
		totalUnrealizedPnL += pos.UnrealizedPnL
		totalRealizedPnL += pos.RealizedPnL
	}

	response := models.PositionsListResponse{
		Positions:          positions,
		TotalUnrealizedPnL: totalUnrealizedPnL,
		TotalRealizedPnL:   totalRealizedPnL,
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(response))
}

// botPositionView is the per-position adoption snapshot returned by GetBotPositions.
// notional_usd / prices / pnl are exchange-reported floats (display values, not
// money ledger fields). Adoption flags are derived from the live bot config.
type botPositionView struct {
	Symbol        string  `json:"symbol"`
	Side          string  `json:"side"`
	Size          float64 `json:"size"`
	NotionalUSD   float64 `json:"notional_usd"`
	EntryPrice    float64 `json:"entry_price"`
	MarkPrice     float64 `json:"mark_price"`
	UnrealizedPnL float64 `json:"unrealized_pnl"`
	Adopted       bool    `json:"adopted"`
	HasExchangeSL bool    `json:"has_exchange_sl"`
	Source        string  `json:"source"`
}

// GetBotPositions handles GET /api/v1/bot/positions.
// It returns every open exchange position annotated with the bot's adoption state.
//
// Adoption is resolved from the live dynamic config flag
// bot.adopt.all_pairs.enabled, mirroring the bot's own matchesPair() logic: when
// cross-pair adoption is enabled the bot actively manages (and places exchange
// stop-losses on) every open position, so adopted=true / source="bot" /
// has_exchange_sl=true. When the flag is off the handler cannot determine the
// single configured pair from config alone, so those positions are reported as
// adopted=false / source="manual" (documented Phase-2 limitation).
func (h *PortfolioHandler) GetBotPositions(c *gin.Context) {
	userID, exists := middleware.GetUserIDFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	exchange := c.DefaultQuery("exchange", "kucoin")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	// Fetch positions: prefer per-user session, fall back to global client.
	var gctPositions []*gct.Position
	if h.sessionMgr != nil && userID != "default" {
		session, sessionErr := h.sessionMgr.GetSession(ctx, userID, exchange)
		if sessionErr == nil {
			if userPositions, err := h.gctClient.GetFuturesPositionsForUser(ctx, session); err == nil {
				gctPositions = userPositions
			} else {
				h.logger.WithError(err).Warn("GetFuturesPositionsForUser failed, falling back to global client")
			}
		}
	}
	if gctPositions == nil {
		var err error
		gctPositions, err = h.gctClient.GetFuturesPositions(ctx, exchange)
		if err != nil {
			h.logger.WithError(err).Warn("Failed to get futures positions, returning empty list")
			c.JSON(http.StatusOK, models.NewSuccessResponse([]botPositionView{}))
			return
		}
	}

	// Resolve the cross-pair adoption flag from the live config snapshot.
	adoptAll := false
	if h.configStore != nil {
		snap := h.configStore.SnapshotForScope([]dynamic.ScopeRef{{Type: configdomain.ScopeGlobal, ID: "*"}})
		adoptAll = snap.Bool("bot.adopt.all_pairs.enabled")
	}

	out := make([]botPositionView, 0, len(gctPositions))
	for _, pos := range gctPositions {
		if pos == nil {
			continue
		}
		// notional = |contracts| * mark price * contract multiplier (base units
		// per contract; e.g. 0.01 for QNTUSDTM). Without the multiplier the
		// exposure is overstated by 1/multiplier (QNT showed 785 vs 7.85).
		notional := pos.Size * pos.CurrentPrice * h.contractMultiplier(ctx, exchange, pos.Symbol)
		if notional < 0 {
			notional = -notional
		}

		// When cross-pair adoption is on the bot manages every open position and
		// keeps an exchange SL on it. Otherwise it is treated as a manual position.
		adopted := adoptAll
		source := "manual"
		if adopted {
			source = "bot"
		}

		out = append(out, botPositionView{
			Symbol:        pos.Symbol,
			Side:          pos.Side,
			Size:          pos.Size,
			NotionalUSD:   notional,
			EntryPrice:    pos.EntryPrice,
			MarkPrice:     pos.CurrentPrice,
			UnrealizedPnL: pos.UnrealizedPnL,
			Adopted:       adopted,
			HasExchangeSL: adopted,
			Source:        source,
		})
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(out))
}

// GetPortfolioOverview handles GET /api/v1/portfolio/overview
// Returns a comprehensive portfolio snapshot: spot wallet, futures wallet, open positions
// with risk metrics, account health, and bot decision context.
func (h *PortfolioHandler) GetPortfolioOverview(c *gin.Context) {
	exchange := c.DefaultQuery("exchange", "kucoin")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	overview, err := h.gctClient.GetPortfolioOverview(ctx, exchange)
	if err != nil {
		h.logger.WithError(err).Error("Failed to get portfolio overview")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("PORTFOLIO_OVERVIEW_ERROR", "Failed to retrieve portfolio overview"))
		return
	}

	// Enrich BotContext. Prefer the live dynamic config snapshot (hot-reloaded);
	// fall back to viper/YAML only when a key is not configured, and finally to the
	// historic hardcoded default so the response never carries a zero value.
	// Audit P1: previously this read viper exclusively, ignoring runtime overrides.
	var snap *dynamic.ScopedSnapshot
	if h.configStore != nil {
		snap = h.configStore.SnapshotForScope([]dynamic.ScopeRef{{Type: configdomain.ScopeGlobal, ID: "*"}})
	}

	tradeSize := h.floatPref(snap, "bot.trade_size_usd", 2.0)
	leverage := int(h.floatPref(snap, "bot.leverage", 5))
	maxDailyLoss := h.floatPref(snap, "bot.max_daily_loss_usd", 20.0)
	maxOpenPositions := int(h.floatPref(snap, "bot.max_open_positions", 1))
	confidenceThreshold := h.floatPref(snap, "bot.confidence_threshold", 0.65)

	positionsUsed := len(overview.Positions)
	canEnter := overview.Risk.CanOpenPosition && positionsUsed < maxOpenPositions
	cannotReason := ""
	if !overview.Risk.CanOpenPosition {
		cannotReason = "account health: " + overview.Risk.AccountHealth
	} else if positionsUsed >= maxOpenPositions {
		cannotReason = fmt.Sprintf("max open positions reached (%d/%d)", positionsUsed, maxOpenPositions)
	}

	overview.BotContext = gct.BotContext{
		ConfiguredTradeSizeUSD: tradeSize,
		ConfiguredLeverage:     leverage,
		MaxDailyLossUSD:        maxDailyLoss,
		CapitalForBotUSD:       overview.Futures.AvailableBalance,
		PositionsUsed:          positionsUsed,
		PositionsMax:           maxOpenPositions,
		CanEnterTrade:          canEnter,
		CannotEnterReason:      cannotReason,
		ConfidenceThreshold:    confidenceThreshold,
	}
	overview.Risk.DailyLossLimitUSD = maxDailyLoss
	overview.Risk.DailyLossRemainingUSD = maxDailyLoss - overview.Risk.DailyLossUSD

	c.JSON(http.StatusOK, models.NewSuccessResponse(overview))
}

// convertPortfolioToResponse converts internal portfolio model to API response
func convertPortfolioToResponse(portfolio *tradingModels.Portfolio) *models.PortfolioResponse {
	if portfolio == nil {
		return &models.PortfolioResponse{
			Holdings:  make(map[string]*models.HoldingResponse),
			UpdatedAt: time.Now(),
		}
	}

	holdings := make(map[string]*models.HoldingResponse)
	for currency, holding := range portfolio.Holdings {
		holdings[currency] = &models.HoldingResponse{
			Currency: holding.Currency,
			Amount:   holding.Amount,
			ValueUSD: holding.ValueUSD,
			AvgPrice: holding.AvgPrice,
		}
	}

	return &models.PortfolioResponse{
		TotalValueUSD: portfolio.TotalValueUSD,
		Holdings:      holdings,
		PnL:           portfolio.PnL,
		PnLPercent:    portfolio.PnLPercent,
		UpdatedAt:     portfolio.Timestamp,
	}
}

// contractMultiplierGetter is the narrow capability the concrete internal/gct
// client exposes (not part of gct.GCTClient so test mocks stay small).
type contractMultiplierGetter interface {
	ContractMultiplier(ctx context.Context, exchange, symbol string) (float64, error)
}

// contractMultiplier returns the futures contract face value for a position
// symbol in either "QNT-USDT" or "QNTUSDTM" form. Falls back to 1.0 (the
// previous behavior) when it cannot be resolved.
func (h *PortfolioHandler) contractMultiplier(ctx context.Context, exchange, symbol string) float64 {
	if h.gctClient == nil {
		return 1.0
	}
	getter, ok := h.gctClient.GetBaseClient().(contractMultiplierGetter)
	if !ok {
		return 1.0
	}
	sym := toFuturesSymbol(symbol)
	if strings.HasPrefix(sym, "BTCUSDT") { // KuCoin lists Bitcoin as XBT
		sym = "XBT" + strings.TrimPrefix(sym, "BTC")
	}
	m, err := getter.ContractMultiplier(ctx, exchange, sym)
	if err != nil || m <= 0 {
		return 1.0
	}
	return m
}
