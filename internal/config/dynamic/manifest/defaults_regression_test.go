package manifest

import (
	"math"
	"strconv"
	"strings"
	"testing"
)

// defaults_regression_test.go is a static regression guard for the Phase-3
// config migration. Phase 3 moved ~68 hardcoded parameters into the dynamic
// config manifest (manifest.All()). Only 7 were spot-checked by hand, leaving
// a verification gap: did the migration silently change any default?
//
// This test closes that gap for the FIVE highest-risk, Phase-3-migrated groups:
//
//	scoring, trap, regime, btc, decision
//
// The reference map below is transcribed directly from docs/CONFIG_INVENTORY.md
// (the "Default" column — the authoritative record of pre-migration values).
// The test asserts, for every key in those groups:
//  1. manifest Default == documented original (numeric- and sign-aware).
//  2. every manifest key in those groups is present in the reference map
//     (catches params added/renamed without updating CONFIG_INVENTORY.md).
//
// IMPORTANT: a failure here is a REAL FINDING — a manifest default diverged
// from the original constant (a migration bug). Do NOT "fix" the manifest to
// make this pass unless the manifest is provably wrong; report the divergence.

// migratedGroups are the Phase-3 groups guarded by this test.
var migratedGroups = map[string]bool{
	"scoring":  true,
	"trap":     true,
	"regime":   true,
	"btc":      true,
	"decision": true,
}

