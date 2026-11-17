package websocket

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// MarketPriceService manages market price subscriptions and updates.
type MarketPriceService struct {
	hub    *Hub
	logger *slog.Logger

	// subscriptions maps symbols to sets of subscribed client IDs.
	subscriptions map[string]map[string]bool

	// priceCache stores the latest price for each symbol.
	priceCache map[string]*MarketPriceUpdatePayload

	// mu protects concurrent access to subscriptions and cache.
	mu sync.RWMutex

	// updateChan receives price updates from external sources.
	updateChan chan *MarketPriceUpdatePayload

	// done signals when the service should stop.
	done chan struct{}
}

// NewMarketPriceService creates a new market price service.
func NewMarketPriceService(hub *Hub, logger *slog.Logger) *MarketPriceService {
	if logger == nil {
		logger = slog.Default()
	}

	return &MarketPriceService{
		hub:           hub,
		logger:        logger.With("component", "market_price_service"),
		subscriptions: make(map[string]map[string]bool),
		priceCache:    make(map[string]*MarketPriceUpdatePayload),
		updateChan:    make(chan *MarketPriceUpdatePayload, 1000),
		done:          make(chan struct{}),
	}
}

// Run starts the market price service.
func (s *MarketPriceService) Run(ctx context.Context) {
	s.logger.Info("market price service started")

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("market price service stopping")
			return

		case <-s.done:
			s.logger.Info("market price service stopped")
			return

		case update := <-s.updateChan:
			s.handlePriceUpdate(update)
		}
	}
}

// Stop stops the market price service.
func (s *MarketPriceService) Stop() {
	close(s.done)
}

// SubscribeToSymbol subscribes a client to price updates for a symbol.
func (s *MarketPriceService) SubscribeToSymbol(clientID, symbol string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.subscriptions[symbol] == nil {
		s.subscriptions[symbol] = make(map[string]bool)
	}

	s.subscriptions[symbol][clientID] = true

	s.logger.Debug("client subscribed to symbol",
		"client_id", clientID,
		"symbol", symbol,
	)

	// Send cached price if available.
	if price, ok := s.priceCache[symbol]; ok {
		s.sendPriceToClient(clientID, price)
	} else {
		// If no cached price, log that we're waiting for first update
		s.logger.Debug("no cached price for symbol, waiting for first update",
			"client_id", clientID,
			"symbol", symbol,
		)
	}
}

// UnsubscribeFromSymbol unsubscribes a client from price updates for a symbol.
func (s *MarketPriceService) UnsubscribeFromSymbol(clientID, symbol string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if subscribers, ok := s.subscriptions[symbol]; ok {
		delete(subscribers, clientID)

		// Clean up empty subscription sets.
		if len(subscribers) == 0 {
			delete(s.subscriptions, symbol)
		}
	}

	s.logger.Debug("client unsubscribed from symbol",
		"client_id", clientID,
		"symbol", symbol,
	)
}

// UnsubscribeClient removes all subscriptions for a client.
func (s *MarketPriceService) UnsubscribeClient(clientID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for symbol, subscribers := range s.subscriptions {
		delete(subscribers, clientID)

		// Clean up empty subscription sets.
		if len(subscribers) == 0 {
			delete(s.subscriptions, symbol)
		}
	}

	s.logger.Debug("client unsubscribed from all symbols", "client_id", clientID)
}

// GetSnapshot returns a snapshot of current prices for the given symbols.
func (s *MarketPriceService) GetSnapshot(symbols []string) []MarketPriceData {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot := make([]MarketPriceData, 0, len(symbols))

	for _, symbol := range symbols {
		if price, ok := s.priceCache[symbol]; ok {
			snapshot = append(snapshot, MarketPriceData{
				Symbol:     price.Symbol,
				Price:      price.Price,
				LastUpdate: price.LastUpdate,
				Source:     price.Source,
			})
		}
	}

	return snapshot
}

// UpdatePrice updates the price for a symbol and broadcasts to subscribers.
func (s *MarketPriceService) UpdatePrice(update *MarketPriceUpdatePayload) {
	select {
	case s.updateChan <- update:
	default:
		s.logger.Warn("update channel full, dropping price update", "symbol", update.Symbol)
	}
}

// handlePriceUpdate processes a price update and broadcasts to subscribers.
func (s *MarketPriceService) handlePriceUpdate(update *MarketPriceUpdatePayload) {
	// Update cache.
	s.mu.Lock()
	s.priceCache[update.Symbol] = update
	subscribers := s.getSubscribers(update.Symbol)
	s.mu.Unlock()

	// Broadcast to subscribers.
	if len(subscribers) > 0 {
		message := NewMarketPriceUpdateMessage(update)
		s.broadcastToClients(subscribers, message)

		s.logger.Debug("price update broadcast",
			"symbol", update.Symbol,
			"price", update.Price,
			"subscribers", len(subscribers),
		)
	}
}

// getSubscribers returns a copy of the subscriber list for a symbol.
func (s *MarketPriceService) getSubscribers(symbol string) []string {
	subscribers := make([]string, 0)

	if clientSet, ok := s.subscriptions[symbol]; ok {
		for clientID := range clientSet {
			subscribers = append(subscribers, clientID)
		}
	}

	return subscribers
}

// broadcastToClients sends a message to specific clients.
func (s *MarketPriceService) broadcastToClients(clientIDs []string, message *WSMessage) {
	data, err := message.Serialize()
	if err != nil {
		s.logger.Error("failed to serialize price update", "error", err)
		return
	}

	for _, clientID := range clientIDs {
		client := s.hub.GetClient(clientID)
		if client != nil {
			client.SendBytes(data)
		}
	}
}

// sendPriceToClient sends a price update to a specific client.
func (s *MarketPriceService) sendPriceToClient(clientID string, price *MarketPriceUpdatePayload) {
	client := s.hub.GetClient(clientID)
	if client != nil {
		message := NewMarketPriceUpdateMessage(price)
		client.SendMessage(message)
	}
}

// GetSubscriberCount returns the number of subscribers for a symbol.
func (s *MarketPriceService) GetSubscriberCount(symbol string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if subscribers, ok := s.subscriptions[symbol]; ok {
		return len(subscribers)
	}

	return 0
}

// GetAllSubscriptions returns all active subscriptions.
func (s *MarketPriceService) GetAllSubscriptions() map[string]int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]int)
	for symbol, subscribers := range s.subscriptions {
		result[symbol] = len(subscribers)
	}

	return result
}

// GetCachedPrice returns the cached price for a symbol.
func (s *MarketPriceService) GetCachedPrice(symbol string) (*MarketPriceUpdatePayload, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	price, ok := s.priceCache[symbol]
	return price, ok
}

// SimulatePriceUpdate simulates a price update for testing.
// This should be replaced with real exchange integration.
func (s *MarketPriceService) SimulatePriceUpdate(symbol string, price float64, source string) {
	update := &MarketPriceUpdatePayload{
		Symbol:     symbol,
		Price:      price,
		LastUpdate: time.Now().UTC().Format(time.RFC3339),
		Source:     source,
	}

	s.UpdatePrice(update)
}
