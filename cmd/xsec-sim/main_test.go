package main

import (
	"math"
	"testing"
)

func TestApplyPeriodReturn(t *testing.T) {
	cases := []struct {
		name    string
		equity  float64
		net     float64
		want    float64
		wantLiq bool
	}{
		{"loss beyond 100% is floored at zero", 30, -2.718, 0, true},
		{"exactly -100% liquidates", 30, -1, 0, true},
		{"half loss", 30, -0.5, 15, false},
		{"gain compounds as before", 30, 0.1, 33, false},
		{"flat period", 30, 0, 30, false},
	}
	for _, c := range cases {
		got, liq := applyPeriodReturn(c.equity, c.net)
		if math.Abs(got-c.want) > 1e-9 || liq != c.wantLiq {
			t.Errorf("%s: applyPeriodReturn(%v, %v) = (%v, %v), want (%v, %v)",
				c.name, c.equity, c.net, got, liq, c.want, c.wantLiq)
		}
		if got < 0 {
			t.Errorf("%s: equity went negative: %v", c.name, got)
		}
	}
}

func TestVerdictFor(t *testing.T) {
	strong := simStats{cagr: 0.40, sharpe: 1.2, maxDD: 0.20, totRet: 0.5}
	posWin := simStats{totRet: 0.1}
	liqWin := simStats{totRet: -1, maxDD: 1, liquidated: true}

	if got := verdictFor(strong, []simStats{posWin, posWin}); got != "PROMISING" {
		t.Errorf("healthy strong run: got %s, want PROMISING", got)
	}
	if got := verdictFor(strong, []simStats{posWin, liqWin}); got != "MARGINAL" {
		t.Errorf("strong run with liquidated window: got %s, want MARGINAL", got)
	}
	// A liquidated window is never PROMISING even if its return were reported positive.
	oddWin := simStats{totRet: 0.05, liquidated: true}
	if got := verdictFor(strong, []simStats{posWin, oddWin}); got == "PROMISING" {
		t.Errorf("liquidated window must exclude PROMISING")
	}
	fullLiq := simStats{cagr: -1, totRet: -1, maxDD: 1, liquidated: true}
	if got := verdictFor(fullLiq, []simStats{posWin}); got != "NEGATIVE" {
		t.Errorf("liquidated full run: got %s, want NEGATIVE", got)
	}
	weak := simStats{cagr: 0.067, sharpe: 0.41, maxDD: 0.23}
	if got := verdictFor(weak, []simStats{posWin}); got != "MARGINAL" {
		t.Errorf("weak positive run: got %s, want MARGINAL", got)
	}
	if got := verdictFor(simStats{cagr: -0.2}, nil); got != "NEGATIVE" {
		t.Errorf("negative run: got %s, want NEGATIVE", got)
	}
}
