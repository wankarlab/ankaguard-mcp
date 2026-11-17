package xsec

import (
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// stubScorer returns a fixed score map for testing wrappers.
type stubScorer struct {
	scores map[string]float64
	asOf   time.Time
}

func (s *stubScorer) Scores(pairs []string) (map[string]float64, time.Time, error) {
	out := make(map[string]float64, len(s.scores))
	for p, v := range s.scores {
		out[p] = v
	}
	return out, s.asOf, nil
}

func TestFundingTiltScorer_AdjustsByLatestRate(t *testing.T) {
	db := openMemDB(t)
	if _, err := db.Exec(`CREATE TABLE funding_archive (
		pair TEXT NOT NULL, ts INTEGER NOT NULL, rate REAL NOT NULL,
		PRIMARY KEY (pair, ts))`); err != nil {
		t.Fatalf("create funding_archive: %v", err)
	}
	now := time.Now()
	insert := func(pair string, ts time.Time, rate float64) {
		if _, err := db.Exec(`INSERT INTO funding_archive (pair, ts, rate) VALUES (?, ?, ?)`,
			pair, ts.Unix(), rate); err != nil {
			t.Fatalf("insert funding: %v", err)
		}
	}
	// POSFUND pays longs 0.1%/period (positive): should be pushed DOWN.
	// Older row first to prove the latest one wins.
	insert("POSFUND", now.Add(-30*time.Hour), 0.01)
	insert("POSFUND", now.Add(-2*time.Hour), 0.001)
	// NEGFUND rewards longs (negative funding): pushed UP.
	insert("NEGFUND", now.Add(-1*time.Hour), -0.002)
	// STALE has only old data (>3 days): adjustment must be 0.
	insert("STALE", now.Add(-4*24*time.Hour), 0.05)

	inner := &stubScorer{
		scores: map[string]float64{"POSFUND": 0.10, "NEGFUND": 0.10, "STALE": 0.10, "NODATA": 0.10},
		asOf:   now,
	}
	const tilt = 10.0
	sc := NewFundingTiltScorer(inner, db, tilt, nil)

	got, asOf, err := sc.Scores([]string{"POSFUND", "NEGFUND", "STALE", "NODATA"})
	if err != nil {
		t.Fatalf("Scores: %v", err)
	}
	if !asOf.Equal(now) {
		t.Errorf("asOf = %v, want inner asOf %v", asOf, now)
	}
	if !approx(got["POSFUND"], 0.10-tilt*0.001, 1e-12) {
		t.Errorf("POSFUND = %v, want %v (latest rate, tilted down)", got["POSFUND"], 0.10-tilt*0.001)
	}
	if !approx(got["NEGFUND"], 0.10+tilt*0.002, 1e-12) {
		t.Errorf("NEGFUND = %v, want %v (negative funding tilts up)", got["NEGFUND"], 0.10+tilt*0.002)
	}
	if !approx(got["STALE"], 0.10, 1e-12) {
		t.Errorf("STALE = %v, want 0.10 (rows older than 3d ignored)", got["STALE"])
	}
	if !approx(got["NODATA"], 0.10, 1e-12) {
		t.Errorf("NODATA = %v, want 0.10 (no funding data, adjustment 0)", got["NODATA"])
	}
}

func TestFundingTiltScorer_MissingTableDegradesToInner(t *testing.T) {
	db := openMemDB(t) // no funding_archive table at all
	inner := &stubScorer{
		scores: map[string]float64{"A": 0.3, "B": -0.1},
		asOf:   time.Date(2026, 6, 9, 0, 0, 0, 0, time.UTC),
	}
	sc := NewFundingTiltScorer(inner, db, 5.0, nil)

	got, _, err := sc.Scores([]string{"A", "B"})
	if err != nil {
		t.Fatalf("Scores with missing table must not error: %v", err)
	}
	if !approx(got["A"], 0.3, 1e-12) || !approx(got["B"], -0.1, 1e-12) {
		t.Errorf("missing table must return inner scores unchanged, got %v", got)
	}
}

func TestFundingTiltScorer_ZeroTiltIsNoop(t *testing.T) {
	db := openMemDB(t)
	inner := &stubScorer{scores: map[string]float64{"A": 0.42}, asOf: time.Now()}
	sc := NewFundingTiltScorer(inner, db, 0, nil)
	got, _, err := sc.Scores([]string{"A"})
	if err != nil {
		t.Fatalf("Scores: %v", err)
	}
	if !approx(got["A"], 0.42, 1e-12) {
		t.Errorf("zero tilt must be a no-op, got %v", got["A"])
	}
}
