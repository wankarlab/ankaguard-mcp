package websocket

import (
	"log/slog"
	"time"
)

// MarketPriceBroadcaster provides a convenient interface for broadcasting market price updates.
type MarketPriceBroadcaster struct {
	hub    *Hub
	logger *slog.Logger
}

// NewMarketPriceBroadcaster creates a new market price broadcaster.
func NewMarketPriceBroadcaster(hub *Hub, logger *slog.Logger) *MarketPriceBroadcaster {
	if logger == nil {
		logger = slog.Default()
	}

	return &MarketPriceBroadcaster{
		hub:    hub,
		logger: logger.With("component", "market_price_broadcaster"),
	}
}

// BroadcastPriceUpdate broadcasts a price update for a symbol.
func (b *MarketPriceBroadcaster) BroadcastPriceUpdate(update *MarketPriceUpdatePayload) {
	if update == nil {
		b.logger.Warn("attempted to broadcast nil price update")
		return
	}

	// Ensure timestamp is set.
	if update.LastUpdate == "" {
		update.LastUpdate = time.Now().UTC().Format(time.RFC3339)
	}

	// Send to market price service for distribution to subscribers.
	b.hub.MarketPriceService().UpdatePrice(update)

	b.logger.Debug("price update broadcast",
		"symbol", update.Symbol,
		"price", update.Price,
		"source", update.Source,
	)
}

// BroadcastSimplePriceUpdate broadcasts a simple price update with minimal fields.
func (b *MarketPriceBroadcaster) BroadcastSimplePriceUpdate(symbol string, price float64, source string) {
	update := &MarketPriceUpdatePayload{
		Symbol:     symbol,
		Price:      price,
		LastUpdate: time.Now().UTC().Format(time.RFC3339),
		Source:     source,
	}

	b.BroadcastPriceUpdate(update)
}

// BroadcastFullPriceUpdate broadcasts a complete price update with all fields.
func (b *MarketPriceBroadcaster) BroadcastFullPriceUpdate(
	symbol string,
	price, bid, ask float64,
	volume24h, change24h, change24hPercent float64,
	high24h, low24h float64,
	source string,
) {
	update := &MarketPriceUpdatePayload{
		Symbol:           symbol,
		Price:            price,
		Bid:              bid,
		Ask:              ask,
		Volume24h:        volume24h,
		Change24h:        change24h,
		Change24hPercent: change24hPercent,
		High24h:          high24h,
		Low24h:           low24h,
		LastUpdate:       time.Now().UTC().Format(time.RFC3339),
		Source:           source,
	}

	b.BroadcastPriceUpdate(update)
}

// BroadcastPriceError broadcasts a price error message.
func (b *MarketPriceBroadcaster) BroadcastPriceError(symbol, errorMsg, source string, retryAfter int) {
	message := NewMarketPriceErrorMessage(symbol, errorMsg, source, retryAfter)
	b.hub.BroadcastToChannel(ChannelMarketPrices, message)

	b.logger.Warn("price error broadcast",
		"symbol", symbol,
		"error", errorMsg,
		"source", source,
	)
}

// GetSubscriberCount returns the number of subscribers for a symbol.
func (b *MarketPriceBroadcaster) GetSubscriberCount(symbol string) int {
	return b.hub.MarketPriceService().GetSubscriberCount(symbol)
}

// GetAllSubscriptions returns all active price subscriptions.
func (b *MarketPriceBroadcaster) GetAllSubscriptions() map[string]int {
	return b.hub.MarketPriceService().GetAllSubscriptions()
}

// GetCachedPrice returns the cached price for a symbol.
func (b *MarketPriceBroadcaster) GetCachedPrice(symbol string) (*MarketPriceUpdatePayload, bool) {
	return b.hub.MarketPriceService().GetCachedPrice(symbol)
}
