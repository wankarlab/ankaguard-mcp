package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rantipay/trading-mcp/internal/domain"
	"github.com/shopspring/decimal"
	"github.com/sirupsen/logrus"
)

// PositionManager manages open positions and their lifecycle
type PositionManager struct {
	positions      map[string]*ManagedPosition
	config         *PositionConfig
	log            *logrus.Logger
	mu             sync.RWMutex
	stopChan       chan struct{}
	running        bool
	orderExecutor  OrderExecutor
	aiOptimizer    *SLTPOptimizer              // AI optimizer for stop loss suggestions
	aiDecisionRepo domain.AIDecisionRepository // Repository for logging AI decisions
	aiCheckTicker  *time.Ticker                // Ticker for periodic AI analysis

	// Condition-based exit support (additive — does not replace SL/TP)
	indicatorProvider     IndicatorDataProvider // optional, injected via EnableConditionBasedExit
	exitConditionsEnabled bool
}

// PositionConfig defines position management behavior
type PositionConfig struct {
	AutoClose             bool    `yaml:"auto_close"`              // Enable automatic closing
	TrailingStop          bool    `yaml:"trailing_stop"`           // Enable trailing stop
	TrailingStopPct       float64 `yaml:"trailing_stop_pct"`       // Trailing stop percentage
	BreakevenAfterPct     float64 `yaml:"breakeven_after_pct"`     // Move SL to breakeven after X% profit
	PartialTakeProfits    bool    `yaml:"partial_take_profits"`    // Enable partial TP
	CheckInterval         int     `yaml:"check_interval"`          // Seconds between checks
	RequireConfirm        bool    `yaml:"require_confirm"`         // Require confirmation before closing
	AIManagement          bool    `yaml:"ai_management"`           // Enable AI-based SL/TP management
	AICheckInterval       int     `yaml:"ai_check_interval"`       // Seconds between AI analysis (default: 60)
	AIConfidenceThreshold float64 `yaml:"ai_confidence_threshold"` // Minimum confidence for auto-adjustment (default: 0.7)
}

// ManagedPosition represents a position being managed
//
// BOT-08/BOT-09: multiple goroutines (monitoringLoop, portfolioExitLoop,
// analysisLoop/executeTrade, and the breakeven/tightening stop-loss
// reissue path) obtain the SAME *ManagedPosition pointer via
// GetAllPositions()/GetPosition() and mutate its fields directly.
// PositionManager.mu only protects the map lookup, not the fields
// themselves. Mu below lets callers serialize field access on a single
// position; new call sites should take it around any read-then-write or
// multi-field update, especially StopLoss/ExchangeStopOrderID/Status.
// NOTE (partial fix): mu is wired into the exchange stop-loss
// cancel+place reissue path (services.AITradingBot.reissueExchangeStopLoss)
// and portfolioTightenSL's StopLoss write, which are the specific races the
// audit flagged. A full sweep of every field access across
// internal/services/ai_trading_bot.go (11k+ lines: monitorPositions,
// executeTrade, onPositionClosed, etc.) was not completed in this pass —
// see the PENDING note left in the audit doc for BOT-08.
type ManagedPosition struct {
	mu sync.Mutex

	ID           string
	Symbol       string
	Side         string // "buy" or "sell"
	EntryPrice   float64
	CurrentPrice float64 // Current market price
	Size         float64
	// NotionalUSD is the position's USD notional value. Size is dimensionally
	// ambiguous (USD target for bot-opened positions, exchange contracts for
	// adopted ones), so realized-PnL must be computed as (priceMove% × NotionalUSD),
	// NOT (priceMove × Size) which silently treated the USD target as a base-asset
	// quantity and inflated PnL by a per-pair factor (price × contract multiplier).
	NotionalUSD     float64
	Leverage        int
	StopLoss        float64
	TakeProfit      float64
	TrailingStopPct float64
	HighestPrice    float64 // For trailing stop (long)
	LowestPrice     float64 // For trailing stop (short)
	OpenedAt        time.Time
	LastChecked     time.Time
	LastAICheck     time.Time // Last time AI analyzed this position
	Status          string    // "open", "closing", "closed"
	AIManaged       bool      // Whether AI is managing this position's SL/TP
	Exchange        string    // Exchange name (e.g., "kucoin")

	// Advanced TP/SL features
	BreakevenApplied     bool    // Whether breakeven has been applied
	TrailingStopEnabled  bool    // Whether trailing stop is enabled
	TrailingStopDistance float64 // Trailing stop distance percentage

	// Exchange-side stop order ID — set after placing a real stop order on the
	// exchange. Non-empty means KuCoin itself enforces the SL independently of
	// this bot's monitoring loop.
	ExchangeStopOrderID string

	// Partial take-profit ladder state. OriginalSize is captured at open so we
	// can dimension each rung off the entry size, not the post-close remainder.
	// PartialTPTriggered tracks which configured levels have already fired,
	// indexed identically to the bot's PartialTP.Levels slice.
	OriginalSize       float64
	PartialTPTriggered []bool

	// MFE (Maximum Favorable Excursion) tracking.
	// PeakPnlPct holds the highest profit % seen during this position's lifetime.
	// Updated on every monitoring tick when pnl > PeakPnlPct.
	// Used by the MFE drawdown guard: if profit retraces more than exit.mfe_drawdown_pct
	// from the peak, the position is closed to protect realised gains.
	PeakPnlPct float64

	// Entry decision context, captured at open and carried through to
	// trade_history persistence so closed-trade analytics can break performance
	// down by signal confidence and entry rationale.
	Confidence     float64 // LLM/technical consensus confidence at entry (0–1)
	DecisionReason string  // Human-readable entry rationale

	// MLProb is P(TP_first) from the ML predictor, captured when the position is
	// opened. Carried through to the post-trade feedback labeler so the ML voter's
	// opinion is credited; a zero value means the ML did not opine (abstained) or
	// the position was adopted without a fresh ML signal.
	MLProb float64

	// Entry signal attribution (Active Profit Engine F0). Captured at open so the
	// post-trade labeler can record per-source calibration observations instead of
	// crediting technical and llm with the same vote (which made them collinear in
	// signal_calibration). EntryRegime is the market regime label at entry so the
	// calibrator can weight sources per regime. Empty/zero = not captured.
	EntryRegime   string  // market regime label at entry (e.g. "trending_up")
	TechnicalVote string  // technical sub-decision action at entry (BUY/SELL/WAIT)
	LLMVote       string  // llm sub-decision action at entry
	TechnicalConf float64 // technical sub-decision confidence at entry
	LLMConf       float64 // llm sub-decision confidence at entry

	// closing is an atomic guard against double-close: BeginClose() flips it 0→1
	// (CAS) and returns false if another path is already closing this position.
	closing int32
}

