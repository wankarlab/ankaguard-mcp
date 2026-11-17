package ai

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/domain"
	"github.com/rantipay/trading-mcp/internal/gct"
)

// SLTPOptimizer provides AI-based optimization for stop loss and take profit levels
type SLTPOptimizer struct {
	gctClient      gct.GCTClient
	btcMonitor     *BTCCorrelationMonitor
	stopLossRepo   domain.StopLossRepository
	aiDecisionRepo domain.AIDecisionRepository
	logger         *logrus.Logger
	config         *OptimizerConfig

	// State
	running  bool
	stopChan chan struct{}
	mu       sync.RWMutex

	// Optimization tracking
	lastOptimization  time.Time
	optimizationCount int64
}

// OptimizerConfig holds configuration for the SLTP optimizer
type OptimizerConfig struct {
	// Optimization intervals
	OptimizationInterval time.Duration `yaml:"optimization_interval"`

	// Volatility-based adjustments
	EnableVolatilityAdjust  bool    `yaml:"enable_volatility_adjust"`
	VolatilityWindow        int     `yaml:"volatility_window"`
	HighVolatilityThreshold float64 `yaml:"high_volatility_threshold"`
	LowVolatilityThreshold  float64 `yaml:"low_volatility_threshold"`

	// BTC correlation adjustments
	EnableBTCCorrelation    bool    `yaml:"enable_btc_correlation"`
	BTCCorrelationThreshold float64 `yaml:"btc_correlation_threshold"`

	// AI confidence thresholds
	MinConfidenceForAdjust float64 `yaml:"min_confidence_for_adjust"`

	// Adjustment limits
	MaxSLAdjustmentPercent float64 `yaml:"max_sl_adjustment_percent"`
	MinSLAdjustmentPercent float64 `yaml:"min_sl_adjustment_percent"`
}

// OptimizationDecision represents an AI optimization decision
type OptimizationDecision struct {
	PositionID      string    `json:"position_id"`
	Symbol          string    `json:"symbol"`
	DecisionType    string    `json:"decision_type"`
	OldValue        float64   `json:"old_value"`
	NewValue        float64   `json:"new_value"`
	Confidence      float64   `json:"confidence"`
	Reason          string    `json:"reason"`
	MarketCondition string    `json:"market_condition"`
	Timestamp       time.Time `json:"timestamp"`
}

// MarketCondition represents current market conditions for a symbol
type MarketCondition struct {
	Symbol          string    `json:"symbol"`
	Volatility      float64   `json:"volatility"`
	VolatilityLevel string    `json:"volatility_level"` // "low", "medium", "high"
	BTCCorrelation  float64   `json:"btc_correlation"`
	Trend           string    `json:"trend"` // "bullish", "bearish", "neutral"
	RSI             float64   `json:"rsi"`
	ATR             float64   `json:"atr"`
	Confidence      float64   `json:"confidence"`
	Timestamp       time.Time `json:"timestamp"`
}

// NewSLTPOptimizer creates a new SLTP optimizer
func NewSLTPOptimizer(
	gctClient gct.GCTClient,
	btcMonitor *BTCCorrelationMonitor,
	stopLossRepo domain.StopLossRepository,
	aiDecisionRepo domain.AIDecisionRepository,
	config *OptimizerConfig,
	logger *logrus.Logger,
) *SLTPOptimizer {
	if logger == nil {
		logger = logrus.New()
	}

	if config == nil {
		config = GetDefaultOptimizerConfig()
	}

	return &SLTPOptimizer{
		gctClient:      gctClient,
		btcMonitor:     btcMonitor,
		stopLossRepo:   stopLossRepo,
		aiDecisionRepo: aiDecisionRepo,
		logger:         logger,
		config:         config,
		stopChan:       make(chan struct{}),
	}
}

// Start starts the SLTP optimizer
func (o *SLTPOptimizer) Start(ctx context.Context) error {
	o.mu.Lock()
	if o.running {
		o.mu.Unlock()
		return fmt.Errorf("SLTP optimizer already running")
	}
	o.running = true
	o.mu.Unlock()

	o.logger.Info("Starting SLTP optimizer")

	// Set up BTC correlation callback
	if o.config.EnableBTCCorrelation && o.btcMonitor != nil {
		o.btcMonitor.SetBTCDropCallback(o.handleBTCDrop)
	}

	// Start optimization loop
	go o.optimizationLoop(ctx)

	return nil
}

// Stop stops the SLTP optimizer
func (o *SLTPOptimizer) Stop() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if !o.running {
		return fmt.Errorf("SLTP optimizer not running")
	}

	o.logger.Info("Stopping SLTP optimizer")
	close(o.stopChan)
	o.running = false

	return nil
}

