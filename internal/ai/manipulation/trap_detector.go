package manipulation

import (
	"context"
	"fmt"
	"math"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/ai"
	dynamic "github.com/rantipay/trading-mcp/internal/config/dynamic"
)

type Candle = ai.Candle

type TrapType string

const (
	TrapNone      TrapType = "NONE"
	TrapBull      TrapType = "BULL_TRAP"
	TrapBear      TrapType = "BEAR_TRAP"
	TrapStopHunt  TrapType = "STOP_HUNT"
	TrapLiquidity TrapType = "LIQUIDITY_GRAB"
)

const (
	ActionAvoid            = "AVOID"
	ActionFadeTheMove      = "FADE_THE_MOVE"
	ActionWaitConfirmation = "WAIT_CONFIRMATION"
	ActionClear            = "CLEAR"
)

type TrapAnalysis struct {
	Detected        bool
	TrapType        TrapType
	Confidence      float64
	ReversalPercent float64
	VolumeAtTrap    float64
	AvgVolume       float64
	VolumeRatio     float64
	SuggestedAction string
	Reasoning       string
	AtCandle        int
}

type Detector struct {
	log                   *logrus.Logger
	minReversal           float64
	lookbackVolume        int
	suspiciousVolumeRatio float64
	wickToBodyRatio       float64
}

func NewDetector(log *logrus.Logger) *Detector {
	if log == nil {
		log = logrus.New()
	}
	return &Detector{
		log:                   log,
		minReversal:           0.5,
		lookbackVolume:        20,
		suspiciousVolumeRatio: 0.7,
		wickToBodyRatio:       2.5,
	}
}

func emptyAnalysis(reason string) *TrapAnalysis {
	return &TrapAnalysis{
		Detected:        false,
		TrapType:        TrapNone,
		Confidence:      0,
		SuggestedAction: ActionClear,
		Reasoning:       reason,
		AtCandle:        -1,
	}
}

// AnalyzeRecent scans the last few candles for trap patterns and returns the strongest one.
// TC-4.3: reads dynamic config thresholds from the default store (context.Background() fallback).
func (d *Detector) AnalyzeRecent(candles []Candle) *TrapAnalysis {
	// TC-4.3: pull a snapshot from the global default store (non-nil when app is initialised).
	cfg := dynamic.SnapshotFromContext(context.Background())

	// Read scan_window from dynamic config; fallback to 5
	scanWindow := 5
	if cfg != nil {
		if v, ok := cfg.IntOk("trap.scan_window"); ok {
			scanWindow = v
		}
	}
	// Update detector fields from dynamic config for this call so sub-methods pick up live values.
	if cfg != nil {
		if v, ok := cfg.FloatOk("trap.min_reversal"); ok {
			d.minReversal = v
		}
		if v, ok := cfg.IntOk("trap.volume.lookback"); ok {
			d.lookbackVolume = v
		}
		if v, ok := cfg.FloatOk("trap.volume.suspicious_ratio"); ok {
			d.suspiciousVolumeRatio = v
		}
		if v, ok := cfg.FloatOk("trap.wick_to_body_ratio"); ok {
			d.wickToBodyRatio = v
		}
	}

	if len(candles) < 5 {
		return emptyAnalysis("insufficient data")
	}

	if len(candles) < 12 {
		scanWindow = len(candles) - 2
	}
	startIdx := len(candles) - scanWindow
	if startIdx < 1 {
		startIdx = 1
	}

	var best *TrapAnalysis
	for i := startIdx; i < len(candles); i++ {
		analysis := d.isLikelyTrapWithCfg(i, candles, cfg)
		if analysis == nil || !analysis.Detected {
			continue
		}
		if best == nil || analysis.Confidence > best.Confidence {
			best = analysis
		}
	}

	if best == nil {
		clean := emptyAnalysis("no manipulation patterns detected")
		clean.AvgVolume = avgVolume(candles, len(candles)-1, d.lookbackVolume)
		return clean
	}

	d.log.WithFields(logrus.Fields{
		"trap_type":  best.TrapType,
		"confidence": best.Confidence,
		"at_candle":  best.AtCandle,
		"action":     best.SuggestedAction,
	}).Debug("manipulation pattern detected")

	return best
}

// IsLikelyTrap evaluates whether the candle at candleIdx represents a trap.
func (d *Detector) IsLikelyTrap(candleIdx int, candles []Candle) *TrapAnalysis {
	return d.isLikelyTrapWithCfg(candleIdx, candles, nil)
}

