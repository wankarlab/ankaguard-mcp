package marketdata

import (
	"context"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
)

// GetCandlesTool obtiene datos OHLCV históricos
type GetCandlesTool struct {
	BaseTool
}

func NewGetCandlesTool(client *gctclient.Client) *GetCandlesTool {
	return &GetCandlesTool{
		BaseTool: BaseTool{client: client},
	}
}

func (t *GetCandlesTool) Name() string {
	return "get_candles"
}

func (t *GetCandlesTool) Description() string {
	return "Get historical OHLCV (Open, High, Low, Close, Volume) candle data for technical analysis"
}

func (t *GetCandlesTool) InputSchema() map[string]interface{} {
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
			"interval": map[string]interface{}{
				"type":        "string",
				"description": "Candle interval (e.g., '1m', '5m', '1h', '1d')",
				"default":     "1h",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Number of candles to return (default: 100)",
				"default":     100,
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

func (t *GetCandlesTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, pair, err := t.validateExchangeAndPair(params)
	if err != nil {
		return nil, err
	}

	interval := getStringParam(params, "interval", "1h")
	limit := getIntParam(params, "limit", 100)

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
		return t.client.GetCandles(ctx, exchange, pair, interval, limit)
	})
}
