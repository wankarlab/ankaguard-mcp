package orders

import (
	"context"
	"fmt"
	"time"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
)

// CancelOrderTool cancela una orden específica
type CancelOrderTool struct {
	BaseTool
}

func NewCancelOrderTool(client *gctclient.Client) *CancelOrderTool {
	return &CancelOrderTool{BaseTool: BaseTool{client: client}}
}

func (t *CancelOrderTool) Name() string {
	return "cancel_order"
}

func (t *CancelOrderTool) Description() string {
	return "Cancel a specific order by order ID"
}

func (t *CancelOrderTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{"type": "string", "description": "Exchange name"},
			"order_id": map[string]interface{}{"type": "string", "description": "Order ID to cancel"},
		},
		"required": []string{"exchange", "order_id"},
	}
}

func (t *CancelOrderTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, ok := params["exchange"].(string)
	if !ok || exchange == "" {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: "exchange is required"}
	}

	orderID, ok := params["order_id"].(string)
	if !ok || orderID == "" {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: "order_id is required"}
	}

	return executeWithContext(ctx, func(ctx context.Context) (interface{}, error) {
		err := t.client.CancelOrder(ctx, exchange, orderID)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"success":  true,
			"exchange": exchange,
			"order_id": orderID,
			"message":  "Order cancelled successfully",
		}, nil
	})
}

// CancelAllOrdersTool cancela todas las órdenes
type CancelAllOrdersTool struct {
	BaseTool
}

func NewCancelAllOrdersTool(client *gctclient.Client) *CancelAllOrdersTool {
	return &CancelAllOrdersTool{BaseTool: BaseTool{client: client}}
}

func (t *CancelAllOrdersTool) Name() string {
	return "cancel_all_orders"
}

func (t *CancelAllOrdersTool) Description() string {
	return "Cancel all open orders for a trading pair or entire exchange"
}

func (t *CancelAllOrdersTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{"type": "string", "description": "Exchange name"},
			"pair":     map[string]interface{}{"type": "string", "description": "Trading pair (optional, cancels all if not specified)"},
		},
		"required": []string{"exchange"},
	}
}

func (t *CancelAllOrdersTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, ok := params["exchange"].(string)
	if !ok || exchange == "" {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: "exchange is required"}
	}

	pair := getStringParam(params, "pair", "")

	return executeWithContext(ctx, func(ctx context.Context) (interface{}, error) {
		// Get all open orders for this exchange/pair
		orders, err := t.client.GetOpenOrders(ctx, exchange, pair)
		if err != nil {
			return nil, err
		}

		cancelledCount := 0
		failedCount := 0
		var lastError error

		// Cancel each order
		for _, order := range orders {
			if orderID, ok := order["order_id"].(string); ok && orderID != "" {
				if err := t.client.CancelOrder(ctx, exchange, orderID); err != nil {
					failedCount++
					lastError = err
				} else {
					cancelledCount++
				}
			}
		}

		result := map[string]interface{}{
			"success":         failedCount == 0,
			"exchange":        exchange,
			"pair":            pair,
			"cancelled_count": cancelledCount,
			"failed_count":    failedCount,
			"total_orders":    len(orders),
		}

		if lastError != nil {
			result["last_error"] = lastError.Error()
		}

		if cancelledCount > 0 {
			result["message"] = fmt.Sprintf("Successfully cancelled %d of %d orders", cancelledCount, len(orders))
		} else if len(orders) == 0 {
			result["message"] = "No open orders to cancel"
		} else {
			result["message"] = fmt.Sprintf("Failed to cancel orders: %v", lastError)
		}

		return result, nil
	})
}

// ModifyOrderTool modifica una orden existente
type ModifyOrderTool struct {
	BaseTool
}

func NewModifyOrderTool(client *gctclient.Client) *ModifyOrderTool {
	return &ModifyOrderTool{BaseTool: BaseTool{client: client}}
}

