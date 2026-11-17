// Package featureeng implements deterministic feature extraction for the predictive
// model pipeline. Features are computed ONLY from candles at or before timestamp t,
// never from future data — this is the primary anti-leakage guardrail.
//
// The same ExtractFeatures function is used in both the live path and the batch
// dataset builder, guaranteeing identical feature values for identical inputs.
package featureeng

import (
	"math"
	"sync"
)

// Tunable thresholds (audit P1). These were hardcoded consts inside the functions
// below and silently shadowed the dynamic config. They are now package vars with
// thread-safe setters so the dynamic config (ai.featureeng.*) can override them
// without changing the pure, ctx-free signatures of ExtractFeatures/ForwardLabel
// (which are called from the excluded ai_trading_bot.go and the dataset builder).
//
// Defaults equal the previous hardcoded values, so behaviour is unchanged until a
// setter is called.
//
// Wired (audit-p1): applyGlobalScalarConfig in cmd/trading-mcp-server/main.go
// calls SetMinCandles / SetFlatThreshold from ai.featureeng.min_candles and
// ai.featureeng.flat_threshold_pct at startup and on every hot-reload.
var (
	featureMu     sync.RWMutex
	minCandlesVar = 35  // MACD slow(26) + signal(9) = 35 warm-up
	flatThreshold = 0.1 // forward-return band (%) classified as "flat"
)

// SetMinCandles overrides the minimum candle count for feature extraction. Values
// below 35 are rejected (MACD warm-up needs 35) so an invalid config never yields
// a degenerate MACD.
func SetMinCandles(n int) {
	if n < 35 {
		return
	}
	featureMu.Lock()
	minCandlesVar = n
	featureMu.Unlock()
}

// SetFlatThreshold overrides the forward-return flat band (%). Negative values are
// ignored; 0 is allowed (every nonzero return becomes up/down).
func SetFlatThreshold(v float64) {
	if v < 0 {
		return
	}
	featureMu.Lock()
	flatThreshold = v
	featureMu.Unlock()
}

func getMinCandles() int {
	featureMu.RLock()
	defer featureMu.RUnlock()
	return minCandlesVar
}

func getFlatThreshold() float64 {
	featureMu.RLock()
	defer featureMu.RUnlock()
	return flatThreshold
}

// Features holds all numeric features for one snapshot at time t.
// All values are computed from candles with timestamp <= t.
type Features struct {
	// RSI (14-period)
	RSI float64 `json:"rsi"`

	// MACD (12/26/9)
	MACDValue     float64 `json:"macd_value"`
	MACDSignal    float64 `json:"macd_signal"`
	MACDHistogram float64 `json:"macd_histogram"`

	// Bollinger Bands (20-period, 2σ)
	BBUpper    float64 `json:"bb_upper"`
	BBMiddle   float64 `json:"bb_middle"`
	BBLower    float64 `json:"bb_lower"`
	BBWidth    float64 `json:"bb_width"`    // (upper-lower)/middle — relative band width
	BBPosition float64 `json:"bb_position"` // (close-lower)/(upper-lower) in [0,1]; >1 or <0 = outside

	// EMA (20-period)
	EMA20      float64 `json:"ema20"`
	PriceVsEMA float64 `json:"price_vs_ema"` // (close - EMA20) / EMA20 * 100 (%)

	// ATR (14-period) — absolute and relative
	ATR    float64 `json:"atr"`
	ATRPct float64 `json:"atr_pct"` // ATR / close * 100 (%)

	// Volume
	Volume      float64 `json:"volume"`
	VolumeRatio float64 `json:"volume_ratio"` // current / 20-period avg; >1 = above avg

	// Trend (derived from EMA slope and candle body)
	TrendSlope float64 `json:"trend_slope"` // (EMA20[t] - EMA20[t-5]) / EMA20[t-5] * 100

	// Hour of day UTC (0-23) — captures intraday seasonality
	HourUTC int `json:"hour_utc"`

	// Candle microstructure
	BodyPct   float64 `json:"body_pct"`   // |close-open| / (high-low); 0 = doji, 1 = no wicks
	UpperWick float64 `json:"upper_wick"` // (high - max(open,close)) / (high-low)
	LowerWick float64 `json:"lower_wick"` // (min(open,close) - low) / (high-low)
	Bullish   int     `json:"bullish"`    // 1 if close > open, 0 otherwise
}

