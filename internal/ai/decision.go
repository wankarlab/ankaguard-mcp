package ai

import (
	"context"
	"fmt"
	"math"

	dynamic "github.com/rantipay/trading-mcp/internal/config/dynamic"
	"github.com/sirupsen/logrus"
)

// DecisionConfig holds configuration for decision engine
type DecisionConfig struct {
	ConfidenceThreshold  float64
	RequireAlignment     bool
	MinTimeframesAligned int
	TradeSize            float64
	Leverage             int

	// Direction control
	AllowShortSelling             bool    // Allow SELL/SHORT signals
	MacroFilterEnabled            bool    // Enable macro trend filter
	MacroBearishStrengthThreshold float64 // Min macro bearish strength to block or allow shorts (0–1)
	MacroBullishStrengthThreshold float64 // Min macro bullish strength to allow only BUYs (0–1)
	CounterTrendFilterEnabled     bool    // Block trades against macro trend
}

// DefaultDecisionConfig returns default configuration
func DefaultDecisionConfig() *DecisionConfig {
	return &DecisionConfig{
		ConfidenceThreshold:           0.50,
		RequireAlignment:              true,
		MinTimeframesAligned:          2,
		TradeSize:                     3.0,
		Leverage:                      5,
		AllowShortSelling:             true,
		MacroFilterEnabled:            true,
		MacroBearishStrengthThreshold: 0.6,
		MacroBullishStrengthThreshold: 0.6,
		CounterTrendFilterEnabled:     true,
	}
}

// UpdateDecisionConfig updates mutable fields at runtime (thread-unsafe — caller must hold lock)
func (de *DecisionEngine) UpdateDecisionConfig(cfg *DecisionConfig) {
	de.config = cfg
}

// TuningInterface allows the decision engine to read dynamic tuning parameters
// from an external store (e.g. TuningStore). The fallback value is used when
// the key is not present in the store or when no store is configured.
type TuningInterface interface {
	Get(key string, fallback float64) float64
}

// DecisionEngine makes trading decisions based on market context
type DecisionEngine struct {
	contextBuilder *ContextBuilder
	log            *logrus.Logger
	config         *DecisionConfig
	btcMonitor     *BTCCorrelationMonitor // optional, may be nil
	trapDetector   TrapDetector           // optional; nil skips trap detection
	tuning         TuningInterface        // optional; nil → use fallback constants
}

// SetBTCMonitor links a BTC correlation monitor to the engine. It is optional —
// if not set, the BTC context check is skipped and EvaluateOpportunity operates
// as before. Safe to call with nil to detach.
func (de *DecisionEngine) SetBTCMonitor(monitor *BTCCorrelationMonitor) {
	de.btcMonitor = monitor
}

// SetTrapDetector links a manipulation/trap detector. Optional — if nil,
// trap detection is skipped and EvaluateOpportunity proceeds as before.
func (de *DecisionEngine) SetTrapDetector(d TrapDetector) {
	de.trapDetector = d
}

// SetTuning wires a TuningInterface into the decision engine. Safe to call
// with nil to detach an existing store (reverts to hardcoded fallbacks).
func (de *DecisionEngine) SetTuning(t TuningInterface) {
	de.tuning = t
}

// getTuning returns the value for key from the tuning store, or fallback when
// no store is configured.
func (de *DecisionEngine) getTuning(key string, fallback float64) float64 {
	if de.tuning == nil {
		return fallback
	}
	return de.tuning.Get(key, fallback)
}

// BTCContextAdjustment represents the adjustment applied to a decision
// based on the current BTC market context.
type BTCContextAdjustment struct {
	Applied            bool
	BTCDirection       string // "up", "down", "neutral"
	BTCMomentum        float64
	Correlation        float64
	OriginalConfidence float64
	AdjustedConfidence float64
	Reasoning          string
}

// deriveBTCDirectionAndMomentum derives a coarse direction ("up"|"down"|"neutral")
// and momentum (signed percentage change over the recent window) from the BTC
// price history exposed by the monitor. Returns ok=false when there is not
// enough data to make a judgement.
// TC-4.5: uses getTuning for btc.history.min_points, btc.momentum.window, btc.direction.neutral_band_pct
func (de *DecisionEngine) deriveBTCDirectionAndMomentum() (direction string, momentum float64, ok bool) {
	if de.btcMonitor == nil {
		return "neutral", 0, false
	}

	history := de.btcMonitor.GetBTCPriceHistory()
	minPoints := int(de.getTuning("btc.history.min_points", 5))
	if minPoints <= 0 {
		minPoints = 5
	}
	if len(history) < minPoints {
		return "neutral", 0, false
	}

	// Use last ~N points to compute net momentum (percent change).
	window := int(de.getTuning("btc.momentum.window", 10))
	if window <= 0 {
		window = 10
	}
	if len(history) < window {
		window = len(history)
	}
	start := history[len(history)-window].Price
	end := history[len(history)-1].Price
	if start <= 0 {
		return "neutral", 0, false
	}

	momentum = (end - start) / start * 100.0 // percent change

	// Direction thresholds: configurable neutral band (default ±0.1%).
	neutralBand := de.getTuning("btc.direction.neutral_band_pct", 0.1)
	if neutralBand <= 0 {
		neutralBand = 0.1
	}
	switch {
	case momentum > neutralBand:
		direction = "up"
	case momentum < -neutralBand:
		direction = "down"
	default:
		direction = "neutral"
	}

	return direction, momentum, true
}

// AdjustDecisionWithBTC takes an existing decision and applies an adjustment
// based on the current BTC context. If btcMonitor is nil or there is no usable
// BTC data, the decision is returned unchanged with Applied=false.
//
// Rules:
//   - BUY against strong BTC down → confidence * 0.5
//   - SELL against strong BTC up  → confidence * 0.5
//   - Aligned with BTC direction  → confidence * 1.10 (capped at 0.95)
//   - BTC neutral or WAIT action  → no change
func (de *DecisionEngine) AdjustDecisionWithBTC(decision *Decision) (*Decision, *BTCContextAdjustment) {
	adj := &BTCContextAdjustment{
		Applied: false,
	}

	if decision == nil {
		return decision, adj
	}
	adj.OriginalConfidence = decision.Confidence
	adj.AdjustedConfidence = decision.Confidence

	if de.btcMonitor == nil {
		adj.Reasoning = "BTC monitor not configured - no adjustment"
		return decision, adj
	}

	direction, momentum, ok := de.deriveBTCDirectionAndMomentum()
	if !ok {
		adj.Reasoning = "Insufficient BTC price history - no adjustment"
		return decision, adj
	}
	adj.BTCDirection = direction
	adj.BTCMomentum = momentum

	// Surface the symbol's correlation with BTC if available.
	for _, c := range de.btcMonitor.GetCorrelations() {
		if c.Symbol != "" {
			// We don't know the trade symbol at this layer; just keep the
			// strongest absolute correlation as a contextual reference.
			if math.Abs(c.Correlation) > math.Abs(adj.Correlation) {
				adj.Correlation = c.Correlation
			}
		}
	}

	// WAIT decisions are not adjusted.
	if decision.Action == "WAIT" {
		adj.Reasoning = "WAIT action - no BTC adjustment"
		return decision, adj
	}

	// TC-4.5: btc.confidence_cap is now dynamic; fallback 0.95
	confidenceCap := de.getTuning("btc.confidence_cap", 0.95)
	if confidenceCap <= 0 {
		confidenceCap = 0.95
	}

	strongMomentumThreshold := de.getTuning("btc.strong_momentum_threshold", 0.30)
	momentumStrong := math.Abs(momentum) > strongMomentumThreshold

	original := decision.Confidence
	adjusted := original
	reasoning := "BTC neutral - no adjustment"

	switch decision.Action {
	case "BUY":
		switch {
		case direction == "down" && momentumStrong:
			adjusted = original * de.getTuning("btc.counter_trend.penalty", 0.85) // reduced from 0.5 — LLM already penalises counter-trend; avoid double-kill
			reasoning = "BTC moving down strongly - slight long confidence reduction"
		case direction == "up":
			adjusted = math.Min(original*de.getTuning("btc.aligned.boost", 1.10), confidenceCap)
			reasoning = "BTC trending up - aligned with long signal"
		default:
			reasoning = "BTC neutral - no adjustment"
		}
	case "SELL":
		switch {
		case direction == "up" && momentumStrong:
			adjusted = original * de.getTuning("btc.counter_trend.penalty", 0.85) // reduced from 0.5 — LLM already penalises counter-trend; avoid double-kill
			reasoning = "BTC moving up strongly - slight short confidence reduction"
		case direction == "down":
			adjusted = math.Min(original*de.getTuning("btc.aligned.boost", 1.10), confidenceCap)
			reasoning = "BTC trending down - aligned with short signal"
		default:
			reasoning = "BTC neutral - no adjustment"
		}
	default:
		reasoning = fmt.Sprintf("Unknown action %q - no BTC adjustment", decision.Action)
	}

	if adjusted != original {
		adj.Applied = true
		decision.Confidence = adjusted
		decision.Reason = fmt.Sprintf("%s | BTC ctx: %s (mom %.2f%%, %s)",
			decision.Reason, direction, momentum, reasoning)
	}
	adj.AdjustedConfidence = adjusted
	adj.Reasoning = reasoning

	de.log.WithFields(logrus.Fields{
		"action":              decision.Action,
		"btc_direction":       direction,
		"btc_momentum":        momentum,
		"original_confidence": original,
		"adjusted_confidence": adjusted,
		"applied":             adj.Applied,
		"reasoning":           reasoning,
	}).Debug("BTC context adjustment evaluated")

	return decision, adj
}

