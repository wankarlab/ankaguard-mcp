package services

import (
	"context"
	"fmt"
	"time"

	"github.com/rantipay/trading-mcp/internal/ai"
	"github.com/rantipay/trading-mcp/internal/config"
	dynamic "github.com/rantipay/trading-mcp/internal/config/dynamic"
	"github.com/sirupsen/logrus"
)

// StartWithConfig starts the bot with a dynamic configuration
func (bot *AITradingBot) StartWithConfig(ctx context.Context, cfg *config.DynamicTradingConfig) error {
	bot.mu.Lock()
	defer bot.mu.Unlock()

	if bot.running {
		return fmt.Errorf("bot is already running")
	}

	// Convert dynamic config to bot config
	botConfig := bot.convertDynamicConfig(cfg)

	// Update bot configuration
	bot.config = botConfig

	// Safety checks
	if !botConfig.DryRun && !botConfig.AutoExecute {
		return fmt.Errorf("auto_execute must be true for live trading (or use dry_run mode)")
	}

	if botConfig.TradeSize <= 0 {
		return fmt.Errorf("trade_size must be positive")
	}

	if botConfig.ConfidenceThreshold < 0.0 || botConfig.ConfidenceThreshold > 1.0 {
		return fmt.Errorf("confidence_threshold must be between 0.0 and 1.0")
	}

	// Verify GCT connection
	if !bot.gctClient.IsConnected() {
		return fmt.Errorf("GoCryptoTrader is not connected")
	}

	bot.running = true
	bot.paused = false
	bot.startedAt = time.Now()
	bot.emergencyStop = false

	bot.log.WithFields(logrus.Fields{
		"profile":              cfg.ProfileName,
		"pair":                 cfg.Pair,
		"exchange":             cfg.Exchange,
		"timeframes":           cfg.Timeframes,
		"confidence_threshold": cfg.ConfidenceThreshold,
		"trade_size":           cfg.TradeSize,
		"leverage":             cfg.Leverage,
		"dry_run":              cfg.DryRun,
		"auto_execute":         cfg.AutoExecute,
		"autonomous_mode":      cfg.AutonomousMode,
	}).Info("🤖 AI Trading Bot starting with dynamic configuration...")

	// Start analysis loop with dynamic interval
	go bot.analysisLoopDynamic(ctx, cfg)

	// Start position monitoring loop with dynamic interval
	go bot.monitoringLoopDynamic(ctx, cfg)

	// Start result processor
	go bot.resultProcessor(ctx)

	// Start daily reset timer
	go bot.dailyResetTimer(ctx)

	// Start auto-configuration loop if in autonomous mode
	if cfg.AutonomousMode {
		go bot.autoConfigurationLoop(ctx)
		bot.log.Info("🤖 Auto-configuration loop started (runs every 6 hours)")
	}

	bot.log.Info("✅ AI Trading Bot started successfully with dynamic configuration")

	return nil
}

// OnConfigUpdate implements ConfigSubscriber interface for hot-reload
func (bot *AITradingBot) OnConfigUpdate(cfg *config.DynamicTradingConfig) error {
	bot.mu.Lock()
	defer bot.mu.Unlock()

	if !bot.running {
		return fmt.Errorf("bot is not running")
	}

	// Convert and update configuration
	newConfig := bot.convertDynamicConfig(cfg)

	// Log configuration changes
	bot.log.WithFields(logrus.Fields{
		"old_timeframes": bot.config.Timeframes,
		"new_timeframes": newConfig.Timeframes,
		"old_tp_pct":     bot.config.DefaultTakeProfitPct,
		"new_tp_pct":     cfg.TakeProfitPct,
		"old_sl_pct":     bot.config.DefaultStopLossPct,
		"new_sl_pct":     cfg.StopLossPct,
		"old_trade_size": bot.config.TradeSize,
		"new_trade_size": newConfig.TradeSize,
		"old_max_trades": bot.config.MaxDailyTrades,
		"new_max_trades": newConfig.MaxDailyTrades,
	}).Info("🔄 Hot-reloading configuration...")

	// Update configuration
	bot.config = newConfig

	bot.log.Info("✅ Configuration hot-reloaded successfully")

	return nil
}

