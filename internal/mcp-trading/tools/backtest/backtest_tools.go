package backtest

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/tools/common"
)

const (
	defaultInterval = "1h"
	defaultLimit    = 500
	maxLimit        = 1500
	defaultFeePct   = 0.1  // per side, percent of notional
	defaultSlipPct  = 0.05 // per side, percent of price
	defaultCapital  = 1000.0
	minTradeWindow  = 20 // candles needed after the strategy warm-up
	maxReturnTrades = 200
)

// candleSource is the only data source of the backtest. *gctclient.Client
// implements it; tests inject a fake.
type candleSource interface {
	GetCandles(ctx context.Context, exchange, pair, interval string, limit int) ([]models.Candle, error)
}

// BacktestStrategyTool replays a simple strategy over real exchange candles.
// The output is an estimate: it states the period, candle source, fee,
// slippage and whether funding was modeled.
type BacktestStrategyTool struct {
	source candleSource
}

// NewBacktestStrategyTool builds the tool on top of the exchange client.
func NewBacktestStrategyTool(client *gctclient.Client) *BacktestStrategyTool {
	return &BacktestStrategyTool{source: client}
}

func (t *BacktestStrategyTool) Name() string { return "backtest_strategy" }

func (t *BacktestStrategyTool) Description() string {
	return "Estimate how a simple strategy (moving_average, rsi, bollinger_bands) would have performed over the most recent real exchange candles. Returns an estimate with its assumptions (period, candle source, fees, slippage, funding not modeled); past results do not predict future ones."
}

func (t *BacktestStrategyTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange":        map[string]interface{}{"type": "string", "description": "Exchange name (e.g., kucoin)"},
			"pair":            map[string]interface{}{"type": "string", "description": "Trading pair (e.g., BTC-USDT)"},
			"strategy":        map[string]interface{}{"type": "string", "enum": []string{StrategyMovingAverage, StrategyRSI, StrategyBollingerBands}, "description": "Strategy to replay (long-only)"},
			"interval":        map[string]interface{}{"type": "string", "default": defaultInterval, "description": "Candle interval (1m, 5m, 15m, 1h, 4h, 1d)"},
			"limit":           map[string]interface{}{"type": "integer", "default": defaultLimit, "description": fmt.Sprintf("Number of most recent candles to replay (max %d)", maxLimit)},
			"parameters":      map[string]interface{}{"type": "object", "description": "Strategy parameters: moving_average {fast_period, slow_period}; rsi {period, oversold, overbought}; bollinger_bands {period, std_dev}"},
			"fee_pct":         map[string]interface{}{"type": "number", "default": defaultFeePct, "description": "Assumed fee per side, percent of notional"},
			"slippage_pct":    map[string]interface{}{"type": "number", "default": defaultSlipPct, "description": "Assumed slippage per side, percent of price"},
			"initial_capital": map[string]interface{}{"type": "number", "default": defaultCapital, "description": "Starting capital in quote currency"},
		},
		"required": []string{"exchange", "pair", "strategy"},
	}
}