// BeginClose atomically marks the position as closing. It returns true for the
// first caller and false if a close is already in progress, preventing two
// monitoring ticks (or an exchange-close racing a bot-close) from issuing
// duplicate close orders. Pair with EndClose() to release the guard when a close
// attempt fails and the position remains open, so the next tick can retry.
func (p *ManagedPosition) BeginClose() bool {
	return atomic.CompareAndSwapInt32(&p.closing, 0, 1)
}

// EndClose releases the closing guard so a future monitoring tick may retry.
// Call this only when a close attempt FAILED and the position is still open.
func (p *ManagedPosition) EndClose() {
	atomic.StoreInt32(&p.closing, 0)
}

// Lock/Unlock (BOT-08) let callers outside this package serialize field
// access on a single position — multiple goroutines (monitoringLoop,
// portfolioExitLoop, analysisLoop/executeTrade) share this same pointer via
// GetAllPositions()/GetPosition(), and PositionManager.mu only protects the
// map, not these fields. Callers must Lock() before reading-then-writing (or
// multi-field updating) StopLoss/ExchangeStopOrderID/Status and Unlock()
// promptly — do not hold this across slow network I/O to the exchange
// except where the call is specifically serializing that I/O itself (see
// services.AITradingBot.reissueExchangeStopLoss).
func (p *ManagedPosition) Lock()   { p.mu.Lock() }
func (p *ManagedPosition) Unlock() { p.mu.Unlock() }

// NewPositionManager creates a new position manager
func NewPositionManager(config *PositionConfig, log *logrus.Logger) *PositionManager {
	if config == nil {
		config = DefaultPositionConfig()
	}

	return &PositionManager{
		positions: make(map[string]*ManagedPosition),
		config:    config,
		log:       log,
		stopChan:  make(chan struct{}),
	}
}

// DefaultPositionConfig returns default position management config
func DefaultPositionConfig() *PositionConfig {
	return &PositionConfig{
		AutoClose:             true,
		TrailingStop:          false,
		TrailingStopPct:       1.0,
		BreakevenAfterPct:     1.5,
		PartialTakeProfits:    false,
		CheckInterval:         10,
		RequireConfirm:        false,
		AIManagement:          false,
		AICheckInterval:       60, // 1 minute
		AIConfidenceThreshold: 0.7,
	}
}

