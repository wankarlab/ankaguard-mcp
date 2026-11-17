package gct

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/rantipay/trading-mcp/internal/risk"
)

func newNotionalClient(price float64, priceAt time.Time, multiplier float64, calls *int) *client {
	c := &client{}
	c.notional.src = &notionalSources{
		ticker: func(ctx context.Context, symbol string, futures bool) (float64, time.Time, error) {
			if calls != nil {
				*calls++
			}
			if price <= 0 {
				return 0, time.Time{}, errors.New("no ticker")
			}
			return price, priceAt, nil
		},
		multiplier: func(ctx context.Context, symbol string) (float64, error) {
			if multiplier <= 0 {
				return 0, errors.New("unknown contract")
			}
			return multiplier, nil
		},
	}
	return c
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

const maxAge = 10 * time.Second

func TestNotional_SpotBaseUnits(t *testing.T) {
	c := newNotionalClient(60000, time.Now(), 0, nil)
	n := c.computeOrderNotional(t.Context(), &SubmitOrderRequest{Pair: "BTC-USDT", Side: "buy", Type: "market", Amount: 0.001}, false, maxAge)
	if n.Unknown || !near(n.USD, 60) {
		t.Fatalf("spot 0.001 @ 60000 = %+v, want 60", n)
	}
}

func TestNotional_FuturesContracts(t *testing.T) {
	// $3 target, contract = 0.01 units at $100 = $1 -> 3 contracts -> $3.
	c := newNotionalClient(100, time.Now(), 0.01, nil)
	n := c.computeOrderNotional(t.Context(), &SubmitOrderRequest{Pair: "XYZ-USDT", Side: "buy", Type: "market", Amount: 3, Leverage: 1}, true, maxAge)
	if n.Unknown || !near(n.USD, 3) {
		t.Fatalf("3 contracts of 0.01 @ 100 = %+v, want 3", n)
	}
}

// One contract worth more than the USD target: the real notional is the contract
// (the 6.58 vs 2.00 case), not the target.
func TestNotional_FuturesMinimumContractExceedsTarget(t *testing.T) {
	c := newNotionalClient(6.58, time.Now(), 1, nil)
	n := c.computeOrderNotional(t.Context(), &SubmitOrderRequest{Pair: "NEAR-USDT", Side: "buy", Type: "market", Amount: 2, Leverage: 1}, true, maxAge)
	if n.Unknown || !near(n.USD, 6.58) {
		t.Fatalf("notional = %+v, want 6.58", n)
	}
}

func TestNotional_LimitUsesOwnPriceWithoutLookup(t *testing.T) {
	calls := 0
	c := newNotionalClient(999, time.Now(), 1, &calls)
	n := c.computeOrderNotional(t.Context(), &SubmitOrderRequest{Pair: "NEAR-USDT", Side: "buy", Type: "limit", Amount: 5, Price: 5, Leverage: 1}, true, maxAge)
	if n.Unknown || !near(n.USD, 5) || calls != 0 {
		t.Fatalf("limit order = %+v, ticker calls %d; want 5 with no ticker call", n, calls)
	}
}

func TestNotional_NoReferencePriceIsUnknown(t *testing.T) {
	c := newNotionalClient(0, time.Time{}, 1, nil)
	n := c.computeOrderNotional(t.Context(), &SubmitOrderRequest{Pair: "NEAR-USDT", Side: "buy", Type: "market", Amount: 2}, true, maxAge)
	if !n.Unknown {
		t.Fatalf("no price must be unknown, got %+v", n)
	}
}

func TestNotional_StaleReferencePriceIsUnknown(t *testing.T) {
	c := newNotionalClient(5, time.Now().Add(-time.Minute), 1, nil)
	n := c.computeOrderNotional(t.Context(), &SubmitOrderRequest{Pair: "NEAR-USDT", Side: "buy", Type: "market", Amount: 2}, true, maxAge)
	if !n.Unknown {
		t.Fatalf("1-minute-old price with 10s max age must be unknown, got %+v", n)
	}
}

func TestNotional_UnknownContractSpecIsUnknown(t *testing.T) {
	c := newNotionalClient(5, time.Now(), 0, nil)
	n := c.computeOrderNotional(t.Context(), &SubmitOrderRequest{Pair: "NEAR-USDT", Side: "buy", Type: "market", Amount: 2}, true, maxAge)
	if !n.Unknown {
		t.Fatalf("missing contract spec must be unknown, got %+v", n)
	}
}

func TestNotional_ReferencePriceCachedWithinMaxAge(t *testing.T) {
	calls := 0
	c := newNotionalClient(5, time.Now(), 1, &calls)
	req := &SubmitOrderRequest{Pair: "NEAR-USDT", Side: "buy", Type: "market", Amount: 2}
	c.computeOrderNotional(t.Context(), req, true, maxAge)
	c.computeOrderNotional(t.Context(), req, true, maxAge)
	if calls != 1 {
		t.Fatalf("ticker calls = %d, want 1 (cache within max age)", calls)
	}
}

// ── evaluateOrderRisk wiring ────────────────────────────────────────────────

type recordingSentinel struct {
	risk.RiskSentinel
	got *risk.TradingAction
}

func (r *recordingSentinel) EvaluateAction(_ context.Context, a *risk.TradingAction) (*risk.RiskDecision, error) {
	r.got = a
	return &risk.RiskDecision{Allowed: true}, nil
}

func TestEvaluateOrderRisk_FillsNotionalForOpens(t *testing.T) {
	c := newNotionalClient(6.58, time.Now(), 1, nil)
	rs := &recordingSentinel{}
	c.SetRiskSentinel(rs)
	err := c.evaluateOrderRisk(t.Context(), &SubmitOrderRequest{Pair: "NEAR-USDT", Side: "buy", Type: "market", Amount: 2, Leverage: 1}, true)
	if err != nil {
		t.Fatal(err)
	}
	if rs.got.Type != risk.ActionOpenPosition || rs.got.OwnPrice || rs.got.NotionalUnknown || !near(rs.got.NotionalUSD, 6.58) {
		t.Fatalf("action = %+v", rs.got)
	}
}

func TestEvaluateOrderRisk_CloseOrdersSkipLookups(t *testing.T) {
	calls := 0
	c := newNotionalClient(0, time.Time{}, 0, &calls) // would be unknown if looked up
	rs := &recordingSentinel{}
	c.SetRiskSentinel(rs)
	for _, req := range []*SubmitOrderRequest{
		{Pair: "NEAR-USDT", Side: "sell", Type: "market", Amount: 500, ReduceOnly: true},
		{Pair: "NEAR-USDT", Side: "sell", Type: "market", Amount: 500, CloseOrder: true},
	} {
		if err := c.evaluateOrderRisk(t.Context(), req, true); err != nil {
			t.Fatal(err)
		}
		if rs.got.Type != risk.ActionClosePosition || calls != 0 {
			t.Fatalf("close order: action %+v, ticker calls %d", rs.got, calls)
		}
	}
}

func TestEvaluateOrderRisk_LimitCarriesOwnPrice(t *testing.T) {
	c := newNotionalClient(0, time.Time{}, 1, nil)
	rs := &recordingSentinel{}
	c.SetRiskSentinel(rs)
	if err := c.evaluateOrderRisk(t.Context(), &SubmitOrderRequest{Pair: "NEAR-USDT", Side: "buy", Type: "limit", Amount: 2, Price: 5}, true); err != nil {
		t.Fatal(err)
	}
	if !rs.got.OwnPrice || rs.got.NotionalUnknown {
		t.Fatalf("limit action = %+v", rs.got)
	}
}
