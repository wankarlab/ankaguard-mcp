package validation

import (
	"fmt"
	"sync"
	"time"
)

// CircuitBreakerManager manages trading circuit breakers
type CircuitBreakerManager struct {
	config   *Config
	halted   bool
	haltedAt *time.Time
	reason   string
	mutex    sync.RWMutex
}

// setConfig swaps the limits; the halt state is kept.
func (cbm *CircuitBreakerManager) setConfig(c *Config) {
	cbm.mutex.Lock()
	cbm.config = c
	cbm.mutex.Unlock()
}

// NewCircuitBreakerManager creates a new circuit breaker manager
func NewCircuitBreakerManager(config *Config) *CircuitBreakerManager {
	return &CircuitBreakerManager{
		config: config,
		halted: false,
	}
}

// Check verifies if trading should continue
func (cbm *CircuitBreakerManager) Check(portfolio *PortfolioState) (*CircuitBreakerStatus, error) {
	cbm.mutex.Lock()
	defer cbm.mutex.Unlock()

	status := &CircuitBreakerStatus{
		TradingHalted:     cbm.halted,
		DailyLossLimit:    false,
		DrawdownLimit:     false,
		ConsecutiveLosses: portfolio.ConsecutiveLosses,
		CurrentDrawdown:   portfolio.CurrentDrawdown,
		DailyLoss:         portfolio.DailyLoss,
		Reason:            cbm.reason,
		HaltedAt:          cbm.haltedAt,
	}

	// If already halted, check if we should resume
	if cbm.halted {
		if cbm.shouldResume(portfolio) {
			cbm.resume()
			status.TradingHalted = false
			status.Reason = ""
			status.HaltedAt = nil
		}
		return status, nil
	}

	// Check all circuit breaker conditions
	if reason := cbm.checkDailyLossLimit(portfolio); reason != "" {
		cbm.halt(reason)
		status.TradingHalted = true
		status.DailyLossLimit = true
		status.Reason = reason
		status.HaltedAt = cbm.haltedAt
		return status, nil
	}

	if reason := cbm.checkDrawdownLimit(portfolio); reason != "" {
		cbm.halt(reason)
		status.TradingHalted = true
		status.DrawdownLimit = true
		status.Reason = reason
		status.HaltedAt = cbm.haltedAt
		return status, nil
	}

	if reason := cbm.checkConsecutiveLosses(portfolio); reason != "" {
		cbm.halt(reason)
		status.TradingHalted = true
		status.Reason = reason
		status.HaltedAt = cbm.haltedAt
		return status, nil
	}

	return status, nil
}

// checkDailyLossLimit checks if daily loss limit is exceeded
func (cbm *CircuitBreakerManager) checkDailyLossLimit(portfolio *PortfolioState) string {
	dailyLossPercent := (portfolio.DailyLoss / portfolio.TotalCapital) * 100

	if dailyLossPercent >= cbm.config.MaxDailyLoss {
		return fmt.Sprintf("Daily loss limit reached: %.2f%% (max %.2f%%)",
			dailyLossPercent, cbm.config.MaxDailyLoss)
	}

	return ""
}

// checkDrawdownLimit checks if maximum drawdown is exceeded
func (cbm *CircuitBreakerManager) checkDrawdownLimit(portfolio *PortfolioState) string {
	drawdownPercent := (portfolio.CurrentDrawdown / portfolio.PeakCapital) * 100

	if drawdownPercent >= cbm.config.MaxDrawdown {
		return fmt.Sprintf("Maximum drawdown reached: %.2f%% (max %.2f%%)",
			drawdownPercent, cbm.config.MaxDrawdown)
	}

	return ""
}

// checkConsecutiveLosses checks if consecutive loss limit is exceeded
func (cbm *CircuitBreakerManager) checkConsecutiveLosses(portfolio *PortfolioState) string {
	if portfolio.ConsecutiveLosses >= cbm.config.ConsecutiveLossLimit {
		return fmt.Sprintf("Consecutive loss limit reached: %d losses (max %d)",
			portfolio.ConsecutiveLosses, cbm.config.ConsecutiveLossLimit)
	}

	return ""
}

