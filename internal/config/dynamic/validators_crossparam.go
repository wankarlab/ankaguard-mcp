package dynamic

import (
	"encoding/json"
	"fmt"
	"sort"
)

// CrossParamValidator validates a set of related parameters that must satisfy invariants
// relative to each other. It receives a (possibly virtual) Snapshot plus the scope chain
// to use for resolution.
type CrossParamValidator func(snap *Snapshot, scope []ScopeRef) error

// crossValidators is the ordered list of cross-param validators executed during BulkSet.
var crossValidators = []CrossParamValidator{
	validateMACDPeriods,
	validateRegimeThresholds,
	validateBreakeven,
	validateDynamicSizingTiers,
	validatePartialTPLevels,
	validateVolatilityBand,
	validateMarketTypeLeverage,
}

// runCrossValidators runs all registered cross-param validators and returns the first error.
func runCrossValidators(snap *Snapshot, scope []ScopeRef) error {
	for _, v := range crossValidators {
		if err := v(snap, scope); err != nil {
			return err
		}
	}
	return nil
}

// validateMACDPeriods enforces fast_period < slow_period.
func validateMACDPeriods(snap *Snapshot, scope []ScopeRef) error {
	ss := &ScopedSnapshot{Snapshot: snap, chain: scope}
	fast := ss.Int("indicators.macd.fast_period")
	slow := ss.Int("indicators.macd.slow_period")
	// Skip if both are zero (key not configured in this snapshot).
	if fast == 0 && slow == 0 {
		return nil
	}
	if fast >= slow {
		return fmt.Errorf("macd.fast_period (%d) must be < slow_period (%d)", fast, slow)
	}
	return nil
}

// validateRegimeThresholds enforces adx.trend_threshold < adx.chaos_threshold.
func validateRegimeThresholds(snap *Snapshot, scope []ScopeRef) error {
	ss := &ScopedSnapshot{Snapshot: snap, chain: scope}
	trend := ss.Float("regime.adx.trend_threshold")
	chaos := ss.Float("regime.adx.chaos_threshold")
	if trend == 0 && chaos == 0 {
		return nil
	}
	if trend >= chaos {
		return fmt.Errorf("regime.adx.trend_threshold (%.4f) must be < chaos_threshold (%.4f)", trend, chaos)
	}
	return nil
}

// validateBreakeven enforces 0 ≤ buffer_pct < trigger_pct.
func validateBreakeven(snap *Snapshot, scope []ScopeRef) error {
	ss := &ScopedSnapshot{Snapshot: snap, chain: scope}
	trigger := ss.Float("scalping.breakeven.trigger_pct")
	buffer := ss.Float("scalping.breakeven.buffer_pct")
	if trigger == 0 && buffer == 0 {
		return nil
	}
	if buffer < 0 {
		return fmt.Errorf("breakeven.buffer_pct (%.4f) must be >= 0", buffer)
	}
	if buffer >= trigger {
		return fmt.Errorf("breakeven.buffer_pct (%.4f) must be < trigger_pct (%.4f)", buffer, trigger)
	}
	return nil
}

// sizingTier represents a single dynamic-sizing tier as stored in JSON.
type sizingTier struct {
	MinScore float64 `json:"min_score"`
	MaxScore float64 `json:"max_score"`
	SizePct  float64 `json:"size_pct"`
}

