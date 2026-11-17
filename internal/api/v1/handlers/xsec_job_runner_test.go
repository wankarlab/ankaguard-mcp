package handlers

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/xsec"
)

func newXSecJobsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// Each pooled connection to ":memory:" is a separate empty database; the
	// runner's goroutine could otherwise read a connection without the job.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func testLogger() *logrus.Logger {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel) // keep test output quiet
	return log
}

func intPtr(v int) *int           { return &v }
func floatPtr(v float64) *float64 { return &v }
func boolPtr(v bool) *bool        { return &v }

// ---- BuildArgv: mode -> argv mapping ----

func TestBuildArgv_SyncBook(t *testing.T) {
	r := NewXSecJobRunner(newXSecJobsTestDB(t), testLogger())
	argv, err := r.BuildArgv(XSecJobModeSyncBook, XSecJobRequest{})
	if err != nil {
		t.Fatalf("build argv: %v", err)
	}
	want := []string{"-db", "data/trading.db", "-sync-book"}
	if !equalArgv(argv, want) {
		t.Fatalf("got %v, want %v", argv, want)
	}
}

func TestBuildArgv_RebalanceDry_NoOverrides(t *testing.T) {
	r := NewXSecJobRunner(newXSecJobsTestDB(t), testLogger())
	argv, err := r.BuildArgv(XSecJobModeRebalanceDry, XSecJobRequest{})
	if err != nil {
		t.Fatalf("build argv: %v", err)
	}
	want := []string{"-db", "data/trading.db", "-rebalance", "-target-source", "compute"}
	if !equalArgv(argv, want) {
		t.Fatalf("got %v, want %v", argv, want)
	}
	for _, forbidden := range []string{"-live", "-i-verified-futures-sizing"} {
		if containsArg(argv, forbidden) {
			t.Fatalf("rebalance-dry argv must never contain %s, got %v", forbidden, argv)
		}
	}
}

func TestBuildArgv_RebalanceDry_WithOverrides(t *testing.T) {
	r := NewXSecJobRunner(newXSecJobsTestDB(t), testLogger())
	argv, err := r.BuildArgv(XSecJobModeRebalanceDry, XSecJobRequest{K: intPtr(5), MaxPosUSD: floatPtr(3.0)})
	if err != nil {
		t.Fatalf("build argv: %v", err)
	}
	want := []string{"-db", "data/trading.db", "-rebalance", "-target-source", "compute", "-k", "5", "-max-pos-usd", "3"}
	if !equalArgv(argv, want) {
		t.Fatalf("got %v, want %v", argv, want)
	}
}

func TestBuildArgv_RebalanceLive_AppendsLiveFlagsExactlyOnce(t *testing.T) {
	r := NewXSecJobRunner(newXSecJobsTestDB(t), testLogger())
	argv, err := r.BuildArgv(XSecJobModeRebalanceLive, XSecJobRequest{K: intPtr(5), MaxPosUSD: floatPtr(3.0)})
	if err != nil {
		t.Fatalf("build argv: %v", err)
	}
	want := []string{"-db", "data/trading.db", "-rebalance", "-target-source", "compute", "-k", "5", "-max-pos-usd", "3", "-live", "-i-verified-futures-sizing"}
	if !equalArgv(argv, want) {
		t.Fatalf("got %v, want %v", argv, want)
	}
	if countArg(argv, "-live") != 1 {
		t.Fatalf("-live must appear exactly once, got argv %v", argv)
	}
	if countArg(argv, "-i-verified-futures-sizing") != 1 {
		t.Fatalf("-i-verified-futures-sizing must appear exactly once, got argv %v", argv)
	}
}

func TestBuildArgv_LiveFlagsNeverAppendedForDryOrOtherModes(t *testing.T) {
	r := NewXSecJobRunner(newXSecJobsTestDB(t), testLogger())
	for _, mode := range []string{XSecJobModeSyncBook, XSecJobModeRebalanceDry} {
		argv, err := r.BuildArgv(mode, XSecJobRequest{K: intPtr(5), MaxPosUSD: floatPtr(3.0)})
		if err != nil && mode == XSecJobModeSyncBook {
			// sync-book ignores k/maxPosUSD entirely; fine either way, but if it
			// errors that would be a behavior change worth catching explicitly.
			t.Fatalf("sync-book build argv unexpectedly failed: %v", err)
		}
		if err != nil {
			continue
		}
		if containsArg(argv, "-live") || containsArg(argv, "-i-verified-futures-sizing") {
			t.Fatalf("mode %s must never receive live flags, got %v", mode, argv)
		}
	}
}

func TestBuildArgv_KOutOfRange(t *testing.T) {
	r := NewXSecJobRunner(newXSecJobsTestDB(t), testLogger())
	for _, k := range []int{0, -1, 21, 100} {
		if _, err := r.BuildArgv(XSecJobModeRebalanceDry, XSecJobRequest{K: intPtr(k)}); err == nil {
			t.Fatalf("expected error for out-of-range k=%d", k)
		}
	}
	for _, k := range []int{1, 20, 10} {
		if _, err := r.BuildArgv(XSecJobModeRebalanceDry, XSecJobRequest{K: intPtr(k)}); err != nil {
			t.Fatalf("expected no error for in-range k=%d, got %v", k, err)
		}
	}
}