// shouldResume checks if trading should resume after halt
func (cbm *CircuitBreakerManager) shouldResume(portfolio *PortfolioState) bool {
	if cbm.haltedAt == nil {
		return false
	}

	// Check if enough time has passed (e.g., 1 hour cooldown)
	cooldownPeriod := 1 * time.Hour
	if time.Since(*cbm.haltedAt) < cooldownPeriod {
		return false
	}

	// Check if conditions have improved
	dailyLossPercent := (portfolio.DailyLoss / portfolio.TotalCapital) * 100
	drawdownPercent := (portfolio.CurrentDrawdown / portfolio.PeakCapital) * 100

	// Resume if losses are below 80% of limits
	if dailyLossPercent < cbm.config.MaxDailyLoss*0.8 &&
		drawdownPercent < cbm.config.MaxDrawdown*0.8 &&
		portfolio.ConsecutiveLosses < cbm.config.ConsecutiveLossLimit {
		return true
	}

	return false
}

// halt stops trading
func (cbm *CircuitBreakerManager) halt(reason string) {
	now := time.Now()
	cbm.halted = true
	cbm.haltedAt = &now
	cbm.reason = reason
}

// resume resumes trading
func (cbm *CircuitBreakerManager) resume() {
	cbm.halted = false
	cbm.haltedAt = nil
	cbm.reason = ""
}

// ManualHalt manually halts trading
func (cbm *CircuitBreakerManager) ManualHalt(reason string) {
	cbm.mutex.Lock()
	defer cbm.mutex.Unlock()
	cbm.halt(reason)
}

// ManualResume manually resumes trading
func (cbm *CircuitBreakerManager) ManualResume() {
	cbm.mutex.Lock()
	defer cbm.mutex.Unlock()
	cbm.resume()
}

// IsHalted returns whether trading is currently halted
func (cbm *CircuitBreakerManager) IsHalted() bool {
	cbm.mutex.RLock()
	defer cbm.mutex.RUnlock()
	return cbm.halted
}

// GetStatus returns current circuit breaker status
func (cbm *CircuitBreakerManager) GetStatus() CircuitBreakerStatus {
	cbm.mutex.RLock()
	defer cbm.mutex.RUnlock()

	return CircuitBreakerStatus{
		TradingHalted: cbm.halted,
		Reason:        cbm.reason,
		HaltedAt:      cbm.haltedAt,
	}
}

// CalculatePositionSizeAdjustment calculates position size reduction based on losing streak
func (cbm *CircuitBreakerManager) CalculatePositionSizeAdjustment(consecutiveLosses int) float64 {
	if consecutiveLosses >= 5 {
		return 0.25 // Reduce to 25% after 5 losses
	} else if consecutiveLosses >= 3 {
		return 0.50 // Reduce to 50% after 3 losses
	}
	return 1.0 // No reduction
}

// ShouldReducePositionSize checks if position size should be reduced
func (cbm *CircuitBreakerManager) ShouldReducePositionSize(portfolio *PortfolioState) (bool, float64) {
	adjustment := cbm.CalculatePositionSizeAdjustment(portfolio.ConsecutiveLosses)
	return adjustment < 1.0, adjustment
}

// GetRecommendedAction returns recommended action based on current state
func (cbm *CircuitBreakerManager) GetRecommendedAction(portfolio *PortfolioState) string {
	cbm.mutex.RLock()
	defer cbm.mutex.RUnlock()

	if cbm.halted {
		return "HALT - Trading is stopped: " + cbm.reason
	}

	dailyLossPercent := (portfolio.DailyLoss / portfolio.TotalCapital) * 100
	drawdownPercent := (portfolio.CurrentDrawdown / portfolio.PeakCapital) * 100

	// Warning levels
	if dailyLossPercent > cbm.config.MaxDailyLoss*0.8 {
		return "WARNING - Approaching daily loss limit"
	}
	if drawdownPercent > cbm.config.MaxDrawdown*0.8 {
		return "WARNING - Approaching drawdown limit"
	}
	if portfolio.ConsecutiveLosses >= 3 {
		return "CAUTION - Reduce position size due to losing streak"
	}

	return "NORMAL - All systems operational"
}
