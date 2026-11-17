package gate

import (
	"encoding/json"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/rantipay/trading-mcp/internal/launch/core"
)

var t0 = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

func cfg() Config {
	return Config{Requirements: Requirements{MinTrades: 30, MinShadowWeeks: 4, MaxFraudBuys: 0}, CILevel: 0.95, Resamples: 2000}
}

// n trades of pnl(i) closed one per hour starting at t0, first opened at t0.
func group(n int, pnl func(i int) float64) Group {
	g := Group{StrategyVersionID: 1, Mode: core.ModeShadow, FirstOpenedAt: t0}
	for i := 0; i < n; i++ {
		g.Trades = append(g.Trades, Trade{NetPnLUSD: pnl(i), ClosedAt: t0.Add(time.Duration(i+1) * time.Hour)})
	}
	return g
}

func now(weeks float64) time.Time { return t0.Add(time.Duration(weeks * 7 * 24 * float64(time.Hour))) }

func TestInsufficientData12of30(t *testing.T) {
	ev := Compute(group(12, func(i int) float64 { return 0.5 }), cfg(), now(1), rand.New(rand.NewSource(1)))
	if ev.Verdict != VerdictInsufficient || ev.Trades != 12 {
		t.Fatalf("%+v", ev)
	}
	if !strings.Contains(ev.Detail, "18 more trades") || !strings.Contains(ev.Detail, "3.0 more weeks") {
		t.Errorf("detail %q", ev.Detail)
	}
}

func TestInsufficientWeeksOnly(t *testing.T) {
	ev := Compute(group(40, func(i int) float64 { return 1 }), cfg(), now(2), rand.New(rand.NewSource(1)))
	if ev.Verdict != VerdictInsufficient || !strings.Contains(ev.Detail, "2.0 more weeks") || strings.Contains(ev.Detail, "trades") {
		t.Fatalf("%+v", ev)
	}
}

func TestNoEdgeWithOneFraudBuyDespitePositivePnL(t *testing.T) {
	g := group(40, func(i int) float64 { return 1 + 0.01*float64(i%5) })
	g.Trades[7].Fraud = true
	ev := Compute(g, cfg(), now(5), rand.New(rand.NewSource(1)))
	if ev.Verdict != VerdictNoEdge || ev.FraudBuys != 1 || ev.NetPnLUSD <= 0 || !strings.Contains(ev.Detail, "fraud_buys") {
		t.Fatalf("%+v", ev)
	}
}

func TestEdgeClearlyPositive(t *testing.T) {
	ev := Compute(group(40, func(i int) float64 { return 1 + 0.1*float64(i%3) }), cfg(), now(5), rand.New(rand.NewSource(1)))
	if ev.Verdict != VerdictEdge || ev.CILow == nil || *ev.CILow <= 0 || ev.ProfitFactor != nil || ev.MaxDrawdownUSD != 0 {
		t.Fatalf("%+v", ev)
	}
	if ev.ShadowWeeks < 4.99 || ev.ShadowWeeks > 5.01 {
		t.Errorf("weeks %v", ev.ShadowWeeks)
	}
}

func TestNoEdgeWhenCIIncludesZero(t *testing.T) {
	// alternating +1 / -0.9: mean 0.05, huge relative noise
	ev := Compute(group(40, func(i int) float64 {
		if i%2 == 0 {
			return 1
		}
		return -0.9
	}), cfg(), now(5), rand.New(rand.NewSource(1)))
	if ev.Verdict != VerdictNoEdge || *ev.CILow > 0 {
		t.Fatalf("%+v", ev)
	}
	// profit factor = 20 / 18 = 1.111...
	if ev.ProfitFactor == nil || *ev.ProfitFactor < 1.11 || *ev.ProfitFactor > 1.12 {
		t.Errorf("pf %v", ev.ProfitFactor)
	}
}

func TestMaxDrawdown(t *testing.T) {
	// cumulative: 2, 1, -1, 0, 3 -> peak 2, trough -1 -> dd 3
	var tr []Trade
	for i, v := range []float64{2, -1, -2, 1, 3} {
		tr = append(tr, Trade{NetPnLUSD: v, ClosedAt: t0.Add(time.Duration(i) * time.Hour)})
	}
	if dd := maxDrawdown(tr); dd != 3 {
		t.Fatalf("dd %v", dd)
	}
}