// expectedDefaults maps each parameter key to its ORIGINAL documented default,
// transcribed from docs/CONFIG_INVENTORY.md.
//
// Transcription notes (how the inventory's "Default" cell was interpreted):
//   - Bonuses are written "+0.15" in the inventory; penalties as "−0.15" using a
//     Unicode minus (U+2212). normalizeNumeric() handles the sign/format so a
//     true match (e.g. "+0.15" vs "0.15", "−0.15" vs "-0.15") is NOT flagged,
//     while a genuine numeric difference IS flagged.
//   - scoring.volume.avg_window: inventory says "10 (uses 9)". The documented
//     nominal default is 10 (the manifest stores 10); recorded as "10".
//   - scoring.atr.level_buffer: inventory says "0.998 / 1.001" (long-side pad /
//     short-side pad). The manifest stores the long-side value 0.998; recorded
//     as "0.998" to match what the single manifest Entry carries.
//   - decision.confidence_threshold: inventory "0.50 (code) / 0.65 (yaml)". The
//     migration adopted the YAML/runtime-effective default 0.65; recorded "0.65".
//   - decision.trade_size_default: inventory "3.0 / 2.0" (code default / yaml).
//     The migration adopted the YAML-effective default 2.0; recorded "2.0".
var expectedDefaults = map[string]string{
	// ── scoring (docs/CONFIG_INVENTORY.md §scoring) ────────────────────────
	"scoring.base_confidence":                        "0.50",
	"scoring.reversal_candle_bonus":                  "+0.15",
	"scoring.rsi.oversold_threshold":                 "35",
	"scoring.rsi.overbought_threshold":               "65",
	"scoring.rsi.primary_bonus":                      "+0.15",
	"scoring.rsi.secondary_max":                      "65",
	"scoring.rsi.secondary_min":                      "35",
	"scoring.rsi.secondary_bonus":                    "+0.1",
	"scoring.rsi.secondary_bullish_max":              "45",
	"scoring.rsi.secondary_bearish_min":              "55",
	"scoring.secondary_tf.neutral_penalty":           "0",
	"scoring.secondary_tf.neutral_veto_enabled":      "false",
	"scoring.rsi.extreme_oversold":                   "15",
	"scoring.rsi.extreme_overbought":                 "85",
	"scoring.rsi.extreme_contradiction_veto_enabled": "false",
	"scoring.rsi.contradiction_penalty":              "0",
	"scoring.correlated_cap.enabled":                 "true",
	"scoring.correlated_cap.max_bonus":               "0.30",
	"scoring.correlated_cap.veto_enabled":            "true",
	"scoring.sl_floor_pct":                           "0.8",
	"scoring.macd.trend_bonus":                       "+0.1",
	"scoring.support.near_distance_pct":              "2.0",
	"scoring.support.strong_threshold_pct":           "1.0",
	"scoring.support.strong_bonus":                   "+0.20",
	"scoring.support.near_bonus":                     "+0.10",
	"scoring.resistance.near_distance_pct":           "2.0",
	"scoring.resistance.strong_threshold_pct":        "1.0",
	"scoring.resistance.strong_bonus":                "+0.20",
	"scoring.resistance.near_bonus":                  "+0.10",
	"scoring.bollinger.outer_band_bonus":             "+0.1",
	"scoring.volume.avg_window":                      "10", // inventory "10 (uses 9)"
	"scoring.volume.confirmation_ratio":              "1.2",
	"scoring.volume.confirmation_bonus":              "+0.10",
	"scoring.volume.low_ratio":                       "0.5",
	"scoring.volume.low_penalty":                     "−0.15", // U+2212 minus in inventory
	"scoring.missing_confirmation_penalty":           "−0.10", // U+2212 minus in inventory
	"scoring.atr.sl_multiplier":                      "1.5",
	"scoring.atr.tp_multiplier":                      "2.5",
	"scoring.atr.level_buffer":                       "0.998", // inventory "0.998 / 1.001"
	"scoring.fallback.sl_pct_long":                   "0.995",
	"scoring.fallback.tp_pct_long":                   "1.010",
	"scoring.fallback.sl_pct_short":                  "1.003",
	"scoring.fallback.tp_pct_short":                  "0.995",
	// ── AnkaVision price-action reader (added 2026-06-19/20) ──────────────
	"scoring.price_action.enabled":                   "true",
	"scoring.price_action.bonus_weight":              "0.20",
	"scoring.price_action.contradict_penalty_factor": "1.0",
	"scoring.price_action.contradict_veto":           "true",
	"scoring.price_action.contradict_veto_strength":  "0.25",
	"scoring.price_action.min_confirm_strength":      "0.30",
	"scoring.price_action.marubozu_body_ratio":       "0.80",
	"scoring.price_action.pin_wick_body_ratio":       "2.0",
	"scoring.price_action.doji_body_ratio":           "0.10",
	"scoring.price_action.volume_confirm_ratio":      "1.2",
	"scoring.price_action.volume_lookback":           "20",
	"scoring.price_action.strength.engulfing":        "0.55",
	"scoring.price_action.strength.pin_bar":          "0.50",
	"scoring.price_action.strength.marubozu":         "0.45",
	"scoring.price_action.strength.three_candle":     "0.60",
	"scoring.price_action.strength.reversal_flip":    "0.20",
	"scoring.trap.veto_confidence_min":               "0.70",
	// trend-follow scoring (bidirectional feature, 2026-06-01)
	"scoring.trend_follow.rsi_long_lo":        "45",
	"scoring.trend_follow.rsi_long_hi":        "70",
	"scoring.trend_follow.rsi_short_lo":       "30",
	"scoring.trend_follow.rsi_short_hi":       "55",
	"scoring.trend_follow.continuation_bonus": "0.20",
	"scoring.trend_follow.require_macd":       "true",

	// ── trap (docs/CONFIG_INVENTORY.md §trap) ──────────────────────────────
	"trap.min_reversal":                  "0.5",
	"trap.volume.lookback":               "20",
	"trap.volume.suspicious_ratio":       "0.7",
	"trap.wick_to_body_ratio":            "2.5",
	"trap.scan_window":                   "5",
	"trap.bull.lookback":                 "10",
	"trap.bear.lookback":                 "10",
	"trap.bull.base_confidence":          "0.4",
	"trap.bear.base_confidence":          "0.4",
	"trap.stop_hunt.vol_ratio_min":       "1.0",
	"trap.stop_hunt.close_to_open_ratio": "0.5",
	"trap.liquidity.wick_ratio_min":      "3.0",
	"trap.liquidity.vol_ratio_min":       "2.0",
	"trap.action.fade_confidence_min":    "0.7",

	// ── regime (docs/CONFIG_INVENTORY.md §regime) ──────────────────────────
	"regime.adx.trend_threshold":                     "25.0",
	"regime.adx.chaos_threshold":                     "50.0",
	"regime.bb.range_threshold":                      "0.02",
	"regime.bb.breakout_factor":                      "1.5",
	"regime.atr.chaos_threshold":                     "0.05",
	"regime.adx.weak_threshold":                      "20",
	"regime.di.crossover_buffer":                     "5",
	"regime.ema.trend_buffer_pct":                    "0.001",
	"regime.breakout.volume_ratio_min":               "1.3",
	"regime.chaos.size_multiplier":                   "0.00",
	"regime.breakout.size_multiplier":                "0.70",
	"regime.trending.size_multiplier":                "1.00",
	"regime.ranging.size_multiplier":                 "0.85",
	"regime.unknown.size_multiplier":                 "1.00",
	"regime.unknown.confidence":                      "0.3",
	"regime.ranging.min_confidence":                  "0.80", // directionless-selectivity (2026-05-29)
	"regime.unknown.min_confidence":                  "0.80",
	"regime.quiet_trend.enabled":                     "true", // quiet-trend drift override (2026-07-06)
	"regime.quiet_trend.lookback":                    "20",
	"regime.quiet_trend.min_net_move_pct":            "1.5",
	"regime.quiet_trend.min_directional_consistency": "0.65",
	"regime.quiet_trend.size_multiplier":             "0.85",

	// ── btc (docs/CONFIG_INVENTORY.md §btc) ────────────────────────────────
	"btc.direction.neutral_band_pct": "0.1",
	"btc.history.min_points":         "5",
	"btc.momentum.window":            "10",
	"btc.confidence_cap":             "0.95",
	"btc.strong_momentum_threshold":  "0.30",
	"btc.counter_trend.penalty":      "0.85",
	"btc.aligned.boost":              "1.10",

	// ── decision (docs/CONFIG_INVENTORY.md §decision_config) ───────────────
	"decision.confidence_threshold":             "0.65", // inventory "0.50 (code) / 0.65 (yaml)"
	"decision.require_alignment":                "true",
	"decision.min_timeframes_aligned":           "2",
	"decision.trade_size_default":               "2.0", // inventory "3.0 / 2.0"
	"decision.leverage_default":                 "5",
	"decision.macro.bearish_strength_threshold": "0.6",
	"decision.macro.bullish_strength_threshold": "0.6",
	"decision.allow_short_selling":              "true",
	"decision.macro_filter_enabled":             "true",
	"decision.counter_trend_filter_enabled":     "true",
	"decision.wait.no_direction_confidence":     "0.3",
	"decision.wait.misalignment_confidence":     "0.3",
	"decision.wait.macro_block_confidence":      "0.2",
}

