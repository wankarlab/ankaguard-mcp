package ai

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/rantipay/trading-mcp/internal/domain"
	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/shopspring/decimal"
	"github.com/sirupsen/logrus"
)

// RiskCalculator calculates portfolio risk and exposure
type RiskCalculator struct {
	gctClient     gct.GCTClient
	positionRepo  domain.PositionRepository
	executionRepo domain.ExecutionHistoryRepository
	config        *PortfolioRiskConfig
	log           *logrus.Logger
}

// PortfolioRiskConfig defines portfolio risk management configuration
type PortfolioRiskConfig struct {
	MaxDailyLossPercent     float64 `yaml:"max_daily_loss_percent"`     // Maximum daily loss as % of portfolio (e.g., 5.0 = 5%)
	MaxPortfolioRiskPercent float64 `yaml:"max_portfolio_risk_percent"` // Maximum portfolio risk as % (e.g., 20.0 = 20%)
	MaxPositionRiskPercent  float64 `yaml:"max_position_risk_percent"`  // Maximum single position risk as % (e.g., 5.0 = 5%)
	LiquidationBuffer       float64 `yaml:"liquidation_buffer"`         // Buffer from liquidation price (e.g., 10.0 = 10%)
}

// DefaultPortfolioRiskConfig returns default risk configuration
func DefaultPortfolioRiskConfig() *PortfolioRiskConfig {
	return &PortfolioRiskConfig{
		MaxDailyLossPercent:     5.0,  // 5% max daily loss
		MaxPortfolioRiskPercent: 20.0, // 20% max portfolio risk
		MaxPositionRiskPercent:  5.0,  // 5% max per position
		LiquidationBuffer:       10.0, // 10% buffer from liquidation
	}
}

// NewRiskCalculator creates a new risk calculator
func NewRiskCalculator(
	gctClient gct.GCTClient,
	positionRepo domain.PositionRepository,
	executionRepo domain.ExecutionHistoryRepository,
	config *PortfolioRiskConfig,
	log *logrus.Logger,
) *RiskCalculator {
	if config == nil {
		config = DefaultPortfolioRiskConfig()
	}

	return &RiskCalculator{
		gctClient:     gctClient,
		positionRepo:  positionRepo,
		executionRepo: executionRepo,
		config:        config,
		log:           log,
	}
}

// PortfolioRisk represents portfolio risk metrics
type PortfolioRisk struct {
	TotalExposure      decimal.Decimal            `json:"total_exposure"`       // Total notional exposure
	TotalRisk          decimal.Decimal            `json:"total_risk"`           // Total risk amount (sum of potential losses)
	RiskPercentage     float64                    `json:"risk_percentage"`      // Risk as % of portfolio value
	MaxDrawdown        float64                    `json:"max_drawdown"`         // Maximum drawdown %
	DailyPnL           decimal.Decimal            `json:"daily_pnl"`            // Today's PnL
	DailyPnLPercent    float64                    `json:"daily_pnl_percent"`    // Today's PnL as %
	PortfolioValue     decimal.Decimal            `json:"portfolio_value"`      // Total portfolio value
	PositionCount      int                        `json:"position_count"`       // Number of open positions
	HighRiskPositions  []string                   `json:"high_risk_positions"`  // Position IDs with high risk
	ExposureBySymbol   map[string]decimal.Decimal `json:"exposure_by_symbol"`   // Exposure breakdown by symbol
	RiskBySymbol       map[string]decimal.Decimal `json:"risk_by_symbol"`       // Risk breakdown by symbol
	DailyLossLimitHit  bool                       `json:"daily_loss_limit_hit"` // Whether daily loss limit is reached
	PortfolioRiskLimit bool                       `json:"portfolio_risk_limit"` // Whether portfolio risk limit is reached
	Timestamp          time.Time                  `json:"timestamp"`
}

