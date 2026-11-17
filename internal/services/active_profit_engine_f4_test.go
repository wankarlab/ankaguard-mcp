package services

import (
	"testing"

	"github.com/rantipay/trading-mcp/internal/ai/regime"
	"github.com/rantipay/trading-mcp/internal/volatility"
)

func TestRegimeProfileBucket(t *testing.T) {
	cases := map[string]string{
		string(regime.RegimeTrendingUp):   "trending",
		string(regime.RegimeTrendingDown): "trending",
		string(regime.RegimeBreakout):     "breakout",
		string(regime.RegimeRanging):      "ranging",
		string(regime.RegimeUnknown):      "ranging",
		string(regime.RegimeChaos):        "ranging", // conservative
		"":                                "ranging",
	}
	for in, want := range cases {
		if got := regimeProfileBucket(in); got != want {
			t.Errorf("regimeProfileBucket(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestVoltierMult(t *testing.T) {
	low, normal, vol, extreme := 0.8, 1.0, 1.4, 1.8
	cases := []struct {
		tier volatility.Tier
		want float64
	}{
		{volatility.TierLow, 0.8},
		{volatility.TierNormal, 1.0},
		{volatility.TierVolatile, 1.4},
		{volatility.TierExtreme, 1.8},
		{volatility.Tier("garbage"), 1.0}, // unknown → normal
	}
	for _, c := range cases {
		if got := voltierMult(c.tier, low, normal, vol, extreme); got != c.want {
			t.Errorf("voltierMult(%q)=%.2f, want %.2f", c.tier, got, c.want)
		}
	}
	// Higher volatility must widen (monotonic), so spikes don't wick us out.
	if !(voltierMult(volatility.TierLow, low, normal, vol, extreme) <
		voltierMult(volatility.TierExtreme, low, normal, vol, extreme)) {
		t.Error("extreme tier must widen more than low tier")
	}
}