func (t *ModifyOrderTool) Name() string {
	return "modify_order"
}

func (t *ModifyOrderTool) Description() string {
	return "Modify an existing order (price and/or amount)"
}

func (t *ModifyOrderTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange":   map[string]interface{}{"type": "string", "description": "Exchange name"},
			"order_id":   map[string]interface{}{"type": "string", "description": "Order ID to modify"},
			"new_price":  map[string]interface{}{"type": "number", "description": "New price (optional)"},
			"new_amount": map[string]interface{}{"type": "number", "description": "New amount (optional)"},
		},
		"required": []string{"exchange", "order_id"},
	}
}

func (t *ModifyOrderTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, ok := params["exchange"].(string)
	if !ok || exchange == "" {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: "exchange is required"}
	}

	orderID, ok := params["order_id"].(string)
	if !ok || orderID == "" {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: "order_id is required"}
	}

	newPrice, _ := getFloatParam(params, "new_price", false)
	newAmount, _ := getFloatParam(params, "new_amount", false)

	// Validate that at least one modification is specified
	if newPrice == 0 && newAmount == 0 {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: "at least one of new_price or new_amount is required"}
	}

	return executeWithContext(ctx, func(ctx context.Context) (interface{}, error) {
		// Get the original order details first
		originalOrder, err := t.client.GetOrderStatus(ctx, exchange, orderID)
		if err != nil {
			return nil, fmt.Errorf("failed to get original order: %w", err)
		}

		// TOOL-05: read the ACTUAL filled quantity before cancelling, so the
		// resubmit only covers the unfilled remainder. Cancelling and blindly
		// resubmitting originalOrder.Amount would double the user's exposure
		// on any order that was partially filled between placement and this
		// call. If the exchange doesn't report fill info for this order (or
		// it's already fully filled), we refuse to guess.
		size, filled, fillErr := t.client.GetOrderFillInfo(ctx, exchange, orderID)
		if fillErr != nil {
			return nil, fmt.Errorf("failed to determine filled quantity before modifying order (refusing to cancel+resubmit blind, which could double exposure on a partial fill): %w", fillErr)
		}
		remaining := size - filled
		if remaining < 0 {
			remaining = 0
		}
		if remaining <= 0 {
			return nil, fmt.Errorf("modify_order: order %s has no remaining unfilled quantity (size=%.8f filled=%.8f) — nothing to modify, it is already fully filled", orderID, size, filled)
		}

		// Cancel the original order
		if err := t.client.CancelOrder(ctx, exchange, orderID); err != nil {
			return nil, fmt.Errorf("failed to cancel original order: %w", err)
		}

		// Prepare new order with modified values
		price := originalOrder.Price
		if newPrice > 0 {
			price = newPrice
		}

		amount := remaining
		if newAmount > 0 {
			// Caller explicitly requested a specific amount — honour it, but
			// never exceed the unfilled remainder (that would still double
			// exposure beyond what was originally requested).
			amount = newAmount
			if amount > remaining {
				amount = remaining
			}
		}

		// Submit new order with modified parameters
		newOrderReq := &models.OrderRequest{
			Exchange: exchange,
			Pair:     originalOrder.Pair,
			Side:     originalOrder.Side,
			Type:     originalOrder.Type,
			Price:    price,
			Amount:   amount,
			ClientID: deterministicClientOrderID(exchange, originalOrder.Pair, originalOrder.Side, originalOrder.Type, amount, price),
		}

		newOrder, err := t.client.SubmitOrder(ctx, newOrderReq)
		if err != nil {
			return nil, fmt.Errorf("failed to submit modified order (original order was cancelled): %w", err)
		}

		return map[string]interface{}{
			"success":                 true,
			"exchange":                exchange,
			"original_order_id":       orderID,
			"new_order_id":            newOrder.OrderID,
			"pair":                    originalOrder.Pair,
			"side":                    originalOrder.Side,
			"type":                    originalOrder.Type,
			"old_price":               originalOrder.Price,
			"new_price":               price,
			"old_amount":              originalOrder.Amount,
			"filled_before_modify":    filled,
			"remaining_before_modify": remaining,
			"new_amount":              amount,
			"message":                 "Order modified successfully (cancelled and resubmitted for the unfilled remainder only)",
		}, nil
	})
}

