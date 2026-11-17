package trading

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rantipay/trading-mcp/internal/domain"
	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/shopspring/decimal"
	"github.com/sirupsen/logrus"
)

// SLTPConfig holds the configuration for the SL/TP Executor
type SLTPConfig struct {
	// Monitoring interval
	CheckIntervalMs int `yaml:"check_interval_ms"`

	// Execution settings
	SlippageTolerancePct float64 `yaml:"slippage_tolerance_pct"`
	MaxRetries           int     `yaml:"max_retries"`
	RetryDelayMs         int     `yaml:"retry_delay_ms"`

	// Trailing stop settings
	TrailingStopEnabled bool `yaml:"trailing_stop_enabled"`

	// Breakeven settings
	BreakevenEnabled    bool    `yaml:"breakeven_enabled"`
	BreakevenTriggerPct float64 `yaml:"breakeven_trigger_pct"`
	BreakevenBufferPct  float64 `yaml:"breakeven_buffer_pct"`
}

// SLTPExecutor is the interface for automatic SL/TP execution
type SLTPExecutor interface {
	// Start begins monitoring positions
	Start(ctx context.Context) error

	// Stop stops monitoring
	Stop() error

	// IsRunning returns whether the executor is running
	IsRunning() bool

	// MonitorPosition adds a position to monitoring
	MonitorPosition(pos *Position) error

	// RemovePosition removes a position from monitoring
	RemovePosition(positionID string) error

	// UpdateSLTP updates SL/TP for a position
	UpdateSLTP(ctx context.Context, positionID string, sl, tp float64) error

	// EnableTrailingStop enables trailing stop for a position
	EnableTrailingStop(ctx context.Context, positionID string, distance float64) error

	// MoveToBreakeven moves SL to breakeven for a position
	MoveToBreakeven(ctx context.Context, positionID string) error

	// GetStats returns execution statistics
	GetStats() *ExecutionStats
}

// ExecutionStats holds statistics about SL/TP executions
type ExecutionStats struct {
	TotalChecks          int64     `json:"total_checks"`
	StopLossExecutions   int64     `json:"stop_loss_executions"`
	TakeProfitExecutions int64     `json:"take_profit_executions"`
	BreakevenMoves       int64     `json:"breakeven_moves"`
	TrailingStopUpdates  int64     `json:"trailing_stop_updates"`
	FailedExecutions     int64     `json:"failed_executions"`
	LastCheck            time.Time `json:"last_check"`
	AverageLatencyMs     float64   `json:"average_latency_ms"`
}

// SLTPNotifier interface for sending notifications
type SLTPNotifier interface {
	NotifyStopLoss(ctx context.Context, executionHistory *domain.ExecutionHistory, config interface{}) error
	NotifyTakeProfit(ctx context.Context, executionHistory *domain.ExecutionHistory, config interface{}) error
}

// sltpExecutor is the concrete implementation
type sltpExecutor struct {
	config          *SLTPConfig
	positionManager PositionTracker
	gctClient       gct.GCTClient
	logger          *logrus.Logger

	// Repositories for persistence
	stopLossRepo      domain.StopLossRepository
	takeProfitRepo    domain.TakeProfitRepository
	executionHistRepo domain.ExecutionHistoryRepository

	// Notifier for execution events
	notifier SLTPNotifier

	// State
	running  bool
	stopChan chan struct{}
	mu       sync.RWMutex

	// Statistics
	stats *ExecutionStats

	// Tracking
	monitoredPositions map[string]*positionTracker
	trackerMu          sync.RWMutex
}

// positionTracker tracks additional state for a monitored position
type positionTracker struct {
	position           *Position
	highestPrice       float64 // For trailing stop (long)
	lowestPrice        float64 // For trailing stop (short)
	breakevenTriggered bool
	lastUpdate         time.Time
}

// NewSLTPExecutor creates a new SL/TP Executor
func NewSLTPExecutor(
	config *SLTPConfig,
	positionManager PositionTracker,
	gctClient gct.GCTClient,
	logger *logrus.Logger,
) SLTPExecutor {
	return NewSLTPExecutorWithRepos(config, positionManager, gctClient, nil, nil, nil, nil, logger)
}

