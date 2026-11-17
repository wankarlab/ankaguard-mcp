package websocket

import (
	"context"
	"log/slog"
	"time"

	"github.com/shopspring/decimal"
)

// GCTPriceAdapter adapts the existing GCTForwarder to work with the new MarketPriceService.
// This provides a bridge until full gRPC integration is complete.
type GCTPriceAdapter struct {
	broadcaster *MarketPriceBroadcaster
	logger      *slog.Logger
	config      *GCTPriceSourceConfig

	// Simulated price data (temporary until gRPC is fully integrated)
	prices map[string]*simulatedPrice

	stopChan chan struct{}
	doneChan chan struct{}
}

type simulatedPrice struct {
	basePrice float64
	lastPrice float64
	timestamp time.Time
}

// NewGCTPriceAdapter creates a new GCT price adapter.
func NewGCTPriceAdapter(
	broadcaster *MarketPriceBroadcaster,
	config *GCTPriceSourceConfig,
	logger *slog.Logger,
) *GCTPriceAdapter {
	if config == nil {
		config = DefaultGCTPriceSourceConfig()
	}

	if logger == nil {
		logger = slog.Default()
	}

	// Initialize with common trading pairs
	prices := map[string]*simulatedPrice{
		"BTC-USDT":  {basePrice: 43250.0, lastPrice: 43250.0, timestamp: time.Now()},
		"ETH-USDT":  {basePrice: 2580.0, lastPrice: 2580.0, timestamp: time.Now()},
		"SHIB-USDT": {basePrice: 0.00001234, lastPrice: 0.00001234, timestamp: time.Now()},
		"BNB-USDT":  {basePrice: 315.0, lastPrice: 315.0, timestamp: time.Now()},
		"SOL-USDT":  {basePrice: 98.5, lastPrice: 98.5, timestamp: time.Now()},
		"ADA-USDT":  {basePrice: 0.52, lastPrice: 0.52, timestamp: time.Now()},
		"XRP-USDT":  {basePrice: 0.61, lastPrice: 0.61, timestamp: time.Now()},
		"DOT-USDT":  {basePrice: 7.25, lastPrice: 7.25, timestamp: time.Now()},
		"DOGE-USDT": {basePrice: 0.082, lastPrice: 0.082, timestamp: time.Now()},
		"AVAX-USDT": {basePrice: 36.5, lastPrice: 36.5, timestamp: time.Now()},
	}

	return &GCTPriceAdapter{
		broadcaster: broadcaster,
		config:      config,
		logger:      logger.With("component", "gct_price_adapter"),
		prices:      prices,
		stopChan:    make(chan struct{}),
		doneChan:    make(chan struct{}),
	}
}

// Start starts the price adapter.
func (a *GCTPriceAdapter) Start(ctx context.Context) error {
	if !a.config.Enabled {
		a.logger.Info("GCT price adapter is disabled")
		return nil
	}

	a.logger.Info("starting GCT price adapter (simulated mode)",
		"exchange", a.config.Exchange,
		"update_interval", a.config.UpdateInterval,
	)

	// Start price simulation loop
	go a.simulatePricesLoop(ctx)

	return nil
}

// Stop stops the price adapter.
func (a *GCTPriceAdapter) Stop() {
	a.logger.Info("stopping GCT price adapter")
	close(a.stopChan)
	<-a.doneChan
}

// simulatePricesLoop simulates price updates.
func (a *GCTPriceAdapter) simulatePricesLoop(ctx context.Context) {
	defer func() {
		a.doneChan <- struct{}{}
	}()

	ticker := time.NewTicker(a.config.UpdateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stopChan:
			return
		case <-ticker.C:
			a.updateAndBroadcastPrices()
		}
	}
}