// isLikelyTrapWithCfg is the cfg-aware variant used internally.
// TC-4.3: reads per-detect-method thresholds from cfg when non-nil.
func (d *Detector) isLikelyTrapWithCfg(candleIdx int, candles []Candle, cfg *dynamic.ScopedSnapshot) *TrapAnalysis {
	if len(candles) < 5 {
		return emptyAnalysis("insufficient data")
	}
	if candleIdx < 1 || candleIdx >= len(candles) {
		return emptyAnalysis("invalid candle index")
	}

	avgVol := avgVolume(candles, candleIdx-1, d.lookbackVolume)
	current := candles[candleIdx]
	volRatio := 0.0
	if avgVol > 0 {
		volRatio = current.Volume / avgVol
	}

	if a := d.detectLiquidityGrabCfg(candleIdx, candles, avgVol, volRatio, cfg); a != nil {
		return a
	}
	if a := d.detectStopHuntCfg(candleIdx, candles, avgVol, volRatio, cfg); a != nil {
		return a
	}
	if a := d.detectBullTrapCfg(candleIdx, candles, avgVol, volRatio, cfg); a != nil {
		return a
	}
	if a := d.detectBearTrapCfg(candleIdx, candles, avgVol, volRatio, cfg); a != nil {
		return a
	}

	clean := emptyAnalysis("no manipulation pattern at candle")
	clean.AtCandle = candleIdx
	clean.VolumeAtTrap = current.Volume
	clean.AvgVolume = avgVol
	clean.VolumeRatio = volRatio
	return clean
}

func (d *Detector) detectBullTrap(idx int, candles []Candle, avgVol, volRatio float64) *TrapAnalysis {
	return d.detectBullTrapCfg(idx, candles, avgVol, volRatio, nil)
}

// TC-4.3: detectBullTrapCfg reads bull.lookback and bull.base_confidence from cfg.
func (d *Detector) detectBullTrapCfg(idx int, candles []Candle, avgVol, volRatio float64, cfg *dynamic.ScopedSnapshot) *TrapAnalysis {
	if idx+1 >= len(candles) {
		return nil
	}

	lookback := 10
	baseConfidence := 0.4
	if cfg != nil {
		if v, ok := cfg.IntOk("trap.bull.lookback"); ok {
			lookback = v
		}
		if v, ok := cfg.FloatOk("trap.bull.base_confidence"); ok {
			baseConfidence = v
		}
	}
	start := idx - lookback
	if start < 0 {
		start = 0
	}
	if start >= idx {
		return nil
	}

	priorHigh := candles[start].High
	priorLow := candles[start].Low
	for i := start; i < idx; i++ {
		if candles[i].High > priorHigh {
			priorHigh = candles[i].High
		}
		if candles[i].Low < priorLow {
			priorLow = candles[i].Low
		}
	}

	current := candles[idx]
	next := candles[idx+1]

	if current.High <= priorHigh {
		return nil
	}
	if volRatio >= d.suspiciousVolumeRatio {
		return nil
	}
	if next.Close >= priorHigh {
		return nil
	}

	moveRange := current.High - priorLow
	if moveRange <= 0 {
		return nil
	}
	reversal := (current.High - next.Close) / moveRange
	if reversal < d.minReversal {
		return nil
	}

	confidence := computeConfidence(reversal, d.suspiciousVolumeRatio-volRatio, baseConfidence)
	return &TrapAnalysis{
		Detected:        true,
		TrapType:        TrapBull,
		Confidence:      confidence,
		ReversalPercent: reversal,
		VolumeAtTrap:    current.Volume,
		AvgVolume:       avgVol,
		VolumeRatio:     volRatio,
		SuggestedAction: pickActionWithCfg(TrapBull, confidence, cfg),
		Reasoning: fmt.Sprintf("false breakout above %.4f with low volume (ratio %.2f) and %.1f%% reversal",
			priorHigh, volRatio, reversal*100),
		AtCandle: idx,
	}
}

func (d *Detector) detectBearTrap(idx int, candles []Candle, avgVol, volRatio float64) *TrapAnalysis {
	return d.detectBearTrapCfg(idx, candles, avgVol, volRatio, nil)
}

