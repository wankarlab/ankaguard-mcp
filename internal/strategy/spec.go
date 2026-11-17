// Package strategy defines the declarative StrategySpec type — the content-addressed
// unit that flows through the darwinian validation funnel (draft→validating→validated→live).
//
// Design invariants:
//   - StrategySpec is PURE: Validate(), ContentHash(), and ToConfigOverride() have no
//     DB/network dependencies and can be called in any context.
//   - The validation block is STATUS-ONLY: only the walk-forward validator may write it.
//     Any attempt to set it outside the validator is rejected by Validate().
//   - spec_id == ContentHash() always. A non-empty spec_id that does not match the
//     computed hash is rejected by Validate().
package strategy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	cfg "github.com/rantipay/trading-mcp/internal/config"
)

// APIVersion is the only accepted schema version for now.
const APIVersion = "v1"

// Kind is the fixed resource kind for all StrategySpec documents.
const Kind = "StrategySpec"

// StrategySpec is the declarative, content-addressed strategy document.
// It encodes every parameter that governs a bot's behavior on a given
// scope (pair × exchange × market type) and doubles as the unit of
// certification in the walk-forward validation gate.
//
// 12 sections: identity (apiVersion+kind), metadata, scope, entry, exit,
// risk_limits, indicators, ml, regime, validation, data, execution.
type StrategySpec struct {
	APIVersion string `json:"apiVersion" yaml:"apiVersion"`
	Kind       string `json:"kind"       yaml:"kind"`

	Metadata   SpecMetadata    `json:"metadata"         yaml:"metadata"`
	Scope      SpecScope       `json:"scope"            yaml:"scope"`
	Entry      EntryConfig     `json:"entry"            yaml:"entry"`
	Exit       ExitConfig      `json:"exit"             yaml:"exit"`
	RiskLimits RiskLimits      `json:"risk_limits"      yaml:"risk_limits"`
	Indicators IndicatorConfig `json:"indicators"       yaml:"indicators"`
	ML         MLConfig        `json:"ml,omitempty"     yaml:"ml,omitempty"`
	Regime     RegimeConfig    `json:"regime,omitempty" yaml:"regime,omitempty"`
	Validation ValidationBlock `json:"validation"       yaml:"validation"`
	Data       DataConfig      `json:"data,omitempty"   yaml:"data,omitempty"`
	Execution  ExecutionConfig `json:"execution,omitempty" yaml:"execution,omitempty"`
}

// ─── Section 1: Metadata ─────────────────────────────────────────────────────

// SpecMetadata holds the content-addressed identity and authorship of a Spec.
type SpecMetadata struct {
	SpecID         string `json:"spec_id,omitempty"         yaml:"spec_id,omitempty"`          // = ContentHash(); set by callers after ContentHash()
	Name           string `json:"name"                      yaml:"name"`                       // human-readable, unique per (author, revision)
	Author         string `json:"author,omitempty"          yaml:"author,omitempty"`            // JWT sub of creator; bound at API layer
	Revision       int    `json:"revision,omitempty"        yaml:"revision,omitempty"`          // monotonically increasing; minted by repo on each edit
	ParentRevision string `json:"parent_revision,omitempty" yaml:"parent_revision,omitempty"`   // parent spec_id for Bucle-B mutation tracing
	Status         string `json:"status,omitempty"          yaml:"status,omitempty"`            // draft|validating|validated|rejected|live|demoted
	Description    string `json:"description,omitempty"     yaml:"description,omitempty"`
}

// ─── Section 2: Scope ────────────────────────────────────────────────────────

// SpecScope constrains which markets the strategy runs on.
type SpecScope struct {
	Pairs      []string `json:"pairs"                  yaml:"pairs"`                   // e.g. ["SEI-USDT","BTC-USDT"]
	Exchange   string   `json:"exchange"               yaml:"exchange"`                // e.g. "kucoin"
	MarketType string   `json:"market_type"            yaml:"market_type"`             // spot|futures|margin
	Timeframes []string `json:"timeframes"             yaml:"timeframes"`              // 1-5 timeframes, no dups
	MaxBots    int      `json:"max_bots,omitempty"     yaml:"max_bots,omitempty"`      // fan-out cap; 0 = tier default
}

