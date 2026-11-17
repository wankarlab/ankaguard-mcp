package exchange

import "testing"

var testNetworks = []NetworkCost{
	{Network: "ERC20", FeeUSD: 8},
	{Network: "TRC20", FeeUSD: 1},
	{Network: "BEP20", FeeUSD: 2},
}

func TestRebalanceBalanced(t *testing.T) {
	inv := NewInventory()
	inv.Set("a", 1000)
	inv.Set("b", 1010)
	// gap=10, total=2010 -> ~0.5% imbalance, under 10% threshold.
	if got := inv.Rebalance(0.10, testNetworks); got != nil {
		t.Fatalf("balanced inventory should yield nil, got %+v", got)
	}
}

func TestRebalanceProposesTransfer(t *testing.T) {
	inv := NewInventory()
	inv.Set("a", 1000)
	inv.Set("b", 200)
	// gap=800, total=1200 -> 66% imbalance > 10% threshold.
	props := inv.Rebalance(0.10, testNetworks)
	if len(props) != 1 {
		t.Fatalf("expected 1 proposal, got %+v", props)
	}
	p := props[0]
	if p.FromExchange != "a" || p.ToExchange != "b" {
		t.Fatalf("expected a->b, got %s->%s", p.FromExchange, p.ToExchange)
	}
	if p.AmountUSD != 400 { // gap/2
		t.Fatalf("expected amount 400, got %v", p.AmountUSD)
	}
	if p.Network != "TRC20" || p.FeeUSD != 1 {
		t.Fatalf("expected cheapest network TRC20 fee 1, got %s fee %v", p.Network, p.FeeUSD)
	}
}

func TestRebalanceFeeExceedsBenefit(t *testing.T) {
	inv := NewInventory()
	inv.Set("a", 100.5)
	inv.Set("b", 99.5)
	// gap=1, total=200 -> 0.5% imbalance. Use a tiny threshold so it trips,
	// but amount moved = gap/2 = 0.5 < cheapest fee (1) -> not worthwhile -> nil.
	if got := inv.Rebalance(0.001, testNetworks); got != nil {
		t.Fatalf("fee>benefit should yield nil, got %+v", got)
	}
}