// NewSLTPExecutorWithRepos creates a new SL/TP Executor with repository support
func NewSLTPExecutorWithRepos(
	config *SLTPConfig,
	positionManager PositionTracker,
	gctClient gct.GCTClient,
	stopLossRepo domain.StopLossRepository,
	takeProfitRepo domain.TakeProfitRepository,
	executionHistRepo domain.ExecutionHistoryRepository,
	notifier SLTPNotifier,
	logger *logrus.Logger,
) SLTPExecutor {
	if logger == nil {
		logger = logrus.New()
	}

	// Set defaults
	if config.CheckIntervalMs == 0 {
		config.CheckIntervalMs = 100 // 100ms default
	}
	if config.MaxRetries == 0 {
		config.MaxRetries = 3
	}
	if config.RetryDelayMs == 0 {
		config.RetryDelayMs = 1000
	}
	if config.SlippageTolerancePct == 0 {
		config.SlippageTolerancePct = 0.1
	}
	if config.BreakevenTriggerPct == 0 {
		config.BreakevenTriggerPct = 0.3
	}
	if config.BreakevenBufferPct == 0 {
		config.BreakevenBufferPct = 0.05
	}

	return &sltpExecutor{
		config:             config,
		positionManager:    positionManager,
		gctClient:          gctClient,
		stopLossRepo:       stopLossRepo,
		takeProfitRepo:     takeProfitRepo,
		executionHistRepo:  executionHistRepo,
		notifier:           notifier,
		logger:             logger,
		stopChan:           make(chan struct{}),
		stats:              &ExecutionStats{},
		monitoredPositions: make(map[string]*positionTracker),
	}
}

// Start begins monitoring positions
func (se *sltpExecutor) Start(ctx context.Context) error {
	se.mu.Lock()
	if se.running {
		se.mu.Unlock()
		return fmt.Errorf("executor already running")
	}
	se.running = true
	se.mu.Unlock()

	se.logger.WithField("interval_ms", se.config.CheckIntervalMs).Info("Starting SL/TP Executor")

	// Load persisted SL/TP configurations if repositories are available
	if se.stopLossRepo != nil {
		if err := se.loadPersistedConfigurations(ctx); err != nil {
			se.logger.WithError(err).Warn("Failed to load persisted configurations")
		}
	}

	// Start monitoring loop
	go se.monitoringLoop(ctx)

	return nil
}

// loadPersistedConfigurations loads active SL/TP configurations from database
func (se *sltpExecutor) loadPersistedConfigurations(ctx context.Context) error {
	// Load active stop losses
	stopLosses, err := se.stopLossRepo.GetActive(ctx)
	if err != nil {
		return fmt.Errorf("failed to load stop losses: %w", err)
	}

	// Load active take profits
	takeProfits, err := se.takeProfitRepo.GetActive(ctx)
	if err != nil {
		return fmt.Errorf("failed to load take profits: %w", err)
	}

	se.logger.WithFields(logrus.Fields{
		"stop_losses":  len(stopLosses),
		"take_profits": len(takeProfits),
	}).Info("Loaded persisted SL/TP configurations")

	// Restore monitoring for positions with active SL/TP
	positionIDs := make(map[string]bool)
	for _, sl := range stopLosses {
		positionIDs[sl.PositionID] = true
	}
	for _, tp := range takeProfits {
		positionIDs[tp.PositionID] = true
	}

	// Get positions from position manager and add to monitoring
	for posID := range positionIDs {
		pos, err := se.positionManager.GetPosition(ctx, posID)
		if err != nil {
			se.logger.WithError(err).WithField("position_id", posID).Warn("Failed to get position")
			continue
		}

		// Restore SL/TP values from database
		for _, sl := range stopLosses {
			if sl.PositionID == posID {
				pos.StopLoss = sl.StopPrice.InexactFloat64()
				pos.TrailingStop = sl.TrailingStop
				pos.TrailingDistance = sl.TrailingDelta.InexactFloat64()
				break
			}
		}

		for _, tp := range takeProfits {
			if tp.PositionID == posID && len(tp.Levels) > 0 {
				// Use first level as main take profit
				pos.TakeProfit = tp.Levels[0].Price.InexactFloat64()
				break
			}
		}

		// Add to monitoring
		if err := se.MonitorPosition(pos); err != nil {
			se.logger.WithError(err).WithField("position_id", posID).Warn("Failed to monitor position")
		}
	}

	return nil
}