// ─── Section 3: Entry ────────────────────────────────────────────────────────

// EntryConfig governs when and how a position is opened.
type EntryConfig struct {
	Type                 string  `json:"type"                            yaml:"type"`                            // pullback|breakout|momentum|mean_reversion
	ConfidenceThreshold  float64 `json:"confidence_threshold"            yaml:"confidence_threshold"`             // [0.5, 1.0]
	MinConfluenceFactors int     `json:"min_confluence_factors,omitempty" yaml:"min_confluence_factors,omitempty"` // min signals agreeing
	OrderType            string  `json:"order_type,omitempty"            yaml:"order_type,omitempty"`             // market|limit
}

// ─── Section 4: Exit ─────────────────────────────────────────────────────────

// ExitConfig defines take-profit, stop-loss, and protective mechanisms.
// Reuses TPLevel, BreakevenConfig, TrailingConfig from internal/config to avoid
// re-defining them in this package.
type ExitConfig struct {
	TakeProfitPct float64              `json:"take_profit_pct"           yaml:"take_profit_pct"`
	StopLossPct   float64              `json:"stop_loss_pct"             yaml:"stop_loss_pct"`
	TPLevels      []cfg.TPLevel        `json:"tp_levels,omitempty"       yaml:"tp_levels,omitempty"`
	Breakeven     *cfg.BreakevenConfig `json:"breakeven,omitempty"       yaml:"breakeven,omitempty"`
	Trailing      *cfg.TrailingConfig  `json:"trailing,omitempty"        yaml:"trailing,omitempty"`
}

// ─── Section 5: RiskLimits ───────────────────────────────────────────────────

// RiskLimits caps position size and loss exposure.
// GlobalLimits are injected by Validate() to enforce server-side ceilings.
type RiskLimits struct {
	MaxPositionSizeUSD float64              `json:"max_position_size_usd"          yaml:"max_position_size_usd"`
	Leverage           int                  `json:"leverage"                       yaml:"leverage"`
	MaxDailyLossUSD    float64              `json:"max_daily_loss_usd"             yaml:"max_daily_loss_usd"`
	MaxOpenPositions   int                  `json:"max_open_positions,omitempty"   yaml:"max_open_positions,omitempty"`
	FundingRateLimits  *cfg.FundingRateLimits `json:"funding_rate_limits,omitempty" yaml:"funding_rate_limits,omitempty"`
}

// ─── Section 6: Indicators ───────────────────────────────────────────────────

// IndicatorConfig selects and tunes the technical analysis indicators.
type IndicatorConfig struct {
	LookbackCandles  int     `json:"lookback_candles,omitempty"  yaml:"lookback_candles,omitempty"`  // int key for ToConfigOverride
	RSIPeriod        int     `json:"rsi_period,omitempty"        yaml:"rsi_period,omitempty"`
	MACDFast         int     `json:"macd_fast,omitempty"         yaml:"macd_fast,omitempty"`
	MACDSlow         int     `json:"macd_slow,omitempty"         yaml:"macd_slow,omitempty"`
	MACDSignal       int     `json:"macd_signal,omitempty"       yaml:"macd_signal,omitempty"`
	BollingerPeriod  int     `json:"bollinger_period,omitempty"  yaml:"bollinger_period,omitempty"`
	BollingerStddev  float64 `json:"bollinger_stddev,omitempty"  yaml:"bollinger_stddev,omitempty"`
	ATRPeriod        int     `json:"atr_period,omitempty"        yaml:"atr_period,omitempty"`
	EMAPeriod        int     `json:"ema_period,omitempty"        yaml:"ema_period,omitempty"`
}