func TestBuildArgv_MaxPosUSDOutOfRange(t *testing.T) {
	r := NewXSecJobRunner(newXSecJobsTestDB(t), testLogger())
	for _, v := range []float64{0, 0.49, 5000.01, 20000} {
		if _, err := r.BuildArgv(XSecJobModeRebalanceDry, XSecJobRequest{MaxPosUSD: floatPtr(v)}); err == nil {
			t.Fatalf("expected error for out-of-range max_pos_usd=%v", v)
		}
	}
	for _, v := range []float64{0.5, 50, 3.0, 533.33, 5000} {
		if _, err := r.BuildArgv(XSecJobModeRebalanceDry, XSecJobRequest{MaxPosUSD: floatPtr(v)}); err != nil {
			t.Fatalf("expected no error for in-range max_pos_usd=%v, got %v", v, err)
		}
	}
}

func TestBuildArgv_LookbackOutOfRange(t *testing.T) {
	r := NewXSecJobRunner(newXSecJobsTestDB(t), testLogger())
	for _, lb := range []int{0, 1, -5, 61, 365} {
		if _, err := r.BuildArgv(XSecJobModeRebalanceDry, XSecJobRequest{Lookback: intPtr(lb)}); err == nil {
			t.Fatalf("expected error for out-of-range lookback=%d", lb)
		}
	}
	for _, lb := range []int{2, 10, 14, 60} {
		if _, err := r.BuildArgv(XSecJobModeRebalanceDry, XSecJobRequest{Lookback: intPtr(lb)}); err != nil {
			t.Fatalf("expected no error for in-range lookback=%d, got %v", lb, err)
		}
	}
}

func TestBuildArgv_MaxTotalUSDOutOfRange(t *testing.T) {
	r := NewXSecJobRunner(newXSecJobsTestDB(t), testLogger())
	for _, v := range []float64{0, 4.99, 100000.01, 500000} {
		if _, err := r.BuildArgv(XSecJobModeRebalanceDry, XSecJobRequest{MaxTotalUSD: floatPtr(v)}); err == nil {
			t.Fatalf("expected error for out-of-range max_total_usd=%v", v)
		}
	}
	for _, v := range []float64{5, 50, 100, 500, 9600, 100000} {
		if _, err := r.BuildArgv(XSecJobModeRebalanceDry, XSecJobRequest{MaxTotalUSD: floatPtr(v)}); err != nil {
			t.Fatalf("expected no error for in-range max_total_usd=%v, got %v", v, err)
		}
	}
}

func TestBuildArgv_RebalanceDry_AllOverridesDeterministicOrder(t *testing.T) {
	r := NewXSecJobRunner(newXSecJobsTestDB(t), testLogger())
	argv, err := r.BuildArgv(XSecJobModeRebalanceDry, XSecJobRequest{
		K: intPtr(10), MaxPosUSD: floatPtr(2.5), Lookback: intPtr(10), MaxTotalUSD: floatPtr(50),
	})
	if err != nil {
		t.Fatalf("build argv: %v", err)
	}
	want := []string{
		"-db", "data/trading.db", "-rebalance", "-target-source", "compute",
		"-k", "10", "-max-pos-usd", "2.5", "-lookback", "10", "-max-total-usd", "50",
	}
	if !equalArgv(argv, want) {
		t.Fatalf("got %v, want %v", argv, want)
	}
}

func TestBuildArgv_Backtest_NoOverrides(t *testing.T) {
	r := NewXSecJobRunner(newXSecJobsTestDB(t), testLogger())
	argv, err := r.BuildArgv(XSecJobModeBacktest, XSecJobRequest{})
	if err != nil {
		t.Fatalf("build argv: %v", err)
	}
	want := []string{"-db", "data/trading.db", "-print-equity"}
	if !equalArgv(argv, want) {
		t.Fatalf("got %v, want %v", argv, want)
	}
	for _, forbidden := range []string{"-live", "-i-verified-futures-sizing", "-rebalance"} {
		if containsArg(argv, forbidden) {
			t.Fatalf("backtest argv must never contain %s, got %v", forbidden, argv)
		}
	}
}

func TestBuildArgv_Backtest_AllOverridesDeterministicOrder(t *testing.T) {
	r := NewXSecJobRunner(newXSecJobsTestDB(t), testLogger())
	argv, err := r.BuildArgv(XSecJobModeBacktest, XSecJobRequest{
		Lookback: intPtr(10), K: intPtr(10), Rebal: intPtr(7), Band: intPtr(2),
		Cost: floatPtr(0.0012), Funding: floatPtr(0.0001), Capital: floatPtr(1000),
		VolWeights: boolPtr(true), MinTurnoverUSD: floatPtr(3_000_000), Windows: intPtr(4),
	})
	if err != nil {
		t.Fatalf("build argv: %v", err)
	}
	want := []string{
		"-db", "data/trading.db",
		"-lb", "10", "-k", "10", "-rebal", "7", "-band", "2",
		"-cost", "0.0012", "-funding", "0.0001", "-capital", "1000",
		"-vol-weights=true", "-min-turnover-usd", "3000000", "-windows", "4", "-print-equity",
	}
	if !equalArgv(argv, want) {
		t.Fatalf("got %v, want %v", argv, want)
	}
}

func TestBuildArgv_Backtest_LookbackOutOfRange(t *testing.T) {
	r := NewXSecJobRunner(newXSecJobsTestDB(t), testLogger())
	for _, v := range []int{0, 1, 121, 1000} {
		if _, err := r.BuildArgv(XSecJobModeBacktest, XSecJobRequest{Lookback: intPtr(v)}); err == nil {
			t.Fatalf("expected error for out-of-range lookback=%d", v)
		}
	}
	for _, v := range []int{2, 120, 10} {
		if _, err := r.BuildArgv(XSecJobModeBacktest, XSecJobRequest{Lookback: intPtr(v)}); err != nil {
			t.Fatalf("expected no error for in-range lookback=%d, got %v", v, err)
		}
	}
}

