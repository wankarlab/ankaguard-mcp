package ai

import (
	"context"
	"sync"

	dynamic "github.com/rantipay/trading-mcp/internal/config/dynamic"
	ta "github.com/rantipay/trading-mcp/internal/indicators"
	"github.com/sirupsen/logrus"
)

// WarmingStatus describes whether an indicator window is ready.
type WarmingStatus struct {
	Ready         bool
	NeededCandles int
	HaveCandles   int
}

// IndicatorClient interface for indicator calculations
type IndicatorClient interface {
	CalculateRSI(ctx context.Context, exchange, pair string, period int) (float64, error)
	CalculateMACD(ctx context.Context, exchange, pair string, fast, slow, signal int) (interface{}, error)
	CalculateBollingerBands(ctx context.Context, exchange, pair string, period int, stdDev float64) (interface{}, error)
}

// candleKey identifies a pair+timeframe candle count bucket.
type candleKey struct {
	pair      string
	timeframe string
}

// IndicatorCalculator calculates technical indicators
type IndicatorCalculator struct {
	client   IndicatorClient
	exchange string
	log      *logrus.Logger

	// TC-5.1: cache versioning via configHash
	mu          sync.RWMutex
	candleCounts map[candleKey]int // tracks how many candles seen per pair/tf (TC-5.4)

	// TC-5.2: subscription teardown
	unsubscribe func()
}

// NewIndicatorCalculator creates a new indicator calculator.
// cfgStore may be nil; when non-nil a subscription is wired for TC-5.2.
func NewIndicatorCalculator(client IndicatorClient, exchange string, log *logrus.Logger) *IndicatorCalculator {
	return &IndicatorCalculator{
		client:       client,
		exchange:     exchange,
		log:          log,
		candleCounts: make(map[candleKey]int),
	}
}

// AttachConfigStore wires a dynamic.ConfigStore subscription so that any change
// to the "indicators" group invalidates the indicator cache (TC-5.2).
// Safe to call multiple times — previous subscription is cleanly removed.
func (ic *IndicatorCalculator) AttachConfigStore(store *dynamic.ConfigStore) {
	if store == nil {
		return
	}
	// Detach previous subscription if any.
	if ic.unsubscribe != nil {
		ic.unsubscribe()
	}
	ic.unsubscribe = store.Subscribe("indicators", func(changes []dynamic.Change) {
		ic.log.WithField("changes", len(changes)).Info("indicator config changed — invalidating cache")
		ic.InvalidateAll()
	})
}

// InvalidateAll drops all cached candle counts, forcing a full recalculation on the
// next CalculateAll call (TC-5.1 / TC-5.2).
func (ic *IndicatorCalculator) InvalidateAll() {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	ic.candleCounts = make(map[candleKey]int)
}

// candleCount returns the number of candles recorded for pair/timeframe (TC-5.4).
func (ic *IndicatorCalculator) candleCount(pair, timeframe string) int {
	ic.mu.RLock()
	defer ic.mu.RUnlock()
	return ic.candleCounts[candleKey{pair, timeframe}]
}

// recordCandleCount updates the stored candle count for pair/timeframe.
func (ic *IndicatorCalculator) recordCandleCount(pair, timeframe string, n int) {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	ic.candleCounts[candleKey{pair, timeframe}] = n
}

// hasWarmedUp returns true when enough candles have been observed to fill the
// requested indicator window (TC-5.4).
func (ic *IndicatorCalculator) hasWarmedUp(pair, timeframe string, neededCandles int) bool {
	return ic.candleCount(pair, timeframe) >= neededCandles
}

// CheckWarming reports whether all indicator windows for pair are sufficiently
// filled with candle data. Call from DecisionEngine before EvaluateOpportunity
// to implement the TC-5.4 WAIT guard (TC-5.4).
func (ic *IndicatorCalculator) CheckWarming(ctx context.Context, pair string) WarmingStatus {
	cfg := dynamic.SnapshotFromContext(ctx)
	macdSlow := 26
	macdSignal := 9
	bbPeriod := 20
	rsiPeriod := 14
	if cfg != nil {
		if v := cfg.Int("indicators.macd.slow_period"); v != 0 {
			macdSlow = v
		}
		if v := cfg.Int("indicators.macd.signal_period"); v != 0 {
			macdSignal = v
		}
		if v := cfg.Int("indicators.bollinger.period"); v != 0 {
			bbPeriod = v
		}
		if v := cfg.Int("indicators.rsi.period"); v != 0 {
			rsiPeriod = v
		}
	}
	needed := macdSlow + macdSignal
	if bbPeriod > needed {
		needed = bbPeriod
	}
	if rsiPeriod > needed {
		needed = rsiPeriod
	}
	have := ic.candleCount(pair, "default")
	return WarmingStatus{
		Ready:         have >= needed,
		NeededCandles: needed,
		HaveCandles:   have,
	}
}