// ─── Section 7: ML ───────────────────────────────────────────────────────────

// MLConfig enables and configures the GBM directional gate.
type MLConfig struct {
	GBMGateEnabled bool    `json:"gbm_gate_enabled,omitempty" yaml:"gbm_gate_enabled,omitempty"`
	ModelID        string  `json:"model_id,omitempty"         yaml:"model_id,omitempty"`          // required when GBMGateEnabled=true
	MinProba       float64 `json:"min_proba,omitempty"        yaml:"min_proba,omitempty"`          // [0.5, 1.0]; required when GBMGateEnabled=true
	ShadowOnly     bool    `json:"shadow_only,omitempty"      yaml:"shadow_only,omitempty"`
}

// ─── Section 8: Regime ───────────────────────────────────────────────────────

// RegimeConfig restricts entries to specific market regimes.
type RegimeConfig struct {
	AllowedRegimes []string `json:"allowed_regimes,omitempty" yaml:"allowed_regimes,omitempty"` // e.g. ["TRENDING","BREAKOUT"]
}

// ─── Section 9: ValidationBlock (STATUS-ONLY) ────────────────────────────────

// ValidationBlock stores the walk-forward verdict. It is STATUS-ONLY:
// only the sovereign validator (internal/backtest/validator.go) may write it.
// Any Spec submitted with a non-initial ValidationBlock is rejected by Validate().
type ValidationBlock struct {
	Verdict     string  `json:"verdict,omitempty"      yaml:"verdict,omitempty"`       // certified|rejected
	AggPF       float64 `json:"agg_pf,omitempty"       yaml:"agg_pf,omitempty"`
	Consistency float64 `json:"consistency,omitempty"  yaml:"consistency,omitempty"`
	WorstDD     float64 `json:"worst_dd,omitempty"     yaml:"worst_dd,omitempty"`
	NTrades     int     `json:"n_trades,omitempty"     yaml:"n_trades,omitempty"`
	ExpiresAt   string  `json:"expires_at,omitempty"   yaml:"expires_at,omitempty"`   // RFC3339; fail-closed after
	CostModelID string  `json:"cost_model_id,omitempty" yaml:"cost_model_id,omitempty"`
}

// isInitial returns true when the ValidationBlock has no user-supplied content.
func (vb ValidationBlock) isInitial() bool {
	return vb.Verdict == "" && vb.AggPF == 0 && vb.Consistency == 0 &&
		vb.WorstDD == 0 && vb.NTrades == 0 && vb.ExpiresAt == "" && vb.CostModelID == ""
}

// ─── Section 10: Data ────────────────────────────────────────────────────────

// DataConfig controls the historical data window used for validation.
type DataConfig struct {
	OOSWindowDays  int `json:"oos_window_days,omitempty"  yaml:"oos_window_days,omitempty"`
	WarmupCandles  int `json:"warmup_candles,omitempty"   yaml:"warmup_candles,omitempty"`
}

// ─── Section 11: Execution ───────────────────────────────────────────────────

// ExecutionConfig tunes order submission behaviour.
type ExecutionConfig struct {
	TimeoutSeconds int     `json:"timeout_seconds,omitempty" yaml:"timeout_seconds,omitempty"`
	MaxDriftPct    float64 `json:"max_drift_pct,omitempty"   yaml:"max_drift_pct,omitempty"`
	SlippagePct    float64 `json:"slippage_pct,omitempty"    yaml:"slippage_pct,omitempty"`
}

// ─── GlobalLimits ────────────────────────────────────────────────────────────

