package marketdata

import (
	"context"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
)

// GetMarkPriceTool obtiene el precio mark de futuros
type GetMarkPriceTool struct {
	BaseTool
}

// NewGetMarkPriceTool crea una nueva instancia
func NewGetMarkPriceTool(client *gctclient.Client) *GetMarkPriceTool {
	return &GetMarkPriceTool{
		BaseTool: BaseTool{client: client},
	}
}

func (t *GetMarkPriceTool) Name() string {
	return "get_mark_price"
}

func (t *GetMarkPriceTool) Description() string {
	return "Get mark price for futures contracts. Mark price is used for liquidation calculations and unrealized PnL."
}

func (t *GetMarkPriceTool) InputSchema() map[string]interface{} {
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

func (t *GetMarkPriceTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, pair, err := t.validateExchangeAndPair(params)
	if err != nil {
		return nil, err
	}

	return executeWithContext(ctx, func(ctx context.Context) (interface{}, error) {
		// Obtener ticker que incluye mark price para futuros
		ticker, err := t.client.GetTicker(ctx, exchange, pair)
		if err != nil {
			return nil, err
		}

		// Para futuros, usar mark price si está disponible, sino usar last price
		markPrice := ticker.Last
		if ticker.MarkPrice > 0 {
			markPrice = ticker.MarkPrice
		}

		return map[string]interface{}{
			"exchange":   exchange,
			"pair":       pair,
			"mark_price": markPrice,
			"last_price": ticker.Last,
			"timestamp":  ticker.Timestamp,
		}, nil
	})
}
