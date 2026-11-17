package regime

import (
	"context"
	"fmt"
	"math"

	"github.com/rantipay/trading-mcp/internal/ai"
	dynamic "github.com/rantipay/trading-mcp/internal/config/dynamic"
	"github.com/rantipay/trading-mcp/internal/indicators"
	"github.com/sirupsen/logrus"
)

type Regime string

const (
	RegimeTrendingUp   Regime = "TRENDING_UP"
	RegimeTrendingDown Regime = "TRENDING_DOWN"
	RegimeRanging      Regime = "RANGING"
	RegimeBreakout     Regime = "BREAKOUT"
	RegimeChaos        Regime = "CHAOS"
	RegimeUnknown      Regime = "UNKNOWN"
	// RegimeQuietTrendUp/Down: a low-ADX, tight-BB read (would otherwise be
	// RANGING) that nonetheless shows a persistent, low-amplitude directional
	// drift — see classifyWithCfg's quiet-trend override.
	RegimeQuietTrendUp   Regime = "QUIET_TREND_UP"
	RegimeQuietTrendDown Regime = "QUIET_TREND_DOWN"
)

const (
	ActionLongOnly      = "OPERATE_LONG_ONLY"
	ActionShortOnly     = "OPERATE_SHORT_ONLY"
	ActionRange         = "OPERATE_RANGE"
	ActionBreakoutEntry = "BREAKOUT_ENTRY"
	ActionPause         = "PAUSE"
)

type RegimeAnalysis struct {
	Regime                  Regime
	Confidence              float64
	ADX                     float64
	BollingerWidth          float64
	ATRPercent              float64
	VolumeRatio             float64
	TrendDirection          string
	Reasoning               string
	SuggestedAction         string
	SuggestedSizeMultiplier float64
}

// TuningInterface allows the regime detector to read dynamic tuning parameters
// from an external store (e.g. TuningStore). The fallback value is used when
// the key is not present in the store or when no store is configured.
type TuningInterface interface {
	Get(key string, fallback float64) float64
}

type Detector struct {
	log    *logrus.Logger
	tuning TuningInterface // optional; nil → use fallback constants

	ADXTrendThreshold        float64
	ADXChaosThreshold        float64
	BBWidthRangeThreshold    float64
	BBWidthBreakoutFactor    float64
	ATRPercentChaosThreshold float64

	adxPeriod       int
	bbPeriod        int
	bbStdDev        float64
	atrPeriod       int
	emaPeriod       int
	volumeLookback  int
	bbWidthLookback int
}

// SetTuning wires a TuningInterface into the detector. Safe to call with nil
// to detach an existing store (reverts to hardcoded fallbacks).
func (d *Detector) SetTuning(t TuningInterface) {
	d.tuning = t
}

// getTuning returns the value for key from the tuning store, or fallback when
// no store is configured.
func (d *Detector) getTuning(key string, fallback float64) float64 {
	if d.tuning == nil {
		return fallback
	}
	return d.tuning.Get(key, fallback)
}

func NewDetector(log *logrus.Logger) *Detector {
	if log == nil {
		log = logrus.New()
	}
	return &Detector{
		log:                      log,
		ADXTrendThreshold:        25.0,
		ADXChaosThreshold:        50.0,
		BBWidthRangeThreshold:    0.02,
		BBWidthBreakoutFactor:    1.5,
		ATRPercentChaosThreshold: 0.05,
		adxPeriod:                14,
		bbPeriod:                 20,
		bbStdDev:                 2.0,
		atrPeriod:                14,
		emaPeriod:                20,
		volumeLookback:           20,
		bbWidthLookback:          20,
	}
}

