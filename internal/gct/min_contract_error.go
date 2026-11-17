package gct

import (
	"fmt"
	"math"
)

// ErrMinContractTooLarge is returned when a single contract of the symbol costs
// more than Factor× the USD target of an opening order. Callers use errors.As to
// tell this sizing mismatch apart from a real exchange failure: the pair cannot
// be traded at this account size (or the multiplier lookup failed).
type ErrMinContractTooLarge struct {
	Symbol              string
	MinContractNotional float64 // USD cost of one contract
	TargetUSD           float64 // requested notional
	Factor              float64 // risk.max_notional_factor in force
	Multiplier          float64
	Price               float64
}

func (e *ErrMinContractTooLarge) Error() string {
	ratio := 0.0
	if e.TargetUSD > 0 {
		ratio = e.MinContractNotional / e.TargetUSD
	}
	return fmt.Sprintf(
		"notional safety check FAILED for %s: 1 contract = $%.2f (multiplier=%.6f, price=%.4f) which is %.1f× the $%.2f target (max %.1f×) — refusing to open oversized position (multiplier API may have defaulted to 1.0)",
		e.Symbol, e.MinContractNotional, e.Multiplier, e.Price, ratio, e.TargetUSD, e.Factor)
}

// MinSizeUSD is the smallest USD target that passes the guard for this symbol.
func (e *ErrMinContractTooLarge) MinSizeUSD() float64 {
	if e.Factor <= 0 {
		return e.MinContractNotional
	}
	return math.Ceil(e.MinContractNotional/e.Factor*100) / 100
}
