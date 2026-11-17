package websocket

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

// tickerGetter is the only method GCTPriceSource needs from a GCT client.
// Using a narrow interface avoids coupling to the full gct.GCTClient.
type tickerGetter interface {
	GetTicker(ctx context.Context, exchange, symbol string) (*Ticker, error)
}

// GCTPriceSource fetches prices from GoCryptoTrader and broadcasts to WebSocket clients.
// This integrates with the existing MarketPriceService to provide real-time price updates.
type GCTPriceSource struct {
	gctClient   tickerGetter
	broadcaster *MarketPriceBroadcaster
	logger      *slog.Logger

	// Configuration
	config *GCTPriceSourceConfig

	// Subscription tracking
	subscribedSymbols map[string]bool
	mu                sync.RWMutex

	// Control channels
	stopChan chan struct{}
	doneChan chan struct{}

	// Metrics
	updateCount   int64
	errorCount    int64
	lastUpdateTime time.Time
}

// GCTPriceSourceConfig holds configuration for the GCT price source.
type GCTPriceSourceConfig struct {
	// Exchange to fetch prices from (e.g., "kucoin", "binance")
	Exchange string

	// UpdateInterval is how often to fetch prices
	UpdateInterval time.Duration

	// ChangeThreshold is the minimum price change % to broadcast (0.1 = 0.1%)
	ChangeThreshold float64

	// BatchSize is the maximum number of symbols to fetch per batch
	BatchSize int

	// Enabled indicates if the source is enabled
	Enabled bool
}

// DefaultGCTPriceSourceConfig returns default configuration.
func DefaultGCTPriceSourceConfig() *GCTPriceSourceConfig {
	return &GCTPriceSourceConfig{
		Exchange:        "kucoin",
		UpdateInterval:  5 * time.Second,
		ChangeThreshold: 0.1, // 0.1% minimum change
		BatchSize:       50,
		Enabled:         true,
	}
}

// NewGCTPriceSource creates a new GCT price source.
func NewGCTPriceSource(
	gctClient tickerGetter,
	broadcaster *MarketPriceBroadcaster,
	config *GCTPriceSourceConfig,
	logger *slog.Logger,
) *GCTPriceSource {
	if config == nil {
		config = DefaultGCTPriceSourceConfig()
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &GCTPriceSource{
		gctClient:         gctClient,
		broadcaster:       broadcaster,
		config:            config,
		logger:            logger.With("component", "gct_price_source"),
		subscribedSymbols: make(map[string]bool),
		stopChan:          make(chan struct{}),
		doneChan:          make(chan struct{}),
	}
}

// Start starts the GCT price source.
func (s *GCTPriceSource) Start(ctx context.Context) error {
	if !s.config.Enabled {
		s.logger.Info("GCT price source is disabled")
		return nil
	}

	s.logger.Info("starting GCT price source",
		"exchange", s.config.Exchange,
		"update_interval", s.config.UpdateInterval,
	)

	// Start price fetching loop
	go s.fetchPricesLoop(ctx)

	// Start subscription monitoring
	go s.monitorSubscriptions(ctx)

	return nil
}

// Stop stops the GCT price source.
func (s *GCTPriceSource) Stop() {
	s.logger.Info("stopping GCT price source")
	close(s.stopChan)
	<-s.doneChan
}

// fetchPricesLoop continuously fetches prices from GCT.
func (s *GCTPriceSource) fetchPricesLoop(ctx context.Context) {
	defer func() {
		s.doneChan <- struct{}{}
	}()

	ticker := time.NewTicker(s.config.UpdateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopChan:
			return
		case <-ticker.C:
			s.fetchAndBroadcastPrices(ctx)
		}
	}
}

// fetchAndBroadcastPrices fetches prices for all subscribed symbols and broadcasts updates.
func (s *GCTPriceSource) fetchAndBroadcastPrices(ctx context.Context) {
	s.mu.RLock()
	symbols := make([]string, 0, len(s.subscribedSymbols))
	for symbol := range s.subscribedSymbols {
		symbols = append(symbols, symbol)
	}
	s.mu.RUnlock()

	if len(symbols) == 0 {
		return
	}

	s.logger.Debug("fetching prices from GCT",
		"symbol_count", len(symbols),
		"exchange", s.config.Exchange,
	)

	// Fetch prices in batches
	for i := 0; i < len(symbols); i += s.config.BatchSize {
		end := i + s.config.BatchSize
		if end > len(symbols) {
			end = len(symbols)
		}

		batch := symbols[i:end]
		s.fetchBatch(ctx, batch)
	}
}

// fetchBatch fetches prices for a batch of symbols.
func (s *GCTPriceSource) fetchBatch(ctx context.Context, symbols []string) {
	for _, symbol := range symbols {
		select {
		case <-ctx.Done():
			return
		case <-s.stopChan:
			return
		default:
			s.fetchAndBroadcastSymbol(ctx, symbol)
		}
	}
}

