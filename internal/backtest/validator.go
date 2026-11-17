package backtest

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/ai"
	"github.com/rantipay/trading-mcp/internal/backtest/domain"
	"github.com/rantipay/trading-mcp/internal/gct"
)

// CandleSource supplies archived historical candles for validation. Implemented
// by storage.CandleArchiveRepository.
type CandleSource interface {
	LoadRange(pair, timeframe string, from, to time.Time) ([]ai.Candle, error)
	Coverage(pair, timeframe string) (count int, oldest, newest time.Time, err error)
}

// ValidationConfig defines the pass/fail thresholds for certifying a strategy.
// The point is anti-overfitting: a strategy must show a positive edge CONSISTENTLY
// across multiple out-of-sample time windows, not just one lucky period.
//
// All PnL and ProfitFactor thresholds are evaluated on NET figures (after fees + slippage
// per domain.DefaultCostModel). A PF ≥ 1.3 net is a meaningful edge; gross-only
// thresholds are misleading in scalping where costs erode much of the alpha.
type ValidationConfig struct {
	Windows         int              // number of sequential out-of-sample windows
	MinTradesTotal  int              // minimum trades across all windows (statistical significance)
	MinProfitFactor float64          // aggregate NET profit-factor threshold
	MinConsistency  float64          // fraction of windows that must be net-profitable [0,1]
	MaxDrawdownPct  float64          // worst acceptable per-window max drawdown (positive %)
	Costs           domain.CostModel // fee + slippage model; zero → DefaultCostModel()
}

// DefaultValidationConfig is a deliberately strict baseline: a strategy that
// can't clear these has no demonstrable edge and should not risk live capital.
// All thresholds apply to NET PnL (after DefaultCostModel fees and slippage).
func DefaultValidationConfig() ValidationConfig {
	return ValidationConfig{
		Windows:         4,
		MinTradesTotal:  30,
		MinProfitFactor: 1.3,
		MinConsistency:  0.6,
		MaxDrawdownPct:  15.0,
		Costs:           domain.DefaultCostModel(),
	}
}

// WindowResult is the outcome of one out-of-sample window.
type WindowResult struct {
	From    time.Time      `json:"from"`
	To      time.Time      `json:"to"`
	Metrics domain.Metrics `json:"metrics"`
	NetPnL  float64        `json:"net_pnl_pct"`
}

// ValidationVerdict is the certification result for a (pair, strategy) combo.
type ValidationVerdict struct {
	Pair            string         `json:"pair"`
	Exchange        string         `json:"exchange"`
	Pass            bool           `json:"pass"`
	Reason          string         `json:"reason"`
	Windows         []WindowResult `json:"windows"`
	NTradesTotal    int            `json:"n_trades_total"`
	AggProfitFactor float64        `json:"agg_profit_factor"`
	Consistency     float64        `json:"consistency"`
	WorstDrawdown   float64        `json:"worst_drawdown"`
	AggregateMetric domain.Metrics `json:"aggregate_metrics"`
	ValidatedAt     time.Time      `json:"validated_at"`
}

// Validator runs walk-forward / out-of-sample robustness validation over archived
// candles and produces a pass/fail verdict that the live gate can consult.
type Validator struct {
	engine  *Engine
	candles CandleSource
	log     *logrus.Logger
}

// NewValidator constructs a Validator.
func NewValidator(engine *Engine, candles CandleSource, log *logrus.Logger) *Validator {
	return &Validator{engine: engine, candles: candles, log: log}
}

