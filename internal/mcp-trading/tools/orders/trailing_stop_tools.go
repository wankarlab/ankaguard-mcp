package orders

import (
	"context"
	"fmt"

	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
)

// CreateTrailingStopTool creates trailing stop orders
type CreateTrailingStopTool struct {
	BaseTool
}

func NewCreateTrailingStopTool(client *gctclient.Client) *CreateTrailingStopTool {
	return &CreateTrailingStopTool{BaseTool: BaseTool{client: client}}
}

func (t *CreateTrailingStopTool) Name() string {
	return "create_trailing_stop"
}

func (t *CreateTrailingStopTool) Description() string {
	return "Create a trailing stop order that follows price movements"
}

func (t *CreateTrailingStopTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name",
			},
			"pair": map[string]interface{}{
				"type":        "string",
				"description": "Trading pair (e.g., 'BTC-USDT')",
			},
			"side": map[string]interface{}{
				"type":        "string",
				"description": "Order side",
				"enum":        []string{"buy", "sell"},
			},
			"amount": map[string]interface{}{
				"type":        "number",
				"description": "Order amount",
			},
			"trail_amount": map[string]interface{}{
				"type":        "number",
				"description": "Trailing amount (price or percentage)",
			},
			"trail_type": map[string]interface{}{
				"type":        "string",
				"description": "Trailing type",
				"enum":        []string{"percentage", "fixed"},
			},
			"close_order": map[string]interface{}{
				"type":        "boolean",
				"description": "Close the ENTIRE position when triggered (ignores amount). Default false: protect only the given amount.",
			},
		},
		"required": []string{"exchange", "pair", "side", "amount", "trail_amount", "trail_type"},
	}
}

func (t *CreateTrailingStopTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, ok := params["exchange"].(string)
	if !ok {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid exchange parameter",
		}
	}

	pair, ok := params["pair"].(string)
	if !ok {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid pair parameter",
		}
	}

	side, ok := params["side"].(string)
	if !ok {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid side parameter",
		}
	}

	amount, ok := params["amount"].(float64)
	if !ok {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid amount parameter",
		}
	}

	trailAmount, ok := params["trail_amount"].(float64)
	if !ok {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid trail_amount parameter",
		}
	}

	trailType, ok := params["trail_type"].(string)
	if !ok {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid trail_type parameter",
		}
	}

	// Get current price for initial stop calculation
	ticker, err := t.client.GetTicker(ctx, exchange, pair)
	if err != nil {
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Failed to get current price",
			Data:    err.Error(),
		}
	}

	stopPrice := trailingStopPrice(ticker.Last, side, trailType, trailAmount)

	// Place a REAL protective stop order via the direct KuCoin REST client.
	// KuCoin has no native trailing-stop primitive, so we anchor a static
	// protective stop at the computed trigger price; the live engine re-arms it
	// as price moves. The order is always reduce-only so it can only protect an
	// existing position, never open or flip one.
	//
	// close_order (default false) selects the protection scope: when true KuCoin
	// closes the ENTIRE position on trigger and ignores `amount`; when false the
	// stop protects exactly `amount` contracts, honouring partial-size intent.
	// (Forcing close_order=true unconditionally silently dropped the requested
	// amount — fixed here.)
	closeOrder := getBool(params, "close_order")

	// ClientFor routes a paper profile to the paper broker; GetBaseClient would
	// always be the real exchange client.
	base, routeErr := t.client.ClientFor(ctx)
	if routeErr != nil {
		return nil, routeErr
	}
	if base == nil {
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "direct exchange client unavailable",
		}
	}

	req := &gct.SubmitOrderRequest{
		Exchange:   exchange,
		Pair:       pair,
		Side:       side,
		Type:       "stop",
		Amount:     amount,
		StopPrice:  stopPrice,
		ReduceOnly: true,
		CloseOrder: closeOrder,
		MarketType: gct.MarketTypeFutures,
		ClientID:   resolveClientOrderID("", exchange, pair, side, "stop", amount, stopPrice),
	}

	order, err := base.SubmitFuturesOrder(ctx, req)
	if err != nil {
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Failed to place protective stop order",
			Data:    err.Error(),
		}
	}

	return map[string]interface{}{
		"success":       true,
		"order":         order,
		"exchange":      exchange,
		"pair":          pair,
		"side":          side,
		"amount":        amount,
		"trail_amount":  trailAmount,
		"trail_type":    trailType,
		"current_price": ticker.Last,
		"stop_price":    stopPrice,
		"client_via":    "direct-rest",
		"message":       fmt.Sprintf("Protective stop placed for %s at %.8f", pair, stopPrice),
	}, nil
}


func getBool(params map[string]interface{}, key string) bool {
	if val, ok := params[key].(bool); ok {
		return val
	}
	return false
}

// trailingStopPrice anchors the initial protective stop at the real last price,
// trailAmount away (percent or absolute), below it for a sell and above for a buy.
func trailingStopPrice(last float64, side, trailType string, trailAmount float64) float64 {
	offset := trailAmount
	if trailType == "percentage" {
		offset = last * trailAmount / 100
	}
	if side == "sell" {
		return last - offset
	}
	return last + offset
}
