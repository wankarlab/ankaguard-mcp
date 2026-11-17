package marketdata

import (
	"context"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
)

// GetTradesTool obtiene trades recientes
type GetTradesTool struct {
	BaseTool
}

func NewGetTradesTool(client *gctclient.Client) *GetTradesTool {
	return &GetTradesTool{BaseTool: BaseTool{client: client}}
}

func (t *GetTradesTool) Name() string {
	return "get_trades"
}

func (t *GetTradesTool) Description() string {
	return "Get recent trades executed on the exchange for a trading pair"
}

func (t *GetTradesTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{"type": "string", "description": "Exchange name"},
			"pair":     map[string]interface{}{"type": "string", "description": "Trading pair"},
			"limit":    map[string]interface{}{"type": "integer", "description": "Number of trades (default: 50)", "default": 50},
		},
		"required": []string{"exchange", "pair"},
	}
}

func (t *GetTradesTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, pair, err := t.validateExchangeAndPair(params)
	if err != nil {
		return nil, err
	}
	limit := getIntParam(params, "limit", 50)

	trades, err := t.client.GetRecentTrades(ctx, exchange, pair, limit)
	if err != nil {
		return map[string]interface{}{
			"exchange": exchange,
			"pair":     pair,
			"trades":   []interface{}{},
			"count":    0,
			"limit":    limit,
			"error":    err.Error(),
		}, nil
	}

	return map[string]interface{}{
		"exchange": exchange,
		"pair":     pair,
		"trades":   trades,
		"count":    len(trades),
		"limit":    limit,
	}, nil
}
