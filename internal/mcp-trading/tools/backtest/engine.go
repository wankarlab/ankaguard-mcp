package backtest

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
)

// Supported strategy names.
const (
	StrategyMovingAverage  = "moving_average"
	StrategyRSI            = "rsi"
	StrategyBollingerBands = "bollinger_bands"
)

// Assumptions that the engine applies and that every result declares.
type simConfig struct {
	InitialCapital float64
	FeePct         float64 // per side, percent of notional
	SlippagePct    float64 // per side, percent of price
}

// trade is one closed round trip (long only).
type trade struct {
	EntryTime  time.Time
	ExitTime   time.Time
	EntryPrice float64
	ExitPrice  float64
	Amount     float64
	PnL        float64
	PnLPct     float64
}

type simResult struct {
	Trades          []trade
	FinalEquity     float64
	ROIPct          float64
	MaxDrawdownPct  float64
	SharpeRatio     float64
	OpenPositionEnd bool
}

// signalFunc returns, for candle index i, whether the strategy wants to be long.
// ok is false while the strategy is still warming up.
type signalFunc func(closes []float64, i int, inPosition bool) (wantLong bool, ok bool)

// normalizeCandles sorts candles by timestamp ascending (the exchange layer
// has returned newest-first for some pairs) and drops candles with a
// non-positive close.
func normalizeCandles(in []models.Candle) []models.Candle {
	out := make([]models.Candle, 0, len(in))
	for _, c := range in {
		if c.Close > 0 {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
	return out
}

func sma(v []float64, end, period int) float64 {
	s := 0.0
	for i := end - period + 1; i <= end; i++ {
		s += v[i]
	}
	return s / float64(period)
}

func stddev(v []float64, end, period int, mean float64) float64 {
	s := 0.0
	for i := end - period + 1; i <= end; i++ {
		d := v[i] - mean
		s += d * d
	}
	return math.Sqrt(s / float64(period))
}

// rsiAt computes Wilder's RSI at index end using only closes[0..end].
func rsiAt(closes []float64, end, period int) float64 {
	gain, loss := 0.0, 0.0
	for i := 1; i <= period; i++ {
		d := closes[i] - closes[i-1]
		if d > 0 {
			gain += d
		} else {
			loss -= d
		}
	}
	gain /= float64(period)
	loss /= float64(period)
	for i := period + 1; i <= end; i++ {
		d := closes[i] - closes[i-1]
		g, l := 0.0, 0.0
		if d > 0 {
			g = d
		} else {
			l = -d
		}
		gain = (gain*float64(period-1) + g) / float64(period)
		loss = (loss*float64(period-1) + l) / float64(period)
	}
	if loss == 0 {
		return 100
	}
	return 100 - 100/(1+gain/loss)
}

// strategyParams are the resolved strategy parameters, echoed in the response.
type strategyParams map[string]float64

func buildStrategy(name string, p map[string]interface{}) (signalFunc, strategyParams, int, error) {
	num := func(key string, def float64) float64 {
		if v, ok := p[key].(float64); ok && v > 0 {
			return v
		}
		return def
	}
	switch name {
	case StrategyMovingAverage:
		fast, slow := int(num("fast_period", 10)), int(num("slow_period", 30))
		if fast >= slow {
			return nil, nil, 0, fmt.Errorf("fast_period (%d) must be smaller than slow_period (%d)", fast, slow)
		}
		fn := func(c []float64, i int, _ bool) (bool, bool) {
			if i < slow-1 {
				return false, false
			}
			return sma(c, i, fast) > sma(c, i, slow), true
		}
		return fn, strategyParams{"fast_period": float64(fast), "slow_period": float64(slow)}, slow, nil
	case StrategyRSI:
		period := int(num("period", 14))
		oversold, overbought := num("oversold", 30), num("overbought", 70)
		if oversold >= overbought {
			return nil, nil, 0, fmt.Errorf("oversold (%.0f) must be smaller than overbought (%.0f)", oversold, overbought)
		}
		fn := func(c []float64, i int, in bool) (bool, bool) {
			if i < period {
				return false, false
			}
			r := rsiAt(c, i, period)
			switch {
			case r <= oversold:
				return true, true
			case r >= overbought:
				return false, true
			default:
				return in, true // hold the current state between thresholds
			}
		}
		return fn, strategyParams{"period": float64(period), "oversold": oversold, "overbought": overbought}, period + 1, nil
	case StrategyBollingerBands:
		period, k := int(num("period", 20)), num("std_dev", 2)
		fn := func(c []float64, i int, in bool) (bool, bool) {
			if i < period-1 {
				return false, false
			}
			mid := sma(c, i, period)
			sd := stddev(c, i, period, mid)
			switch {
			case c[i] <= mid-k*sd:
				return true, true
			case c[i] >= mid:
				return false, true
			default:
				return in, true
			}
		}
		return fn, strategyParams{"period": float64(period), "std_dev": k}, period, nil
	}
	return nil, nil, 0, fmt.Errorf("unknown strategy %q (supported: %s, %s, %s)", name, StrategyMovingAverage, StrategyRSI, StrategyBollingerBands)
}

// simulate replays signal over the candles. Signals are evaluated on the close
// of candle i and filled at the close of candle i plus/minus slippage, with a
// fee per side. One long position with all the capital at a time. A position
// still open at the end is marked to the last close and not counted as a trade.
func simulate(candles []models.Candle, signal signalFunc, cfg simConfig, periodsPerYear float64) simResult {
	closes := make([]float64, len(candles))
	for i, c := range candles {
		closes[i] = c.Close
	}
	cash := cfg.InitialCapital
	var amount, entryPrice, entryCost float64
	var entryTime time.Time
	inPos := false
	var res simResult

	equity := make([]float64, len(candles))
	fee := cfg.FeePct / 100
	slip := cfg.SlippagePct / 100
	for i := range candles {
		want, ok := signal(closes, i, inPos)
		if ok && want && !inPos {
			price := closes[i] * (1 + slip)
			amount = cash / (price * (1 + fee))
			entryCost = cash
			cash = 0
			entryPrice, entryTime, inPos = price, candles[i].Timestamp, true
		} else if ok && !want && inPos {
			price := closes[i] * (1 - slip)
			proceeds := amount * price * (1 - fee)
			res.Trades = append(res.Trades, trade{
				EntryTime: entryTime, ExitTime: candles[i].Timestamp,
				EntryPrice: entryPrice, ExitPrice: price, Amount: amount,
				PnL: proceeds - entryCost, PnLPct: (proceeds/entryCost - 1) * 100,
			})
			cash, amount, inPos = proceeds, 0, false
		}
		if inPos {
			equity[i] = amount * closes[i]
		} else {
			equity[i] = cash
		}
	}
	res.OpenPositionEnd = inPos
	res.FinalEquity = equity[len(equity)-1]
	res.ROIPct = (res.FinalEquity/cfg.InitialCapital - 1) * 100

	peak := equity[0]
	for _, e := range equity {
		if e > peak {
			peak = e
		}
		if peak > 0 {
			if dd := (e/peak - 1) * 100; dd < res.MaxDrawdownPct {
				res.MaxDrawdownPct = dd
			}
		}
	}

	rets := make([]float64, 0, len(equity)-1)
	for i := 1; i < len(equity); i++ {
		if equity[i-1] > 0 {
			rets = append(rets, equity[i]/equity[i-1]-1)
		}
	}
	if len(rets) > 1 {
		mean := 0.0
		for _, r := range rets {
			mean += r
		}
		mean /= float64(len(rets))
		v := 0.0
		for _, r := range rets {
			v += (r - mean) * (r - mean)
		}
		sd := math.Sqrt(v / float64(len(rets)-1))
		if sd > 0 {
			res.SharpeRatio = mean / sd * math.Sqrt(periodsPerYear)
		}
	}
	return res
}

// intervalDuration parses candle intervals such as 1m, 15m, 1h, 4h, 1d, 1w.
func intervalDuration(interval string) (time.Duration, error) {
	if len(interval) < 2 {
		return 0, fmt.Errorf("invalid interval %q", interval)
	}
	var n int
	if _, err := fmt.Sscanf(interval[:len(interval)-1], "%d", &n); err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid interval %q", interval)
	}
	switch interval[len(interval)-1] {
	case 'm':
		return time.Duration(n) * time.Minute, nil
	case 'h':
		return time.Duration(n) * time.Hour, nil
	case 'd':
		return time.Duration(n) * 24 * time.Hour, nil
	case 'w':
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	}
	return 0, fmt.Errorf("invalid interval %q (use m, h, d or w)", interval)
}