// GetOrderStatusTool obtiene el estado de una orden
type GetOrderStatusTool struct {
	BaseTool
}

func NewGetOrderStatusTool(client *gctclient.Client) *GetOrderStatusTool {
	return &GetOrderStatusTool{BaseTool: BaseTool{client: client}}
}

func (t *GetOrderStatusTool) Name() string {
	return "get_order_status"
}

func (t *GetOrderStatusTool) Description() string {
	return "Get the current status of a specific order"
}

func (t *GetOrderStatusTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange":    map[string]interface{}{"type": "string", "description": "Exchange name"},
			"order_id":    map[string]interface{}{"type": "string", "description": "Order ID"},
			"market_type": map[string]interface{}{"type": "string", "description": "Market type: 'spot', 'futures', 'margin', or 'options' (optional)", "enum": []string{"spot", "futures", "margin", "options"}},
		},
		"required": []string{"exchange", "order_id"},
	}
}

func (t *GetOrderStatusTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, ok := params["exchange"].(string)
	if !ok || exchange == "" {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: "exchange is required"}
	}

	orderID, ok := params["order_id"].(string)
	if !ok || orderID == "" {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: "order_id is required"}
	}

	return executeWithContext(ctx, func(ctx context.Context) (interface{}, error) {
		return t.client.GetOrderStatus(ctx, exchange, orderID)
	})
}

// GetOpenOrdersTool obtiene órdenes abiertas
type GetOpenOrdersTool struct {
	BaseTool
}

func NewGetOpenOrdersTool(client *gctclient.Client) *GetOpenOrdersTool {
	return &GetOpenOrdersTool{BaseTool: BaseTool{client: client}}
}

func (t *GetOpenOrdersTool) Name() string {
	return "get_open_orders"
}

func (t *GetOpenOrdersTool) Description() string {
	return "Get all open orders for a trading pair or entire exchange"
}

func (t *GetOpenOrdersTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{"type": "string", "description": "Exchange name"},
			"pair":     map[string]interface{}{"type": "string", "description": "Trading pair (optional)"},
		},
		"required": []string{"exchange"},
	}
}

func (t *GetOpenOrdersTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, ok := params["exchange"].(string)
	if !ok || exchange == "" {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: "exchange is required"}
	}

	pair := getStringParam(params, "pair", "")

	return executeWithContext(ctx, func(ctx context.Context) (interface{}, error) {
		orders, err := t.client.GetOpenOrders(ctx, exchange, pair)
		if err != nil {
			return nil, err
		}

		return map[string]interface{}{
			"exchange": exchange,
			"pair":     pair,
			"orders":   orders,
			"count":    len(orders),
		}, nil
	})
}

// GetOrderHistoryTool obtiene historial de órdenes
type GetOrderHistoryTool struct {
	BaseTool
}

func NewGetOrderHistoryTool(client *gctclient.Client) *GetOrderHistoryTool {
	return &GetOrderHistoryTool{BaseTool: BaseTool{client: client}}
}

func (t *GetOrderHistoryTool) Name() string {
	return "get_order_history"
}

func (t *GetOrderHistoryTool) Description() string {
	return "Get order history (completed, cancelled, and failed orders) with optional time filtering"
}