// Start starts the position monitoring loop
func (pm *PositionManager) Start(ctx context.Context, priceProvider PriceProvider) {
	pm.mu.Lock()
	if pm.running {
		pm.mu.Unlock()
		return
	}
	pm.running = true
	pm.mu.Unlock()

	pm.log.WithFields(logrus.Fields{
		"auto_close":        pm.config.AutoClose,
		"trailing_stop":     pm.config.TrailingStop,
		"check_interval":    pm.config.CheckInterval,
		"require_confirm":   pm.config.RequireConfirm,
		"ai_management":     pm.config.AIManagement,
		"ai_check_interval": pm.config.AICheckInterval,
	}).Info("Position manager started")

	ticker := time.NewTicker(time.Duration(pm.config.CheckInterval) * time.Second)
	defer ticker.Stop()

	// Start AI analysis ticker if AI management is enabled
	var aiCheckChan <-chan time.Time
	if pm.config.AIManagement && pm.aiOptimizer != nil {
		pm.aiCheckTicker = time.NewTicker(time.Duration(pm.config.AICheckInterval) * time.Second)
		defer pm.aiCheckTicker.Stop()
		aiCheckChan = pm.aiCheckTicker.C

		pm.log.Info("AI-based position management enabled")
	}

	for {
		select {
		case <-ticker.C:
			pm.checkPositions(ctx, priceProvider)
		case <-aiCheckChan:
			if pm.config.AIManagement && pm.aiOptimizer != nil {
				pm.analyzePositionsWithAI(ctx)
			}
		case <-pm.stopChan:
			pm.log.Info("Position manager stopped")
			return
		case <-ctx.Done():
			pm.log.Info("Position manager context cancelled")
			return
		}
	}
}

// Stop stops the position manager
func (pm *PositionManager) Stop() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.running {
		close(pm.stopChan)
		pm.running = false
	}
}

// AddPosition adds a position to be managed
func (pm *PositionManager) AddPosition(pos *ManagedPosition) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	pos.OpenedAt = time.Now()
	pos.LastChecked = time.Now()
	pos.Status = "open"
	pos.HighestPrice = pos.EntryPrice
	pos.LowestPrice = pos.EntryPrice
	pos.TrailingStopPct = pm.config.TrailingStopPct

	pm.positions[pos.ID] = pos

	pm.log.WithFields(logrus.Fields{
		"position_id": pos.ID,
		"symbol":      pos.Symbol,
		"side":        pos.Side,
		"entry":       pos.EntryPrice,
		"stop_loss":   pos.StopLoss,
		"take_profit": pos.TakeProfit,
	}).Info("Position added to manager")
}

// RemovePosition removes a position from management
func (pm *PositionManager) RemovePosition(positionID string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	delete(pm.positions, positionID)
	pm.log.WithField("position_id", positionID).Info("Position removed from manager")
}

// GetPosition returns a managed position
func (pm *PositionManager) GetPosition(positionID string) (*ManagedPosition, bool) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	pos, exists := pm.positions[positionID]
	return pos, exists
}

// GetAllPositions returns all managed positions
func (pm *PositionManager) GetAllPositions() []*ManagedPosition {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	positions := make([]*ManagedPosition, 0, len(pm.positions))
	for _, pos := range pm.positions {
		positions = append(positions, pos)
	}
	return positions
}

// checkPositions checks all positions and takes action if needed
func (pm *PositionManager) checkPositions(ctx context.Context, priceProvider PriceProvider) {
	pm.mu.Lock()
	positions := make([]*ManagedPosition, 0, len(pm.positions))
	for _, pos := range pm.positions {
		if pos.Status == "open" {
			positions = append(positions, pos)
		}
	}
	pm.mu.Unlock()

	if len(positions) == 0 {
		return
	}

	pm.log.WithField("count", len(positions)).Debug("Checking positions")

	for _, pos := range positions {
		pm.checkPosition(ctx, pos, priceProvider)
	}
}

