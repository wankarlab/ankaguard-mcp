// Package backtest implements the walk-forward backtesting engine.
// It replays historical candles through the DecisionEngine using a virtual config
// snapshot so that parameter changes can be evaluated without touching production config.
package backtest

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/ai"
	"github.com/rantipay/trading-mcp/internal/backtest/domain"
	dynamic "github.com/rantipay/trading-mcp/internal/config/dynamic"
	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/killswitch"
)

// Engine replays historical market data through the DecisionEngine using a virtual
// configuration snapshot, producing a BacktestResult without touching live config or orders.
//
// Cost model: fees and slippage are deducted from each trade via BacktestRequest.Costs
// (defaults to DefaultCostModel() — KuCoin futures taker 0.06%, maker 0.02%, SL slippage 0.05%).
// BacktestTrade.GrossPnLPct = price delta only; PnLPct = net of costs.
//
// Remaining limitations:
//   - No partial fills: all-or-nothing position sizing
//   - No latency: execution assumed instantaneous
//   - No funding rates for futures
//   - GetCandles uses limit (count), not from/to; count estimated from horizon duration
type Engine struct {
	gctClient   gct.GCTClient
	cfgStore    *dynamic.ConfigStore
	decisionEng *ai.DecisionEngine
	log         *logrus.Logger
}

// NewEngine constructs a BacktestEngine.
func NewEngine(
	gctClient gct.GCTClient,
	cfgStore *dynamic.ConfigStore,
	decisionEng *ai.DecisionEngine,
	log *logrus.Logger,
) *Engine {
	return &Engine{
		gctClient:   gctClient,
		cfgStore:    cfgStore,
		decisionEng: decisionEng,
		log:         log,
	}
}

// Run executes a full walk-forward backtest for the given request.
// It does NOT modify the live ConfigStore or place any real orders.
func (e *Engine) Run(ctx context.Context, req domain.BacktestRequest) (*domain.BacktestResult, error) {
	e.log.WithFields(logrus.Fields{
		"pair":     req.Pair,
		"exchange": req.Exchange,
		"from":     req.From,
		"to":       req.To,
	}).Info("backtest engine: starting run")

	// 1. Load historical candles for the three relevant timeframes.
	// gct.GCTClient.GetCandles uses limit (count), so estimate from the horizon duration.
	horizonDur := req.To.Sub(req.From)
	limit1m := int(math.Ceil(horizonDur.Minutes())) + 20 // +20 warming buffer
	limit5m := int(math.Ceil(horizonDur.Minutes()/5)) + 20
	limit15m := int(math.Ceil(horizonDur.Minutes()/15)) + 20

	candles1m, err := e.loadCandles(ctx, req.Exchange, req.Pair, "1m", limit1m)
	if err != nil {
		return nil, fmt.Errorf("load 1m candles: %w", err)
	}
	candles5m, err := e.loadCandles(ctx, req.Exchange, req.Pair, "5m", limit5m)
	if err != nil {
		return nil, fmt.Errorf("load 5m candles: %w", err)
	}
	candles15m, err := e.loadCandles(ctx, req.Exchange, req.Pair, "15m", limit15m)
	if err != nil {
		return nil, fmt.Errorf("load 15m candles: %w", err)
	}

	if len(candles5m) == 0 {
		return nil, fmt.Errorf("no 5m candles returned for %s/%s", req.Exchange, req.Pair)
	}

	return e.Replay(ctx, req, candles1m, candles5m, candles15m)
}

