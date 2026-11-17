package xsec

import (
	"database/sql"
	"math"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// TestEvalShadowEquity checks the forward paper equity math against hand-computed
// returns: one long and one short over one period, equal-weight, each leg half
// the book, minus turnover cost on the initial (fully fresh) rotation.
func TestEvalShadowEquity(t *testing.T) {
	db, err := sql.Open("sqlite3", t.TempDir()+"/shadow.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE candle_archive (
		pair TEXT, timeframe TEXT, timestamp INTEGER, open REAL, high REAL, low REAL, close REAL, volume REAL,
		PRIMARY KEY (pair, timeframe, timestamp))`); err != nil {
		t.Fatal(err)
	}
	if err := EnsureShadowSchema(db); err != nil {
		t.Fatal(err)
	}

	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.AddDate(0, 0, 7)
	// LONG pair AAA: 100 -> 110 (+10%). SHORT pair BBB: 50 -> 45 (-10%, a WIN for a short).
	ins := func(pair string, ts time.Time, close float64) {
		if _, err := db.Exec(`INSERT INTO candle_archive (pair,timeframe,timestamp,open,high,low,close,volume) VALUES (?,?,?,?,?,?,?,?)`,
			pair, "1d", ts.Unix(), close, close, close, close, 1.0); err != nil {
			t.Fatal(err)
		}
	}
	ins("AAA-USDT", t0, 100)
	ins("AAA-USDT", t1, 110)
	ins("BBB-USDT", t0, 50)
	ins("BBB-USDT", t1, 45)

	baskets := Baskets{Longs: []string{"AAA-USDT"}, Shorts: []string{"BBB-USDT"}}
	if err := RecordShadow(db, t0, "X", baskets, map[string]float64{"AAA-USDT": 1, "BBB-USDT": -1}); err != nil {
		t.Fatal(err)
	}
	if err := RecordShadow(db, t1, "X", baskets, map[string]float64{"AAA-USDT": 1, "BBB-USDT": -1}); err != nil {
		t.Fatal(err)
	}

	// Gross: longRet=+0.10, shortRet=-0.10 → book = 0.5*0.10 - 0.5*(-0.10) = +0.10.
	eqGross, err := EvalShadowEquity(db, "X", "1d", 0)
	if err != nil {
		t.Fatalf("eval gross: %v", err)
	}
	if eqGross.NPeriods != 1 {
		t.Fatalf("want 1 period, got %d", eqGross.NPeriods)
	}
	if math.Abs(eqGross.GrossRet-0.10) > 1e-9 {
		t.Fatalf("gross ret: want +0.10, got %v", eqGross.GrossRet)
	}
	if math.Abs(eqGross.TotalRet-0.10) > 1e-9 {
		t.Fatalf("net ret (cost 0): want +0.10, got %v", eqGross.TotalRet)
	}

	// With cost: first period is a full fresh rotation (turnover=1.0), so net =
	// 0.10 - 1.0*0.0012 = 0.0988.
	eqCost, err := EvalShadowEquity(db, "X", "1d", 0.0012)
	if err != nil {
		t.Fatalf("eval cost: %v", err)
	}
	if math.Abs(eqCost.TotalRet-0.0988) > 1e-9 {
		t.Fatalf("net ret (cost .0012): want +0.0988, got %v", eqCost.TotalRet)
	}
	if math.Abs(eqCost.GrossRet-0.10) > 1e-9 {
		t.Fatalf("gross unchanged by cost: want +0.10, got %v", eqCost.GrossRet)
	}
}

// TestEvalShadowEquity_Empty: a scorer with no records yields an empty curve.
func TestEvalShadowEquity_Empty(t *testing.T) {
	db, err := sql.Open("sqlite3", t.TempDir()+"/shadow.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE candle_archive (pair TEXT, timeframe TEXT, timestamp INTEGER, open REAL, high REAL, low REAL, close REAL, volume REAL, PRIMARY KEY(pair,timeframe,timestamp))`); err != nil {
		t.Fatal(err)
	}
	eq, err := EvalShadowEquity(db, "nobody", "1d", 0)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if eq.NPeriods != 0 || len(eq.Curve) != 0 {
		t.Fatalf("want empty, got %d periods", eq.NPeriods)
	}
}