// PositionRisk represents individual position risk metrics
type PositionRisk struct {
	PositionID            string          `json:"position_id"`
	Symbol                string          `json:"symbol"`
	Side                  string          `json:"side"`
	Size                  decimal.Decimal `json:"size"`
	EntryPrice            decimal.Decimal `json:"entry_price"`
	CurrentPrice          decimal.Decimal `json:"current_price"`
	LiquidationPrice      decimal.Decimal `json:"liquidation_price"`
	RiskAmount            decimal.Decimal `json:"risk_amount"`             // Potential loss amount
	RiskPercentage        float64         `json:"risk_percentage"`         // Risk as % of portfolio
	DistanceToLiquidation float64         `json:"distance_to_liquidation"` // % distance to liquidation
	RiskLevel             string          `json:"risk_level"`              // "low", "medium", "high", "critical"
	UnrealizedPnL         decimal.Decimal `json:"unrealized_pnl"`
	UnrealizedPnLPercent  float64         `json:"unrealized_pnl_percent"`
	Leverage              int             `json:"leverage"`
	MarginUsed            decimal.Decimal `json:"margin_used"`
	Exposure              decimal.Decimal `json:"exposure"` // Notional exposure (size * price)
}

// CalculatePortfolioRisk calculates total portfolio risk and exposure
func (rc *RiskCalculator) CalculatePortfolioRisk(ctx context.Context) (*PortfolioRisk, error) {
	// Get all open positions from GoCryptoTrader
	gctPositions, err := rc.gctClient.GetFuturesPositions(ctx, "kucoin")
	if err != nil {
		return nil, fmt.Errorf("failed to get positions: %w", err)
	}

	// Get portfolio value
	portfolio, err := rc.gctClient.GetPortfolio(ctx)
	if err != nil {
		rc.log.WithError(err).Warn("Failed to get portfolio, using positions value")
		portfolio = &gct.Portfolio{TotalValue: 0}
	}

	portfolioValue := decimal.NewFromFloat(portfolio.TotalValue)
	if portfolioValue.IsZero() {
		// Fallback: calculate from positions
		portfolioValue = decimal.NewFromFloat(10000) // Default assumption
	}

	// Calculate daily PnL
	dailyPnL, err := rc.GetDailyPnL(ctx)
	if err != nil {
		rc.log.WithError(err).Warn("Failed to get daily PnL")
		dailyPnL = decimal.Zero
	}

	// Initialize risk metrics
	risk := &PortfolioRisk{
		TotalExposure:     decimal.Zero,
		TotalRisk:         decimal.Zero,
		RiskPercentage:    0,
		MaxDrawdown:       0,
		DailyPnL:          dailyPnL,
		PortfolioValue:    portfolioValue,
		PositionCount:     len(gctPositions),
		HighRiskPositions: make([]string, 0),
		ExposureBySymbol:  make(map[string]decimal.Decimal),
		RiskBySymbol:      make(map[string]decimal.Decimal),
		Timestamp:         time.Now(),
	}

	// Calculate risk for each position
	for _, gctPos := range gctPositions {
		posRisk, err := rc.calculatePositionRiskFromGCT(ctx, gctPos, portfolioValue)
		if err != nil {
			rc.log.WithError(err).WithField("symbol", gctPos.Symbol).Warn("Failed to calculate position risk")
			continue
		}

		// Accumulate totals
		risk.TotalExposure = risk.TotalExposure.Add(posRisk.Exposure)
		risk.TotalRisk = risk.TotalRisk.Add(posRisk.RiskAmount)

		// Track exposure and risk by symbol
		risk.ExposureBySymbol[posRisk.Symbol] = posRisk.Exposure
		risk.RiskBySymbol[posRisk.Symbol] = posRisk.RiskAmount

		// Track high-risk positions
		if posRisk.RiskLevel == "high" || posRisk.RiskLevel == "critical" {
			risk.HighRiskPositions = append(risk.HighRiskPositions, posRisk.PositionID)
		}
	}

	// Calculate risk percentage
	if !portfolioValue.IsZero() {
		riskPct, _ := risk.TotalRisk.Div(portfolioValue).Mul(decimal.NewFromInt(100)).Float64()
		risk.RiskPercentage = riskPct

		dailyPnLPct, _ := dailyPnL.Div(portfolioValue).Mul(decimal.NewFromInt(100)).Float64()
		risk.DailyPnLPercent = dailyPnLPct
	}

	// Check if limits are hit
	risk.DailyLossLimitHit = risk.DailyPnLPercent < -rc.config.MaxDailyLossPercent
	risk.PortfolioRiskLimit = risk.RiskPercentage > rc.config.MaxPortfolioRiskPercent

	rc.log.WithFields(logrus.Fields{
		"total_exposure":       risk.TotalExposure.String(),
		"total_risk":           risk.TotalRisk.String(),
		"risk_percentage":      fmt.Sprintf("%.2f%%", risk.RiskPercentage),
		"daily_pnl":            risk.DailyPnL.String(),
		"daily_pnl_percent":    fmt.Sprintf("%.2f%%", risk.DailyPnLPercent),
		"position_count":       risk.PositionCount,
		"high_risk_positions":  len(risk.HighRiskPositions),
		"daily_loss_limit_hit": risk.DailyLossLimitHit,
		"portfolio_risk_limit": risk.PortfolioRiskLimit,
	}).Info("Portfolio risk calculated")

	return risk, nil
}