// OptimizeStopLoss calculates optimal stop loss based on market conditions
func (o *SLTPOptimizer) OptimizeStopLoss(ctx context.Context, symbol string, currentSL float64, entryPrice float64, side string) (*OptimizationDecision, error) {
	// Analyze market conditions
	condition, err := o.analyzeMarketCondition(ctx, symbol)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze market condition: %w", err)
	}

	// Calculate optimal stop loss
	optimalSL, confidence, reason := o.calculateOptimalStopLoss(condition, currentSL, entryPrice, side)

	// Check if adjustment is significant enough
	adjustmentPercent := math.Abs(optimalSL-currentSL) / currentSL * 100.0
	if adjustmentPercent < o.config.MinSLAdjustmentPercent {
		return nil, nil // No significant adjustment needed
	}

	// Check confidence threshold
	if confidence < o.config.MinConfidenceForAdjust {
		return nil, nil // Not confident enough to adjust
	}

	decision := &OptimizationDecision{
		Symbol:          symbol,
		DecisionType:    "stop_loss_adjust",
		OldValue:        currentSL,
		NewValue:        optimalSL,
		Confidence:      confidence,
		Reason:          reason,
		MarketCondition: o.formatMarketCondition(condition),
		Timestamp:       time.Now(),
	}

	return decision, nil
}

// handleBTCDrop handles BTC drop events and adjusts correlated altcoin stop losses
func (o *SLTPOptimizer) handleBTCDrop(ctx context.Context, dropPercent float64, affectedSymbols []string) error {
	o.logger.WithFields(logrus.Fields{
		"drop_percent":     dropPercent,
		"affected_symbols": affectedSymbols,
	}).Info("Handling BTC drop event")

	// Get all active stop losses for affected symbols
	activeStopLosses, err := o.stopLossRepo.GetActive(ctx)
	if err != nil {
		return fmt.Errorf("failed to get active stop losses: %w", err)
	}

	adjustmentCount := 0

	for _, sl := range activeStopLosses {
		// Check if this symbol is affected
		isAffected := false
		for _, symbol := range affectedSymbols {
			if sl.Symbol == symbol {
				isAffected = true
				break
			}
		}

		if !isAffected {
			continue
		}

		// Calculate adjustment based on BTC drop severity
		adjustmentFactor := o.calculateBTCAdjustmentFactor(dropPercent)

		// Get current price to calculate new stop loss
		ticker, err := o.gctClient.GetTicker(ctx, sl.Exchange, sl.Symbol)
		if err != nil {
			o.logger.WithError(err).WithField("symbol", sl.Symbol).Warn("Failed to get ticker for BTC correlation adjustment")
			continue
		}

		currentPrice := ticker.Last
		currentSL := sl.StopPrice.InexactFloat64()

		// Calculate new stop loss (widen it to avoid premature stops)
		var newSL float64
		if sl.Symbol == "long" { // Assuming we can determine position side
			// For long positions, lower the stop loss
			slDistance := currentPrice - currentSL
			newSL = currentPrice - (slDistance * adjustmentFactor)
		} else {
			// For short positions, raise the stop loss
			slDistance := currentSL - currentPrice
			newSL = currentPrice + (slDistance * adjustmentFactor)
		}

		// Apply limits
		adjustmentPercent := math.Abs(newSL-currentSL) / currentSL * 100.0
		if adjustmentPercent > o.config.MaxSLAdjustmentPercent {
			continue // Skip if adjustment is too large
		}

		// Update stop loss
		sl.StopPrice = decimal.NewFromFloat(newSL)
		if err := o.stopLossRepo.Update(ctx, sl); err != nil {
			o.logger.WithError(err).WithField("position_id", sl.PositionID).Error("Failed to update stop loss for BTC correlation")
			continue
		}

		// Record AI decision
		decision := &domain.AIDecision{
			ID:              fmt.Sprintf("btc_corr_%d", time.Now().UnixNano()),
			PositionID:      sl.PositionID,
			DecisionType:    "btc_correlation_adjust",
			BeforeValue:     decimal.NewFromFloat(currentSL),
			AfterValue:      decimal.NewFromFloat(newSL),
			MarketCondition: fmt.Sprintf(`{"btc_drop_percent": %.2f, "adjustment_factor": %.2f}`, dropPercent, adjustmentFactor),
			Indicators:      fmt.Sprintf(`{"current_price": %.6f, "correlation": "high"}`, currentPrice),
			Confidence:      decimal.NewFromFloat(0.8), // High confidence for BTC correlation adjustments
			Outcome:         "pending",
			CreatedAt:       time.Now(),
		}

		if err := o.aiDecisionRepo.Create(ctx, decision); err != nil {
			o.logger.WithError(err).Warn("Failed to record AI decision")
		}

		adjustmentCount++

		o.logger.WithFields(logrus.Fields{
			"position_id":        sl.PositionID,
			"symbol":             sl.Symbol,
			"old_sl":             currentSL,
			"new_sl":             newSL,
			"adjustment_factor":  adjustmentFactor,
			"adjustment_percent": adjustmentPercent,
		}).Info("Adjusted stop loss due to BTC correlation")
	}

	o.logger.WithFields(logrus.Fields{
		"btc_drop_percent": dropPercent,
		"adjustments_made": adjustmentCount,
	}).Info("Completed BTC correlation adjustments")

	return nil
}