// Candle is a minimal OHLCV snapshot (mirrors ai.Candle to avoid import cycles).
type Candle struct {
	Open    float64
	High    float64
	Low     float64
	Close   float64
	Volume  float64
	HourUTC int // extracted from candle timestamp by the caller
}

// ExtractFeatures computes all features from the candle window ending at the last
// element. The caller must ensure the slice contains ONLY candles at or before t
// and is sorted ascending by timestamp (no lookahead whatsoever).
//
// Returns nil if fewer than 35 candles are provided (minimum for MACD warm-up).
func ExtractFeatures(candles []Candle) *Features {
	if len(candles) < getMinCandles() {
		return nil
	}

	closes := make([]float64, len(candles))
	highs := make([]float64, len(candles))
	lows := make([]float64, len(candles))
	vols := make([]float64, len(candles))
	for i, c := range candles {
		closes[i] = c.Close
		highs[i] = c.High
		lows[i] = c.Low
		vols[i] = c.Volume
	}

	last := candles[len(candles)-1]
	f := &Features{HourUTC: last.HourUTC}

	// RSI (14)
	f.RSI = calcRSI(closes, 14)

	// MACD (12/26/9)
	f.MACDValue, f.MACDSignal, f.MACDHistogram = calcMACD(closes, 12, 26, 9)

	// EMA20
	ema20s := calcEMASeries(closes, 20)
	f.EMA20 = ema20s[len(ema20s)-1]
	if f.EMA20 != 0 {
		f.PriceVsEMA = (last.Close - f.EMA20) / f.EMA20 * 100
	}

	// EMA slope: compare EMA20 now vs 5 bars ago
	if len(ema20s) >= 6 {
		prev5 := ema20s[len(ema20s)-6]
		if prev5 != 0 {
			f.TrendSlope = (f.EMA20 - prev5) / prev5 * 100
		}
	}

	// Bollinger Bands (20, 2σ)
	f.BBUpper, f.BBMiddle, f.BBLower = calcBollinger(closes, 20, 2.0)
	bandRange := f.BBUpper - f.BBLower
	if f.BBMiddle != 0 {
		f.BBWidth = bandRange / f.BBMiddle * 100
	}
	if bandRange != 0 {
		f.BBPosition = (last.Close - f.BBLower) / bandRange
	}

	// ATR (14)
	f.ATR = calcATR(highs, lows, closes, 14)
	if last.Close != 0 {
		f.ATRPct = f.ATR / last.Close * 100
	}

	// Volume ratio (current / 20-period avg)
	f.Volume = last.Volume
	if len(vols) >= 20 {
		avg := mean(vols[len(vols)-20:])
		if avg != 0 {
			f.VolumeRatio = last.Volume / avg
		}
	}

	// Candle microstructure
	rng := last.High - last.Low
	if rng > 0 {
		f.BodyPct = math.Abs(last.Close-last.Open) / rng
		upperWickTop := math.Max(last.Open, last.Close)
		lowerWickBot := math.Min(last.Open, last.Close)
		f.UpperWick = (last.High - upperWickTop) / rng
		f.LowerWick = (lowerWickBot - last.Low) / rng
	}
	if last.Close > last.Open {
		f.Bullish = 1
	}

	return f
}