func (d *Detector) Analyze(candles []ai.Candle) (*RegimeAnalysis, error) {
	// TC-4.4: pull dynamic config snapshot from global default store
	cfg := dynamic.SnapshotFromContext(context.Background())

	// TC-5.5: read indicator periods from dynamic config with struct-field fallbacks.
	adxPeriod := d.adxPeriod
	bbPeriod := d.bbPeriod
	bbStdDev := d.bbStdDev
	atrPeriod := d.atrPeriod
	emaPeriod := d.emaPeriod
	volumeLookback := d.volumeLookback
	bbWidthLookback := d.bbWidthLookback

	if cfg != nil {
		if v, ok := cfg.IntOk("indicators.regime.adx_period"); ok {
			adxPeriod = v
		}
		if v, ok := cfg.IntOk("indicators.regime.bb_period"); ok {
			bbPeriod = v
		}
		if v, ok := cfg.FloatOk("indicators.regime.bb_stddev"); ok {
			bbStdDev = v
		}
		if v, ok := cfg.IntOk("indicators.regime.atr_period"); ok {
			atrPeriod = v
		}
		if v, ok := cfg.IntOk("indicators.regime.ema_period"); ok {
			emaPeriod = v
		}
		if v, ok := cfg.IntOk("indicators.regime.volume_lookback"); ok {
			volumeLookback = v
		}
		if v, ok := cfg.IntOk("indicators.regime.bb_width_lookback"); ok {
			bbWidthLookback = v
		}
	}

	minRequired := adxPeriod*2 + 1
	if bbPeriod+bbWidthLookback > minRequired {
		minRequired = bbPeriod + bbWidthLookback
	}
	if len(candles) < minRequired {
		return nil, fmt.Errorf("insufficient candles: need at least %d, got %d", minRequired, len(candles))
	}

	highs := make([]float64, len(candles))
	lows := make([]float64, len(candles))
	closes := make([]float64, len(candles))
	volumes := make([]float64, len(candles))
	for i, c := range candles {
		highs[i] = c.High
		lows[i] = c.Low
		closes[i] = c.Close
		volumes[i] = c.Volume
	}

	currentPrice := closes[len(closes)-1]

	adxValue, diPlus, diMinus, err := d.computeADX(highs, lows, closes, adxPeriod)
	if err != nil {
		d.log.Warnf("ADX computation failed: %v", err)
	}

	bb, err := indicators.BollingerBandsCalculate(closes, bbPeriod, bbStdDev)
	if err != nil {
		return nil, fmt.Errorf("bollinger calculation failed: %w", err)
	}
	bbLen := len(bb.Middle)
	if bbLen == 0 {
		return nil, fmt.Errorf("bollinger output empty")
	}

	currentBBWidth := 0.0
	if bb.Middle[bbLen-1] != 0 {
		currentBBWidth = (bb.Upper[bbLen-1] - bb.Lower[bbLen-1]) / bb.Middle[bbLen-1]
	}

	// Pass local bbWidthLookback so averageBBWidth uses the dynamic value.
	avgBBWidth := d.averageBBWidthN(bb, bbWidthLookback)

	atrValues, err := indicators.ATR(highs, lows, closes, atrPeriod, true)
	atrPercent := 0.0
	if err != nil {
		d.log.Warnf("ATR computation failed: %v", err)
	} else if len(atrValues) > 0 && currentPrice > 0 {
		atrPercent = atrValues[len(atrValues)-1] / currentPrice
	}

	ema := d.calculateEMA(closes, emaPeriod)

	// Pass local volumeLookback so computeVolumeRatio uses the dynamic value.
	volumeRatio := d.computeVolumeRatioN(volumes, volumeLookback)

	// TC-4.4: read ema.trend_buffer_pct and di.crossover_buffer from dynamic config
	emaTrendBufferPct := 0.001
	diCrossoverBuffer := 5.0
	if cfg != nil {
		if v, ok := cfg.FloatOk("regime.ema.trend_buffer_pct"); ok {
			emaTrendBufferPct = v
		}
		if v, ok := cfg.FloatOk("regime.di.crossover_buffer"); ok {
			diCrossoverBuffer = v
		}
	}

	trendDirection := "neutral"
	if ema > 0 {
		if currentPrice > ema*(1.0+emaTrendBufferPct) {
			trendDirection = "up"
		} else if currentPrice < ema*(1.0-emaTrendBufferPct) {
			trendDirection = "down"
		}
	}
	if diPlus > diMinus+diCrossoverBuffer {
		trendDirection = "up"
	} else if diMinus > diPlus+diCrossoverBuffer {
		trendDirection = "down"
	}

	analysis := &RegimeAnalysis{
		ADX:            adxValue,
		BollingerWidth: currentBBWidth,
		ATRPercent:     atrPercent,
		VolumeRatio:    volumeRatio,
		TrendDirection: trendDirection,
	}

	d.classifyWithCfg(analysis, avgBBWidth, closes, cfg)

	d.log.Debugf("Regime analysis: regime=%s confidence=%.2f adx=%.2f bbWidth=%.4f atrPct=%.4f volRatio=%.2f trend=%s",
		analysis.Regime, analysis.Confidence, analysis.ADX, analysis.BollingerWidth,
		analysis.ATRPercent, analysis.VolumeRatio, analysis.TrendDirection)

	return analysis, nil
}

