package manifest

import "testing"

func TestPaperEntries_RegisteredWithDefaults(t *testing.T) {
	all := map[string]Entry{}
	for _, e := range All() {
		all[e.Key] = e
	}
	want := map[string]string{
		"paper.initial_balance_usd": "1000.0",
		"paper.slippage_pct":        "0.05",
		"paper.taker_fee_pct":       "0.06",
	}
	for k, def := range want {
		e, ok := all[k]
		if !ok {
			t.Errorf("%s: not registered", k)
			continue
		}
		if e.Group != "paper" || e.Default != def {
			t.Errorf("%s: group=%q default=%q, want paper/%s", k, e.Group, e.Default, def)
		}
		if v := mustFloat(t, k, e.Default); v < mustFloat(t, k, e.Min) || v > mustFloat(t, k, e.Max) {
			t.Errorf("%s: default %s outside [%s,%s]", k, e.Default, e.Min, e.Max)
		}
	}
}