// checkPosition checks a single position
func (pm *PositionManager) checkPosition(ctx context.Context, pos *ManagedPosition, priceProvider PriceProvider) {
	// Get current price
	currentPrice, err := priceProvider.GetPrice(ctx, pos.Symbol)
	if err != nil {
		pm.log.WithError(err).WithField("symbol", pos.Symbol).Error("Failed to get price")
		return
	}

	pos.LastChecked = time.Now()

	// Calculate P&L
	var pnlPct float64
	if pos.Side == "buy" {
		pnlPct = ((currentPrice - pos.EntryPrice) / pos.EntryPrice) * 100
	} else {
		pnlPct = ((pos.EntryPrice - currentPrice) / pos.EntryPrice) * 100
	}

	// Update highest/lowest for trailing stop
	if pos.Side == "buy" && currentPrice > pos.HighestPrice {
		pos.HighestPrice = currentPrice
	} else if pos.Side == "sell" && currentPrice < pos.LowestPrice {
		pos.LowestPrice = currentPrice
	}

	// Check trailing stop
	if pm.config.TrailingStop {
		pm.updateTrailingStop(pos, currentPrice)
	}

	// Check breakeven
	if pm.config.BreakevenAfterPct > 0 && pnlPct >= pm.config.BreakevenAfterPct {
		pm.moveToBreakeven(pos)
	}

	// Check stop loss
	if pm.shouldCloseAtStopLoss(pos, currentPrice) {
		pm.closePosition(ctx, pos, currentPrice, "stop_loss", pnlPct)
		return
	}

	// Check take profit
	if pm.shouldCloseAtTakeProfit(pos, currentPrice) {
		pm.closePosition(ctx, pos, currentPrice, "take_profit", pnlPct)
		return
	}

	// Condition-based exit: close on RSI reversal, MACD cross,
	// volume death, profit lock. This is ADDITIVE — price-based SL/TP
	// already ran above. Only fires when EnableConditionBasedExit was called
	// with a provider.
	if pm.exitConditionsEnabled && pm.indicatorProvider != nil {
		if shouldExit, reason := pm.evaluatePositionExitConditions(pos); shouldExit {
			pm.log.WithFields(logrus.Fields{
				"symbol": pos.Symbol,
				"side":   pos.Side,
				"reason": reason,
			}).Info("🚨 Condition-based exit triggered")
			pm.closePosition(ctx, pos, currentPrice, "condition_exit:"+reason, pnlPct)
			return
		}
	}

	// Log status
	pm.log.WithFields(logrus.Fields{
		"position_id":   pos.ID,
		"symbol":        pos.Symbol,
		"current_price": currentPrice,
		"entry_price":   pos.EntryPrice,
		"pnl_pct":       fmt.Sprintf("%.2f%%", pnlPct),
		"stop_loss":     pos.StopLoss,
		"take_profit":   pos.TakeProfit,
	}).Debug("Position checked")
}

// shouldCloseAtStopLoss checks if position should close at stop loss
func (pm *PositionManager) shouldCloseAtStopLoss(pos *ManagedPosition, currentPrice float64) bool {
	if pos.Side == "buy" {
		return currentPrice <= pos.StopLoss
	}
	return currentPrice >= pos.StopLoss
}

// shouldCloseAtTakeProfit checks if position should close at take profit
func (pm *PositionManager) shouldCloseAtTakeProfit(pos *ManagedPosition, currentPrice float64) bool {
	if pos.Side == "buy" {
		return currentPrice >= pos.TakeProfit
	}
	return currentPrice <= pos.TakeProfit
}

// updateTrailingStop updates trailing stop loss
func (pm *PositionManager) updateTrailingStop(pos *ManagedPosition, currentPrice float64) {
	var newStopLoss float64

	if pos.Side == "buy" {
		// Long position: trail below highest price
		newStopLoss = pos.HighestPrice * (1 - pos.TrailingStopPct/100)
		if newStopLoss > pos.StopLoss {
			oldSL := pos.StopLoss
			pos.StopLoss = newStopLoss
			pm.log.WithFields(logrus.Fields{
				"position_id": pos.ID,
				"old_sl":      oldSL,
				"new_sl":      newStopLoss,
				"highest":     pos.HighestPrice,
			}).Info("Trailing stop updated (LONG)")
		}
	} else {
		// Short position: trail above lowest price
		newStopLoss = pos.LowestPrice * (1 + pos.TrailingStopPct/100)
		if newStopLoss < pos.StopLoss {
			oldSL := pos.StopLoss
			pos.StopLoss = newStopLoss
			pm.log.WithFields(logrus.Fields{
				"position_id": pos.ID,
				"old_sl":      oldSL,
				"new_sl":      newStopLoss,
				"lowest":      pos.LowestPrice,
			}).Info("Trailing stop updated (SHORT)")
		}
	}
}

// moveToBreakeven moves stop loss to breakeven
func (pm *PositionManager) moveToBreakeven(pos *ManagedPosition) {
	if pos.Side == "buy" && pos.StopLoss < pos.EntryPrice {
		oldSL := pos.StopLoss
		pos.StopLoss = pos.EntryPrice
		pm.log.WithFields(logrus.Fields{
			"position_id": pos.ID,
			"old_sl":      oldSL,
			"new_sl":      pos.EntryPrice,
		}).Info("Stop loss moved to breakeven")
	} else if pos.Side == "sell" && pos.StopLoss > pos.EntryPrice {
		oldSL := pos.StopLoss
		pos.StopLoss = pos.EntryPrice
		pm.log.WithFields(logrus.Fields{
			"position_id": pos.ID,
			"old_sl":      oldSL,
			"new_sl":      pos.EntryPrice,
		}).Info("Stop loss moved to breakeven")
	}
}

