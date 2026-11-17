package marketdata

import (
	"context"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
)

// GetIndexPriceTool obtiene el precio índice de futuros
type GetIndexPriceTool struct {
	BaseTool
}

// NewGetIndexPriceTool crea una nueva instancia
func NewGetIndexPriceTool(client *gctclient.Client) *GetIndexPriceTool {
	return &GetIndexPriceTool{
		BaseTool: BaseTool{client: client},
	}
}

func (t *GetIndexPriceTool) Name() string {
	return "get_index_price"
}

func (t *GetIndexPriceTool) Description() string {
	return "Get index price for futures contracts. Index price is the weighted average price from multiple spot exchanges."
}

func (t *GetIndexPriceTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name (e.g., 'kucoin')",
			},
			"pair": map[string]interface{}{
				"type":        "string",
				"description": "Futures trading pair (e.g., 'BTC-USDT', 'DOGE-USDT')",
			},
		},
		"required": []string{"exchange", "pair"},
	}
}

func (t *GetIndexPriceTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, pair, err := t.validateExchangeAndPair(params)
	if err != nil {
		return nil, err
	}

	return executeWithContext(ctx, func(ctx context.Context) (interface{}, error) {
		// Obtener ticker que incluye index price para futuros
		ticker, err := t.client.GetTicker(ctx, exchange, pair)
		if err != nil {
			return nil, err
		}

		// Para futuros, usar index price si está disponible, sino usar last price
		indexPrice := ticker.Last
		if ticker.IndexPrice > 0 {
			indexPrice = ticker.IndexPrice
		}

		return map[string]interface{}{
			"exchange":    exchange,
			"pair":        pair,
			"index_price": indexPrice,
			"last_price":  ticker.Last,
			"timestamp":   ticker.Timestamp,
		}, nil
	})
}