// calculatePositionRiskFromGCT calculates risk for a GCT position
func (rc *RiskCalculator) calculatePositionRiskFromGCT(ctx context.Context, gctPos *gct.Position, portfolioValue decimal.Decimal) (*PositionRisk, error) {
	entryPrice := decimal.NewFromFloat(gctPos.EntryPrice)
	currentPrice := decimal.NewFromFloat(gctPos.CurrentPrice)
	size := decimal.NewFromFloat(gctPos.Size)
	liquidationPrice := decimal.NewFromFloat(gctPos.LiquidationPrice)
	unrealizedPnL := decimal.NewFromFloat(gctPos.UnrealizedPnL)
	margin := decimal.NewFromFloat(gctPos.Margin)

	// Calculate exposure (notional value)
	exposure := size.Mul(currentPrice)

	// Calculate risk amount (distance to liquidation)
	var riskAmount decimal.Decimal
	var distanceToLiquidation float64

	if gctPos.Side == "long" {
		// For long: risk is if price drops to liquidation
		priceDiff := currentPrice.Sub(liquidationPrice)
		riskAmount = size.Mul(priceDiff)
		if !currentPrice.IsZero() {
			distPct, _ := priceDiff.Div(currentPrice).Mul(decimal.NewFromInt(100)).Float64()
			distanceToLiquidation = distPct
		}
	} else {
		// For short: risk is if price rises to liquidation
		priceDiff := liquidationPrice.Sub(currentPrice)
		riskAmount = size.Mul(priceDiff)
		if !currentPrice.IsZero() {
			distPct, _ := priceDiff.Div(currentPrice).Mul(decimal.NewFromInt(100)).Float64()
			distanceToLiquidation = distPct
		}
	}

	// Ensure risk amount is positive
	if riskAmount.IsNegative() {
		riskAmount = decimal.Zero
	}

	// Calculate risk percentage
	var riskPercentage float64
	if !portfolioValue.IsZero() {
		riskPct, _ := riskAmount.Div(portfolioValue).Mul(decimal.NewFromInt(100)).Float64()
		riskPercentage = riskPct
	}

	// Calculate unrealized PnL percentage
	var unrealizedPnLPercent float64
	if !margin.IsZero() {
		pnlPct, _ := unrealizedPnL.Div(margin).Mul(decimal.NewFromInt(100)).Float64()
		unrealizedPnLPercent = pnlPct
	}

	// Determine risk level
	riskLevel := rc.determineRiskLevel(riskPercentage, distanceToLiquidation)

	posRisk := &PositionRisk{
		PositionID:            fmt.Sprintf("%s_%s", gctPos.Symbol, gctPos.Side),
		Symbol:                gctPos.Symbol,
		Side:                  gctPos.Side,
		Size:                  size,
		EntryPrice:            entryPrice,
		CurrentPrice:          currentPrice,
		LiquidationPrice:      liquidationPrice,
		RiskAmount:            riskAmount,
		RiskPercentage:        riskPercentage,
		DistanceToLiquidation: distanceToLiquidation,
		RiskLevel:             riskLevel,
		UnrealizedPnL:         unrealizedPnL,
		UnrealizedPnLPercent:  unrealizedPnLPercent,
		Leverage:              gctPos.Leverage,
		MarginUsed:            margin,
		Exposure:              exposure,
	}

	return posRisk, nil
}