// NewDecisionEngine creates a new decision engine with default config
func NewDecisionEngine(contextBuilder *ContextBuilder, log *logrus.Logger) *DecisionEngine {
	return &DecisionEngine{
		contextBuilder: contextBuilder,
		log:            log,
		config:         DefaultDecisionConfig(),
	}
}

// NewDecisionEngineWithConfig creates a new decision engine with custom config
func NewDecisionEngineWithConfig(contextBuilder *ContextBuilder, log *logrus.Logger, config *DecisionConfig) *DecisionEngine {
	return &DecisionEngine{
		contextBuilder: contextBuilder,
		log:            log,
		config:         config,
	}
}

// EvaluateOpportunity evaluates if there's a trading opportunity
// REFACTORED: Now uses dynamic timeframe access and context-based config snapshot
func (de *DecisionEngine) EvaluateOpportunity(goCtx context.Context, ctx *MarketContext) (*Decision, error) {
	de.log.Debug("Evaluating trading opportunity")

	// TC-4.1: extract dynamic config snapshot for this call. Non-nil guarantee
	// provided by the default-store fallback in SnapshotFromContext.
	cfg := dynamic.SnapshotFromContext(goCtx)

	// Snapshot overrides for decision.* params — applied per-call so changes
	// in Postgres take effect on the next evaluation without restart.
	effectiveCfg := *de.config // copy — never mutate the shared struct
	if cfg != nil {
		if v, ok := cfg.BoolOk("decision.allow_short_selling"); ok {
			effectiveCfg.AllowShortSelling = v
		}
		if v, ok := cfg.BoolOk("decision.macro_filter_enabled"); ok {
			effectiveCfg.MacroFilterEnabled = v
		}
		if v, ok := cfg.BoolOk("decision.counter_trend_filter_enabled"); ok {
			effectiveCfg.CounterTrendFilterEnabled = v
		}
		if v, ok := cfg.BoolOk("decision.require_alignment"); ok {
			effectiveCfg.RequireAlignment = v
		}
		if v, ok := cfg.FloatOk("decision.macro.bearish_strength_threshold"); ok && v > 0 {
			effectiveCfg.MacroBearishStrengthThreshold = v
		}
		if v, ok := cfg.FloatOk("decision.macro.bullish_strength_threshold"); ok && v > 0 {
			effectiveCfg.MacroBullishStrengthThreshold = v
		}
		if v, ok := cfg.FloatOk("decision.confidence_threshold"); ok && v > 0 {
			effectiveCfg.ConfidenceThreshold = v
		}
	}

	// TC-5.4 TODO: Add warming guard here once DecisionEngine holds an
	// *IndicatorCalculator reference. Call ic.CheckWarming(goCtx, ctx.Pair)
	// and return Decision{Action:"WAIT", Reason: fmt.Sprintf("indicator warming up ...")}
	// when WarmingStatus.Ready == false. Skipped to avoid invasive struct change.

	// Ensure style is set
	ctx.EnsureStyleSet()

	// Get primary and macro contexts
	primaryCtx := ctx.GetPrimaryContext()
	if primaryCtx == nil {
		return nil, fmt.Errorf("primary timeframe context not available")
	}

	macroCtx := ctx.GetMacroContext()

	// When per-pair directional control is active (suppress_countertrend /
	// trend_follow), the bot's OWN 1h-EMA50 macro trend (ctx.MacroTrend) plus the
	// executeTrade trend filter are the direction authority. Bypass the legacy
	// internal macro/alignment/counter-trend filters below — they use a different
	// (shorter-TF) macro definition and would short-circuit to a mean-reversion
	// side before the directional override at the switch runs. legacyDirection is
	// true only when the feature is off (DirectionalMode unset).
	legacyDirection := ctx.DirectionalMode == ""

	// Macro trend filter — thresholds are configurable
	if macroCtx != nil && effectiveCfg.MacroFilterEnabled && legacyDirection {
		bearishThreshold := effectiveCfg.MacroBearishStrengthThreshold
		bullishThreshold := effectiveCfg.MacroBullishStrengthThreshold

		if macroCtx.Trend == "bearish" && macroCtx.Strength > bearishThreshold {
			// If shorts are allowed, route to bearish setup instead of blocking
			if effectiveCfg.AllowShortSelling {
				de.log.WithField("macro_strength", macroCtx.Strength).Debug("Strong bearish macro — routing to SHORT setup")
				return de.evaluateBearishSetup(ctx, cfg)
			}
			macroBlockConf := 0.2 // fallback
			if cfg != nil {
				if v, ok := cfg.FloatOk("decision.wait.macro_block_confidence"); ok {
					macroBlockConf = v
				}
			}
			return &Decision{
				Action:     "WAIT",
				Confidence: macroBlockConf,
				Reason:     fmt.Sprintf("Strong bearish macro (strength: %.2f) — shorts disabled", macroCtx.Strength),
			}, nil
		}
		if macroCtx.Trend == "bullish" && macroCtx.Strength > bullishThreshold {
			de.log.Debug("Strong bullish macro — only allowing BUY setups")
		}
	}

	// 1. Check trend alignment (if required)
	aligned, direction := de.contextBuilder.GetTrendAlignment(ctx)

	misalignConf := 0.3 // fallback
	if cfg != nil {
		if v, ok := cfg.FloatOk("decision.wait.misalignment_confidence"); ok {
			misalignConf = v
		}
	}
	if effectiveCfg.RequireAlignment && !aligned && legacyDirection {
		secondaryCtxs := ctx.GetSecondaryContexts()
		secondaryTrend := "unknown"
		if len(secondaryCtxs) > 0 {
			secondaryTrend = secondaryCtxs[0].Trend
		}
		return &Decision{
			Action:     "WAIT",
			Confidence: misalignConf,
			Reason:     fmt.Sprintf("Trends not aligned: primary=%s, secondary=%s", primaryCtx.Trend, secondaryTrend),
		}, nil
	}

	// If alignment not required, use primary timeframe
	if !aligned {
		direction = primaryCtx.Trend
		if direction == "sideways" {
			// Fallback to secondary timeframe
			secondaryCtxs := ctx.GetSecondaryContexts()
			if len(secondaryCtxs) > 0 {
				direction = secondaryCtxs[0].Trend
			}
		}
	}

	// Counter-trend filter: avoid BUY when macro is bearish (configurable)
	if macroCtx != nil && effectiveCfg.MacroFilterEnabled && effectiveCfg.CounterTrendFilterEnabled && legacyDirection {
		if macroCtx.Trend == "bearish" && direction == "bullish" {
			if effectiveCfg.AllowShortSelling {
				// Flip to short instead of waiting
				de.log.Debug("Counter-trend filter: bearish macro + bullish signal → routing to SHORT")
				return de.evaluateBearishSetup(ctx, cfg)
			}
			return &Decision{
				Action:     "WAIT",
				Confidence: misalignConf,
				Reason:     "Avoiding BUY in macro bearish trend (counter-trend filter active)",
			}, nil
		}
	}

	// 2. Find entry setup based on direction
	var decision *Decision
	var err error

	noDirectionConf := 0.3 // fallback
	if cfg != nil {
		if v, ok := cfg.FloatOk("decision.wait.no_direction_confidence"); ok {
			noDirectionConf = v
		}
	}

	// ── Per-pair directional control (futures bidirectional trading) ──────────
	// ctx.DirectionalMode is set by the bot's runAnalysis from THIS pair's own 1h
	// EMA50 trend (ctx.MacroTrend), and only when the feature is enabled + futures.
	// Empty/"off" → no change (legacy mean-reversion routing below).
	//
	//   suppress_countertrend: if the mean-reversion `direction` fights the macro
	//     trend (e.g. SELL signal while the pair is in an uptrend), WAIT instead of
	//     emitting the side the executeTrade trend filter will block anyway.
	//   trend_follow: OVERRIDE `direction` with the macro trend so the bot takes the
	//     ALIGNED side per pair (long on uptrend pairs, short on downtrend pairs).
	//     The setups score it as pullback-in-trend (see evaluateBullish/BearishSetup,
	//     gated on ctx.DirectionalMode=="trend_follow").
	switch ctx.DirectionalMode {
	case "suppress_countertrend":
		if ctx.MacroTrend == "bullish" && direction == "bearish" {
			return &Decision{Action: "WAIT", Confidence: noDirectionConf,
				Reason: "directional: counter-trend SELL suppressed (pair in macro uptrend)"}, nil
		}
		if ctx.MacroTrend == "bearish" && direction == "bullish" {
			return &Decision{Action: "WAIT", Confidence: noDirectionConf,
				Reason: "directional: counter-trend BUY suppressed (pair in macro downtrend)"}, nil
		}
	case "trend_follow":
		if ctx.MacroTrend == "bullish" {
			direction = "bullish"
		} else if ctx.MacroTrend == "bearish" {
			direction = "bearish"
		}
		// neutral macro → leave the mean-reversion direction untouched.
	}

	// ── Falling-knife / rising-knife entry guard ──────────────────────────────
	// Block an entry when the primary (entry) timeframe shows a strong recent
	// move AGAINST the side we're about to take. The macro trend filter only
	// guards on the 1h timescale (≈6h lookback) — too coarse to catch an intraday
	// waterfall: WLD dropped ~4% in 44min while its lagging 1h EMA50 still read
	// bullish, so trend_follow kept buying the knife and lost every entry. This
	// guard checks the ACTUAL entry timescale (primary TF). Gated; default on
	// with a conservative threshold so it only blocks genuine adverse spikes.
	if cfg != nil && (direction == "bullish" || direction == "bearish") {
		guardEnabled := true
		if v, ok := cfg.BoolOk("bot.entry.adverse_guard.enabled"); ok {
			guardEnabled = v
		}
		if guardEnabled {
			lookback := 6
			if v, ok := cfg.IntOk("bot.entry.adverse_guard.lookback_candles"); ok && v > 0 {
				lookback = v
			}
			maxAdverse := 0.015 // 1.5%
			if v, ok := cfg.FloatOk("bot.entry.adverse_guard.max_adverse_pct"); ok && v > 0 {
				maxAdverse = v / 100
			}
			if mv, ok := recentMovePct(primaryCtx.Candles, lookback); ok {
				if direction == "bullish" && mv <= -maxAdverse {
					return &Decision{Action: "WAIT", Confidence: noDirectionConf,
						Reason: fmt.Sprintf("falling-knife guard: %s fell %.2f%% over last %d candles", primaryCtx.Timeframe, mv*100, lookback)}, nil
				}
				if direction == "bearish" && mv >= maxAdverse {
					return &Decision{Action: "WAIT", Confidence: noDirectionConf,
						Reason: fmt.Sprintf("rising-knife guard: %s rose %.2f%% over last %d candles", primaryCtx.Timeframe, mv*100, lookback)}, nil
				}
			}

			// Secondary-TF check: the primary TF window (e.g. 5m×6 ≈ 30min) is too
			// short to catch a knife that develops on a wider timescale — TAO-USDT
			// #2237 fell 3.5% over 90min on the 15m TF while the 5m primary window
			// showed only ~0.5%, so the guard above missed it. This mirrors the same
			// logic on GetSecondaryContexts()[0], independently toggleable/tunable.
			secEnabled := true
			if v, ok := cfg.BoolOk("bot.entry.adverse_guard.secondary_enabled"); ok {
				secEnabled = v
			}
			if secEnabled {
				if secCtxs := ctx.GetSecondaryContexts(); len(secCtxs) > 0 && secCtxs[0] != nil {
					secCtx := secCtxs[0]
					secLookback := 6
					if v, ok := cfg.IntOk("bot.entry.adverse_guard.secondary_lookback_candles"); ok && v > 0 {
						secLookback = v
					}
					secMaxAdverse := 0.025 // 2.5%
					if v, ok := cfg.FloatOk("bot.entry.adverse_guard.secondary_max_adverse_pct"); ok && v > 0 {
						secMaxAdverse = v / 100
					}
					if mv, ok := recentMovePct(secCtx.Candles, secLookback); ok {
						if direction == "bullish" && mv <= -secMaxAdverse {
							return &Decision{Action: "WAIT", Confidence: noDirectionConf,
								Reason: fmt.Sprintf("falling-knife guard (secondary TF): %s fell %.2f%% over last %d candles", secCtx.Timeframe, mv*100, secLookback)}, nil
						}
						if direction == "bearish" && mv >= secMaxAdverse {
							return &Decision{Action: "WAIT", Confidence: noDirectionConf,
								Reason: fmt.Sprintf("rising-knife guard (secondary TF): %s rose %.2f%% over last %d candles", secCtx.Timeframe, mv*100, secLookback)}, nil
						}
					}
				}
			}
		}
	}

	switch direction {
	case "bullish":
		decision, err = de.evaluateBullishSetup(ctx, cfg)
	case "bearish":
		decision, err = de.evaluateBearishSetup(ctx, cfg)
	default:
		return &Decision{
			Action:     "WAIT",
			Confidence: noDirectionConf,
			Reason:     "No clear direction",
		}, nil
	}

	if err != nil {
		return nil, err
	}

	// 3. Validate confidence threshold (configurable)
	if decision.Confidence < effectiveCfg.ConfidenceThreshold {
		decision.Action = "WAIT"
		decision.Reason = fmt.Sprintf("Confidence too low: %.2f (threshold: %.2f, original: %s)",
			decision.Confidence, effectiveCfg.ConfidenceThreshold, decision.Reason)
	}

	// 4. Apply configured trade size and leverage
	decision.Size = de.config.TradeSize
	decision.Leverage = de.config.Leverage

	// 5. Optional BTC context adjustment - only runs if a monitor is wired.
	// Defaults remain unchanged when btcMonitor is nil.
	if de.btcMonitor != nil {
		decision, _ = de.AdjustDecisionWithBTC(decision)
		// Re-check confidence threshold after adjustment so a downgraded
		// decision is correctly demoted to WAIT.
		if decision.Action != "WAIT" && decision.Confidence < effectiveCfg.ConfidenceThreshold {
			decision.Action = "WAIT"
			decision.Reason = fmt.Sprintf("Confidence too low after BTC adjustment: %.2f (threshold: %.2f, original: %s)",
				decision.Confidence, effectiveCfg.ConfidenceThreshold, decision.Reason)
		}
	}

	// 6. Trap detection: if manipulation is detected with high confidence, veto the trade.
	if de.trapDetector != nil {
		primaryCtxForTrap := ctx.GetPrimaryContext()
		if primaryCtxForTrap != nil && len(primaryCtxForTrap.Candles) >= 10 {
			trap := de.trapDetector.AnalyzeRecentTrap(primaryCtxForTrap.Candles)
			trapVetoMin := 0.70 // fallback
			if cfg != nil {
				if v, ok := cfg.FloatOk("scoring.trap.veto_confidence_min"); ok {
					trapVetoMin = v
				}
			}
			if trap != nil && trap.Detected && trap.Confidence >= trapVetoMin {
				de.logAt(goCtx, logrus.WarnLevel, logrus.Fields{
					"trap_type":  trap.TrapType,
					"confidence": trap.Confidence,
					"action":     trap.SuggestedAction,
				}, "decision: manipulation detected — vetoing trade")
				decision.Action = "HOLD"
				decision.Confidence = 0
				decision.Reason = fmt.Sprintf("%s | TRAP VETO: %s (conf=%.2f)", decision.Reason, trap.TrapType, trap.Confidence)
			}
		}
	}

	de.logAt(goCtx, logrus.InfoLevel, logrus.Fields{
		"action":     decision.Action,
		"confidence": decision.Confidence,
		"reason":     decision.Reason,
	}, "Decision made")

	return decision, nil
}

