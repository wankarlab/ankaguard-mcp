package risk

import (
	"strings"
	"testing"

	"github.com/rantipay/trading-mcp/internal/config/dynamic"
)

// Scenarios of openspec/changes/guarded-mcp-product/specs/order-notional-cap.

func TestCheckOrderCap(t *testing.T) {
	open := func(n float64) *TradingAction { return &TradingAction{Type: ActionOpenPosition, NotionalUSD: n} }
	unknown := &TradingAction{Type: ActionOpenPosition, NotionalUnknown: true, NotionalReason: "no recent reference price for X"}
	limit := func(n float64) *TradingAction {
		return &TradingAction{Type: ActionOpenPosition, NotionalUSD: n, OwnPrice: true, Price: 100}
	}
	closeBig := &TradingAction{Type: ActionClosePosition, NotionalUSD: 500}
	closeUnknown := &TradingAction{Type: ActionClosePosition, NotionalUnknown: true}

	cases := []struct {
		name     string
		action   *TradingAction
		cap      float64
		enforce  bool
		wantCode string // "" = allowed
	}{
		{"market above cap, enforced", open(6.58), 2, true, CodeOrderCapExceeded},
		{"market within cap, enforced", open(1.5), 2, true, ""},
		{"no cap configured", open(60), 0, true, ""},
		{"market without price, enforcement off: not evaluated", open(6.58), 2, false, ""},
		{"limit with own price above cap, enforcement off: still evaluated", limit(6.58), 2, false, CodeOrderCapExceeded},
		{"limit with own price within cap", limit(1.9), 2, false, ""},
		{"unknown notional, enforced: fail closed", unknown, 2, true, CodeOrderNotionalUnknown},
		{"unknown notional, enforcement off: unchanged", unknown, 2, false, ""},
		{"unknown notional, no cap", unknown, 0, true, ""},
		{"big close, enforced", closeBig, 2, true, ""},
		{"close without reference price, enforced", closeUnknown, 2, true, ""},
		{"big close, enforcement off", closeBig, 2, false, ""},
	}
	for _, c := range cases {
		got := checkOrderCap(c.action, c.cap, c.enforce)
		switch {
		case c.wantCode == "" && got != nil:
			t.Errorf("%s: rejected with %s, want allowed", c.name, got.code)
		case c.wantCode != "" && got == nil:
			t.Errorf("%s: allowed, want %s", c.name, c.wantCode)
		case got != nil && got.code != c.wantCode:
			t.Errorf("%s: code %s, want %s", c.name, got.code, c.wantCode)
		}
	}
}

func TestCheckOrderCap_MessageCarriesNotionalAndCap(t *testing.T) {
	got := checkOrderCap(&TradingAction{Type: ActionOpenPosition, NotionalUSD: 6.58}, 2, true)
	if got == nil || !strings.Contains(got.reason, "6.58") || !strings.Contains(got.reason, "2.00") ||
		!strings.Contains(got.reason, "ORDER_CAP_EXCEEDED") {
		t.Fatalf("reason must carry code, notional and cap, got %+v", got)
	}
}

// Callers that predate NotionalUSD (Size*Price, no notional fields) keep the old behaviour.
func TestCheckOrderCap_LegacyCallers(t *testing.T) {
	if got := checkOrderCap(&TradingAction{Type: ActionOpenPosition, Size: 1, Price: 3}, 2, false); got == nil {
		t.Error("legacy priced order above the cap must still be rejected")
	}
	if got := checkOrderCap(&TradingAction{Type: ActionOpenPosition, Size: 1}, 2, false); got != nil {
		t.Error("legacy priceless order with enforcement off must not be evaluated")
	}
	if got := checkOrderCap(&TradingAction{Type: ActionOpenPosition, Size: 1}, 2, true); got == nil || got.code != CodeOrderNotionalUnknown {
		t.Error("legacy priceless order with enforcement on must fail closed")
	}
}

// Effective cap scenarios: relative lower than absolute, and no cap.
func TestEffectiveCap_Scenarios(t *testing.T) {
	snap := dynamic.NewStaticSnapshot(map[string]string{"risk.max_order_size_usd": "10", "risk.max_order_size_pct": "5"})
	if got := effectiveOrderCap(snap, 100); got != 5 {
		t.Errorf("relative lower than absolute: cap %.2f, want 5", got)
	}
	snap = dynamic.NewStaticSnapshot(map[string]string{"risk.max_order_size_usd": "0", "risk.max_order_size_pct": "0"})
	if got := effectiveOrderCap(snap, 100); got != 0 {
		t.Errorf("both zero: cap %.2f, want 0", got)
	}
}

func TestSentinelEvaluateAction_ReturnsCode(t *testing.T) {
	s := &sentinel{
		config: &RiskConfig{},
		state:  &RiskState{ExposureBySymbol: map[string]float64{}},
	}
	// Without a config store there is no cap: nothing is rejected by it.
	d, err := s.EvaluateAction(t.Context(), &TradingAction{Type: ActionOpenPosition, NotionalUnknown: true})
	if err != nil {
		t.Fatal(err)
	}
	if d.Code != "" {
		t.Errorf("no cap configured but code %q set", d.Code)
	}
	if s.ReferencePriceMaxAge() != DefaultReferencePriceMaxAge {
		t.Errorf("default max age = %s", s.ReferencePriceMaxAge())
	}
	if s.EnforceCapOnMarketOrders() {
		t.Error("enforcement must be off without a config store")
	}
}
