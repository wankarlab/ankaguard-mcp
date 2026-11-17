package gct

import "testing"

// TestSupportedGranularities verifies the single-source granularity ladders.
// Futures must NOT include 3m / 6h / 1w (KuCoin futures kline granularity does
// not support them); spot must.
func TestSupportedGranularities(t *testing.T) {
	futures := SupportedGranularities(true)
	spot := SupportedGranularities(false)

	// Futures must exclude the unsupported timeframes.
	for _, bad := range []string{"3m", "6h", "1w"} {
		if contains(futures, bad) {
			t.Errorf("futures granularities must not include %q: %v", bad, futures)
		}
	}
	// Futures must include the core scalping/trend timeframes.
	for _, want := range []string{"1m", "5m", "15m", "30m", "1h", "4h"} {
		if !contains(futures, want) {
			t.Errorf("futures granularities missing %q: %v", want, futures)
		}
	}
	// Spot must include the futures-unsupported ones.
	for _, want := range []string{"3m", "6h", "1w"} {
		if !contains(spot, want) {
			t.Errorf("spot granularities missing %q: %v", want, spot)
		}
	}
	// Ladders must be ascending and unique (the ladder code relies on order).
	if !ascendingUnique(futures) {
		t.Errorf("futures ladder not ascending/unique: %v", futures)
	}
	if !ascendingUnique(spot) {
		t.Errorf("spot ladder not ascending/unique: %v", spot)
	}
	// Returned slice must be a copy (mutating it must not corrupt the source).
	futures[0] = "MUTATED"
	if SupportedGranularities(true)[0] == "MUTATED" {
		t.Error("SupportedGranularities must return a copy, not the backing slice")
	}
}

func TestIsGranularitySupported(t *testing.T) {
	cases := []struct {
		tf        string
		isFutures bool
		want      bool
	}{
		{"5m", true, true},
		{"1m", true, true},
		{"3m", true, false},  // not in futures
		{"3m", false, true},  // in spot
		{"6h", true, false},  // not in futures
		{"6h", false, true},  // in spot
		{"99x", true, false}, // nonsense
	}
	for _, c := range cases {
		if got := IsGranularitySupported(c.tf, c.isFutures); got != c.want {
			t.Errorf("IsGranularitySupported(%q, futures=%v)=%v, want %v", c.tf, c.isFutures, got, c.want)
		}
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func ascendingUnique(s []string) bool {
	minutes := map[string]int{
		"1m": 1, "3m": 3, "5m": 5, "15m": 15, "30m": 30,
		"1h": 60, "2h": 120, "4h": 240, "6h": 360, "8h": 480,
		"12h": 720, "1d": 1440, "1w": 10080,
	}
	prev := -1
	for _, tf := range s {
		m, ok := minutes[tf]
		if !ok || m <= prev {
			return false
		}
		prev = m
	}
	return true
}

// TestDeriveConfirmationTF covers the auto-derived exit-confirmation ladder.
func TestDeriveConfirmationTF(t *testing.T) {
	cases := []struct {
		primary   string
		isFutures bool
		steps     int
		want      string
	}{
		{"5m", true, 1, "1m"},
		{"15m", true, 1, "5m"},
		{"30m", true, 1, "15m"},
		{"1h", true, 1, "30m"},
		{"4h", true, 1, "2h"},  // futures ladder includes the 2h rung
		{"1d", true, 1, "12h"}, // ...and 12h
		{"1m", true, 1, ""},    // already finest
		{"5m", false, 1, "3m"}, // spot has the 3m rung
		{"3m", true, 1, "1m"},  // 3m not on futures ladder → largest below
		{"1h", true, 2, "15m"}, // two rungs down
	}
	for _, c := range cases {
		if got := DeriveConfirmationTF(c.primary, c.isFutures, c.steps); got != c.want {
			t.Errorf("DeriveConfirmationTF(%q, futures=%v, steps=%d)=%q, want %q",
				c.primary, c.isFutures, c.steps, got, c.want)
		}
	}
}

// TestDeriveConfirmationTF_NeverEmits3mOnFutures is the critical safety check:
// futures must never receive 3m (the kline granularity converter rejects it).
func TestDeriveConfirmationTF_NeverEmits3mOnFutures(t *testing.T) {
	for _, primary := range SupportedGranularities(true) {
		for steps := 1; steps <= 3; steps++ {
			if got := DeriveConfirmationTF(primary, true, steps); got == "3m" {
				t.Errorf("DeriveConfirmationTF(%q, futures, %d) emitted unsupported 3m", primary, steps)
			}
		}
	}
}