// recentMovePct returns the signed price change as a fraction over the last
// `lookback` candles: (newest.Close - older.Close) / older.Close. It is
// order-agnostic (detects ascending vs newest-first by timestamp) so it stays
// correct even for the REST newest-first series that caused prior candle-
// ordering bugs. Returns ok=false when there isn't enough data.
func recentMovePct(candles []Candle, lookback int) (float64, bool) {
	n := len(candles)
	if n < 2 || lookback < 1 {
		return 0, false
	}
	if lookback >= n {
		lookback = n - 1
	}
	ascending := !candles[0].Timestamp.After(candles[n-1].Timestamp)
	var newest, older Candle
	if ascending {
		newest = candles[n-1]
		older = candles[n-1-lookback]
	} else {
		newest = candles[0]
		older = candles[lookback]
	}
	if older.Close <= 0 {
		return 0, false
	}
	return (newest.Close - older.Close) / older.Close, true
}

// priceActionEnabled reports whether the graded price-action reader is active
// (defaults on; the manifest seeds the key to "true").
func priceActionEnabled(cfg *dynamic.ScopedSnapshot) bool {
	if cfg == nil {
		return true
	}
	return cfg.Bool("scoring.price_action.enabled")
}

// priceActionParamsFromCfg builds the detector thresholds from dynamic config,
// falling back to baseline defaults for any key not set.
func priceActionParamsFromCfg(cfg *dynamic.ScopedSnapshot) PriceActionParams {
	p := DefaultPriceActionParams()
	if cfg == nil {
		return p
	}
	if v, ok := cfg.FloatOk("scoring.price_action.marubozu_body_ratio"); ok {
		p.MarubozuBodyRatio = v
	}
	if v, ok := cfg.FloatOk("scoring.price_action.pin_wick_body_ratio"); ok {
		p.PinWickBodyRatio = v
	}
	if v, ok := cfg.FloatOk("scoring.price_action.doji_body_ratio"); ok {
		p.DojiBodyRatio = v
	}
	if v, ok := cfg.FloatOk("scoring.price_action.volume_confirm_ratio"); ok {
		p.VolumeConfirmRatio = v
	}
	if v := cfg.Int("scoring.price_action.volume_lookback"); v > 0 {
		p.VolumeLookback = v
	}
	if v, ok := cfg.FloatOk("scoring.price_action.strength.engulfing"); ok {
		p.EngulfingStrength = v
	}
	if v, ok := cfg.FloatOk("scoring.price_action.strength.pin_bar"); ok {
		p.PinBarStrength = v
	}
	if v, ok := cfg.FloatOk("scoring.price_action.strength.marubozu"); ok {
		p.MarubozuStrength = v
	}
	if v, ok := cfg.FloatOk("scoring.price_action.strength.three_candle"); ok {
		p.ThreeCandleStrength = v
	}
	if v, ok := cfg.FloatOk("scoring.price_action.strength.reversal_flip"); ok {
		p.ReversalFlipStrength = v
	}
	return p
}