// Validate splits the archived history for `pair` into `cfg.Windows` sequential
// out-of-sample windows, backtests the strategy (baseline + overrides) on each,
// and certifies it only if the edge holds CONSISTENTLY across windows.
// PnL figures in results and the pass/fail decision are NET of cfg.Costs (fees + slippage).
func (v *Validator) Validate(ctx context.Context, pair, exchange, baseline string, overrides map[string]string, cfg ValidationConfig) (*ValidationVerdict, error) {
	if cfg.Windows < 1 {
		cfg.Windows = 1
	}
	// Apply default cost model if caller left it zero-valued.
	costs := cfg.Costs
	if costs.EntryTakerFeePct == 0 && costs.ExitTakerFeePct == 0 {
		costs = domain.DefaultCostModel()
	}
	count, oldest, newest, err := v.candles.Coverage(pair, "5m")
	if err != nil {
		return nil, fmt.Errorf("coverage: %w", err)
	}
	const warmup = 12 * time.Hour // covers the engine's 100×5m warmup with margin
	if count < 200 || newest.Sub(oldest) < warmup+cfg.windowMinSpan() {
		return &ValidationVerdict{
			Pair: pair, Exchange: exchange, Pass: false,
			Reason:      fmt.Sprintf("insufficient history: %d candles spanning %s (need more backfill)", count, newest.Sub(oldest).Round(time.Hour)),
			ValidatedAt: time.Now(),
		}, nil
	}

	// Usable span starts after the warmup buffer so window 1 has indicators ready.
	spanStart := oldest.Add(warmup)
	totalSpan := newest.Sub(spanStart)
	winSpan := totalSpan / time.Duration(cfg.Windows)

	verdict := &ValidationVerdict{Pair: pair, Exchange: exchange, ValidatedAt: time.Now()}
	var allTrades []domain.BacktestTrade
	profitableWindows := 0
	worstDD := 0.0

	for w := 0; w < cfg.Windows; w++ {
		winStart := spanStart.Add(time.Duration(w) * winSpan)
		winEnd := winStart.Add(winSpan)
		if w == cfg.Windows-1 {
			winEnd = newest
		}

		// Load candles covering [winStart - warmup, winEnd] for all timeframes.
		loadFrom := winStart.Add(-warmup)
		c5, _ := v.loadGct(pair, "5m", loadFrom, winEnd)
		if len(c5) == 0 {
			continue
		}
		c1, _ := v.loadGct(pair, "1m", loadFrom, winEnd)
		c15, _ := v.loadGct(pair, "15m", loadFrom, winEnd)

		req := domain.BacktestRequest{
			Pair: pair, Exchange: exchange,
			BaselinePreset: baseline, Overrides: overrides,
			From: winStart, To: winEnd, TriggeredBy: "validator",
			Costs: costs, // net-of-fee simulation
		}
		res, rerr := v.engine.Replay(ctx, req, c1, c5, c15)
		if rerr != nil {
			v.log.WithError(rerr).WithField("pair", pair).Debug("validator: window replay failed")
			continue
		}

		net := 0.0
		for _, t := range res.Trades {
			net += t.PnLPct
		}
		if net > 0 {
			profitableWindows++
		}
		if res.Metrics.MaxDrawdown > worstDD {
			worstDD = res.Metrics.MaxDrawdown
		}
		allTrades = append(allTrades, res.Trades...)
		verdict.Windows = append(verdict.Windows, WindowResult{From: winStart, To: winEnd, Metrics: res.Metrics, NetPnL: net})
	}

	// Aggregate across all windows.
	agg := CalculateMetrics(allTrades)
	verdict.AggregateMetric = agg
	verdict.NTradesTotal = agg.NTrades
	verdict.AggProfitFactor = agg.ProfitFactor
	verdict.WorstDrawdown = worstDD
	if len(verdict.Windows) > 0 {
		verdict.Consistency = float64(profitableWindows) / float64(len(verdict.Windows))
	}

	verdict.Pass, verdict.Reason = evaluate(verdict, cfg)
	v.log.WithFields(logrus.Fields{
		"pair": pair, "pass": verdict.Pass, "trades": verdict.NTradesTotal,
		"pf": fmt.Sprintf("%.2f", verdict.AggProfitFactor), "consistency": fmt.Sprintf("%.0f%%", verdict.Consistency*100),
		"worst_dd": fmt.Sprintf("%.1f%%", verdict.WorstDrawdown), "reason": verdict.Reason,
	}).Info("strategy validation complete")
	return verdict, nil
}

// EvaluateVerdict is the exported version of evaluate for testing.
// Production code calls evaluate() directly; tests use EvaluateVerdict.
func EvaluateVerdict(v *ValidationVerdict, cfg ValidationConfig) (bool, string) {
	return evaluate(v, cfg)
}

// evaluate applies the pass/fail rules and returns a human-readable reason.
func evaluate(v *ValidationVerdict, cfg ValidationConfig) (bool, string) {
	if v.NTradesTotal < cfg.MinTradesTotal {
		return false, fmt.Sprintf("only %d trades (need ≥%d for significance)", v.NTradesTotal, cfg.MinTradesTotal)
	}
	if v.AggProfitFactor < cfg.MinProfitFactor {
		return false, fmt.Sprintf("profit factor %.2f below %.2f (no edge)", v.AggProfitFactor, cfg.MinProfitFactor)
	}
	if v.Consistency < cfg.MinConsistency {
		return false, fmt.Sprintf("edge inconsistent: only %.0f%% of windows profitable (need %.0f%%)", v.Consistency*100, cfg.MinConsistency*100)
	}
	if v.WorstDrawdown > cfg.MaxDrawdownPct {
		return false, fmt.Sprintf("worst-window drawdown %.1f%% exceeds %.1f%%", v.WorstDrawdown, cfg.MaxDrawdownPct)
	}
	return true, fmt.Sprintf("validated: PF %.2f, %.0f%% windows green, %d trades, max DD %.1f%%",
		v.AggProfitFactor, v.Consistency*100, v.NTradesTotal, v.WorstDrawdown)
}

// loadGct loads archived candles and converts ai.Candle → *gct.Candle for the engine.
func (v *Validator) loadGct(pair, tf string, from, to time.Time) ([]*gct.Candle, error) {
	aic, err := v.candles.LoadRange(pair, tf, from, to)
	if err != nil {
		return nil, err
	}
	out := make([]*gct.Candle, len(aic))
	for i, c := range aic {
		out[i] = &gct.Candle{
			Timestamp: c.Timestamp,
			Open:      c.Open, High: c.High, Low: c.Low, Close: c.Close, Volume: c.Volume,
		}
	}
	return out, nil
}

// windowMinSpan is the minimum total span required to run the configured windows
// with at least a few hours of test data each.
func (c ValidationConfig) windowMinSpan() time.Duration {
	return time.Duration(c.Windows) * 6 * time.Hour
}