func TestBuildArgv_Backtest_WindowsOutOfRange(t *testing.T) {
	r := NewXSecJobRunner(newXSecJobsTestDB(t), testLogger())
	for _, v := range []int{0, -1, 13, 100} {
		if _, err := r.BuildArgv(XSecJobModeBacktest, XSecJobRequest{Windows: intPtr(v)}); err == nil {
			t.Fatalf("expected error for out-of-range windows=%d", v)
		}
	}
	for _, v := range []int{1, 12, 4} {
		if _, err := r.BuildArgv(XSecJobModeBacktest, XSecJobRequest{Windows: intPtr(v)}); err != nil {
			t.Fatalf("expected no error for in-range windows=%d, got %v", v, err)
		}
	}
}

func TestBuildArgv_Backtest_CostOutOfRange(t *testing.T) {
	r := NewXSecJobRunner(newXSecJobsTestDB(t), testLogger())
	for _, v := range []float64{-0.001, 0.021, 1} {
		if _, err := r.BuildArgv(XSecJobModeBacktest, XSecJobRequest{Cost: floatPtr(v)}); err == nil {
			t.Fatalf("expected error for out-of-range cost=%v", v)
		}
	}
	for _, v := range []float64{0, 0.02, 0.0012} {
		if _, err := r.BuildArgv(XSecJobModeBacktest, XSecJobRequest{Cost: floatPtr(v)}); err != nil {
			t.Fatalf("expected no error for in-range cost=%v, got %v", v, err)
		}
	}
}

func TestBuildArgv_Backtest_DoesNotRequireLevel2(t *testing.T) {
	if xsecJobModesRequiringLevel2[XSecJobModeBacktest] {
		t.Fatal("backtest is read-only historical simulation (no exchange calls) — must not require Level 2")
	}
}

// Regression test for 2026-07-23: a bool flag emitted as two argv elements
// ("-vol-weights", "true") makes Go's flag package treat "-vol-weights" as
// already satisfied and "true" as the first POSITIONAL argument, silently
// ending flag parsing right there — every flag appended after it in argv is
// then silently ignored by the subprocess (verified against the real
// xsec-sim binary: with vol-weights sent as two tokens, -min-turnover-usd
// and -windows never took effect on ANY backtest run, since the UI always
// sends vol_weights). Must always be a single "-flag=value" token.
func TestBuildArgv_Backtest_VolWeightsIsSingleToken(t *testing.T) {
	r := NewXSecJobRunner(newXSecJobsTestDB(t), testLogger())
	for _, v := range []bool{true, false} {
		argv, err := r.BuildArgv(XSecJobModeBacktest, XSecJobRequest{VolWeights: boolPtr(v)})
		if err != nil {
			t.Fatalf("build argv: %v", err)
		}
		want := "-vol-weights=" + strconv.FormatBool(v)
		found := false
		for _, a := range argv {
			if a == "-vol-weights" {
				t.Fatalf("vol-weights must never be a bare flag followed by a separate value token, got argv %v", argv)
			}
			if a == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected argv to contain %q, got %v", want, argv)
		}
	}
}

// ---- BuildArgv: backtest-sweep ----

func TestBuildArgv_BacktestSweep_BaseExcludesSweptDimension(t *testing.T) {
	r := NewXSecJobRunner(newXSecJobsTestDB(t), testLogger())
	argv, err := r.BuildArgv(XSecJobModeBacktestSweep, XSecJobRequest{
		Lookback: intPtr(10), K: intPtr(99), // K present but must be dropped: sweep_param=k
		SweepParam:  "k",
		SweepValues: []float64{8, 9, 10},
	})
	if err != nil {
		t.Fatalf("build argv: %v", err)
	}
	if containsArg(argv, "-k") {
		t.Fatalf("base argv must exclude the swept dimension (-k), got %v", argv)
	}
	if !containsArg(argv, "-lb") {
		t.Fatalf("base argv must keep non-swept overrides (-lb), got %v", argv)
	}
	if !containsArg(argv, "-print-equity") {
		t.Fatalf("backtest-sweep base argv must still request -print-equity, got %v", argv)
	}
}

func TestBuildArgv_BacktestSweep_InvalidParam(t *testing.T) {
	r := NewXSecJobRunner(newXSecJobsTestDB(t), testLogger())
	_, err := r.BuildArgv(XSecJobModeBacktestSweep, XSecJobRequest{SweepParam: "cost", SweepValues: []float64{1}})
	if err == nil {
		t.Fatal("expected error for sweep_param outside {lb,k,rebal,band}, got nil")
	}
}

func TestBuildArgv_BacktestSweep_ValuesOutOfRange(t *testing.T) {
	r := NewXSecJobRunner(newXSecJobsTestDB(t), testLogger())
	_, err := r.BuildArgv(XSecJobModeBacktestSweep, XSecJobRequest{SweepParam: "k", SweepValues: []float64{5, 21}})
	if err == nil {
		t.Fatal("expected error when any sweep value is out of the k range [1,20], got nil")
	}
}

func TestBuildArgv_BacktestSweep_TooManyValues(t *testing.T) {
	r := NewXSecJobRunner(newXSecJobsTestDB(t), testLogger())
	values := make([]float64, maxSweepValues+1)
	for i := range values {
		values[i] = 2 // valid lb value, only the COUNT should fail
	}
	_, err := r.BuildArgv(XSecJobModeBacktestSweep, XSecJobRequest{SweepParam: "lb", SweepValues: values})
	if err == nil {
		t.Fatalf("expected error for %d sweep values (max %d), got nil", len(values), maxSweepValues)
	}
}