// priceActionBonusWeight scales the [0,1] price-action strength into a confidence
// delta. priceActionContradictFactor scales the penalty applied when the read
// contradicts the setup direction.
func priceActionBonusWeight(cfg *dynamic.ScopedSnapshot) float64 {
	if cfg != nil {
		if v, ok := cfg.FloatOk("scoring.price_action.bonus_weight"); ok && v > 0 {
			return v
		}
	}
	return 0.20
}

func priceActionContradictFactor(cfg *dynamic.ScopedSnapshot) float64 {
	if cfg != nil {
		if v, ok := cfg.FloatOk("scoring.price_action.contradict_penalty_factor"); ok && v >= 0 {
			return v
		}
	}
	return 1.0
}

// priceActionContradictVeto reports whether a contradicting price-action read
// should HARD-veto the entry (confidence forced to 0) rather than just apply a
// soft penalty. Defaults to true.
func priceActionContradictVeto(cfg *dynamic.ScopedSnapshot) bool {
	if cfg != nil {
		if v, ok := cfg.BoolOk("scoring.price_action.contradict_veto"); ok {
			return v
		}
	}
	return true
}

// priceActionContradictVetoStrength is the minimum opposing-pattern strength
// required to trigger the hard veto. Weaker contradictions only apply the soft
// penalty.
func priceActionContradictVetoStrength(cfg *dynamic.ScopedSnapshot) float64 {
	if cfg != nil {
		if v, ok := cfg.FloatOk("scoring.price_action.contradict_veto_strength"); ok && v > 0 {
			return v
		}
	}
	return 0.25
}

// priceActionMinConfirmStrength is the minimum strength for a SUPPORTING
// price-action read to count as confirmation and add its bonus. Weaker reads are
// ignored (treated as noise).
func priceActionMinConfirmStrength(cfg *dynamic.ScopedSnapshot) float64 {
	if cfg != nil {
		if v, ok := cfg.FloatOk("scoring.price_action.min_confirm_strength"); ok && v > 0 {
			return v
		}
	}
	return 0.30
}