// closePosition closes a position
func (pm *PositionManager) closePosition(ctx context.Context, pos *ManagedPosition, currentPrice float64, reason string, pnlPct float64) {
	if !pm.config.AutoClose {
		pm.log.WithFields(logrus.Fields{
			"position_id": pos.ID,
			"reason":      reason,
			"pnl_pct":     fmt.Sprintf("%.2f%%", pnlPct),
		}).Warn("🔔 Position should close but auto_close is disabled")
		return
	}

	if pm.config.RequireConfirm {
		pm.log.WithFields(logrus.Fields{
			"position_id": pos.ID,
			"reason":      reason,
			"pnl_pct":     fmt.Sprintf("%.2f%%", pnlPct),
		}).Warn("🔔 Position ready to close - confirmation required")
		// TODO: Implement confirmation mechanism
		return
	}

	pm.log.WithFields(logrus.Fields{
		"position_id": pos.ID,
		"symbol":      pos.Symbol,
		"side":        pos.Side,
		"entry_price": pos.EntryPrice,
		"close_price": currentPrice,
		"pnl_pct":     fmt.Sprintf("%.2f%%", pnlPct),
		"reason":      reason,
	}).Info("🚀 Closing position automatically")

	pos.Status = "closing"

	// Execute close order via order executor
	if pm.orderExecutor != nil {
		err := pm.orderExecutor.ClosePosition(ctx, pos, currentPrice, reason)
		if err != nil {
			pm.log.WithError(err).Error("Failed to close position")
			pos.Status = "open" // Revert status
			return
		}
		pos.Status = "closed"
		pm.RemovePosition(pos.ID)
	} else {
		pm.log.Warn("No order executor configured - position not closed")
	}
}

// SetOrderExecutor sets the order executor for closing positions
func (pm *PositionManager) SetOrderExecutor(executor OrderExecutor) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.orderExecutor = executor
}

// SetAIOptimizer sets the AI optimizer for stop loss suggestions
func (pm *PositionManager) SetAIOptimizer(optimizer *SLTPOptimizer) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.aiOptimizer = optimizer
}

// SetAIDecisionRepo sets the AI decision repository for logging
func (pm *PositionManager) SetAIDecisionRepo(repo domain.AIDecisionRepository) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.aiDecisionRepo = repo
}

// analyzePositionsWithAI performs AI analysis on all open positions
func (pm *PositionManager) analyzePositionsWithAI(ctx context.Context) {
	pm.mu.Lock()
	positions := make([]*ManagedPosition, 0, len(pm.positions))
	for _, pos := range pm.positions {
		if pos.Status == "open" {
			positions = append(positions, pos)
		}
	}
	pm.mu.Unlock()

	if len(positions) == 0 {
		return
	}

	pm.log.WithField("count", len(positions)).Debug("Analyzing positions with AI")

	for _, pos := range positions {
		pm.analyzePositionWithAI(ctx, pos)
	}
}

// analyzePositionWithAI analyzes a single position and suggests SL adjustments
func (pm *PositionManager) analyzePositionWithAI(ctx context.Context, pos *ManagedPosition) {
	// Skip if AI optimizer is not available
	if pm.aiOptimizer == nil {
		return
	}

	// Skip if position doesn't have exchange set
	if pos.Exchange == "" {
		pos.Exchange = "kucoin" // Default to kucoin
	}

	// Get current price (use highest/lowest as proxy for current price)
	currentPrice := pos.HighestPrice
	if pos.Side == "sell" {
		currentPrice = pos.LowestPrice
	}

	// Check if stop loss should be adjusted
	shouldAdjust, suggestedSL, reasoning, err := pm.aiOptimizer.ShouldAdjustStopLoss(
		ctx,
		pos.Exchange,
		pos.Symbol,
		pos.Side,
		pos.EntryPrice,
		currentPrice,
		pos.StopLoss,
	)

	if err != nil {
		pm.log.WithError(err).WithField("position_id", pos.ID).Warn("Failed to get AI stop loss suggestion")
		return
	}

	pos.LastAICheck = time.Now()

	if !shouldAdjust {
		pm.log.WithFields(logrus.Fields{
			"position_id": pos.ID,
			"symbol":      pos.Symbol,
		}).Debug("AI suggests no stop loss adjustment needed")
		return
	}

	pm.log.WithFields(logrus.Fields{
		"position_id":  pos.ID,
		"symbol":       pos.Symbol,
		"current_sl":   pos.StopLoss,
		"suggested_sl": suggestedSL,
		"reasoning":    reasoning,
	}).Info("AI suggests stop loss adjustment")

	// Get market condition for confidence check
	condition, err := pm.aiOptimizer.DetectMarketCondition(ctx, pos.Exchange, pos.Symbol)
	if err != nil {
		pm.log.WithError(err).Warn("Failed to get market condition")
		return
	}

	// Auto-adjust if confidence is high and AI management is enabled for this position
	if pos.AIManaged && condition.Confidence >= pm.config.AIConfidenceThreshold {
		oldSL := pos.StopLoss
		pos.StopLoss = suggestedSL

		pm.log.WithFields(logrus.Fields{
			"position_id": pos.ID,
			"symbol":      pos.Symbol,
			"old_sl":      oldSL,
			"new_sl":      suggestedSL,
			"confidence":  condition.Confidence,
			"reasoning":   reasoning,
		}).Info("🤖 AI auto-adjusted stop loss")

		// Log AI decision to database
		pm.logAIDecision(ctx, pos, "stop_loss_adjust", oldSL, suggestedSL, condition, reasoning)
	} else {
		pm.log.WithFields(logrus.Fields{
			"position_id":  pos.ID,
			"symbol":       pos.Symbol,
			"suggested_sl": suggestedSL,
			"confidence":   condition.Confidence,
			"threshold":    pm.config.AIConfidenceThreshold,
			"ai_managed":   pos.AIManaged,
			"reasoning":    reasoning,
		}).Info("💡 AI suggestion available but not auto-applied (low confidence or AI management disabled)")
	}
}

