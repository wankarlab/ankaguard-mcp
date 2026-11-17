package marketdata

import (
	"context"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
)

// GetMarketTypesTool returns list of supported market types
type GetMarketTypesTool struct {
	BaseTool
}

func NewGetMarketTypesTool(client *gctclient.Client) *GetMarketTypesTool {
	return &GetMarketTypesTool{
		BaseTool: BaseTool{client: client},
	}
}

func (t *GetMarketTypesTool) Name() string {
	return "get_market_types"
}

func (t *GetMarketTypesTool) Description() string {
	return "Get list of supported market types (spot, futures, margin, options)"
}

func (t *GetMarketTypesTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

func (t *GetMarketTypesTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	return map[string]interface{}{
		"market_types": []string{"spot", "futures", "margin", "options"},
		"default":      "futures",
		"description": map[string]string{
			"spot":    "Spot trading - direct buy/sell without leverage",
			"futures": "Futures trading - contracts with leverage and expiration",
			"margin":  "Margin trading - trading with borrowed funds",
			"options": "Options trading - call/put contracts",
		},
	}, nil
}