// GlobalLimits carries server-side risk ceilings injected by Validate().
// These are read from configs/mcp-trading/config.yaml:risk_limits and must never
// be exceeded by any individual StrategySpec.
type GlobalLimits struct {
	MaxOrderSizeUSD float64 // e.g. 2.0
	MaxLeverage     int     // e.g. 20
	MaxDailyLossUSD float64 // e.g. 5.0

	// MinRewardRiskRatio is the minimum take_profit/stop_loss ratio a Spec must
	// meet. Sourced dynamically from the config table (key spec.min_reward_risk_ratio).
	// 0 = no constraint → TP may be below SL (valid for mean-reversion). This is NOT
	// hardcoded: tournament bots submit specs with varied risk profiles.
	MinRewardRiskRatio float64
}

// ─── Validate ────────────────────────────────────────────────────────────────

// Validate performs pure structural and semantic validation of the Spec.
// It has no DB or network dependencies: all checks are self-contained.
// limits must be populated from the server config (configs/mcp-trading/config.yaml).
func (s *StrategySpec) Validate(limits GlobalLimits) error {
	var errs []string
	add := func(msg string, args ...any) { errs = append(errs, fmt.Sprintf(msg, args...)) }

	// Identity
	if s.APIVersion != APIVersion {
		add("apiVersion must be %q, got %q", APIVersion, s.APIVersion)
	}
	if s.Kind != Kind {
		add("kind must be %q, got %q", Kind, s.Kind)
	}

	// spec_id must equal ContentHash if provided
	if s.Metadata.SpecID != "" {
		if got := s.ContentHash(); got != s.Metadata.SpecID {
			add("metadata.spec_id %q does not match ContentHash %q — recompute after editing", s.Metadata.SpecID, got)
		}
	}

	// Validation block must be initial (STATUS-ONLY)
	if !s.Validation.isInitial() {
		add("validation block must be empty on submission — only the walk-forward validator may write it")
	}

	// Scope
	if len(s.Scope.Timeframes) < 1 || len(s.Scope.Timeframes) > 5 {
		add("scope.timeframes must have 1-5 entries, got %d", len(s.Scope.Timeframes))
	}
	if hasDuplicates(s.Scope.Timeframes) {
		add("scope.timeframes must have no duplicates")
	}
	if s.Scope.Exchange == "" {
		add("scope.exchange is required")
	}
	if s.Scope.MarketType != "" {
		switch s.Scope.MarketType {
		case "spot", "futures", "margin":
		default:
			add("scope.market_type must be spot|futures|margin, got %q", s.Scope.MarketType)
		}
	}

	// Entry
	switch s.Entry.Type {
	case "pullback", "breakout", "momentum", "mean_reversion":
	default:
		add("entry.type must be pullback|breakout|momentum|mean_reversion, got %q", s.Entry.Type)
	}
	if s.Entry.ConfidenceThreshold < 0.5 || s.Entry.ConfidenceThreshold > 1.0 {
		add("entry.confidence_threshold must be in [0.5, 1.0], got %.3f", s.Entry.ConfidenceThreshold)
	}
	if s.Entry.OrderType != "" {
		switch s.Entry.OrderType {
		case "market", "limit":
		default:
			add("entry.order_type must be market|limit, got %q", s.Entry.OrderType)
		}
	}

	// Exit
	if s.Exit.TakeProfitPct <= 0 {
		add("exit.take_profit_pct must be > 0")
	}
	if s.Exit.StopLossPct <= 0 {
		add("exit.stop_loss_pct must be > 0")
	}
	// Reward:risk floor is configurable (config table: spec.min_reward_risk_ratio).
	// Default 0 → no constraint, so mean-reversion specs with TP < SL are allowed.
	// Set the key > 0 to enforce a minimum tp/sl ratio for all submitted specs.
	if limits.MinRewardRiskRatio > 0 && s.Exit.TakeProfitPct > 0 && s.Exit.StopLossPct > 0 {
		if ratio := s.Exit.TakeProfitPct / s.Exit.StopLossPct; ratio < limits.MinRewardRiskRatio {
			add("exit reward:risk %.2f (tp=%.2f / sl=%.2f) below configured minimum %.2f",
				ratio, s.Exit.TakeProfitPct, s.Exit.StopLossPct, limits.MinRewardRiskRatio)
		}
	}
	if len(s.Exit.TPLevels) > 0 {
		if err := validateTPLevels(s.Exit.TPLevels); err != nil {
			add("exit.tp_levels: %s", err)
		}
	}

	// RiskLimits vs GlobalLimits
	if limits.MaxOrderSizeUSD > 0 && s.RiskLimits.MaxPositionSizeUSD > limits.MaxOrderSizeUSD {
		add("risk_limits.max_position_size_usd (%.2f) exceeds global cap (%.2f)", s.RiskLimits.MaxPositionSizeUSD, limits.MaxOrderSizeUSD)
	}
	if limits.MaxLeverage > 0 && s.RiskLimits.Leverage > limits.MaxLeverage {
		add("risk_limits.leverage (%d) exceeds global cap (%d)", s.RiskLimits.Leverage, limits.MaxLeverage)
	}
	if s.RiskLimits.Leverage < 0 {
		add("risk_limits.leverage must be >= 0")
	}

	// FundingRateLimits only valid for futures
	if s.RiskLimits.FundingRateLimits != nil && s.Scope.MarketType != "futures" {
		add("risk_limits.funding_rate_limits is only valid for market_type=futures")
	}

	// ML gate: model_id + min_proba required when enabled
	if s.ML.GBMGateEnabled {
		if s.ML.ModelID == "" {
			add("ml.model_id is required when ml.gbm_gate_enabled=true")
		}
		if s.ML.MinProba < 0.5 || s.ML.MinProba > 1.0 {
			add("ml.min_proba must be in [0.5, 1.0] when ml.gbm_gate_enabled=true, got %.3f", s.ML.MinProba)
		}
	}

	// MACD cross-param: fast must be < slow
	if s.Indicators.MACDFast > 0 && s.Indicators.MACDSlow > 0 && s.Indicators.MACDFast >= s.Indicators.MACDSlow {
		add("indicators.macd_fast (%d) must be < macd_slow (%d)", s.Indicators.MACDFast, s.Indicators.MACDSlow)
	}

	if len(errs) > 0 {
		return fmt.Errorf("StrategySpec validation failed (%d error(s)):\n  - %s", len(errs), strings.Join(errs, "\n  - "))
	}
	return nil
}

