package xsec

import (
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// newTestDB creates an in-memory SQLite DB with the candle_archive schema.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`
		CREATE TABLE candle_archive (
			pair      TEXT    NOT NULL,
			timeframe TEXT    NOT NULL,
			timestamp INTEGER NOT NULL,
			open      REAL,
			high      REAL,
			low       REAL,
			close     REAL,
			volume    REAL,
			PRIMARY KEY (pair, timeframe, timestamp)
		)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	return db
}

// seedSeries inserts a daily close series for a pair, one bar per day ending today.
func seedSeries(t *testing.T, db *sql.DB, pair string, closes []float64) {
	t.Helper()
	base := time.Now().Truncate(24 * time.Hour).Add(-time.Duration(len(closes)) * 24 * time.Hour)
	for i, c := range closes {
		ts := base.Add(time.Duration(i) * 24 * time.Hour).Unix()
		_, err := db.Exec(
			`INSERT INTO candle_archive (pair, timeframe, timestamp, open, high, low, close, volume)
			 VALUES (?, '1d', ?, ?, ?, ?, ?, ?)`,
			pair, ts, c, c, c, c, 1.0)
		if err != nil {
			t.Fatalf("insert %s bar %d: %v", pair, i, err)
		}
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestRank_MomentumBaskets(t *testing.T) {
	db := newTestDB(t)

	// 5 pairs, 4 bars each. lookback=3 => momentum = close[3]/close[0] - 1.
	// WIN1: 100 -> 200  => +1.00 (strongest)
	// WIN2: 100 -> 150  => +0.50
	// FLAT: 100 -> 100  =>  0.00
	// LOSE1:100 -> 80   => -0.20
	// LOSE2:100 -> 50   => -0.50 (weakest)
	seedSeries(t, db, "WIN1", []float64{100, 120, 160, 200})
	seedSeries(t, db, "WIN2", []float64{100, 110, 130, 150})
	seedSeries(t, db, "FLAT", []float64{100, 105, 102, 100})
	seedSeries(t, db, "LOSE1", []float64{100, 95, 90, 80})
	seedSeries(t, db, "LOSE2", []float64{100, 80, 65, 50})

	r := NewMomentumRanker(db, "1d")
	pairs := []string{"WIN1", "WIN2", "FLAT", "LOSE1", "LOSE2"}

	baskets, err := r.Rank(pairs, 3, 2)
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}

	if len(baskets.Longs) != 2 || len(baskets.Shorts) != 2 {
		t.Fatalf("expected 2 longs and 2 shorts, got %d/%d", len(baskets.Longs), len(baskets.Shorts))
	}
	if !contains(baskets.Longs, "WIN1") || !contains(baskets.Longs, "WIN2") {
		t.Errorf("Longs should be WIN1,WIN2; got %v", baskets.Longs)
	}
	if !contains(baskets.Shorts, "LOSE1") || !contains(baskets.Shorts, "LOSE2") {
		t.Errorf("Shorts should be LOSE1,LOSE2; got %v", baskets.Shorts)
	}
	// Top long must be the strongest performer.
	if baskets.Longs[0] != "WIN1" {
		t.Errorf("strongest long should be WIN1, got %s", baskets.Longs[0])
	}
	if baskets.AsOf.IsZero() {
		t.Error("AsOf should be set to the latest candle timestamp")
	}
}

func TestRank_DropsInsufficientHistory(t *testing.T) {
	db := newTestDB(t)

	// 4 pairs with enough history (lookback=2 needs 3 bars), 1 pair with too few.
	seedSeries(t, db, "A", []float64{100, 150, 200}) // +1.00
	seedSeries(t, db, "B", []float64{100, 110, 120}) // +0.20
	seedSeries(t, db, "C", []float64{100, 95, 90})   // -0.10
	seedSeries(t, db, "D", []float64{100, 80, 60})   // -0.40
	seedSeries(t, db, "SHORTHIST", []float64{100})   // dropped

	r := NewMomentumRanker(db, "1d")
	pairs := []string{"A", "B", "C", "D", "SHORTHIST"}

	baskets, err := r.Rank(pairs, 2, 2)
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	if contains(baskets.Longs, "SHORTHIST") || contains(baskets.Shorts, "SHORTHIST") {
		t.Errorf("SHORTHIST lacks history and must be dropped; longs=%v shorts=%v",
			baskets.Longs, baskets.Shorts)
	}
	if !contains(baskets.Longs, "A") || !contains(baskets.Shorts, "D") {
		t.Errorf("expected A long and D short; longs=%v shorts=%v", baskets.Longs, baskets.Shorts)
	}
}

func TestRank_NotEnoughPairs(t *testing.T) {
	db := newTestDB(t)
	seedSeries(t, db, "A", []float64{100, 110, 120})
	seedSeries(t, db, "B", []float64{100, 90, 80})

	r := NewMomentumRanker(db, "1d")
	// k=2 needs 2*k=4 valid pairs but only 2 exist.
	if _, err := r.Rank([]string{"A", "B"}, 2, 2); err == nil {
		t.Fatal("expected error when fewer than 2*k pairs have history")
	}
}

func TestLatestClose(t *testing.T) {
	db := newTestDB(t)
	seedSeries(t, db, "A", []float64{100, 150, 175})
	seedSeries(t, db, "B", []float64{50, 60, 42})

	r := NewMomentumRanker(db, "1d")
	closes, err := r.LatestClose([]string{"A", "B", "MISSING"})
	if err != nil {
		t.Fatalf("LatestClose: %v", err)
	}
	if closes["A"] != 175 {
		t.Errorf("A latest close = %v, want 175", closes["A"])
	}
	if closes["B"] != 42 {
		t.Errorf("B latest close = %v, want 42", closes["B"])
	}
	if _, ok := closes["MISSING"]; ok {
		t.Error("MISSING has no candles and must be absent from the result")
	}
}
