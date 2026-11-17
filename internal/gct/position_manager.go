package gct

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// PositionCache tracks and manages open futures positions with caching
type PositionCache struct {
	client   *KuCoinRESTClient
	cache    map[string]*Position
	cacheMux sync.RWMutex
	logger   *logrus.Logger
	stopChan chan struct{}
}

// NewPositionCache creates a new position manager
func NewPositionCache(client *KuCoinRESTClient, logger *logrus.Logger) (*PositionCache, error) {
	if client == nil {
		return nil, fmt.Errorf("kucoin client is nil")
	}
	if logger == nil {
		return nil, fmt.Errorf("logger is nil")
	}

	pm := &PositionCache{
		client:   client,
		cache:    make(map[string]*Position),
		logger:   logger,
		stopChan: make(chan struct{}),
	}

	// Start background cache refresh
	go pm.refreshLoop()

	return pm, nil
}

// GetPositions fetches all positions from KuCoin API
func (pm *PositionCache) GetPositions(ctx context.Context) ([]*Position, error) {
	if pm == nil {
		return nil, fmt.Errorf("position manager is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	pm.logger.Debug("Fetching all positions from KuCoin API")

	rawPositions, err := pm.client.GetPositions(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get positions from API: %w", err)
	}

	if rawPositions == nil {
		pm.logger.Debug("No positions returned from API")
		return []*Position{}, nil
	}

	positions := make([]*Position, 0, len(rawPositions))

	for _, raw := range rawPositions {
		if raw == nil {
			continue
		}

		position, err := pm.parsePosition(raw)
		if err != nil {
			pm.logger.Warnf("Failed to parse position: %v", err)
			// Recover the symbol from raw so the caller knows this pair
			// has an unreadable position and must not open a new one.
			if sym, ok := raw["symbol"].(string); ok && sym != "" {
				positions = append(positions, &Position{
					Symbol:    sym,
					Side:      "unknown",
					Size:      1, // non-zero so Size > 0 guard passes
					Leverage:  1,
					UpdatedAt: time.Now(),
				})
				pm.logger.Warnf("Injecting sentinel position for %s to block new orders", sym)
			}
			continue
		}

		if position != nil && position.Size > 0 {
			positions = append(positions, position)

			// Update cache
			pm.cacheMux.Lock()
			pm.cache[position.Symbol] = position
			pm.cacheMux.Unlock()
		}
	}

	pm.logger.Infof("Found %d open positions", len(positions))
	return positions, nil
}

// GetPosition fetches a specific position
func (pm *PositionCache) GetPosition(ctx context.Context, symbol string) (*Position, error) {
	if pm == nil {
		return nil, fmt.Errorf("position manager is nil")
	}
	if symbol == "" {
		return nil, fmt.Errorf("symbol is empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Try cache first
	pm.cacheMux.RLock()
	if cached, ok := pm.cache[symbol]; ok {
		if time.Since(cached.UpdatedAt) < 5*time.Second {
			pm.cacheMux.RUnlock()
			pm.logger.Debugf("Position cache hit for %s", symbol)
			return cached, nil
		}
	}
	pm.cacheMux.RUnlock()

	// Fetch from API
	pm.logger.Debugf("Fetching position for %s from API", symbol)
	raw, err := pm.client.GetPosition(ctx, symbol)
	if err != nil {
		if err == ErrPositionNotFound {
			return nil, err
		}
		return nil, fmt.Errorf("failed to get position from API: %w", err)
	}

	if raw == nil {
		return nil, ErrPositionNotFound
	}

	position, err := pm.parsePosition(raw)
	if err != nil {
		return nil, fmt.Errorf("failed to parse position: %w", err)
	}

	if position == nil {
		return nil, ErrPositionNotFound
	}

	// Update cache
	pm.cacheMux.Lock()
	pm.cache[symbol] = position
	pm.cacheMux.Unlock()

	return position, nil
}

// parsePosition parses KuCoin position response into Position struct
func (pm *PositionCache) parsePosition(raw map[string]interface{}) (*Position, error) {
	if pm == nil {
		return nil, fmt.Errorf("position manager is nil")
	}
	if raw == nil {
		return nil, fmt.Errorf("raw position data is nil")
	}

	// Extract symbol
	symbol, ok := raw["symbol"].(string)
	if !ok || symbol == "" {
		return nil, fmt.Errorf("invalid or missing symbol")
	}

	// Extract currentQty
	currentQty, ok := raw["currentQty"].(float64)
	if !ok {
		return nil, fmt.Errorf("invalid or missing currentQty for %s", symbol)
	}

	// No position if qty is 0
	if currentQty == 0 {
		return nil, nil
	}

	// Determine side based on qty sign
	side := "long"
	size := currentQty
	if currentQty < 0 {
		side = "short"
		size = -currentQty
	}

	// Extract margin mode
	crossMode, ok := raw["crossMode"].(bool)
	if !ok {
		pm.logger.Warnf("Missing crossMode for %s, defaulting to ISOLATED", symbol)
		crossMode = false
	}

	marginMode := "ISOLATED"
	if crossMode {
		marginMode = "CROSSED"
	}

	// Extract other fields with safe type assertions
	position := &Position{
		Symbol:     symbol,
		Side:       side,
		Size:       size,
		MarginMode: marginMode,
		UpdatedAt:  time.Now(),
	}

	// Entry price
	if avgEntryPrice, ok := raw["avgEntryPrice"].(float64); ok {
		position.EntryPrice = avgEntryPrice
	}

	// Current/mark price
	if markPrice, ok := raw["markPrice"].(float64); ok {
		position.CurrentPrice = markPrice
	}

	// Unrealized PnL
	if unrealisedPnl, ok := raw["unrealisedPnl"].(float64); ok {
		position.UnrealizedPnL = unrealisedPnl
	}

	// Realized PnL
	if realisedPnl, ok := raw["realisedPnl"].(float64); ok {
		position.RealizedPnL = realisedPnl
	}

	// Leverage — KuCoin sometimes returns 0.999... which truncates to 0.
	// Round to nearest integer and default to 1 if still zero or missing.
	if realLeverage, ok := raw["realLeverage"].(float64); ok {
		position.Leverage = int(math.Round(realLeverage))
	}
	if position.Leverage < 1 {
		position.Leverage = 1
	}

	// Liquidation price
	if liquidationPrice, ok := raw["liquidationPrice"].(float64); ok {
		position.LiquidationPrice = liquidationPrice
	}

	// Margin/cost
	if currentCost, ok := raw["currentCost"].(float64); ok {
		position.Margin = currentCost
	}

	// Validate position
	if !position.IsValid() {
		return nil, fmt.Errorf("parsed position is invalid for %s", symbol)
	}

	return position, nil
}

// CalculatePnL calculates PnL for a position given current price
func (pm *PositionCache) CalculatePnL(position *Position, currentPrice float64) float64 {
	if pm == nil || position == nil {
		return 0
	}
	if !position.IsValid() {
		return 0
	}
	if currentPrice <= 0 {
		return 0
	}

	priceDiff := currentPrice - position.EntryPrice

	// For short positions, profit when price goes down
	if position.Side == "short" {
		priceDiff = -priceDiff
	}

	return priceDiff * position.Size
}

// RefreshCache updates the position cache from API
func (pm *PositionCache) RefreshCache(ctx context.Context) error {
	if pm == nil {
		return fmt.Errorf("position manager is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	pm.logger.Debug("Refreshing position cache")

	_, err := pm.GetPositions(ctx)
	if err != nil {
		return fmt.Errorf("failed to refresh cache: %w", err)
	}

	return nil
}

// refreshLoop runs background cache refresh every 30 seconds.
// 30s interval gives the KuCoin REST API enough breathing room between calls;
// 20s context gives doRequestWithRetry time for one full attempt + one retry.
func (pm *PositionCache) refreshLoop() {
	if pm == nil {
		return
	}

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-pm.stopChan:
			pm.logger.Debug("Position manager refresh loop stopped")
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			if err := pm.RefreshCache(ctx); err != nil {
				pm.logger.Warnf("Failed to refresh position cache: %v", err)
			}
			cancel()
		}
	}
}

// Stop stops the background refresh loop
func (pm *PositionCache) Stop() {
	if pm == nil {
		return
	}

	close(pm.stopChan)
	pm.logger.Info("Position manager stopped")
}

// ClearCache clears the position cache
func (pm *PositionCache) ClearCache() {
	if pm == nil {
		return
	}

	pm.cacheMux.Lock()
	defer pm.cacheMux.Unlock()

	pm.cache = make(map[string]*Position)
	pm.logger.Debug("Position cache cleared")
}

// ClearCacheForSymbol clears cache for a specific symbol
func (pm *PositionCache) ClearCacheForSymbol(symbol string) {
	if pm == nil || symbol == "" {
		return
	}

	pm.cacheMux.Lock()
	defer pm.cacheMux.Unlock()

	delete(pm.cache, symbol)
	pm.logger.Debugf("Cache cleared for %s", symbol)
}