// logAIDecision logs an AI decision to the database
func (pm *PositionManager) logAIDecision(ctx context.Context, pos *ManagedPosition, decisionType string, beforeValue, afterValue float64, condition *MarketCondition, reasoning string) {
	if pm.aiDecisionRepo == nil {
		return
	}

	// Marshal market condition to JSON
	conditionJSON, err := json.Marshal(condition)
	if err != nil {
		pm.log.WithError(err).Warn("Failed to marshal market condition")
		return
	}

	// Calculate PnL percent
	var pnlPercent float64
	currentPrice := pos.HighestPrice
	if pos.Side == "sell" {
		currentPrice = pos.LowestPrice
		pnlPercent = ((pos.EntryPrice - currentPrice) / pos.EntryPrice) * 100
	} else {
		pnlPercent = ((currentPrice - pos.EntryPrice) / pos.EntryPrice) * 100
	}

	// Marshal indicators to JSON
	indicators := map[string]float64{
		"rsi":         condition.RSI,
		"atr":         condition.ATR,
		"pnl_percent": pnlPercent,
	}
	indicatorsJSON, err := json.Marshal(indicators)
	if err != nil {
		pm.log.WithError(err).Warn("Failed to marshal indicators")
		return
	}

	decision := &domain.AIDecision{
		ID:              fmt.Sprintf("aid_%d", time.Now().Unix()),
		PositionID:      pos.ID,
		DecisionType:    decisionType,
		BeforeValue:     decimal.NewFromFloat(beforeValue),
		AfterValue:      decimal.NewFromFloat(afterValue),
		MarketCondition: string(conditionJSON),
		Indicators:      string(indicatorsJSON),
		Confidence:      decimal.NewFromFloat(condition.Confidence),
		Outcome:         "", // Will be set later when position closes
		CreatedAt:       time.Now(),
	}

	if err := pm.aiDecisionRepo.Create(ctx, decision); err != nil {
		pm.log.WithError(err).Warn("Failed to save AI decision")
	} else {
		pm.log.WithFields(logrus.Fields{
			"decision_id":   decision.ID,
			"position_id":   pos.ID,
			"decision_type": decisionType,
			"confidence":    condition.Confidence,
		}).Debug("AI decision logged to database")
	}
}

// PriceProvider interface for getting current prices
type PriceProvider interface {
	GetPrice(ctx context.Context, symbol string) (float64, error)
}

// OrderExecutor interface for executing close orders
type OrderExecutor interface {
	ClosePosition(ctx context.Context, pos *ManagedPosition, currentPrice float64, reason string) error
}

// ExitCondition represents a condition that triggers position closure
type ExitCondition struct {
	Name       string
	Triggered  bool
	Confidence float64 // 0-1
	Reason     string
}

// ExitConditionAnalysis is the result of evaluating all exit conditions
type ExitConditionAnalysis struct {
	ShouldExit        bool
	Conditions        []ExitCondition // all evaluated conditions
	TriggeredCount    int
	PrimaryReason     string
	OverallConfidence float64
}

