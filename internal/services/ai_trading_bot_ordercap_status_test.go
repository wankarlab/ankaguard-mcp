package services

import "testing"

type fakeEnforcingCapSentinel struct {
	fakeCapSentinel
	enforce bool
}

func (f fakeEnforcingCapSentinel) EnforceCapOnMarketOrders() bool { return f.enforce }

// Status exposes the cap, whether it applies to market orders, and the
// cap-vs-bot incoherence with both values (spec order-notional-cap).
func TestSizingStatus_ExposesOrderCapState(t *testing.T) {
	bot := newSizingTestBot(fixedCapital(27.18, 25))
	v := defaultSizingValues()
	v["bot.sizing.mode"] = "absolute"
	v["dynamic_sizing.tiers"] = `[{"name":"max","min_confidence":0.9,"max_confidence":1,"size_usd":7}]`
	ctx := sizingCtx(v)

	bot.riskSentinel = fakeEnforcingCapSentinel{fakeCapSentinel{cap: 2}, false}
	r := bot.SizingStatus(ctx)
	if r.OrderCapUSD != 2 || r.CapAppliesToMarketOrders || !r.CapIncoherent || r.BotMaxNotionalUSD != 7 ||
		r.CapWarning == "" || r.CapNotAppliedWarning == "" {
		t.Fatalf("cap 2 / bot 7 / not enforced: %+v", r)
	}

	bot.riskSentinel = fakeEnforcingCapSentinel{fakeCapSentinel{cap: 10}, true}
	r = bot.SizingStatus(ctx)
	if !r.CapAppliesToMarketOrders || r.CapIncoherent || r.CapWarning != "" || r.CapNotAppliedWarning != "" {
		t.Fatalf("cap 10 / enforced must show no warning: %+v", r)
	}
}