// CalculatePositionRisk calculates risk for a specific position
func (rc *RiskCalculator) CalculatePositionRisk(ctx context.Context, position *domain.Position) (*PositionRisk, error) {
	// Get portfolio value for percentage calculations
	portfolio, err := rc.gctClient.GetPortfolio(ctx)
	if err != nil {
		rc.log.WithError(err).Warn("Failed to get portfolio")
		portfolio = &gct.Portfolio{TotalValue: 10000} // Default
	}
	portfolioValue := decimal.NewFromFloat(portfolio.TotalValue)

	// Get current price
	ticker, err := rc.gctClient.GetTicker(ctx, position.ExchangeID, position.Symbol)
	if err != nil {
		return nil, fmt.Errorf("failed to get ticker: %w", err)
	}
	currentPrice := decimal.NewFromFloat(ticker.Last)

	// Calculate exposure
	exposure := position.Size.Mul(currentPrice)

	// Estimate liquidation price (simplified calculation)
	// For a more accurate calculation, we'd need margin mode and other details
	var liquidationPrice decimal.Decimal
	leverageDec := decimal.NewFromInt(int64(position.Leverage))

	if position.Side == domain.PositionSideLong {
		// Long liquidation: entry * (1 - 1/leverage)
		liquidationPrice = position.EntryPrice.Mul(
			decimal.NewFromInt(1).Sub(decimal.NewFromInt(1).Div(leverageDec)),
		)
	} else {
		// Short liquidation: entry * (1 + 1/leverage)
		liquidationPrice = position.EntryPrice.Mul(
			decimal.NewFromInt(1).Add(decimal.NewFromInt(1).Div(leverageDec)),
		)
	}

	// Calculate risk amount
	var riskAmount decimal.Decimal
	var distanceToLiquidation float64

	if position.Side == domain.PositionSideLong {
		priceDiff := currentPrice.Sub(liquidationPrice)
		riskAmount = position.Size.Mul(priceDiff)
		if !currentPrice.IsZero() {
			distPct, _ := priceDiff.Div(currentPrice).Mul(decimal.NewFromInt(100)).Float64()
			distanceToLiquidation = distPct
		}
	} else {
		priceDiff := liquidationPrice.Sub(currentPrice)
		riskAmount = position.Size.Mul(priceDiff)
		if !currentPrice.IsZero() {
			distPct, _ := priceDiff.Div(currentPrice).Mul(decimal.NewFromInt(100)).Float64()
			distanceToLiquidation = distPct
		}
	}

	if riskAmount.IsNegative() {
		riskAmount = decimal.Zero
	}

	// Calculate risk percentage
	var riskPercentage float64
	if !portfolioValue.IsZero() {
		riskPct, _ := riskAmount.Div(portfolioValue).Mul(decimal.NewFromInt(100)).Float64()
		riskPercentage = riskPct
	}

	// Calculate unrealized PnL percentage
	var unrealizedPnLPercent float64
	if !position.MarginUsed.IsZero() {
		pnlPct, _ := position.UnrealizedPnL.Div(position.MarginUsed).Mul(decimal.NewFromInt(100)).Float64()
		unrealizedPnLPercent = pnlPct
	}

	// Determine risk level
	riskLevel := rc.determineRiskLevel(riskPercentage, distanceToLiquidation)

	return &PositionRisk{
		PositionID:            position.ID,
		Symbol:                position.Symbol,
		Side:                  string(position.Side),
		Size:                  position.Size,
		EntryPrice:            position.EntryPrice,
		CurrentPrice:          currentPrice,
		LiquidationPrice:      liquidationPrice,
		RiskAmount:            riskAmount,
		RiskPercentage:        riskPercentage,
		DistanceToLiquidation: distanceToLiquidation,
		RiskLevel:             riskLevel,
		UnrealizedPnL:         position.UnrealizedPnL,
		UnrealizedPnLPercent:  unrealizedPnLPercent,
		Leverage:              position.Leverage,
		MarginUsed:            position.MarginUsed,
		Exposure:              exposure,
	}, nil
}