// MarketSnapshot is the input to evaluate exit conditions
type MarketSnapshot struct {
	CurrentPrice float64
	RSI          float64 // current RSI of the pair
	PrevRSI      float64 // RSI of previous candle
	MACD         float64
	MACDSignal   float64
	PrevMACD     float64
	Volume       float64
	AvgVolume    float64 // recent average volume
	ATR          float64
	EntryPrice   float64
	Side         string // "BUY" or "SELL"

	// Configurable profit-lock thresholds (read from dynamic config at call time).
	// Zero values fall back to hardcoded defaults inside EvaluateExitConditions.
	ProfitLockMinPct   float64 // ratio (e.g. 0.008 = 0.8%)
	ProfitLockRSILong  float64 // RSI above which profit-lock fires for longs
	ProfitLockRSIShort float64 // RSI below which profit-lock fires for shorts
}

// IndicatorDataProvider is an optional dependency that supplies live
// indicator values so the manager can build a MarketSnapshot at evaluation time.
// Caller injects this via EnableConditionBasedExit.
type IndicatorDataProvider interface {
	GetIndicators(symbol string) (*PositionIndicators, error)
}

// PositionIndicators bundles the indicator values needed to evaluate
// condition-based exits for a single symbol.
type PositionIndicators struct {
	CurrentPrice float64
	RSI          float64
	PrevRSI      float64
	MACD         float64
	MACDSignal   float64
	PrevMACD     float64
	Volume       float64
	AvgVolume    float64
	ATR          float64
}

// EnableConditionBasedExit activates closing positions based on market
// condition signals (RSI reversal, MACD cross, volume death, profit lock).
// This is ADDITIVE — price-based SL/TP closing keeps working independently.
// Requires an IndicatorDataProvider to fetch real-time indicators.
// Passing nil disables the feature.
func (pm *PositionManager) EnableConditionBasedExit(provider IndicatorDataProvider) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.indicatorProvider = provider
	pm.exitConditionsEnabled = provider != nil
}

// evaluatePositionExitConditions builds a MarketSnapshot from the live
// indicator provider and runs EvaluateExitConditions. Returns (true, reason)
// only when the analysis says the position should close.
func (pm *PositionManager) evaluatePositionExitConditions(pos *ManagedPosition) (bool, string) {
	if pm.indicatorProvider == nil {
		return false, ""
	}
	indicators, err := pm.indicatorProvider.GetIndicators(pos.Symbol)
	if err != nil || indicators == nil {
		return false, ""
	}

	snapshot := &MarketSnapshot{
		CurrentPrice: indicators.CurrentPrice,
		RSI:          indicators.RSI,
		PrevRSI:      indicators.PrevRSI,
		MACD:         indicators.MACD,
		MACDSignal:   indicators.MACDSignal,
		PrevMACD:     indicators.PrevMACD,
		Volume:       indicators.Volume,
		AvgVolume:    indicators.AvgVolume,
		ATR:          indicators.ATR,
		EntryPrice:   pos.EntryPrice,
		// EvaluateExitConditions accepts both upper/lower-case sides;
		// normalize to upper-case for consistency.
		Side: strings.ToUpper(pos.Side),
	}

	analysis := pm.EvaluateExitConditions(snapshot)
	if analysis.ShouldExit {
		return true, analysis.PrimaryReason
	}
	return false, ""
}

