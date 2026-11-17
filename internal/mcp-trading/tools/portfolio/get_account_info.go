package portfolio

import (
	"context"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
)

// GetAccountInfoTool obtiene información completa de la cuenta
type GetAccountInfoTool struct {
	client *gctclient.Client
}

func NewGetAccountInfoTool(client *gctclient.Client) *GetAccountInfoTool {
	return &GetAccountInfoTool{client: client}
}

func (t *GetAccountInfoTool) Name() string {
	return "get_account_info"
}

func (t *GetAccountInfoTool) Description() string {
	return "Get complete account information including all balances, account type, permissions, and trading limits"
}

func (t *GetAccountInfoTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name (e.g., 'kucoin')",
			},
		},
		"required": []string{"exchange"},
	}
}

func (t *GetAccountInfoTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, ok := params["exchange"].(string)
	if !ok || exchange == "" {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "exchange is required",
		}
	}

	// Obtener portfolio que incluye información de cuenta
	portfolio, err := t.client.GetPortfolio(ctx)
	if err != nil {
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Failed to get account info",
			Data:    err.Error(),
		}
	}

	// Retornar información de cuenta con portfolio
	return map[string]interface{}{
		"exchange":        exchange,
		"total_value_usd": portfolio.TotalValueUSD,
		"holdings":        portfolio.Holdings,
		"pnl":             portfolio.PnL,
		"pnl_percent":     portfolio.PnLPercent,
		"timestamp":       portfolio.Timestamp,
	}, nil
}
