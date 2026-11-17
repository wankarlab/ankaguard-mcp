package risk

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/rantipay/trading-mcp/internal/trading"
)

// GetPositionsTool provides the get_positions MCP tool
type GetPositionsTool struct {
	client          *gctclient.Client
	positionManager trading.PositionTracker
}

// NewGetPositionsTool creates a new GetPositionsTool
func NewGetPositionsTool(client *gctclient.Client, manager trading.PositionTracker) *GetPositionsTool {
	return &GetPositionsTool{
		client:          client,
		positionManager: manager,
	}
}

// Name returns the tool name. It must differ from portfolio's "get_positions"
// (the exchange-level positions tool clients already use): both are registered
// on the same MCP server, and a duplicate name aborts startup.
func (t *GetPositionsTool) Name() string {
	return "get_tracked_positions"
}

// Description returns the tool description
func (t *GetPositionsTool) Description() string {
	return "Get positions tracked by the risk PositionTracker with details including P&L, SL/TP, and monitoring status"
}

// InputSchema returns the input schema
func (t *GetPositionsTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"include_history": map[string]interface{}{
				"type":        "boolean",
				"description": "Include closed positions in the response",
				"default":     false,
			},
			"history_limit": map[string]interface{}{
				"type":        "integer",
				"description": "Number of closed positions to include (max 100)",
				"default":     10,
				"minimum":     1,
				"maximum":     100,
			},
		},
		"required": []string{},
	}
}

// Execute executes the tool
func (t *GetPositionsTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	if t.positionManager == nil {
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Internal error",
			Data:    "Position Manager not initialized",
		}
	}

	// Parse arguments
	includeHistory := false
	if val, ok := args["include_history"].(bool); ok {
		includeHistory = val
	}

	historyLimit := 10
	if val, ok := args["history_limit"].(float64); ok {
		historyLimit = int(val)
		if historyLimit < 1 {
			historyLimit = 1
		}
		if historyLimit > 100 {
			historyLimit = 100
		}
	}

	// Get open positions
	openPositions, err := t.positionManager.GetOpenPositions(ctx)
	if err != nil {
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Failed to get open positions",
			Data:    err.Error(),
		}
	}

	// Create summary text
	summaryText := "📊 **Position Summary**\n\n"
	summaryText += fmt.Sprintf("**Open Positions:** %d\n\n", len(openPositions))

	if len(openPositions) == 0 {
		summaryText += "No open positions.\n"
	} else {
		// Calculate total exposure and P&L
		totalExposure, _ := t.positionManager.GetTotalExposure(ctx)
		totalUnrealizedPnL := 0.0

		for _, pos := range openPositions {
			totalUnrealizedPnL += pos.UnrealizedPnL
		}

		summaryText += fmt.Sprintf("**Total Exposure:** $%.2f\n", totalExposure)
		summaryText += fmt.Sprintf("**Total Unrealized P&L:** $%.2f\n\n", totalUnrealizedPnL)

		// List each position
		for i, pos := range openPositions {
			summaryText += fmt.Sprintf("**Position %d:**\n", i+1)
			summaryText += fmt.Sprintf("• ID: %s\n", pos.ID)
			summaryText += fmt.Sprintf("• Symbol: %s\n", pos.Symbol)
			summaryText += fmt.Sprintf("• Side: %s\n", pos.Side)
			summaryText += fmt.Sprintf("• Size: %.6f\n", pos.Size)
			summaryText += fmt.Sprintf("• Entry Price: $%.6f\n", pos.EntryPrice)
			summaryText += fmt.Sprintf("• Current Price: $%.6f\n", pos.CurrentPrice)
			summaryText += fmt.Sprintf("• Leverage: %.1fx\n", pos.Leverage)
			summaryText += fmt.Sprintf("• Stop Loss: $%.6f\n", pos.StopLoss)
			summaryText += fmt.Sprintf("• Take Profit: $%.6f\n", pos.TakeProfit)
			summaryText += fmt.Sprintf("• Unrealized P&L: $%.2f\n", pos.UnrealizedPnL)
			summaryText += fmt.Sprintf("• Strategy: %s\n", pos.Strategy)
			summaryText += fmt.Sprintf("• Open Time: %s\n", pos.OpenTime.Format("2006-01-02 15:04:05"))
			if pos.TrailingStop {
				summaryText += fmt.Sprintf("• Trailing Stop: Enabled (%.2f%%)\n", pos.TrailingDistance*100)
			}
			summaryText += "\n"
		}
	}

	// Prepare response
	response := map[string]interface{}{
		"summary":        summaryText,
		"open_positions": openPositions,
		"total_exposure": 0.0,
	}

	if totalExp, err := t.positionManager.GetTotalExposure(ctx); err == nil {
		response["total_exposure"] = totalExp
	}

	// Include history if requested
	if includeHistory {
		history, err := t.positionManager.GetPositionHistory(ctx, historyLimit)
		if err == nil {
			response["closed_positions"] = history
			summaryText += fmt.Sprintf("\n**Closed Positions (last %d):** %d\n", historyLimit, len(history))
		}
	}

	// Convert positions to JSON
	positionsJSON, _ := json.MarshalIndent(openPositions, "", "  ")
	response["positions_json"] = json.RawMessage(positionsJSON)

	return response, nil
}