// CalculateAll calculates all indicators for a timeframe.
// Periods are read from the dynamic config snapshot carried in ctx (TC-5.3).
func (ic *IndicatorCalculator) CalculateAll(ctx context.Context, pair string, candles []Candle) (*Indicators, error) {
	// TC-5.3: read period params from dynamic snapshot with literal fallbacks.
	cfg := dynamic.SnapshotFromContext(ctx)

	rsiPeriod := 14
	rsiNeutralFallback := 50.0
	macdFast := 12
	macdSlow := 26
	macdSignal := 9
	macdHistogramZero := 0.0
	bbPeriod := 20
	bbStdDev := 2.0
	emaPeriod := 20

	if cfg != nil {
		if v := cfg.Int("indicators.rsi.period"); v != 0 {
			rsiPeriod = v
		}
		if v := cfg.Float("indicators.rsi.neutral_fallback"); v != 0 {
			rsiNeutralFallback = v
		}
		if v := cfg.Int("indicators.macd.fast_period"); v != 0 {
			macdFast = v
		}
		if v := cfg.Int("indicators.macd.slow_period"); v != 0 {
			macdSlow = v
		}
		if v := cfg.Int("indicators.macd.signal_period"); v != 0 {
			macdSignal = v
		}
		if v := cfg.Float("indicators.macd.histogram_zero"); v != 0 {
			macdHistogramZero = v
		}
		if v := cfg.Int("indicators.bollinger.period"); v != 0 {
			bbPeriod = v
		}
		if v := cfg.Float("indicators.bollinger.stddev"); v != 0 {
			bbStdDev = v
		}
		if v := cfg.Int("indicators.ema.default_period"); v != 0 {
			emaPeriod = v
		}
	}

	// TC-5.4: track candle counts per pair for the warming guard used by DecisionEngine.
	// neededCandles = largest window across all indicators.
	neededCandles := macdSlow + macdSignal // macd needs the most history
	if bbPeriod > neededCandles {
		neededCandles = bbPeriod
	}
	if rsiPeriod > neededCandles {
		neededCandles = rsiPeriod
	}
	ic.recordCandleCount(pair, "default", len(candles))
	_ = neededCandles // used via NeedCandles() / hasWarmedUp() from DecisionEngine

	result := &Indicators{}

	// Compute indicators from THIS timeframe's candles. The previous
	// remote-client path recalculated RSI/MACD/Bollinger on a fixed 1h
	// interval regardless of the timeframe under analysis, so every
	// "RSI 5m/15m" logged in entry decisions was a stale 1h value.
	closes := make([]float64, len(candles))
	for i := range candles {
		closes[i] = candles[i].Close
	}

	// Calculate RSI (Wilder smoothing)
	if len(closes) >= rsiPeriod+1 {
		result.RSI = ta.CalculateRSI(closes, rsiPeriod)
	} else {
		ic.log.Warnf("Insufficient candles for RSI(%d) on %s: %d — neutral fallback", rsiPeriod, pair, len(closes))
		result.RSI = rsiNeutralFallback
	}

	// Calculate MACD
	macdRes, macdErr := ta.MACD(closes, macdFast, macdSlow, macdSignal)
	if macdErr != nil || len(macdRes.Histogram) == 0 {
		ic.log.Warnf("Failed to calculate MACD for %s: %v", pair, macdErr)
		result.MACD = &MACD{Trend: "neutral"}
	} else {
		macd := &MACD{
			Value:     macdRes.MACD[len(macdRes.MACD)-1],
			Signal:    macdRes.Signal[len(macdRes.Signal)-1],
			Histogram: macdRes.Histogram[len(macdRes.Histogram)-1],
			Trend:     "neutral",
		}
		if macd.Histogram > macdHistogramZero {
			macd.Trend = "bullish"
		} else if macd.Histogram < macdHistogramZero {
			macd.Trend = "bearish"
		}
		result.MACD = macd
	}

	// Calculate Bollinger Bands
	bbRes, bbErr := ta.BollingerBandsCalculate(closes, bbPeriod, bbStdDev)
	if bbErr != nil || len(bbRes.Middle) == 0 {
		ic.log.Warnf("Failed to calculate Bollinger Bands for %s: %v", pair, bbErr)
		result.Bollinger = &Bollinger{Position: "middle"}
	} else {
		bb := &Bollinger{
			Upper:    bbRes.Upper[len(bbRes.Upper)-1],
			Middle:   bbRes.Middle[len(bbRes.Middle)-1],
			Lower:    bbRes.Lower[len(bbRes.Lower)-1],
			Position: "middle",
		}
		if bb.Middle != 0 {
			bb.Width = (bb.Upper - bb.Lower) / bb.Middle * 100
		}
		if len(candles) > 0 {
			currentPrice := candles[len(candles)-1].Close
			switch {
			case currentPrice > bb.Upper:
				bb.Position = "outside_upper"
			case currentPrice < bb.Lower:
				bb.Position = "outside_lower"
			case currentPrice > bb.Middle:
				bb.Position = "upper"
			default:
				bb.Position = "lower"
			}
		}
		result.Bollinger = bb
	}

	// Calculate EMA from candles using the configured period
	result.EMA20 = ic.calculateEMA(candles, emaPeriod)

	// Get volume
	if len(candles) > 0 {
		result.Volume = candles[len(candles)-1].Volume
	}

	return result, nil
}