// convertDynamicConfig converts DynamicTradingConfig to TradingBotConfig
func (bot *AITradingBot) convertDynamicConfig(cfg *config.DynamicTradingConfig) *TradingBotConfig {
	return &TradingBotConfig{
		// Trading parameters
		Pair:                cfg.Pair,
		Exchange:            cfg.Exchange,
		MarketType:          cfg.MarketType,
		ModeSelection:       cfg.ModeSelection,
		Timeframes:          cfg.Timeframes,
		ConfidenceThreshold: cfg.ConfidenceThreshold,
		TradeSize:           cfg.TradeSize,
		Leverage:            cfg.Leverage,

		// Timing
		AnalysisInterval: time.Duration(cfg.AnalysisIntervalSeconds) * time.Second,
		MonitorInterval:  time.Duration(cfg.MonitorIntervalSeconds) * time.Second,

		// Safety limits
		MaxDailyLoss:         cfg.MaxDailyLoss,
		MaxDailyTrades:       cfg.MaxDailyTrades,
		MaxConsecutiveErrors: cfg.ConsecutiveLossLimit,
		MaxOpenPositions:     cfg.MaxOpenPositions,

		// Circuit breaker
		EnableCircuitBreaker: true,
		CircuitBreakerWindow: 5 * time.Minute,
		CircuitBreakerLimit:  5,

		// Auto-execution
		AutoExecute: cfg.AutoExecute,
		DryRun:      cfg.DryRun,

		// Autonomous mode
		AutonomousMode:       cfg.AutonomousMode,
		AutoSetSLTP:          true,
		DefaultStopLossPct:   cfg.StopLossPct,
		DefaultTakeProfitPct: cfg.TakeProfitPct,

		// Advanced TP/SL features
		TPLevels:        cfg.TPLevels,
		BreakevenConfig: cfg.BreakevenConfig,
		// Preserve the YAML-loaded trailing config when the DynamicTradingConfig
		// default has Enabled=false. The dynamic config DB uses GetDefault() which
		// sets Enabled=false, so every hot-reload would silently disable trailing
		// (and since enableTrailingStop is only called at trade-open, existing
		// positions lose their trailing protection permanently until restart).
		TrailingConfig: func() *config.TrailingConfig {
			if cfg.TrailingConfig != nil && cfg.TrailingConfig.Enabled {
				return cfg.TrailingConfig
			}
			if bot.config.TrailingConfig != nil && bot.config.TrailingConfig.Enabled {
				return bot.config.TrailingConfig
			}
			return cfg.TrailingConfig
		}(),

		// Post-Close Behavior
		PostCloseReanalysis:    cfg.PostCloseReanalysis,
		ReanalysisDelaySeconds: cfg.ReanalysisDelaySeconds,
		MinConfluenceFactors:   cfg.MinConfluenceFactors,
		QuickReentryEnabled:    cfg.QuickReentryEnabled,
		MaxReentrySeconds:      cfg.MaxReentrySeconds,

		// Direction Switching
		DirectionSwitchingEnabled: cfg.DirectionSwitchingEnabled,
		MinSwitchIntervalMinutes:  cfg.MinSwitchIntervalMinutes,
		AutoCloseOpposite:         cfg.AutoCloseOpposite,
		SwitchCooldownMinutes:     cfg.SwitchCooldownMinutes,

		// Market condition pausing
		PauseOnAdverseConditions: cfg.PauseOnNews,
		AdverseConditions:        []string{"high_volatility", "btc_dump", "low_liquidity"},
	}
}

