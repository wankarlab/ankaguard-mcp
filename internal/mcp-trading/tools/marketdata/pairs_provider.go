package marketdata

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/tools/common"
	"github.com/sirupsen/logrus"
)

// PairInfo represents information about a trading pair
type PairInfo struct {
	Symbol         string `json:"symbol"`
	StandardSymbol string `json:"standard_symbol,omitempty"`
	Base           string `json:"base"`
	Quote          string `json:"quote"`
	MarketType     string `json:"market_type"`
}

// PairsResult contains the result of a pairs query
type PairsResult struct {
	Pairs      []PairInfo
	DataSource string // always "gct": there is no fallback list
	Cached     bool
	Timestamp  time.Time
}

// PairsProvider handles retrieval of trading pairs with fallback logic
type PairsProvider struct {
	client *gctclient.Client
	cache  *PairsCache
	logger *logrus.Logger
}

// NewPairsProvider creates a new pairs provider
func NewPairsProvider(client *gctclient.Client, cache *PairsCache, logger *logrus.Logger) *PairsProvider {
	return &PairsProvider{
		client: client,
		cache:  cache,
		logger: logger,
	}
}

// GetPairs retrieves all trading pairs for an exchange (all market types)
func (p *PairsProvider) GetPairs(ctx context.Context, exchange string) (*PairsResult, error) {
	return p.getPairsInternal(ctx, exchange, nil)
}

// GetPairsByType retrieves trading pairs filtered by market type
func (p *PairsProvider) GetPairsByType(ctx context.Context, exchange string, marketType string) (*PairsResult, error) {
	return p.getPairsInternal(ctx, exchange, &marketType)
}

// getPairsInternal is the internal implementation for getting pairs
func (p *PairsProvider) getPairsInternal(ctx context.Context, exchange string, marketType *string) (*PairsResult, error) {
	// Generate cache key
	cacheKey := GenerateCacheKey(exchange, marketType)

	// Check cache first
	if cached, found := p.cache.Get(cacheKey); found {
		if p.logger != nil {
			p.logger.WithFields(logrus.Fields{
				"exchange":    exchange,
				"market_type": marketType,
				"cache_key":   cacheKey,
			}).Debug("Pairs cache hit")
		}
		cached.Cached = true
		return cached, nil
	}

	// Try to get from GCT first
	result, err := p.tryGetFromGCT(ctx, exchange, marketType)
	if err == nil {
		// Success - cache and return
		result.Cached = false
		p.cache.Set(cacheKey, result, 5*time.Minute)

		if p.logger != nil {
			p.logger.WithFields(logrus.Fields{
				"exchange":    exchange,
				"market_type": marketType,
				"pairs_count": len(result.Pairs),
			}).Info("Successfully retrieved pairs from GCT")
		}

		return result, nil
	}

	// GCT failed: report it. A made-up pair list would look like real exchange data.
	if p.logger != nil {
		p.logger.WithFields(logrus.Fields{
			"exchange":    exchange,
			"market_type": marketType,
			"error":       err.Error(),
		}).Warn("Failed to get pairs from GCT")
	}
	return nil, common.DataUnavailable("could not get the pair list from the exchange: "+err.Error(), map[string]interface{}{"exchange": exchange})
}

// tryGetFromGCT attempts to retrieve pairs from GoCryptoTrader
func (p *PairsProvider) tryGetFromGCT(ctx context.Context, exchange string, marketType *string) (*PairsResult, error) {
	// Check if client is connected
	if p.client == nil || !p.client.IsConnected() {
		return nil, fmt.Errorf("GCT client not connected")
	}

	// Try to get exchange info which contains supported pairs
	exchangeInfo, err := p.client.GetExchangeInfo(ctx, exchange)
	if err != nil {
		return nil, fmt.Errorf("failed to get exchange info: %w", err)
	}

	// Extract supported assets from exchange info
	supportedAssets, ok := exchangeInfo["supported_assets"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("no supported assets found in exchange info")
	}

	pairs := make([]PairInfo, 0)

	// Process each asset type (spot, futures, margin, etc.)
	for assetType, assetData := range supportedAssets {
		// Filter by market type if specified
		if marketType != nil && *marketType != "" && assetType != *marketType {
			continue
		}

		assetInfo, ok := assetData.(map[string]interface{})
		if !ok {
			continue
		}

		// Get enabled pairs for this asset type
		enabledPairs, ok := assetInfo["enabled_pairs"].(string)
		if !ok || enabledPairs == "" {
			continue
		}

		// Parse the pairs string (format: "BTC-USDT,ETH-USDT,...")
		pairList := strings.Split(enabledPairs, ",")
		for _, pairStr := range pairList {
			pairStr = strings.TrimSpace(pairStr)
			if pairStr == "" {
				continue
			}

			// Parse pair to get base and quote
			base := ""
			quote := ""
			if idx := strings.Index(pairStr, "-"); idx > 0 {
				base = pairStr[:idx]
				quote = pairStr[idx+1:]
			}

			pairs = append(pairs, PairInfo{
				Symbol:     pairStr,
				Base:       base,
				Quote:      quote,
				MarketType: assetType,
			})
		}
	}

	if len(pairs) == 0 {
		return nil, fmt.Errorf("no pairs found for exchange %s", exchange)
	}

	return &PairsResult{
		Pairs:      pairs,
		DataSource: "gct",
		Cached:     false,
		Timestamp:  time.Now(),
	}, nil
}

// GetMarketTypesCount returns the count of pairs per market type, or nil when
// the exchange pair list is not available.
func (p *PairsProvider) GetMarketTypesCount(exchange string) map[string]int {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := p.GetPairs(ctx, exchange)
	if err != nil {
		return nil
	}
	counts := make(map[string]int)
	for _, pair := range result.Pairs {
		counts[pair.MarketType]++
	}
	return counts
}
