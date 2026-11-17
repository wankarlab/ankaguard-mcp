package services

import (
	"context"
	"fmt"
	"time"

	"github.com/rantipay/trading-mcp/internal/ai"
	"github.com/sirupsen/logrus"
)

// PostCloseState tracks state after closing a position
type PostCloseState struct {
	LastCloseTime       time.Time
	LastCloseReason     string
	LastCloseSide       string
	LastClosePnL        float64
	LastSwitchTime      time.Time
	ConsecutiveWins     int
	ConsecutiveLosses   int
	ReanalysisScheduled bool
}

// onPositionClosed is called when a position is closed
func (bot *AITradingBot) onPositionClosed(ctx context.Context, position *ai.ManagedPosition, reason string, pnl float64) {
	bot.mu.Lock()
	defer bot.mu.Unlock()

	// Update post-close state
	if bot.postCloseState == nil {
		bot.postCloseState = &PostCloseState{}
	}

	bot.postCloseState.LastCloseTime = time.Now()
	bot.postCloseState.LastCloseReason = reason
	bot.postCloseState.LastCloseSide = position.Side
	bot.postCloseState.LastClosePnL = pnl

	// Update win/loss streaks
	if pnl > 0 {
		bot.postCloseState.ConsecutiveWins++
		bot.postCloseState.ConsecutiveLosses = 0
	} else if pnl < 0 {
		bot.postCloseState.ConsecutiveLosses++
		bot.postCloseState.ConsecutiveWins = 0
	}

	bot.log.WithFields(logrus.Fields{
		"position_id":        position.ID,
		"reason":             reason,
		"pnl":                fmt.Sprintf("$%.2f", pnl),
		"consecutive_wins":   bot.postCloseState.ConsecutiveWins,
		"consecutive_losses": bot.postCloseState.ConsecutiveLosses,
	}).Info("📊 Position closed - post-close analysis triggered")

	// Check if post-close reanalysis is enabled
	if !bot.config.PostCloseReanalysis {
		bot.log.Debug("Post-close reanalysis disabled")
		return
	}

	// Schedule reanalysis after delay
	bot.postCloseState.ReanalysisScheduled = true

	go bot.schedulePostCloseReanalysis(ctx, position)
}

// schedulePostCloseReanalysis schedules a reanalysis after position close
func (bot *AITradingBot) schedulePostCloseReanalysis(ctx context.Context, closedPosition *ai.ManagedPosition) {
	bot.mu.RLock()
	delaySeconds := bot.config.ReanalysisDelaySeconds
	bot.mu.RUnlock()

	// Wait for configured delay
	if delaySeconds > 0 {
		bot.log.WithField("delay_seconds", delaySeconds).Debug("⏳ Waiting before post-close reanalysis")
		time.Sleep(time.Duration(delaySeconds) * time.Second)
	}

	// Check if bot is still running
	bot.mu.RLock()
	if !bot.running || bot.paused || bot.emergencyStop {
		bot.mu.RUnlock()
		bot.log.Debug("Bot not active - skipping post-close reanalysis")
		return
	}
	bot.mu.RUnlock()

	bot.log.Info("🔄 Starting post-close reanalysis...")

	// Perform reanalysis
	if err := bot.performPostCloseReanalysis(ctx, closedPosition); err != nil {
		bot.log.WithError(err).Warn("Post-close reanalysis failed")
	}

	bot.mu.Lock()
	if bot.postCloseState != nil {
		bot.postCloseState.ReanalysisScheduled = false
	}
	bot.mu.Unlock()
}