// determineRiskLevel determines risk level based on metrics
func (rc *RiskCalculator) determineRiskLevel(riskPercentage, distanceToLiquidation float64) string {
	// Critical: very close to liquidation or exceeds position risk limit
	if distanceToLiquidation < rc.config.LiquidationBuffer || riskPercentage > rc.config.MaxPositionRiskPercent*1.5 {
		return "critical"
	}

	// High: approaching limits
	if distanceToLiquidation < rc.config.LiquidationBuffer*2 || riskPercentage > rc.config.MaxPositionRiskPercent {
		return "high"
	}

	// Medium: moderate risk
	if distanceToLiquidation < rc.config.LiquidationBuffer*3 || riskPercentage > rc.config.MaxPositionRiskPercent*0.5 {
		return "medium"
	}

	// Low: safe
	return "low"
}

// GetDailyPnL calculates today's PnL from execution history
func (rc *RiskCalculator) GetDailyPnL(ctx context.Context) (decimal.Decimal, error) {
	if rc.executionRepo == nil {
		return decimal.Zero, nil
	}

	// Get today's start time
	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	// Get executions from today
	executions, err := rc.executionRepo.GetByDateRange(ctx, startOfDay, now)
	if err != nil {
		return decimal.Zero, fmt.Errorf("failed to get execution history: %w", err)
	}

	// Sum up PnL
	totalPnL := decimal.Zero
	for _, exec := range executions {
		totalPnL = totalPnL.Add(exec.PnL)
	}

	return totalPnL, nil
}

// CheckDailyLossLimit checks if daily loss limit has been reached
func (rc *RiskCalculator) CheckDailyLossLimit(ctx context.Context) (bool, decimal.Decimal, error) {
	dailyPnL, err := rc.GetDailyPnL(ctx)
	if err != nil {
		return false, decimal.Zero, err
	}

	// Get portfolio value
	portfolio, err := rc.gctClient.GetPortfolio(ctx)
	if err != nil {
		return false, dailyPnL, fmt.Errorf("failed to get portfolio: %w", err)
	}

	portfolioValue := decimal.NewFromFloat(portfolio.TotalValue)
	if portfolioValue.IsZero() {
		return false, dailyPnL, nil
	}

	// Calculate daily loss percentage
	dailyLossPct, _ := dailyPnL.Div(portfolioValue).Mul(decimal.NewFromInt(100)).Float64()

	// Check if limit is hit (negative PnL exceeds limit)
	limitHit := dailyLossPct < -rc.config.MaxDailyLossPercent

	if limitHit {
		rc.log.WithFields(logrus.Fields{
			"daily_pnl":       dailyPnL.String(),
			"daily_loss_pct":  fmt.Sprintf("%.2f%%", dailyLossPct),
			"max_loss_pct":    fmt.Sprintf("%.2f%%", rc.config.MaxDailyLossPercent),
			"portfolio_value": portfolioValue.String(),
		}).Warn("⚠️  Daily loss limit reached!")
	}

	return limitHit, dailyPnL, nil
}

