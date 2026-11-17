package orderflow

import (
	"context"
	"fmt"
	"time"

	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/sirupsen/logrus"
)

// GCTOrderBookFetcher implements OrderBookFetcher using the GCT client.
// It polls the order book via REST (gRPC under the hood) at the interval
// configured in the Feeder. For true real-time data, a WebSocket adapter
// would replace this, but REST polling is correct and sufficient for the
// current use case.
type GCTOrderBookFetcher struct {
	log    *logrus.Logger
	client gct.GCTClient
}

// NewGCTOrderBookFetcher creates a fetcher backed by the given GCTClient.
func NewGCTOrderBookFetcher(log *logrus.Logger, client gct.GCTClient) *GCTOrderBookFetcher {
	if log == nil {
		log = logrus.New()
	}
	return &GCTOrderBookFetcher{
		log:    log,
		client: client,
	}
}

// GetOrderBook implements OrderBookFetcher. It fetches the current order book
// for the given exchange/pair and converts it to an OrderBookSnapshot.
// The depth parameter is accepted for interface compatibility but the underlying
// GCT gRPC call does not currently support per-request depth configuration.
func (f *GCTOrderBookFetcher) GetOrderBook(ctx context.Context, exchange, pair string, depth int) (*OrderBookSnapshot, error) {
	if f.client == nil {
		return nil, fmt.Errorf("gct_fetcher: client is nil")
	}

	ob, err := f.client.GetOrderbook(ctx, exchange, pair)
	if err != nil {
		return nil, fmt.Errorf("gct_fetcher: get orderbook %s/%s: %w", exchange, pair, err)
	}
	if ob == nil {
		return nil, fmt.Errorf("gct_fetcher: nil orderbook for %s/%s", exchange, pair)
	}

	snapshot := &OrderBookSnapshot{
		Pair:      pair,
		Timestamp: time.Now().UTC(),
	}

	// Honour depth limit when converting levels.
	bidCount := len(ob.Bids)
	if depth > 0 && bidCount > depth {
		bidCount = depth
	}
	askCount := len(ob.Asks)
	if depth > 0 && askCount > depth {
		askCount = depth
	}

	snapshot.Bids = make([]OrderLevel, 0, bidCount)
	for _, bid := range ob.Bids[:bidCount] {
		snapshot.Bids = append(snapshot.Bids, OrderLevel{
			Price: bid.Price,
			Size:  bid.Amount,
		})
	}

	snapshot.Asks = make([]OrderLevel, 0, askCount)
	for _, ask := range ob.Asks[:askCount] {
		snapshot.Asks = append(snapshot.Asks, OrderLevel{
			Price: ask.Price,
			Size:  ask.Amount,
		})
	}

	return snapshot, nil
}
