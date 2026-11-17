package adapters

import (
	"context"
	"fmt"
	"sort"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/ai"
	"github.com/rantipay/trading-mcp/internal/master"
	"github.com/rantipay/trading-mcp/internal/master/universe"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
)

// GCTAdapter adapts gctclient.Client to master.GCTAdapter.
type GCTAdapter struct {
	log    *logrus.Logger
	client *gctclient.Client
	// provider supplies the live pair universe (AUTO/MANUAL/HYBRID). Injected via
	// SetUniverse after construction so main.go can build both pieces independently.
	provider universe.PairUniverseProvider
	// filters holds the discovery screening criteria. Stage-1 (volume/exclude)
	// filters are applied here; stage-2 (volatility/correlation) in the scanner.
	filters universe.DiscoveryFilters
	// fallbackPairs is used when GetAllTickers returns nothing (current GCT
	// implementation is a stub). Adjust per exchange as needed.
	fallbackPairs map[string][]string
}

// NewGCTAdapter constructs the adapter. client must be non-nil for live data.
func NewGCTAdapter(log *logrus.Logger, client *gctclient.Client) *GCTAdapter {
	if log == nil {
		log = logrus.New()
	}
	return &GCTAdapter{
		log:    log,
		client: client,
		// fallbackPairs is now an emergency fallback only. Real pair universe comes
		// from the dynamic discovery provider (config.pair_universe.source).
		fallbackPairs: map[string][]string{},
	}
}

// SetUniverse injects the live pair-universe provider and discovery filters.
// Called from main.go after both the adapter and provider are constructed.
func (g *GCTAdapter) SetUniverse(p universe.PairUniverseProvider, f universe.DiscoveryFilters) {
	g.provider = p
	g.filters = f
}

// GetTopPairsByVolume is the stage-1 discovery funnel: it sources the live pair
// universe (dynamic provider, with the static map as an emergency fallback),
// fetches each ticker, applies exclude + volume filters, then ranks by 24h
// volume and cuts to n.
//
// Stage-2 filters (volatility/ATR and BTC correlation) are NOT applied here —
// they require candles and are enforced later in the market scanner.
func (g *GCTAdapter) GetTopPairsByVolume(ctx context.Context, exchange string, n int) ([]master.TickerSummary, error) {
	if g.client == nil {
		return nil, fmt.Errorf("gct client not initialized")
	}
	if n <= 0 {
		n = 20
	}

	// Source pairs: prefer the live universe provider; fall back to the static
	// emergency map on provider error or when no provider is wired.
	var pairs []string
	if g.provider != nil {
		p, err := g.provider.Pairs(ctx, exchange)
		if err != nil {
			g.log.WithError(err).WithField("exchange", exchange).Warn("gct_adapter: universe provider error, using fallback pairs")
			pairs = g.fallbackPairs[exchange]
		} else {
			pairs = p
		}
	} else {
		pairs = g.fallbackPairs[exchange]
	}
	if len(pairs) == 0 {
		g.log.WithField("exchange", exchange).Warn("gct_adapter: no pair universe available for exchange")
		return []master.TickerSummary{}, nil
	}

	// Batch volume source: a single call returns real 24h volume for the whole
	// exchange. Per-pair GetTicker (gRPC) returns volume=0 for non-streamed
	// pairs, which silently collapsed the universe to the ~4 streamed majors.
	// When the batch source is unavailable (non-kucoin or fetch error) we fall
	// back to the per-pair path, preserving previous behavior.
	stats, statsErr := g.client.GetSpotTickerStats(ctx, exchange)
	if statsErr != nil {
		g.log.WithError(statsErr).WithField("exchange", exchange).
			Warn("gct_adapter: batch ticker stats failed, falling back to per-pair tickers")
		stats = nil
	}

	out := make([]master.TickerSummary, 0, len(pairs))
	skippedNoSpot := 0
	for _, p := range pairs {
		if g.filters.IsExcluded(p) {
			continue
		}

		var volume24hUSD, lastPrice, change float64
		if len(stats) > 0 {
			s, ok := stats[p]
			if !ok {
				// Pair absent from the spot batch (e.g. a futures-only contract
				// with no spot market). The scanner fetches spot candles, so such
				// pairs can't be analyzed anyway — skip without a per-pair call.
				skippedNoSpot++
				continue
			}
			volume24hUSD = s.VolumeUSD
			lastPrice = s.Last
			change = s.ChangePct
		} else {
			// Legacy fallback: per-pair ticker fetch.
			ticker, err := g.client.GetTicker(ctx, exchange, p)
			if err != nil {
				g.log.WithError(err).WithField("pair", p).Debug("gct_adapter: ticker fetch failed")
				continue
			}
			volume24hUSD = ticker.Volume * ticker.Last
			if volume24hUSD == 0 && ticker.Volume > 0 {
				volume24hUSD = ticker.Volume // use raw volume if price unavailable
			}
			lastPrice = ticker.Last
			change = ticker.Change24h
		}

		if !g.filters.PassesVolume(volume24hUSD) {
			continue
		}
		out = append(out, master.TickerSummary{
			Pair:             p,
			Exchange:         exchange,
			Volume24h:        volume24hUSD,
			LastPrice:        lastPrice,
			Change24hPercent: change,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Volume24h > out[j].Volume24h })
	if len(out) > n {
		out = out[:n]
	}

	g.log.WithFields(logrus.Fields{
		"exchange":       exchange,
		"universe":       len(pairs),
		"passed_volume":  len(out),
		"skipped_nospot": skippedNoSpot,
		"batch_source":   len(stats) > 0,
	}).Info("gct_adapter: stage-1 volume funnel complete")

	return out, nil
}

// GetUSDTBalance implements master.BalanceFetcher. It returns the total USDT
// balance for the given exchange using the underlying gctclient. Spot balance
// is tried first; futures balance is the fallback. Returns an error only when
// both attempts fail so callers can gracefully retain their last known value.
func (g *GCTAdapter) GetUSDTBalance(ctx context.Context, exchange string) (float64, error) {
	if g.client == nil {
		return 0, fmt.Errorf("gct_adapter: client not initialised")
	}
	bal, err := g.client.GetBalance(ctx, exchange, "USDT")
	if err != nil {
		return 0, fmt.Errorf("gct_adapter: GetBalance(USDT): %w", err)
	}
	if bal == nil {
		return 0, fmt.Errorf("gct_adapter: nil balance returned for USDT")
	}
	return bal.Total, nil
}

// Ensure GCTAdapter satisfies the BalanceFetcher interface at compile time.
var _ master.BalanceFetcher = (*GCTAdapter)(nil)

// GetCandles fetches candles via gctclient and converts them into ai.Candle.
func (g *GCTAdapter) GetCandles(ctx context.Context, exchange, pair, interval string, limit int) ([]ai.Candle, error) {
	if g.client == nil {
		return nil, fmt.Errorf("gct client not initialized")
	}
	candles, err := g.client.GetCandles(ctx, exchange, pair, interval, limit)
	if err != nil {
		return nil, err
	}
	out := make([]ai.Candle, len(candles))
	for i, c := range candles {
		out[i] = ai.Candle{
			Timestamp: c.Timestamp,
			Open:      c.Open,
			High:      c.High,
			Low:       c.Low,
			Close:     c.Close,
			Volume:    c.Volume,
		}
	}
	return out, nil
}