// Stop stops monitoring
func (se *sltpExecutor) Stop() error {
	se.mu.Lock()
	defer se.mu.Unlock()

	if !se.running {
		return fmt.Errorf("executor not running")
	}

	se.logger.Info("Stopping SL/TP Executor")
	close(se.stopChan)
	se.running = false

	return nil
}

// IsRunning returns whether the executor is running
func (se *sltpExecutor) IsRunning() bool {
	se.mu.RLock()
	defer se.mu.RUnlock()
	return se.running
}

// MonitorPosition adds a position to monitoring
func (se *sltpExecutor) MonitorPosition(pos *Position) error {
	se.trackerMu.Lock()
	defer se.trackerMu.Unlock()

	tracker := &positionTracker{
		position:     pos,
		highestPrice: pos.CurrentPrice,
		lowestPrice:  pos.CurrentPrice,
		lastUpdate:   time.Now(),
	}

	se.monitoredPositions[pos.ID] = tracker

	// Persist stop loss if configured and repository is available
	if pos.StopLoss > 0 && se.stopLossRepo != nil {
		stopLoss := &domain.StopLoss{
			ID:            generateSLTPID("sl"),
			PositionID:    pos.ID,
			Symbol:        pos.Symbol,
			Exchange:      pos.Exchange,
			StopPrice:     decimal.NewFromFloat(pos.StopLoss),
			TrailingStop:  pos.TrailingStop,
			TrailingDelta: decimal.NewFromFloat(pos.TrailingDistance),
			AIManaged:     false,
			Status:        "active",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}

		if err := se.stopLossRepo.Create(context.Background(), stopLoss); err != nil {
			se.logger.WithError(err).Warn("Failed to persist stop loss")
		}
	}

	// Persist take profit if configured and repository is available
	if pos.TakeProfit > 0 && se.takeProfitRepo != nil {
		takeProfit := &domain.TakeProfit{
			ID:         generateSLTPID("tp"),
			PositionID: pos.ID,
			Symbol:     pos.Symbol,
			Exchange:   pos.Exchange,
			Levels: []domain.TPLevel{
				{
					Price:      decimal.NewFromFloat(pos.TakeProfit),
					Percentage: decimal.NewFromInt(100), // 100% of position
					Executed:   false,
				},
			},
			Status:    "active",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}

		if err := se.takeProfitRepo.Create(context.Background(), takeProfit); err != nil {
			se.logger.WithError(err).Warn("Failed to persist take profit")
		}
	}

	se.logger.WithFields(logrus.Fields{
		"position_id": pos.ID,
		"symbol":      pos.Symbol,
		"side":        pos.Side,
	}).Info("Position added to SL/TP monitoring")

	return nil
}

// RemovePosition removes a position from monitoring
func (se *sltpExecutor) RemovePosition(positionID string) error {
	se.trackerMu.Lock()
	defer se.trackerMu.Unlock()

	delete(se.monitoredPositions, positionID)

	se.logger.WithField("position_id", positionID).Info("Position removed from monitoring")

	return nil
}

// monitoringLoop is the main monitoring loop
func (se *sltpExecutor) monitoringLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(se.config.CheckIntervalMs) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			se.logger.Info("Monitoring loop stopped: context cancelled")
			return
		case <-se.stopChan:
			se.logger.Info("Monitoring loop stopped: stop signal received")
			return
		case <-ticker.C:
			startTime := time.Now()
			se.checkAllPositions(ctx)

			// Update stats
			se.mu.Lock()
			se.stats.TotalChecks++
			se.stats.LastCheck = time.Now()
			latency := time.Since(startTime).Milliseconds()

			// Calculate rolling average latency
			if se.stats.AverageLatencyMs == 0 {
				se.stats.AverageLatencyMs = float64(latency)
			} else {
				se.stats.AverageLatencyMs = (se.stats.AverageLatencyMs*0.9 + float64(latency)*0.1)
			}
			se.mu.Unlock()
		}
	}
}

// checkAllPositions checks all monitored positions
func (se *sltpExecutor) checkAllPositions(ctx context.Context) {
	se.trackerMu.RLock()
	trackers := make([]*positionTracker, 0, len(se.monitoredPositions))
	for _, tracker := range se.monitoredPositions {
		trackers = append(trackers, tracker)
	}
	se.trackerMu.RUnlock()

	for _, tracker := range trackers {
		se.checkPosition(ctx, tracker)
	}
}

