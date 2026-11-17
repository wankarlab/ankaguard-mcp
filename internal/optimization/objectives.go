package optimization

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	backtestdomain "github.com/rantipay/trading-mcp/internal/backtest/domain"
)

// Objective evaluates a set of backtest metrics and returns a scalar score.
// Higher is always better. Implementations should return math.Inf(-1) to
// discard trials that do not meet minimum quality constraints.
type Objective func(metrics backtestdomain.Metrics) float64

// Minimum number of trades required for a trial to be considered valid.
// Trials below this threshold are penalised to -Inf to prevent overfitting
// on a small sample (e.g., 3 trades with 100% win rate).
const minTradesRequired = 20

// penaltyGuard returns math.Inf(-1) if metrics.NTrades < minTradesRequired,
// otherwise returns the value produced by inner.
func penaltyGuard(inner func(m backtestdomain.Metrics) float64) Objective {
	return func(m backtestdomain.Metrics) float64 {
		if m.NTrades < minTradesRequired {
			return math.Inf(-1)
		}
		return inner(m)
	}
}

// MaximizeSharpe maximises the Sharpe ratio with n_trades penalty.
var MaximizeSharpe Objective = penaltyGuard(func(m backtestdomain.Metrics) float64 {
	return m.Sharpe
})

// MaximizeProfitFactor maximises the profit factor with n_trades penalty.
var MaximizeProfitFactor Objective = penaltyGuard(func(m backtestdomain.Metrics) float64 {
	return m.ProfitFactor
})

// MaximizeWinRate maximises win rate (0–1) with n_trades penalty.
var MaximizeWinRate Objective = penaltyGuard(func(m backtestdomain.Metrics) float64 {
	return m.WinRate
})

// MinimizeMaxDrawdown minimises drawdown by returning its negative value.
// Lower drawdown → higher score (less negative).
var MinimizeMaxDrawdown Objective = penaltyGuard(func(m backtestdomain.Metrics) float64 {
	return -m.MaxDrawdown
})

// MaximizeExpectedValue maximises the trade expected value with n_trades penalty.
var MaximizeExpectedValue Objective = penaltyGuard(func(m backtestdomain.Metrics) float64 {
	return m.ExpectedValue
})

// MultiObjective creates a weighted combination of named objectives.
//
// Supported weight keys:
//
//	"sharpe", "profit_factor", "win_rate", "max_drawdown" (negated), "expected_value"
//
// Example:
//
//	MultiObjective(map[string]float64{"sharpe": 0.5, "win_rate": 0.3, "max_drawdown": -0.2})
func MultiObjective(weights map[string]float64) Objective {
	return penaltyGuard(func(m backtestdomain.Metrics) float64 {
		score := 0.0
		for name, w := range weights {
			score += w * metricByName(m, name)
		}
		return score
	})
}

// metricByName retrieves a named metric value; unknown names return 0.
func metricByName(m backtestdomain.Metrics, name string) float64 {
	switch strings.ToLower(name) {
	case "sharpe":
		return m.Sharpe
	case "profit_factor":
		return m.ProfitFactor
	case "win_rate":
		return m.WinRate
	case "max_drawdown":
		return m.MaxDrawdown
	case "expected_value":
		return m.ExpectedValue
	case "n_trades":
		return float64(m.NTrades)
	default:
		return 0
	}
}

// ParseObjective converts a string name to an Objective.
//
// Supported names:
//   - "maximize_sharpe"
//   - "maximize_profit_factor"
//   - "maximize_win_rate"
//   - "minimize_max_drawdown"
//   - "maximize_expected_value"
//   - "multi:<w>*<metric>[+<w>*<metric>...]"
//     e.g. "multi:0.5*sharpe+0.3*win_rate-0.2*max_drawdown"
func ParseObjective(name string) (Objective, error) {
	switch name {
	case "maximize_sharpe":
		return MaximizeSharpe, nil
	case "maximize_profit_factor":
		return MaximizeProfitFactor, nil
	case "maximize_win_rate":
		return MaximizeWinRate, nil
	case "minimize_max_drawdown":
		return MinimizeMaxDrawdown, nil
	case "maximize_expected_value":
		return MaximizeExpectedValue, nil
	}

	if strings.HasPrefix(name, "multi:") {
		expr := strings.TrimPrefix(name, "multi:")
		weights, err := parseMultiExpr(expr)
		if err != nil {
			return nil, fmt.Errorf("parse multi objective %q: %w", name, err)
		}
		return MultiObjective(weights), nil
	}

	return nil, fmt.Errorf("unknown objective %q; supported: maximize_sharpe, maximize_profit_factor, maximize_win_rate, minimize_max_drawdown, maximize_expected_value, multi:<expr>", name)
}

// parseMultiExpr parses "0.5*sharpe+0.3*win_rate-0.2*max_drawdown" into a weight map.
func parseMultiExpr(expr string) (map[string]float64, error) {
	// Normalise signs: replace "-" with "+-" so we can split on "+".
	expr = strings.ReplaceAll(expr, " ", "")
	expr = strings.ReplaceAll(expr, "-", "+-")
	terms := strings.Split(expr, "+")
	weights := make(map[string]float64)
	for _, term := range terms {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		parts := strings.SplitN(term, "*", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid term %q; expected <weight>*<metric>", term)
		}
		w, err := strconv.ParseFloat(parts[0], 64)
		if err != nil {
			return nil, fmt.Errorf("invalid weight %q in term %q: %w", parts[0], term, err)
		}
		metric := strings.ToLower(strings.TrimSpace(parts[1]))
		weights[metric] = w
	}
	if len(weights) == 0 {
		return nil, fmt.Errorf("no valid terms found in expression %q", expr)
	}
	return weights, nil
}