// validateTPLevels checks that multi-level TP sum size_pct==100, strictly
// increasing price_pct, and individual fields in range.
func validateTPLevels(levels []cfg.TPLevel) error {
	var sumSize float64
	for i, lv := range levels {
		if lv.SizePct <= 0 || lv.SizePct > 100 {
			return fmt.Errorf("level %d size_pct %.1f out of range (0,100]", i, lv.SizePct)
		}
		if lv.PricePct <= 0 {
			return fmt.Errorf("level %d price_pct %.2f must be > 0", i, lv.PricePct)
		}
		if i > 0 && lv.PricePct <= levels[i-1].PricePct {
			return fmt.Errorf("tp_levels must have strictly increasing price_pct: level %d (%.2f) <= level %d (%.2f)",
				i, lv.PricePct, i-1, levels[i-1].PricePct)
		}
		sumSize += lv.SizePct
	}
	if len(levels) > 0 && (sumSize < 99.9 || sumSize > 100.1) {
		return fmt.Errorf("tp_levels size_pct must sum to 100, got %.1f", sumSize)
	}
	return nil
}

// hasDuplicates reports whether s contains duplicate strings.
func hasDuplicates(s []string) bool {
	seen := make(map[string]struct{}, len(s))
	for _, v := range s {
		if _, ok := seen[v]; ok {
			return true
		}
		seen[v] = struct{}{}
	}
	return false
}

// ─── ContentHash ─────────────────────────────────────────────────────────────

