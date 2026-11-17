package feedback

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// newTestDB sets up an in-memory SQLite with the calibration tables.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	schema := `
	CREATE TABLE signal_calibration (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		source TEXT, regime TEXT, pair TEXT,
		predicted TEXT, actual TEXT,
		confidence REAL, pnl_pct REAL,
		ts INTEGER DEFAULT (strftime('%s','now'))
	);
	CREATE TABLE signal_source_weights (
		source TEXT, regime TEXT,
		weight REAL DEFAULT 0.33,
		hit_rate REAL DEFAULT 0.5,
		sample_n INTEGER DEFAULT 0,
		updated_at INTEGER DEFAULT (strftime('%s','now')),
		PRIMARY KEY (source, regime)
	);
	INSERT INTO signal_source_weights (source, regime) VALUES
	  ('technical','RANGING'),('llm','RANGING'),('ml','RANGING');
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

// TestCalibrator_Recalibrate verifies that a source with a high win rate gets
// a higher weight after recalibration.
func TestCalibrator_Recalibrate(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	// Insert 20 wins for "technical" and 20 losses for "ml" in RANGING.
	ts := time.Now().Unix()
	for i := 0; i < 20; i++ {
		db.Exec(`INSERT INTO signal_calibration (source,regime,pair,predicted,actual,confidence,pnl_pct,ts)
			VALUES ('technical','RANGING','BTC-USDT','BUY','win',0.7,0.5,?)`, ts-int64(i))
		db.Exec(`INSERT INTO signal_calibration (source,regime,pair,predicted,actual,confidence,pnl_pct,ts)
			VALUES ('ml','RANGING','BTC-USDT','BUY','loss',0.6,-0.4,?)`, ts-int64(i))
	}

	cal := NewCalibrator(db, nil)
	if err := cal.Recalibrate(context.Background()); err != nil {
		t.Fatalf("Recalibrate: %v", err)
	}

	// technical should have higher weight than ml after consistent wins.
	wTech := cal.WeightFor(context.Background(), "technical", "RANGING")
	wML := cal.WeightFor(context.Background(), "ml", "RANGING")

	if wTech <= wML {
		t.Errorf("expected technical weight (%.4f) > ml weight (%.4f) after 20 wins vs losses",
			wTech, wML)
	}
	t.Logf("technical=%.4f, ml=%.4f, llm=%.4f",
		wTech, wML,
		cal.WeightFor(context.Background(), "llm", "RANGING"))
}

// TestMLWeightFor verifies that MLWeightFor returns the ML weight without panic.
func TestMLWeightFor(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	cal := NewCalibrator(db, nil)
	w := cal.MLWeightFor("RANGING")
	if w <= 0 || w > 1 {
		t.Errorf("unexpected MLWeightFor: %.4f", w)
	}
}

// TestDefaultWeight verifies the default weight values.
func TestDefaultWeight(t *testing.T) {
	if w := defaultWeight("ml"); w != 0.10 {
		t.Errorf("ml default weight: want 0.10, got %.2f", w)
	}
	if w := defaultWeight("technical"); w != 0.33 {
		t.Errorf("technical default weight: want 0.33, got %.2f", w)
	}
}

// newBootstrapDB adds a feature_store with two regimes of differing TP base-rate.
func newBootstrapDB(t *testing.T) *sql.DB {
	t.Helper()
	db := newTestDB(t)
	if _, err := db.Exec(`CREATE TABLE feature_store (
		ts INTEGER, pair TEXT, horizon_minutes INTEGER,
		features_json TEXT, regime TEXT,
		label_hit_tp INTEGER, label_hit_sl INTEGER,
		label_fwd_return REAL, label_direction TEXT)`); err != nil {
		t.Fatalf("feature_store schema: %v", err)
	}
	// TRENDING_UP: 160/200 TP (0.80). RANGING: 60/200 TP (0.30).
	for i := 0; i < 200; i++ {
		tpUp := 0
		if i < 160 {
			tpUp = 1
		}
		tpRange := 0
		if i < 60 {
			tpRange = 1
		}
		db.Exec(`INSERT INTO feature_store (ts,pair,horizon_minutes,features_json,regime,label_hit_tp,label_direction)
			VALUES (?, 'BTC-USDT', 60, '{}', 'TRENDING_UP', ?, 'up')`, i, tpUp)
		db.Exec(`INSERT INTO feature_store (ts,pair,horizon_minutes,features_json,regime,label_hit_tp,label_direction)
			VALUES (?, 'BTC-USDT', 60, '{}', 'RANGING', ?, 'up')`, i, tpRange)
	}
	return db
}

// TestCalibrator_Bootstrap verifies that with thin signal_calibration data the
// technical/ml weights become regime-aware from the feature_store base-rate
// (breaks the flat-0.33 inertia) instead of being identical across regimes.
func TestCalibrator_Bootstrap(t *testing.T) {
	db := newBootstrapDB(t)
	defer db.Close()

	cal := NewCalibrator(db, nil) // bootstrap on by default, no signal_calibration rows
	if err := cal.Recalibrate(context.Background()); err != nil {
		t.Fatalf("Recalibrate: %v", err)
	}
	ctx := context.Background()
	techTrend := cal.WeightFor(ctx, "technical", "TRENDING_UP") // base-rate 0.80
	techRange := cal.WeightFor(ctx, "technical", "RANGING")     // base-rate 0.30
	// The high-base-rate regime must get a clearly higher technical weight.
	if !(techTrend > techRange*2) {
		t.Errorf("bootstrap should make technical weight regime-aware: TRENDING_UP=%.4f should clearly exceed RANGING=%.4f",
			techTrend, techRange)
	}

	// With bootstrap OFF and no data, weights are regime-AGNOSTIC (same everywhere).
	cal2 := NewCalibrator(db, nil)
	cal2.SetBootstrap(false)
	if err := cal2.Recalibrate(ctx); err != nil {
		t.Fatalf("Recalibrate (bootstrap off): %v", err)
	}
	wA := cal2.WeightFor(ctx, "technical", "BREAKOUT")
	wB := cal2.WeightFor(ctx, "technical", "CHAOS")
	if wA != wB {
		t.Errorf("bootstrap OFF should be regime-agnostic, got BREAKOUT=%.4f CHAOS=%.4f", wA, wB)
	}
}
