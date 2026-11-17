package exchange

import "testing"

func TestMedianLast(t *testing.T) {
	// even count (2): mean of the two
	two := map[string]PriceQuote{
		"a": {Exchange: "a", Last: 100},
		"b": {Exchange: "b", Last: 110},
	}
	if got := medianLast(two); got != 105 {
		t.Fatalf("medianLast(2) = %v, want 105", got)
	}

	// odd count (3): middle value
	three := map[string]PriceQuote{
		"a": {Exchange: "a", Last: 100},
		"b": {Exchange: "b", Last: 200},
		"c": {Exchange: "c", Last: 150},
	}
	if got := medianLast(three); got != 150 {
		t.Fatalf("medianLast(3) = %v, want 150", got)
	}

	// 0 entries -> 0
	if got := medianLast(map[string]PriceQuote{}); got != 0 {
		t.Fatalf("medianLast(0) = %v, want 0", got)
	}

	// 1 entry -> that value
	one := map[string]PriceQuote{"a": {Exchange: "a", Last: 42}}
	if got := medianLast(one); got != 42 {
		t.Fatalf("medianLast(1) = %v, want 42", got)
	}
}

func TestEvaluateFlagsDivergentExchange(t *testing.T) {
	cb := NewCircuitBreaker(3) // 3% threshold
	// median of {100,100,105} = 100; "c" is 5% off -> suspect.
	matrix := map[string]PriceQuote{
		"a": {Exchange: "a", Last: 100},
		"b": {Exchange: "b", Last: 100},
		"c": {Exchange: "c", Last: 105},
	}
	res := cb.Evaluate(matrix)
	if res["a"] || res["b"] {
		t.Fatalf("a/b should not be suspect: %+v", res)
	}
	if !res["c"] {
		t.Fatalf("c (5%% off median, threshold 3%%) should be suspect: %+v", res)
	}
	if !cb.IsSuspect("c") {
		t.Fatalf("IsSuspect(c) should be true after Evaluate")
	}
	if cb.IsSuspect("a") {
		t.Fatalf("IsSuspect(a) should be false")
	}
}

func TestEvaluateBalancedFlagsNone(t *testing.T) {
	cb := NewCircuitBreaker(3)
	matrix := map[string]PriceQuote{
		"a": {Exchange: "a", Last: 100},
		"b": {Exchange: "b", Last: 101},
		"c": {Exchange: "c", Last: 99},
	}
	res := cb.Evaluate(matrix)
	for ex, suspect := range res {
		if suspect {
			t.Fatalf("balanced matrix flagged %s as suspect: %+v", ex, res)
		}
	}
}

func TestReset(t *testing.T) {
	cb := NewCircuitBreaker(3)
	cb.Evaluate(map[string]PriceQuote{
		"a": {Exchange: "a", Last: 100},
		"b": {Exchange: "b", Last: 100},
		"c": {Exchange: "c", Last: 105},
	})
	if !cb.IsSuspect("c") {
		t.Fatalf("precondition: c should be suspect")
	}
	cb.Reset("c")
	if cb.IsSuspect("c") {
		t.Fatalf("IsSuspect(c) should be false after Reset")
	}
}