// validateDynamicSizingTiers checks that tiers have no overlaps and together cover [0.65, 1.0].
// If the JSON is absent or unparseable, the validator passes silently (best-effort).
func validateDynamicSizingTiers(snap *Snapshot, scope []ScopeRef) error {
	ss := &ScopedSnapshot{Snapshot: snap, chain: scope}
	raw := ss.String("scalping.dynamic_sizing.tiers")
	if raw == "" {
		return nil
	}

	var tiers []sizingTier
	if err := json.Unmarshal([]byte(raw), &tiers); err != nil {
		// Unparseable JSON — skip, will be caught by JSON schema validator later.
		return nil
	}
	if len(tiers) == 0 {
		return nil
	}

	// Sort by min_score so we can check coverage and overlaps in order.
	sort.Slice(tiers, func(i, j int) bool {
		return tiers[i].MinScore < tiers[j].MinScore
	})

	const coverageStart = 0.65
	const coverageEnd = 1.0

	// Check that tiers start at (or before) coverageStart.
	if tiers[0].MinScore > coverageStart {
		return fmt.Errorf("dynamic_sizing tiers must start at or before %.2f (first tier starts at %.4f)",
			coverageStart, tiers[0].MinScore)
	}

	// Check for gaps, overlaps, and that coverage reaches the end.
	prev := tiers[0].MaxScore
	for i := 1; i < len(tiers); i++ {
		t := tiers[i]
		if t.MinScore < prev {
			return fmt.Errorf("dynamic_sizing tier %d overlaps with previous (min=%.4f < prev_max=%.4f)",
				i, t.MinScore, prev)
		}
		if t.MinScore > prev {
			return fmt.Errorf("dynamic_sizing tier %d has a gap (min=%.4f > prev_max=%.4f)",
				i, t.MinScore, prev)
		}
		prev = t.MaxScore
	}

	if prev < coverageEnd {
		return fmt.Errorf("dynamic_sizing tiers do not cover up to %.2f (last tier max=%.4f)",
			coverageEnd, prev)
	}

	return nil
}

// partialTPLevel represents one partial take-profit level.
type partialTPLevel struct {
	TpPct    float64 `json:"tp_pct"`
	ClosePct float64 `json:"close_pct"`
}

// validatePartialTPLevels checks that tp_pct values are strictly increasing and the sum of
// close_pct is ≤ 100.
func validatePartialTPLevels(snap *Snapshot, scope []ScopeRef) error {
	ss := &ScopedSnapshot{Snapshot: snap, chain: scope}
	raw := ss.String("scalping.partial_tp.levels")
	if raw == "" {
		return nil
	}

	var levels []partialTPLevel
	if err := json.Unmarshal([]byte(raw), &levels); err != nil {
		return nil // best-effort; unparseable JSON is handled elsewhere
	}
	if len(levels) == 0 {
		return nil
	}

	var totalClose float64
	prevTP := -1.0
	for i, l := range levels {
		if l.TpPct <= prevTP {
			return fmt.Errorf("partial_tp level %d: tp_pct (%.4f) must be > previous (%.4f)",
				i, l.TpPct, prevTP)
		}
		prevTP = l.TpPct
		totalClose += l.ClosePct
	}

	if totalClose > 100.0 {
		return fmt.Errorf("partial_tp: sum of close_pct (%.4f) exceeds 100", totalClose)
	}

	return nil
}

// validateMarketTypeLeverage enforces that spot mode cannot use leverage > 1.
// The leverage key used is decision.leverage_default (the manifest-registered default lever).
func validateMarketTypeLeverage(snap *Snapshot, scope []ScopeRef) error {
	ss := &ScopedSnapshot{Snapshot: snap, chain: scope}
	marketType := ss.String("bot.market_type")
	if marketType == "" || marketType != "spot" {
		return nil
	}
	leverage := ss.Int("decision.leverage_default")
	if leverage == 0 {
		// Key absent — nothing to enforce.
		return nil
	}
	if leverage > 1 {
		return fmt.Errorf("bot.market_type=spot requires decision.leverage_default <= 1, got %d", leverage)
	}
	return nil
}

// validateVolatilityBand enforces min_volatility_atr_pct < max_volatility_atr_pct.
func validateVolatilityBand(snap *Snapshot, scope []ScopeRef) error {
	ss := &ScopedSnapshot{Snapshot: snap, chain: scope}
	minVol := ss.Float("pair_universe.filter.min_volatility_atr_pct")
	maxVol := ss.Float("pair_universe.filter.max_volatility_atr_pct")
	if minVol == 0 && maxVol == 0 {
		return nil
	}
	if minVol >= maxVol {
		return fmt.Errorf("pair_universe.filter.min_volatility_atr_pct (%.4f) must be < max_volatility_atr_pct (%.4f)", minVol, maxVol)
	}
	return nil
}
