package portfolio

import (
	"context"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
)

// GetPortfolioOverviewTool returns a unified snapshot covering spot wallet, futures
// wallet, open positions with risk metrics, account health, and bot decision context.
type GetPortfolioOverviewTool struct {
	client *gctclient.Client
}

// NewGetPortfolioOverviewTool creates a new GetPortfolioOverviewTool.
func NewGetPortfolioOverviewTool(client *gctclient.Client) *GetPortfolioOverviewTool {
	return &GetPortfolioOverviewTool{client: client}
}

func (t *GetPortfolioOverviewTool) Name() string {
	return "get_portfolio_overview"
}

func (t *GetPortfolioOverviewTool) Description() string {
	return "Get comprehensive portfolio overview: spot wallet, futures wallet, open positions " +
		"with risk metrics, account health, and bot decision context. Use this for any balance, " +
		"capital, or risk-related analysis."
}

func (t *GetPortfolioOverviewTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name (default: kucoin)",
				"default":     "kucoin",
			},
		},
	}
}

func (t *GetPortfolioOverviewTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange := "kucoin"
	if ex, ok := params["exchange"].(string); ok && ex != "" {
		exchange = ex
	}

	overview, err := t.client.GetPortfolioOverview(ctx, exchange)
	if err != nil {
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Failed to get portfolio overview: " + err.Error(),
			Data:    err.Error(),
		}
	}
	return overview, nil
}