func TestBuildArgv_BacktestSweep_EmptyValues(t *testing.T) {
	r := NewXSecJobRunner(newXSecJobsTestDB(t), testLogger())
	_, err := r.BuildArgv(XSecJobModeBacktestSweep, XSecJobRequest{SweepParam: "lb", SweepValues: nil})
	if err == nil {
		t.Fatal("expected error for empty sweep_values, got nil")
	}
}

// ---- BuildArgv: backtest advanced flags (Fase 3) ----

func TestBuildArgv_Backtest_AdvancedFlagsAllPresent(t *testing.T) {
	r := NewXSecJobRunner(newXSecJobsTestDB(t), testLogger())
	argv, err := r.BuildArgv(XSecJobModeBacktest, XSecJobRequest{
		StopShort: floatPtr(0.1), StopLong: floatPtr(0.1), TPLiquidate: floatPtr(0.02),
		MakerFillProb: floatPtr(0.8), TakerCost: floatPtr(0.0024), VolTarget: floatPtr(0.5),
		MaxShortWeight: floatPtr(0.3), FundingTilt: floatPtr(0.5), Pairs: "BTC-USDT,ETH-USDT",
		StartFrac: floatPtr(0), EndFrac: floatPtr(0.7),
	})
	if err != nil {
		t.Fatalf("build argv: %v", err)
	}
	for _, want := range []string{
		"-stop-short", "-stop-long", "-tp-liquidate", "-maker-fill-prob", "-taker-cost",
		"-vol-target", "-max-short-weight", "-funding-tilt", "-pairs", "-start-frac", "-end-frac",
	} {
		if !containsArg(argv, want) {
			t.Fatalf("expected argv to contain %s, got %v", want, argv)
		}
	}
	if !containsArg(argv, "BTC-USDT,ETH-USDT") {
		t.Fatalf("expected -pairs value to be passed through verbatim, got %v", argv)
	}
}

func TestBuildArgv_Backtest_StopShortOutOfRange(t *testing.T) {
	r := NewXSecJobRunner(newXSecJobsTestDB(t), testLogger())
	for _, v := range []float64{-0.01, 0.51} {
		if _, err := r.BuildArgv(XSecJobModeBacktest, XSecJobRequest{StopShort: floatPtr(v)}); err == nil {
			t.Fatalf("expected error for out-of-range stop_short=%v", v)
		}
	}
	for _, v := range []float64{0, 0.5, 0.1} {
		if _, err := r.BuildArgv(XSecJobModeBacktest, XSecJobRequest{StopShort: floatPtr(v)}); err != nil {
			t.Fatalf("expected no error for in-range stop_short=%v, got %v", v, err)
		}
	}
}

func TestBuildArgv_Backtest_PairsValidatesEachEntry(t *testing.T) {
	r := NewXSecJobRunner(newXSecJobsTestDB(t), testLogger())
	if _, err := r.BuildArgv(XSecJobModeBacktest, XSecJobRequest{Pairs: "BTC-USDT, not-a-pair"}); err == nil {
		t.Fatal("expected error for an invalid pair in the CSV list")
	}
	if _, err := r.BuildArgv(XSecJobModeBacktest, XSecJobRequest{Pairs: "BTC-USDT, ETH-USDT"}); err != nil {
		t.Fatalf("expected no error for a valid CSV pair list (with spaces), got %v", err)
	}
}

func TestBuildArgv_Backtest_StartFracMustBeLessThanEndFrac(t *testing.T) {
	r := NewXSecJobRunner(newXSecJobsTestDB(t), testLogger())
	if _, err := r.BuildArgv(XSecJobModeBacktest, XSecJobRequest{StartFrac: floatPtr(0.7), EndFrac: floatPtr(0.7)}); err == nil {
		t.Fatal("expected error when start_frac == end_frac")
	}
	if _, err := r.BuildArgv(XSecJobModeBacktest, XSecJobRequest{StartFrac: floatPtr(0.8), EndFrac: floatPtr(0.3)}); err == nil {
		t.Fatal("expected error when start_frac > end_frac")
	}
	if _, err := r.BuildArgv(XSecJobModeBacktest, XSecJobRequest{StartFrac: floatPtr(0.3), EndFrac: floatPtr(0.7)}); err != nil {
		t.Fatalf("expected no error when start_frac < end_frac, got %v", err)
	}
}

func TestBuildArgv_BacktestSweep_DoesNotRequireLevel2(t *testing.T) {
	if xsecJobModesRequiringLevel2[XSecJobModeBacktestSweep] {
		t.Fatal("backtest-sweep is read-only historical simulation (no exchange calls) — must not require Level 2")
	}
}

func TestSweepSpecFor_ResolvesFlagAndValues(t *testing.T) {
	spec := sweepSpecFor(XSecJobRequest{
		Mode: XSecJobModeBacktestSweep, SweepParam: "rebal", SweepValues: []float64{5, 7, 10},
	})
	if spec == nil {
		t.Fatal("expected a non-nil sweepSpec for a backtest-sweep request")
	}
	if spec.flag != "-rebal" {
		t.Fatalf("flag: want -rebal, got %q", spec.flag)
	}
	if !equalArgv(argvFromFloats(spec.values), argvFromFloats([]float64{5, 7, 10})) {
		t.Fatalf("values: got %v", spec.values)
	}
}

func TestSweepSpecFor_NilForNonSweepModes(t *testing.T) {
	if sweepSpecFor(XSecJobRequest{Mode: XSecJobModeBacktest}) != nil {
		t.Fatal("expected nil sweepSpec for mode=backtest")
	}
	if sweepSpecFor(XSecJobRequest{Mode: XSecJobModeSyncBook}) != nil {
		t.Fatal("expected nil sweepSpec for mode=sync-book")
	}
}