// checkPosition checks a single position
func (se *sltpExecutor) checkPosition(ctx context.Context, tracker *positionTracker) {
	pos := tracker.position

	// Get current price from position manager
	currentPos, err := se.positionManager.GetPosition(ctx, pos.ID)
	if err != nil {
		se.logger.WithError(err).WithField("position_id", pos.ID).Warn("Failed to get position")
		return
	}

	// Update tracker
	tracker.position = currentPos
	currentPrice := currentPos.CurrentPrice

	// Update highest/lowest for trailing stop
	if currentPos.Side == "long" && currentPrice > tracker.highestPrice {
		tracker.highestPrice = currentPrice
	} else if currentPos.Side == "short" && (tracker.lowestPrice == 0 || currentPrice < tracker.lowestPrice) {
		tracker.lowestPrice = currentPrice
	}

	// Check Stop Loss
	if se.shouldTriggerStopLoss(currentPos, currentPrice) {
		se.logger.WithFields(logrus.Fields{
			"position_id":   pos.ID,
			"symbol":        pos.Symbol,
			"current_price": currentPrice,
			"stop_loss":     pos.StopLoss,
		}).Warn("Stop loss triggered")

		if err := se.executeStopLoss(ctx, currentPos); err != nil {
			se.logger.WithError(err).Error("Failed to execute stop loss")
			se.mu.Lock()
			se.stats.FailedExecutions++
			se.mu.Unlock()
		} else {
			se.mu.Lock()
			se.stats.StopLossExecutions++
			se.mu.Unlock()
			se.RemovePosition(pos.ID)
		}
		return
	}

	// Check Multi-Level Take Profit
	if err := se.checkMultiLevelTakeProfit(ctx, currentPos, currentPrice); err != nil {
		se.logger.WithError(err).Error("Failed to check multi-level take profit")
		se.mu.Lock()
		se.stats.FailedExecutions++
		se.mu.Unlock()
	}

	// Check legacy single Take Profit (for backward compatibility)
	if se.shouldTriggerTakeProfit(currentPos, currentPrice) {
		se.logger.WithFields(logrus.Fields{
			"position_id":   pos.ID,
			"symbol":        pos.Symbol,
			"current_price": currentPrice,
			"take_profit":   pos.TakeProfit,
		}).Info("Legacy take profit triggered")

		if err := se.executeTakeProfit(ctx, currentPos); err != nil {
			se.logger.WithError(err).Error("Failed to execute take profit")
			se.mu.Lock()
			se.stats.FailedExecutions++
			se.mu.Unlock()
		} else {
			se.mu.Lock()
			se.stats.TakeProfitExecutions++
			se.mu.Unlock()
			se.RemovePosition(pos.ID)
		}
		return
	}

	// Check Trailing Stop
	if se.config.TrailingStopEnabled && currentPos.TrailingStop {
		se.updateTrailingStop(ctx, tracker, currentPrice)
	}

	// Check Breakeven
	if se.config.BreakevenEnabled && !tracker.breakevenTriggered {
		se.checkBreakeven(ctx, tracker, currentPrice)
	}

	tracker.lastUpdate = time.Now()
}

// shouldTriggerStopLoss checks if stop loss should be triggered
func (se *sltpExecutor) shouldTriggerStopLoss(pos *Position, currentPrice float64) bool {
	if pos.StopLoss == 0 {
		return false
	}

	if pos.Side == "long" {
		return currentPrice <= pos.StopLoss
	}
	// short
	return currentPrice >= pos.StopLoss
}

// shouldTriggerTakeProfit checks if take profit should be triggered
func (se *sltpExecutor) shouldTriggerTakeProfit(pos *Position, currentPrice float64) bool {
	if pos.TakeProfit == 0 {
		return false
	}

	if pos.Side == "long" {
		return currentPrice >= pos.TakeProfit
	}
	// short
	return currentPrice <= pos.TakeProfit
}