// analysisLoopDynamic runs market analysis with dynamic interval
func (bot *AITradingBot) analysisLoopDynamic(ctx context.Context, cfg *config.DynamicTradingConfig) {
	interval := time.Duration(cfg.AnalysisIntervalSeconds) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	bot.log.WithField("interval", interval).Info("📊 Dynamic analysis loop started")

	for {
		select {
		case <-ctx.Done():
			bot.log.Info("Analysis loop stopped (context cancelled)")
			return

		case <-bot.stopChan:
			bot.log.Info("Analysis loop stopped (stop signal)")
			return

		case paused := <-bot.pauseChan:
			if paused {
				bot.log.Info("Analysis loop paused")
				// Wait for resume
				<-bot.pauseChan
				bot.log.Info("Analysis loop resumed")
			}

		case <-ticker.C:
			bot.mu.RLock()
			if bot.emergencyStop {
				bot.mu.RUnlock()
				continue
			}
			paused := bot.paused
			pair := bot.config.Pair
			exchange := bot.config.Exchange
			configStore := bot.configStore
			bot.mu.RUnlock()

			if paused {
				continue
			}

			// Run analysis with timeout
			analysisCtx, cancel := context.WithTimeout(ctx, 30*time.Second)

			// MU-6: Inject per-user scoped snapshot if ConfigStore is available.
			// The scope chain is: bot_instance(userID) → pair → exchange → global.
			// Dynamic overrides (breakeven.trigger_pct, dynamic_sizing.tiers, etc.)
			// resolve per user above the persisted YAML config.
			if configStore != nil {
				userBotID := bot.effectiveUserID()
				chain := dynamic.BotScopeChain(userBotID, pair, exchange)
				snapshot := configStore.SnapshotForScope(chain)
				analysisCtx = dynamic.WithSnapshot(analysisCtx, snapshot)
			}

			bot.runAnalysis(analysisCtx)
			cancel()
		}
	}
}

// monitoringLoopDynamic monitors positions with dynamic interval
func (bot *AITradingBot) monitoringLoopDynamic(ctx context.Context, cfg *config.DynamicTradingConfig) {
	interval := time.Duration(cfg.MonitorIntervalSeconds) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	bot.log.WithField("interval", interval).Info("👁️  Dynamic position monitoring loop started")

	for {
		select {
		case <-ctx.Done():
			return
		case <-bot.stopChan:
			return
		case <-ticker.C:
			monitorCtx := ctx

			// MU-6: Inject per-user scoped snapshot if ConfigStore is available.
			// The scope chain is: bot_instance(userID) → pair → exchange → global.
			// Dynamic overrides (e.g., bot.position.sync_interval) resolve per user.
			bot.mu.RLock()
			pair := bot.config.Pair
			exchange := bot.config.Exchange
			configStore := bot.configStore
			bot.mu.RUnlock()

			if configStore != nil {
				userBotID := bot.effectiveUserID()
				chain := dynamic.BotScopeChain(userBotID, pair, exchange)
				snapshot := configStore.SnapshotForScope(chain)
				monitorCtx = dynamic.WithSnapshot(monitorCtx, snapshot)
			}

			bot.monitorPositions(monitorCtx)
		}
	}
}

// GetDynamicConfig returns the current configuration as DynamicTradingConfig
func (bot *AITradingBot) GetDynamicConfig() *config.DynamicTradingConfig {
	bot.mu.RLock()
	defer bot.mu.RUnlock()

	return &config.DynamicTradingConfig{
		Pair:                    bot.config.Pair,
		Exchange:                bot.config.Exchange,
		Timeframes:              bot.config.Timeframes,
		ConfidenceThreshold:     bot.config.ConfidenceThreshold,
		TradeSize:               bot.config.TradeSize,
		Leverage:                bot.config.Leverage,
		AnalysisIntervalSeconds: int(bot.config.AnalysisInterval.Seconds()),
		MonitorIntervalSeconds:  int(bot.config.MonitorInterval.Seconds()),
		MaxDailyLoss:            bot.config.MaxDailyLoss,
		MaxDailyTrades:          bot.config.MaxDailyTrades,
		ConsecutiveLossLimit:    bot.config.MaxConsecutiveErrors,
		MaxOpenPositions:        bot.config.MaxOpenPositions,
		TakeProfitPct:           bot.config.DefaultTakeProfitPct,
		StopLossPct:             bot.config.DefaultStopLossPct,
		AutoExecute:             bot.config.AutoExecute,
		DryRun:                  bot.config.DryRun,
		AutonomousMode:          bot.config.AutonomousMode,
		CreatedAt:               bot.startedAt,
		UpdatedAt:               time.Now(),
	}
}

