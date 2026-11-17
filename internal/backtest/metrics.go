// Package backtest — metrics.go implements statistical summary calculations for backtest results.
package backtest

import (
	"math"

	"github.com/rantipay/trading-mcp/internal/backtest/domain"
)

// annualizationFactor converts per-trade std-dev to an annualised Sharpe ratio.
// Assumption: 5-minute candles, 252 trading days, 24h/day, 12 candles/hour → 252*24*12 = 72576.
const annualizationFactor = 72576.0

// CalculateMetrics derives all summary statistics from a slice of completed trades.
// All divisions are guarded against zero denominators.
func CalculateMetrics(trades []domain.BacktestTrade) domain.Metrics {
	n := len(trades)
	if n == 0 {
		return domain.Metrics{}
	}

	returns := make([]float64, n)
	for i, t := range trades {
		returns[i] = t.PnLPct
	}

	// --- Win/Loss partitioning ---
	var wins, losses []float64
	for _, r := range returns {
		if r > 0 {
			wins = append(wins, r)
		} else {
			losses = append(losses, r)
		}
	}

	winRate := float64(len(wins)) / float64(n)

	// --- Average win / loss ---
	avgWin := mean(wins)
	avgLoss := math.Abs(mean(losses))

	// --- Largest win / loss ---
	largestWin := maxSlice(wins)
	largestLoss := math.Abs(minSlice(losses))

	// --- Profit factor ---
	sumWins := sumSlice(wins)
	sumLosses := math.Abs(sumSlice(losses))
	profitFactor := 0.0
	if sumLosses > 0 {
		profitFactor = sumWins / sumLosses
	}

	// --- Sharpe (annualised) ---
	mu := mean(returns)
	sigma := stdDev(returns, mu)
	sharpe := 0.0
	if sigma > 0 {
		sharpe = mu / sigma * math.Sqrt(annualizationFactor)
	}

	// --- Max drawdown (peak-to-trough on cumulative equity) ---
	maxDD := maxDrawdown(returns)

	// --- Consecutive wins / losses ---
	maxConsecWins, maxConsecLosses := consecutiveStreaks(returns)

	// --- Expected value ---
	expectedValue := avgWin*winRate - avgLoss*(1-winRate)

	return domain.Metrics{
		NTrades:            n,
		WinRate:            winRate,
		Sharpe:             sharpe,
		MaxDrawdown:        maxDD * 100, // EV-3: convert fraction→percentage to match MaxDrawdownPct=15.0 threshold
		ProfitFactor:       profitFactor,
		AvgWinPct:          avgWin,
		AvgLossPct:         avgLoss,
		LargestWin:         largestWin,
		LargestLoss:        largestLoss,
		NConsecutiveWins:   maxConsecWins,
		NConsecutiveLosses: maxConsecLosses,
		ExpectedValue:      expectedValue,
	}
}

// mean returns the arithmetic mean of a float64 slice. Returns 0 for empty slice.
func mean(s []float64) float64 {
	if len(s) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range s {
		sum += v
	}
	return sum / float64(len(s))
}

// stdDev returns population standard deviation. Returns 0 for slices of length < 2.
func stdDev(s []float64, mu float64) float64 {
	if len(s) < 2 {
		return 0
	}
	variance := 0.0
	for _, v := range s {
		d := v - mu
		variance += d * d
	}
	return math.Sqrt(variance / float64(len(s)))
}

// sumSlice sums all elements.
func sumSlice(s []float64) float64 {
	t := 0.0
	for _, v := range s {
		t += v
	}
	return t
}

// maxSlice returns the maximum value in the slice, or 0 for empty.
func maxSlice(s []float64) float64 {
	if len(s) == 0 {
		return 0
	}
	m := s[0]
	for _, v := range s[1:] {
		if v > m {
			m = v
		}
	}
	return m
}

// minSlice returns the minimum value in the slice, or 0 for empty.
func minSlice(s []float64) float64 {
	if len(s) == 0 {
		return 0
	}
	m := s[0]
	for _, v := range s[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

// maxDrawdown computes the maximum peak-to-trough percentage decline on the
// cumulative equity curve built from the return series.
func maxDrawdown(returns []float64) float64 {
	if len(returns) == 0 {
		return 0
	}
	equity := 100.0
	peak := equity
	maxDD := 0.0
	for _, r := range returns {
		equity += equity * r / 100.0
		if equity > peak {
			peak = equity
		}
		if peak > 0 {
			dd := (peak - equity) / peak
			if dd > maxDD {
				maxDD = dd
			}
		}
	}
	return maxDD
}

// consecutiveStreaks returns the maximum consecutive win and loss streaks.
func consecutiveStreaks(returns []float64) (maxWins, maxLosses int) {
	curWins, curLosses := 0, 0
	for _, r := range returns {
		if r > 0 {
			curWins++
			curLosses = 0
		} else {
			curLosses++
			curWins = 0
		}
		if curWins > maxWins {
			maxWins = curWins
		}
		if curLosses > maxLosses {
			maxLosses = curLosses
		}
	}
	return
}