// calculateEMA calculates Exponential Moving Average
func (ic *IndicatorCalculator) calculateEMA(candles []Candle, period int) float64 {
	if len(candles) < period {
		return 0
	}

	// Simple moving average for first value
	sum := 0.0
	for i := 0; i < period; i++ {
		sum += candles[i].Close
	}
	ema := sum / float64(period)

	// Calculate EMA
	multiplier := 2.0 / float64(period+1)
	for i := period; i < len(candles); i++ {
		ema = (candles[i].Close-ema)*multiplier + ema
	}

	return ema
}

// AnalyzeTrend analyzes trend from indicators
func (ic *IndicatorCalculator) AnalyzeTrend(indicators *Indicators, candles []Candle) (string, float64) {
	if len(candles) < 2 {
		return "sideways", 0.5
	}

	// RSI extreme thresholds from dynamic config (global scope fallback)
	rsiOversold := 30.0
	rsiOverbought := 70.0
	if cfg := dynamic.SnapshotFromContext(context.Background()); cfg != nil {
		if v := cfg.Float("indicators.trend.rsi_oversold"); v != 0 {
			rsiOversold = v
		}
		if v := cfg.Float("indicators.trend.rsi_overbought"); v != 0 {
			rsiOverbought = v
		}
	}

	bullishSignals := 0
	bearishSignals := 0
	totalSignals := 0

	// RSI analysis
	totalSignals++
	if indicators.RSI < rsiOversold {
		bullishSignals++ // oversold
	} else if indicators.RSI > rsiOverbought {
		bearishSignals++ // overbought
	}

	// MACD analysis
	totalSignals++
	if indicators.MACD.Trend == "bullish" {
		bullishSignals++
	} else if indicators.MACD.Trend == "bearish" {
		bearishSignals++
	}

	// Price vs EMA
	currentPrice := candles[len(candles)-1].Close
	if indicators.EMA20 > 0 {
		totalSignals++
		if currentPrice > indicators.EMA20 {
			bullishSignals++
		} else {
			bearishSignals++
		}
	}

	// Bollinger position
	totalSignals++
	switch indicators.Bollinger.Position {
	case "outside_lower", "lower":
		bullishSignals++
	case "outside_upper", "upper":
		bearishSignals++
	}

	// Determine trend and strength (more aggressive detection)
	strength := float64(bullishSignals) / float64(totalSignals)

	// Strong signals override (RSI extremes)
	if indicators.RSI > rsiOverbought {
		return "bearish", 0.7 // Overbought = bearish
	} else if indicators.RSI < rsiOversold {
		return "bullish", 0.7 // Oversold = bullish
	}

	if bullishSignals > bearishSignals {
		return "bullish", strength
	} else if bearishSignals > bullishSignals {
		return "bearish", 1.0 - strength
	}

	return "sideways", 0.5
}

// DetectKeyLevels detects support and resistance from candles.
// The lookback window is read from dynamic config (indicators.key_levels.lookback, TC-5.3).
func (ic *IndicatorCalculator) DetectKeyLevels(candles []Candle) *KeyLevels {
	// TC-5.3: read lookback from dynamic config
	lookback := 10
	cfg := dynamic.SnapshotFromContext(context.Background())
	if cfg != nil {
		if v := cfg.Int("indicators.key_levels.lookback"); v != 0 {
			lookback = v
		}
	}

	if len(candles) < lookback {
		return &KeyLevels{}
	}

	// Simple approach: recent high/low
	var high, low float64
	for i := len(candles) - lookback; i < len(candles); i++ {
		if candles[i].High > high || high == 0 {
			high = candles[i].High
		}
		if candles[i].Low < low || low == 0 {
			low = candles[i].Low
		}
	}

	currentPrice := candles[len(candles)-1].Close

	return &KeyLevels{
		Support:    low,
		Resistance: high,
		Distance: &LevelDistance{
			ToSupport:    (currentPrice - low) / currentPrice * 100,
			ToResistance: (high - currentPrice) / currentPrice * 100,
		},
	}
}