// performPostCloseReanalysis performs market analysis after closing a position
func (bot *AITradingBot) performPostCloseReanalysis(ctx context.Context, closedPosition *ai.ManagedPosition) error {
	// Check if we should reanalyze
	if !bot.shouldReAnalyze() {
		bot.log.Debug("Conditions not met for reanalysis")
		return nil
	}

	// Get current market context
	bot.mu.RLock()
	timeframes := bot.config.Timeframes
	pair := bot.config.Pair
	bot.mu.RUnlock()

	strategy := bot.createDynamicStrategy(timeframes)
	marketCtx, err := bot.contextBuilder.BuildContext(ctx, pair, strategy)
	if err != nil {
		return fmt.Errorf("failed to build market context: %w", err)
	}

	// Evaluate opportunity
	decision, err := bot.decisionEngine.EvaluateOpportunity(ctx, marketCtx)
	if err != nil {
		return fmt.Errorf("failed to evaluate opportunity: %w", err)
	}

	bot.log.WithFields(logrus.Fields{
		"action":     decision.Action,
		"confidence": fmt.Sprintf("%.2f%%", decision.Confidence*100),
		"reason":     decision.Reason,
	}).Info("📊 Post-close analysis complete")

	// Check if we should take action
	if decision.Action == "WAIT" {
		bot.log.Debug("No trading opportunity found in post-close analysis")
		return nil
	}

	// Check confidence threshold
	bot.mu.RLock()
	threshold := bot.config.ConfidenceThreshold
	bot.mu.RUnlock()

	if decision.Confidence < threshold {
		bot.log.WithFields(logrus.Fields{
			"confidence": fmt.Sprintf("%.2f%%", decision.Confidence*100),
			"threshold":  fmt.Sprintf("%.2f%%", threshold*100),
		}).Debug("Confidence below threshold - no action")
		return nil
	}

	// Determine if we should switch direction
	newDirection := decision.Action
	oldDirection := closedPosition.Side

	if newDirection != oldDirection {
		// Direction switch requested
		if !bot.canSwitchDirection(newDirection) {
			bot.log.WithFields(logrus.Fields{
				"old_direction": oldDirection,
				"new_direction": newDirection,
			}).Warn("⚠️ Direction switch blocked by cooldown")
			return nil
		}

		bot.log.WithFields(logrus.Fields{
			"old_direction": oldDirection,
			"new_direction": newDirection,
		}).Info("🔄 Direction switch approved")
	}

	// Check if quick reentry is enabled and applicable
	bot.mu.RLock()
	quickReentryEnabled := bot.config.QuickReentryEnabled
	maxReentrySeconds := bot.config.MaxReentrySeconds
	bot.mu.RUnlock()

	timeSinceClose := time.Since(bot.postCloseState.LastCloseTime).Seconds()

	if quickReentryEnabled && timeSinceClose <= float64(maxReentrySeconds) {
		bot.log.WithFields(logrus.Fields{
			"time_since_close": fmt.Sprintf("%.1fs", timeSinceClose),
			"max_reentry":      maxReentrySeconds,
		}).Info("⚡ Quick reentry conditions met")

		// Check confluence factors
		if bot.hasMinimumConfluence(marketCtx, decision) {
			bot.log.Info("✅ Minimum confluence factors present - executing quick reentry")

			// Execute trade if auto-execute is enabled
			if bot.config.AutoExecute && bot.config.AutonomousMode {
				go bot.executeTrade(ctx, decision, marketCtx)
			} else {
				bot.log.Info("📋 Auto-execute disabled - signal logged only")
			}
		} else {
			bot.log.Warn("⚠️ Insufficient confluence factors for quick reentry")
		}
	} else {
		bot.log.WithFields(logrus.Fields{
			"time_since_close": fmt.Sprintf("%.1fs", timeSinceClose),
			"quick_reentry":    quickReentryEnabled,
		}).Debug("Quick reentry not applicable - will wait for next analysis cycle")
	}

	return nil
}

// shouldReAnalyze checks if conditions are met for post-close reanalysis
func (bot *AITradingBot) shouldReAnalyze() bool {
	bot.mu.RLock()
	defer bot.mu.RUnlock()

	// Check if post-close reanalysis is enabled
	if !bot.config.PostCloseReanalysis {
		return false
	}

	// Check if bot is in valid state
	if !bot.running || bot.paused || bot.emergencyStop {
		return false
	}

	// Check safety limits
	if !bot.checkSafetyLimits() {
		bot.log.Debug("Safety limits exceeded - skipping reanalysis")
		return false
	}

	// Check if we have open positions (don't reanalyze if already in position)
	openPositions := bot.positionManager.GetAllPositions()
	if len(openPositions) > 0 {
		bot.log.Debug("Open positions exist - skipping reanalysis")
		return false
	}

	return true
}

// canSwitchDirection checks if direction switching is allowed
func (bot *AITradingBot) canSwitchDirection(newDirection string) bool {
	bot.mu.RLock()
	defer bot.mu.RUnlock()

	// Check if direction switching is enabled
	if !bot.config.DirectionSwitchingEnabled {
		bot.log.Debug("Direction switching disabled")
		return false
	}

	// Check if we have post-close state
	if bot.postCloseState == nil {
		return true // First trade, allow any direction
	}

	// Check cooldown
	cooldownMinutes := bot.config.SwitchCooldownMinutes
	if cooldownMinutes > 0 {
		timeSinceLastSwitch := time.Since(bot.postCloseState.LastSwitchTime)
		cooldownDuration := time.Duration(cooldownMinutes) * time.Minute

		if timeSinceLastSwitch < cooldownDuration {
			remainingCooldown := cooldownDuration - timeSinceLastSwitch
			bot.log.WithFields(logrus.Fields{
				"remaining_cooldown": remainingCooldown.String(),
				"cooldown_minutes":   cooldownMinutes,
			}).Debug("Direction switch in cooldown")
			return false
		}
	}

	// Check minimum interval between switches
	minIntervalMinutes := bot.config.MinSwitchIntervalMinutes
	if minIntervalMinutes > 0 {
		timeSinceLastClose := time.Since(bot.postCloseState.LastCloseTime)
		minInterval := time.Duration(minIntervalMinutes) * time.Minute

		if timeSinceLastClose < minInterval {
			bot.log.WithFields(logrus.Fields{
				"time_since_close": timeSinceLastClose.String(),
				"min_interval":     minInterval.String(),
			}).Debug("Minimum switch interval not met")
			return false
		}
	}

	// Update last switch time
	bot.postCloseState.LastSwitchTime = time.Now()

	return true
}

