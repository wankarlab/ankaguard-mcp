package ai

import (
	"fmt"
	"math"
)

// Price-action reader — the "see the chart like a human" entry brain.
//
// The legacy entry detection only checked a crude 2-candle colour flip
// (one green after one red). A human reading the same chart reads the *geometry*:
// long vs short bodies, rejection wicks, full-bodied marubozu candles with
// volume, indecision dojis, and multi-candle sequences (a pause then a candle
// that resumes the move). DetectPriceAction grades all of that into one signed,
// confidence-scaled signal that the setup evaluators fold into their score.
//
// Pure functions only — no config/network/bot deps — so the logic is unit-
// testable and deterministic. Thresholds arrive via PriceActionParams (filled
// from the dynamic config by the caller).

// PriceActionParams holds the tunable thresholds for the detector. The caller
// populates it from the dynamic config; Defaults() gives sensible baselines.
type PriceActionParams struct {
	// Marubozu: body must be at least this fraction of the candle's full range.
	MarubozuBodyRatio float64
	// Pin bar: rejection wick must be at least this multiple of the body.
	PinWickBodyRatio float64
	// Pin bar: the opposite (small) wick must be at most this fraction of range.
	PinOppositeWickMax float64
	// Doji: body at or below this fraction of range = indecision.
	DojiBodyRatio float64
	// Volume confirmation: current volume vs recent average.
	VolumeConfirmRatio float64
	// VolumeLookback candles used for the average-volume baseline.
	VolumeLookback int
	// Per-pattern base strengths (pre volume/size scaling), each in [0,1].
	EngulfingStrength    float64
	PinBarStrength       float64
	MarubozuStrength     float64
	ThreeCandleStrength  float64
	ReversalFlipStrength float64
}

// DefaultPriceActionParams returns baseline thresholds.
func DefaultPriceActionParams() PriceActionParams {
	return PriceActionParams{
		MarubozuBodyRatio:    0.80,
		PinWickBodyRatio:     2.0,
		PinOppositeWickMax:   0.25,
		DojiBodyRatio:        0.10,
		VolumeConfirmRatio:   1.2,
		VolumeLookback:       20,
		EngulfingStrength:    0.55,
		PinBarStrength:       0.50,
		MarubozuStrength:     0.45,
		ThreeCandleStrength:  0.60,
		ReversalFlipStrength: 0.20,
	}
}

// PriceActionSignal is the graded read of recent candle geometry.
type PriceActionSignal struct {
	Direction string   // "bullish" | "bearish" | "neutral"
	Strength  float64  // [0,1] net conviction toward Direction
	Patterns  []string // human-readable detected patterns (for logging/reasons)
}

// candleGeom holds the derived geometry of one candle.
type candleGeom struct {
	body      float64 // |close-open|
	rng       float64 // high-low
	upperWick float64
	lowerWick float64
	bullish   bool
	bodyRatio float64 // body/range; 0 = doji, 1 = marubozu
}

func geom(c Candle) candleGeom {
	g := candleGeom{
		body:    math.Abs(c.Close - c.Open),
		rng:     c.High - c.Low,
		bullish: c.Close >= c.Open,
	}
	top := c.Open
	bottom := c.Close
	if g.bullish {
		top = c.Close
		bottom = c.Open
	}
	g.upperWick = c.High - top
	g.lowerWick = bottom - c.Low
	if g.rng > 0 {
		g.bodyRatio = g.body / g.rng
	}
	return g
}

