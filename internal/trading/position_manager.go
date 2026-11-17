package trading

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/risk"
	"github.com/sirupsen/logrus"
)

// PositionStatus represents the status of a position
type PositionStatus int

const (
	// PositionOpen indicates the position is currently open
	PositionOpen PositionStatus = iota
	// PositionClosing indicates the position is being closed
	PositionClosing
	// PositionClosed indicates the position has been closed
	PositionClosed
)

// String returns the string representation of PositionStatus
func (ps PositionStatus) String() string {
	switch ps {
	case PositionOpen:
		return "open"
	case PositionClosing:
		return "closing"
	case PositionClosed:
		return "closed"
	default:
		return "unknown"
	}
}

// Position represents a trading position
type Position struct {
	// Identification
	ID       string `json:"id"`
	Symbol   string `json:"symbol"`
	Exchange string `json:"exchange"`

	// Position details
	Side         string  `json:"side"` // "long" or "short"
	EntryPrice   float64 `json:"entry_price"`
	CurrentPrice float64 `json:"current_price"`
	Size         float64 `json:"size"`
	Leverage     float64 `json:"leverage"`

	// Risk management
	StopLoss         float64 `json:"stop_loss"`
	TakeProfit       float64 `json:"take_profit"`
	TrailingStop     bool    `json:"trailing_stop"`
	TrailingDistance float64 `json:"trailing_distance,omitempty"`
	BreakevenMoved   bool    `json:"breakeven_moved"`

	// P&L tracking
	UnrealizedPnL float64 `json:"unrealized_pnl"`
	RealizedPnL   float64 `json:"realized_pnl"`

	// Timestamps
	OpenTime  time.Time  `json:"open_time"`
	CloseTime *time.Time `json:"close_time,omitempty"`

	// Metadata
	Strategy    string         `json:"strategy"`
	Status      PositionStatus `json:"status"`
	CloseReason string         `json:"close_reason,omitempty"`

	// Internal tracking
	LastUpdate time.Time `json:"last_update"`
}

// PositionTracker is the interface for managing trading positions.
// This is the contract used by monitoring and risk tools; the concrete
// in-process implementation lives here as positionManager (unexported).
// The production position manager with full SL/TP and AI logic is
// internal/ai.PositionManager.
type PositionTracker interface {
	// OpenPosition opens a new position with Risk Sentinel validation
	OpenPosition(ctx context.Context, pos *Position) error

	// ClosePosition closes an existing position
	ClosePosition(ctx context.Context, positionID string, reason string) error

	// PartialClosePosition partially closes a position with specified size
	PartialClosePosition(ctx context.Context, positionID string, sizeToClose float64, reason string) error

	// UpdatePosition updates an existing position (price, P&L, etc.)
	UpdatePosition(ctx context.Context, pos *Position) error

	// GetPosition retrieves a specific position by ID
	GetPosition(ctx context.Context, positionID string) (*Position, error)

	// GetOpenPositions returns all open positions
	GetOpenPositions(ctx context.Context) ([]*Position, error)

	// GetOpenPositionsCount returns the count of open positions
	GetOpenPositionsCount(ctx context.Context) int

	// GetTotalExposure calculates total exposure across all positions
	GetTotalExposure(ctx context.Context) (float64, error)

	// GetExposureBySymbol calculates exposure for a specific symbol
	GetExposureBySymbol(ctx context.Context, symbol string) (float64, error)

	// CheckConflict checks if a new position would conflict with existing ones
	CheckConflict(ctx context.Context, newPos *Position) (bool, string, error)

	// GetPositionsByStrategy returns positions filtered by strategy
	GetPositionsByStrategy(ctx context.Context, strategy string) ([]*Position, error)

	// GetPositionHistory returns closed positions
	GetPositionHistory(ctx context.Context, limit int) ([]*Position, error)
}

