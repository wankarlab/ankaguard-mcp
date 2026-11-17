package risk

import "testing"

// TestEvaluateLiquidationRisk covers the four guard actions across long and
// short positions.  All cases use minBufferPct = 10.0 % for easy arithmetic.
//
//   Thresholds (minBufferPct = 10):
//     GuardOK             bufferPct >= 10
//     GuardReduceLeverage 5 <= bufferPct < 10
//     GuardConvertToSpot  2.5 <= bufferPct < 5
//     GuardClose          bufferPct < 2.5
func TestEvaluateLiquidationRisk(t *testing.T) {
	const min = 10.0 // minBufferPct

	tests := []struct {
		name             string
		entryPrice       float64
		markPrice        float64
		liquidationPrice float64
		side             string
		want             GuardAction
	}{
		// ── Long positions ──────────────────────────────────────────────────
		{
			name:             "long: safe buffer (15%) → OK",
			entryPrice:       100,
			markPrice:        100,
			liquidationPrice: 85, // 15% below mark
			side:             "long",
			want:             GuardOK,
		},
		{
			name:             "long: thin buffer (7%) → ReduceLeverage",
			entryPrice:       100,
			markPrice:        100,
			liquidationPrice: 93, // 7% below mark
			side:             "long",
			want:             GuardReduceLeverage,
		},
		{
			name:             "long: near liquidation (3%) → ConvertToSpot",
			entryPrice:       100,
			markPrice:        100,
			liquidationPrice: 97, // 3% below mark
			side:             "long",
			want:             GuardConvertToSpot,
		},
		{
			name:             "long: critically close (1%) → Close",
			entryPrice:       100,
			markPrice:        100,
			liquidationPrice: 99, // 1% below mark
			side:             "long",
			want:             GuardClose,
		},
		{
			name:             "long: liq >= mark (already liquidated) → Close",
			entryPrice:       100,
			markPrice:        100,
			liquidationPrice: 100,
			side:             "long",
			want:             GuardClose,
		},
		// ── Short positions ─────────────────────────────────────────────────
		{
			name:             "short: safe buffer (15%) → OK",
			entryPrice:       100,
			markPrice:        100,
			liquidationPrice: 115, // 15% above mark
			side:             "short",
			want:             GuardOK,
		},
		{
			name:             "short: thin buffer (7%) → ReduceLeverage",
			entryPrice:       100,
			markPrice:        100,
			liquidationPrice: 107, // 7% above mark
			side:             "short",
			want:             GuardReduceLeverage,
		},
		{
			name:             "short: near liquidation (3%) → ConvertToSpot",
			entryPrice:       100,
			markPrice:        100,
			liquidationPrice: 103, // 3% above mark
			side:             "short",
			want:             GuardConvertToSpot,
		},
		{
			name:             "short: critically close (1%) → Close",
			entryPrice:       100,
			markPrice:        100,
			liquidationPrice: 101, // 1% above mark
			side:             "short",
			want:             GuardClose,
		},
		{
			name:             "short: liq <= mark (already liquidated) → Close",
			entryPrice:       100,
			markPrice:        100,
			liquidationPrice: 100,
			side:             "short",
			want:             GuardClose,
		},
		// ── Edge cases ──────────────────────────────────────────────────────
		{
			name:             "invalid side → Close",
			entryPrice:       100,
			markPrice:        100,
			liquidationPrice: 85,
			side:             "unknown",
			want:             GuardClose,
		},
		{
			name:             "zero mark price → Close",
			entryPrice:       100,
			markPrice:        0,
			liquidationPrice: 85,
			side:             "long",
			want:             GuardClose,
		},
		{
			name:             "zero minBuffer → OK (no constraint)",
			entryPrice:       100,
			markPrice:        100,
			liquidationPrice: 99,
			side:             "long",
			want:             GuardOK, // minBufferPct=0 disables the guard
		},
		// Alternative side spellings
		{
			name:             "side=buy treated as long → OK",
			entryPrice:       100,
			markPrice:        100,
			liquidationPrice: 85,
			side:             "buy",
			want:             GuardOK,
		},
		{
			name:             "side=sell treated as short → OK",
			entryPrice:       100,
			markPrice:        100,
			liquidationPrice: 115,
			side:             "sell",
			want:             GuardOK,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			buf := min
			if tc.name == "zero minBuffer → OK (no constraint)" {
				buf = 0
			}
			got := EvaluateLiquidationRisk(tc.entryPrice, tc.markPrice, tc.liquidationPrice, buf, tc.side)
			if got != tc.want {
				t.Errorf("EvaluateLiquidationRisk() = %s (%d), want %s (%d)",
					got, got, tc.want, tc.want)
			}
		})
	}
}

// TestGuardActionString ensures the stringer covers all variants.
func TestGuardActionString(t *testing.T) {
	cases := map[GuardAction]string{
		GuardOK:             "OK",
		GuardReduceLeverage: "ReduceLeverage",
		GuardConvertToSpot:  "ConvertToSpot",
		GuardClose:          "Close",
		GuardAction(99):     "Unknown",
	}
	for action, expected := range cases {
		if got := action.String(); got != expected {
			t.Errorf("GuardAction(%d).String() = %q, want %q", action, got, expected)
		}
	}
}