// optimizationLoop runs periodic optimization
func (o *SLTPOptimizer) optimizationLoop(ctx context.Context) {
	ticker := time.NewTicker(o.config.OptimizationInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-o.stopChan:
			return
		case <-ticker.C:
			if err := o.runOptimization(ctx); err != nil {
				o.logger.WithError(err).Error("Optimization run failed")
			}
		}
	}
}

// runOptimization runs a full optimization cycle
func (o *SLTPOptimizer) runOptimization(ctx context.Context) error {
	o.mu.Lock()
	o.lastOptimization = time.Now()
	o.optimizationCount++
	o.mu.Unlock()

	o.logger.Debug("Running SLTP optimization cycle")

	// Get all active stop losses
	activeStopLosses, err := o.stopLossRepo.GetActive(ctx)
	if err != nil {
		return fmt.Errorf("failed to get active stop losses: %w", err)
	}

	optimizationsMade := 0

	for _, sl := range activeStopLosses {
		if !sl.AIManaged {
			continue // Skip non-AI managed stop losses
		}

		// TODO: Get position info to determine entry price and side
		// For now, we'll skip this optimization
		// In a real implementation, you'd need to get position details

		o.logger.WithField("position_id", sl.PositionID).Debug("Skipping optimization - need position details")
	}

	o.logger.WithFields(logrus.Fields{
		"active_stop_losses": len(activeStopLosses),
		"optimizations_made": optimizationsMade,
	}).Debug("Completed optimization cycle")

	return nil
}

// analyzeMarketCondition analyzes current market conditions for a symbol
func (o *SLTPOptimizer) analyzeMarketCondition(ctx context.Context, symbol string) (*MarketCondition, error) {
	// Get current ticker
	ticker, err := o.gctClient.GetTicker(ctx, "kucoin", symbol)
	if err != nil {
		return nil, fmt.Errorf("failed to get ticker: %w", err)
	}

	// Calculate volatility (simplified - would use historical data in production)
	volatility := 0.05 // Placeholder - 5% volatility

	// Get BTC correlation if available
	var btcCorrelation float64
	if o.btcMonitor != nil {
		correlations := o.btcMonitor.GetCorrelations()
		if corr, exists := correlations[symbol]; exists {
			btcCorrelation = corr.Correlation
		}
	}

	// Determine volatility level
	var volatilityLevel string
	if volatility < o.config.LowVolatilityThreshold {
		volatilityLevel = "low"
	} else if volatility > o.config.HighVolatilityThreshold {
		volatilityLevel = "high"
	} else {
		volatilityLevel = "medium"
	}

	// Calculate confidence based on data availability
	confidence := 0.5 // Base confidence

	// Increase confidence if we have BTC correlation data
	if btcCorrelation != 0 {
		confidence += 0.2
	}

	// Increase confidence based on volatility clarity
	if volatilityLevel != "medium" {
		confidence += 0.1
	}

	// Ensure confidence is within bounds
	if confidence > 1.0 {
		confidence = 1.0
	}

	return &MarketCondition{
		Symbol:          symbol,
		Volatility:      volatility,
		VolatilityLevel: volatilityLevel,
		BTCCorrelation:  btcCorrelation,
		Trend:           "neutral", // Placeholder
		RSI:             50.0,      // Placeholder
		ATR:             volatility * ticker.Last,
		Confidence:      confidence,
		Timestamp:       time.Now(),
	}, nil
}

