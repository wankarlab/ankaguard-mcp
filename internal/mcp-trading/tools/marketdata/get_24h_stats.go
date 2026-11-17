package marketdata

import (
	"context"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
)

// Get24hStatsTool obtiene estadísticas de 24 horas
type Get24hStatsTool struct {
	BaseTool
}

func NewGet24hStatsTool(client *gctclient.Client) *Get24hStatsTool {
	return &Get24hStatsTool{BaseTool: BaseTool{client: client}}
}

func (t *Get24hStatsTool) Name() string {
	return "get_24h_stats"
}

func (t *Get24hStatsTool) Description() string {
	return "Get 24-hour trading statistics including volume, price change, high, and low"
}

func (t *Get24hStatsTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{"type": "string", "description": "Exchange name"},
			"pair":     map[string]interface{}{"type": "string", "description": "Trading pair"},
		},
		"required": []string{"exchange", "pair"},
	}
}

func (t *Get24hStatsTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, pair, err := t.validateExchangeAndPair(params)
	if err != nil {
		return nil, err
	}

	// Use ticker data which includes 24h stats
	return executeWithContext(ctx, func(ctx context.Context) (interface{}, error) {
		ticker, err := t.client.GetTicker24h(ctx, exchange, pair)
		if err != nil {
			return nil, err
		}

		return map[string]interface{}{
			"exchange":   ticker.Exchange,
			"pair":       ticker.Pair,
			"volume_24h": ticker.Volume,
			"high_24h":   ticker.High24h,
			"low_24h":    ticker.Low24h,
			"change_24h": ticker.Change24h,
			"last_price": ticker.Last,
			"timestamp":  ticker.Timestamp,
		}, nil
	})
}