// Replay runs the backtest over pre-loaded candle slices (e.g. from the durable
// candle_archive) instead of fetching live. This is what the walk-forward
// validator uses to evaluate a strategy across many historical windows without
// hammering the exchange. Candles must be ascending by time.
//
// If req.Costs is zero-valued, DefaultCostModel() is applied automatically so
// callers that do not set it still get realistic net PnL figures.
func (e *Engine) Replay(ctx context.Context, req domain.BacktestRequest, candles1m, candles5m, candles15m []*gct.Candle) (*domain.BacktestResult, error) {
	if len(candles5m) == 0 {
		return nil, fmt.Errorf("no 5m candles provided for %s/%s", req.Exchange, req.Pair)
	}
	// Ring 0 (global kill switch) freezes every background engine, including
	// historical replays (validation, optimizer, auto-tuner): they burn CPU and
	// flood the log with virtual decisions while the operator wants a quiet system.
	if killswitch.Ring0Active() {
		return nil, killswitch.ErrActive
	}
	// Replayed decisions are virtual — mark the context so they log at Debug.
	ctx = ai.WithReplay(ctx)

	// Apply default cost model if the caller left it zero-valued.
	costs := req.Costs
	if costs.EntryTakerFeePct == 0 && costs.ExitTakerFeePct == 0 {
		costs = domain.DefaultCostModel()
	}

	// 2. Build a virtual snapshot: clone current (or preset baseline) + overlay overrides.
	virtSnap := e.cfgStore.SnapshotWithOverrides(req.BaselinePreset, req.Overrides)

	// Wrap the virtual Snapshot in a ScopedSnapshot (global scope) and inject it into
	// the context so that DecisionEngine reads it via SnapshotFromContext.
	scopedVirt := virtSnap.ToScopedSnapshot()
	virtCtx := dynamic.WithSnapshot(ctx, scopedVirt)

	// 3. Walk-forward replay over 5m candles.
	result := &domain.BacktestResult{}
	var openPos *virtualPosition

	const warmupCandles = 100
	for i, c5 := range candles5m {
		// Skip warming period so indicators are initialised.
		if i < warmupCandles {
			continue
		}

		// Filter to the requested horizon window.
		if c5.Timestamp.Before(req.From) {
			continue
		}
		if c5.Timestamp.After(req.To) {
			break
		}

		// Build a MarketContext for this candle using available data up to index i.
		mctx := e.buildContext(req.Pair, c5, candles1m, candles5m, candles15m, i)

		// Attempt to close open position: check SL/TP/timeout against this candle.
		if openPos != nil {
			if trade, closed := openPos.tryClose(c5, costs); closed {
				result.Trades = append(result.Trades, trade)
				openPos = nil
			}
		}

		// Evaluate trading decision with the virtual config snapshot.
		if i%64 == 0 && killswitch.Ring0Active() {
			return nil, killswitch.ErrActive
		}
		decision, err := e.decisionEng.EvaluateOpportunity(virtCtx, mctx)
		if err != nil {
			e.log.WithError(err).Debug("backtest: EvaluateOpportunity error; skipping candle")
			continue
		}

		if openPos == nil && (decision.Action == "BUY" || decision.Action == "SELL") {
			openPos = openVirtualPosition(c5, decision)
		}
	}

	// 4. Close any still-open position at the last candle.
	if openPos != nil && len(candles5m) > 0 {
		last := candles5m[len(candles5m)-1]
		trade := openPos.forceClose(last, "timeout", costs)
		result.Trades = append(result.Trades, trade)
	}

	// 5. Compute summary metrics.
	result.Metrics = CalculateMetrics(result.Trades)

	e.log.WithFields(logrus.Fields{
		"n_trades": result.Metrics.NTrades,
		"win_rate": result.Metrics.WinRate,
		"sharpe":   result.Metrics.Sharpe,
	}).Info("backtest engine: run complete")

	return result, nil
}

// loadCandles fetches historical candles via gctClient.
// TODO(TC-7.2): gct.GCTClient.GetCandles takes limit (count), not from/to. Adapt when a
// range-based method becomes available on gctClient.
func (e *Engine) loadCandles(ctx context.Context, exchange, pair, interval string, limit int) ([]*gct.Candle, error) {
	candles, err := e.gctClient.GetCandles(ctx, exchange, pair, interval, limit)
	if err != nil {
		return nil, err
	}
	return candles, nil
}

// buildContext constructs a MarketContext for the candle at position i using all data
// available up to that point (no lookahead).
func (e *Engine) buildContext(pair string, c5 *gct.Candle, candles1m, candles5m, candles15m []*gct.Candle, i int) *ai.MarketContext {
	// Helper to convert gct.Candle slice to ai.Candle slice (they share the same structure).
	convertCandles := func(src []*gct.Candle, upToIdx int) []ai.Candle {
		end := upToIdx + 1
		if end > len(src) {
			end = len(src)
		}
		out := make([]ai.Candle, end)
		for j, c := range src[:end] {
			out[j] = ai.Candle{
				Timestamp: c.Timestamp,
				Open:      c.Open,
				High:      c.High,
				Low:       c.Low,
				Close:     c.Close,
				Volume:    c.Volume,
			}
		}
		return out
	}

	// Find the 1m and 15m indices that correspond to this 5m candle's timestamp.
	limit1m := findTimestampIndex(candles1m, c5.Timestamp)
	limit15m := findTimestampIndex(candles15m, c5.Timestamp)

	ai1m := convertCandles(candles1m, limit1m)
	ai5m := convertCandles(candles5m, i)
	ai15m := convertCandles(candles15m, limit15m)

	mctx := &ai.MarketContext{
		Pair:         pair,
		Timestamp:    c5.Timestamp,
		CurrentPrice: c5.Close,
		Timeframes:   make(map[string]*ai.TimeframeContext),
	}

	if len(ai1m) > 0 {
		mctx.Timeframes["1m"] = &ai.TimeframeContext{
			Timeframe:  "1m",
			Candles:    ai1m,
			Indicators: computeIndicators(ai1m),
		}
	}
	if len(ai5m) > 0 {
		mctx.Timeframes["5m"] = &ai.TimeframeContext{
			Timeframe:  "5m",
			Trend:      deriveTrend(ai5m),
			Candles:    ai5m,
			Indicators: computeIndicators(ai5m),
		}
	}
	if len(ai15m) > 0 {
		mctx.Timeframes["15m"] = &ai.TimeframeContext{
			Timeframe:  "15m",
			Trend:      deriveTrend(ai15m),
			Candles:    ai15m,
			Indicators: computeIndicators(ai15m),
		}
	}

	return mctx
}