// argvFromFloats is a small test-only helper to compare float64 slices via
// the existing equalArgv string-slice helper.
func argvFromFloats(vs []float64) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = strconv.FormatFloat(v, 'f', -1, 64)
	}
	return out
}

func TestBuildArgv_CancelOrder_ValidatesOrderID(t *testing.T) {
	r := NewXSecJobRunner(newXSecJobsTestDB(t), testLogger())

	valid := []string{"1234567890", "123456789012345678901234", "0000000000"}
	for _, id := range valid {
		argv, err := r.BuildArgv(XSecJobModeCancelOrder, XSecJobRequest{OrderID: id})
		if err != nil {
			t.Fatalf("expected order_id %q to be valid, got error: %v", id, err)
		}
		want := []string{"-cancel-order", id}
		if !equalArgv(argv, want) {
			t.Fatalf("got %v, want %v", argv, want)
		}
	}

	invalid := []string{
		"",
		"123",                        // too short
		"12345678901234567890123456", // too long (26 digits)
		"abc1234567890",              // non-numeric
		"1234567890; rm -rf /",       // shell injection attempt
		"1234567890 ",                // trailing whitespace
	}
	for _, id := range invalid {
		if _, err := r.BuildArgv(XSecJobModeCancelOrder, XSecJobRequest{OrderID: id}); err == nil {
			t.Fatalf("expected order_id %q to be rejected", id)
		}
	}
}

func TestBuildArgv_CloseLeg_ValidatesPair(t *testing.T) {
	r := NewXSecJobRunner(newXSecJobsTestDB(t), testLogger())

	valid := []string{"AAVE-USDT", "ADA-USDT", "SOL-USDT", "H-USDT"}
	for _, pair := range valid {
		argv, err := r.BuildArgv(XSecJobModeCloseLeg, XSecJobRequest{Pair: pair})
		if err != nil {
			t.Fatalf("expected pair %q to be valid, got error: %v", pair, err)
		}
		want := []string{"-db", "data/trading.db", "-close-leg", pair}
		if !equalArgv(argv, want) {
			t.Fatalf("got %v, want %v", argv, want)
		}
	}

	invalid := []string{
		"",
		"aave-usdt",             // lowercase
		"AAVE",                  // missing -USDT
		"AAVE-BTC",              // wrong quote
		"AAVE-USDT; rm -rf /",   // shell injection attempt
		"AAVE-USDT ",            // trailing whitespace
		"TOOLONGBASESYMBOL-USDT", // exceeds 15-char base cap
	}
	for _, pair := range invalid {
		if _, err := r.BuildArgv(XSecJobModeCloseLeg, XSecJobRequest{Pair: pair}); err == nil {
			t.Fatalf("expected pair %q to be rejected", pair)
		}
	}
}

func TestBuildArgv_UnknownMode(t *testing.T) {
	r := NewXSecJobRunner(newXSecJobsTestDB(t), testLogger())
	if _, err := r.BuildArgv("delete-everything", XSecJobRequest{}); err == nil {
		t.Fatalf("expected error for unknown mode")
	}
}

// ---- Start: validation, single-flight, dry-run-first guard ----

func TestStart_InvalidModeReturns400(t *testing.T) {
	db := newXSecJobsTestDB(t)
	r := NewXSecJobRunner(db, testLogger()).WithBinPath("/bin/echo")

	_, err := r.Start(XSecJobRequest{Mode: "not-a-real-mode"}, "user-1")
	assertStartErrorStatus(t, err, 400)
}

func TestStart_RebalanceLive_RejectedWithoutPriorDryRun(t *testing.T) {
	db := newXSecJobsTestDB(t)
	r := NewXSecJobRunner(db, testLogger()).WithBinPath("/bin/echo")

	_, err := r.Start(XSecJobRequest{Mode: XSecJobModeRebalanceLive, K: intPtr(5), MaxPosUSD: floatPtr(3.0)}, "user-1")
	assertStartErrorStatus(t, err, 412)
}

func TestStart_RebalanceLive_RejectedWhenOverridesDiffer(t *testing.T) {
	db := newXSecJobsTestDB(t)
	r := NewXSecJobRunner(db, testLogger()).WithBinPath("/bin/echo")

	// A completed dry run exists, but with k=5, not k=7.
	dryArgs, err := r.BuildArgv(XSecJobModeRebalanceDry, XSecJobRequest{K: intPtr(5), MaxPosUSD: floatPtr(3.0)})
	if err != nil {
		t.Fatalf("build dry argv: %v", err)
	}
	id, err := xsec.CreateJob(db, XSecJobModeRebalanceDry, dryArgs, "user-1")
	if err != nil {
		t.Fatalf("create dry job: %v", err)
	}
	if err := xsec.FinishJob(db, id, xsec.JobStatusDone, 0, "plan"); err != nil {
		t.Fatalf("finish dry job: %v", err)
	}

	_, err = r.Start(XSecJobRequest{Mode: XSecJobModeRebalanceLive, K: intPtr(7), MaxPosUSD: floatPtr(3.0)}, "user-1")
	assertStartErrorStatus(t, err, 412)
}