// PositionPriority represents a position with priority for closing
type PositionPriority struct {
	Position *gct.Position
	Priority float64 // Higher = close first
	Reason   string
}

// PrioritizePositionsToClose determines which positions to close when risk limit is exceeded
func (rc *RiskCalculator) PrioritizePositionsToClose(ctx context.Context) ([]*PositionPriority, error) {
	// Get all positions
	positions, err := rc.gctClient.GetFuturesPositions(ctx, "kucoin")
	if err != nil {
		return nil, fmt.Errorf("failed to get positions: %w", err)
	}

	if len(positions) == 0 {
		return []*PositionPriority{}, nil
	}

	// Get portfolio value
	portfolio, err := rc.gctClient.GetPortfolio(ctx)
	if err != nil {
		rc.log.WithError(err).Warn("Failed to get portfolio")
		portfolio = &gct.Portfolio{TotalValue: 10000}
	}
	portfolioValue := decimal.NewFromFloat(portfolio.TotalValue)

	// Calculate priority for each position
	priorities := make([]*PositionPriority, 0, len(positions))

	for _, pos := range positions {
		posRisk, err := rc.calculatePositionRiskFromGCT(ctx, pos, portfolioValue)
		if err != nil {
			rc.log.WithError(err).WithField("symbol", pos.Symbol).Warn("Failed to calculate position risk")
			continue
		}

		priority := rc.calculateClosePriority(posRisk)
		reason := rc.getCloseReason(posRisk)

		priorities = append(priorities, &PositionPriority{
			Position: pos,
			Priority: priority,
			Reason:   reason,
		})
	}

	// Sort by priority (highest first)
	sort.Slice(priorities, func(i, j int) bool {
		return priorities[i].Priority > priorities[j].Priority
	})

	rc.log.WithField("count", len(priorities)).Info("Positions prioritized for closing")

	return priorities, nil
}

// calculateClosePriority calculates priority score for closing a position
// Higher score = close first
func (rc *RiskCalculator) calculateClosePriority(posRisk *PositionRisk) float64 {
	priority := 0.0

	// Factor 1: Risk level (40% weight)
	switch posRisk.RiskLevel {
	case "critical":
		priority += 40.0
	case "high":
		priority += 30.0
	case "medium":
		priority += 15.0
	case "low":
		priority += 5.0
	}

	// Factor 2: Unrealized PnL (30% weight)
	// Prioritize closing losing positions
	if posRisk.UnrealizedPnLPercent < -10 {
		priority += 30.0 // Large loss
	} else if posRisk.UnrealizedPnLPercent < -5 {
		priority += 20.0 // Medium loss
	} else if posRisk.UnrealizedPnLPercent < 0 {
		priority += 10.0 // Small loss
	} else if posRisk.UnrealizedPnLPercent > 20 {
		priority += 5.0 // Large profit - consider taking profit
	}

	// Factor 3: Distance to liquidation (30% weight)
	if posRisk.DistanceToLiquidation < 10 {
		priority += 30.0 // Very close
	} else if posRisk.DistanceToLiquidation < 20 {
		priority += 20.0 // Close
	} else if posRisk.DistanceToLiquidation < 30 {
		priority += 10.0 // Moderate
	}

	return priority
}

// getCloseReason returns a human-readable reason for closing
func (rc *RiskCalculator) getCloseReason(posRisk *PositionRisk) string {
	reasons := []string{}

	if posRisk.RiskLevel == "critical" {
		reasons = append(reasons, "Critical risk level")
	}

	if posRisk.DistanceToLiquidation < rc.config.LiquidationBuffer {
		reasons = append(reasons, fmt.Sprintf("Too close to liquidation (%.1f%%)", posRisk.DistanceToLiquidation))
	}

	if posRisk.UnrealizedPnLPercent < -10 {
		reasons = append(reasons, fmt.Sprintf("Large unrealized loss (%.1f%%)", posRisk.UnrealizedPnLPercent))
	}

	if posRisk.RiskPercentage > rc.config.MaxPositionRiskPercent {
		reasons = append(reasons, fmt.Sprintf("Exceeds position risk limit (%.1f%%)", posRisk.RiskPercentage))
	}

	if len(reasons) == 0 {
		return "Risk management"
	}

	return reasons[0] // Return primary reason
}