func (d *Detector) classify(a *RegimeAnalysis, avgBBWidth float64, closes []float64) {
	d.classifyWithCfg(a, avgBBWidth, closes, nil)
}

// TC-4.4: classifyWithCfg reads the 10 regime thresholds from cfg when non-nil.
func (d *Detector) classifyWithCfg(a *RegimeAnalysis, avgBBWidth float64, closes []float64, cfg *dynamic.ScopedSnapshot) {
	// TC-4.4: read all 10 non-indicator regime thresholds
	adxTrendThreshold := d.ADXTrendThreshold        // default from constructor (25.0)
	adxChaosThreshold := d.ADXChaosThreshold        // default (50.0)
	adxWeakThreshold := 20.0                        // default
	bbRangeThreshold := d.BBWidthRangeThreshold     // default (0.02)
	bbBreakoutFactor := d.BBWidthBreakoutFactor     // default (1.5)
	atrChaosThreshold := d.ATRPercentChaosThreshold // default (0.05)
	breakoutVolumeRatioMin := 1.3                   // default
	unknownConfidence := 0.3                        // default

	if cfg != nil {
		if v, ok := cfg.FloatOk("regime.adx.trend_threshold"); ok {
			adxTrendThreshold = v
		}
		if v, ok := cfg.FloatOk("regime.adx.chaos_threshold"); ok {
			adxChaosThreshold = v
		}
		if v, ok := cfg.FloatOk("regime.adx.weak_threshold"); ok {
			adxWeakThreshold = v
		}
		if v, ok := cfg.FloatOk("regime.bb.range_threshold"); ok {
			bbRangeThreshold = v
		}
		if v, ok := cfg.FloatOk("regime.bb.breakout_factor"); ok {
			bbBreakoutFactor = v
		}
		if v, ok := cfg.FloatOk("regime.atr.chaos_threshold"); ok {
			atrChaosThreshold = v
		}
		if v, ok := cfg.FloatOk("regime.breakout.volume_ratio_min"); ok {
			breakoutVolumeRatioMin = v
		}
		if v, ok := cfg.FloatOk("regime.unknown.confidence"); ok {
			unknownConfidence = v
		}
	}

	bbExpansion := 0.0
	if avgBBWidth > 0 {
		bbExpansion = a.BollingerWidth / avgBBWidth
	}

	if a.ADX > adxChaosThreshold && a.ATRPercent > atrChaosThreshold {
		a.Regime = RegimeChaos
		a.Confidence = math.Min(1.0, (a.ADX-adxChaosThreshold)/50.0+0.6)
		a.SuggestedAction = ActionPause
		a.SuggestedSizeMultiplier = d.getTuning("regime.chaos.size_multiplier", 0.00)
		a.Reasoning = fmt.Sprintf("Extreme volatility: ADX=%.1f (>%.0f) and ATR%%=%.2f%% (>%.2f%%)",
			a.ADX, adxChaosThreshold, a.ATRPercent*100, atrChaosThreshold*100)
		return
	}

	if bbExpansion >= bbBreakoutFactor && a.VolumeRatio >= breakoutVolumeRatioMin {
		a.Regime = RegimeBreakout
		a.Confidence = math.Min(1.0, (bbExpansion-1.0)/2.0+0.5)
		a.SuggestedSizeMultiplier = d.getTuning("regime.breakout.size_multiplier", 0.70)
		a.SuggestedAction = ActionBreakoutEntry
		a.Reasoning = fmt.Sprintf("Volatility expansion: BB width %.1fx avg, volume %.1fx avg, direction=%s",
			bbExpansion, a.VolumeRatio, a.TrendDirection)
		return
	}

	if a.ADX > adxTrendThreshold && a.TrendDirection != "neutral" {
		a.Confidence = math.Min(1.0, (a.ADX-adxTrendThreshold)/50.0+0.5)
		a.SuggestedSizeMultiplier = d.getTuning("regime.trending.size_multiplier", 1.00)
		if a.TrendDirection == "up" {
			a.Regime = RegimeTrendingUp
			a.SuggestedAction = ActionLongOnly
			a.Reasoning = fmt.Sprintf("Strong uptrend: ADX=%.1f (>%.0f), price above EMA", a.ADX, adxTrendThreshold)
		} else {
			a.Regime = RegimeTrendingDown
			a.SuggestedAction = ActionShortOnly
			a.Reasoning = fmt.Sprintf("Strong downtrend: ADX=%.1f (>%.0f), price below EMA", a.ADX, adxTrendThreshold)
		}
		return
	}

	if a.ADX < adxWeakThreshold && a.BollingerWidth < bbRangeThreshold {
		// Quiet-trend override: ADX/BB-width alone measure candle-to-candle
		// volatility, not directional persistence — a slow, low-amplitude grind
		// (small true range per candle) reads as RANGING even while the pair
		// steadily drifts one way over the lookback window. Check for that
		// drift before settling on RANGING; if found, reclassify with a
		// directional SuggestedAction so entries can't fade against it.
		quietTrendEnabled := true
		if cfg != nil {
			if v, ok := cfg.BoolOk("regime.quiet_trend.enabled"); ok {
				quietTrendEnabled = v
			}
		}
		if quietTrendEnabled {
			lookback := 20
			minNetMovePct := 1.5
			minConsistency := 0.65
			if cfg != nil {
				if v, ok := cfg.IntOk("regime.quiet_trend.lookback"); ok && v > 0 {
					lookback = v
				}
				if v, ok := cfg.FloatOk("regime.quiet_trend.min_net_move_pct"); ok && v > 0 {
					minNetMovePct = v
				}
				if v, ok := cfg.FloatOk("regime.quiet_trend.min_directional_consistency"); ok && v > 0 {
					minConsistency = v
				}
			}
			if netMovePct, consistency, ok := quietTrendDrift(closes, lookback); ok &&
				math.Abs(netMovePct) >= minNetMovePct && consistency >= minConsistency {
				a.Confidence = math.Min(1.0, (adxWeakThreshold-a.ADX)/adxWeakThreshold+0.4)
				a.SuggestedSizeMultiplier = d.getTuning("regime.quiet_trend.size_multiplier", 0.85)
				if netMovePct > 0 {
					a.Regime = RegimeQuietTrendUp
					a.SuggestedAction = ActionLongOnly
				} else {
					a.Regime = RegimeQuietTrendDown
					a.SuggestedAction = ActionShortOnly
				}
				a.Reasoning = fmt.Sprintf("Quiet trend: ADX=%.1f BBwidth=%.4f but net move %.2f%% over %d candles (%.0f%% directional consistency)",
					a.ADX, a.BollingerWidth, netMovePct, lookback, consistency*100)
				return
			}
		}

		a.Regime = RegimeRanging
		a.Confidence = math.Min(1.0, (adxWeakThreshold-a.ADX)/adxWeakThreshold+0.4)
		a.SuggestedAction = ActionRange
		a.SuggestedSizeMultiplier = d.getTuning("regime.ranging.size_multiplier", 0.85) // reduced from 0.5 — strong technical signals (>90%) pass threshold; weak ones (<75%) still blocked
		a.Reasoning = fmt.Sprintf("Tight range: ADX=%.1f (<%.0f), BB width=%.4f (<%.4f)",
			a.ADX, adxWeakThreshold, a.BollingerWidth, bbRangeThreshold)
		return
	}

	a.Regime = RegimeUnknown
	a.Confidence = unknownConfidence
	a.SuggestedAction = ActionPause
	a.SuggestedSizeMultiplier = d.getTuning("regime.unknown.size_multiplier", 1.00) // no penalty when regime is ambiguous — insufficient data to justify scaling down
	a.Reasoning = fmt.Sprintf("Ambiguous conditions: ADX=%.1f BBWidth=%.4f bbExpansion=%.2fx",
		a.ADX, a.BollingerWidth, bbExpansion)
}