// TC-4.3: detectBearTrapCfg reads bear.lookback and bear.base_confidence from cfg.
func (d *Detector) detectBearTrapCfg(idx int, candles []Candle, avgVol, volRatio float64, cfg *dynamic.ScopedSnapshot) *TrapAnalysis {
	if idx+1 >= len(candles) {
		return nil
	}

	lookback := 10
	baseConfidence := 0.4
	if cfg != nil {
		if v, ok := cfg.IntOk("trap.bear.lookback"); ok {
			lookback = v
		}
		if v, ok := cfg.FloatOk("trap.bear.base_confidence"); ok {
			baseConfidence = v
		}
	}
	start := idx - lookback
	if start < 0 {
		start = 0
	}
	if start >= idx {
		return nil
	}

	priorLow := candles[start].Low
	priorHigh := candles[start].High
	for i := start; i < idx; i++ {
		if candles[i].Low < priorLow {
			priorLow = candles[i].Low
		}
		if candles[i].High > priorHigh {
			priorHigh = candles[i].High
		}
	}

	current := candles[idx]
	next := candles[idx+1]

	if current.Low >= priorLow {
		return nil
	}
	if volRatio >= d.suspiciousVolumeRatio {
		return nil
	}
	if next.Close <= priorLow {
		return nil
	}

	moveRange := priorHigh - current.Low
	if moveRange <= 0 {
		return nil
	}
	reversal := (next.Close - current.Low) / moveRange
	if reversal < d.minReversal {
		return nil
	}

	confidence := computeConfidence(reversal, d.suspiciousVolumeRatio-volRatio, baseConfidence)
	return &TrapAnalysis{
		Detected:        true,
		TrapType:        TrapBear,
		Confidence:      confidence,
		ReversalPercent: reversal,
		VolumeAtTrap:    current.Volume,
		AvgVolume:       avgVol,
		VolumeRatio:     volRatio,
		SuggestedAction: pickActionWithCfg(TrapBear, confidence, cfg),
		Reasoning: fmt.Sprintf("false breakdown below %.4f with low volume (ratio %.2f) and %.1f%% reversal",
			priorLow, volRatio, reversal*100),
		AtCandle: idx,
	}
}

func (d *Detector) detectStopHunt(idx int, candles []Candle, avgVol, volRatio float64) *TrapAnalysis {
	return d.detectStopHuntCfg(idx, candles, avgVol, volRatio, nil)
}

// TC-4.3: detectStopHuntCfg reads stop_hunt.vol_ratio_min and close_to_open_ratio from cfg.
func (d *Detector) detectStopHuntCfg(idx int, candles []Candle, avgVol, volRatio float64, cfg *dynamic.ScopedSnapshot) *TrapAnalysis {
	if idx+1 >= len(candles) {
		return nil
	}

	volRatioMin := 1.0
	closeToOpenRatio := 0.5
	if cfg != nil {
		if v, ok := cfg.FloatOk("trap.stop_hunt.vol_ratio_min"); ok {
			volRatioMin = v
		}
		if v, ok := cfg.FloatOk("trap.stop_hunt.close_to_open_ratio"); ok {
			closeToOpenRatio = v
		}
	}

	current := candles[idx]
	next := candles[idx+1]

	body := math.Abs(current.Close - current.Open)
	if body <= 0 {
		body = 1e-9
	}
	upperWick := current.High - math.Max(current.Open, current.Close)
	lowerWick := math.Min(current.Open, current.Close) - current.Low

	upperRatio := upperWick / body
	lowerRatio := lowerWick / body

	if volRatio < volRatioMin {
		return nil
	}

	totalRange := current.High - current.Low
	if totalRange <= 0 {
		return nil
	}

	var (
		isHunt    bool
		direction string
		reversal  float64
	)

	if upperRatio >= d.wickToBodyRatio && upperRatio > lowerRatio {
		// price spiked up to grab stops, then closed back near open
		closeNearOpen := math.Abs(next.Close-current.Open) <= closeToOpenRatio*body
		retraced := next.Close < current.High
		if closeNearOpen && retraced {
			isHunt = true
			direction = "upper"
			reversal = (current.High - next.Close) / totalRange
		}
	} else if lowerRatio >= d.wickToBodyRatio && lowerRatio > upperRatio {
		closeNearOpen := math.Abs(next.Close-current.Open) <= closeToOpenRatio*body
		retraced := next.Close > current.Low
		if closeNearOpen && retraced {
			isHunt = true
			direction = "lower"
			reversal = (next.Close - current.Low) / totalRange
		}
	}

	if !isHunt || reversal < d.minReversal {
		return nil
	}

	wickStrength := math.Max(upperRatio, lowerRatio) / d.wickToBodyRatio
	confidence := computeConfidence(reversal, math.Min(wickStrength-1, 1.0), math.Min(volRatio-1, 1.0))

	return &TrapAnalysis{
		Detected:        true,
		TrapType:        TrapStopHunt,
		Confidence:      confidence,
		ReversalPercent: reversal,
		VolumeAtTrap:    current.Volume,
		AvgVolume:       avgVol,
		VolumeRatio:     volRatio,
		SuggestedAction: pickActionWithCfg(TrapStopHunt, confidence, cfg),
		Reasoning: fmt.Sprintf("stop hunt on %s wick (wick/body %.2f, vol ratio %.2f, reversal %.1f%%)",
			direction, math.Max(upperRatio, lowerRatio), volRatio, reversal*100),
		AtCandle: idx,
	}
}