// evaluateBullishSetup evaluates bullish entry setup
// REFACTORED: Now uses dynamic timeframe access and cfg snapshot for all thresholds
func (de *DecisionEngine) evaluateBullishSetup(ctx *MarketContext, cfg *dynamic.ScopedSnapshot) (*Decision, error) {
	// TC-4.2: read base confidence from dynamic config; fallback to 0.50
	baseConf := 0.50
	if cfg != nil {
		if v, ok := cfg.FloatOk("scoring.base_confidence"); ok {
			baseConf = v
		}
	}
	confidence := baseConf
	reasons := []string{}

	// Get primary context
	primaryCtx := ctx.GetPrimaryContext()
	if primaryCtx == nil {
		return nil, fmt.Errorf("primary context not available")
	}

	// Get secondary context (if available)
	var secondaryCtx *TimeframeContext
	secondaryCtxs := ctx.GetSecondaryContexts()
	if len(secondaryCtxs) > 0 {
		secondaryCtx = secondaryCtxs[0]
	}

	// Scoring thresholds from dynamic config
	reversalBonus := 0.15
	rsiPrimaryBonus := 0.15
	rsiOversoldThreshold := 35.0
	rsiSecondaryMax := 65.0
	rsiSecondaryBonus := 0.10
	rsiSecondaryBullishMax := 45.0 // bullish: secondary RSI must be BELOW this for directional bonus
	rsiSecondaryNeutralPenaltyLong := 0.0
	rsiSecondaryNeutralVetoLong := false
	rsiExtremeOverbought := 85.0   // secondary RSI at/above this is an EXTREME contradiction for LONG
	rsiExtremeVetoEnabled := false // hard-veto counter-direction entries at RSI extremes
	rsiContradictionPenalty := 0.0 // 0 → falls back to rsiSecondaryBonus (legacy behavior)
	macdTrendBonus := 0.10
	supportNearDistancePct := 2.0
	supportStrongThresholdPct := 1.0
	supportStrongBonus := 0.20
	supportNearBonus := 0.10
	bollingerOuterBonus := 0.10
	volConfirmRatio := 1.2
	volConfirmBonus := 0.10
	volLowRatio := 0.5
	volLowPenalty := -0.15
	missingConfirmPenalty := -0.10
	atrSLMultiplier := 1.5
	atrTPMultiplier := 2.5
	atrLevelBuffer := 0.998
	fallbackSLLong := 0.995
	fallbackTPLong := 1.010
	correlatedCapEnabled := true
	correlatedCapMaxBonus := 0.30
	correlatedCapVetoEnabled := true

	if cfg != nil {
		if v, ok := cfg.FloatOk("scoring.reversal_candle_bonus"); ok {
			reversalBonus = v
		}
		if v, ok := cfg.FloatOk("scoring.rsi.primary_bonus"); ok {
			rsiPrimaryBonus = v
		}
		if v, ok := cfg.FloatOk("scoring.rsi.oversold_threshold"); ok {
			rsiOversoldThreshold = v
		}
		if v, ok := cfg.FloatOk("scoring.rsi.secondary_max"); ok {
			rsiSecondaryMax = v
		}
		if v, ok := cfg.FloatOk("scoring.rsi.secondary_bonus"); ok {
			rsiSecondaryBonus = v
		}
		if v, ok := cfg.FloatOk("scoring.rsi.secondary_bullish_max"); ok {
			rsiSecondaryBullishMax = v
		}
		if v, ok := cfg.FloatOk("scoring.secondary_tf.neutral_penalty"); ok {
			rsiSecondaryNeutralPenaltyLong = v
		}
		if v, ok := cfg.BoolOk("scoring.secondary_tf.neutral_veto_enabled"); ok {
			rsiSecondaryNeutralVetoLong = v
		}
		if v, ok := cfg.FloatOk("scoring.rsi.extreme_overbought"); ok {
			rsiExtremeOverbought = v
		}
		if v, ok := cfg.BoolOk("scoring.rsi.extreme_contradiction_veto_enabled"); ok {
			rsiExtremeVetoEnabled = v
		}
		if v, ok := cfg.FloatOk("scoring.rsi.contradiction_penalty"); ok {
			rsiContradictionPenalty = v
		}
		if v, ok := cfg.FloatOk("scoring.macd.trend_bonus"); ok {
			macdTrendBonus = v
		}
		if v, ok := cfg.FloatOk("scoring.support.near_distance_pct"); ok {
			supportNearDistancePct = v
		}
		if v, ok := cfg.FloatOk("scoring.support.strong_threshold_pct"); ok {
			supportStrongThresholdPct = v
		}
		if v, ok := cfg.FloatOk("scoring.support.strong_bonus"); ok {
			supportStrongBonus = v
		}
		if v, ok := cfg.FloatOk("scoring.support.near_bonus"); ok {
			supportNearBonus = v
		}
		if v, ok := cfg.FloatOk("scoring.bollinger.outer_band_bonus"); ok {
			bollingerOuterBonus = v
		}
		if v, ok := cfg.FloatOk("scoring.volume.confirmation_ratio"); ok {
			volConfirmRatio = v
		}
		if v, ok := cfg.FloatOk("scoring.volume.confirmation_bonus"); ok {
			volConfirmBonus = v
		}
		if v, ok := cfg.FloatOk("scoring.volume.low_ratio"); ok {
			volLowRatio = v
		}
		if v, ok := cfg.FloatOk("scoring.volume.low_penalty"); ok {
			volLowPenalty = v
		}
		if v, ok := cfg.FloatOk("scoring.missing_confirmation_penalty"); ok {
			missingConfirmPenalty = v
		}
		if v, ok := cfg.FloatOk("scoring.atr.sl_multiplier"); ok {
			atrSLMultiplier = v
		}
		if v, ok := cfg.FloatOk("scoring.atr.tp_multiplier"); ok {
			atrTPMultiplier = v
		}
		if v, ok := cfg.FloatOk("scoring.atr.level_buffer"); ok {
			atrLevelBuffer = v
		}
		if v, ok := cfg.FloatOk("scoring.fallback.sl_pct_long"); ok {
			fallbackSLLong = v
		}
		if v, ok := cfg.FloatOk("scoring.fallback.tp_pct_long"); ok {
			fallbackTPLong = v
		}
		if v, ok := cfg.BoolOk("scoring.correlated_cap.enabled"); ok {
			correlatedCapEnabled = v
		}
		if v, ok := cfg.FloatOk("scoring.correlated_cap.max_bonus"); ok && v > 0 {
			correlatedCapMaxBonus = v
		}
		if v, ok := cfg.BoolOk("scoring.correlated_cap.veto_enabled"); ok {
			correlatedCapVetoEnabled = v
		}
	}

	// Correlated dip-signal bonus tracker: RSI-primary-oversold, RSI-secondary-
	// bullish, MACD-bullish, strong-support and lower-Bollinger are largely the
	// SAME underlying fact ("price just fell to a local floor") read five
	// different ways. TAO-USDT #2237 stacked all five to confidence 1.0 while
	// buying a knife falling -3.5% on the 15m scale. correlatedDipBonus sums
	// just these overlapping contributions so they can be capped below,
	// independently of genuinely distinct signals (reversal candle, volume).
	correlatedDipBonus := 0.0

	// Reversal / continuation confirmation. Prefer the graded price-action reader
	// (reads body size, rejection wicks, volume and 3-candle sequences — "see the
	// chart like a human") when enabled; fall back to the legacy 2-candle colour
	// flip otherwise.
	hasReversalConfirmation := false
	priceActionVeto := false
	if priceActionEnabled(cfg) {
		pa := DetectPriceAction(primaryCtx.Candles, priceActionParamsFromCfg(cfg))
		paWeight := priceActionBonusWeight(cfg)
		switch pa.Direction {
		case "bullish":
			if pa.Strength >= priceActionMinConfirmStrength(cfg) {
				hasReversalConfirmation = true
				confidence += paWeight * pa.Strength
				reasons = append(reasons, pa.Reason())
			} else {
				reasons = append(reasons, "price-action bullish but below min strength (ignored): "+pa.Reason())
			}
		case "bearish":
			confidence -= paWeight * pa.Strength * priceActionContradictFactor(cfg)
			reasons = append(reasons, "price-action contradicts long: "+pa.Reason())
			if priceActionContradictVeto(cfg) && pa.Strength >= priceActionContradictVetoStrength(cfg) {
				priceActionVeto = true
			}
		}
	} else if len(primaryCtx.Candles) >= 2 {
		lastCandle := primaryCtx.Candles[len(primaryCtx.Candles)-1]
		prevCandle := primaryCtx.Candles[len(primaryCtx.Candles)-2]

		// Vela alcista (cierre > apertura) después de vela bajista
		if lastCandle.Close > lastCandle.Open && prevCandle.Close < prevCandle.Open {
			hasReversalConfirmation = true
			confidence += reversalBonus
			reasons = append(reasons, "Bullish reversal candle confirmed")
		}
	}

	// Check RSI oversold on primary timeframe
	if primaryCtx.Indicators.RSI < rsiOversoldThreshold {
		confidence += rsiPrimaryBonus
		correlatedDipBonus += rsiPrimaryBonus
		reasons = append(reasons, fmt.Sprintf("RSI %s oversold", primaryCtx.Timeframe))
	}

	// Check secondary TF RSI alignment with LONG direction.
	// Three zones: LOW (<bullishMax) = directional bonus; NEUTRAL = no bonus (optional penalty/veto);
	// HIGH (>secondaryMax) = contradicts LONG (penalty).
	if secondaryCtx != nil {
		rsiSec := secondaryCtx.Indicators.RSI
		switch {
		case rsiSec < rsiSecondaryBullishMax:
			confidence += rsiSecondaryBonus
			correlatedDipBonus += rsiSecondaryBonus
			reasons = append(reasons, fmt.Sprintf("RSI %s bullish pressure (%.1f)", secondaryCtx.Timeframe, rsiSec))
		case rsiSec < rsiSecondaryMax:
			// neutral zone — no bonus; was previously giving false +bonus ("healthy")
			if rsiSecondaryNeutralPenaltyLong != 0 {
				confidence += rsiSecondaryNeutralPenaltyLong
			}
			if rsiSecondaryNeutralVetoLong {
				return nil, fmt.Errorf("secondary TF RSI neutral (%.1f) — does not confirm LONG direction", rsiSec)
			}
		default:
			// RSI overbought on secondary TF — contradicts LONG.
			// At EXTREME overbought the bounce-down risk dominates any bullish
			// setup — hard veto (strongest contradiction must not weigh less
			// than the neutral-zone veto above).
			if rsiExtremeVetoEnabled && rsiSec >= rsiExtremeOverbought {
				return nil, fmt.Errorf("secondary TF RSI extreme overbought (%.1f ≥ %.1f) — vetoes LONG", rsiSec, rsiExtremeOverbought)
			}
			penalty := rsiContradictionPenalty
			if penalty == 0 {
				penalty = rsiSecondaryBonus
			}
			confidence -= penalty
			reasons = append(reasons, fmt.Sprintf("RSI %s overbought (%.1f) — contradicts LONG", secondaryCtx.Timeframe, rsiSec))
		}
	}

	// Check MACD bullish
	if primaryCtx.Indicators.MACD.Trend == "bullish" {
		confidence += macdTrendBonus
		correlatedDipBonus += macdTrendBonus
		reasons = append(reasons, fmt.Sprintf("MACD %s bullish", primaryCtx.Timeframe))
	}

	// Verificar soporte real con precio histórico
	atStrongSupport := false
	if de.contextBuilder.IsNearSupport(ctx, supportNearDistancePct) {
		// Verificar que el soporte ha sido testeado antes
		if ctx.KeyLevels != nil && ctx.KeyLevels.Support > 0 {
			supportDistance := ((ctx.CurrentPrice - ctx.KeyLevels.Support) / ctx.KeyLevels.Support) * 100
			if supportDistance < supportStrongThresholdPct {
				atStrongSupport = true
				confidence += supportStrongBonus
				correlatedDipBonus += supportStrongBonus
				reasons = append(reasons, fmt.Sprintf("At strong support level (%.2f%%)", supportDistance))
			} else {
				confidence += supportNearBonus
				correlatedDipBonus += supportNearBonus
				reasons = append(reasons, "Near support level")
			}
		}
	}

	// Check Bollinger position
	if primaryCtx.Indicators.Bollinger.Position == "lower" || primaryCtx.Indicators.Bollinger.Position == "outside_lower" {
		confidence += bollingerOuterBonus
		correlatedDipBonus += bollingerOuterBonus
		reasons = append(reasons, "Price at lower Bollinger")
	}

	// Filtro de volumen - requiere volumen creciente
	hasVolumeConfirmation := false
	if len(primaryCtx.Candles) >= 3 {
		lastCandle := primaryCtx.Candles[len(primaryCtx.Candles)-1]
		avgVolume := 0.0
		for i := len(primaryCtx.Candles) - 10; i < len(primaryCtx.Candles)-1; i++ {
			if i >= 0 {
				avgVolume += primaryCtx.Candles[i].Volume
			}
		}
		avgVolume /= 9.0

		if lastCandle.Volume > avgVolume*volConfirmRatio {
			hasVolumeConfirmation = true
			confidence += volConfirmBonus
			reasons = append(reasons, "Volume confirmation (above average)")
		} else if lastCandle.Volume < avgVolume*volLowRatio {
			confidence += volLowPenalty
			reasons = append(reasons, "⚠️ Low volume warning")
		}
	}

	// Trend-follow scoring (futures bidirectional): when the bot derived this as
	// the trend-ALIGNED side (pair in a macro uptrend), score a LONG on a healthy
	// pullback-in-uptrend (RSI in a mid-band + MACD bullish) instead of demanding
	// an oversold reversal at support — that reversal logic never fires on a pair
	// that is steadily climbing, which is exactly why the bot couldn't long
	// uptrends. Gated entirely on ctx.DirectionalMode=="trend_follow"; OFF =
	// legacy mean-reversion scoring is byte-identical.
	trendFollow := ctx.DirectionalMode == "trend_follow"
	if trendFollow {
		rsiLoBand, rsiHiBand, contBonus := 45.0, 70.0, 0.20
		requireMACD := true
		if cfg != nil {
			if v, ok := cfg.FloatOk("scoring.trend_follow.rsi_long_lo"); ok {
				rsiLoBand = v
			}
			if v, ok := cfg.FloatOk("scoring.trend_follow.rsi_long_hi"); ok {
				rsiHiBand = v
			}
			if v, ok := cfg.FloatOk("scoring.trend_follow.continuation_bonus"); ok {
				contBonus = v
			}
			if v, ok := cfg.BoolOk("scoring.trend_follow.require_macd"); ok {
				requireMACD = v
			}
		}
		// Require MACD momentum to AGREE with the trend (default on) so the bonus
		// fires on a genuine pullback-in-uptrend, not a stalling/rolling-over trend
		// — without this a falling-knife long could clear the threshold on RSI alone.
		rsi := primaryCtx.Indicators.RSI
		macdOK := !requireMACD || primaryCtx.Indicators.MACD.Trend == "bullish"
		if rsi >= rsiLoBand && rsi <= rsiHiBand && macdOK {
			confidence += contBonus
			reasons = append(reasons, fmt.Sprintf("Trend-follow: pullback in uptrend (RSI %.0f in [%.0f,%.0f], MACD bullish)", rsi, rsiLoBand, rsiHiBand))
		}
	}

	// Penalizar si no hay confirmaciones clave. Skipped in trend-follow mode: a
	// trend continuation entry does not require a reversal candle or being at
	// support — requiring them would veto every valid pullback-in-trend long.
	if !hasReversalConfirmation && !atStrongSupport && !trendFollow {
		confidence += missingConfirmPenalty
		reasons = append(reasons, "⚠️ Missing reversal confirmation")
	}

	// Anti-saturation cap: the five correlated dip-signal bonuses tracked above
	// all read the same underlying fact ("price at a local floor") and can stack
	// to confidence 1.0 on their own, masking that no INDEPENDENT confirmation
	// (reversal candle, volume) exists. Cap their combined contribution;
	// default ON, dynamically tunable/disableable for Anka Arena competition use.
	// The 1e-9 slack absorbs float64 addition error (0.10+0.20 > 0.30) so an
	// exactly-at-cap sum doesn't emit a spurious "capped" reason.
	if correlatedCapEnabled && correlatedDipBonus > correlatedCapMaxBonus+1e-9 {
		// Veto (default on): a saturated correlated stack with NO independent
		// confirmation (no reversal price-action, no above-average volume) is a
		// pure "it's cheap" bet — the exact fingerprint of 2026-07-02's losing
		// dip-buys (capped flag went 0/4 that afternoon). Dampening alone left
		// confidence ≥0.90, still clearing the entry threshold. Returns WAIT (like
		// the knife guard) so the veto is a normal no-trade decision, not an error.
		if correlatedCapVetoEnabled && !hasReversalConfirmation && !hasVolumeConfirmation {
			return &Decision{Action: "WAIT", Confidence: 0,
				Reason: fmt.Sprintf("🚫 correlated dip-signal stack %.2f exceeds cap %.2f with no independent confirmation — LONG vetoed", correlatedDipBonus, correlatedCapMaxBonus)}, nil
		}
		excess := correlatedDipBonus - correlatedCapMaxBonus
		confidence -= excess
		reasons = append(reasons, fmt.Sprintf("⚠️ correlated dip-signal bonus capped (%.2f > %.2f)", correlatedDipBonus, correlatedCapMaxBonus))
	}

	// Confidence is a probability-like score; additive factors can exceed 1.0
	// (e.g. reversal + RSI + strong support stack to 1.10), which distorts
	// downstream confidence-tier sizing. Cap at 1.0.
	if confidence > 1.0 {
		confidence = 1.0
	}

	// Hard veto: a strong opposing price-action read kills the entry outright,
	// regardless of how many other bonuses stacked up. Prevents shorting into a
	// bullish reversal (and vice-versa).
	if priceActionVeto {
		confidence = 0
		reasons = append(reasons, "🚫 entry vetoed: price-action contradicts long direction")
	}

	// Calculate entry parameters
	entry := ctx.CurrentPrice

	// Stop Loss dinámico basado en ATR
	atr := primaryCtx.Indicators.ATR
	if atr > 0 {
		stopLoss := entry - (atr * atrSLMultiplier)
		takeProfit := entry + (atr * atrTPMultiplier)

		// Ajustar basado en soporte/resistencia si están cerca.
		// In trend_follow the entry is a pullback in an uptrend so the recent high
		// (resistance) sits just overhead; clamping TP to it would set the target a
		// hair above entry → instant fee-eating close. Skip the TP-to-resistance
		// clamp in trend_follow (we follow the trend through it); keep the SL clamp.
		if ctx.KeyLevels != nil {
			if ctx.KeyLevels.Support > 0 && ctx.KeyLevels.Support > stopLoss {
				stopLoss = ctx.KeyLevels.Support * atrLevelBuffer
			}
			if !trendFollow && ctx.KeyLevels.Resistance > 0 && ctx.KeyLevels.Resistance < takeProfit {
				takeProfit = ctx.KeyLevels.Resistance * atrLevelBuffer
			}
		}

		return &Decision{
			Action:     "BUY",
			Confidence: confidence,
			Reason:     fmt.Sprintf("Bullish setup: %v", reasons),
			Entry:      entry,
			StopLoss:   stopLoss,
			TakeProfit: takeProfit,
			Size:       de.config.TradeSize,
			Leverage:   de.config.Leverage,
			Strategy:   "ai_multi_timeframe_enhanced",
		}, nil
	}

	// Fallback a stop loss fijo si no hay ATR
	stopLoss := entry * fallbackSLLong
	takeProfit := entry * fallbackTPLong

	// Adjust based on support/resistance (skip TP-to-resistance clamp in trend_follow)
	if ctx.KeyLevels != nil {
		if ctx.KeyLevels.Support > 0 {
			stopLoss = ctx.KeyLevels.Support * atrLevelBuffer
		}
		if !trendFollow && ctx.KeyLevels.Resistance > 0 {
			takeProfit = ctx.KeyLevels.Resistance * atrLevelBuffer
		}
	}

	return &Decision{
		Action:     "BUY",
		Confidence: confidence,
		Reason:     fmt.Sprintf("Bullish setup: %v", reasons),
		Entry:      entry,
		StopLoss:   stopLoss,
		TakeProfit: takeProfit,
		Size:       de.config.TradeSize,
		Leverage:   de.config.Leverage,
		Strategy:   "ai_multi_timeframe_enhanced",
	}, nil
}

