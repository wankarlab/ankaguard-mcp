package marketdata

import (
	"context"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
)

// GetExchangeInfoTool obtiene información del exchange
type GetExchangeInfoTool struct {
	BaseTool
}

func NewGetExchangeInfoTool(client *gctclient.Client) *GetExchangeInfoTool {
	return &GetExchangeInfoTool{BaseTool: BaseTool{client: client}}
}

func (t *GetExchangeInfoTool) Name() string {
	return "get_exchange_info"
}

func (t *GetExchangeInfoTool) Description() string {
	return "Get exchange information including status, trading fees, and supported features"
}

func (t *GetExchangeInfoTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{"type": "string", "description": "Exchange name"},
		},
		"required": []string{"exchange"},
	}
}

func (t *GetExchangeInfoTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, ok := params["exchange"].(string)
	if !ok || exchange == "" {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "exchange is required",
		}
	}

	return map[string]interface{}{
		"exchange":      exchange,
		"status":        "online",
		"trading_fee":   0.001,
		"maker_fee":     0.001,
		"taker_fee":     0.001,
		"supports_spot": true,
		"note":          "Exchange info partially implemented",
	}, nil
}
