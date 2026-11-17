package services

import "testing"

// TestParseAllowlist verifies the ranging allowlist parses to a normalised set so
// membership is robust to formatting (spaces, blanks) and to spot/futures symbol
// variants — SOL-USDT and SOLUSDTM must be treated as the same pair.
func TestParseAllowlist(t *testing.T) {
	set := parseAllowlist("SOL-USDT, TAO-USDT ,,XRP-USDT")
	if len(set) != 3 {
		t.Fatalf("expected 3 entries, got %d (%v)", len(set), set)
	}
	inList := func(pair string) bool {
		_, ok := set[normalizeSymbolForMatch(pair)]
		return ok
	}
	for _, p := range []string{"SOL-USDT", "SOLUSDTM", "TAO-USDT", "XRP-USDT"} {
		if !inList(p) {
			t.Errorf("%s should be in the allowlist (SOL-USDT==SOLUSDTM)", p)
		}
	}
	for _, p := range []string{"DOGE-USDT", "ETH-USDT"} {
		if inList(p) {
			t.Errorf("%s must NOT be in the allowlist", p)
		}
	}
}

// TestParseAllowlist_Empty ensures an empty/whitespace string yields an empty set
// (callers treat that as "no restriction").
func TestParseAllowlist_Empty(t *testing.T) {
	if got := parseAllowlist("   "); len(got) != 0 {
		t.Errorf("expected empty set, got %v", got)
	}
}

// TestRangingAllowSet_NilSnapshot guards the backward-compatible default: with no
// snapshot there is no restriction, so the orchestrator behaves exactly as before.
func TestRangingAllowSet_NilSnapshot(t *testing.T) {
	if got := rangingAllowSet(nil); got != nil {
		t.Errorf("nil snapshot must yield nil (no restriction), got %v", got)
	}
}