// KellyInput is the input for Kelly Criterion position sizing
type KellyInput struct {
	Confidence       float64 // 0-1, confidence in the trade
	WinRate          float64 // 0-1, historical win rate of pair/strategy
	AvgWinPercent    float64 // average % gained on wins (e.g. 0.02 = 2%)
	AvgLossPercent   float64 // average % lost on losses (e.g. 0.01 = 1%)
	AvailableCapital float64 // available capital in USD
	CurrentPrice     float64 // current price of the pair
	ATR              float64 // current ATR (for volatility cap)
	MaxPositionUSD   float64 // hard cap from config
	MinPositionUSD   float64 // viable minimum (e.g. exchange min order size)

	// P4.1 — ML-calibrated probability fields (optional; zero = not available).
	// When set, MLProbability replaces WinRate as the forward-looking win estimate,
	// blended with the historical rate for stability.
	// MLSigma penalizes Kelly fraction when model uncertainty is high.
	MLProbability float64 // P(TP_first) from the ML predictor [0,1]
	MLSigma       float64 // model uncertainty σ [0,1]; higher → smaller size
	MLWeight      float64 // blend weight of ML prob vs historical [0,1] (e.g. 0.4)
}

// KellyResult is the result of the Kelly calculation
type KellyResult struct {
	SizeUSD          float64
	SizeUnits        float64 // SizeUSD / CurrentPrice
	KellyFraction    float64 // raw Kelly fraction calculated
	AdjustedFraction float64 // applied fraction (with safety factor)
	UsedConfidence   float64
	UsedWinRate      float64
	Reasoning        string
	AppliedCaps      []string // which caps were applied, e.g. "max_position", "atr_volatility"
}

