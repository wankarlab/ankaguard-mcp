package monitoring

import "math"

// MetricsCalculator calculates performance metrics
type MetricsCalculator struct{}

// NewMetricsCalculator creates a new metrics calculator
func NewMetricsCalculator() *MetricsCalculator {
	return &MetricsCalculator{}
}

// Calculate computes metrics from decision records
func (mc *MetricsCalculator) Calculate(
	decisions []*DecisionRecord,
	period string,
) *PerformanceMetrics {
	metrics := &PerformanceMetrics{
		Period:         period,
		TotalDecisions: len(decisions),
	}

	if len(decisions) == 0 {
		return metrics
	}

	// Separate by outcome
	var wins, losses, scratches int
	var totalPnL, totalWinPnL, totalLossPnL float64
	var pnls []float64

	for _, decision := range decisions {
		if decision.Outcome == "" {
			continue // Skip open positions
		}

		pnls = append(pnls, decision.ActualPnL)
		totalPnL += decision.ActualPnL

		switch decision.Outcome {
		case "win", "take_profit":
			wins++
			totalWinPnL += decision.ActualPnL
		case "loss", "stop_loss":
			losses++
			totalLossPnL += decision.ActualPnL
		case "scratch", "break_even":
			scratches++
		}
	}

	closedTrades := wins + losses + scratches

	// Basic metrics
	metrics.ExecutedTrades = closedTrades
	metrics.WinningTrades = wins
	metrics.LosingTrades = losses
	metrics.ScratchTrades = scratches
	metrics.TotalPnL = totalPnL

	// Win rate
	if closedTrades > 0 {
		metrics.WinRate = float64(wins) / float64(closedTrades)
	}

	// Average win/loss
	if wins > 0 {
		metrics.AverageWin = totalWinPnL / float64(wins)
	}
	if losses > 0 {
		metrics.AverageLoss = totalLossPnL / float64(losses)
	}

	// Profit factor
	if totalLossPnL != 0 {
		metrics.ProfitFactor = math.Abs(totalWinPnL / totalLossPnL)
	}

	// Circuit breaker activations
	metrics.CircuitBreakerActivations = mc.countCircuitBreakers(decisions)

	return metrics
}

// countCircuitBreakers counts circuit breaker activations
func (mc *MetricsCalculator) countCircuitBreakers(decisions []*DecisionRecord) int {
	// This would need to be tracked separately in the system
	// For now, return 0
	return 0
}