// ComputeADX computes ADX and the directional indicators (+DI/-DI) using
// Wilder's smoothing. It is a pure function (no Detector state) so position
// management and the exit-decision fusion can reuse it on exit candles without
// instantiating a Detector. Returns an error when there is insufficient data.
func ComputeADX(highs, lows, closes []float64, period int) (adx, diPlus, diMinus float64, err error) {
	if len(highs) < period*2+1 {
		return 0, 0, 0, fmt.Errorf("insufficient data for ADX")
	}

	tr := make([]float64, len(highs))
	dmPlus := make([]float64, len(highs))
	dmMinus := make([]float64, len(highs))

	for i := 1; i < len(highs); i++ {
		tr1 := highs[i] - lows[i]
		tr2 := math.Abs(highs[i] - closes[i-1])
		tr3 := math.Abs(lows[i] - closes[i-1])
		tr[i] = math.Max(tr1, math.Max(tr2, tr3))

		upMove := highs[i] - highs[i-1]
		downMove := lows[i-1] - lows[i]
		if upMove > downMove && upMove > 0 {
			dmPlus[i] = upMove
		}
		if downMove > upMove && downMove > 0 {
			dmMinus[i] = downMove
		}
	}

	smoothedTR := wildersSmoothing(tr[1:], period)
	smoothedDMPlus := wildersSmoothing(dmPlus[1:], period)
	smoothedDMMinus := wildersSmoothing(dmMinus[1:], period)

	if len(smoothedTR) == 0 {
		return 0, 0, 0, fmt.Errorf("smoothed TR empty")
	}

	diPlusSeries := make([]float64, len(smoothedTR))
	diMinusSeries := make([]float64, len(smoothedTR))
	for i := range smoothedTR {
		if smoothedTR[i] != 0 {
			diPlusSeries[i] = (smoothedDMPlus[i] / smoothedTR[i]) * 100
			diMinusSeries[i] = (smoothedDMMinus[i] / smoothedTR[i]) * 100
		}
	}

	dx := make([]float64, len(diPlusSeries))
	for i := range diPlusSeries {
		sum := diPlusSeries[i] + diMinusSeries[i]
		if sum != 0 {
			dx[i] = (math.Abs(diPlusSeries[i]-diMinusSeries[i]) / sum) * 100
		}
	}

	adxSeries := wildersSmoothing(dx, period)
	if len(adxSeries) == 0 {
		return 0, diPlusSeries[len(diPlusSeries)-1], diMinusSeries[len(diMinusSeries)-1], nil
	}

	return adxSeries[len(adxSeries)-1], diPlusSeries[len(diPlusSeries)-1], diMinusSeries[len(diMinusSeries)-1], nil
}

