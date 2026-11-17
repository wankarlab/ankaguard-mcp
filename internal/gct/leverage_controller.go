package gct

import (
	"context"
	"fmt"
	"sync"

	"github.com/sirupsen/logrus"
)

// LeverageController manages leverage settings per symbol with caching
type LeverageController struct {
	client      *KuCoinRESTClient
	cache       map[string]int    // symbol -> leverage
	marginCache map[string]string // symbol -> margin_mode
	cacheMux    sync.RWMutex
	logger      *logrus.Logger
}

// NewLeverageController creates a new leverage controller
func NewLeverageController(client *KuCoinRESTClient, logger *logrus.Logger) (*LeverageController, error) {
	if client == nil {
		return nil, fmt.Errorf("kucoin client is nil")
	}
	if logger == nil {
		return nil, fmt.Errorf("logger is nil")
	}

	return &LeverageController{
		client:      client,
		cache:       make(map[string]int),
		marginCache: make(map[string]string),
		logger:      logger,
	}, nil
}

// GetMarginMode retrieves margin mode for a symbol
func (lc *LeverageController) GetMarginMode(ctx context.Context, symbol string) (string, error) {
	if lc == nil {
		return "", fmt.Errorf("leverage controller is nil")
	}
	if symbol == "" {
		return "", fmt.Errorf("symbol is empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Check cache first
	lc.cacheMux.RLock()
	if mode, ok := lc.marginCache[symbol]; ok {
		lc.cacheMux.RUnlock()
		lc.logger.Debugf("Margin mode cache hit for %s: %s", symbol, mode)
		return mode, nil
	}
	lc.cacheMux.RUnlock()

	// Fetch from API via position
	lc.logger.Debugf("Fetching margin mode for %s from API", symbol)
	raw, err := lc.client.GetPosition(ctx, symbol)
	if err != nil {
		// Position might not exist yet, return empty string (not an error)
		lc.logger.Debugf("Could not get position for %s: %v", symbol, err)
		return "", nil
	}

	if raw == nil {
		lc.logger.Debugf("No position data for %s", symbol)
		return "", nil
	}

	// Parse margin mode
	crossMode, ok := raw["crossMode"].(bool)
	if !ok {
		lc.logger.Warnf("Could not parse crossMode for %s", symbol)
		return "", nil
	}

	mode := "ISOLATED"
	if crossMode {
		mode = "CROSSED"
	}

	// Update cache
	lc.cacheMux.Lock()
	lc.marginCache[symbol] = mode
	lc.cacheMux.Unlock()

	lc.logger.Infof("Margin mode for %s: %s", symbol, mode)
	return mode, nil
}

// SetLeverage sets leverage for a symbol and updates cache
func (lc *LeverageController) SetLeverage(ctx context.Context, symbol string, leverage int) error {
	if lc == nil {
		return fmt.Errorf("leverage controller is nil")
	}
	if symbol == "" {
		return fmt.Errorf("symbol is empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Validate leverage
	if err := lc.ValidateLeverage(symbol, leverage); err != nil {
		return fmt.Errorf("leverage validation failed: %w", err)
	}

	lc.logger.Infof("Setting leverage for %s to %dx", symbol, leverage)

	// Set leverage via API
	if err := lc.client.SetLeverage(ctx, symbol, leverage); err != nil {
		return fmt.Errorf("failed to set leverage: %w", err)
	}

	// Update cache
	lc.cacheMux.Lock()
	lc.cache[symbol] = leverage
	lc.cacheMux.Unlock()

	lc.logger.Infof("✓ Leverage set to %dx for %s", leverage, symbol)
	return nil
}

// GetLeverage retrieves cached leverage or fetches from API
func (lc *LeverageController) GetLeverage(ctx context.Context, symbol string) (int, error) {
	if lc == nil {
		return 0, fmt.Errorf("leverage controller is nil")
	}
	if symbol == "" {
		return 0, fmt.Errorf("symbol is empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Check cache
	lc.cacheMux.RLock()
	if leverage, ok := lc.cache[symbol]; ok {
		lc.cacheMux.RUnlock()
		return leverage, nil
	}
	lc.cacheMux.RUnlock()

	// Fetch from API via position
	raw, err := lc.client.GetPosition(ctx, symbol)
	if err != nil {
		return 0, fmt.Errorf("failed to get position: %w", err)
	}

	if raw == nil {
		return 0, fmt.Errorf("no position data for %s", symbol)
	}

	// Parse leverage
	realLeverage, ok := raw["realLeverage"].(float64)
	if !ok {
		return 0, fmt.Errorf("could not parse realLeverage for %s", symbol)
	}

	leverage := int(realLeverage)

	// Update cache
	lc.cacheMux.Lock()
	lc.cache[symbol] = leverage
	lc.cacheMux.Unlock()

	return leverage, nil
}

// ValidateLeverage checks if leverage is within allowed range
func (lc *LeverageController) ValidateLeverage(symbol string, leverage int) error {
	if lc == nil {
		return fmt.Errorf("leverage controller is nil")
	}
	if symbol == "" {
		return fmt.Errorf("symbol is empty")
	}

	return ValidateLeverageValue(leverage)
}

// ClearCache clears the leverage and margin mode cache
func (lc *LeverageController) ClearCache() {
	if lc == nil {
		return
	}

	lc.cacheMux.Lock()
	defer lc.cacheMux.Unlock()

	lc.cache = make(map[string]int)
	lc.marginCache = make(map[string]string)
	lc.logger.Debug("Leverage cache cleared")
}

// ClearCacheForSymbol clears cache for a specific symbol
func (lc *LeverageController) ClearCacheForSymbol(symbol string) {
	if lc == nil || symbol == "" {
		return
	}

	lc.cacheMux.Lock()
	defer lc.cacheMux.Unlock()

	delete(lc.cache, symbol)
	delete(lc.marginCache, symbol)
	lc.logger.Debugf("Cache cleared for %s", symbol)
}

// SetMarginMode sets the margin mode for a symbol
// mode: "ISOLATED" or "CROSSED"
// Note: This can only be set when there's no open position for the symbol
func (lc *LeverageController) SetMarginMode(ctx context.Context, symbol string, mode string) error {
	if lc == nil {
		return fmt.Errorf("leverage controller is nil")
	}
	if symbol == "" {
		return fmt.Errorf("symbol is empty")
	}
	if mode != "ISOLATED" && mode != "CROSSED" && mode != "CROSS" {
		return fmt.Errorf("invalid margin mode: %s (must be ISOLATED or CROSSED)", mode)
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Normalize mode name (CROSS -> CROSSED)
	if mode == "CROSS" {
		mode = "CROSSED"
	}

	lc.logger.Debugf("Setting margin mode for %s to %s", symbol, mode)

	// Call KuCoin API
	err := lc.client.SetMarginMode(ctx, symbol, mode)
	if err != nil {
		return fmt.Errorf("failed to set margin mode: %w", err)
	}

	// Update cache
	lc.cacheMux.Lock()
	lc.marginCache[symbol] = mode
	lc.cacheMux.Unlock()

	lc.logger.Infof("✓ Margin mode set to %s for %s", mode, symbol)
	return nil
}

// ClearMarginCache clears the margin mode cache for a symbol
func (lc *LeverageController) ClearMarginCache(symbol string) {
	if lc == nil {
		return
	}

	lc.cacheMux.Lock()
	delete(lc.marginCache, symbol)
	lc.cacheMux.Unlock()

	lc.logger.Debugf("Cleared margin mode cache for %s", symbol)
}