// createDynamicStrategy creates a Strategy with dynamic timeframes
func (bot *AITradingBot) createDynamicStrategy(timeframes []string) *ai.Strategy {
	// Create timeframe configs with equal weights
	tfConfigs := make([]ai.TimeframeConfig, len(timeframes))
	weight := 1.0 / float64(len(timeframes))

	for i, tf := range timeframes {
		tfConfigs[i] = ai.TimeframeConfig{
			Interval: tf,
			Weight:   weight,
		}
	}

	// Create strategy with dynamic timeframes
	strategy := &ai.Strategy{
		Name:               "dynamic-scalping",
		Type:               ai.StyleScalping,
		Timeframes:         tfConfigs,
		RequiredTimeframes: timeframes,
		CandleLimits: map[string]int{
			"1m":  100,
			"2m":  100,
			"3m":  100,
			"5m":  100,
			"15m": 100,
			"30m": 50,
			"1h":  50,
		},
		Confidence: bot.config.ConfidenceThreshold,
	}

	return strategy
}

// setMultiLevelTPSL sets multi-level take profit and stop loss for a position
func (bot *AITradingBot) setMultiLevelTPSL(ctx context.Context, position *ai.ManagedPosition, decision *ai.Decision) error {
	bot.mu.RLock()
	tpLevels := bot.config.TPLevels
	slPct := bot.config.DefaultStopLossPct
	bot.mu.RUnlock()

	// If no multi-level TP configured, use simple TP/SL
	if len(tpLevels) == 0 {
		return bot.setAutonomousSLTP(ctx, position, decision)
	}

	bot.log.WithFields(logrus.Fields{
		"position_id": position.ID,
		"tp_levels":   len(tpLevels),
	}).Info("🎯 Setting multi-level take profit...")

	// Calculate stop loss. Prefer the position's authoritative SL (recomputed
	// from the actual fill in executeTrade); the decision holds the PRE-recompute
	// value which can be a ~0.3% clamp-to-support stop that noise-wicks in minutes.
	var stopLoss float64
	if position.StopLoss > 0 {
		stopLoss = position.StopLoss
	} else if decision != nil && decision.StopLoss > 0 {
		stopLoss = decision.StopLoss
	} else {
		if position.Side == "buy" || position.Side == "long" {
			stopLoss = position.EntryPrice * (1 - slPct/100)
		} else {
			stopLoss = position.EntryPrice * (1 + slPct/100)
		}
	}

	// Set initial stop loss
	if bot.sltpExecutor != nil {
		if err := bot.sltpExecutor.UpdateSLTP(ctx, position.ID, stopLoss, 0); err != nil {
			bot.log.WithError(err).WithField("position_id", position.ID).Warn("Failed to update SL/TP via executor")
		}
	}

	// Log multi-level TP configuration
	for i, level := range tpLevels {
		var tpPrice float64
		if position.Side == "buy" || position.Side == "long" {
			tpPrice = position.EntryPrice * (1 + level.PricePct/100)
		} else {
			tpPrice = position.EntryPrice * (1 - level.PricePct/100)
		}

		bot.log.WithFields(logrus.Fields{
			"level":     i + 1,
			"price_pct": level.PricePct,
			"size_pct":  level.SizePct,
			"tp_price":  tpPrice,
			"entry":     position.EntryPrice,
		}).Info("📊 TP Level configured")
	}

	bot.log.WithFields(logrus.Fields{
		"position_id": position.ID,
		"stop_loss":   stopLoss,
		"sl_pct":      fmt.Sprintf("%.2f%%", slPct),
		"levels":      len(tpLevels),
	}).Info("✅ Multi-level TP/SL configured successfully")

	// Note: Actual multi-level TP execution would require integration with
	// position monitoring to close partial positions at each level
	// This is a foundation for that functionality

	return nil
}