// positionManager is the concrete implementation of PositionTracker
type positionManager struct {
	positions       map[string]*Position
	closedPositions []*Position
	gctClient       gct.GCTClient
	riskSentinel    risk.RiskSentinel
	logger          *logrus.Logger
	mu              sync.RWMutex

	// Configuration
	maxHistorySize int
}

// NewPositionManager creates a new Position Manager instance
func NewPositionManager(
	gctClient gct.GCTClient,
	riskSentinel risk.RiskSentinel,
	logger *logrus.Logger,
) PositionTracker {
	if logger == nil {
		logger = logrus.New()
	}

	return &positionManager{
		positions:       make(map[string]*Position),
		closedPositions: make([]*Position, 0),
		gctClient:       gctClient,
		riskSentinel:    riskSentinel,
		logger:          logger,
		maxHistorySize:  1000, // Keep last 1000 closed positions
	}
}

// OpenPosition opens a new position with Risk Sentinel validation
func (pm *positionManager) OpenPosition(ctx context.Context, pos *Position) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Validate position
	if err := pm.validatePosition(pos); err != nil {
		return fmt.Errorf("invalid position: %w", err)
	}

	// Check for conflicts
	hasConflict, reason, err := pm.checkConflictInternal(pos)
	if err != nil {
		return fmt.Errorf("conflict check failed: %w", err)
	}
	if hasConflict {
		return fmt.Errorf("position conflict: %s", reason)
	}

	// Validate with Risk Sentinel
	action := &risk.TradingAction{
		Type:     risk.ActionOpenPosition,
		Symbol:   pos.Symbol,
		Side:     pos.Side,
		Size:     pos.Size,
		Leverage: pos.Leverage,
		Price:    pos.EntryPrice,
	}

	decision, err := pm.riskSentinel.EvaluateAction(ctx, action)
	if err != nil {
		return fmt.Errorf("risk evaluation failed: %w", err)
	}

	if !decision.Allowed {
		pm.logger.WithFields(logrus.Fields{
			"symbol": pos.Symbol,
			"side":   pos.Side,
			"size":   pos.Size,
			"reason": decision.Reason,
		}).Warn("Position rejected by Risk Sentinel")
		return fmt.Errorf("risk sentinel rejected: %s", decision.Reason)
	}

	// Adjust size if needed
	if pos.Size > decision.MaxSize {
		pm.logger.WithFields(logrus.Fields{
			"original_size": pos.Size,
			"max_size":      decision.MaxSize,
		}).Warn("Reducing position size per Risk Sentinel")
		pos.Size = decision.MaxSize
	}

	// Adjust leverage if needed
	if pos.Leverage > decision.MaxLeverage {
		pm.logger.WithFields(logrus.Fields{
			"original_leverage": pos.Leverage,
			"max_leverage":      decision.MaxLeverage,
		}).Warn("Reducing leverage per Risk Sentinel")
		pos.Leverage = decision.MaxLeverage
	}

	// Generate position ID if not provided
	if pos.ID == "" {
		pos.ID = generatePositionID()
	}

	// Set initial values
	pos.Status = PositionOpen
	pos.OpenTime = time.Now()
	pos.LastUpdate = time.Now()
	pos.CurrentPrice = pos.EntryPrice
	pos.UnrealizedPnL = 0

	// Store position
	pm.positions[pos.ID] = pos

	// Note: Risk Sentinel exposure is updated via UpdateState when position closes

	pm.logger.WithFields(logrus.Fields{
		"position_id": pos.ID,
		"symbol":      pos.Symbol,
		"side":        pos.Side,
		"size":        pos.Size,
		"entry_price": pos.EntryPrice,
		"leverage":    pos.Leverage,
		"stop_loss":   pos.StopLoss,
		"take_profit": pos.TakeProfit,
		"strategy":    pos.Strategy,
	}).Info("Position opened successfully")

	// Log warnings if any
	for _, warning := range decision.Warnings {
		pm.logger.WithField("position_id", pos.ID).Warn(warning)
	}

	return nil
}