// checkMultiLevelTakeProfit checks and executes multi-level take profit
func (se *sltpExecutor) checkMultiLevelTakeProfit(ctx context.Context, pos *Position, currentPrice float64) error {
	if se.takeProfitRepo == nil {
		return nil
	}

	// Get take profit configuration from database
	takeProfit, err := se.takeProfitRepo.GetByPositionID(ctx, pos.ID)
	if err != nil || takeProfit == nil || takeProfit.Status != "active" {
		return nil
	}

	// Check each TP level
	for i, level := range takeProfit.Levels {
		if level.Executed {
			continue // Skip already executed levels
		}

		// Check if this level should be triggered
		shouldTrigger := false
		if pos.Side == "long" {
			shouldTrigger = currentPrice >= level.Price.InexactFloat64()
		} else {
			shouldTrigger = currentPrice <= level.Price.InexactFloat64()
		}

		if shouldTrigger {
			// Execute this TP level
			if err := se.executeTPLevel(ctx, pos, takeProfit, i, currentPrice); err != nil {
				se.logger.WithError(err).WithFields(logrus.Fields{
					"position_id": pos.ID,
					"tp_level":    i + 1,
					"tp_price":    level.Price.InexactFloat64(),
				}).Error("Failed to execute TP level")
				continue
			}

			// Mark level as executed
			takeProfit.Levels[i].Executed = true
			now := time.Now()
			takeProfit.Levels[i].ExecutedAt = &now

			// Update in database
			if err := se.takeProfitRepo.Update(ctx, takeProfit); err != nil {
				se.logger.WithError(err).Warn("Failed to update TP level status")
			}
		}
	}

	return nil
}

// executeTPLevel executes a specific take profit level
func (se *sltpExecutor) executeTPLevel(ctx context.Context, pos *Position, tp *domain.TakeProfit, levelIndex int, currentPrice float64) error {
	level := tp.Levels[levelIndex]

	// Calculate size to close (percentage of original position)
	sizeToClose := pos.Size * (level.Percentage.InexactFloat64() / 100.0)

	se.logger.WithFields(logrus.Fields{
		"position_id":   pos.ID,
		"symbol":        pos.Symbol,
		"tp_level":      levelIndex + 1,
		"tp_price":      level.Price.InexactFloat64(),
		"percentage":    level.Percentage.InexactFloat64(),
		"size_to_close": sizeToClose,
		"current_price": currentPrice,
	}).Info("Executing TP level")

	// Execute partial close via position manager
	err := se.positionManager.PartialClosePosition(ctx, pos.ID, sizeToClose, fmt.Sprintf("take_profit_level_%d", levelIndex+1))
	if err != nil {
		return fmt.Errorf("failed to partially close position: %w", err)
	}

	// Calculate PnL for this partial close
	var pnl float64
	if pos.Side == "long" {
		pnl = (currentPrice - pos.EntryPrice) * sizeToClose
	} else {
		pnl = (pos.EntryPrice - currentPrice) * sizeToClose
	}

	// Save execution history
	if se.executionHistRepo != nil {
		history := &domain.ExecutionHistory{
			ID:             generateSLTPID("exec"),
			PositionID:     pos.ID,
			Type:           "take_profit",
			Symbol:         pos.Symbol,
			Exchange:       pos.Exchange,
			TriggerPrice:   level.Price,
			ExecutionPrice: decimal.NewFromFloat(currentPrice),
			Size:           decimal.NewFromFloat(sizeToClose),
			PnL:            decimal.NewFromFloat(pnl),
			Reason:         fmt.Sprintf("Take profit level %d triggered (%.1f%%)", levelIndex+1, level.Percentage.InexactFloat64()),
			ExecutedAt:     time.Now(),
		}

		if err := se.executionHistRepo.Create(ctx, history); err != nil {
			se.logger.WithError(err).Warn("Failed to save TP execution history")
		} else {
			// Send notification if notifier is available
			if se.notifier != nil {
				if err := se.notifier.NotifyTakeProfit(ctx, history, nil); err != nil {
					se.logger.WithError(err).Warn("Failed to send TP notification")
				}
			}
		}
	}

	// Update position size
	pos.Size -= sizeToClose

	return nil
}

