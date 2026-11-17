package exchange

import (
	"context"
	"fmt"
	"sort"
)

// Allocation is a single child order routed to one venue.
type Allocation struct {
	Exchange string  `json:"exchange"`
	Symbol   string  `json:"symbol"`
	SizeBase float64 `json:"size_base"`
	EstPrice float64 `json:"est_price"` // effective (fee+slippage adjusted) per-unit price
}

// SplitOrder partitions `size` across venues to minimize total cost
// (slippage + fees). It is deliberately CONSERVATIVE:
//
//   - It first ranks venues by effective price for the side (best first) using
//     the same fee+slippage model as BestVenue.
//   - It greedily fills from the best venue, then evaluates whether spilling the
//     remainder onto the next-best venue actually lowers the total estimated
//     cost. Because slippage rises with the fraction of liquidity consumed, a
//     large order on a thin top-of-book can be cheaper split across venues.
//   - SPLIT RULE: a split onto an additional venue is only kept if the estimated
//     saving (reduced slippage on the primary venue) strictly EXCEEDS the extra
//     cost of executing on the second venue. Otherwise the whole size stays on
//     the single best venue (== BestVenue). When in doubt, do NOT split.
//
// The sum of all returned Allocation.SizeBase always equals `size`.
func (r *Router) SplitOrder(ctx context.Context, pair, side string, size float64) ([]Allocation, error) {
	if size <= 0 {
		return nil, fmt.Errorf("split order: size must be > 0, got %v", size)
	}
	if side != "buy" && side != "sell" {
		return nil, fmt.Errorf("split order: invalid side %q (want buy|sell)", side)
	}

	matrix, err := r.PriceMatrix(ctx, pair)
	if err != nil {
		return nil, err
	}

	// Rank venues by effective price for the WHOLE size on each venue.
	cands := r.effectiveForSide(matrix, side, size)
	if len(cands) == 0 {
		return nil, fmt.Errorf("split order: no usable %s price for %s", side, pair)
	}
	sortByBest(cands, side)

	best := cands[0]
	single := []Allocation{{
		Exchange: best.quote.Exchange,
		Symbol:   best.quote.Symbol,
		SizeBase: size,
		EstPrice: best.effPrice,
	}}

	// Only one venue available → single allocation.
	if len(cands) < 2 {
		return single, nil
	}
	second := cands[1]

	// Cost of putting the full size on the best venue.
	fullCost := costOnVenue(r, best, side, size)

	// Try a 50/50 split between the two best venues as a representative
	// alternative. Slippage is convex in size, so halving the size on each
	// venue more than halves each venue's slippage component.
	half := size / 2
	splitCost := costOnVenue(r, best, side, half) + costOnVenue(r, second, side, half)

	// Conservative gate: only split if it strictly beats the single venue.
	// (splitCost is total *cost*; lower is always better for both sides because
	// costOnVenue returns a positive friction+price deviation magnitude.)
	if splitCost >= fullCost {
		return single, nil
	}

	a := []Allocation{
		{
			Exchange: best.quote.Exchange,
			Symbol:   best.quote.Symbol,
			SizeBase: half,
			EstPrice: effPriceOnVenue(r, best, side, half),
		},
		{
			Exchange: second.quote.Exchange,
			Symbol:   second.quote.Symbol,
			SizeBase: size - half, // guarantees exact sum == size
			EstPrice: effPriceOnVenue(r, second, side, size-half),
		},
	}
	return a, nil
}

// sortByBest orders candidates best-first: lowest effective price for buy,
// highest effective price for sell.
func sortByBest(c []venueEffective, side string) {
	sort.SliceStable(c, func(i, j int) bool {
		if side == "buy" {
			return c[i].effPrice < c[j].effPrice
		}
		return c[i].effPrice > c[j].effPrice
	})
}

// effPriceOnVenue recomputes the effective per-unit price for a specific size
// on a venue (slippage depends on size).
func effPriceOnVenue(r *Router, v venueEffective, side string, size float64) float64 {
	feeBps := v.feeBps
	slipBps := EstimateSlippageBps(size, v.quote.Volume, slippageImpactK)
	costFrac := (feeBps + slipBps) * 0.0001
	if side == "buy" {
		return v.rawPrice * (1 + costFrac)
	}
	return v.rawPrice * (1 - costFrac)
}

// costOnVenue returns a positive scalar cost (USD-ish) of executing `size` on a
// venue: the absolute deviation of the effective notional from the raw notional.
// Lower is better for both sides. This captures fees + slippage scaled by size.
func costOnVenue(r *Router, v venueEffective, side string, size float64) float64 {
	eff := effPriceOnVenue(r, v, side, size)
	rawNotional := v.rawPrice * size
	effNotional := eff * size
	// buy: effNotional > rawNotional (we pay more) → cost = eff - raw
	// sell: effNotional < rawNotional (we get less) → cost = raw - eff
	if side == "buy" {
		return effNotional - rawNotional
	}
	return rawNotional - effNotional
}
