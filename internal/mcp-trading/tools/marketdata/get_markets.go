package marketdata

import (
	"context"
	"time"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/tools/common"
)

// GetMarketsTool retrieves available trading pairs with optional market type filtering
type GetMarketsTool struct {
	BaseTool
	pairsProvider *PairsProvider
}

func NewGetMarketsTool(client *gctclient.Client, pairsProvider *PairsProvider) *GetMarketsTool {
	return &GetMarketsTool{
		BaseTool:      BaseTool{client: client},
		pairsProvider: pairsProvider,
	}
}

func (t *GetMarketsTool) Name() string {
	return "get_markets"
}

func (t *GetMarketsTool) Description() string {
	return "Get list of available trading pairs on an exchange. Supports optional filtering by market type (spot, futures, margin, options). Returns detailed pair information with market features."
}

func (t *GetMarketsTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name (e.g., 'kucoin')",
			},
			"market_type": map[string]interface{}{
				"type":        "string",
				"description": "Optional: Filter by market type (spot, futures, margin, options)",
				"enum":        []string{"spot", "futures", "margin", "options"},
			},
		},
		"required": []string{"exchange"},
	}
}

func (t *GetMarketsTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	// Validate exchange parameter
	exchange, ok := params["exchange"].(string)
	if !ok || exchange == "" {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "exchange is required and must be a string",
		}
	}

	// Get optional market_type parameter
	var marketType *string
	if mt, ok := params["market_type"].(string); ok && mt != "" {
		// Validate market_type
		if err := t.validateMarketType(mt); err != nil {
			return nil, err
		}
		marketType = &mt
	}

	// Get pairs from provider
	var result *PairsResult
	var err error

	if marketType != nil {
		result, err = t.pairsProvider.GetPairsByType(ctx, exchange, *marketType)
	} else {
		result, err = t.pairsProvider.GetPairs(ctx, exchange)
	}

	if err != nil {
		if common.IsDataUnavailable(err) {
			return nil, err
		}
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Internal error",
			Data:    err.Error(),
		}
	}

	// Build response
	response := map[string]interface{}{
		"exchange":    exchange,
		"pairs":       result.Pairs,
		"total_count": len(result.Pairs),
		"data_source": result.DataSource,
		"cached":      result.Cached,
		"timestamp":   result.Timestamp.Format(time.RFC3339),
		"version":     "2.0",
	}

	// Add market_type to response if filtered
	if marketType != nil {
		response["market_type"] = *marketType

		// Add features for the specific market type
		features := GetMarketFeatures(*marketType)
		response["features"] = features
	} else {
		response["market_type"] = nil
	}

	// Add market types count
	marketTypesCount := t.pairsProvider.GetMarketTypesCount(exchange)
	response["market_types_count"] = marketTypesCount

	return response, nil
}