// hasMinimumConfluence checks if minimum confluence factors are present
func (bot *AITradingBot) hasMinimumConfluence(marketCtx *ai.MarketContext, decision *ai.Decision) bool {
	bot.mu.RLock()
	minFactors := bot.config.MinConfluenceFactors
	bot.mu.RUnlock()

	confluenceCount, _ := bot.countConfluenceFactors(marketCtx, decision)

	bot.log.WithFields(logrus.Fields{
		"confluence_count": confluenceCount,
		"min_required":     minFactors,
	}).Debug("Confluence factors checked")

	return confluenceCount >= minFactors
}

// countConfluenceFactors counts the independent factors supporting a decision
// (out of 5) and names them. Shared by the quick re-entry check and the AI-02
// pre-trade validation, so both judge trade quality with the same yardstick.
func (bot *AITradingBot) countConfluenceFactors(marketCtx *ai.MarketContext, decision *ai.Decision) (int, []string) {
	var factors []string
	if decision != nil && decision.Confidence >= 0.70 {
		factors = append(factors, "high_confidence")
	}
	action := ""
	if decision != nil {
		action = decision.Action
	}
	if bot.checkTimeframeAlignment(marketCtx, action) {
		factors = append(factors, "timeframe_alignment")
	}
	if bot.checkStrongTrend(marketCtx) {
		factors = append(factors, "strong_trend")
	}
	if bot.checkVolumeConfirmation(marketCtx) {
		factors = append(factors, "volume_confirmation")
	}
	if bot.checkRSINotExtreme(marketCtx) {
		factors = append(factors, "rsi_not_extreme")
	}
	return len(factors), factors
}

// checkTimeframeAlignment checks if multiple timeframes agree on direction
func (bot *AITradingBot) checkTimeframeAlignment(marketCtx *ai.MarketContext, action string) bool {
	if marketCtx == nil || marketCtx.Timeframes == nil {
		return false
	}

	alignedCount := 0
	totalTimeframes := len(marketCtx.Timeframes)

	if totalTimeframes == 0 {
		return false
	}

	for _, tf := range marketCtx.Timeframes {
		if action == "BUY" && tf.Trend == "bullish" {
			alignedCount++
		} else if action == "SELL" && tf.Trend == "bearish" {
			alignedCount++
		}
	}

	// At least 60% of timeframes should be aligned
	return float64(alignedCount)/float64(totalTimeframes) >= 0.6
}

// checkStrongTrend checks if there's a strong trend
func (bot *AITradingBot) checkStrongTrend(marketCtx *ai.MarketContext) bool {
	if marketCtx == nil || marketCtx.Timeframes == nil || len(marketCtx.Timeframes) == 0 {
		return false
	}

	// Check first timeframe
	for _, tf := range marketCtx.Timeframes {
		if tf.Trend != "sideways" && tf.Trend != "" {
			return true
		}
		break // Only check first
	}

	return false
}

// checkVolumeConfirmation checks if volume supports the move
func (bot *AITradingBot) checkVolumeConfirmation(marketCtx *ai.MarketContext) bool {
	if marketCtx == nil || marketCtx.Timeframes == nil || len(marketCtx.Timeframes) == 0 {
		return false
	}

	// Check if volume is above average in any timeframe
	for _, tf := range marketCtx.Timeframes {
		if tf.Indicators.Volume > 0 {
			// If we have volume data, consider it confirmed
			return true
		}
		break // Only check first
	}

	return false
}

// checkRSINotExtreme checks if RSI is not in extreme zones
func (bot *AITradingBot) checkRSINotExtreme(marketCtx *ai.MarketContext) bool {
	if marketCtx == nil || marketCtx.Timeframes == nil || len(marketCtx.Timeframes) == 0 {
		return false
	}

	// Check first timeframe RSI
	for _, tf := range marketCtx.Timeframes {
		rsi := tf.Indicators.RSI
		// RSI should be between 30 and 70 (not extreme)
		return rsi > 30 && rsi < 70
	}

	return false
}

// GetPostCloseState returns the current post-close state
func (bot *AITradingBot) GetPostCloseState() *PostCloseState {
	bot.mu.RLock()
	defer bot.mu.RUnlock()

	if bot.postCloseState == nil {
		return nil
	}

	// Return a copy to avoid race conditions
	return &PostCloseState{
		LastCloseTime:       bot.postCloseState.LastCloseTime,
		LastCloseReason:     bot.postCloseState.LastCloseReason,
		LastCloseSide:       bot.postCloseState.LastCloseSide,
		LastClosePnL:        bot.postCloseState.LastClosePnL,
		LastSwitchTime:      bot.postCloseState.LastSwitchTime,
		ConsecutiveWins:     bot.postCloseState.ConsecutiveWins,
		ConsecutiveLosses:   bot.postCloseState.ConsecutiveLosses,
		ReanalysisScheduled: bot.postCloseState.ReanalysisScheduled,
	}
}
