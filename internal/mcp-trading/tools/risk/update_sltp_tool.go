package risk

import (
	"context"
	"fmt"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/rantipay/trading-mcp/internal/trading"
)

// UpdateSLTPTool provides the update_sltp MCP tool
type UpdateSLTPTool struct {
	client       *gctclient.Client
	sltpExecutor trading.SLTPExecutor
}

// NewUpdateSLTPTool creates a new UpdateSLTPTool
func NewUpdateSLTPTool(client *gctclient.Client, executor trading.SLTPExecutor) *UpdateSLTPTool {
	return &UpdateSLTPTool{
		client:       client,
		sltpExecutor: executor,
	}
}

// Name returns the tool name
func (t *UpdateSLTPTool) Name() string {
	return "update_sltp"
}

// Description returns the tool description
func (t *UpdateSLTPTool) Description() string {
	return "Update stop-loss and take-profit levels for an open position"
}

// InputSchema returns the input schema
func (t *UpdateSLTPTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"position_id": map[string]interface{}{
				"type":        "string",
				"description": "Position ID to update",
			},
			"stop_loss": map[string]interface{}{
				"type":        "number",
				"description": "New stop loss price (optional)",
			},
			"take_profit": map[string]interface{}{
				"type":        "number",
				"description": "New take profit price (optional)",
			},
		},
		"required": []string{"position_id"},
	}
}

// Execute executes the tool
func (t *UpdateSLTPTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	if t.sltpExecutor == nil {
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Internal error",
			Data:    "SL/TP Executor not initialized",
		}
	}

	// Extract position ID
	positionID, ok := args["position_id"].(string)
	if !ok || positionID == "" {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "position_id is required and must be a non-empty string",
		}
	}

	// Extract stop loss (optional)
	var stopLoss float64
	hasStopLoss := false
	if sl, ok := args["stop_loss"].(float64); ok {
		stopLoss = sl
		hasStopLoss = true
	}

	// Extract take profit (optional)
	var takeProfit float64
	hasTakeProfit := false
	if tp, ok := args["take_profit"].(float64); ok {
		takeProfit = tp
		hasTakeProfit = true
	}

	// At least one must be provided
	if !hasStopLoss && !hasTakeProfit {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "At least one of stop_loss or take_profit must be provided",
		}
	}

	// Update SL/TP
	err := t.sltpExecutor.UpdateSLTP(ctx, positionID, stopLoss, takeProfit)
	if err != nil {
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Failed to update SL/TP",
			Data:    err.Error(),
		}
	}

	message := "✅ **SL/TP Updated Successfully**\n\n"
	message += "Position ID: " + positionID + "\n"
	if hasStopLoss {
		message += "New Stop Loss: $" + formatFloat(stopLoss) + "\n"
	}
	if hasTakeProfit {
		message += "New Take Profit: $" + formatFloat(takeProfit) + "\n"
	}

	return map[string]interface{}{
		"success":     true,
		"message":     message,
		"position_id": positionID,
		"stop_loss":   stopLoss,
		"take_profit": takeProfit,
	}, nil
}

// formatFloat formats a float64 to 6 decimal places
func formatFloat(value float64) string {
	return fmt.Sprintf("%.6f", value)
}
