package handlers

import "testing"

func hasWarn(ws []string, code string) bool {
	for _, w := range ws {
		if w == code {
			return true
		}
	}
	return false
}

func TestComputeSuggestedParams(t *testing.T) {
	// $4000, 3x, 80% util, live universe of 18 pairs. Gross = 9600. k target 12
	// but universe caps it at 18/2 = 9. Per-leg = 9600/18 = 533.33 >> floor, so
	// no min-leg reduction. Feasible.
	r := computeSuggestedParams(4000, 3, 0.80, 18, 3_000_000)
	if r.K != 9 {
		t.Fatalf("K = %d, want 9 (universe-capped)", r.K)
	}
	if !hasWarn(r.Warnings, "k_capped_by_universe") {
		t.Errorf("expected k_capped_by_universe warning, got %v", r.Warnings)
	}
	if hasWarn(r.Warnings, "k_reduced_for_min_leg") {
		t.Errorf("did not expect min-leg reduction at $4000, got %v", r.Warnings)
	}
	if r.Legs != 18 {
		t.Errorf("Legs = %d, want 18", r.Legs)
	}
	if r.GrossExposure != 9600 {
		t.Errorf("GrossExposure = %v, want 9600", r.GrossExposure)
	}
	if r.MarginUsed != 3200 { // 9600 / 3
		t.Errorf("MarginUsed = %v, want 3200", r.MarginUsed)
	}
	wantPerLeg := round2(9600.0 / 18.0)
	if r.PerLegUSD != wantPerLeg || r.MaxPosUSD != wantPerLeg {
		t.Errorf("PerLegUSD = %v / MaxPosUSD = %v, want %v", r.PerLegUSD, r.MaxPosUSD, wantPerLeg)
	}
	if !r.Feasible {
		t.Errorf("expected feasible")
	}
	if r.Lookback != 14 || r.Rebal != 5 || r.Band != 0 || !r.VolWeights {
		t.Errorf("robust config not applied: lb=%d rebal=%d band=%d vw=%v", r.Lookback, r.Rebal, r.Band, r.VolWeights)
	}

	// Large universe (>=24): k reaches the research target of 12, no cap.
	r = computeSuggestedParams(100000, 3, 0.80, 40, 3_000_000)
	if r.K != 12 {
		t.Fatalf("K = %d, want 12 (research target, universe not limiting)", r.K)
	}
	if hasWarn(r.Warnings, "k_capped_by_universe") {
		t.Errorf("did not expect universe cap with 40 pairs, got %v", r.Warnings)
	}

	// Universe unavailable (fetch failed): fall back to target k, flagged.
	r = computeSuggestedParams(4000, 3, 0.80, -1, 3_000_000)
	if r.K != 12 {
		t.Fatalf("K = %d, want 12 when universe unknown", r.K)
	}
	if !hasWarn(r.Warnings, "universe_unavailable") {
		t.Errorf("expected universe_unavailable warning, got %v", r.Warnings)
	}
	if r.UniverseSize != -1 {
		t.Errorf("UniverseSize = %d, want -1", r.UniverseSize)
	}

	// Micro capital: gross so small that per-leg would fall below the floor at
	// k=9 → reduce k until per-leg >= 10. $200 * 3 * 0.8 = 480 gross; need
	// 480/(2k) >= 10 → k <= 24; but universe caps k at 9 first, and 480/18 =
	// 26.7 >= 10, so k stays 9. Use an even smaller account to force reduction.
	r = computeSuggestedParams(60, 3, 0.80, 18, 3_000_000)
	// gross = 144. At k=9, per-leg = 144/18 = 8 < 10 → reduce. At k=7, 144/14 =
	// 10.28 >= 10. So k should drop below 9.
	if r.K >= 9 {
		t.Fatalf("K = %d, expected reduction below universe cap for min-leg", r.K)
	}
	if r.PerLegUSD < suggestMinLegUSD {
		t.Errorf("PerLegUSD = %v below floor %v after reduction", r.PerLegUSD, suggestMinLegUSD)
	}
	if !hasWarn(r.Warnings, "k_reduced_for_min_leg") {
		t.Errorf("expected k_reduced_for_min_leg, got %v", r.Warnings)
	}

	// Capital too small even at k=1: below floor, not feasible.
	r = computeSuggestedParams(10, 1, 0.80, 18, 3_000_000)
	// gross = 8; at k=1, per-leg = 8/2 = 4 < 10.
	if r.K != 1 {
		t.Fatalf("K = %d, want 1 (floor)", r.K)
	}
	if r.Feasible {
		t.Errorf("expected infeasible for tiny capital")
	}
	if !hasWarn(r.Warnings, "capital_below_min") {
		t.Errorf("expected capital_below_min, got %v", r.Warnings)
	}

	// High leverage warning.
	r = computeSuggestedParams(4000, 10, 0.80, 40, 3_000_000)
	if !hasWarn(r.Warnings, "high_leverage") {
		t.Errorf("expected high_leverage warning at 10x, got %v", r.Warnings)
	}

	// Defaults normalization: invalid leverage/util fall back.
	r = computeSuggestedParams(4000, 0, 0, 40, 3_000_000)
	if r.Leverage != suggestDefaultLeverage || r.Utilization != suggestDefaultUtilization {
		t.Errorf("defaults not applied: lev=%v util=%v", r.Leverage, r.Utilization)
	}
}

func TestComputeSuggestedParams_LiveRuns1xWarning(t *testing.T) {
	// Leverage > 1 is backtest-only: live always runs 1x.
	r := computeSuggestedParams(4000, 3, 0.80, 40, 3_000_000)
	if !hasWarn(r.Warnings, "live_runs_1x") {
		t.Errorf("expected live_runs_1x at 3x, got %v", r.Warnings)
	}
	// 1x sizing is executable live: no warning, math unchanged.
	r = computeSuggestedParams(4000, 1, 0.80, 40, 3_000_000)
	if hasWarn(r.Warnings, "live_runs_1x") {
		t.Errorf("did not expect live_runs_1x at 1x, got %v", r.Warnings)
	}
	if r.GrossExposure != 3200 {
		t.Errorf("GrossExposure = %v, want 3200 (math must not change)", r.GrossExposure)
	}
}