// ForwardLabel computes the outcome labels for a snapshot at position idx within
// the candle slice, looking forward horizonBars candles.
//
// Anti-leakage: this function MUST only be called during batch labeling, never
// during live feature extraction. The caller is responsible for ensuring idx is
// in the train/label split (not the test window).
//
// sl and tp are percentage distances from the entry price (e.g. 0.8 = 0.8%).
// Returns (fwdReturn%, hitTP, hitSL, direction) or zero values if horizonBars
// extends beyond the slice.
func ForwardLabel(candles []Candle, idx int, horizonBars int, slPct, tpPct float64) (fwdReturn float64, hitTP, hitSL bool, direction string) {
	if idx+horizonBars >= len(candles) {
		return 0, false, false, ""
	}
	entry := candles[idx].Close
	if entry == 0 {
		return 0, false, false, ""
	}

	slPrice := entry * (1 - slPct/100)
	tpPrice := entry * (1 + tpPct/100)

	for i := idx + 1; i <= idx+horizonBars; i++ {
		c := candles[i]
		if c.Low <= slPrice {
			hitSL = true
			fwdReturn = (slPrice - entry) / entry * 100
			direction = "down"
			return
		}
		if c.High >= tpPrice {
			hitTP = true
			fwdReturn = (tpPrice - entry) / entry * 100
			direction = "up"
			return
		}
	}

	// Neither hit: use close at horizon
	endClose := candles[idx+horizonBars].Close
	fwdReturn = (endClose - entry) / entry * 100
	ft := getFlatThreshold()
	switch {
	case fwdReturn > ft:
		direction = "up"
	case fwdReturn < -ft:
		direction = "down"
	default:
		direction = "flat"
	}
	return
}

// ---- pure math helpers (no external deps, deterministic) ----

func calcRSI(closes []float64, period int) float64 {
	if len(closes) <= period {
		return 50
	}
	gains, losses := 0.0, 0.0
	for i := len(closes) - period; i < len(closes); i++ {
		d := closes[i] - closes[i-1]
		if d > 0 {
			gains += d
		} else {
			losses -= d
		}
	}
	if losses == 0 {
		return 100
	}
	rs := (gains / float64(period)) / (losses / float64(period))
	return 100 - 100/(1+rs)
}

// calcEMASeries returns the full EMA series for the given period.
func calcEMASeries(closes []float64, period int) []float64 {
	out := make([]float64, len(closes))
	if len(closes) == 0 {
		return out
	}
	k := 2.0 / float64(period+1)
	out[0] = closes[0]
	for i := 1; i < len(closes); i++ {
		out[i] = closes[i]*k + out[i-1]*(1-k)
	}
	return out
}

func calcMACD(closes []float64, fast, slow, signal int) (macdVal, signalVal, histogram float64) {
	if len(closes) < slow+signal {
		return 0, 0, 0
	}
	fastEMA := calcEMASeries(closes, fast)
	slowEMA := calcEMASeries(closes, slow)
	macdLine := make([]float64, len(closes))
	for i := range closes {
		macdLine[i] = fastEMA[i] - slowEMA[i]
	}
	signalLine := calcEMASeries(macdLine, signal)
	last := len(closes) - 1
	macdVal = macdLine[last]
	signalVal = signalLine[last]
	histogram = macdVal - signalVal
	return
}

func calcBollinger(closes []float64, period int, stdDevMult float64) (upper, middle, lower float64) {
	if len(closes) < period {
		if len(closes) > 0 {
			c := closes[len(closes)-1]
			return c, c, c
		}
		return 0, 0, 0
	}
	window := closes[len(closes)-period:]
	middle = mean(window)
	sd := stdDev(window, middle)
	upper = middle + stdDevMult*sd
	lower = middle - stdDevMult*sd
	return
}

func calcATR(highs, lows, closes []float64, period int) float64 {
	n := len(closes)
	if n <= 1 {
		return 0
	}
	trs := make([]float64, n-1)
	for i := 1; i < n; i++ {
		hl := highs[i] - lows[i]
		hcp := math.Abs(highs[i] - closes[i-1])
		lcp := math.Abs(lows[i] - closes[i-1])
		trs[i-1] = math.Max(hl, math.Max(hcp, lcp))
	}
	if len(trs) < period {
		return mean(trs)
	}
	return mean(trs[len(trs)-period:])
}

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