// computeADX is the method form retained for existing Detector callers; it
// delegates to the exported pure ComputeADX.
func (d *Detector) computeADX(highs, lows, closes []float64, period int) (adx, diPlus, diMinus float64, err error) {
	return ComputeADX(highs, lows, closes, period)
}

func wildersSmoothing(data []float64, period int) []float64 {
	if len(data) < period {
		return []float64{}
	}
	smoothed := make([]float64, len(data)-period+1)
	sum := 0.0
	for i := 0; i < period; i++ {
		sum += data[i]
	}
	smoothed[0] = sum / float64(period)
	for i := 1; i < len(smoothed); i++ {
		smoothed[i] = (smoothed[i-1]*float64(period-1) + data[period-1+i]) / float64(period)
	}
	return smoothed
}

func (d *Detector) averageBBWidth(bb *indicators.BollingerBands) float64 {
	return d.averageBBWidthN(bb, d.bbWidthLookback)
}

// averageBBWidthN computes average BB width over the last lookback candles (TC-5.5).
func (d *Detector) averageBBWidthN(bb *indicators.BollingerBands, lookback int) float64 {
	n := len(bb.Middle)
	if n == 0 {
		return 0
	}
	if lookback > n {
		lookback = n
	}
	start := n - lookback
	sum := 0.0
	count := 0
	for i := start; i < n; i++ {
		if bb.Middle[i] != 0 {
			sum += (bb.Upper[i] - bb.Lower[i]) / bb.Middle[i]
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

func (d *Detector) computeVolumeRatio(volumes []float64) float64 {
	return d.computeVolumeRatioN(volumes, d.volumeLookback)
}

// computeVolumeRatioN computes volume ratio using an explicit lookback window (TC-5.5).
func (d *Detector) computeVolumeRatioN(volumes []float64, lookback int) float64 {
	if len(volumes) == 0 {
		return 1.0
	}
	if lookback > len(volumes)-1 {
		lookback = len(volumes) - 1
	}
	if lookback <= 0 {
		return 1.0
	}
	start := len(volumes) - 1 - lookback
	sum := 0.0
	for i := start; i < len(volumes)-1; i++ {
		sum += volumes[i]
	}
	avg := sum / float64(lookback)
	if avg == 0 {
		return 1.0
	}
	return volumes[len(volumes)-1] / avg
}

// quietTrendDrift measures net directional persistence over the trailing
// lookback candles, independent of ADX/BB volatility: netMovePct is the signed
// % change from the first to the last close in the window, and consistency is
// the fraction of candle-to-candle steps that agree with that sign (a window
// that nets a small drift by bouncing wildly scores low consistency here even
// though its net move might clear the threshold). ok=false when there aren't
// enough candles to fill the window.
func quietTrendDrift(closes []float64, lookback int) (netMovePct, consistency float64, ok bool) {
	if lookback < 2 || len(closes) < lookback+1 {
		return 0, 0, false
	}
	start := len(closes) - 1 - lookback
	first := closes[start]
	last := closes[len(closes)-1]
	if first == 0 {
		return 0, 0, false
	}
	netMovePct = (last - first) / first * 100
	sign := 1.0
	if netMovePct < 0 {
		sign = -1.0
	}

	agree, total := 0, 0
	for i := start + 1; i < len(closes); i++ {
		step := closes[i] - closes[i-1]
		if step == 0 {
			continue
		}
		total++
		if (step > 0 && sign > 0) || (step < 0 && sign < 0) {
			agree++
		}
	}
	if total == 0 {
		return netMovePct, 0, true
	}
	consistency = float64(agree) / float64(total)
	return netMovePct, consistency, true
}

func (d *Detector) calculateEMA(closes []float64, period int) float64 {
	if len(closes) < period {
		return 0
	}
	sum := 0.0
	for i := 0; i < period; i++ {
		sum += closes[i]
	}
	ema := sum / float64(period)
	multiplier := 2.0 / float64(period+1)
	for i := period; i < len(closes); i++ {
		ema = (closes[i]-ema)*multiplier + ema
	}
	return ema
}