// fetchAndBroadcastSymbol fetches price for a single symbol and broadcasts if changed.
func (s *GCTPriceSource) fetchAndBroadcastSymbol(ctx context.Context, symbol string) {
	// Fetch ticker from GCT
	ticker, err := s.gctClient.GetTicker(ctx, s.config.Exchange, symbol)
	if err != nil {
		s.errorCount++
		s.logger.Debug("failed to fetch ticker from GCT",
			"symbol", symbol,
			"exchange", s.config.Exchange,
			"error", err,
		)
		return
	}

	// Convert GCT ticker to market price update
	update := s.convertTickerToUpdate(ticker)

	// Check if price changed significantly
	if s.shouldBroadcast(symbol, update) {
		s.broadcaster.BroadcastPriceUpdate(update)
		s.updateCount++
		s.lastUpdateTime = time.Now()

		s.logger.Debug("broadcasted price update",
			"symbol", symbol,
			"price", update.Price,
			"source", update.Source,
		)
	}
}

// convertTickerToUpdate converts a GCT ticker to a market price update.
func (s *GCTPriceSource) convertTickerToUpdate(ticker *Ticker) *MarketPriceUpdatePayload {
	// Calculate 24h change
	var change24h, change24hPercent float64
	if !ticker.Open.IsZero() {
		change24h, _ = ticker.Last.Sub(ticker.Open).Float64()
		change24hPercent, _ = ticker.Last.Sub(ticker.Open).Div(ticker.Open).Mul(decimal.NewFromInt(100)).Float64()
	}

	price, _ := ticker.Last.Float64()
	bid, _ := ticker.Bid.Float64()
	ask, _ := ticker.Ask.Float64()
	volume24h, _ := ticker.Volume.Float64()
	high24h, _ := ticker.High.Float64()
	low24h, _ := ticker.Low.Float64()

	return &MarketPriceUpdatePayload{
		Symbol:           ticker.Symbol,
		Price:            price,
		Bid:              bid,
		Ask:              ask,
		Volume24h:        volume24h,
		Change24h:        change24h,
		Change24hPercent: change24hPercent,
		High24h:          high24h,
		Low24h:           low24h,
		LastUpdate:       ticker.Timestamp.Format(time.RFC3339),
		Source:           ticker.Exchange,
	}
}

// shouldBroadcast determines if a price update should be broadcast.
func (s *GCTPriceSource) shouldBroadcast(symbol string, update *MarketPriceUpdatePayload) bool {
	// Get cached price
	cached, exists := s.broadcaster.GetCachedPrice(symbol)
	if !exists {
		return true // Always broadcast if no cached price
	}

	// Calculate price change percentage
	if cached.Price == 0 {
		return true
	}

	changePercent := ((update.Price - cached.Price) / cached.Price) * 100
	if changePercent < 0 {
		changePercent = -changePercent
	}

	// Broadcast if change exceeds threshold
	return changePercent >= s.config.ChangeThreshold
}

// monitorSubscriptions monitors the broadcaster for new subscriptions.
func (s *GCTPriceSource) monitorSubscriptions(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopChan:
			return
		case <-ticker.C:
			s.syncSubscriptions()
		}
	}
}

// syncSubscriptions syncs subscribed symbols from the broadcaster.
func (s *GCTPriceSource) syncSubscriptions() {
	// Get all subscriptions from broadcaster
	subscriptions := s.broadcaster.GetAllSubscriptions()

	s.mu.Lock()
	defer s.mu.Unlock()

	// Update subscribed symbols
	newSymbols := make(map[string]bool)
	for symbol := range subscriptions {
		newSymbols[symbol] = true
	}

	// Log new subscriptions
	for symbol := range newSymbols {
		if !s.subscribedSymbols[symbol] {
			s.logger.Info("new symbol subscription", "symbol", symbol)
		}
	}

	// Log removed subscriptions
	for symbol := range s.subscribedSymbols {
		if !newSymbols[symbol] {
			s.logger.Info("symbol unsubscribed", "symbol", symbol)
		}
	}

	s.subscribedSymbols = newSymbols
}

// GetMetrics returns metrics for the price source.
func (s *GCTPriceSource) GetMetrics() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return map[string]interface{}{
		"enabled":           s.config.Enabled,
		"exchange":          s.config.Exchange,
		"subscribed_symbols": len(s.subscribedSymbols),
		"update_count":      s.updateCount,
		"error_count":       s.errorCount,
		"last_update":       s.lastUpdateTime,
		"update_interval":   s.config.UpdateInterval.String(),
	}
}

// IsHealthy returns true if the price source is healthy.
func (s *GCTPriceSource) IsHealthy() bool {
	if !s.config.Enabled {
		return false
	}

	// Check if we've had recent updates
	if time.Since(s.lastUpdateTime) > s.config.UpdateInterval*3 {
		return false
	}

	// Check error rate
	if s.updateCount > 0 {
		errorRate := float64(s.errorCount) / float64(s.updateCount+s.errorCount)
		if errorRate > 0.5 { // More than 50% errors
			return false
		}
	}

	return true
}

// GetConfig returns the current configuration.
func (s *GCTPriceSource) GetConfig() *GCTPriceSourceConfig {
	return s.config
}

// UpdateConfig updates the configuration (requires restart to take effect).
func (s *GCTPriceSource) UpdateConfig(config *GCTPriceSourceConfig) {
	s.config = config
}
