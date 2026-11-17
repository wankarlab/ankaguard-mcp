package backtest

import (
	"github.com/rantipay/trading-mcp/internal/ai"
	ind "github.com/rantipay/trading-mcp/internal/indicators"
)

// computeIndicators derives the technical indicators the DecisionEngine expects
// (RSI, MACD, Bollinger, ATR, EMA20, Volume) purely from a candle slice — no
// network. The live path fetches these via the IndicatorCalculator's client;
// for backtesting we must compute them from the historical window so replayed
// decisions match what the live bot would have seen. Always returns non-nil so
// the DecisionEngine never nil-derefs primaryCtx.Indicators.
func computeIndicators(candles []ai.Candle) *ai.Indicators {
	out := &ai.Indicators{}
	if len(candles) < 30 {
		return out // not warmed up; non-nil zero value
	}
	closes := make([]float64, len(candles))
	highs := make([]float64, len(candles))
	lows := make([]float64, len(candles))
	for i, c := range candles {
		closes[i] = c.Close
		highs[i] = c.High
		lows[i] = c.Low
	}
	out.RSI = ind.CalculateRSI(closes, 14)
	out.Volume = candles[len(candles)-1].Volume

	if m, err := ind.MACD(closes, 12, 26, 9); err == nil && len(m.MACD) > 0 {
		macd := &ai.MACD{Value: last(m.MACD), Signal: last(m.Signal), Histogram: last(m.Histogram)}
		switch {
		case macd.Histogram > 0:
			macd.Trend = "bullish"
		case macd.Histogram < 0:
			macd.Trend = "bearish"
		default:
			macd.Trend = "neutral"
		}
		out.MACD = macd
	}

	if b, err := ind.BollingerBandsCalculate(closes, 20, 2.0); err == nil && len(b.Middle) > 0 {
		bb := &ai.Bollinger{Upper: last(b.Upper), Middle: last(b.Middle), Lower: last(b.Lower)}
		if bb.Middle != 0 {
			bb.Width = (bb.Upper - bb.Lower) / bb.Middle
		}
		price := closes[len(closes)-1]
		switch {
		case price > bb.Upper:
			bb.Position = "outside_upper"
		case price < bb.Lower:
			bb.Position = "outside_lower"
		case price >= bb.Middle:
			bb.Position = "upper"
		default:
			bb.Position = "lower"
		}
		out.Bollinger = bb
	}

	if a, err := ind.ATR(highs, lows, closes, 14, false); err == nil && len(a) > 0 {
		out.ATR = last(a)
	}
	if e, err := ind.EMA(closes, 20); err == nil && len(e) > 0 {
		out.EMA20 = last(e)
	}
	return out
}

// last returns the final element of a slice, or 0 if empty.
func last(s []float64) float64 {
	if len(s) == 0 {
		return 0
	}
	return s[len(s)-1]
}
