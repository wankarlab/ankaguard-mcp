package exchange

import (
	"math"
	"sort"
	"sync"
)

// CircuitBreaker (DM-8.3) detects cross-exchange price divergence. It treats the
// MEDIAN of the last-traded prices across venues as "truth": any exchange whose
// last price diverges from the median by more than maxDivergencePct is marked
// suspect, and operations depending on it should pause until the divergence
// clears (or it is manually Reset).
//
// NOTE: the median is a weak signal with fewer than 3 exchanges. With 2 venues
// the "median" is their arithmetic mean, so a single bad feed pulls the median
// toward itself and divergence detection becomes unreliable. Callers should
// treat suspect flags with <3 venues as low-confidence.
type CircuitBreaker struct {
	maxDivergencePct float64
	tripped          map[string]bool
	mu               sync.Mutex
}

// NewCircuitBreaker builds a CircuitBreaker with the given divergence threshold
// (in percent of the median).
func NewCircuitBreaker(maxDivergencePct float64) *CircuitBreaker {
	return &CircuitBreaker{
		maxDivergencePct: maxDivergencePct,
		tripped:          make(map[string]bool),
	}
}

// Evaluate computes the median Last across the matrix and returns a map of
// exchange -> suspect. An exchange is suspect if |last-median|/median*100
// exceeds maxDivergencePct. The internal tripped state is replaced with the
// result so IsSuspect reflects the latest evaluation. A median of 0 (no usable
// data) yields no suspects.
func (cb *CircuitBreaker) Evaluate(matrix map[string]PriceQuote) map[string]bool {
	result := make(map[string]bool, len(matrix))
	median := medianLast(matrix)

	for ex, q := range matrix {
		suspect := false
		if median > 0 && q.Last > 0 {
			divergence := math.Abs(q.Last-median) / median * 100
			if divergence > cb.maxDivergencePct {
				suspect = true
			}
		}
		result[ex] = suspect
	}

	cb.mu.Lock()
	cb.tripped = make(map[string]bool, len(result))
	for ex, suspect := range result {
		cb.tripped[ex] = suspect
	}
	cb.mu.Unlock()

	return result
}

// IsSuspect reports whether the exchange was flagged suspect by the most recent
// Evaluate (and not subsequently Reset).
func (cb *CircuitBreaker) IsSuspect(exchange string) bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.tripped[exchange]
}

// Reset clears the suspect flag for a single exchange.
func (cb *CircuitBreaker) Reset(exchange string) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	delete(cb.tripped, exchange)
}

// medianLast returns the median of the Last prices across the matrix. It is a
// pure helper with no shared state.
//
//   - 0 entries  -> 0
//   - 1 entry    -> that entry's Last
//   - odd count  -> the middle value
//   - even count -> the arithmetic mean of the two middle values
//
// With fewer than 3 entries the median is a weak measure of "truth": a single
// outlier dominates it (see CircuitBreaker docs).
func medianLast(matrix map[string]PriceQuote) float64 {
	n := len(matrix)
	if n == 0 {
		return 0
	}
	vals := make([]float64, 0, n)
	for _, q := range matrix {
		vals = append(vals, q.Last)
	}
	sort.Float64s(vals)

	mid := n / 2
	if n%2 == 1 {
		return vals[mid]
	}
	return (vals[mid-1] + vals[mid]) / 2
}
