package gct

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"
)

// Order notional in USD, computed once per order for the RiskSentinel
// (OpenSpec guarded-mcp-product, D4). The unit of SubmitOrderRequest.Amount
// depends on the route, and this file is the single place that knows it:
//
//   - Futures route (SubmitFuturesOrder -> FuturesManager.PlaceFuturesOrder):
//     Amount is a USD sizing target, NOT a quantity. The manager converts it to
//     whole contracts with contracts = max(1, round(USD / (price*multiplier)))
//     (futures_manager.go, "Step 5"), where multiplier is the KuCoin contract
//     "multiplier" (base units per contract, GetContractMultiplier) and price is
//     the order's limit price or the futures ticker. The real notional is
//     therefore contracts*multiplier*price, which can exceed Amount when one
//     contract is worth more than the target (e.g. $2 target, 1 contract =
//     $6.58). futuresContracts below is the shared formula.
//   - Spot route (SubmitOrder -> GCT gRPC): Amount is base-asset units, so the
//     notional is Amount*price.
//   - A futures-looking pair that reaches the spot path (no leverage and no
//     explicit futures market type) has no defined unit: its notional is
//     reported unknown instead of guessed.
//
// Orders without a price of their own use a reference price no older than
// risk.reference_price_max_age; a missing/stale price or a missing contract
// multiplier makes the notional unknown (the sentinel fails closed on it).

// futuresContracts is the USD -> whole contracts conversion used by
// PlaceFuturesOrder and by the notional calculation, so both agree.
func futuresContracts(usdAmount, price, multiplier float64) float64 {
	return math.Max(1, math.Round(usdAmount/(price*multiplier)))
}

// orderNotional is the result of computeOrderNotional.
type orderNotional struct {
	USD     float64
	Unknown bool
	Reason  string
}

func unknownNotional(format string, args ...interface{}) orderNotional {
	return orderNotional{Unknown: true, Reason: fmt.Sprintf(format, args...)}
}

// referencePrice is a cached real price of a symbol.
type referencePrice struct {
	price float64
	at    time.Time
}

// priceSource and specSource are the two exchange lookups the notional needs;
// they are fields so tests can stub them.
type notionalSources struct {
	// ticker returns the last price and the exchange timestamp of the snapshot
	// (zero when unknown).
	ticker func(ctx context.Context, symbol string, futures bool) (float64, time.Time, error)
	// multiplier returns base units per contract of a futures symbol.
	multiplier func(ctx context.Context, symbol string) (float64, error)
}

// notionalState caches reference prices and contract multipliers.
type notionalState struct {
	mu          sync.Mutex
	prices      map[string]referencePrice
	multipliers map[string]float64 // static per symbol
	src         *notionalSources   // nil = use the KuCoin REST client
}

// referencePriceFor returns a real price of symbol no older than maxAge.
func (c *client) referencePriceFor(ctx context.Context, symbol string, futures bool, maxAge time.Duration) (float64, error) {
	key := symbol
	if futures {
		key = "F:" + symbol
	}
	now := time.Now()
	c.notional.mu.Lock()
	if rp, ok := c.notional.prices[key]; ok && now.Sub(rp.at) <= maxAge {
		c.notional.mu.Unlock()
		return rp.price, nil
	}
	src := c.notional.src
	c.notional.mu.Unlock()

	var (
		price float64
		ts    time.Time
		err   error
	)
	switch {
	case src != nil && src.ticker != nil:
		price, ts, err = src.ticker(ctx, symbol, futures)
	case c.kucoinClient != nil:
		var t *Ticker
		if futures {
			t, err = c.kucoinClient.GetFuturesTicker(ctx, symbol)
		} else {
			t, err = c.kucoinClient.GetTicker(ctx, symbol)
		}
		if err == nil && t != nil {
			price, ts = t.Last, t.Timestamp
		}
	default:
		err = fmt.Errorf("no price source available")
	}
	if err != nil {
		return 0, err
	}
	if price <= 0 {
		return 0, fmt.Errorf("non-positive price %.8f", price)
	}
	// The exchange timestamp, when present and plausible, must also be recent:
	// a ticker snapshot older than the allowed age is not a "recent real price".
	if ts.Year() >= 2020 && now.Sub(ts) > maxAge {
		return 0, fmt.Errorf("price snapshot is %s old (max %s)", now.Sub(ts).Round(time.Second), maxAge)
	}
	c.notional.mu.Lock()
	if c.notional.prices == nil {
		c.notional.prices = make(map[string]referencePrice)
	}
	c.notional.prices[key] = referencePrice{price: price, at: now}
	c.notional.mu.Unlock()
	return price, nil
}

// contractMultiplierStrict returns the contract multiplier or an error; unlike
// FuturesManager.contractMultiplierFor it never falls back to 1.0.
func (c *client) contractMultiplierStrict(ctx context.Context, symbol string) (float64, error) {
	c.notional.mu.Lock()
	if m, ok := c.notional.multipliers[symbol]; ok {
		c.notional.mu.Unlock()
		return m, nil
	}
	src := c.notional.src
	c.notional.mu.Unlock()

	var (
		m   float64
		err error
	)
	switch {
	case src != nil && src.multiplier != nil:
		m, err = src.multiplier(ctx, symbol)
	case c.kucoinClient != nil:
		m, err = c.kucoinClient.GetContractMultiplier(ctx, symbol)
	default:
		err = fmt.Errorf("no contract specification source available")
	}
	if err != nil {
		return 0, err
	}
	if m <= 0 {
		return 0, fmt.Errorf("invalid multiplier %.6f", m)
	}
	c.notional.mu.Lock()
	if c.notional.multipliers == nil {
		c.notional.multipliers = make(map[string]float64)
	}
	c.notional.multipliers[symbol] = m
	c.notional.mu.Unlock()
	return m, nil
}

// computeOrderNotional returns the real USD notional of req for the given route.
func (c *client) computeOrderNotional(ctx context.Context, req *SubmitOrderRequest, futures bool, maxAge time.Duration) orderNotional {
	if req.Amount <= 0 {
		return unknownNotional("non-positive amount %.8f", req.Amount)
	}

	if futures {
		symbol := convertPairToFuturesSymbol(req.Pair)
		multiplier, err := c.contractMultiplierStrict(ctx, symbol)
		if err != nil {
			return unknownNotional("contract specification of %s unavailable: %v", symbol, err)
		}
		price := req.Price
		if price <= 0 {
			price, err = c.referencePriceFor(ctx, symbol, true, maxAge)
			if err != nil {
				return unknownNotional("no recent reference price for %s: %v", symbol, err)
			}
		}
		contracts := futuresContracts(req.Amount, price, multiplier)
		return orderNotional{USD: contracts * multiplier * price}
	}

	if assetType := detectAssetType(req.Pair); assetType == "futures" {
		return unknownNotional("%s looks like a futures pair on the spot path: quantity unit is undefined", req.Pair)
	}
	price := req.Price
	if price <= 0 {
		var err error
		price, err = c.referencePriceFor(ctx, req.Pair, false, maxAge)
		if err != nil {
			return unknownNotional("no recent reference price for %s: %v", req.Pair, err)
		}
	}
	return orderNotional{USD: req.Amount * price}
}