// ClosePosition closes an existing position
func (pm *positionManager) ClosePosition(ctx context.Context, positionID string, reason string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	pos, exists := pm.positions[positionID]
	if !exists {
		return fmt.Errorf("position not found: %s", positionID)
	}

	if pos.Status == PositionClosed {
		return fmt.Errorf("position already closed: %s", positionID)
	}

	// Mark as closing
	pos.Status = PositionClosing
	pos.CloseReason = reason

	pm.logger.WithFields(logrus.Fields{
		"position_id": positionID,
		"symbol":      pos.Symbol,
		"reason":      reason,
	}).Info("Closing position")

	// Execute close order via GCT
	// Note: In production, this would call gctClient.SubmitOrder
	// For now, we'll simulate the close

	// Calculate final P&L
	pos.RealizedPnL = pos.UnrealizedPnL

	// Mark as closed
	pos.Status = PositionClosed
	now := time.Now()
	pos.CloseTime = &now
	pos.LastUpdate = now

	// Update Risk Sentinel with trade result
	trade := &risk.Trade{
		Symbol:     pos.Symbol,
		Side:       pos.Side,
		Size:       pos.Size,
		EntryPrice: pos.EntryPrice,
		ExitPrice:  pos.CurrentPrice,
		PnL:        pos.RealizedPnL,
		Timestamp:  now,
	}

	if err := pm.riskSentinel.UpdateState(ctx, trade); err != nil {
		pm.logger.WithError(err).Error("Failed to update Risk Sentinel state")
		// Don't fail the close operation, just log the error
	}

	// Move to closed positions
	pm.closedPositions = append(pm.closedPositions, pos)

	// Trim history if needed
	if len(pm.closedPositions) > pm.maxHistorySize {
		pm.closedPositions = pm.closedPositions[len(pm.closedPositions)-pm.maxHistorySize:]
	}

	// Remove from active positions
	delete(pm.positions, positionID)

	pm.logger.WithFields(logrus.Fields{
		"position_id":  positionID,
		"symbol":       pos.Symbol,
		"realized_pnl": pos.RealizedPnL,
		"close_reason": reason,
		"duration":     now.Sub(pos.OpenTime).String(),
	}).Info("Position closed successfully")

	return nil
}

// PartialClosePosition partially closes a position with specified size
func (pm *positionManager) PartialClosePosition(ctx context.Context, positionID string, sizeToClose float64, reason string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	pos, exists := pm.positions[positionID]
	if !exists {
		return fmt.Errorf("position not found: %s", positionID)
	}

	if pos.Status == PositionClosed {
		return fmt.Errorf("position already closed: %s", positionID)
	}

	if sizeToClose <= 0 {
		return fmt.Errorf("invalid size to close: %f", sizeToClose)
	}

	if sizeToClose >= pos.Size {
		// If closing entire position, use regular close
		return pm.ClosePosition(ctx, positionID, reason)
	}

	pm.logger.WithFields(logrus.Fields{
		"position_id":   positionID,
		"symbol":        pos.Symbol,
		"size_to_close": sizeToClose,
		"remaining":     pos.Size - sizeToClose,
		"reason":        reason,
	}).Info("Partially closing position")

	// Execute partial close order via GCT
	// Note: In production, this would call gctClient.SubmitOrder
	// For now, we'll simulate the partial close

	// Calculate P&L for the closed portion
	var partialPnL float64
	if pos.Side == "long" {
		partialPnL = (pos.CurrentPrice - pos.EntryPrice) * sizeToClose
	} else {
		partialPnL = (pos.EntryPrice - pos.CurrentPrice) * sizeToClose
	}

	// Update position size and realized PnL
	pos.Size -= sizeToClose
	pos.RealizedPnL += partialPnL
	pos.LastUpdate = time.Now()

	// Update Risk Sentinel with partial trade result
	trade := &risk.Trade{
		Symbol:     pos.Symbol,
		Side:       pos.Side,
		Size:       sizeToClose,
		EntryPrice: pos.EntryPrice,
		ExitPrice:  pos.CurrentPrice,
		PnL:        partialPnL,
		Timestamp:  time.Now(),
	}

	if err := pm.riskSentinel.UpdateState(ctx, trade); err != nil {
		pm.logger.WithError(err).Error("Failed to update Risk Sentinel state for partial close")
		// Don't fail the operation, just log the error
	}

	pm.logger.WithFields(logrus.Fields{
		"position_id":    positionID,
		"symbol":         pos.Symbol,
		"closed_size":    sizeToClose,
		"remaining_size": pos.Size,
		"partial_pnl":    partialPnL,
		"close_reason":   reason,
	}).Info("Position partially closed successfully")

	return nil
}