// executeStopLoss executes a stop loss order
func (se *sltpExecutor) executeStopLoss(ctx context.Context, pos *Position) error {
	se.logger.WithFields(logrus.Fields{
		"position_id": pos.ID,
		"symbol":      pos.Symbol,
		"side":        pos.Side,
	}).Warn("Executing stop loss")

	// Close position via position manager
	err := se.positionManager.ClosePosition(ctx, pos.ID, "stop_loss")
	if err != nil {
		return err
	}

	// Mark stop loss as triggered in database
	if se.stopLossRepo != nil {
		stopLoss, err := se.stopLossRepo.GetByPositionID(ctx, pos.ID)
		if err == nil && stopLoss != nil {
			if err := se.stopLossRepo.MarkTriggered(ctx, stopLoss.ID, time.Now()); err != nil {
				se.logger.WithError(err).Warn("Failed to mark stop loss as triggered")
			}
		}
	}

	// Save execution history
	if se.executionHistRepo != nil {
		history := &domain.ExecutionHistory{
			ID:             generateSLTPID("exec"),
			PositionID:     pos.ID,
			Type:           "stop_loss",
			Symbol:         pos.Symbol,
			Exchange:       pos.Exchange,
			TriggerPrice:   decimal.NewFromFloat(pos.StopLoss),
			ExecutionPrice: decimal.NewFromFloat(pos.CurrentPrice),
			Size:           decimal.NewFromFloat(pos.Size),
			PnL:            decimal.NewFromFloat(pos.UnrealizedPnL),
			Reason:         "Stop loss triggered",
			ExecutedAt:     time.Now(),
		}

		if err := se.executionHistRepo.Create(ctx, history); err != nil {
			se.logger.WithError(err).Warn("Failed to save execution history")
		} else {
			// Send notification if notifier is available
			if se.notifier != nil {
				if err := se.notifier.NotifyStopLoss(ctx, history, nil); err != nil {
					se.logger.WithError(err).Warn("Failed to send stop loss notification")
				}
			}
		}
	}

	return nil
}

// executeTakeProfit executes a take profit order
func (se *sltpExecutor) executeTakeProfit(ctx context.Context, pos *Position) error {
	se.logger.WithFields(logrus.Fields{
		"position_id": pos.ID,
		"symbol":      pos.Symbol,
		"side":        pos.Side,
	}).Info("Executing take profit")

	// Close position via position manager
	err := se.positionManager.ClosePosition(ctx, pos.ID, "take_profit")
	if err != nil {
		return err
	}

	// Update take profit status in database
	if se.takeProfitRepo != nil {
		takeProfit, err := se.takeProfitRepo.GetByPositionID(ctx, pos.ID)
		if err == nil && takeProfit != nil {
			takeProfit.Status = "triggered"
			if err := se.takeProfitRepo.Update(ctx, takeProfit); err != nil {
				se.logger.WithError(err).Warn("Failed to update take profit status")
			}
		}
	}

	// Save execution history
	if se.executionHistRepo != nil {
		history := &domain.ExecutionHistory{
			ID:             generateSLTPID("exec"),
			PositionID:     pos.ID,
			Type:           "take_profit",
			Symbol:         pos.Symbol,
			Exchange:       pos.Exchange,
			TriggerPrice:   decimal.NewFromFloat(pos.TakeProfit),
			ExecutionPrice: decimal.NewFromFloat(pos.CurrentPrice),
			Size:           decimal.NewFromFloat(pos.Size),
			PnL:            decimal.NewFromFloat(pos.UnrealizedPnL),
			Reason:         "Take profit triggered",
			ExecutedAt:     time.Now(),
		}

		if err := se.executionHistRepo.Create(ctx, history); err != nil {
			se.logger.WithError(err).Warn("Failed to save execution history")
		} else {
			// Send notification if notifier is available
			if se.notifier != nil {
				if err := se.notifier.NotifyTakeProfit(ctx, history, nil); err != nil {
					se.logger.WithError(err).Warn("Failed to send take profit notification")
				}
			}
		}
	}

	return nil
}

// updateTrailingStop updates trailing stop for a position
func (se *sltpExecutor) updateTrailingStop(ctx context.Context, tracker *positionTracker, currentPrice float64) {
	pos := tracker.position

	if pos.TrailingDistance == 0 {
		return
	}

	var newStopLoss float64
	shouldUpdate := false

	if pos.Side == "long" {
		// For long, move SL up as price increases
		newStopLoss = tracker.highestPrice * (1 - pos.TrailingDistance/100.0)
		shouldUpdate = newStopLoss > pos.StopLoss
	} else {
		// For short, move SL down as price decreases
		newStopLoss = tracker.lowestPrice * (1 + pos.TrailingDistance/100.0)
		shouldUpdate = newStopLoss < pos.StopLoss || pos.StopLoss == 0
	}

	if shouldUpdate {
		oldStopLoss := pos.StopLoss
		pos.StopLoss = newStopLoss

		// Update position
		if err := se.positionManager.UpdatePosition(ctx, pos); err != nil {
			se.logger.WithError(err).Error("Failed to update trailing stop")
			return
		}

		se.logger.WithFields(logrus.Fields{
			"position_id":   pos.ID,
			"old_sl":        oldStopLoss,
			"new_sl":        newStopLoss,
			"current_price": currentPrice,
		}).Info("Trailing stop updated")

		se.mu.Lock()
		se.stats.TrailingStopUpdates++
		se.mu.Unlock()
	}
}