func TestStart_RebalanceLive_RejectedWhenLookbackOrMaxTotalDiffer(t *testing.T) {
	db := newXSecJobsTestDB(t)
	r := NewXSecJobRunner(db, testLogger()).WithBinPath("/bin/echo")

	// A completed dry run exists with lookback=10, max_total=50.
	dryReq := XSecJobRequest{K: intPtr(10), MaxPosUSD: floatPtr(2.5), Lookback: intPtr(10), MaxTotalUSD: floatPtr(50)}
	dryArgs, err := r.BuildArgv(XSecJobModeRebalanceDry, dryReq)
	if err != nil {
		t.Fatalf("build dry argv: %v", err)
	}
	id, err := xsec.CreateJob(db, XSecJobModeRebalanceDry, dryArgs, "user-1")
	if err != nil {
		t.Fatalf("create dry job: %v", err)
	}
	if err := xsec.FinishJob(db, id, xsec.JobStatusDone, 0, "plan"); err != nil {
		t.Fatalf("finish dry job: %v", err)
	}

	// Same k/max_pos but a DIFFERENT lookback must not pass the gate.
	_, err = r.Start(XSecJobRequest{
		Mode: XSecJobModeRebalanceLive,
		K:    intPtr(10), MaxPosUSD: floatPtr(2.5), Lookback: intPtr(14), MaxTotalUSD: floatPtr(50),
	}, "user-1")
	assertStartErrorStatus(t, err, 412)

	// Same lookback but a DIFFERENT max_total must not pass either.
	_, err = r.Start(XSecJobRequest{
		Mode: XSecJobModeRebalanceLive,
		K:    intPtr(10), MaxPosUSD: floatPtr(2.5), Lookback: intPtr(10), MaxTotalUSD: floatPtr(100),
	}, "user-1")
	assertStartErrorStatus(t, err, 412)

	// Identical overrides pass.
	liveReq := dryReq
	liveReq.Mode = XSecJobModeRebalanceLive
	jobID, err := r.Start(liveReq, "user-1")
	if err != nil {
		t.Fatalf("expected identical overrides to pass the gate, got: %v", err)
	}
	waitForJobToFinish(t, db, jobID)
}

func TestStart_RebalanceLive_AllowedAfterMatchingDryRun(t *testing.T) {
	db := newXSecJobsTestDB(t)
	r := NewXSecJobRunner(db, testLogger()).WithBinPath("/bin/echo")

	dryArgs, err := r.BuildArgv(XSecJobModeRebalanceDry, XSecJobRequest{K: intPtr(5), MaxPosUSD: floatPtr(3.0)})
	if err != nil {
		t.Fatalf("build dry argv: %v", err)
	}
	id, err := xsec.CreateJob(db, XSecJobModeRebalanceDry, dryArgs, "user-1")
	if err != nil {
		t.Fatalf("create dry job: %v", err)
	}
	if err := xsec.FinishJob(db, id, xsec.JobStatusDone, 0, "plan"); err != nil {
		t.Fatalf("finish dry job: %v", err)
	}

	jobID, err := r.Start(XSecJobRequest{Mode: XSecJobModeRebalanceLive, K: intPtr(5), MaxPosUSD: floatPtr(3.0)}, "user-1")
	if err != nil {
		t.Fatalf("expected rebalance-live to be allowed, got error: %v", err)
	}
	waitForJobToFinish(t, db, jobID)

	job, err := xsec.GetJob(db, jobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if !containsArg(job.Args, "-live") || !containsArg(job.Args, "-i-verified-futures-sizing") {
		t.Fatalf("expected live flags in job args, got %v", job.Args)
	}
}

func TestStart_RebalanceLive_DryRunOutsideWindowRejected(t *testing.T) {
	db := newXSecJobsTestDB(t)
	r := NewXSecJobRunner(db, testLogger()).WithBinPath("/bin/echo")

	dryArgs, err := r.BuildArgv(XSecJobModeRebalanceDry, XSecJobRequest{K: intPtr(5), MaxPosUSD: floatPtr(3.0)})
	if err != nil {
		t.Fatalf("build dry argv: %v", err)
	}
	id, err := xsec.CreateJob(db, XSecJobModeRebalanceDry, dryArgs, "user-1")
	if err != nil {
		t.Fatalf("create dry job: %v", err)
	}
	if err := xsec.FinishJob(db, id, xsec.JobStatusDone, 0, "plan"); err != nil {
		t.Fatalf("finish dry job: %v", err)
	}
	// Backdate started_at well past the 10-minute dry-run-first window.
	if _, err := db.Exec(`UPDATE xsec_jobs SET started_at = ? WHERE id = ?`,
		time.Now().UTC().Add(-1*time.Hour).Format("2006-01-02 15:04:05"), id); err != nil {
		t.Fatalf("backdate job: %v", err)
	}

	_, err = r.Start(XSecJobRequest{Mode: XSecJobModeRebalanceLive, K: intPtr(5), MaxPosUSD: floatPtr(3.0)}, "user-1")
	assertStartErrorStatus(t, err, 412)
}

func TestStart_SingleFlight_RejectsWhileAnotherJobRunning(t *testing.T) {
	db := newXSecJobsTestDB(t)
	r := NewXSecJobRunner(db, testLogger()).WithBinPath("/bin/echo")

	// Simulate an already-running job directly (bypassing Start/execute) so the
	// test does not depend on subprocess timing.
	runningID, err := xsec.CreateJob(db, XSecJobModeSyncBook, []string{"-db", "data/trading.db", "-sync-book"}, "user-1")
	if err != nil {
		t.Fatalf("create running job: %v", err)
	}

	_, err = r.Start(XSecJobRequest{Mode: XSecJobModeSyncBook}, "user-2")
	startErr := assertStartErrorStatus(t, err, 409)
	if startErr.RunningJobID != runningID {
		t.Fatalf("expected conflict to report running job %d, got %d", runningID, startErr.RunningJobID)
	}
}

func TestStart_SucceedsAfterPriorJobFinished(t *testing.T) {
	db := newXSecJobsTestDB(t)
	r := NewXSecJobRunner(db, testLogger()).WithBinPath("/bin/echo")

	prevID, err := xsec.CreateJob(db, XSecJobModeSyncBook, []string{"-db", "data/trading.db", "-sync-book"}, "user-1")
	if err != nil {
		t.Fatalf("create previous job: %v", err)
	}
	if err := xsec.FinishJob(db, prevID, xsec.JobStatusDone, 0, "ok"); err != nil {
		t.Fatalf("finish previous job: %v", err)
	}

	jobID, err := r.Start(XSecJobRequest{Mode: XSecJobModeSyncBook}, "user-2")
	if err != nil {
		t.Fatalf("expected job to start once the prior one is finished, got: %v", err)
	}
	waitForJobToFinish(t, db, jobID)
}

// ---- subprocess execution ----

func TestExecute_SuccessMarksJobDoneWithCapturedOutput(t *testing.T) {
	db := newXSecJobsTestDB(t)
	r := NewXSecJobRunner(db, testLogger()).WithBinPath("/bin/echo")

	id, err := xsec.CreateJob(db, XSecJobModeSyncBook, []string{"hello-xsec-test"}, "user-1")
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	r.execute(id, XSecJobModeSyncBook, []string{"hello-xsec-test"}, nil)

	job, err := xsec.GetJob(db, id)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if job.Status != xsec.JobStatusDone {
		t.Fatalf("expected status done, got %s (output=%s)", job.Status, job.Output)
	}
	if !strings.Contains(job.Output, "hello-xsec-test") {
		t.Fatalf("expected captured output to contain echoed arg, got %q", job.Output)
	}
	if !job.ExitCode.Valid || job.ExitCode.Int64 != 0 {
		t.Fatalf("expected exit code 0, got %+v", job.ExitCode)
	}
}

func TestExecute_MissingBinaryMarksJobError(t *testing.T) {
	db := newXSecJobsTestDB(t)
	r := NewXSecJobRunner(db, testLogger()).WithBinPath("/no/such/xsec-live-binary")

	id, err := xsec.CreateJob(db, XSecJobModeSyncBook, []string{"-sync-book"}, "user-1")
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	r.execute(id, XSecJobModeSyncBook, []string{"-sync-book"}, nil)

	job, err := xsec.GetJob(db, id)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if job.Status != xsec.JobStatusError {
		t.Fatalf("expected status error, got %s", job.Status)
	}
	if !job.ExitCode.Valid || job.ExitCode.Int64 != -1 {
		t.Fatalf("expected exit code -1 for a start failure, got %+v", job.ExitCode)
	}
}

func TestExecute_TimeoutKillsSubprocessAndMarksError(t *testing.T) {
	db := newXSecJobsTestDB(t)
	r := NewXSecJobRunner(db, testLogger()).WithBinPath("/bin/sleep").WithTimeout(100 * time.Millisecond)

	id, err := xsec.CreateJob(db, XSecJobModeSyncBook, []string{"5"}, "user-1")
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	start := time.Now()
	r.execute(id, XSecJobModeSyncBook, []string{"5"}, nil)
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("expected the timeout to kill the subprocess well before its 5s sleep, took %v", elapsed)
	}

	job, err := xsec.GetJob(db, id)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if job.Status != xsec.JobStatusError {
		t.Fatalf("expected status error after timeout, got %s", job.Status)
	}
}