// EvaluateExitConditions analyzes whether the position should close BY CONDITION.
// This is ADDITIVE — price-based SL/TP closing keeps working independently.
// If it returns ShouldExit=true, the caller decides whether to close.
func (pm *PositionManager) EvaluateExitConditions(snapshot *MarketSnapshot) *ExitConditionAnalysis {
	analysis := &ExitConditionAnalysis{
		Conditions: make([]ExitCondition, 0, 4),
	}

	if snapshot == nil {
		return analysis
	}

	// Normalize side: accept BUY/SELL or buy/sell
	isLong := snapshot.Side == "BUY" || snapshot.Side == "buy"
	isShort := snapshot.Side == "SELL" || snapshot.Side == "sell"

	// Calculate profit ratio (positive when in profit)
	var profitRatio float64
	if snapshot.EntryPrice > 0 {
		if isLong {
			profitRatio = (snapshot.CurrentPrice - snapshot.EntryPrice) / snapshot.EntryPrice
		} else if isShort {
			profitRatio = (snapshot.EntryPrice - snapshot.CurrentPrice) / snapshot.EntryPrice
		}
	}

	if isLong {
		// 1. RSI Reversal — overbought reverting
		rsiReversal := ExitCondition{Name: "rsi_reversal"}
		if snapshot.RSI > 70 && snapshot.PrevRSI > snapshot.RSI {
			rsiReversal.Triggered = true
			rsiReversal.Confidence = 0.8
			rsiReversal.Reason = "RSI reverting from overbought"
		}
		analysis.Conditions = append(analysis.Conditions, rsiReversal)

		// 2. MACD Cross Down — bearish crossover
		macdCross := ExitCondition{Name: "macd_cross_down"}
		if snapshot.PrevMACD > snapshot.MACDSignal && snapshot.MACD <= snapshot.MACDSignal {
			macdCross.Triggered = true
			macdCross.Confidence = 0.7
			macdCross.Reason = "MACD bearish crossover"
		}
		analysis.Conditions = append(analysis.Conditions, macdCross)

		// 3. Volume Death — volume drying while in profit
		volDeath := ExitCondition{Name: "volume_death"}
		if snapshot.AvgVolume > 0 && snapshot.Volume < snapshot.AvgVolume*0.4 && profitRatio > 0 {
			volDeath.Triggered = true
			volDeath.Confidence = 0.5
			volDeath.Reason = "Volume drying up while in profit"
		}
		analysis.Conditions = append(analysis.Conditions, volDeath)

		// 4. Profit Lock — lock in on overbought (thresholds from dynamic config)
		plMin := snapshot.ProfitLockMinPct
		if plMin <= 0 {
			plMin = 0.008 // fallback 0.8%
		}
		plRSI := snapshot.ProfitLockRSILong
		if plRSI <= 0 {
			plRSI = 72
		}
		profitLock := ExitCondition{Name: "profit_lock"}
		if profitRatio > plMin && snapshot.RSI > plRSI {
			profitLock.Triggered = true
			profitLock.Confidence = 0.85
			profitLock.Reason = fmt.Sprintf("Locking in profit on overbought (%.1f%% gain, RSI %.0f)", profitRatio*100, snapshot.RSI)
		}
		analysis.Conditions = append(analysis.Conditions, profitLock)
	} else if isShort {
		// 1. RSI Reversal — oversold reverting upward
		rsiReversal := ExitCondition{Name: "rsi_reversal"}
		if snapshot.RSI < 30 && snapshot.PrevRSI < snapshot.RSI {
			rsiReversal.Triggered = true
			rsiReversal.Confidence = 0.8
			rsiReversal.Reason = "RSI reverting from oversold"
		}
		analysis.Conditions = append(analysis.Conditions, rsiReversal)

		// 2. MACD Cross Up — bullish crossover (bad for short)
		macdCross := ExitCondition{Name: "macd_cross_up"}
		if snapshot.PrevMACD < snapshot.MACDSignal && snapshot.MACD >= snapshot.MACDSignal {
			macdCross.Triggered = true
			macdCross.Confidence = 0.7
			macdCross.Reason = "MACD bullish crossover"
		}
		analysis.Conditions = append(analysis.Conditions, macdCross)

		// 3. Volume Death — volume drying while in profit
		volDeath := ExitCondition{Name: "volume_death"}
		if snapshot.AvgVolume > 0 && snapshot.Volume < snapshot.AvgVolume*0.4 && profitRatio > 0 {
			volDeath.Triggered = true
			volDeath.Confidence = 0.5
			volDeath.Reason = "Volume drying up while in profit"
		}
		analysis.Conditions = append(analysis.Conditions, volDeath)

		// 4. Profit Lock — lock in on oversold (thresholds from dynamic config)
		plMinS := snapshot.ProfitLockMinPct
		if plMinS <= 0 {
			plMinS = 0.008
		}
		plRSIS := snapshot.ProfitLockRSIShort
		if plRSIS <= 0 {
			plRSIS = 28
		}
		profitLock := ExitCondition{Name: "profit_lock"}
		if profitRatio > plMinS && snapshot.RSI < plRSIS {
			profitLock.Triggered = true
			profitLock.Confidence = 0.85
			profitLock.Reason = fmt.Sprintf("Locking in profit on oversold (%.1f%% gain, RSI %.0f)", profitRatio*100, snapshot.RSI)
		}
		analysis.Conditions = append(analysis.Conditions, profitLock)
	}

	// Aggregate triggered conditions
	var triggeredConfSum float64
	var topConf float64
	for _, c := range analysis.Conditions {
		if c.Triggered {
			analysis.TriggeredCount++
			triggeredConfSum += c.Confidence
			if c.Confidence > topConf {
				topConf = c.Confidence
				analysis.PrimaryReason = c.Reason
			}
		}
	}

	if analysis.TriggeredCount > 0 {
		analysis.OverallConfidence = triggeredConfSum / float64(analysis.TriggeredCount)
	}

	// Decision rules
	if analysis.TriggeredCount >= 2 {
		analysis.ShouldExit = true
	} else if analysis.TriggeredCount == 1 && topConf >= 0.8 {
		analysis.ShouldExit = true
	}

	return analysis
}