func (t *GetOrderHistoryTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange":   map[string]interface{}{"type": "string", "description": "Exchange name"},
			"pair":       map[string]interface{}{"type": "string", "description": "Trading pair (optional)"},
			"start_time": map[string]interface{}{"type": "string", "description": "Start time for filtering orders (RFC3339 format, e.g., '2024-01-15T00:00:00Z'). Defaults to 24 hours ago if not specified."},
			"end_time":   map[string]interface{}{"type": "string", "description": "End time for filtering orders (RFC3339 format, e.g., '2024-01-16T00:00:00Z'). Defaults to current time if not specified."},
			"limit":      map[string]interface{}{"type": "integer", "description": "Maximum number of orders to return (default: 50, max: 500)", "default": 50},
		},
		"required": []string{"exchange"},
	}
}

func (t *GetOrderHistoryTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, ok := params["exchange"].(string)
	if !ok || exchange == "" {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: "exchange is required"}
	}

	pair := getStringParam(params, "pair", "")

	// Parse limit with validation
	limit := 50
	if l, ok := params["limit"]; ok {
		switch v := l.(type) {
		case int:
			limit = v
		case float64:
			limit = int(v)
		}
	}
	// Enforce maximum limit
	if limit > 500 {
		limit = 500
	}
	if limit < 1 {
		limit = 50
	}

	// Parse time filters with defaults
	now := time.Now().UTC()
	endTime := now
	startTime := now.Add(-24 * time.Hour) // Default: last 24 hours

	// Parse start_time if provided
	if startTimeStr := getStringParam(params, "start_time", ""); startTimeStr != "" {
		parsedTime, err := parseTimeParam(startTimeStr)
		if err != nil {
			return nil, &models.MCPError{
				Code:    -32602,
				Message: "Invalid params",
				Data:    fmt.Sprintf("start_time must be in RFC3339 format (e.g., '2024-01-15T00:00:00Z'): %v", err),
			}
		}
		startTime = parsedTime
	}

	// Parse end_time if provided
	if endTimeStr := getStringParam(params, "end_time", ""); endTimeStr != "" {
		parsedTime, err := parseTimeParam(endTimeStr)
		if err != nil {
			return nil, &models.MCPError{
				Code:    -32602,
				Message: "Invalid params",
				Data:    fmt.Sprintf("end_time must be in RFC3339 format (e.g., '2024-01-16T00:00:00Z'): %v", err),
			}
		}
		endTime = parsedTime
	}

	// Validate time range
	if startTime.After(endTime) {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "start_time cannot be after end_time",
		}
	}

	return executeWithContext(ctx, func(ctx context.Context) (interface{}, error) {
		orders, err := t.client.GetOrderHistoryWithTimeFilter(ctx, exchange, pair, limit, startTime, endTime)
		if err != nil {
			return nil, err
		}

		return map[string]interface{}{
			"exchange":   exchange,
			"pair":       pair,
			"orders":     orders,
			"count":      len(orders),
			"limit":      limit,
			"start_time": startTime.Format(time.RFC3339),
			"end_time":   endTime.Format(time.RFC3339),
		}, nil
	})
}

// parseTimeParam attempts to parse a time string in multiple formats
func parseTimeParam(timeStr string) (time.Time, error) {
	// Try RFC3339 first (most common)
	if t, err := time.Parse(time.RFC3339, timeStr); err == nil {
		return t, nil
	}

	// Try RFC3339Nano
	if t, err := time.Parse(time.RFC3339Nano, timeStr); err == nil {
		return t, nil
	}

	// Try common date-only format
	if t, err := time.Parse("2006-01-02", timeStr); err == nil {
		return t, nil
	}

	// Try datetime without timezone
	if t, err := time.Parse("2006-01-02T15:04:05", timeStr); err == nil {
		return t.UTC(), nil
	}

	// Try Unix timestamp (seconds)
	var unixSec int64
	if _, err := fmt.Sscanf(timeStr, "%d", &unixSec); err == nil && unixSec > 1000000000 {
		return time.Unix(unixSec, 0).UTC(), nil
	}

	return time.Time{}, fmt.Errorf("unrecognized time format: %s", timeStr)
}
