package exchange

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// ConsolidatedLevel is a single price level attributed to its source exchange.
type ConsolidatedLevel struct {
	Exchange string  `json:"exchange"`
	Price    float64 `json:"price"`
	Size     float64 `json:"size"`
}

// ConsolidatedBook aggregates order-book levels across exchanges.
type ConsolidatedBook struct {
	Pair      string              `json:"pair"`
	Bids      []ConsolidatedLevel `json:"bids"`
	Asks      []ConsolidatedLevel `json:"asks"`
	BestBid   ConsolidatedLevel   `json:"best_bid"`
	BestAsk   ConsolidatedLevel   `json:"best_ask"`
	Crossed   bool                `json:"crossed"`
	SpreadPct float64             `json:"spread_pct"`
	Timestamp time.Time           `json:"timestamp"`
}

// OrderBookSource is an optional real depth feed. bids and asks are each a slice
// of [price, size] pairs. It may be nil (then top-of-book from tickers is used).
type OrderBookSource interface {
	GetOrderBook(ctx context.Context, exchange, pair string, depth int) (bids [][2]float64, asks [][2]float64, err error)
}

// ConsolidatedBook builds an aggregated book across active exchanges. If an
// OrderBookSource is configured it is used for depth; otherwise it falls back to
// the PriceMatrix top-of-book (one bid and one ask level per exchange).
func (r *Router) ConsolidatedBook(ctx context.Context, pair string, depth int) (*ConsolidatedBook, error) {
	var (
		bids []ConsolidatedLevel
		asks []ConsolidatedLevel
	)

	if r.books != nil {
		exchanges, err := r.ActiveExchanges(ctx)
		if err != nil {
			return nil, fmt.Errorf("resolve active exchanges: %w", err)
		}
		for _, ex := range exchanges {
			symbol := pair
			if r.conv != nil {
				if converted, cerr := r.conv.ConvertSymbol(pair, r.marketType, ex); cerr == nil && converted != "" {
					symbol = converted
				}
			}
			b, a, berr := r.books.GetOrderBook(ctx, ex, symbol, depth)
			if berr != nil {
				continue // skip; never crash on a missing exchange
			}
			for _, lvl := range b {
				bids = append(bids, ConsolidatedLevel{Exchange: ex, Price: lvl[0], Size: lvl[1]})
			}
			for _, lvl := range a {
				asks = append(asks, ConsolidatedLevel{Exchange: ex, Price: lvl[0], Size: lvl[1]})
			}
		}
	} else {
		matrix, err := r.PriceMatrix(ctx, pair)
		if err != nil {
			return nil, err
		}
		for _, q := range matrix {
			if q.Bid > 0 {
				bids = append(bids, ConsolidatedLevel{Exchange: q.Exchange, Price: q.Bid})
			}
			if q.Ask > 0 {
				asks = append(asks, ConsolidatedLevel{Exchange: q.Exchange, Price: q.Ask})
			}
		}
	}

	if len(bids) == 0 && len(asks) == 0 {
		return nil, fmt.Errorf("consolidated book empty for %s", pair)
	}

	// Sort bids descending (best/highest first), asks ascending (best/lowest first).
	sort.Slice(bids, func(i, j int) bool { return bids[i].Price > bids[j].Price })
	sort.Slice(asks, func(i, j int) bool { return asks[i].Price < asks[j].Price })

	if depth > 0 {
		if len(bids) > depth {
			bids = bids[:depth]
		}
		if len(asks) > depth {
			asks = asks[:depth]
		}
	}

	bestBid, bestAsk, crossed := detectCrossedBook(bids, asks)

	book := &ConsolidatedBook{
		Pair:      pair,
		Bids:      bids,
		Asks:      asks,
		BestBid:   bestBid,
		BestAsk:   bestAsk,
		Crossed:   crossed,
		Timestamp: time.Now(),
	}

	if bestBid.Price > 0 && bestAsk.Price > 0 {
		mid := (bestBid.Price + bestAsk.Price) / 2
		if mid > 0 {
			book.SpreadPct = (bestAsk.Price - bestBid.Price) / mid * 100
		}
	}

	return book, nil
}

// detectCrossedBook returns the global best bid and best ask and whether the
// book is synthetically crossed (best bid > best ask, from DIFFERENT exchanges),
// which signals a cross-exchange arbitrage opportunity. It is pure: bids must be
// sorted descending and asks ascending (it does not re-sort, only inspects).
func detectCrossedBook(bids, asks []ConsolidatedLevel) (best ConsolidatedLevel, bestAsk ConsolidatedLevel, crossed bool) {
	// Defensive: pick the global extremes regardless of input order.
	for _, b := range bids {
		if best.Price == 0 || b.Price > best.Price {
			best = b
		}
	}
	for _, a := range asks {
		if bestAsk.Price == 0 || a.Price < bestAsk.Price {
			bestAsk = a
		}
	}

	if best.Price > 0 && bestAsk.Price > 0 &&
		best.Price > bestAsk.Price &&
		best.Exchange != bestAsk.Exchange {
		crossed = true
	}
	return best, bestAsk, crossed
}