// findTimestampIndex returns the index of the last candle with Timestamp <= ts.
func findTimestampIndex(candles []*gct.Candle, ts time.Time) int {
	idx := 0
	for i, c := range candles {
		if !c.Timestamp.After(ts) {
			idx = i
		}
	}
	return idx
}

// deriveTrend computes a simple trend label from the last few candles.
func deriveTrend(candles []ai.Candle) string {
	if len(candles) < 5 {
		return "sideways"
	}
	last := candles[len(candles)-1].Close
	prev := candles[len(candles)-5].Close
	if last > prev*1.002 {
		return "bullish"
	}
	if last < prev*0.998 {
		return "bearish"
	}
	return "sideways"
}

// ---- virtual position helpers ----

// virtualPosition tracks a simulated open trade.
type virtualPosition struct {
	side       string
	entryPrice float64
	entryTime  time.Time
	stopLoss   float64
	takeProfit float64
	confidence float64
}

// openVirtualPosition creates a new virtual position from a decision signal.
func openVirtualPosition(c *gct.Candle, decision *ai.Decision) *virtualPosition {
	return &virtualPosition{
		side:       decision.Action, // "BUY" or "SELL"
		entryPrice: c.Close,
		entryTime:  c.Timestamp,
		stopLoss:   decision.StopLoss,
		takeProfit: decision.TakeProfit,
		confidence: decision.Confidence,
	}
}

// tryClose checks whether the candle triggers SL or TP. Returns (trade, true) if closed.
func (p *virtualPosition) tryClose(c *gct.Candle, costs domain.CostModel) (domain.BacktestTrade, bool) {
	if p.side == "BUY" {
		if p.stopLoss > 0 && c.Low <= p.stopLoss {
			return p.buildTrade(c, p.stopLoss, "stop_loss", costs), true
		}
		if p.takeProfit > 0 && c.High >= p.takeProfit {
			return p.buildTrade(c, p.takeProfit, "take_profit", costs), true
		}
	} else { // SELL / SHORT
		if p.stopLoss > 0 && c.High >= p.stopLoss {
			return p.buildTrade(c, p.stopLoss, "stop_loss", costs), true
		}
		if p.takeProfit > 0 && c.Low <= p.takeProfit {
			return p.buildTrade(c, p.takeProfit, "take_profit", costs), true
		}
	}
	return domain.BacktestTrade{}, false
}

// forceClose closes the position at the candle's close price with the given reason.
func (p *virtualPosition) forceClose(c *gct.Candle, reason string, costs domain.CostModel) domain.BacktestTrade {
	return p.buildTrade(c, c.Close, reason, costs)
}

// buildTrade constructs a BacktestTrade from the exit data, deducting fees and slippage.
//
// Cost model:
//   - Entry: taker fee (market order, always).
//   - Exit on SL: taker fee + slippage (adverse fill).
//   - Exit on TP: maker fee (limit order assumed; no slippage — fills at target price).
//   - Timeout / force-close: taker fee (market order to exit).
//
// GrossPnLPct = raw price delta; PnLPct = net of all costs; TotalCostPct = deducted.
func (p *virtualPosition) buildTrade(c *gct.Candle, exitPrice float64, reason string, costs domain.CostModel) domain.BacktestTrade {
	var grossPnlPct float64
	if p.entryPrice != 0 {
		if p.side == "BUY" {
			grossPnlPct = (exitPrice - p.entryPrice) / p.entryPrice * 100
		} else {
			grossPnlPct = (p.entryPrice - exitPrice) / p.entryPrice * 100
		}
	}

	// Determine exit fee and slippage based on exit reason.
	exitFeePct := costs.ExitTakerFeePct
	slippagePct := 0.0
	if reason == "stop_loss" {
		slippagePct = costs.SlippagePct // SL always fills at a worse price in live
	} else if reason == "take_profit" {
		exitFeePct = costs.ExitMakerFeePct // TP uses a limit order
	}

	totalCostPct := costs.EntryTakerFeePct + exitFeePct + slippagePct
	netPnlPct := grossPnlPct - totalCostPct

	exitTime := c.Timestamp
	return domain.BacktestTrade{
		CandleTime:   p.entryTime,
		Side:         p.side,
		EntryPrice:   p.entryPrice,
		ExitPrice:    exitPrice,
		ExitTime:     &exitTime,
		ExitReason:   reason,
		GrossPnLPct:  grossPnlPct,
		PnLPct:       netPnlPct,
		TotalCostPct: totalCostPct,
		Confidence:   p.confidence,
	}
}
