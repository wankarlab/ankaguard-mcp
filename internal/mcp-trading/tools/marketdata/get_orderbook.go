package marketdata

import (
	"context"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
)

// GetOrderbookTool obtiene el libro de órdenes
type GetOrderbookTool struct {
	BaseTool
}

func NewGetOrderbookTool(client *gctclient.Client) *GetOrderbookTool {
	return &GetOrderbookTool{
		BaseTool: BaseTool{client: client},
	}
}

func (t *GetOrderbookTool) Name() string {
	return "get_orderbook"
}

func (t *GetOrderbookTool) Description() string {
	return "Get orderbook data with bids and asks for a trading pair. Optionally specify depth to limit results."
}

func (t *GetOrderbookTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name",
			},
			"pair": map[string]interface{}{
				"type":        "string",
				"description": "Trading pair (e.g., 'BTC-USDT' for spot, 'BTCUSDTM' for futures, or 'BTC-USDT' with market_type)",
			},
			"depth": map[string]interface{}{
				"type":        "integer",
				"description": "Number of price levels to return (default: 20)",
				"default":     20,
			},
			"market_type": map[string]interface{}{
				"type":        "string",
				"description": "Market type: 'spot', 'futures', 'margin', or 'options' (optional, enables symbol conversion)",
				"enum":        []string{"spot", "futures", "margin", "options"},
			},
		},
		"required": []string{"exchange", "pair"},
	}
}

func (t *GetOrderbookTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, pair, err := t.validateExchangeAndPair(params)
	if err != nil {
		return nil, err
	}

	depth := getIntParam(params, "depth", 20)

	// Handle optional market_type parameter for symbol conversion
	if marketType, ok := params["market_type"].(string); ok && marketType != "" {
		// Validate market_type
		if err := t.validateMarketType(marketType); err != nil {
			return nil, err
		}

		// Convert symbol using market_type
		convertedPair, err := t.convertSymbol(pair, marketType, exchange)
		if err != nil {
			return nil, err
		}
		pair = convertedPair
	}

	return executeWithContext(ctx, func(ctx context.Context) (interface{}, error) {
		return t.client.GetOrderbook(ctx, exchange, pair, depth)
	})
}
