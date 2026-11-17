package xsec

import (
	"context"
	"database/sql"
	"math"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func openMemDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func approx(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol
}

func TestPaperPortfolio_RebalanceAndPnL(t *testing.T) {
	db := openMemDB(t)
	const start = 1000.0

	p, err := NewPaperPortfolio(db, start)
	if err != nil {
		t.Fatalf("new portfolio: %v", err)
	}

	ctx := context.Background()
	t0 := time.Date(2026, 6, 9, 0, 0, 0, 0, time.UTC)

	// First rebalance: 2 longs, 2 shorts, no cost.
	target1 := Baskets{
		Longs:  []string{"AAA", "BBB"},
		Shorts: []string{"CCC", "DDD"},
		AsOf:   t0,
	}
	prices1 := map[string]float64{
		"AAA": 10, "BBB": 20, "CCC": 5, "DDD": 100,
	}
	sum1, err := p.Rebalance(ctx, target1, prices1, 0.0)
	if err != nil {
		t.Fatalf("rebalance1: %v", err)
	}
	if sum1.Opened != 4 || sum1.Closed != 0 {
		t.Fatalf("rebalance1 opened/closed = %d/%d, want 4/0", sum1.Opened, sum1.Closed)
	}
	if !approx(sum1.Equity, start, 1e-9) {
		t.Fatalf("rebalance1 equity = %v, want %v", sum1.Equity, start)
	}
	// Each side gets half equity: $500 long across 2 = $250 each; same for shorts.
	positions, eq := p.Snapshot(prices1)
	if len(positions) != 4 {
		t.Fatalf("got %d positions, want 4", len(positions))
	}
	for _, pos := range positions {
		if !approx(pos.Notional, 250, 1e-9) {
			t.Fatalf("%s notional = %v, want 250", pos.Pair, pos.Notional)
		}
	}
	// Dollar neutrality: long notional == short notional.
	var longNot, shortNot float64
	for _, pos := range positions {
		if pos.Dir == 1 {
			longNot += pos.Notional
		} else {
			shortNot += pos.Notional
		}
	}
	if !approx(longNot, shortNot, 1e-9) || !approx(longNot, 500, 1e-9) {
		t.Fatalf("not dollar-neutral: long=%v short=%v", longNot, shortNot)
	}
	if !approx(eq, start, 1e-9) {
		t.Fatalf("snapshot equity = %v, want %v", eq, start)
	}

	// Second rebalance: prices move, basket changes.
	// AAA +10% (long win), BBB unchanged (kept),
	// CCC -20% (short win), DDD +10% (short loss).
	// New basket drops BBB long -> add EEE long; drops DDD short -> add FFF short.
	prices2 := map[string]float64{
		"AAA": 11,  // +10%
		"BBB": 20,  // unchanged
		"CCC": 4,   // -20%
		"DDD": 110, // +10%
		"EEE": 50,
		"FFF": 7,
	}
	// Expected realized PnL at the moment of closing (before reopen):
	//  BBB long closed at unchanged price -> pnl 0
	//  DDD short closed at +10% -> pnl = -0.10 * 250 = -25
	// AAA long (+10%, kept open) and CCC short (-20%, kept open) remain as MtM.
	const costFrac = 0.001 // 0.1% round-trip on turnover

	t1 := t0.Add(time.Hour)
	target2 := Baskets{
		Longs:  []string{"AAA", "EEE"},
		Shorts: []string{"CCC", "FFF"},
		AsOf:   t1,
	}

	// Equity BEFORE rebalance (MtM at prices2) for sanity:
	//  cash=0; AAA mtm=275, BBB mtm=250, CCC mtm=300, DDD mtm=225 => 1050
	_, preEq := p.Snapshot(prices2)
	if !approx(preEq, 1050, 1e-9) {
		t.Fatalf("pre-rebalance equity = %v, want 1050", preEq)
	}

	sum2, err := p.Rebalance(ctx, target2, prices2, costFrac)
	if err != nil {
		t.Fatalf("rebalance2: %v", err)
	}
	// Closed: BBB, DDD (2). Opened: EEE, FFF (2). AAA, CCC kept.
	if sum2.Closed != 2 || sum2.Opened != 2 {
		t.Fatalf("rebalance2 opened/closed = %d/%d, want 2/2", sum2.Opened, sum2.Closed)
	}

	// Turnover cost: close BBB(250)+DDD(250) + open EEE+FFF.
	// After closes, equity (MtM) = 1050 - close fees.
	// close fees = (250+250)*0.001 = 0.5
	// equity for sizing = 1050 - 0.5 = 1049.5
	// new positions sized from that equity:
	//   long side: AAA kept(275 mtm) + EEE new. EEE = 1049.5*0.5/2 = 262.375
	//   short side: CCC kept(300 mtm) + FFF new. FFF = 262.375
	// open fees = (262.375 + 262.375)*0.001 = 0.52475
	expClose := 500 * costFrac
	expEEE := (1050 - expClose) * 0.5 / 2
	expOpen := 2 * expEEE * costFrac
	expCost := expClose + expOpen
	if !approx(sum2.CostPaid, expCost, 1e-6) {
		t.Fatalf("rebalance2 cost = %v, want %v", sum2.CostPaid, expCost)
	}

	// Verify realized PnL happened: equity dropped only by costs vs pre-rebalance
	// MtM (realized PnL is already embedded in the MtM, so equity is continuous
	// except for fees paid).
	if !approx(sum2.Equity, preEq-expCost, 1e-6) {
		t.Fatalf("rebalance2 equity = %v, want %v", sum2.Equity, preEq-expCost)
	}

	// New EEE/FFF notionals should be equal (dollar neutral new legs).
	positions2, _ := p.Snapshot(prices2)
	pos := make(map[string]Position)
	for _, x := range positions2 {
		pos[x.Pair] = x
	}
	if _, ok := pos["BBB"]; ok {
		t.Fatalf("BBB should be closed")
	}
	if _, ok := pos["DDD"]; ok {
		t.Fatalf("DDD should be closed")
	}
	if !approx(pos["EEE"].Notional, expEEE, 1e-6) {
		t.Fatalf("EEE notional = %v, want %v", pos["EEE"].Notional, expEEE)
	}
	if !approx(pos["FFF"].Notional, expEEE, 1e-6) {
		t.Fatalf("FFF notional = %v, want %v", pos["FFF"].Notional, expEEE)
	}
	// EEE entry should be its current price (newly opened).
	if !approx(pos["EEE"].Entry, 50, 1e-9) {
		t.Fatalf("EEE entry = %v, want 50", pos["EEE"].Entry)
	}
	// AAA kept its original entry.
	if !approx(pos["AAA"].Entry, 10, 1e-9) {
		t.Fatalf("AAA entry = %v, want 10 (kept)", pos["AAA"].Entry)
	}
}

func TestPaperPortfolio_PersistAndReload(t *testing.T) {
	db := openMemDB(t)
	ctx := context.Background()

	p, err := NewPaperPortfolio(db, 1000)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	target := Baskets{
		Longs:  []string{"AAA"},
		Shorts: []string{"BBB"},
		AsOf:   time.Date(2026, 6, 9, 0, 0, 0, 0, time.UTC),
	}
	prices := map[string]float64{"AAA": 10, "BBB": 20}
	if _, err := p.Rebalance(ctx, target, prices, 0); err != nil {
		t.Fatalf("rebalance: %v", err)
	}

	// Reload from the same db; positions + equity should restore.
	p2, err := NewPaperPortfolio(db, 1000)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	positions, eq := p2.Snapshot(prices)
	if len(positions) != 2 {
		t.Fatalf("reloaded %d positions, want 2", len(positions))
	}
	if !approx(eq, 1000, 1e-9) {
		t.Fatalf("reloaded equity = %v, want 1000", eq)
	}
}

func TestRebalanceTarget_WeightedSizingAndGrossScale(t *testing.T) {
	db := openMemDB(t)
	ctx := context.Background()
	const start = 1000.0

	p, err := NewPaperPortfolio(db, start)
	if err != nil {
		t.Fatalf("new portfolio: %v", err)
	}

	target := Target{
		Baskets: Baskets{
			Longs:  []string{"AAA", "BBB"},
			Shorts: []string{"CCC", "DDD"},
			AsOf:   time.Date(2026, 6, 9, 0, 0, 0, 0, time.UTC),
		},
		LongWeights:  map[string]float64{"AAA": 0.7, "BBB": 0.3},
		ShortWeights: map[string]float64{"CCC": 0.6, "DDD": 0.4},
		GrossScale:   0.8,
	}
	prices := map[string]float64{"AAA": 10, "BBB": 20, "CCC": 5, "DDD": 100}

	sum, err := p.RebalanceTarget(ctx, target, prices, 0)
	if err != nil {
		t.Fatalf("RebalanceTarget: %v", err)
	}
	if sum.Opened != 4 {
		t.Fatalf("opened = %d, want 4", sum.Opened)
	}

	// legSize_i = equity * 0.5 * gross * weight_i
	positions, _ := p.Snapshot(prices)
	pos := map[string]Position{}
	for _, x := range positions {
		pos[x.Pair] = x
	}
	wantNotional := map[string]float64{
		"AAA": start * 0.5 * 0.8 * 0.7, // 280
		"BBB": start * 0.5 * 0.8 * 0.3, // 120
		"CCC": start * 0.5 * 0.8 * 0.6, // 240
		"DDD": start * 0.5 * 0.8 * 0.4, // 160
	}
	for pair, want := range wantNotional {
		if !approx(pos[pair].Notional, want, 1e-9) {
			t.Errorf("%s notional = %v, want %v", pair, pos[pair].Notional, want)
		}
	}
	// Dollar neutrality preserved at scaled gross: each side = 400.
	if !approx(pos["AAA"].Notional+pos["BBB"].Notional, 400, 1e-9) {
		t.Errorf("long side != 400")
	}
	if !approx(pos["CCC"].Notional+pos["DDD"].Notional, 400, 1e-9) {
		t.Errorf("short side != 400")
	}
	// No cost: equity preserved.
	if !approx(sum.Equity, start, 1e-9) {
		t.Errorf("equity = %v, want %v", sum.Equity, start)
	}
}

func TestRebalanceTarget_ResizesKeptPositionOnWeightShift(t *testing.T) {
	db := openMemDB(t)
	ctx := context.Background()
	const start = 1000.0

	p, err := NewPaperPortfolio(db, start)
	if err != nil {
		t.Fatalf("new portfolio: %v", err)
	}
	asOf := time.Date(2026, 6, 9, 0, 0, 0, 0, time.UTC)
	prices := map[string]float64{"AAA": 10, "BBB": 20, "CCC": 5, "DDD": 100}
	baskets := Baskets{Longs: []string{"AAA", "BBB"}, Shorts: []string{"CCC", "DDD"}, AsOf: asOf}

	// Open equal-weight: every leg notional = 250.
	if _, err := p.RebalanceTarget(ctx, Target{Baskets: baskets}, prices, 0); err != nil {
		t.Fatalf("rebalance 1: %v", err)
	}

	// Same baskets, same prices, but AAA shifts to weight 0.8 (target 400) and
	// BBB to 0.2 (target 100): both differ >10% from 250 -> resized in place.
	// Shorts get explicit equal weights with a tiny imbalance below the 10%
	// band (0.52/0.48 -> 260/240, ~4-8% drift): must NOT be resized.
	target2 := Target{
		Baskets:      baskets,
		LongWeights:  map[string]float64{"AAA": 0.8, "BBB": 0.2},
		ShortWeights: map[string]float64{"CCC": 0.52, "DDD": 0.48},
		GrossScale:   1.0,
	}
	const costFrac = 0.001
	sum, err := p.RebalanceTarget(ctx, target2, prices, costFrac)
	if err != nil {
		t.Fatalf("rebalance 2: %v", err)
	}
	if sum.Resized != 2 {
		t.Fatalf("resized = %d, want 2 (AAA up, BBB down)", sum.Resized)
	}
	if sum.Opened != 0 || sum.Closed != 0 {
		t.Fatalf("opened/closed = %d/%d, want 0/0", sum.Opened, sum.Closed)
	}

	positions, _ := p.Snapshot(prices)
	pos := map[string]Position{}
	for _, x := range positions {
		pos[x.Pair] = x
	}
	if !approx(pos["AAA"].Notional, 400, 1e-9) {
		t.Errorf("AAA notional = %v, want 400 (resized up)", pos["AAA"].Notional)
	}
	if !approx(pos["BBB"].Notional, 100, 1e-9) {
		t.Errorf("BBB notional = %v, want 100 (resized down)", pos["BBB"].Notional)
	}
	if !approx(pos["CCC"].Notional, 250, 1e-9) || !approx(pos["DDD"].Notional, 250, 1e-9) {
		t.Errorf("shorts within 10%% band must keep notional 250, got CCC=%v DDD=%v",
			pos["CCC"].Notional, pos["DDD"].Notional)
	}
	// Cost charged on the traded delta only: |400-250| + |100-250| = 300.
	if !approx(sum.CostPaid, 300*costFrac, 1e-9) {
		t.Errorf("cost = %v, want %v (delta-only)", sum.CostPaid, 300*costFrac)
	}
}

func TestRebalanceTarget_LegacyEqualsRebalance(t *testing.T) {
	// The same sequence through Rebalance (wrapper) and through RebalanceTarget
	// with an empty Target must produce identical books — the baseline must be
	// reproduced exactly when every overlay is off.
	run := func(useWrapper bool) ([]Position, float64) {
		db := openMemDB(t)
		ctx := context.Background()
		p, err := NewPaperPortfolio(db, 1000)
		if err != nil {
			t.Fatalf("new: %v", err)
		}
		b1 := Baskets{Longs: []string{"AAA", "BBB"}, Shorts: []string{"CCC", "DDD"},
			AsOf: time.Date(2026, 6, 9, 0, 0, 0, 0, time.UTC)}
		p1 := map[string]float64{"AAA": 10, "BBB": 20, "CCC": 5, "DDD": 100}
		b2 := Baskets{Longs: []string{"AAA", "EEE"}, Shorts: []string{"CCC", "FFF"},
			AsOf: b1.AsOf.Add(time.Hour)}
		p2 := map[string]float64{"AAA": 13, "BBB": 21, "CCC": 4, "DDD": 110, "EEE": 50, "FFF": 7}
		step := func(b Baskets, prices map[string]float64) {
			var err error
			if useWrapper {
				_, err = p.Rebalance(ctx, b, prices, 0.001)
			} else {
				_, err = p.RebalanceTarget(ctx, Target{Baskets: b}, prices, 0.001)
			}
			if err != nil {
				t.Fatalf("step: %v", err)
			}
		}
		step(b1, p1)
		step(b2, p2)
		positions, eq := p.Snapshot(p2)
		return positions, eq
	}

	posA, eqA := run(true)
	posB, eqB := run(false)
	if !approx(eqA, eqB, 1e-9) {
		t.Fatalf("equity differs: wrapper %v vs target %v", eqA, eqB)
	}
	mapOf := func(ps []Position) map[string]Position {
		m := map[string]Position{}
		for _, p := range ps {
			m[p.Pair] = p
		}
		return m
	}
	ma, mb := mapOf(posA), mapOf(posB)
	if len(ma) != len(mb) {
		t.Fatalf("position count differs: %d vs %d", len(ma), len(mb))
	}
	for pair, a := range ma {
		b := mb[pair]
		if a.Dir != b.Dir || !approx(a.Entry, b.Entry, 1e-12) || !approx(a.Notional, b.Notional, 1e-9) {
			t.Errorf("%s differs: %+v vs %+v", pair, a, b)
		}
	}
}

func TestParams_LoadSetRoundTrip(t *testing.T) {
	db := openMemDB(t)

	// Fresh DB: empty map, table auto-created.
	params, err := LoadParams(db)
	if err != nil {
		t.Fatalf("LoadParams: %v", err)
	}
	if len(params) != 0 {
		t.Fatalf("fresh params = %v, want empty", params)
	}

	if err := SetParam(db, "xsec.k", "9"); err != nil {
		t.Fatalf("SetParam: %v", err)
	}
	if err := SetParam(db, "xsec.k", "11"); err != nil { // upsert
		t.Fatalf("SetParam upsert: %v", err)
	}
	if err := SetParam(db, "xsec.vol_weights", "true"); err != nil {
		t.Fatalf("SetParam bool: %v", err)
	}

	params, err = LoadParams(db)
	if err != nil {
		t.Fatalf("LoadParams 2: %v", err)
	}
	if params["xsec.k"] != "11" {
		t.Errorf("xsec.k = %q, want 11 (last write wins)", params["xsec.k"])
	}
	if params["xsec.vol_weights"] != "true" {
		t.Errorf("xsec.vol_weights = %q, want true", params["xsec.vol_weights"])
	}
}

func TestPaperPortfolio_EmptyBaskets(t *testing.T) {
	db := openMemDB(t)
	ctx := context.Background()
	p, err := NewPaperPortfolio(db, 500)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	sum, err := p.Rebalance(ctx, Baskets{AsOf: time.Now()}, map[string]float64{}, 0.001)
	if err != nil {
		t.Fatalf("empty rebalance: %v", err)
	}
	if sum.Opened != 0 || sum.Closed != 0 {
		t.Fatalf("empty: opened/closed = %d/%d, want 0/0", sum.Opened, sum.Closed)
	}
	if !approx(sum.Equity, 500, 1e-9) {
		t.Fatalf("empty equity = %v, want 500", sum.Equity)
	}
}