func (d *Detector) detectLiquidityGrab(idx int, candles []Candle, avgVol, volRatio float64) *TrapAnalysis {
	return d.detectLiquidityGrabCfg(idx, candles, avgVol, volRatio, nil)
}

// TC-4.3: detectLiquidityGrabCfg reads liquidity.wick_ratio_min and vol_ratio_min from cfg.
func (d *Detector) detectLiquidityGrabCfg(idx int, candles []Candle, avgVol, volRatio float64, cfg *dynamic.ScopedSnapshot) *TrapAnalysis {
	if idx-1 < 0 {
		return nil
	}

	wickRatioMin := 3.0
	liqVolRatioMin := 2.0
	if cfg != nil {
		if v, ok := cfg.FloatOk("trap.liquidity.wick_ratio_min"); ok {
			wickRatioMin = v
		}
		if v, ok := cfg.FloatOk("trap.liquidity.vol_ratio_min"); ok {
			liqVolRatioMin = v
		}
	}

	current := candles[idx]
	body := math.Abs(current.Close - current.Open)
	if body <= 0 {
		body = 1e-9
	}
	upperWick := current.High - math.Max(current.Open, current.Close)
	lowerWick := math.Min(current.Open, current.Close) - current.Low
	maxWick := math.Max(upperWick, lowerWick)
	wickRatio := maxWick / body

	if wickRatio < wickRatioMin {
		return nil
	}
	if volRatio < liqVolRatioMin {
		return nil
	}

	prev := candles[idx-1]
	prevHigh := math.Max(prev.Open, prev.Close)
	prevLow := math.Min(prev.Open, prev.Close)
	closesInsidePrev := current.Close <= prevHigh && current.Close >= prevLow
	if !closesInsidePrev {
		// allow soft inside if range overlaps
		if current.Close > prev.High || current.Close < prev.Low {
			return nil
		}
	}

	// no directional continuation in following candles
	noContinuation := true
	if idx+1 < len(candles) {
		next := candles[idx+1]
		if upperWick > lowerWick && next.Close > current.High {
			noContinuation = false
		}
		if lowerWick > upperWick && next.Close < current.Low {
			noContinuation = false
		}
	}
	if !noContinuation {
		return nil
	}

	totalRange := current.High - current.Low
	if totalRange <= 0 {
		return nil
	}
	reversal := maxWick / totalRange

	confidence := computeConfidence(reversal, math.Min(wickRatio/4.0, 1.0), math.Min(volRatio/3.0, 1.0))

	return &TrapAnalysis{
		Detected:        true,
		TrapType:        TrapLiquidity,
		Confidence:      confidence,
		ReversalPercent: reversal,
		VolumeAtTrap:    current.Volume,
		AvgVolume:       avgVol,
		VolumeRatio:     volRatio,
		SuggestedAction: pickActionWithCfg(TrapLiquidity, confidence, cfg),
		Reasoning: fmt.Sprintf("liquidity grab: wick/body %.2f, volume %.2fx avg, closed inside prior range",
			wickRatio, volRatio),
		AtCandle: idx,
	}
}

func avgVolume(candles []Candle, endIdx, lookback int) float64 {
	if endIdx < 0 || len(candles) == 0 {
		return 0
	}
	if endIdx >= len(candles) {
		endIdx = len(candles) - 1
	}
	start := endIdx - lookback + 1
	if start < 0 {
		start = 0
	}
	count := 0
	sum := 0.0
	for i := start; i <= endIdx; i++ {
		sum += candles[i].Volume
		count++
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

func computeConfidence(parts ...float64) float64 {
	if len(parts) == 0 {
		return 0
	}
	sum := 0.0
	for _, p := range parts {
		if p < 0 {
			p = 0
		}
		if p > 1 {
			p = 1
		}
		sum += p
	}
	conf := sum / float64(len(parts))
	if conf > 1 {
		conf = 1
	}
	if conf < 0 {
		conf = 0
	}
	return conf
}

func pickAction(trap TrapType, confidence float64) string {
	return pickActionWithCfg(trap, confidence, nil)
}

// TC-4.3: pickActionWithCfg reads trap.action.fade_confidence_min from cfg.
func pickActionWithCfg(trap TrapType, confidence float64, cfg *dynamic.ScopedSnapshot) string {
	fadeMin := 0.7 // default
	if cfg != nil {
		if v, ok := cfg.FloatOk("trap.action.fade_confidence_min"); ok {
			fadeMin = v
		}
	}
	switch trap {
	case TrapBull, TrapBear:
		if confidence > fadeMin {
			return ActionFadeTheMove
		}
		return ActionWaitConfirmation
	case TrapStopHunt:
		return ActionWaitConfirmation
	case TrapLiquidity:
		return ActionAvoid
	default:
		return ActionClear
	}
}