func TestBootstrapDeterministicWithSeed(t *testing.T) {
	vals := []float64{1, -2, 3, 0.5, -1, 2, 2.5, -0.5}
	a1, b1, _ := Bootstrap(vals, 0.95, 10000, rand.New(rand.NewSource(42)))
	a2, b2, _ := Bootstrap(vals, 0.95, 10000, rand.New(rand.NewSource(42)))
	a3, _, _ := Bootstrap(vals, 0.95, 10000, rand.New(rand.NewSource(43)))
	if a1 != a2 || b1 != b2 {
		t.Fatal("same seed must give the same interval")
	}
	if a1 == a3 {
		t.Log("different seeds happened to coincide (unlikely)")
	}
	if a1 >= b1 {
		t.Fatalf("lo %v hi %v", a1, b1)
	}
	if _, _, ok := Bootstrap([]float64{1}, 0.95, 100, rand.New(rand.NewSource(1))); ok {
		t.Fatal("n=1 has no interval")
	}
}

func TestCounterfactualExcludedFromGate(t *testing.T) {
	if !Eligible(core.ChannelAuto) || !Eligible(core.ChannelManual) || Eligible(core.ChannelCounterfactual) {
		t.Fatal("only auto and manual feed the gate")
	}
}

func TestEvaluationJSONShape(t *testing.T) {
	ev := Compute(group(12, func(i int) float64 { return 0.5 }), cfg(), now(1), rand.New(rand.NewSource(1)))
	b, _ := json.Marshal(ev)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"id", "strategy_version_id", "mode", "verdict", "trades", "shadow_weeks", "net_pnl_usd", "mean_pnl_usd",
		"ci_low", "ci_high", "ci_level", "profit_factor", "max_drawdown_usd", "fraud_buys", "requirements", "detail", "evaluated_at"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing %s", k)
		}
	}
	req := m["requirements"].(map[string]any)
	for _, k := range []string{"min_trades", "min_shadow_weeks", "max_fraud_buys"} {
		if _, ok := req[k]; !ok {
			t.Errorf("missing requirements.%s", k)
		}
	}
}

func TestHumanLift(t *testing.T) {
	rows := []LiftRow{
		{core.ChannelManual, "community", 2}, {core.ChannelManual, "chart", -1}, {core.ChannelManual, "chart", 1},
		{core.ChannelCounterfactual, "community", -1}, {core.ChannelCounterfactual, "chart", -2},
		{core.ChannelAuto, "none", 0.5},
	}
	r := BuildHumanLift(rows, 3)
	if r.Status != "OK" || r.Channels.Manual.Trades != 3 || r.Channels.Manual.NetPnLUSD != 2 || r.Channels.Counterfactual.NetPnLUSD != -3 {
		t.Fatalf("%+v", r)
	}
	if r.ManualMinusCounterfactualNetUSD == nil || *r.ManualMinusCounterfactualNetUSD != 5 {
		t.Fatalf("diff %v", r.ManualMinusCounterfactualNetUSD)
	}
	if got := r.Channels.Manual.WinRate; got < 0.666 || got > 0.667 {
		t.Errorf("win rate %v", got)
	}
	if r.Channels.Manual.ProfitFactor == nil || *r.Channels.Manual.ProfitFactor != 3 {
		t.Errorf("pf %v", r.Channels.Manual.ProfitFactor)
	}
	if len(r.ByCategory) != 2 || r.ByCategory[0].Category != "chart" || r.ByCategory[0].Manual.Trades != 2 || r.ByCategory[1].Counterfactual.NetPnLUSD != -1 {
		t.Errorf("by_category %+v", r.ByCategory)
	}
	if r := BuildHumanLift(rows, 4); r.Status != VerdictInsufficient || r.ManualMinusCounterfactualNetUSD != nil {
		t.Errorf("insufficient: %+v", r)
	}
}