func TestExecuteSweep_ConcatenatesEachRunWithDelimiter(t *testing.T) {
	db := newXSecJobsTestDB(t)
	r := NewXSecJobRunner(db, testLogger()).WithSimBinPath("/bin/echo")

	argv := []string{"-db", "data/trading.db", "-print-equity"}
	id, err := xsec.CreateJob(db, XSecJobModeBacktestSweep, argv, "user-1")
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	sweep := &sweepSpec{flag: "-k", values: []float64{8, 9, 10}}
	r.execute(id, XSecJobModeBacktestSweep, argv, sweep)

	job, err := xsec.GetJob(db, id)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if job.Status != xsec.JobStatusDone {
		t.Fatalf("expected status done, got %s (output=%s)", job.Status, job.Output)
	}
	for i, v := range sweep.values {
		header := fmt.Sprintf("### SWEEP i=%d k=%v ###", i+1, v)
		if !strings.Contains(job.Output, header) {
			t.Fatalf("expected output to contain %q, got %q", header, job.Output)
		}
		// /bin/echo just echoes its argv, so each run's captured output
		// includes its own "-k 8" / "-k 9" / "-k 10" — confirms each
		// iteration actually got its OWN distinct argv, not a shared/stale one.
		runArg := fmt.Sprintf("-k %d", int(v))
		if !strings.Contains(job.Output, runArg) {
			t.Fatalf("expected output to contain %q (run %d's own argv), got %q", runArg, i+1, job.Output)
		}
	}
}

func TestExecuteSweep_OneFailureDoesNotAbortTheRest(t *testing.T) {
	db := newXSecJobsTestDB(t)
	r := NewXSecJobRunner(db, testLogger()).WithSimBinPath("/no/such/xsec-sim-binary")

	argv := []string{"-db", "data/trading.db"}
	id, err := xsec.CreateJob(db, XSecJobModeBacktestSweep, argv, "user-1")
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	sweep := &sweepSpec{flag: "-k", values: []float64{8, 9}}
	r.execute(id, XSecJobModeBacktestSweep, argv, sweep)

	job, err := xsec.GetJob(db, id)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	// Every run fails (binary doesn't exist) -> the whole sweep job is error,
	// but BOTH runs must still have been attempted (visible in the output),
	// not aborted after the first failure.
	if job.Status != xsec.JobStatusError {
		t.Fatalf("expected status error when every run fails, got %s", job.Status)
	}
	if !strings.Contains(job.Output, "### SWEEP i=1 k=8 ###") || !strings.Contains(job.Output, "### SWEEP i=2 k=9 ###") {
		t.Fatalf("expected both sweep runs to be attempted and recorded, got %q", job.Output)
	}
}