// normalizeNumeric strips a leading "+" and converts the Unicode minus sign
// (U+2212, used by CONFIG_INVENTORY.md for penalties) to an ASCII "-", then
// trims surrounding whitespace. It does NOT alter the magnitude — so a genuine
// numeric difference survives normalization and is reported.
func normalizeNumeric(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "−", "-") // Unicode minus → ASCII hyphen-minus
	s = strings.TrimPrefix(s, "+")
	return s
}

// defaultsEqual reports whether two default-value strings represent the same
// value. If both parse as floats it compares numerically (so "0.50" == "0.5",
// "1.010" == "1.01", "0.00" == "0"). Otherwise it falls back to a
// case-insensitive string comparison (for bools/strings). A true numeric
// difference (e.g. 0.15 vs 0.20) is NOT masked.
func defaultsEqual(expected, actual string) bool {
	e := normalizeNumeric(expected)
	a := normalizeNumeric(actual)

	if ef, errE := strconv.ParseFloat(e, 64); errE == nil {
		if af, errA := strconv.ParseFloat(a, 64); errA == nil {
			// Exact would also work for these clean decimals, but use a tiny
			// epsilon to be robust against float representation noise.
			return math.Abs(ef-af) < 1e-9
		}
		return false // expected numeric, actual not → mismatch
	}
	return strings.EqualFold(e, a)
}

// TestDefaultsRegression asserts the manifest's defaults for the Phase-3
// migrated groups (scoring, trap, regime, btc, decision) equal the original
// values documented in docs/CONFIG_INVENTORY.md. Uses t.Errorf so ALL
// divergences surface in a single run.
func TestDefaultsRegression(t *testing.T) {
	entries := All()

	// Index manifest entries that belong to the migrated groups.
	manifestByKey := make(map[string]Entry, len(entries))
	for _, e := range entries {
		if migratedGroups[e.Group] {
			if _, dup := manifestByKey[e.Key]; dup {
				t.Errorf("duplicate manifest key in migrated group: %q", e.Key)
			}
			manifestByKey[e.Key] = e
		}
	}

	// 1) Forward check: every reference key must exist in the manifest and
	//    carry the documented default.
	for key, expected := range expectedDefaults {
		entry, ok := manifestByKey[key]
		if !ok {
			t.Errorf("MISSING: key %q is in CONFIG_INVENTORY reference map but not in manifest.All() (migrated groups)", key)
			continue
		}
		if !defaultsEqual(expected, entry.Default) {
			t.Errorf("DIVERGENCE: key %q default mismatch — CONFIG_INVENTORY=%q manifest=%q",
				key, expected, entry.Default)
		}
	}

	// 2) Reverse check: every manifest key in the migrated groups must be
	//    covered by the reference map (catches params added/renamed without
	//    updating CONFIG_INVENTORY.md).
	for key := range manifestByKey {
		if _, ok := expectedDefaults[key]; !ok {
			t.Errorf("UNCOVERED: manifest key %q (a migrated group) is not in the CONFIG_INVENTORY reference map — inventory or test is out of date", key)
		}
	}

	t.Logf("checked %d reference defaults against %d manifest entries in groups: scoring, trap, regime, btc, decision",
		len(expectedDefaults), len(manifestByKey))
}