// checkBreakeven checks and moves SL to breakeven if conditions are met
func (se *sltpExecutor) checkBreakeven(ctx context.Context, tracker *positionTracker, currentPrice float64) {
	pos := tracker.position

	// Calculate profit percentage
	var profitPct float64
	if pos.Side == "long" {
		profitPct = ((currentPrice - pos.EntryPrice) / pos.EntryPrice) * 100.0
	} else {
		profitPct = ((pos.EntryPrice - currentPrice) / pos.EntryPrice) * 100.0
	}

	// Check if profit threshold is met
	if profitPct >= se.config.BreakevenTriggerPct {
		// Calculate breakeven with buffer
		var newStopLoss float64
		if pos.Side == "long" {
			newStopLoss = pos.EntryPrice * (1 + se.config.BreakevenBufferPct/100.0)
		} else {
			newStopLoss = pos.EntryPrice * (1 - se.config.BreakevenBufferPct/100.0)
		}

		// Only update if new SL is better than current
		shouldUpdate := false
		if pos.Side == "long" {
			shouldUpdate = newStopLoss > pos.StopLoss || pos.StopLoss == 0
		} else {
			shouldUpdate = newStopLoss < pos.StopLoss || pos.StopLoss == 0
		}

		if shouldUpdate {
			oldStopLoss := pos.StopLoss
			pos.StopLoss = newStopLoss
			tracker.breakevenTriggered = true

			// Update position
			if err := se.positionManager.UpdatePosition(ctx, pos); err != nil {
				se.logger.WithError(err).Error("Failed to move to breakeven")
				return
			}

			se.logger.WithFields(logrus.Fields{
				"position_id": pos.ID,
				"old_sl":      oldStopLoss,
				"new_sl":      newStopLoss,
				"profit_pct":  profitPct,
			}).Info("Stop loss moved to breakeven")

			se.mu.Lock()
			se.stats.BreakevenMoves++
			se.mu.Unlock()
		}
	}
}

// UpdateSLTP updates SL/TP for a position
func (se *sltpExecutor) UpdateSLTP(ctx context.Context, positionID string, sl, tp float64) error {
	se.trackerMu.RLock()
	tracker, exists := se.monitoredPositions[positionID]
	se.trackerMu.RUnlock()

	if !exists {
		return fmt.Errorf("position not monitored: %s", positionID)
	}

	pos := tracker.position
	pos.StopLoss = sl
	pos.TakeProfit = tp

	if err := se.positionManager.UpdatePosition(ctx, pos); err != nil {
		return fmt.Errorf("failed to update position: %w", err)
	}

	// Update stop loss in database
	if sl > 0 && se.stopLossRepo != nil {
		stopLoss, err := se.stopLossRepo.GetByPositionID(ctx, positionID)
		if err == nil && stopLoss != nil {
			stopLoss.StopPrice = decimal.NewFromFloat(sl)
			stopLoss.UpdatedAt = time.Now()
			if err := se.stopLossRepo.Update(ctx, stopLoss); err != nil {
				se.logger.WithError(err).Warn("Failed to update stop loss in database")
			}
		} else {
			// Create new stop loss if it doesn't exist
			newStopLoss := &domain.StopLoss{
				ID:            generateSLTPID("sl"),
				PositionID:    positionID,
				Symbol:        pos.Symbol,
				Exchange:      pos.Exchange,
				StopPrice:     decimal.NewFromFloat(sl),
				TrailingStop:  pos.TrailingStop,
				TrailingDelta: decimal.NewFromFloat(pos.TrailingDistance),
				AIManaged:     false,
				Status:        "active",
				CreatedAt:     time.Now(),
				UpdatedAt:     time.Now(),
			}
			if err := se.stopLossRepo.Create(ctx, newStopLoss); err != nil {
				se.logger.WithError(err).Warn("Failed to create stop loss in database")
			}
		}
	}

	// Update take profit in database
	if tp > 0 && se.takeProfitRepo != nil {
		takeProfit, err := se.takeProfitRepo.GetByPositionID(ctx, positionID)
		if err == nil && takeProfit != nil {
			if len(takeProfit.Levels) > 0 {
				takeProfit.Levels[0].Price = decimal.NewFromFloat(tp)
			} else {
				takeProfit.Levels = []domain.TPLevel{
					{
						Price:      decimal.NewFromFloat(tp),
						Percentage: decimal.NewFromInt(100),
						Executed:   false,
					},
				}
			}
			takeProfit.UpdatedAt = time.Now()
			if err := se.takeProfitRepo.Update(ctx, takeProfit); err != nil {
				se.logger.WithError(err).Warn("Failed to update take profit in database")
			}
		} else {
			// Create new take profit if it doesn't exist
			newTakeProfit := &domain.TakeProfit{
				ID:         generateSLTPID("tp"),
				PositionID: positionID,
				Symbol:     pos.Symbol,
				Exchange:   pos.Exchange,
				Levels: []domain.TPLevel{
					{
						Price:      decimal.NewFromFloat(tp),
						Percentage: decimal.NewFromInt(100),
						Executed:   false,
					},
				},
				Status:    "active",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}
			if err := se.takeProfitRepo.Create(ctx, newTakeProfit); err != nil {
				se.logger.WithError(err).Warn("Failed to create take profit in database")
			}
		}
	}

	se.logger.WithFields(logrus.Fields{
		"position_id": positionID,
		"stop_loss":   sl,
		"take_profit": tp,
	}).Info("SL/TP updated")

	return nil
}