// checkAndApplyBreakeven checks if position should move to breakeven
func (bot *AITradingBot) checkAndApplyBreakeven(ctx context.Context, position *ai.ManagedPosition) error {
	bot.mu.RLock()
	breakevenConfig := bot.config.BreakevenConfig
	bot.mu.RUnlock()

	// Resolve breakeven activation + values from the LIVE dynamic config. The
	// struct flag (bot.config.BreakevenConfig.Enabled) is rewritten to false on
	// every hot-reload (GetDefault) and was never preserved, so breakeven silently
	// stopped moving the stop to entry on winners (SOL rode +1.4% with its stop at
	// the original loss level). A positive live breakeven.trigger_pct means active.
	var triggerPct, bufferPct float64
	enabled := breakevenConfig != nil && breakevenConfig.Enabled
	if breakevenConfig != nil {
		triggerPct = breakevenConfig.TriggerPct
		bufferPct = breakevenConfig.BufferPct
	}
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if v := snap.Float("breakeven.trigger_pct"); v > 0 {
			triggerPct = v
			enabled = true
		}
		bufferPct = snap.Float("breakeven.buffer_pct")
	}
	if !enabled || triggerPct <= 0 {
		return nil
	}

	// Skip if position already at breakeven or better
	if position.BreakevenApplied {
		return nil
	}

	// Get current price (would need to be passed or fetched)
	currentPrice := position.CurrentPrice
	if currentPrice == 0 {
		return nil // No current price available
	}

	// Calculate profit percentage
	var profitPct float64
	if position.Side == "buy" || position.Side == "long" {
		profitPct = ((currentPrice - position.EntryPrice) / position.EntryPrice) * 100
	} else {
		profitPct = ((position.EntryPrice - currentPrice) / position.EntryPrice) * 100
	}

	// Check if trigger reached
	if profitPct >= triggerPct {
		// Calculate new stop loss at breakeven + buffer.
		// LONG: SL below entry (entry*(1-buf)) — triggers if price drops back.
		// SHORT: SL above entry (entry*(1+buf)) — triggers if price rises back.
		var newSL float64
		if position.Side == "buy" || position.Side == "long" {
			newSL = position.EntryPrice * (1 - bufferPct/100)
		} else {
			newSL = position.EntryPrice * (1 + bufferPct/100)
		}

		// Update stop loss to breakeven
		if bot.sltpExecutor != nil {
			if err := bot.sltpExecutor.UpdateSLTP(ctx, position.ID, newSL, position.TakeProfit); err != nil {
				bot.log.WithError(err).WithField("position_id", position.ID).Warn("Failed to update SL/TP via executor")
			}
		}

		// Mark as applied
		position.BreakevenApplied = true
		position.StopLoss = newSL

		// Cancel the existing exchange stop-order and place a new one at the
		// breakeven price. This is best-effort — failures are only logged.
		if position.ExchangeStopOrderID != "" {
			go bot.cancelExchangeStopLoss(context.Background(), position)
		}
		go bot.placeExchangeStopLoss(context.Background(), position)

		bot.log.WithFields(logrus.Fields{
			"position_id": position.ID,
			"entry_price": position.EntryPrice,
			"new_sl":      newSL,
			"profit_pct":  fmt.Sprintf("%.2f%%", profitPct),
			"trigger_pct": triggerPct,
			"buffer_pct":  bufferPct,
		}).Info("🛡️ Stop loss moved to breakeven")
	}

	return nil
}