// DetectPriceAction reads the candle window (ascending, oldest→newest) and
// returns the net directional signal. Needs at least 3 candles; returns a
// neutral signal otherwise.
func DetectPriceAction(candles []Candle, p PriceActionParams) PriceActionSignal {
	out := PriceActionSignal{Direction: "neutral"}
	n := len(candles)
	if n < 3 {
		return out
	}

	last := candles[n-1]
	prev := candles[n-2]
	gLast := geom(last)
	gPrev := geom(prev)

	// Average volume baseline over the lookback (excluding the last candle).
	avgVol := averageVolume(candles, p.VolumeLookback)
	volConfirmed := avgVol > 0 && last.Volume >= p.VolumeConfirmRatio*avgVol

	// Net score: + bullish, - bearish. Each detector contributes a signed weight.
	var score float64
	add := func(dir int, strength float64, name string) {
		if strength <= 0 {
			return
		}
		score += float64(dir) * strength
		out.Patterns = append(out.Patterns, name)
	}

	// 1) Engulfing: last body fully covers prev body AND opposite colour. Strength
	//    scales with how decisively it engulfs (body ratio), boosted by volume.
	if gLast.body > 0 && gPrev.body > 0 && gLast.body >= gPrev.body && gLast.bullish != gPrev.bullish {
		engulfs := (gLast.bullish && last.Close >= prev.Open && last.Open <= prev.Close) ||
			(!gLast.bullish && last.Open >= prev.Close && last.Close <= prev.Open)
		if engulfs {
			s := p.EngulfingStrength * clamp01(gLast.body/(gPrev.body+1e-12)/2)
			if volConfirmed {
				s *= 1.25
			}
			dir := 1
			name := "bullish_engulfing"
			if !gLast.bullish {
				dir = -1
				name = "bearish_engulfing"
			}
			add(dir, clamp01(s), name)
		}
	}

	// 2) Pin bar / hammer (bullish) / shooting star (bearish): a long rejection
	//    wick with a small body and a small opposite wick.
	if gLast.rng > 0 && gLast.body > 0 {
		// Bullish hammer: long lower wick rejecting downside.
		if gLast.lowerWick >= p.PinWickBodyRatio*gLast.body &&
			gLast.upperWick <= p.PinOppositeWickMax*gLast.rng {
			s := p.PinBarStrength * clamp01(gLast.lowerWick/gLast.rng)
			add(1, clamp01(s), "hammer")
		}
		// Bearish shooting star: long upper wick rejecting upside.
		if gLast.upperWick >= p.PinWickBodyRatio*gLast.body &&
			gLast.lowerWick <= p.PinOppositeWickMax*gLast.rng {
			s := p.PinBarStrength * clamp01(gLast.upperWick/gLast.rng)
			add(-1, clamp01(s), "shooting_star")
		}
	}

	// 3) Marubozu: a near-full-body candle = strong conviction in its direction.
	//    Volume confirmation matters most here (a big body on big volume).
	if gLast.bodyRatio >= p.MarubozuBodyRatio {
		s := p.MarubozuStrength * clamp01(gLast.bodyRatio)
		if volConfirmed {
			s *= 1.3
		}
		dir := 1
		name := "bullish_marubozu"
		if !gLast.bullish {
			dir = -1
			name = "bearish_marubozu"
		}
		add(dir, clamp01(s), name)
	}

	// 4) Three-candle continuation (the operator's pattern): a pause — two small
	//    bodies after a clear move — then the latest candle resumes the prior
	//    direction. Reads as "the trend caught its breath and continued".
	if n >= 4 {
		if dir, ok := threeCandleContinuation(candles, p); ok {
			name := "three_candle_continuation_up"
			if dir < 0 {
				name = "three_candle_continuation_down"
			}
			s := p.ThreeCandleStrength
			if volConfirmed {
				s *= 1.15
			}
			add(dir, clamp01(s), name)
		}
	}

	// 5) Legacy colour flip (kept as a weak tie-breaker): green after red, etc.
	if gLast.bullish != gPrev.bullish && gLast.bodyRatio > p.DojiBodyRatio {
		dir := 1
		if !gLast.bullish {
			dir = -1
		}
		add(dir, p.ReversalFlipStrength, "reversal_flip")
	}

	// Doji on the last candle = indecision: damp the net conviction.
	if gLast.bodyRatio <= p.DojiBodyRatio {
		score *= 0.5
		out.Patterns = append(out.Patterns, "doji_indecision")
	}

	if score > 0 {
		out.Direction = "bullish"
	} else if score < 0 {
		out.Direction = "bearish"
	}
	out.Strength = clamp01(math.Abs(score))
	return out
}

// threeCandleContinuation detects: a directional candle, then two small-bodied
// "pause" candles, then a candle resuming the original direction. Returns the
// resumed direction (+1 up, -1 down) and ok=true when matched.
func threeCandleContinuation(candles []Candle, p PriceActionParams) (int, bool) {
	n := len(candles)
	if n < 4 {
		return 0, false
	}
	impulse := geom(candles[n-4]) // the move before the pause
	pauseA := geom(candles[n-3])
	pauseB := geom(candles[n-2])
	resume := geom(candles[n-1])

	// The impulse and resume must be decisive (non-doji) and same direction.
	if impulse.bodyRatio <= p.DojiBodyRatio || resume.bodyRatio <= p.DojiBodyRatio {
		return 0, false
	}
	if impulse.bullish != resume.bullish {
		return 0, false
	}
	// The two middle candles are a genuine pause: small bodies relative to the
	// impulse (consolidation), not a continuation themselves.
	if pauseA.body >= impulse.body || pauseB.body >= impulse.body {
		return 0, false
	}
	if resume.bullish {
		return 1, true
	}
	return -1, true
}

func averageVolume(candles []Candle, lookback int) float64 {
	n := len(candles)
	if n < 2 || lookback <= 0 {
		return 0
	}
	// Average the candles before the last one.
	start := n - 1 - lookback
	if start < 0 {
		start = 0
	}
	var sum float64
	var count int
	for i := start; i < n-1; i++ {
		sum += candles[i].Volume
		count++
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// Reason renders the signal for log/decision reasons.
func (s PriceActionSignal) Reason() string {
	if s.Direction == "neutral" || len(s.Patterns) == 0 {
		return "price-action: neutral"
	}
	return fmt.Sprintf("price-action %s (%.0f%%): %v", s.Direction, s.Strength*100, s.Patterns)
}