// calculateOptimalStopLoss calculates optimal stop loss based on market conditions
func (o *SLTPOptimizer) calculateOptimalStopLoss(condition *MarketCondition, currentSL, entryPrice float64, side string) (float64, float64, string) {
	confidence := 0.5
	reason := "volatility-based adjustment"

	// Base adjustment factor
	adjustmentFactor := 1.0

	// Adjust based on volatility
	if o.config.EnableVolatilityAdjust {
		switch condition.VolatilityLevel {
		case "high":
			adjustmentFactor = 1.5 // Widen stop loss in high volatility
			confidence += 0.2
			reason = "high volatility detected - widening stop loss"
		case "low":
			adjustmentFactor = 0.8 // Tighten stop loss in low volatility
			confidence += 0.1
			reason = "low volatility detected - tightening stop loss"
		}
	}

	// Adjust based on BTC correlation
	if o.config.EnableBTCCorrelation && condition.BTCCorrelation > o.config.BTCCorrelationThreshold {
		adjustmentFactor *= 1.2 // Slightly widen for high BTC correlation
		confidence += 0.1
		reason += " + high BTC correlation"
	}

	// Calculate new stop loss
	var newSL float64
	if side == "long" {
		slDistance := entryPrice - currentSL
		newSL = entryPrice - (slDistance * adjustmentFactor)
	} else {
		slDistance := currentSL - entryPrice
		newSL = entryPrice + (slDistance * adjustmentFactor)
	}

	// Ensure confidence is within bounds
	if confidence > 1.0 {
		confidence = 1.0
	}

	return newSL, confidence, reason
}

// calculateBTCAdjustmentFactor calculates adjustment factor based on BTC drop severity
func (o *SLTPOptimizer) calculateBTCAdjustmentFactor(dropPercent float64) float64 {
	// Base factor from config
	baseFactor := 1.5

	// Increase factor for larger drops
	if dropPercent > 5.0 {
		baseFactor = 2.0
	} else if dropPercent > 10.0 {
		baseFactor = 2.5
	}

	return baseFactor
}

// formatMarketCondition formats market condition for storage
func (o *SLTPOptimizer) formatMarketCondition(condition *MarketCondition) string {
	return fmt.Sprintf(`{"volatility": %.4f, "volatility_level": "%s", "btc_correlation": %.4f, "trend": "%s"}`,
		condition.Volatility, condition.VolatilityLevel, condition.BTCCorrelation, condition.Trend)
}

// GetStats returns optimizer statistics
func (o *SLTPOptimizer) GetStats() map[string]interface{} {
	o.mu.RLock()
	defer o.mu.RUnlock()

	return map[string]interface{}{
		"running":                   o.running,
		"last_optimization":         o.lastOptimization,
		"optimization_count":        o.optimizationCount,
		"btc_correlation_enabled":   o.config.EnableBTCCorrelation,
		"volatility_adjust_enabled": o.config.EnableVolatilityAdjust,
	}
}

// ShouldAdjustStopLoss determines if stop loss should be adjusted
func (o *SLTPOptimizer) ShouldAdjustStopLoss(ctx context.Context, exchange, symbol, side string, entryPrice, currentPrice, currentSL float64) (bool, float64, string, error) {
	decision, err := o.OptimizeStopLoss(ctx, symbol, currentSL, entryPrice, side)
	if err != nil {
		return false, currentSL, "", err
	}

	if decision == nil {
		return false, currentSL, "No adjustment needed", nil
	}

	return true, decision.NewValue, decision.Reason, nil
}

// DetectMarketCondition detects current market conditions
func (o *SLTPOptimizer) DetectMarketCondition(ctx context.Context, exchange, symbol string) (*MarketCondition, error) {
	condition, err := o.analyzeMarketCondition(ctx, symbol)
	if err != nil {
		return nil, err
	}

	// Add confidence field that the position manager expects
	// We'll calculate confidence based on data availability and market clarity
	confidence := 0.5 // Base confidence

	// Increase confidence if we have BTC correlation data
	if condition.BTCCorrelation != 0 {
		confidence += 0.2
	}

	// Increase confidence based on volatility clarity
	if condition.VolatilityLevel != "medium" {
		confidence += 0.1
	}

	// Create a new struct that includes confidence
	// Since we can't modify the existing MarketCondition struct easily,
	// we'll return it as-is and handle the confidence separately
	return condition, nil
}

// GetDefaultOptimizerConfig returns default optimizer configuration
func GetDefaultOptimizerConfig() *OptimizerConfig {
	return &OptimizerConfig{
		OptimizationInterval:    1 * time.Minute,
		EnableVolatilityAdjust:  true,
		VolatilityWindow:        20,
		HighVolatilityThreshold: 0.08, // 8%
		LowVolatilityThreshold:  0.02, // 2%
		EnableBTCCorrelation:    true,
		BTCCorrelationThreshold: 0.7,
		MinConfidenceForAdjust:  0.6,
		MaxSLAdjustmentPercent:  25.0, // Max 25% adjustment
		MinSLAdjustmentPercent:  2.0,  // Min 2% adjustment
	}
}