// enableTrailingStop enables trailing stop for a position
func (bot *AITradingBot) enableTrailingStop(ctx context.Context, position *ai.ManagedPosition) error {
	bot.mu.RLock()
	trailingConfig := bot.config.TrailingConfig
	bot.mu.RUnlock()

	// Resolve whether trailing is active from the LIVE dynamic config, not the
	// struct flag. bot.config.TrailingConfig.Enabled is rewritten on every
	// hot-reload and was observed toggling to false while the dynamic config kept
	// trailing on — silently killing profit protection on open winners (a SOL short
	// rode +1.4% with its exchange stop parked at the original loss level). A
	// positive live trailing.distance_pct is the source of truth: it means trail.
	distancePct := 0.0
	enabled := false
	if trailingConfig != nil {
		distancePct = trailingConfig.DistancePct
		enabled = trailingConfig.Enabled
	}
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if v := snap.Float("trailing.distance_pct"); v > 0 {
			distancePct = v
			enabled = true
		}
	}
	if !enabled || distancePct <= 0 {
		return nil
	}

	// Skip if already enabled
	if position.TrailingStopEnabled {
		return nil
	}

	bot.log.WithFields(logrus.Fields{
		"position_id": position.ID,
		"distance":    fmt.Sprintf("%.2f%%", distancePct),
	}).Info("📈 Enabling trailing stop...")

	// Mark as enabled
	position.TrailingStopEnabled = true
	position.TrailingStopDistance = distancePct

	bot.log.WithFields(logrus.Fields{
		"position_id": position.ID,
		"distance":    fmt.Sprintf("%.2f%%", distancePct),
	}).Info("✅ Trailing stop enabled")

	// Note: Actual trailing stop logic would be implemented in position monitoring
	// This marks the position for trailing stop behavior

	return nil
}