func (t *BacktestStrategyTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, pair, err := common.ValidateExchangeAndPair(params)
	if err != nil {
		return nil, err
	}
	strategy := strings.ToLower(common.GetStringParam(params, "strategy", ""))
	if strategy == "" {
		return nil, fmt.Errorf("strategy parameter is required")
	}
	interval := common.GetStringParam(params, "interval", defaultInterval)
	step, err := intervalDuration(interval)
	if err != nil {
		return nil, err
	}
	limit := common.GetIntParam(params, "limit", defaultLimit)
	if limit < 2 || limit > maxLimit {
		return nil, fmt.Errorf("limit must be between 2 and %d", maxLimit)
	}
	userParams, _ := params["parameters"].(map[string]interface{})
	signal, resolved, warmup, err := buildStrategy(strategy, userParams)
	if err != nil {
		return nil, err
	}
	cfg := simConfig{
		InitialCapital: common.GetFloatParam(params, "initial_capital", defaultCapital),
		FeePct:         common.GetFloatParam(params, "fee_pct", defaultFeePct),
		SlippagePct:    common.GetFloatParam(params, "slippage_pct", defaultSlipPct),
	}
	if cfg.InitialCapital <= 0 || cfg.FeePct < 0 || cfg.SlippagePct < 0 {
		return nil, fmt.Errorf("initial_capital must be positive; fee_pct and slippage_pct cannot be negative")
	}

	raw, err := t.source.GetCandles(ctx, exchange, pair, interval, limit)
	if err != nil {
		return nil, common.DataUnavailable("could not fetch candles from the exchange: "+err.Error(), map[string]interface{}{
			"exchange": exchange, "pair": pair, "interval": interval,
		})
	}
	candles := normalizeCandles(raw)
	needed := warmup + minTradeWindow
	if len(candles) < needed {
		return nil, common.DataUnavailable(fmt.Sprintf("not enough candles for %s: have %d, need %d", strategy, len(candles), needed), map[string]interface{}{
			"candles_available": len(candles), "candles_required": needed, "candles_missing": needed - len(candles),
			"exchange": exchange, "pair": pair, "interval": interval,
		})
	}

	periodsPerYear := float64(365*24*time.Hour) / float64(step)
	sim := simulate(candles, signal, cfg, periodsPerYear)

	wins := 0
	var trades []map[string]interface{}
	for _, tr := range sim.Trades {
		if tr.PnL > 0 {
			wins++
		}
		trades = append(trades, map[string]interface{}{
			"entry_time": tr.EntryTime, "exit_time": tr.ExitTime,
			"type": "long", "entry_price": tr.EntryPrice, "exit_price": tr.ExitPrice,
			"amount": tr.Amount, "pnl": round(tr.PnL, 8), "pnl_percent": round(tr.PnLPct, 4),
		})
	}
	truncated := false
	if len(trades) > maxReturnTrades {
		trades, truncated = trades[len(trades)-maxReturnTrades:], true
	}
	winRate := 0.0
	if n := len(sim.Trades); n > 0 {
		winRate = float64(wins) / float64(n) * 100
	}

	return map[string]interface{}{
		"estimate":   true,
		"exchange":   exchange,
		"pair":       pair,
		"strategy":   strategy,
		"parameters": resolved,
		"assumptions": map[string]interface{}{
			"period_start":          candles[0].Timestamp,
			"period_end":            candles[len(candles)-1].Timestamp,
			"candles_used":          len(candles),
			"interval":              interval,
			"candle_source":         fmt.Sprintf("%s historic candles through GoCryptoTrader (real market data, not generated)", exchange),
			"fee_pct_per_side":      cfg.FeePct,
			"slippage_pct_per_side": cfg.SlippagePct,
			"funding_modeled":       false,
			"execution_model":       "long-only, one position using all capital, signals evaluated and filled at the candle close with slippage and fee; no order book depth, partial fills or latency",
			"initial_capital":       cfg.InitialCapital,
			"disclaimer":            "Estimate over past data; it does not predict future results.",
		},
		// Flat metrics: the REST backtesting handler reads them at the root.
		"final_equity":         round(sim.FinalEquity, 8),
		"roi":                  round(sim.ROIPct, 4),
		"max_drawdown":         round(sim.MaxDrawdownPct, 4),
		"sharpe_ratio":         round(sim.SharpeRatio, 4),
		"total_trades":         len(sim.Trades),
		"winning_trades":       wins,
		"losing_trades":        len(sim.Trades) - wins,
		"win_rate_pct":         round(winRate, 2),
		"open_position_at_end": sim.OpenPositionEnd,
		"units":                "roi, max_drawdown (negative) and win_rate_pct are percentages",
		"trades":               trades,
		"trades_truncated":     truncated,
	}, nil
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}