// ---- test helpers ----

func equalArgv(a, b []string) bool {
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

func containsArg(argv []string, want string) bool {
	for _, a := range argv {
		if a == want {
			return true
		}
	}
	return false
}

func countArg(argv []string, want string) int {
	n := 0
	for _, a := range argv {
		if a == want {
			n++
		}
	}
	return n
}

func assertStartErrorStatus(t *testing.T, err error, wantStatus int) *XSecJobStartError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error with status %d, got nil", wantStatus)
	}
	startErr, ok := err.(*XSecJobStartError)
	if !ok {
		t.Fatalf("expected *XSecJobStartError, got %T: %v", err, err)
	}
	if startErr.Status != wantStatus {
		t.Fatalf("expected status %d, got %d (%s)", wantStatus, startErr.Status, startErr.Message)
	}
	return startErr
}

// waitForJobToFinish polls until the job leaves status=running or the test
// deadline is hit — Start()'s subprocess runs in a background goroutine.
func waitForJobToFinish(t *testing.T, db *sql.DB, jobID int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, err := xsec.GetJob(db, jobID)
		if err != nil {
			t.Fatalf("get job: %v", err)
		}
		if job.Status != xsec.JobStatusRunning {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job %d did not finish within the test deadline", jobID)
}

// ---- autoconfig mode (read-only sizing search) ----

func TestBuildArgv_Autoconfig_NoOverrides(t *testing.T) {
	r := NewXSecJobRunner(newXSecJobsTestDB(t), testLogger())
	argv, err := r.BuildArgv(XSecJobModeAutoconfig, XSecJobRequest{})
	if err != nil {
		t.Fatalf("build argv: %v", err)
	}
	want := []string{"-db", "data/trading.db", "-autoconfig"}
	if !equalArgv(argv, want) {
		t.Fatalf("got %v, want %v", argv, want)
	}
	for _, forbidden := range []string{"-live", "-i-verified-futures-sizing", "-rebalance"} {
		if containsArg(argv, forbidden) {
			t.Fatalf("autoconfig argv must never contain %s, got %v", forbidden, argv)
		}
	}
}

func TestBuildArgv_Autoconfig_WithOverrides(t *testing.T) {
	r := NewXSecJobRunner(newXSecJobsTestDB(t), testLogger())
	argv, err := r.BuildArgv(XSecJobModeAutoconfig, XSecJobRequest{
		Capital: floatPtr(27.5), Utilization: floatPtr(0.8), Lookback: intPtr(14),
	})
	if err != nil {
		t.Fatalf("build argv: %v", err)
	}
	want := []string{"-db", "data/trading.db", "-autoconfig", "-autoconfig-capital", "27.5", "-autoconfig-util", "0.8", "-lookback", "14"}
	if !equalArgv(argv, want) {
		t.Fatalf("got %v, want %v", argv, want)
	}
}

func TestBuildArgv_Autoconfig_BoundsRejected(t *testing.T) {
	r := NewXSecJobRunner(newXSecJobsTestDB(t), testLogger())
	cases := []XSecJobRequest{
		{Capital: floatPtr(9.99)},
		{Capital: floatPtr(10_000_001)},
		{Utilization: floatPtr(0)},
		{Utilization: floatPtr(1.01)},
		{Lookback: intPtr(1)},
	}
	for i, req := range cases {
		if _, err := r.BuildArgv(XSecJobModeAutoconfig, req); err == nil {
			t.Errorf("case %d: expected validation error", i)
		}
	}
	// Boundaries are accepted.
	if _, err := r.BuildArgv(XSecJobModeAutoconfig, XSecJobRequest{Capital: floatPtr(10), Utilization: floatPtr(1)}); err != nil {
		t.Errorf("boundary values rejected: %v", err)
	}
	if _, err := r.BuildArgv(XSecJobModeAutoconfig, XSecJobRequest{Capital: floatPtr(10_000_000)}); err != nil {
		t.Errorf("max capital rejected: %v", err)
	}
}

func TestAutoconfig_ReadOnlyPosture(t *testing.T) {
	if xsecJobModesRequiringLevel2[XSecJobModeAutoconfig] {
		t.Fatal("autoconfig is read-only (places no orders) — must mirror rebalance-dry and not require Level 2")
	}
	if xsecJobModesRequiringLevel2[XSecJobModeRebalanceDry] != xsecJobModesRequiringLevel2[XSecJobModeAutoconfig] {
		t.Fatal("autoconfig level must match rebalance-dry")
	}
}

func TestStart_Autoconfig_NoDryRunFirstAndSingleFlight(t *testing.T) {
	db := newXSecJobsTestDB(t)
	r := NewXSecJobRunner(db, testLogger()).WithBinPath("/bin/echo")

	// Accepted with no prior dry-run (unlike rebalance-live).
	id, err := r.Start(XSecJobRequest{Mode: XSecJobModeAutoconfig}, "user-1")
	if err != nil {
		t.Fatalf("autoconfig start: %v", err)
	}
	waitForJobToFinish(t, db, id)

	// Honors the single-job lock.
	running, err := xsec.CreateJob(db, XSecJobModeSyncBook, []string{"-sync-book"}, "user-1")
	if err != nil {
		t.Fatalf("create blocking job: %v", err)
	}
	_, err = r.Start(XSecJobRequest{Mode: XSecJobModeAutoconfig}, "user-1")
	assertStartErrorStatus(t, err, 409)
	_ = xsec.FinishJob(db, running, xsec.JobStatusDone, 0, "")
}
