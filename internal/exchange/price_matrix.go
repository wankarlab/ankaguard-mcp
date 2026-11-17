package exchange

import (
	"context"
	"fmt"
	"sync"
)

// PriceMatrix resolves active exchanges and fetches each one's top-of-book
// ticker in parallel. Exchanges whose ticker fetch fails are skipped. If every
// exchange fails (and at least one was attempted), an error is returned.
func (r *Router) PriceMatrix(ctx context.Context, pair string) (map[string]PriceQuote, error) {
	exchanges, err := r.ActiveExchanges(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve active exchanges: %w", err)
	}
	if len(exchanges) == 0 {
		return nil, fmt.Errorf("no active exchanges")
	}

	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		matrix = make(map[string]PriceQuote, len(exchanges))
	)

	for _, ex := range exchanges {
		ex := ex
		wg.Add(1)
		go func() {
			defer wg.Done()

			if ctx.Err() != nil {
				return
			}

			symbol := pair
			if r.conv != nil {
				if converted, cerr := r.conv.ConvertSymbol(pair, r.marketType, ex); cerr == nil && converted != "" {
					symbol = converted
				}
				// On conversion error, fall back to the raw pair; the ticker
				// call below may still succeed or be skipped on error.
			}

			ticker, terr := r.tickers.GetTicker(ctx, ex, symbol)
			if terr != nil || ticker == nil {
				// Skip this exchange; do not fail the whole matrix.
				return
			}

			mu.Lock()
			matrix[ex] = PriceQuote{
				Exchange:  ex,
				Bid:       ticker.Bid,
				Ask:       ticker.Ask,
				Last:      ticker.Last,
				Volume:    ticker.Volume,
				Symbol:    symbol,
				Timestamp: ticker.Timestamp,
			}
			mu.Unlock()
		}()
	}

	wg.Wait()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(matrix) == 0 {
		return nil, fmt.Errorf("price matrix empty: all %d exchange(s) failed for %s", len(exchanges), pair)
	}
	return matrix, nil
}

// venueEffective holds the fee+slippage-adjusted economics for one venue.
type venueEffective struct {
	quote     PriceQuote
	rawPrice  float64 // ask for buy, bid for sell
	effPrice  float64 // fee+slippage adjusted per-unit price
	feeBps    float64
	slipBps   float64
}

// slippageImpactK is the linear price-impact coefficient (bps per 1x of
// top-of-book liquidity consumed) used when no real depth is available. We use
// PriceQuote.Volume as a rough top-of-book proxy.
const slippageImpactK = 10.0

// effectiveForSide computes per-venue effective prices for the requested side.
// For "buy" the relevant raw price is Ask and costs increase it; for "sell" the
// relevant raw price is Bid and costs decrease the proceeds. Venues without a
// usable price for the side are skipped.
func (r *Router) effectiveForSide(matrix map[string]PriceQuote, side string, size float64) []venueEffective {
	out := make([]venueEffective, 0, len(matrix))
	for _, q := range matrix {
		fee := r.feeFor(q.Exchange)
		feeBps := fee.TakerBps
		slipBps := EstimateSlippageBps(size, q.Volume, slippageImpactK)

		var raw float64
		switch side {
		case "buy":
			raw = q.Ask
		case "sell":
			raw = q.Bid
		}
		if raw <= 0 {
			continue
		}

		// Cost fraction = (fee + slippage) in bps → per-unit price adjustment.
		costFrac := (feeBps + slipBps) * 0.0001
		var eff float64
		if side == "buy" {
			eff = raw * (1 + costFrac) // pay more
		} else {
			eff = raw * (1 - costFrac) // receive less
		}

		out = append(out, venueEffective{
			quote:    q,
			rawPrice: raw,
			effPrice: eff,
			feeBps:   feeBps,
			slipBps:  slipBps,
		})
	}
	return out
}

// BestVenue selects the best execution venue for a side, accounting for taker
// fees and an estimated slippage impact (DM-6.1).
//
//   - buy  -> the venue with the lowest effective cost (raw ask + fees + slippage)
//   - sell -> the venue with the highest effective proceeds (raw bid - fees - slippage)
//
// Venue.Price holds the raw touch price; Venue.EffectivePrice holds the
// fee+slippage-adjusted per-unit price. When only one usable venue exists the
// selection is identical to the basic behavior (it is simply that venue).
func (r *Router) BestVenue(ctx context.Context, pair, side string, size float64) (Venue, error) {
	matrix, err := r.PriceMatrix(ctx, pair)
	if err != nil {
		return Venue{}, err
	}
	if len(matrix) == 0 {
		return Venue{}, fmt.Errorf("best venue: empty price matrix for %s", pair)
	}
	if side != "buy" && side != "sell" {
		return Venue{}, fmt.Errorf("best venue: invalid side %q (want buy|sell)", side)
	}

	cands := r.effectiveForSide(matrix, side, size)
	if len(cands) == 0 {
		return Venue{}, fmt.Errorf("best venue: no usable %s price for %s", side, pair)
	}

	best := cands[0]
	for _, c := range cands[1:] {
		if side == "buy" {
			if c.effPrice < best.effPrice {
				best = c
			}
		} else { // sell
			if c.effPrice > best.effPrice {
				best = c
			}
		}
	}

	var verb string
	if side == "buy" {
		verb = "lowest effective cost"
	} else {
		verb = "highest effective proceeds"
	}
	return Venue{
		Exchange:       best.quote.Exchange,
		Symbol:         best.quote.Symbol,
		Price:          best.rawPrice,
		EffectivePrice: best.effPrice,
		Reason: fmt.Sprintf("%s on %s among %d venue(s): raw=%.6f eff=%.6f (fee=%.1fbps slip=%.1fbps)",
			verb, best.quote.Exchange, len(matrix), best.rawPrice, best.effPrice, best.feeBps, best.slipBps),
	}, nil
}