// UpdatePosition updates an existing position
func (pm *positionManager) UpdatePosition(ctx context.Context, pos *Position) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	existing, exists := pm.positions[pos.ID]
	if !exists {
		return fmt.Errorf("position not found: %s", pos.ID)
	}

	if existing.Status != PositionOpen {
		return fmt.Errorf("cannot update non-open position: %s", pos.ID)
	}

	// Update price and P&L
	existing.CurrentPrice = pos.CurrentPrice
	existing.UnrealizedPnL = pm.calculatePnL(existing)
	existing.LastUpdate = time.Now()

	// Update SL/TP if provided
	if pos.StopLoss > 0 {
		existing.StopLoss = pos.StopLoss
	}
	if pos.TakeProfit > 0 {
		existing.TakeProfit = pos.TakeProfit
	}

	// Update trailing stop settings
	if pos.TrailingStop {
		existing.TrailingStop = pos.TrailingStop
		existing.TrailingDistance = pos.TrailingDistance
	}

	pm.logger.WithFields(logrus.Fields{
		"position_id":    pos.ID,
		"current_price":  existing.CurrentPrice,
		"unrealized_pnl": existing.UnrealizedPnL,
	}).Debug("Position updated")

	return nil
}

// GetPosition retrieves a specific position by ID
func (pm *positionManager) GetPosition(ctx context.Context, positionID string) (*Position, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	pos, exists := pm.positions[positionID]
	if !exists {
		return nil, fmt.Errorf("position not found: %s", positionID)
	}

	// Return a copy to prevent external modifications
	return pm.copyPosition(pos), nil
}

// GetOpenPositions returns all open positions
func (pm *positionManager) GetOpenPositions(ctx context.Context) ([]*Position, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	positions := make([]*Position, 0, len(pm.positions))
	for _, pos := range pm.positions {
		if pos.Status == PositionOpen {
			positions = append(positions, pm.copyPosition(pos))
		}
	}

	return positions, nil
}

// GetOpenPositionsCount returns the count of open positions
func (pm *positionManager) GetOpenPositionsCount(ctx context.Context) int {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	count := 0
	for _, pos := range pm.positions {
		if pos.Status == PositionOpen {
			count++
		}
	}

	return count
}

// GetTotalExposure calculates total exposure across all positions
func (pm *positionManager) GetTotalExposure(ctx context.Context) (float64, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	totalExposure := 0.0
	for _, pos := range pm.positions {
		if pos.Status == PositionOpen {
			exposure := pos.Size * pos.CurrentPrice * pos.Leverage
			totalExposure += exposure
		}
	}

	return totalExposure, nil
}

// GetExposureBySymbol calculates exposure for a specific symbol
func (pm *positionManager) GetExposureBySymbol(ctx context.Context, symbol string) (float64, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	exposure := 0.0
	for _, pos := range pm.positions {
		if pos.Status == PositionOpen && pos.Symbol == symbol {
			exposure += pos.Size * pos.CurrentPrice * pos.Leverage
		}
	}

	return exposure, nil
}

// CheckConflict checks if a new position would conflict with existing ones
func (pm *positionManager) CheckConflict(ctx context.Context, newPos *Position) (bool, string, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	return pm.checkConflictInternal(newPos)
}