// evaluateBearishSetup evaluates bearish entry setup
// REFACTORED: Now uses dynamic timeframe access and cfg snapshot for all thresholds
func (de *DecisionEngine) evaluateBearishSetup(ctx *MarketContext, cfg *dynamic.ScopedSnapshot) (*Decision, error) {
	// TC-4.2: read base confidence from dynamic config; fallback to 0.50
	baseConf := 0.50
	if cfg != nil {
		if v, ok := cfg.FloatOk("scoring.base_confidence"); ok {
			baseConf = v
		}
	}
	confidence := baseConf
	reasons := []string{}

	// Get primary context
	primaryCtx := ctx.GetPrimaryContext()
	if primaryCtx == nil {
		return nil, fmt.Errorf("primary context not available")
	}

	// Get secondary context (if available)
	var secondaryCtx *TimeframeContext
	secondaryCtxs := ctx.GetSecondaryContexts()
	if len(secondaryCtxs) > 0 {
		secondaryCtx = secondaryCtxs[0]
	}

	// Scoring thresholds from dynamic config
	reversalBonus := 0.15
	rsiPrimaryBonus := 0.15
	rsiOverboughtThreshold := 65.0
	rsiSecondaryMin := 35.0
	rsiSecondaryBonus := 0.10
	rsiSecondaryBearishMin := 55.0 // bearish: secondary RSI must be ABOVE this for directional bonus
	rsiSecondaryNeutralPenaltyShort := 0.0
	rsiSecondaryNeutralVetoShort := false
	rsiExtremeOversold := 15.0     // secondary RSI at/below this is an EXTREME contradiction for SHORT
	rsiExtremeVetoEnabled := false // hard-veto counter-direction entries at RSI extremes
	rsiContradictionPenalty := 0.0 // 0 → falls back to rsiSecondaryBonus (legacy behavior)
	macdTrendBonus := 0.10
	resistanceNearDistancePct := 2.0
	resistanceStrongThresholdPct := 1.0
	resistanceStrongBonus := 0.20
	resistanceNearBonus := 0.10
	bollingerOuterBonus := 0.10
	volConfirmRatio := 1.2
	volConfirmBonus := 0.10
	volLowRatio := 0.5
	volLowPenalty := -0.15
	missingConfirmPenalty := -0.10
	atrSLMultiplier := 1.5
	atrTPMultiplier := 2.5
	atrLevelBufferShort := 1.001
	fallbackSLShort := 1.003
	fallbackTPShort := 0.995
	correlatedCapEnabled := true
	correlatedCapMaxBonus := 0.30
	correlatedCapVetoEnabled := true

	if cfg != nil {
		if v, ok := cfg.FloatOk("scoring.reversal_candle_bonus"); ok {
			reversalBonus = v
		}
		if v, ok := cfg.FloatOk("scoring.rsi.primary_bonus"); ok {
			rsiPrimaryBonus = v
		}
		if v, ok := cfg.FloatOk("scoring.rsi.overbought_threshold"); ok {
			rsiOverboughtThreshold = v
		}
		if v, ok := cfg.FloatOk("scoring.rsi.secondary_min"); ok {
			rsiSecondaryMin = v
		}
		if v, ok := cfg.FloatOk("scoring.rsi.secondary_bonus"); ok {
			rsiSecondaryBonus = v
		}
		if v, ok := cfg.FloatOk("scoring.rsi.secondary_bearish_min"); ok {
			rsiSecondaryBearishMin = v
		}
		if v, ok := cfg.FloatOk("scoring.secondary_tf.neutral_penalty"); ok {
			rsiSecondaryNeutralPenaltyShort = v
		}
		if v, ok := cfg.BoolOk("scoring.secondary_tf.neutral_veto_enabled"); ok {
			rsiSecondaryNeutralVetoShort = v
		}
		if v, ok := cfg.FloatOk("scoring.rsi.extreme_oversold"); ok {
			rsiExtremeOversold = v
		}
		if v, ok := cfg.BoolOk("scoring.rsi.extreme_contradiction_veto_enabled"); ok {
			rsiExtremeVetoEnabled = v
		}
		if v, ok := cfg.FloatOk("scoring.rsi.contradiction_penalty"); ok {
			rsiContradictionPenalty = v
		}
		if v, ok := cfg.FloatOk("scoring.macd.trend_bonus"); ok {
			macdTrendBonus = v
		}
		if v, ok := cfg.FloatOk("scoring.resistance.near_distance_pct"); ok {
			resistanceNearDistancePct = v
		}
		if v, ok := cfg.FloatOk("scoring.resistance.strong_threshold_pct"); ok {
			resistanceStrongThresholdPct = v
		}
		if v, ok := cfg.FloatOk("scoring.resistance.strong_bonus"); ok {
			resistanceStrongBonus = v
		}
		if v, ok := cfg.FloatOk("scoring.resistance.near_bonus"); ok {
			resistanceNearBonus = v
		}
		if v, ok := cfg.FloatOk("scoring.bollinger.outer_band_bonus"); ok {
			bollingerOuterBonus = v
		}
		if v, ok := cfg.FloatOk("scoring.volume.confirmation_ratio"); ok {
			volConfirmRatio = v
		}
		if v, ok := cfg.FloatOk("scoring.volume.confirmation_bonus"); ok {
			volConfirmBonus = v
		}
		if v, ok := cfg.FloatOk("scoring.volume.low_ratio"); ok {
			volLowRatio = v
		}
		if v, ok := cfg.FloatOk("scoring.volume.low_penalty"); ok {
			volLowPenalty = v
		}
		if v, ok := cfg.FloatOk("scoring.missing_confirmation_penalty"); ok {
			missingConfirmPenalty = v
		}
		if v, ok := cfg.FloatOk("scoring.atr.sl_multiplier"); ok {
			atrSLMultiplier = v
		}
		if v, ok := cfg.FloatOk("scoring.atr.tp_multiplier"); ok {
			atrTPMultiplier = v
		}
		// Short side: 1.001 buffer (price slightly above resistance/support).
		// scoring.atr.level_buffer default=0.998 is for longs; for shorts we
		// use 2-fallbackSLShort as the symmetric buffer (1.001 when fallback=1.003).
		if v, ok := cfg.FloatOk("scoring.fallback.sl_pct_short"); ok {
			fallbackSLShort = v
		}
		if v, ok := cfg.FloatOk("scoring.fallback.tp_pct_short"); ok {
			fallbackTPShort = v
		}
		// Short ATR buffer: complement of level_buffer (0.998 → 1.002 for shorts).
		if v, ok := cfg.FloatOk("scoring.atr.level_buffer"); ok {
			atrLevelBufferShort = 2.0 - v // e.g. 2-0.998=1.002
		}
		if v, ok := cfg.BoolOk("scoring.correlated_cap.enabled"); ok {
			correlatedCapEnabled = v
		}
		if v, ok := cfg.FloatOk("scoring.correlated_cap.max_bonus"); ok && v > 0 {
			correlatedCapMaxBonus = v
		}
		if v, ok := cfg.BoolOk("scoring.correlated_cap.veto_enabled"); ok {
			correlatedCapVetoEnabled = v
		}
	}

	// Correlated dip-signal bonus tracker (mirror of evaluateBullishSetup): see
	// that function's comment — same overlapping-signal saturation risk applies
	// to shorts (RSI-primary-overbought, RSI-secondary-bearish, MACD-bearish,
	// strong-resistance, upper-Bollinger).
	correlatedDipBonus := 0.0

	// Reversal / continuation confirmation — graded price-action reader (mirror of
	// the bullish path): a bearish read confirms the short; a bullish read
	// contradicts it and is penalized. Falls back to the legacy colour flip.
	hasReversalConfirmation := false
	priceActionVeto := false
	if priceActionEnabled(cfg) {
		pa := DetectPriceAction(primaryCtx.Candles, priceActionParamsFromCfg(cfg))
		paWeight := priceActionBonusWeight(cfg)
		switch pa.Direction {
		case "bearish":
			if pa.Strength >= priceActionMinConfirmStrength(cfg) {
				hasReversalConfirmation = true
				confidence += paWeight * pa.Strength
				reasons = append(reasons, pa.Reason())
			} else {
				reasons = append(reasons, "price-action bearish but below min strength (ignored): "+pa.Reason())
			}
		case "bullish":
			confidence -= paWeight * pa.Strength * priceActionContradictFactor(cfg)
			reasons = append(reasons, "price-action contradicts short: "+pa.Reason())
			if priceActionContradictVeto(cfg) && pa.Strength >= priceActionContradictVetoStrength(cfg) {
				priceActionVeto = true
			}
		}
	} else if len(primaryCtx.Candles) >= 2 {
		lastCandle := primaryCtx.Candles[len(primaryCtx.Candles)-1]
		prevCandle := primaryCtx.Candles[len(primaryCtx.Candles)-2]

		if lastCandle.Close < lastCandle.Open && prevCandle.Close > prevCandle.Open {
			hasReversalConfirmation = true
			confidence += reversalBonus
			reasons = append(reasons, "Bearish reversal candle confirmed")
		}
	}

	// Check RSI overbought on primary timeframe
	if primaryCtx.Indicators.RSI > rsiOverboughtThreshold {
		confidence += rsiPrimaryBonus
		correlatedDipBonus += rsiPrimaryBonus
		reasons = append(reasons, fmt.Sprintf("RSI %s overbought", primaryCtx.Timeframe))
	}

	// Check secondary TF RSI alignment with SHORT direction.
	// Three zones: HIGH (>bearishMin) = directional bonus; NEUTRAL = no bonus (optional penalty/veto);
	// LOW (<secondaryMin) = contradicts SHORT (penalty).
	if secondaryCtx != nil {
		rsiSec := secondaryCtx.Indicators.RSI
		switch {
		case rsiSec > rsiSecondaryBearishMin:
			confidence += rsiSecondaryBonus
			correlatedDipBonus += rsiSecondaryBonus
			reasons = append(reasons, fmt.Sprintf("RSI %s bearish pressure (%.1f)", secondaryCtx.Timeframe, rsiSec))
		case rsiSec > rsiSecondaryMin:
			// neutral zone — no bonus; was previously giving false +bonus ("healthy")
			if rsiSecondaryNeutralPenaltyShort != 0 {
				confidence += rsiSecondaryNeutralPenaltyShort
			}
			if rsiSecondaryNeutralVetoShort {
				return nil, fmt.Errorf("secondary TF RSI neutral (%.1f) — does not confirm SHORT direction", rsiSec)
			}
		default:
			// RSI oversold on secondary TF — contradicts SHORT.
			// At EXTREME oversold the bounce-up risk dominates any bearish
			// setup — hard veto (strongest contradiction must not weigh less
			// than the neutral-zone veto above).
			if rsiExtremeVetoEnabled && rsiSec <= rsiExtremeOversold {
				return nil, fmt.Errorf("secondary TF RSI extreme oversold (%.1f ≤ %.1f) — vetoes SHORT", rsiSec, rsiExtremeOversold)
			}
			penalty := rsiContradictionPenalty
			if penalty == 0 {
				penalty = rsiSecondaryBonus
			}
			confidence -= penalty
			reasons = append(reasons, fmt.Sprintf("RSI %s oversold (%.1f) — contradicts SHORT", secondaryCtx.Timeframe, rsiSec))
		}
	}

	// Check MACD bearish
	if primaryCtx.Indicators.MACD.Trend == "bearish" {
		confidence += macdTrendBonus
		correlatedDipBonus += macdTrendBonus
		reasons = append(reasons, fmt.Sprintf("MACD %s bearish", primaryCtx.Timeframe))
	}

	// Check near resistance
	atStrongResistance := false
	if de.contextBuilder.IsNearResistance(ctx, resistanceNearDistancePct) {
		if ctx.KeyLevels != nil && ctx.KeyLevels.Resistance > 0 {
			resistanceDistance := ((ctx.KeyLevels.Resistance - ctx.CurrentPrice) / ctx.KeyLevels.Resistance) * 100
			if resistanceDistance < resistanceStrongThresholdPct {
				atStrongResistance = true
				confidence += resistanceStrongBonus
				correlatedDipBonus += resistanceStrongBonus
				reasons = append(reasons, fmt.Sprintf("At strong resistance level (%.2f%%)", resistanceDistance))
			} else {
				confidence += resistanceNearBonus
				correlatedDipBonus += resistanceNearBonus
				reasons = append(reasons, "Near resistance level")
			}
		}
	}

	// Check Bollinger position
	if primaryCtx.Indicators.Bollinger.Position == "upper" || primaryCtx.Indicators.Bollinger.Position == "outside_upper" {
		confidence += bollingerOuterBonus
		correlatedDipBonus += bollingerOuterBonus
		reasons = append(reasons, "Price at upper Bollinger")
	}

	hasVolumeConfirmation := false
	if len(primaryCtx.Candles) >= 3 {
		lastCandle := primaryCtx.Candles[len(primaryCtx.Candles)-1]
		avgVolume := 0.0
		for i := len(primaryCtx.Candles) - 10; i < len(primaryCtx.Candles)-1; i++ {
			if i >= 0 {
				avgVolume += primaryCtx.Candles[i].Volume
			}
		}
		avgVolume /= 9.0

		if lastCandle.Volume > avgVolume*volConfirmRatio {
			hasVolumeConfirmation = true
			confidence += volConfirmBonus
			reasons = append(reasons, "Volume confirmation (above average)")
		} else if lastCandle.Volume < avgVolume*volLowRatio {
			confidence += volLowPenalty
			reasons = append(reasons, "⚠️ Low volume warning")
		}
	}

	// Trend-follow scoring (futures bidirectional): when the bot derived this as
	// the trend-ALIGNED side (pair in a macro DOWNtrend), score a SHORT on a
	// healthy pullback-in-downtrend (RSI in a mid-band + MACD bearish) instead of
	// demanding an overbought reversal at resistance. Gated on
	// ctx.DirectionalMode=="trend_follow"; OFF = legacy scoring is byte-identical.
	trendFollow := ctx.DirectionalMode == "trend_follow"
	if trendFollow {
		rsiLoBand, rsiHiBand, contBonus := 30.0, 55.0, 0.20
		requireMACD := true
		if cfg != nil {
			if v, ok := cfg.FloatOk("scoring.trend_follow.rsi_short_lo"); ok {
				rsiLoBand = v
			}
			if v, ok := cfg.FloatOk("scoring.trend_follow.rsi_short_hi"); ok {
				rsiHiBand = v
			}
			if v, ok := cfg.FloatOk("scoring.trend_follow.continuation_bonus"); ok {
				contBonus = v
			}
			if v, ok := cfg.BoolOk("scoring.trend_follow.require_macd"); ok {
				requireMACD = v
			}
		}
		// Require MACD bearish (default on) so a short fires on a genuine
		// pullback-in-downtrend, not a bottoming/reversing move.
		rsi := primaryCtx.Indicators.RSI
		macdOK := !requireMACD || primaryCtx.Indicators.MACD.Trend == "bearish"
		if rsi >= rsiLoBand && rsi <= rsiHiBand && macdOK {
			confidence += contBonus
			reasons = append(reasons, fmt.Sprintf("Trend-follow: pullback in downtrend (RSI %.0f in [%.0f,%.0f], MACD bearish)", rsi, rsiLoBand, rsiHiBand))
		}
	}

	// Skipped in trend-follow mode (continuation entry needs no reversal candle).
	if !hasReversalConfirmation && !atStrongResistance && !trendFollow {
		confidence += missingConfirmPenalty
		reasons = append(reasons, "⚠️ Missing reversal confirmation")
	}

	// Anti-saturation cap (mirror of evaluateBullishSetup): cap the combined
	// contribution of the correlated dip/rally-signal bonuses. The 1e-9 slack
	// absorbs float64 addition error (0.10+0.20 > 0.30).
	if correlatedCapEnabled && correlatedDipBonus > correlatedCapMaxBonus+1e-9 {
		// Veto (default on) — see the bullish mirror for rationale.
		if correlatedCapVetoEnabled && !hasReversalConfirmation && !hasVolumeConfirmation {
			return &Decision{Action: "WAIT", Confidence: 0,
				Reason: fmt.Sprintf("🚫 correlated dip-signal stack %.2f exceeds cap %.2f with no independent confirmation — SHORT vetoed", correlatedDipBonus, correlatedCapMaxBonus)}, nil
		}
		excess := correlatedDipBonus - correlatedCapMaxBonus
		confidence -= excess
		reasons = append(reasons, fmt.Sprintf("⚠️ correlated dip-signal bonus capped (%.2f > %.2f)", correlatedDipBonus, correlatedCapMaxBonus))
	}

	// Confidence is a probability-like score; additive factors can exceed 1.0,
	// which distorts downstream confidence-tier sizing. Cap at 1.0.
	if confidence > 1.0 {
		confidence = 1.0
	}

	// Hard veto: a strong opposing price-action read kills the entry outright,
	// regardless of how many other bonuses stacked up. Prevents shorting into a
	// bullish reversal (and vice-versa).
	if priceActionVeto {
		confidence = 0
		reasons = append(reasons, "🚫 entry vetoed: price-action contradicts short direction")
	}

	// Calculate entry parameters
	entry := ctx.CurrentPrice

	atr := primaryCtx.Indicators.ATR
	if atr > 0 {
		stopLoss := entry + (atr * atrSLMultiplier)
		takeProfit := entry - (atr * atrTPMultiplier)

		// Skip the TP-to-support clamp in trend_follow (pullback in a downtrend sits
		// just above support; clamping TP to it sets the target a hair from entry →
		// instant close). Keep the SL-to-resistance clamp.
		if ctx.KeyLevels != nil {
			if ctx.KeyLevels.Resistance > 0 && ctx.KeyLevels.Resistance < stopLoss {
				stopLoss = ctx.KeyLevels.Resistance * atrLevelBufferShort
			}
			if !trendFollow && ctx.KeyLevels.Support > 0 && ctx.KeyLevels.Support > takeProfit {
				takeProfit = ctx.KeyLevels.Support * atrLevelBufferShort
			}
		}

		return &Decision{
			Action:     "SELL",
			Confidence: confidence,
			Reason:     fmt.Sprintf("Bearish setup: %v", reasons),
			Entry:      entry,
			StopLoss:   stopLoss,
			TakeProfit: takeProfit,
			Size:       de.config.TradeSize,
			Leverage:   de.config.Leverage,
			Strategy:   "ai_multi_timeframe_enhanced",
		}, nil
	}

	stopLoss := entry * fallbackSLShort
	takeProfit := entry * fallbackTPShort

	if ctx.KeyLevels != nil {
		if ctx.KeyLevels.Resistance > 0 {
			stopLoss = ctx.KeyLevels.Resistance * atrLevelBufferShort
		}
		if !trendFollow && ctx.KeyLevels.Support > 0 {
			takeProfit = ctx.KeyLevels.Support * atrLevelBufferShort
		}
	}

	return &Decision{
		Action:     "SELL",
		Confidence: confidence,
		Reason:     fmt.Sprintf("Bearish setup: %v", reasons),
		Entry:      entry,
		StopLoss:   stopLoss,
		TakeProfit: takeProfit,
		Size:       de.config.TradeSize,
		Leverage:   de.config.Leverage,
		Strategy:   "ai_multi_timeframe_enhanced",
	}, nil
}
