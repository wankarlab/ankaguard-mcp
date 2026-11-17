package exchange

import (
	"math"
	"testing"
)

func approxEq(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

func TestComputeEVHighFeesNegative(t *testing.T) {
	// $1000 notional, taker 50bps + slippage 30bps = 80bps = $8 cost, no funding.
	in := EVInput{
		Side:        "buy",
		GrossPrice:  100,
		SizeBase:    10,
		NotionalUSD: 1000,
		FeeBps:      50,
		SlippageBps: 30,
	}
	ev := ComputeEV(in)
	if !approxEq(ev.FeesUSD, 5, 1e-9) {
		t.Fatalf("fees: want 5, got %v", ev.FeesUSD)
	}
	if !approxEq(ev.SlippageUSD, 3, 1e-9) {
		t.Fatalf("slippage: want 3, got %v", ev.SlippageUSD)
	}
	if ev.ExpectedValueUSD >= 0 {
		t.Fatalf("EV should be negative with high friction, got %v", ev.ExpectedValueUSD)
	}
	if MeetsThreshold(ev, 0) {
		t.Fatalf("should NOT meet threshold 0, ev=%v", ev.ExpectedValueUSD)
	}
}

func TestComputeEVCleanSpreadPositive(t *testing.T) {
	// Low fee + funding received outweighs costs → positive EV.
	in := EVInput{
		Side:            "buy",
		GrossPrice:      100,
		SizeBase:        10,
		NotionalUSD:     1000,
		FeeBps:          2, // $0.20
		SlippageBps:     0,
		ExpectedFunding: 5, // received $5
	}
	ev := ComputeEV(in)
	// EV = -0.20 - 0 + 5 = 4.80
	if !approxEq(ev.ExpectedValueUSD, 4.8, 1e-9) {
		t.Fatalf("EV want 4.8, got %v", ev.ExpectedValueUSD)
	}
	if !MeetsThreshold(ev, 0) {
		t.Fatalf("should meet threshold 0, ev=%v", ev.ExpectedValueUSD)
	}
}

func TestComputeEVNetPriceSignBySide(t *testing.T) {
	buy := ComputeEV(EVInput{Side: "buy", GrossPrice: 100, SizeBase: 1, NotionalUSD: 100, FeeBps: 10})
	if buy.NetPriceUSD >= 0 {
		t.Fatalf("buy net price should be negative cash flow, got %v", buy.NetPriceUSD)
	}
	sell := ComputeEV(EVInput{Side: "sell", GrossPrice: 100, SizeBase: 1, NotionalUSD: 100, FeeBps: 10})
	if sell.NetPriceUSD <= 0 {
		t.Fatalf("sell net price should be positive cash flow, got %v", sell.NetPriceUSD)
	}
}

func TestEstimateSlippageBps(t *testing.T) {
	// k=10, consume half of top-of-book → 5 bps.
	if got := EstimateSlippageBps(5, 10, 10); !approxEq(got, 5, 1e-9) {
		t.Fatalf("want 5 bps, got %v", got)
	}
	// No liquidity info → 0.
	if got := EstimateSlippageBps(5, 0, 10); got != 0 {
		t.Fatalf("want 0 with no liquidity, got %v", got)
	}
	// Larger size → more slippage (monotonic).
	small := EstimateSlippageBps(1, 100, 10)
	big := EstimateSlippageBps(50, 100, 10)
	if big <= small {
		t.Fatalf("slippage should grow with size: small=%v big=%v", small, big)
	}
}