// EnableTrailingStop enables trailing stop for a position
func (se *sltpExecutor) EnableTrailingStop(ctx context.Context, positionID string, distance float64) error {
	se.trackerMu.RLock()
	tracker, exists := se.monitoredPositions[positionID]
	se.trackerMu.RUnlock()

	if !exists {
		return fmt.Errorf("position not monitored: %s", positionID)
	}

	pos := tracker.position
	pos.TrailingStop = true
	pos.TrailingDistance = distance

	if err := se.positionManager.UpdatePosition(ctx, pos); err != nil {
		return fmt.Errorf("failed to update position: %w", err)
	}

	se.logger.WithFields(logrus.Fields{
		"position_id": positionID,
		"distance":    distance,
	}).Info("Trailing stop enabled")

	return nil
}

// MoveToBreakeven moves SL to breakeven for a position
func (se *sltpExecutor) MoveToBreakeven(ctx context.Context, positionID string) error {
	se.trackerMu.RLock()
	tracker, exists := se.monitoredPositions[positionID]
	se.trackerMu.RUnlock()

	if !exists {
		return fmt.Errorf("position not monitored: %s", positionID)
	}

	pos := tracker.position

	// Set SL to entry price with small buffer
	var newStopLoss float64
	if pos.Side == "long" {
		newStopLoss = pos.EntryPrice * (1 + se.config.BreakevenBufferPct/100.0)
	} else {
		newStopLoss = pos.EntryPrice * (1 - se.config.BreakevenBufferPct/100.0)
	}

	pos.StopLoss = newStopLoss
	pos.BreakevenMoved = true
	tracker.breakevenTriggered = true

	if err := se.positionManager.UpdatePosition(ctx, pos); err != nil {
		return fmt.Errorf("failed to update position: %w", err)
	}

	se.logger.WithFields(logrus.Fields{
		"position_id": positionID,
		"new_sl":      newStopLoss,
		"entry_price": pos.EntryPrice,
	}).Info("Moved to breakeven")

	se.mu.Lock()
	se.stats.BreakevenMoves++
	se.mu.Unlock()

	return nil
}

// GetStats returns execution statistics
func (se *sltpExecutor) GetStats() *ExecutionStats {
	se.mu.RLock()
	defer se.mu.RUnlock()

	// Return a copy
	statsCopy := *se.stats
	return &statsCopy
}

// Helper function to round to precision
func roundToPrice(price float64, precision int) float64 {
	multiplier := math.Pow(10, float64(precision))
	return math.Round(price*multiplier) / multiplier
}

// generateSLTPID generates a unique ID for SL/TP records
func generateSLTPID(prefix string) string {
	return fmt.Sprintf("%s_%s", prefix, uuid.New().String()[:8])
}