// CalculateKellyPositionSize calculates size using Kelly + safety factor + caps.
// If insufficient data (WinRate=0 or insufficient samples), uses conservative fallback.
func (rc *RiskCalculator) CalculateKellyPositionSize(input *KellyInput) *KellyResult {
	result := &KellyResult{
		UsedConfidence: input.Confidence,
		UsedWinRate:    input.WinRate,
		AppliedCaps:    make([]string, 0),
	}

	// Clamp confidence to [0, 1]
	confidence := input.Confidence
	if confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}

	const (
		safetyFactor  = 0.5  // half-Kelly
		kellyHardCap  = 0.25 // never allocate more than 25% raw Kelly
		fallbackBase  = 0.02 // 2% fallback fraction
		highVolThresh = 0.05 // ATR/Price > 5% = high volatility
		highVolReduce = 0.5  // halve size on high vol
	)

	// P4.1: Blend ML probability with historical win rate when available.
	// MLWeight controls how much weight the ML forward estimate carries vs history.
	// Typical: MLWeight=0.4 means 40% ML probability, 60% historical win rate.
	effectiveWinRate := input.WinRate
	mlSource := ""
	if input.MLProbability > 0 && input.MLWeight > 0 {
		w := input.MLWeight
		if w > 1 {
			w = 1
		}
		blended := input.MLProbability*w + input.WinRate*(1-w)
		effectiveWinRate = blended
		mlSource = fmt.Sprintf(" [ML blend: hist=%.2f ml=%.2f w=%.1f → eff=%.2f]",
			input.WinRate, input.MLProbability, w, blended)
	}

	// Detect insufficient data → fallback
	insufficientData := effectiveWinRate <= 0 ||
		input.AvgWinPercent <= 0 ||
		input.AvgLossPercent <= 0

	var kellyFraction, adjustedFraction float64
	var reasoningPrefix string

	if insufficientData {
		// Conservative fallback: 2% of capital scaled by confidence
		kellyFraction = 0
		adjustedFraction = fallbackBase * confidence
		reasoningPrefix = "fallback conservative sizing - insufficient win rate data"
		result.AppliedCaps = append(result.AppliedCaps, "fallback_conservative")
	} else {
		// Kelly formula: f = (WinRate * (1 + R) - 1) / R
		// where R = AvgWin / AvgLoss
		R := input.AvgWinPercent / input.AvgLossPercent
		kellyFraction = (effectiveWinRate*(1+R) - 1) / R

		if kellyFraction <= 0 {
			// Negative edge → don't trade
			result.KellyFraction = kellyFraction
			result.AdjustedFraction = 0
			result.SizeUSD = 0
			result.SizeUnits = 0
			result.AppliedCaps = append(result.AppliedCaps, "negative_edge_no_trade")
			result.Reasoning = fmt.Sprintf(
				"Kelly: f=%.4f (negative edge), no trade%s",
				kellyFraction, mlSource,
			)
			return result
		}

		// Cap raw Kelly to 0.25 (full Kelly is too aggressive)
		if kellyFraction > kellyHardCap {
			result.AppliedCaps = append(result.AppliedCaps, "kelly_hard_cap_25pct")
			kellyFraction = kellyHardCap
		}

		// P4.1: Apply sigma penalty: high model uncertainty → reduce fraction.
		// sigma=1 (max uncertainty) → penalty=0.5; sigma=0 (no uncertainty) → no penalty.
		sigmaPenalty := 1.0
		if input.MLSigma > 0 {
			sigmaPenalty = 1.0 - input.MLSigma*0.5
			if sigmaPenalty < 0.5 {
				sigmaPenalty = 0.5
			}
			result.AppliedCaps = append(result.AppliedCaps, fmt.Sprintf("ml_sigma_penalty_%.2f", sigmaPenalty))
		}

		// Apply safety factor (half-Kelly), confidence scaling, and sigma penalty.
		adjustedFraction = kellyFraction * safetyFactor * confidence * sigmaPenalty
		reasoningPrefix = fmt.Sprintf(
			"Kelly: f=%.4f, half-Kelly + confidence adjustment = %.4f (sigma_penalty=%.2f)%s",
			kellyFraction, adjustedFraction, sigmaPenalty, mlSource,
		)
	}

	result.KellyFraction = kellyFraction
	result.AdjustedFraction = adjustedFraction

	// Compute initial size
	sizeUSD := input.AvailableCapital * adjustedFraction

	// Cap 1: MaxPositionUSD
	if input.MaxPositionUSD > 0 && sizeUSD > input.MaxPositionUSD {
		sizeUSD = input.MaxPositionUSD
		result.AppliedCaps = append(result.AppliedCaps, "max_position")
	}

	// Cap 2: Volatility (ATR-based)
	if input.CurrentPrice > 0 && input.ATR > 0 {
		atrRatio := input.ATR / input.CurrentPrice
		if atrRatio > highVolThresh {
			sizeUSD *= highVolReduce
			result.AppliedCaps = append(result.AppliedCaps, "atr_volatility")
		}
	}

	// Cap 3: MinPositionUSD floor → if below minimum, don't open
	if input.MinPositionUSD > 0 && sizeUSD < input.MinPositionUSD {
		result.AppliedCaps = append(result.AppliedCaps, "below_min_position_no_trade")
		result.SizeUSD = 0
		result.SizeUnits = 0
		result.Reasoning = fmt.Sprintf(
			"%s, capital allocated $%.2f below minimum $%.2f - no trade",
			reasoningPrefix, sizeUSD, input.MinPositionUSD,
		)
		return result
	}

	result.SizeUSD = sizeUSD
	if input.CurrentPrice > 0 {
		result.SizeUnits = sizeUSD / input.CurrentPrice
	}

	result.Reasoning = fmt.Sprintf(
		"%s, capital allocated $%.2f",
		reasoningPrefix, sizeUSD,
	)

	return result
}
