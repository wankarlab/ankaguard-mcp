package gct

import (
	"errors"
	"testing"
	"time"

	"github.com/rantipay/trading-mcp/internal/risk"
)

// A3: the spot route does not send reduceOnly/closeOrder to the exchange, so a
// spot "reduce_only" buy is an ordinary buy and must be evaluated (and capped)
// as an opening order. Futures keep treating the flags as a close.
func TestEvaluateOrderRisk_SpotReduceOnlyBuyIsAnOpen(t *testing.T) {
	c := newNotionalClient(60000, time.Now(), 1, nil)
	rs := &recordingSentinel{}
	c.SetRiskSentinel(rs)
	for _, req := range []*SubmitOrderRequest{
		{Pair: "BTC-USDT", Side: "buy", Type: "market", Amount: 1, ReduceOnly: true},
		{Pair: "BTC-USDT", Side: "buy", Type: "market", Amount: 1, CloseOrder: true},
		// A futures-looking pair sent down the spot route: a sell could open a short.
		{Pair: "BTC-USDTM", Side: "sell", Type: "market", Amount: 1, ReduceOnly: true},
	} {
		if err := c.evaluateOrderRisk(t.Context(), req, false); err != nil {
			t.Fatal(err)
		}
		if rs.got.Type != risk.ActionOpenPosition {
			t.Fatalf("%s %s on spot route evaluated as %v, want open", req.Side, req.Pair, rs.got.Type)
		}
		// (the futures-looking pair has no defined unit on the spot route: its
		// notional is unknown, which the sentinel rejects when a cap is set.)
		if req.Pair == "BTC-USDT" && (rs.got.NotionalUnknown || rs.got.NotionalUSD <= 0) {
			t.Fatalf("spot reduce-only buy skipped the notional: %+v", rs.got)
		}
	}
	// A spot sell of a spot pair can only dispose of holdings: still a close.
	if err := c.evaluateOrderRisk(t.Context(), &SubmitOrderRequest{Pair: "BTC-USDT", Side: "sell", Type: "market", Amount: 1, ReduceOnly: true}, false); err != nil {
		t.Fatal(err)
	}
	if rs.got.Type != risk.ActionClosePosition {
		t.Fatalf("spot reduce-only sell evaluated as %v, want close", rs.got.Type)
	}
	// Futures route: unchanged.
	if err := c.evaluateOrderRisk(t.Context(), &SubmitOrderRequest{Pair: "BTC-USDTM", Side: "buy", Type: "market", Amount: 1, ReduceOnly: true}, true); err != nil {
		t.Fatal(err)
	}
	if rs.got.Type != risk.ActionClosePosition {
		t.Fatalf("futures reduce-only buy evaluated as %v, want close", rs.got.Type)
	}
}

// A2: the live order gate (product edition) blocks every order that adds
// exposure, on both routes, even without a sentinel; reductions and closes of
// futures positions always pass so exposure can be unwound.
func TestEvaluateOrderRisk_LiveGate(t *testing.T) {
	gateErr := errors.New("LICENSE_REQUIRED: test")
	c := newNotionalClient(100, time.Now(), 1, nil)
	calls := 0
	c.SetLiveOrderGate(func() error { calls++; return gateErr })

	opens := []struct {
		req     *SubmitOrderRequest
		futures bool
	}{
		{&SubmitOrderRequest{Pair: "BTC-USDTM", Side: "buy", Type: "market", Amount: 5}, true},
		{&SubmitOrderRequest{Pair: "BTC-USDT", Side: "buy", Type: "limit", Amount: 1, Price: 1}, false},
		{&SubmitOrderRequest{Pair: "BTC-USDT", Side: "buy", Type: "market", Amount: 1, ReduceOnly: true}, false},
	}
	for _, o := range opens {
		if err := c.evaluateOrderRisk(t.Context(), o.req, o.futures); !errors.Is(err, gateErr) {
			t.Fatalf("open %+v not gated: %v", o.req, err)
		}
	}
	closes := []*SubmitOrderRequest{
		{Pair: "BTC-USDTM", Side: "sell", Type: "market", Amount: 5, ReduceOnly: true},
		{Pair: "BTC-USDTM", Side: "buy", Type: "market", Amount: 5, CloseOrder: true},
	}
	before := calls
	for _, r := range closes {
		if err := c.evaluateOrderRisk(t.Context(), r, true); err != nil {
			t.Fatalf("close %+v blocked: %v", r, err)
		}
	}
	if calls != before {
		t.Fatal("the gate was consulted for a close")
	}

	// With the gate open the order reaches the sentinel as usual.
	rs := &recordingSentinel{}
	c.SetRiskSentinel(rs)
	c.SetLiveOrderGate(func() error { return nil })
	if err := c.evaluateOrderRisk(t.Context(), opens[0].req, true); err != nil || rs.got == nil {
		t.Fatalf("open with gate open: %v %+v", err, rs.got)
	}
}

// Without a gate (standard build) nothing changes.
func TestEvaluateOrderRisk_NoGateNoSentinelIsNoop(t *testing.T) {
	c := &client{}
	if err := c.evaluateOrderRisk(t.Context(), &SubmitOrderRequest{Pair: "BTC-USDTM", Side: "buy", Amount: 1}, true); err != nil {
		t.Fatal(err)
	}
}