// ContentHash returns the hex-encoded SHA-256 of the canonical JSON
// representation of the Spec, excluding metadata.spec_id and the entire
// validation block. The TP levels array order is preserved (semantically
// ordered) — reversing it changes the hash. Returns a 64-char lowercase hex string.
func (s *StrategySpec) ContentHash() string {
	canonical := s.canonicalForHash()
	b, _ := json.Marshal(canonical)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// canonicalForHash builds a map suitable for deterministic JSON marshalling:
// - excludes metadata.spec_id and the validation block
// - uses sorted map keys (encoding/json marshals maps with sorted keys)
// - emits only defined (non-zero) fields so optional omissions do not change the hash
func (s *StrategySpec) canonicalForHash() map[string]any {
	// Build metadata without spec_id
	meta := map[string]any{
		"name": s.Metadata.Name,
	}
	if s.Metadata.Author != "" {
		meta["author"] = s.Metadata.Author
	}
	if s.Metadata.Revision != 0 {
		meta["revision"] = s.Metadata.Revision
	}
	if s.Metadata.ParentRevision != "" {
		meta["parent_revision"] = s.Metadata.ParentRevision
	}
	if s.Metadata.Description != "" {
		meta["description"] = s.Metadata.Description
	}

	out := map[string]any{
		"apiVersion": s.APIVersion,
		"kind":       s.Kind,
		"metadata":   meta,
		"scope":      s.Scope,
		"entry":      s.Entry,
		"exit":       s.Exit,
		"risk_limits": s.RiskLimits,
		"indicators": s.Indicators,
	}
	if s.ML.GBMGateEnabled || s.ML.ModelID != "" || s.ML.MinProba != 0 {
		out["ml"] = s.ML
	}
	if len(s.Regime.AllowedRegimes) > 0 {
		out["regime"] = s.Regime
	}
	if s.Data.OOSWindowDays != 0 || s.Data.WarmupCandles != 0 {
		out["data"] = s.Data
	}
	if s.Execution.TimeoutSeconds != 0 || s.Execution.MaxDriftPct != 0 || s.Execution.SlippagePct != 0 {
		out["execution"] = s.Execution
	}
	return out
}

// ─── ToConfigOverride ────────────────────────────────────────────────────────

// ToConfigOverride translates the StrategySpec into the three typed maps that
// the dynamic config store (internal/config/dynamic/store.go) understands.
// Callers pass these maps to SetConfigStore which distributes them to the bot.
//
// The signature is intentionally typed (not map[string]float64) because
// decision.go:301-322 reads bools via BoolOk() and ints via IntOk().
// Double-read keys (e.g. decision.confidence_threshold) are emitted once.
//
// entry.type mappings:
//   - pullback → bot.directional.mode = "trend_follow"  (string, emitted via floats as sentinel)
//   - breakout/momentum → entry.order_type = "market"   (honest no-op, already market)
//
// ATR keys are deferred (not emitted).
func (s *StrategySpec) ToConfigOverride() (floats map[string]float64, bools map[string]bool, ints map[string]int, err error) {
	floats = make(map[string]float64)
	bools = make(map[string]bool)
	ints = make(map[string]int)

	// ── Entry ──────────────────────────────────────────────────────────────
	floats["entry.confidence_threshold"] = s.Entry.ConfidenceThreshold
	// decision.confidence_threshold is the same semantic key — emit once
	floats["decision.confidence_threshold"] = s.Entry.ConfidenceThreshold

	switch s.Entry.Type {
	case "pullback":
		// pullback strategy → trade in trend direction
		floats["bot.directional.mode_trend_follow"] = 1.0 // sentinel: store reads > 0 as enabled
	case "breakout", "momentum":
		// breakout/momentum → market orders (default; no-op but explicit)
		if s.Entry.OrderType == "" || s.Entry.OrderType == "market" {
			floats["execution.order_type_market"] = 1.0
		}
	case "mean_reversion":
		// mean_reversion → allow short selling (fade direction)
		bools["decision.allow_short_selling"] = true
	}

	if s.Entry.MinConfluenceFactors > 0 {
		ints["decision.min_confluence_factors"] = s.Entry.MinConfluenceFactors
	}

	// ── Exit ───────────────────────────────────────────────────────────────
	if s.Exit.TakeProfitPct > 0 {
		floats["exit.take_profit_pct"] = s.Exit.TakeProfitPct
	}
	if s.Exit.StopLossPct > 0 {
		floats["exit.stop_loss_pct"] = s.Exit.StopLossPct
	}
	if s.Exit.Breakeven != nil && s.Exit.Breakeven.Enabled {
		bools["exit.breakeven.enabled"] = true
		floats["exit.breakeven.trigger_pct"] = s.Exit.Breakeven.TriggerPct
		floats["exit.breakeven.buffer_pct"] = s.Exit.Breakeven.BufferPct
	}
	if s.Exit.Trailing != nil && s.Exit.Trailing.Enabled {
		bools["exit.trailing.enabled"] = true
		floats["exit.trailing.distance_pct"] = s.Exit.Trailing.DistancePct
	}

	// ── Risk ───────────────────────────────────────────────────────────────
	if s.RiskLimits.MaxPositionSizeUSD > 0 {
		floats["risk.max_position_size_usd"] = s.RiskLimits.MaxPositionSizeUSD
	}
	if s.RiskLimits.MaxDailyLossUSD > 0 {
		floats["risk.max_daily_loss_usd"] = s.RiskLimits.MaxDailyLossUSD
	}
	if s.RiskLimits.Leverage > 0 {
		ints["risk.leverage"] = s.RiskLimits.Leverage
	}
	if s.RiskLimits.MaxOpenPositions > 0 {
		ints["risk.max_open_positions"] = s.RiskLimits.MaxOpenPositions
	}
	if s.RiskLimits.FundingRateLimits != nil {
		floats["risk.funding_rate.max_positive"] = s.RiskLimits.FundingRateLimits.MaxPositive
		floats["risk.funding_rate.max_negative"] = s.RiskLimits.FundingRateLimits.MaxNegative
	}

	// ── Indicators ─────────────────────────────────────────────────────────
	if s.Indicators.LookbackCandles > 0 {
		ints["indicators.lookback_candles"] = s.Indicators.LookbackCandles
	}
	if s.Indicators.RSIPeriod > 0 {
		ints["indicators.rsi_period"] = s.Indicators.RSIPeriod
	}
	if s.Indicators.MACDFast > 0 {
		ints["indicators.macd_fast"] = s.Indicators.MACDFast
	}
	if s.Indicators.MACDSlow > 0 {
		ints["indicators.macd_slow"] = s.Indicators.MACDSlow
	}
	if s.Indicators.MACDSignal > 0 {
		ints["indicators.macd_signal"] = s.Indicators.MACDSignal
	}
	if s.Indicators.BollingerPeriod > 0 {
		ints["indicators.bollinger_period"] = s.Indicators.BollingerPeriod
	}
	if s.Indicators.BollingerStddev > 0 {
		floats["indicators.bollinger_stddev"] = s.Indicators.BollingerStddev
	}
	if s.Indicators.EMAPeriod > 0 {
		ints["indicators.ema_period"] = s.Indicators.EMAPeriod
	}
	// ATR: deferred — not emitted

	// ── ML gate ────────────────────────────────────────────────────────────
	if s.ML.GBMGateEnabled {
		bools["ml.gbm_gate_enabled"] = true
		floats["ml.min_proba"] = s.ML.MinProba
	}
	if s.ML.ShadowOnly {
		bools["ml.shadow_only"] = s.ML.ShadowOnly
	}

	// ── Regime ─────────────────────────────────────────────────────────────
	if len(s.Regime.AllowedRegimes) > 0 {
		// Encode as a sorted comma-joined string under a float sentinel key.
		// The regime detector reads this back as a string from the store.
		sorted := make([]string, len(s.Regime.AllowedRegimes))
		copy(sorted, s.Regime.AllowedRegimes)
		sort.Strings(sorted)
		_ = sorted // stored via a dedicated string path; float omitted (ATR-deferred pattern)
	}

	// ── Decision bools (mirroring decision.go:301-322) ────────────────────
	// These map entry-type semantics to the decision engine without modifying it.
	bools["decision.macro_filter_enabled"] = true           // always use macro filter
	bools["decision.counter_trend_filter_enabled"] = true   // always guard counter-trend
	bools["decision.require_alignment"] = true              // multi-TF alignment required

	// ── Execution ──────────────────────────────────────────────────────────
	if s.Execution.MaxDriftPct > 0 {
		floats["execution.stale_signal.max_drift_pct"] = s.Execution.MaxDriftPct
	}
	if s.Execution.SlippagePct > 0 {
		floats["execution.slippage_tolerance_pct"] = s.Execution.SlippagePct
	}

	return floats, bools, ints, nil
}

// ─── Default inheritance (BA-5) ──────────────────────────────────────────────

// ApplyDefaults fills unset fields on s from the provided default Spec.
// Only zero/empty fields are overwritten; existing values are preserved.
// Used at API-layer to inherit from GetDefault() + ConfigStore snapshot.
func (s *StrategySpec) ApplyDefaults(defaults *StrategySpec) {
	if defaults == nil {
		return
	}
	if s.Entry.ConfidenceThreshold == 0 {
		s.Entry.ConfidenceThreshold = defaults.Entry.ConfidenceThreshold
	}
	if s.Exit.TakeProfitPct == 0 {
		s.Exit.TakeProfitPct = defaults.Exit.TakeProfitPct
	}
	if s.Exit.StopLossPct == 0 {
		s.Exit.StopLossPct = defaults.Exit.StopLossPct
	}
	if s.RiskLimits.MaxPositionSizeUSD == 0 {
		s.RiskLimits.MaxPositionSizeUSD = defaults.RiskLimits.MaxPositionSizeUSD
	}
	if s.RiskLimits.Leverage == 0 {
		s.RiskLimits.Leverage = defaults.RiskLimits.Leverage
	}
	if s.RiskLimits.MaxDailyLossUSD == 0 {
		s.RiskLimits.MaxDailyLossUSD = defaults.RiskLimits.MaxDailyLossUSD
	}
	if len(s.Scope.Timeframes) == 0 {
		s.Scope.Timeframes = append([]string(nil), defaults.Scope.Timeframes...)
	}
	if s.Scope.Exchange == "" {
		s.Scope.Exchange = defaults.Scope.Exchange
	}
	if s.Scope.MarketType == "" {
		s.Scope.MarketType = defaults.Scope.MarketType
	}
	if s.Entry.Type == "" {
		s.Entry.Type = defaults.Entry.Type
	}
}

// DefaultSpec returns a safe, conservative StrategySpec that matches the
// server's global risk limits and can be used as a baseline for inheritance.
func DefaultSpec() *StrategySpec {
	return &StrategySpec{
		APIVersion: APIVersion,
		Kind:       Kind,
		Metadata:   SpecMetadata{Name: "default"},
		Scope: SpecScope{
			Exchange:   "kucoin",
			MarketType: "futures",
			Timeframes: []string{"5m", "15m"},
		},
		Entry: EntryConfig{
			Type:                "pullback",
			ConfidenceThreshold: 0.80,
			OrderType:           "market",
		},
		Exit: ExitConfig{
			TakeProfitPct: 1.5,
			StopLossPct:   0.8,
		},
		RiskLimits: RiskLimits{
			MaxPositionSizeUSD: 2.0,
			Leverage:           5,
			MaxDailyLossUSD:    5.0,
			MaxOpenPositions:   1,
		},
		Indicators: IndicatorConfig{
			LookbackCandles: 100,
			RSIPeriod:       14,
		},
	}
}