// updateAndBroadcastPrices updates simulated prices and broadcasts them.
func (a *GCTPriceAdapter) updateAndBroadcastPrices() {
	// Get all subscriptions
	subscriptions := a.broadcaster.GetAllSubscriptions()

	for symbol := range subscriptions {
		// Get or create price data
		priceData, exists := a.prices[symbol]
		if !exists {
			// Create new price data for unknown symbols
			priceData = &simulatedPrice{
				basePrice: 100.0, // Default base price
				lastPrice: 100.0,
				timestamp: time.Now(),
			}
			a.prices[symbol] = priceData
			
			a.logger.Info("initialized price data for new symbol",
				"symbol", symbol,
				"base_price", priceData.basePrice,
			)
		}

		// Simulate price movement (±0.5% random walk)
		change := (float64(time.Now().UnixNano()%1000) - 500) / 100000 // ±0.5%
		newPrice := priceData.lastPrice * (1 + change)

		// Calculate 24h change
		change24h := newPrice - priceData.basePrice
		change24hPercent := (change24h / priceData.basePrice) * 100

		// Create price update
		update := &MarketPriceUpdatePayload{
			Symbol:           symbol,
			Price:            newPrice,
			Bid:              newPrice * 0.9995, // 0.05% spread
			Ask:              newPrice * 1.0005,
			Volume24h:        float64(time.Now().UnixNano()%10000000) / 100,
			Change24h:        change24h,
			Change24hPercent: change24hPercent,
			High24h:          priceData.basePrice * 1.05,
			Low24h:           priceData.basePrice * 0.95,
			LastUpdate:       time.Now().UTC().Format(time.RFC3339),
			Source:           a.config.Exchange,
		}

		// Broadcast update
		a.broadcaster.BroadcastPriceUpdate(update)

		// Update last price
		priceData.lastPrice = newPrice
		priceData.timestamp = time.Now()

		a.logger.Debug("broadcasted simulated price",
			"symbol", symbol,
			"price", newPrice,
			"change_24h_percent", change24hPercent,
		)
	}
}

// GetTicker simulates getting a ticker (implements GCTClient interface partially).
func (a *GCTPriceAdapter) GetTicker(ctx context.Context, exchange, symbol string) (*Ticker, error) {
	priceData, exists := a.prices[symbol]
	if !exists {
		// Return default ticker for unknown symbols
		priceData = &simulatedPrice{
			basePrice: 100.0,
			lastPrice: 100.0,
			timestamp: time.Now(),
		}
	}

	// Create ticker
	last := decimal.NewFromFloat(priceData.lastPrice)
	bid := last.Mul(decimal.NewFromFloat(0.9995))
	ask := last.Mul(decimal.NewFromFloat(1.0005))
	open := decimal.NewFromFloat(priceData.basePrice)
	high := open.Mul(decimal.NewFromFloat(1.05))
	low := open.Mul(decimal.NewFromFloat(0.95))
	volume := decimal.NewFromInt(int64(time.Now().UnixNano() % 1000000))

	ticker := &Ticker{
		Exchange:      exchange,
		Symbol:        symbol,
		Bid:           bid,
		Ask:           ask,
		Last:          last,
		High:          high,
		Low:           low,
		Volume:        volume,
		QuoteVolume:   volume.Mul(last),
		Open:          open,
		Change:        last.Sub(open),
		ChangePercent: last.Sub(open).Div(open).Mul(decimal.NewFromInt(100)),
		Timestamp:     time.Now(),
	}

	return ticker, nil
}

// IsHealthy returns true if the adapter is healthy.
func (a *GCTPriceAdapter) IsHealthy() bool {
	return a.config.Enabled
}

// GetMetrics returns metrics for the adapter.
func (a *GCTPriceAdapter) GetMetrics() map[string]interface{} {
	return map[string]interface{}{
		"enabled":        a.config.Enabled,
		"exchange":       a.config.Exchange,
		"simulated":      true,
		"tracked_symbols": len(a.prices),
		"update_interval": a.config.UpdateInterval.String(),
	}
}
