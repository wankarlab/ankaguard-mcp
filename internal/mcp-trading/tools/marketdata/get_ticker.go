package marketdata

import (
	"context"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
)

// GetTickerTool obtiene el precio actual de un par
type GetTickerTool struct {
	BaseTool
}

// NewGetTickerTool crea una nueva instancia
func NewGetTickerTool(client *gctclient.Client) *GetTickerTool {
	return &GetTickerTool{
		BaseTool: BaseTool{client: client},
	}
}

func (t *GetTickerTool) Name() string {
	return "get_ticker"
}

func (t *GetTickerTool) Description() string {
	return "Get current ticker data including last price, bid, ask, volume, and 24h statistics for a trading pair"
}

func (t *GetTickerTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name (e.g., 'kucoin', 'binance')",
			},
			"pair": map[string]interface{}{
				"type":        "string",
				"description": "Trading pair (e.g., 'BTC-USDT' for spot, 'BTCUSDTM' for futures, or 'BTC-USDT' with market_type)",
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

func (t *GetTickerTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, pair, err := t.validateExchangeAndPair(params)
	if err != nil {
		return nil, err
	}

	// NEW: Handle optional market_type parameter for symbol conversion
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
		return t.client.GetTicker(ctx, exchange, pair)
	})
}
