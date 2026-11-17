package xsec

import (
	"math"
	"math/rand"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func sumWeights(w map[string]float64) float64 {
	var s float64
	for _, v := range w {
		s += v
	}
	return s
}

func TestCapWeights_RedistributesProportionally(t *testing.T) {
	in := map[string]float64{"A": 0.5, "B": 0.3, "C": 0.2}
	out := CapWeights(in, 0.35)

	// A capped at 0.35; excess 0.15 flows to B:C (0.3:0.2) -> B 0.39 > cap,
	// second pass caps B at 0.35 and pushes the rest to C -> C 0.30.
	if !approx(out["A"], 0.35, 1e-9) {
		t.Errorf("A = %v, want 0.35", out["A"])
	}
	if !approx(out["B"], 0.35, 1e-9) {
		t.Errorf("B = %v, want 0.35", out["B"])
	}
	if !approx(out["C"], 0.30, 1e-9) {
		t.Errorf("C = %v, want 0.30", out["C"])
	}
	if !approx(sumWeights(out), 1.0, 1e-9) {
		t.Errorf("sum = %v, want 1.0", sumWeights(out))
	}
	// Input must not be mutated.
	if in["A"] != 0.5 {
		t.Errorf("input mutated: A = %v", in["A"])
	}
}

func TestCapWeights_NoopWhenBelowCapOrDisabled(t *testing.T) {
	in := map[string]float64{"A": 0.4, "B": 0.6}

	out := CapWeights(in, 0) // disabled
	if !approx(out["A"], 0.4, 1e-12) || !approx(out["B"], 0.6, 1e-12) {
		t.Errorf("maxW=0 must be a no-op, got %v", out)
	}

	out = CapWeights(in, 0.7) // everything already below cap
	if !approx(out["A"], 0.4, 1e-12) || !approx(out["B"], 0.6, 1e-12) {
		t.Errorf("all-below-cap must be unchanged, got %v", out)
	}
}

func TestCapWeights_InfeasibleCapReducesGross(t *testing.T) {
	in := map[string]float64{"A": 0.4, "B": 0.3, "C": 0.2, "D": 0.1}
	out := CapWeights(in, 0.05) // 4 * 0.05 = 0.2 < 1: infeasible
	for p, w := range out {
		if w > 0.05+1e-12 {
			t.Errorf("%s = %v exceeds the cap", p, w)
		}
	}
	if sumWeights(out) > 0.2+1e-9 {
		t.Errorf("infeasible cap should drop gross to 0.2, got %v", sumWeights(out))
	}
}

func TestVolTargetScale_NeutralWithFewObservations(t *testing.T) {
	rets := []float64{0.01, -0.02, 0.005, 0.01, -0.01, 0.02, 0.0, 0.01, -0.005}
	if got := VolTargetScale(rets, 0.20, 12); got != 1.0 {
		t.Errorf("with %d obs (<10) scale = %v, want 1.0", len(rets), got)
	}
	if got := VolTargetScale(nil, 0.20, 12); got != 1.0 {
		t.Errorf("nil returns scale = %v, want 1.0", got)
	}
	if got := VolTargetScale(append(rets, 0.01, 0.0), 0, 12); got != 1.0 {
		t.Errorf("target=0 scale = %v, want 1.0 (off)", got)
	}
}

func TestVolTargetScale_Clamps(t *testing.T) {
	// Tiny daily moves (~0.01% => ~0.2%/yr realized): scale wants to explode,
	// must clamp at 1.5.
	quiet := make([]float64, 30)
	for i := range quiet {
		quiet[i] = 0.0001
	}
	if got := VolTargetScale(quiet, 0.20, 12); got != 1.5 {
		t.Errorf("quiet history scale = %v, want clamp 1.5", got)
	}

	// Huge daily moves (10%/day => ~190%/yr realized): must clamp at 0.25.
	wild := make([]float64, 30)
	for i := range wild {
		if i%2 == 0 {
			wild[i] = 0.10
		} else {
			wild[i] = -0.10
		}
	}
	if got := VolTargetScale(wild, 0.20, 12); got != 0.25 {
		t.Errorf("wild history scale = %v, want clamp 0.25", got)
	}
}

func TestVolTargetScale_MidRange(t *testing.T) {
	// Constant |r| = 1.5%/day -> realized = 0.015*sqrt(365) ~ 28.66%/yr.
	// Target 20% -> scale ~ 0.698, inside the clamp band.
	rets := make([]float64, 60)
	for i := range rets {
		if i%2 == 0 {
			rets[i] = 0.015
		} else {
			rets[i] = -0.015
		}
	}
	got := VolTargetScale(rets, 0.20, 12)
	want := 0.20 / (0.015 * math.Sqrt(365))
	if !approx(got, want, 1e-6) {
		t.Errorf("scale = %v, want %v", got, want)
	}
}

func TestInverseVolWeights_LowVolGetsMoreWeight(t *testing.T) {
	db := newTestDB(t)
	rng := rand.New(rand.NewSource(42))

	// LOWVOL: ~0.5% daily noise. HIGHVOL: ~5% daily noise. 40 bars each.
	gen := func(vol float64) []float64 {
		price := 100.0
		out := []float64{price}
		for i := 0; i < 40; i++ {
			price *= math.Exp(vol * rng.NormFloat64())
			out = append(out, price)
		}
		return out
	}
	seedSeries(t, db, "LOWVOL", gen(0.005))
	seedSeries(t, db, "HIGHVOL", gen(0.05))

	w, err := InverseVolWeights(db, "1d", []string{"LOWVOL", "HIGHVOL"}, 20)
	if err != nil {
		t.Fatalf("InverseVolWeights: %v", err)
	}
	if !approx(sumWeights(w), 1.0, 1e-9) {
		t.Fatalf("weights sum = %v, want 1.0", sumWeights(w))
	}
	if w["LOWVOL"] <= w["HIGHVOL"] {
		t.Errorf("LOWVOL weight (%v) should exceed HIGHVOL weight (%v)", w["LOWVOL"], w["HIGHVOL"])
	}
}

func TestInverseVolWeights_InsufficientDataAndFlatSeries(t *testing.T) {
	db := newTestDB(t)
	seedSeries(t, db, "GOOD", []float64{
		100, 102, 99, 103, 101, 104, 100, 105, 103, 106,
		104, 107, 103, 108, 106, 109, 105, 110, 108, 111, 109,
	}) // 21 bars, enough for window=10
	seedSeries(t, db, "SHORT", []float64{100, 101})    // insufficient history
	seedSeries(t, db, "FLAT", make21Flat())            // sigma = 0 guard
	pairs := []string{"GOOD", "SHORT", "FLAT", "NONE"} // NONE: no candles at all

	w, err := InverseVolWeights(db, "1d", pairs, 10)
	if err != nil {
		t.Fatalf("InverseVolWeights: %v", err)
	}
	if !approx(sumWeights(w), 1.0, 1e-9) {
		t.Fatalf("weights sum = %v, want 1.0", sumWeights(w))
	}
	// All four pairs must receive a positive weight (residual = mean inv-vol),
	// and the unmeasurable ones share the same residual weight.
	for _, p := range pairs {
		if w[p] <= 0 {
			t.Errorf("%s weight = %v, want > 0", p, w[p])
		}
	}
	if !approx(w["SHORT"], w["FLAT"], 1e-12) || !approx(w["SHORT"], w["NONE"], 1e-12) {
		t.Errorf("unmeasurable pairs should share the residual weight: %v", w)
	}
}

func make21Flat() []float64 {
	out := make([]float64, 21)
	for i := range out {
		out[i] = 100
	}
	return out
}

func TestInverseVolWeights_AllUnmeasurableFallsBackToEqual(t *testing.T) {
	db := newTestDB(t)
	seedSeries(t, db, "A", []float64{100})
	w, err := InverseVolWeights(db, "1d", []string{"A", "B"}, 10)
	if err != nil {
		t.Fatalf("InverseVolWeights: %v", err)
	}
	if !approx(w["A"], 0.5, 1e-12) || !approx(w["B"], 0.5, 1e-12) {
		t.Errorf("expected equal fallback weights, got %v", w)
	}
}

func TestPortfolioDailyReturns(t *testing.T) {
	db := openMemDB(t)
	if _, err := db.Exec(`CREATE TABLE xsec_equity (
		ts TIMESTAMP NOT NULL, equity REAL NOT NULL,
		n_long INTEGER NOT NULL, n_short INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create xsec_equity: %v", err)
	}
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	insert := func(ts time.Time, eq float64) {
		if _, err := db.Exec(
			`INSERT INTO xsec_equity (ts, equity, n_long, n_short) VALUES (?, ?, 7, 7)`,
			ts, eq); err != nil {
			t.Fatalf("insert equity: %v", err)
		}
	}
	insert(base, 1000)                    // day 1
	insert(base.Add(2*time.Hour), 1010)   // day 1, later row wins
	insert(base.AddDate(0, 0, 1), 1020)   // day 2: +0.990099%
	insert(base.AddDate(0, 0, 2), 969)    // day 3: -5%
	insert(base.AddDate(0, 0, 3), 1065.9) // day 4: +10%

	rets, err := PortfolioDailyReturns(db, 0)
	if err != nil {
		t.Fatalf("PortfolioDailyReturns: %v", err)
	}
	want := []float64{1020.0/1010.0 - 1, 969.0/1020.0 - 1, 1065.9/969.0 - 1}
	if len(rets) != len(want) {
		t.Fatalf("got %d returns, want %d: %v", len(rets), len(want), rets)
	}
	for i := range want {
		if !approx(rets[i], want[i], 1e-9) {
			t.Errorf("ret[%d] = %v, want %v", i, rets[i], want[i])
		}
	}

	// maxDays trims from the front (keeps the most recent observations).
	rets2, err := PortfolioDailyReturns(db, 2)
	if err != nil {
		t.Fatalf("PortfolioDailyReturns maxDays: %v", err)
	}
	if len(rets2) != 2 || !approx(rets2[1], want[2], 1e-9) {
		t.Errorf("maxDays=2 -> %v, want last two of %v", rets2, want)
	}
}

func TestBuildBaskets_Deterministic(t *testing.T) {
	scores := map[string]float64{
		"A": 0.5, "B": 0.5, // tie at the top: must break by pair name
		"C": 0.1, "D": 0.0, "E": -0.2, "F": -0.2, // tie at the bottom too
	}
	asOf := time.Date(2026, 6, 9, 0, 0, 0, 0, time.UTC)
	first := BuildBaskets(scores, 2, asOf, 0, nil, nil)
	for i := 0; i < 25; i++ {
		got := BuildBaskets(scores, 2, asOf, 0, nil, nil)
		if len(got.Longs) != 2 || len(got.Shorts) != 2 {
			t.Fatalf("iteration %d: sizes %d/%d", i, len(got.Longs), len(got.Shorts))
		}
		for j := range got.Longs {
			if got.Longs[j] != first.Longs[j] || got.Shorts[j] != first.Shorts[j] {
				t.Fatalf("iteration %d not deterministic: %v/%v vs %v/%v",
					i, got.Longs, got.Shorts, first.Longs, first.Shorts)
			}
		}
	}
	// Ties broken alphabetically: A before B on top, E before F at the bottom.
	if first.Longs[0] != "A" || first.Longs[1] != "B" {
		t.Errorf("longs = %v, want [A B]", first.Longs)
	}
	if first.Shorts[0] != "E" || first.Shorts[1] != "F" {
		t.Errorf("shorts = %v, want [E F]", first.Shorts)
	}
}

// TestBuildBaskets_BandZero_MatchesStrict is the regression-safety contract:
// band=0 (or nil prev maps) must reproduce plain top-k/bottom-k exactly, even
// when non-nil (but irrelevant) prev maps are supplied.
func TestBuildBaskets_BandZero_MatchesStrict(t *testing.T) {
	scores := map[string]float64{
		"A": 0.9, "B": 0.5, "C": 0.4, "D": 0.3, "E": 0.2, "F": 0.1,
	}
	asOf := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	strict := BuildBaskets(scores, 2, asOf, 0, nil, nil)
	withIgnoredPrev := BuildBaskets(scores, 2, asOf, 0,
		map[string]bool{"C": true}, map[string]bool{"D": true})
	if !equalSlices(strict.Longs, withIgnoredPrev.Longs) || !equalSlices(strict.Shorts, withIgnoredPrev.Shorts) {
		t.Fatalf("band=0 must ignore prev maps: strict=%v/%v, got=%v/%v",
			strict.Longs, strict.Shorts, withIgnoredPrev.Longs, withIgnoredPrev.Shorts)
	}
	if !equalSlices(strict.Longs, []string{"A", "B"}) {
		t.Errorf("longs = %v, want [A B]", strict.Longs)
	}
	if !equalSlices(strict.Shorts, []string{"E", "F"}) {
		t.Errorf("shorts = %v, want [E F]", strict.Shorts)
	}
}

// TestBuildBaskets_Band_KeepsIncumbent verifies the no-trade band (rank
// hysteresis) ported from cmd/xsec-sim's selectWithBand: an incumbent still
// inside the top/bottom k+band buffer is kept ahead of a better-ranked
// newcomer, which only fills the remaining slot.
func TestBuildBaskets_Band_KeepsIncumbent(t *testing.T) {
	// Descending by score: A(0.9) B(0.5) C(0.4) D(0.3) E(0.2) F(0.1).
	scores := map[string]float64{
		"A": 0.9, "B": 0.5, "C": 0.4, "D": 0.3, "E": 0.2, "F": 0.1,
	}
	asOf := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	k, band := 2, 1

	// Long side: C is rank 3 (inside k+band=3) and was previously held, so it
	// should be kept over B even though B outranks it; A still fills the
	// other slot as the clear #1.
	prevLong := map[string]bool{"C": true}
	// Short side: D is the 3rd-worst (inside the bottom k+band=3) and was
	// previously held, so it should be kept over E; F still fills the other
	// slot as the clear worst.
	prevShort := map[string]bool{"D": true}

	got := BuildBaskets(scores, k, asOf, band, prevLong, prevShort)
	if !equalSlices(got.Longs, []string{"C", "A"}) {
		t.Errorf("longs = %v, want [C A] (incumbent C kept over rank-2 B)", got.Longs)
	}
	if !equalSlices(got.Shorts, []string{"D", "F"}) {
		t.Errorf("shorts = %v, want [D F] (incumbent D kept over rank-2-worst E)", got.Shorts)
	}

	// Without band (strict), the same scores must fall back to plain top-k.
	strict := BuildBaskets(scores, k, asOf, 0, prevLong, prevShort)
	if !equalSlices(strict.Longs, []string{"A", "B"}) {
		t.Errorf("strict longs = %v, want [A B]", strict.Longs)
	}
	if !equalSlices(strict.Shorts, []string{"E", "F"}) {
		t.Errorf("strict shorts = %v, want [E F]", strict.Shorts)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
