package websocket

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	mcpserver "github.com/rantipay/trading-mcp/internal/mcp-trading/server"
)

// resolveTargetUserID derives the user_id a subscription tool should act on.
// It ignores any client-supplied "user_id" and uses the authenticated
// caller's own id (TOOL-04: don't trust body params for identity). An
// authenticated level>=4 admin may act on behalf of another user by
// supplying "user_id" explicitly; that override is logged.
func resolveTargetUserID(ctx context.Context, params map[string]interface{}, toolName string) (string, *models.MCPError) {
	callerID, hasCaller := mcpserver.UserIDFromContext(ctx)
	requested, _ := params["user_id"].(string)

	if !hasCaller {
		if requested == "" {
			return "", &models.MCPError{Code: -32602, Message: "Invalid user_id parameter"}
		}
		return requested, nil
	}

	if requested == "" || requested == callerID {
		return callerID, nil
	}

	level, _ := mcpserver.UserLevelFromContext(ctx)
	if level < 4 {
		return "", &models.MCPError{
			Code:    -32001,
			Message: "Forbidden: cannot act on another user's subscriptions",
		}
	}

	logrus.WithFields(logrus.Fields{
		"tool":        toolName,
		"admin_id":    callerID,
		"target_user": requested,
	}).Warn("Admin acting on behalf of another user (subscriptions)")

	return requested, nil
}

// SubscribeMarketDataTool manages WebSocket market data subscriptions
type SubscribeMarketDataTool struct {
	BaseTool
}

func NewSubscribeMarketDataTool(client *gctclient.Client) *SubscribeMarketDataTool {
	return &SubscribeMarketDataTool{BaseTool: BaseTool{client: client}}
}

func (t *SubscribeMarketDataTool) Name() string {
	return "subscribe_market_data"
}

func (t *SubscribeMarketDataTool) Description() string {
	return "Subscribe to real-time market data via WebSocket"
}

func (t *SubscribeMarketDataTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Subscription action",
				"enum":        []string{"subscribe", "unsubscribe"},
			},
			"channel": map[string]interface{}{
				"type":        "string",
				"description": "Data channel to subscribe to",
				"enum":        []string{"price", "ticker", "orderbook", "trades", "candles"},
			},
			"symbol": map[string]interface{}{
				"type":        "string",
				"description": "Trading pair symbol (e.g., 'BTC-USDT')",
			},
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name",
			},
			"interval": map[string]interface{}{
				"type":        "string",
				"description": "Interval for candles (e.g., '1m', '5m', '1h')",
			},
			"depth": map[string]interface{}{
				"type":        "number",
				"description": "Orderbook depth (for orderbook channel)",
			},
		},
		"required": []string{"action", "channel", "symbol", "exchange"},
	}
}

func (t *SubscribeMarketDataTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	action, ok := params["action"].(string)
	if !ok {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid action parameter",
		}
	}

	channel, ok := params["channel"].(string)
	if !ok {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid channel parameter",
		}
	}

	symbol, ok := params["symbol"].(string)
	if !ok {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid symbol parameter",
		}
	}

	exchange, ok := params["exchange"].(string)
	if !ok {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid exchange parameter",
		}
	}

	subscription := &models.WebSocketSubscription{
		Channel:  channel,
		Symbol:   symbol,
		Exchange: exchange,
		Active:   action == "subscribe",
		Data: map[string]interface{}{
			"interval": getString(params, "interval"),
			"depth":    getFloat(params, "depth"),
		},
	}

	var result interface{}
	var err error

	if action == "subscribe" {
		result, err = t.client.SubscribeWebSocket(ctx, subscription)
	} else {
		result, err = t.client.UnsubscribeWebSocket(ctx, subscription)
	}

	if err != nil {
		return nil, &models.MCPError{
			Code:    -32603,
			Message: fmt.Sprintf("Failed to %s WebSocket", action),
			Data:    err.Error(),
		}
	}

	return map[string]interface{}{
		"success":      true,
		"action":       action,
		"channel":      channel,
		"symbol":       symbol,
		"exchange":     exchange,
		"message":      fmt.Sprintf("Successfully %sd to %s %s on %s", action, channel, symbol, exchange),
		"subscription": subscription,
		"result":       result,
	}, nil
}

// GetActiveSubscriptionsTool gets all active WebSocket subscriptions
type GetActiveSubscriptionsTool struct {
	BaseTool
}

func NewGetActiveSubscriptionsTool(client *gctclient.Client) *GetActiveSubscriptionsTool {
	return &GetActiveSubscriptionsTool{BaseTool: BaseTool{client: client}}
}

func (t *GetActiveSubscriptionsTool) Name() string {
	return "get_active_subscriptions"
}

func (t *GetActiveSubscriptionsTool) Description() string {
	return "Get all active WebSocket subscriptions"
}

func (t *GetActiveSubscriptionsTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Filter by exchange (optional)",
			},
			"channel": map[string]interface{}{
				"type":        "string",
				"description": "Filter by channel (optional)",
			},
		},
	}
}

func (t *GetActiveSubscriptionsTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange := getString(params, "exchange")
	channel := getString(params, "channel")

	subscriptions, err := t.client.GetActiveSubscriptions(ctx, exchange, channel)
	if err != nil {
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Failed to get active subscriptions",
			Data:    err.Error(),
		}
	}

	return map[string]interface{}{
		"success":       true,
		"subscriptions": subscriptions,
		"count":         len(subscriptions),
		"filters": map[string]interface{}{
			"exchange": exchange,
			"channel":  channel,
		},
	}, nil
}

// ManageUserSubscriptionsTool manages user-specific subscriptions
type ManageUserSubscriptionsTool struct {
	BaseTool
}

func NewManageUserSubscriptionsTool(client *gctclient.Client) *ManageUserSubscriptionsTool {
	return &ManageUserSubscriptionsTool{BaseTool: BaseTool{client: client}}
}

func (t *ManageUserSubscriptionsTool) Name() string {
	return "manage_user_subscriptions"
}

func (t *ManageUserSubscriptionsTool) Description() string {
	return "Manage user-specific market data subscriptions and preferences"
}

func (t *ManageUserSubscriptionsTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action to perform",
				"enum":        []string{"set", "get", "add", "remove"},
			},
			"user_id": map[string]interface{}{
				"type":        "string",
				"description": "User ID",
			},
			"symbols": map[string]interface{}{
				"type":        "array",
				"description": "Array of symbols to subscribe to",
				"items": map[string]interface{}{
					"type": "string",
				},
			},
			"channels": map[string]interface{}{
				"type":        "array",
				"description": "Array of channels to subscribe to",
				"items": map[string]interface{}{
					"type": "string",
					"enum": []string{"price", "ticker", "orderbook", "trades", "candles"},
				},
			},
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name",
			},
		},
		"required": []string{"action", "user_id"},
	}
}

func (t *ManageUserSubscriptionsTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	action, ok := params["action"].(string)
	if !ok {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid action parameter",
		}
	}

	userID, mcpErr := resolveTargetUserID(ctx, params, "manage_user_subscriptions")
	if mcpErr != nil {
		return nil, mcpErr
	}

	switch action {
	case "get":
		subscription, err := t.client.GetUserSubscriptions(ctx, userID)
		if err != nil {
			return nil, &models.MCPError{
				Code:    -32603,
				Message: "Failed to get user subscriptions",
				Data:    err.Error(),
			}
		}
		return map[string]interface{}{
			"success":      true,
			"subscription": subscription,
		}, nil

	case "set", "add", "remove":
		// Parse symbols
		var symbols []string
		if symbolsData, ok := params["symbols"].([]interface{}); ok {
			symbols = make([]string, 0, len(symbolsData))
			for _, symbol := range symbolsData {
				if symbolStr, ok := symbol.(string); ok {
					symbols = append(symbols, symbolStr)
				}
			}
		}

		// Parse channels
		var channels []string
		if channelsData, ok := params["channels"].([]interface{}); ok {
			channels = make([]string, 0, len(channelsData))
			for _, channel := range channelsData {
				if channelStr, ok := channel.(string); ok {
					channels = append(channels, channelStr)
				}
			}
		}

		exchange := getString(params, "exchange")

		subscription := &models.MarketDataSubscription{
			UserID:   userID,
			Symbols:  symbols,
			Channels: channels,
			Exchange: exchange,
			Active:   action != "remove",
		}

		var result interface{}
		var err error

		switch action {
		case "set":
			result, err = t.client.SetUserSubscriptions(ctx, subscription)
		case "add":
			result, err = t.client.AddUserSubscriptions(ctx, subscription)
		case "remove":
			result, err = t.client.RemoveUserSubscriptions(ctx, subscription)
		}

		if err != nil {
			return nil, &models.MCPError{
				Code:    -32603,
				Message: fmt.Sprintf("Failed to %s user subscriptions", action),
				Data:    err.Error(),
			}
		}

		return map[string]interface{}{
			"success":      true,
			"action":       action,
			"user_id":      userID,
			"subscription": result,
			"message":      fmt.Sprintf("User subscriptions %s successfully", action),
		}, nil

	default:
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid action parameter",
		}
	}
}

// GetWebSocketStatusTool gets WebSocket connection status
type GetWebSocketStatusTool struct {
	BaseTool
}

func NewGetWebSocketStatusTool(client *gctclient.Client) *GetWebSocketStatusTool {
	return &GetWebSocketStatusTool{BaseTool: BaseTool{client: client}}
}

func (t *GetWebSocketStatusTool) Name() string {
	return "get_websocket_status"
}

func (t *GetWebSocketStatusTool) Description() string {
	return "Get WebSocket connection status and statistics"
}

func (t *GetWebSocketStatusTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name (optional)",
			},
		},
	}
}

func (t *GetWebSocketStatusTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange := getString(params, "exchange")

	status, err := t.client.GetWebSocketStatus(ctx, exchange)
	if err != nil {
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Failed to get WebSocket status",
			Data:    err.Error(),
		}
	}

	return map[string]interface{}{
		"success": true,
		"status":  status,
	}, nil
}

// BaseTool provides common functionality for WebSocket tools
type BaseTool struct {
	client *gctclient.Client
}

// Helper functions
func getString(params map[string]interface{}, key string) string {
	if val, ok := params[key].(string); ok {
		return val
	}
	return ""
}

func getFloat(params map[string]interface{}, key string) float64 {
	if val, ok := params[key].(float64); ok {
		return val
	}
	return 0.0
}
