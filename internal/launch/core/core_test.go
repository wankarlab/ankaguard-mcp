package core

import (
	"testing"
	"time"
)

func TestStrategyValidate(t *testing.T) {
	if err := DefaultStrategyV1().Validate(); err != nil {
		t.Fatalf("default strategy invalid: %v", err)
	}
	p := DefaultStrategyV1()
	p.TakeProfits = []TakeProfitLevel{{GainPct: 10, SellPct: 70}, {GainPct: 20, SellPct: 50}}
	if err := p.Validate(); err == nil {
		t.Fatal("expected partials > 100 % to fail")
	}
	p = DefaultStrategyV1()
	p.StopLossPct = 0
	if err := p.Validate(); err == nil {
		t.Fatal("expected missing SL to fail")
	}
}

func TestSnapshotMinExpiryAndStale(t *testing.T) {
	now := time.Now()
	s := Snapshot{
		FieldPriceUSD:     {Value: 1, ExpiresAt: now.Add(30 * time.Second)},
		FieldLiquidityUSD: {Value: 1, ExpiresAt: now.Add(10 * time.Second)},
	}
	if got := s.MinExpiry(CriticalFields...); !got.Equal(now.Add(10 * time.Second)) {
		t.Fatalf("min expiry = %v", got)
	}
	if !s[FieldLiquidityUSD].Stale(now.Add(11 * time.Second)) {
		t.Fatal("expected stale")
	}
	if (DataPoint{Value: 0}).Valid() {
		t.Fatal("zero price must be invalid")
	}
}
