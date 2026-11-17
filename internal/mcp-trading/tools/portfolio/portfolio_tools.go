package portfolio

import (
	"context"
	"fmt"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
)

// GetPortfolioTool obtiene el portfolio completo
type GetPortfolioTool struct {
	client *gctclient.Client
}

func NewGetPortfolioTool(client *gctclient.Client) *GetPortfolioTool {
	return &GetPortfolioTool{client: client}
}

func (t *GetPortfolioTool) Name() string {
	return "get_portfolio"
}

func (t *GetPortfolioTool) Description() string {
	return "Get complete portfolio with all holdings, total value in USD, and P&L"
}

func (t *GetPortfolioTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

func (t *GetPortfolioTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	portfolio, err := t.client.GetPortfolio(ctx)
	if err != nil {
		// Log the actual error for debugging
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Failed to get portfolio: " + err.Error(),
			Data:    err.Error(),
		}
	}
	return portfolio, nil
}

// GetBalanceTool obtiene el balance de una moneda específica
type GetBalanceTool struct {
	client *gctclient.Client
}

func NewGetBalanceTool(client *gctclient.Client) *GetBalanceTool {
	return &GetBalanceTool{client: client}
}

func (t *GetBalanceTool) Name() string {
	return "get_balance"
}

func (t *GetBalanceTool) Description() string {
	return "Get balance for a specific currency on an exchange (available, locked, and total)"
}

func (t *GetBalanceTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name",
			},
			"currency": map[string]interface{}{
				"type":        "string",
				"description": "Currency code (e.g., 'BTC', 'USDT')",
			},
		},
		"required": []string{"exchange", "currency"},
	}
}

func (t *GetBalanceTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, ok := params["exchange"].(string)
	if !ok || exchange == "" {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "exchange is required",
		}
	}

	currency, ok := params["currency"].(string)
	if !ok || currency == "" {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "currency is required",
		}
	}

	balance, err := t.client.GetBalance(ctx, exchange, currency)
	if err != nil {
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Internal error",
			Data:    err.Error(),
		}
	}

	return balance, nil
}

// GetPositionsTool obtiene posiciones abiertas
type GetPositionsTool struct {
	client *gctclient.Client
}

func NewGetPositionsTool(client *gctclient.Client) *GetPositionsTool {
	return &GetPositionsTool{client: client}
}

func (t *GetPositionsTool) Name() string {
	return "get_positions"
}

func (t *GetPositionsTool) Description() string {
	return "Get all open positions with entry price, current price, and unrealized P&L"
}

func (t *GetPositionsTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name (optional, returns all exchanges if not specified)",
			},
			"market_type": map[string]interface{}{
				"type":        "string",
				"description": "Filter by market type: 'spot', 'futures', 'margin', or 'options' (optional)",
				"enum":        []string{"spot", "futures", "margin", "options"},
			},
		},
	}
}

func (t *GetPositionsTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange := "kucoin" // Default to kucoin
	if ex, ok := params["exchange"].(string); ok && ex != "" {
		exchange = ex
	}

	marketType := ""
	if mt, ok := params["market_type"].(string); ok {
		marketType = mt
	}

	// Get futures positions (for now, only futures are implemented)
	if marketType == "" || marketType == "futures" {
		positions, err := t.client.GetFuturesPositions(ctx, exchange)
		if err != nil {
			return nil, &models.MCPError{
				Code:    -32603,
				Message: "Failed to get positions",
				Data:    err.Error(),
			}
		}

		// Filter by market_type if specified
		var filteredPositions interface{}
		filteredPositions = positions
		if marketType != "" {
			filtered := make([]interface{}, 0)
			for _, pos := range positions {
				if string(pos.MarketType) == marketType {
					filtered = append(filtered, pos)
				}
			}
			filteredPositions = filtered
		}

		count := len(positions)
		if marketType != "" {
			if filtered, ok := filteredPositions.([]interface{}); ok {
				count = len(filtered)
			}
		}

		return map[string]interface{}{
			"exchange":    exchange,
			"market_type": marketType,
			"positions":   filteredPositions,
			"count":       count,
		}, nil
	}

	// Spot and margin positions are not read by this tool: an empty list would
	// look like "no positions", so refuse instead.
	return nil, &models.MCPError{
		Code:    -32602,
		Message: "Invalid params",
		Data:    fmt.Sprintf("market_type %q is not supported by get_positions; only futures positions are available (use get_balance for spot)", marketType),
	}
}

// GetPnLTool calcula profit & loss
type GetPnLTool struct {
	client *gctclient.Client
}

func NewGetPnLTool(client *gctclient.Client) *GetPnLTool {
	return &GetPnLTool{client: client}
}

func (t *GetPnLTool) Name() string {
	return "get_pnl"
}

func (t *GetPnLTool) Description() string {
	return "Calculate profit and loss (total and per position) with percentage returns"
}

func (t *GetPnLTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name (optional)",
			},
			"pair": map[string]interface{}{
				"type":        "string",
				"description": "Trading pair (optional, calculates for specific pair)",
			},
		},
	}
}

func (t *GetPnLTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange := ""
	if ex, ok := params["exchange"].(string); ok {
		exchange = ex
	}

	pair := ""
	if p, ok := params["pair"].(string); ok {
		pair = p
	}

	// Get portfolio to calculate P&L
	portfolio, err := t.client.GetPortfolio(ctx)
	if err != nil {
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Internal error",
			Data:    err.Error(),
		}
	}

	return map[string]interface{}{
		"exchange":      exchange,
		"pair":          pair,
		"total_pnl":     portfolio.PnL,
		"total_pnl_pct": portfolio.PnLPercent,
		"positions":     []interface{}{},
		"note":          "Per-position P&L calculation not yet fully implemented",
	}, nil
}

// GetTradeHistoryTool obtiene historial de trades
type GetTradeHistoryTool struct {
	client *gctclient.Client
}

func NewGetTradeHistoryTool(client *gctclient.Client) *GetTradeHistoryTool {
	return &GetTradeHistoryTool{client: client}
}

func (t *GetTradeHistoryTool) Name() string {
	return "get_trade_history"
}

func (t *GetTradeHistoryTool) Description() string {
	return "Get trade history with timestamps, prices, amounts, and fees"
}

func (t *GetTradeHistoryTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name",
			},
			"pair": map[string]interface{}{
				"type":        "string",
				"description": "Trading pair (optional)",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Number of trades to return (default: 50)",
				"default":     50,
			},
		},
		"required": []string{"exchange"},
	}
}

func (t *GetTradeHistoryTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, ok := params["exchange"].(string)
	if !ok || exchange == "" {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "exchange is required",
		}
	}

	pair := ""
	if p, ok := params["pair"].(string); ok {
		pair = p
	}

	limit := 50
	if l, ok := params["limit"]; ok {
		switch v := l.(type) {
		case int:
			limit = v
		case float64:
			limit = int(v)
		}
	}

	trades, err := t.client.GetTradeHistory(ctx, exchange, pair, limit)
	if err != nil {
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Failed to get trade history",
			Data:    err.Error(),
		}
	}

	return map[string]interface{}{
		"exchange": exchange,
		"pair":     pair,
		"trades":   trades,
		"count":    len(trades),
		"limit":    limit,
	}, nil
}
