package handlers

import "testing"

// Funding sign convention is easy to get backwards; pin it. Positive rate =
// longs pay shorts, so a long's cashflow is negative (pays) and a short's is
// positive (receives). Negative rate flips both.
func TestFundingCashflow_SignConvention(t *testing.T) {
	cases := []struct {
		name     string
		dir      int
		notional float64
		rate     float64
		want     float64
	}{
		{"long pays on positive rate", 1, 100, 0.0001, -0.01},
		{"short receives on positive rate", -1, 100, 0.0001, 0.01},
		{"long receives on negative rate", 1, 100, -0.0001, 0.01},
		{"short pays on negative rate", -1, 100, -0.0001, -0.01},
		{"zero rate is flat", 1, 100, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fundingCashflow(tc.dir, tc.notional, tc.rate)
			if diff := got - tc.want; diff > 1e-9 || diff < -1e-9 {
				t.Fatalf("fundingCashflow(%d, %g, %g) = %g, want %g", tc.dir, tc.notional, tc.rate, got, tc.want)
			}
		})
	}
}

// On a dollar-neutral book (equal long/short notional) with the SAME rate per
// pair, per-leg funding nets to zero — the market-neutral funding ideal. Net is
// only non-zero when rates differ across legs (the real-world case the card
// surfaces).
func TestFundingCashflow_NeutralBookSameRateNetsZero(t *testing.T) {
	rate := 0.0002
	long := fundingCashflow(1, 50, rate)
	short := fundingCashflow(-1, 50, rate)
	if net := long + short; net > 1e-9 || net < -1e-9 {
		t.Fatalf("neutral book same-rate net = %g, want 0", net)
	}
}