// updateTrailingStop updates the trailing stop based on current price.
//
// CRITICAL: this re-places the EXCHANGE stop order, not just the in-memory SL.
// The exchange stop is the only protection that survives a bot crash/restart, so
// ratcheting only the in-memory value (the old behavior) left the durable stop
// parked at its original level — the in-memory SL would say +14% while the
// exchange would still fire at breakeven. Now the exchange stop follows price up
// tick by tick, which is what actually locks the profit (INJ rode +16% and gave
// it all back on 2026-06-01 precisely because the exchange stop never moved).
//
// The trailing distance is read live from dynamic config each tick so it can be
// retuned without restart, and (when trailing.pnl_mode is on) divided by leverage
// so the distance is expressed in account-PnL % rather than raw price %.
// updateTrailingStop ratchets the position's stop toward the current price.
// widenFactor (>= 1) loosens the trailing distance — passed by the continuation
// gate and the volatility-tier multiplier so a fast/volatile move is not stopped
// out by noise. distanceOverridePct (> 0) replaces the configured base distance
// with a regime-profile distance. Pass widenFactor=1, distanceOverridePct=0 for
// the default behaviour.
func (bot *AITradingBot) updateTrailingStop(ctx context.Context, position *ai.ManagedPosition, currentPrice float64, widenFactor, distanceOverridePct float64) error {
	if !position.TrailingStopEnabled {
		return nil
	}
	if widenFactor < 1 {
		widenFactor = 1
	}

	// Live trailing distance (price-move %); fall back to the value stamped at
	// enable time when no snapshot is available.
	distancePct := position.TrailingStopDistance
	pnlMode := false
	armPct := -1.0 // <0 → default to the final trailing distance (first trailed stop lands at breakeven)
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if v := snap.Float("trailing.distance_pct"); v > 0 {
			distancePct = v
		}
		if v, ok := snap.BoolOk("trailing.pnl_mode"); ok {
			pnlMode = v
		}
		if v, ok := snap.FloatOk("trailing.arm_pct"); ok && v >= 0 {
			armPct = v
		}
	}
	// Regime-profile base distance override (Active Profit Engine F4): a
	// per-regime trailing distance replaces the configured base before pnl_mode
	// and widen are applied.
	if distanceOverridePct > 0 {
		distancePct = distanceOverridePct
	}
	// pnl_mode: distance is expressed as account PnL %; convert to price-move % by
	// dividing by leverage so a 5× position trails the same DOLLAR distance as a
	// 1× one. Default OFF preserves the legacy price-move semantics.
	if pnlMode && position.Leverage > 1 {
		distancePct = distancePct / float64(position.Leverage)
	}
	// Continuation gate + volatility tier: loosen the trail in a confirmed healthy
	// trend and/or higher volatility.
	distancePct *= widenFactor
	if distancePct <= 0 {
		return nil
	}

	isLong := position.Side == "buy" || position.Side == "long"

	// Arm gate: do NOT trail until the position is genuinely in profit. Without
	// this, updateTrailingStop ratcheted the stop from the very FIRST tick —
	// instantly shrinking the entry SL floor (e.g. 1.2%) down to the trailing
	// distance (0.8%) and then tighter as price wiggled, so noise wicked
	// positions out in minutes. This was the dominant loss mode through
	// 2026-06-24: SL deaths recorded at 0.3-0.6% with sl_pct ≈ |pnl_pct|,
	// DESPITE the entry/adopt floor — because trailing overwrote the floor on
	// tick 1. Only once profit >= armPct does the trailed stop (current ∓
	// distance) sit at/above entry, locking a gain instead of cutting inside
	// the noise floor. armPct defaults to the trailing distance (so the first
	// trailed stop lands exactly at breakeven); tune via trailing.arm_pct
	// (0 = trail immediately, restores legacy behaviour).
	if armPct < 0 {
		armPct = distancePct
	}
	if armPct > 0 {
		var profitPct float64
		if position.EntryPrice > 0 {
			if isLong {
				profitPct = (currentPrice - position.EntryPrice) / position.EntryPrice * 100
			} else {
				profitPct = (position.EntryPrice - currentPrice) / position.EntryPrice * 100
			}
		}
		if profitPct < armPct {
			return nil
		}
	}

	var newSL float64
	moved := false
	if isLong {
		newSL = currentPrice * (1 - distancePct/100)
		if newSL > position.StopLoss { // only move stop up
			moved = true
		}
	} else {
		newSL = currentPrice * (1 + distancePct/100)
		if position.StopLoss <= 0 || newSL < position.StopLoss { // only move stop down
			moved = true
		}
	}
	if !moved {
		return nil
	}

	oldSL := position.StopLoss
	if bot.sltpExecutor != nil {
		if err := bot.sltpExecutor.UpdateSLTP(ctx, position.ID, newSL, position.TakeProfit); err != nil {
			bot.log.WithError(err).WithField("position_id", position.ID).Debug("Trailing: executor UpdateSLTP failed (non-fatal)")
		}
	}
	position.StopLoss = newSL

	// Ratchet the durable exchange stop order: cancel the old one and place a new
	// one at the trailed level. Best-effort and async so a slow exchange call does
	// not stall the monitor loop. Without this the exchange stop never moves.
	if !bot.config.DryRun {
		go func(p *ai.ManagedPosition) {
			bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if p.ExchangeStopOrderID != "" {
				bot.cancelExchangeStopLoss(bgCtx, p)
			}
			bot.placeExchangeStopLoss(bgCtx, p)
		}(position)
	}

	bot.log.WithFields(logrus.Fields{
		"position_id":   position.ID,
		"symbol":        position.Symbol,
		"current_price": currentPrice,
		"old_sl":        oldSL,
		"new_sl":        newSL,
		"distance_pct":  fmt.Sprintf("%.2f%%", distancePct),
	}).Info("📈 Trailing stop ratcheted — exchange stop moved to lock profit")

	return nil
}