// checkConflictInternal checks for conflicts (internal, no lock)
func (pm *positionManager) checkConflictInternal(newPos *Position) (bool, string, error) {
	// Check for opposite positions on same symbol
	for _, pos := range pm.positions {
		if pos.Symbol == newPos.Symbol && pos.Status == PositionOpen {
			if pos.Side != newPos.Side {
				return true, fmt.Sprintf("opposite position exists: %s %s (position %s)",
					pos.Side, pos.Symbol, pos.ID), nil
			}
		}
	}

	// TODO: Implement correlation-based conflict detection
	// This would check if opening a position on a correlated asset
	// would increase risk beyond acceptable levels

	return false, "", nil
}

// GetPositionsByStrategy returns positions filtered by strategy
func (pm *positionManager) GetPositionsByStrategy(ctx context.Context, strategy string) ([]*Position, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	positions := make([]*Position, 0)
	for _, pos := range pm.positions {
		if pos.Status == PositionOpen && pos.Strategy == strategy {
			positions = append(positions, pm.copyPosition(pos))
		}
	}

	return positions, nil
}

// GetPositionHistory returns closed positions
func (pm *positionManager) GetPositionHistory(ctx context.Context, limit int) ([]*Position, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	if limit <= 0 || limit > len(pm.closedPositions) {
		limit = len(pm.closedPositions)
	}

	// Return most recent positions
	start := len(pm.closedPositions) - limit
	positions := make([]*Position, limit)

	for i := 0; i < limit; i++ {
		positions[i] = pm.copyPosition(pm.closedPositions[start+i])
	}

	return positions, nil
}

// Helper functions

// validatePosition validates a position before opening
func (pm *positionManager) validatePosition(pos *Position) error {
	if pos.Symbol == "" {
		return fmt.Errorf("symbol is required")
	}

	if pos.Side != "long" && pos.Side != "short" {
		return fmt.Errorf("invalid side: %s (must be 'long' or 'short')", pos.Side)
	}

	if pos.Size <= 0 {
		return fmt.Errorf("size must be positive: %f", pos.Size)
	}

	if pos.EntryPrice <= 0 {
		return fmt.Errorf("entry price must be positive: %f", pos.EntryPrice)
	}

	if pos.Leverage <= 0 {
		return fmt.Errorf("leverage must be positive: %f", pos.Leverage)
	}

	// Validate SL/TP logic
	if pos.Side == "long" {
		if pos.StopLoss > 0 && pos.StopLoss >= pos.EntryPrice {
			return fmt.Errorf("stop loss must be below entry price for long positions")
		}
		if pos.TakeProfit > 0 && pos.TakeProfit <= pos.EntryPrice {
			return fmt.Errorf("take profit must be above entry price for long positions")
		}
	} else { // short
		if pos.StopLoss > 0 && pos.StopLoss <= pos.EntryPrice {
			return fmt.Errorf("stop loss must be above entry price for short positions")
		}
		if pos.TakeProfit > 0 && pos.TakeProfit >= pos.EntryPrice {
			return fmt.Errorf("take profit must be below entry price for short positions")
		}
	}

	return nil
}

// calculatePnL calculates unrealized P&L for a position
func (pm *positionManager) calculatePnL(pos *Position) float64 {
	if pos.Side == "long" {
		return (pos.CurrentPrice - pos.EntryPrice) * pos.Size
	}
	// short
	return (pos.EntryPrice - pos.CurrentPrice) * pos.Size
}

// copyPosition creates a deep copy of a position
func (pm *positionManager) copyPosition(pos *Position) *Position {
	copy := *pos
	if pos.CloseTime != nil {
		closeTime := *pos.CloseTime
		copy.CloseTime = &closeTime
	}
	return &copy
}

// generatePositionID generates a unique position ID
func generatePositionID() string {
	return fmt.Sprintf("pos_%s", uuid.New().String()[:8])
}
