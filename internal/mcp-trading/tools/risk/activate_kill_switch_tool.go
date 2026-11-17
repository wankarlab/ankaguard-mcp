package risk

import (
	"context"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/rantipay/trading-mcp/internal/risk"
)

// ActivateKillSwitchTool provides the activate_kill_switch MCP tool
type ActivateKillSwitchTool struct {
	client       *gctclient.Client
	riskSentinel risk.RiskSentinel
}

// NewActivateKillSwitchTool creates a new ActivateKillSwitchTool
func NewActivateKillSwitchTool(client *gctclient.Client, sentinel risk.RiskSentinel) *ActivateKillSwitchTool {
	return &ActivateKillSwitchTool{
		client:       client,
		riskSentinel: sentinel,
	}
}

// Name returns the tool name
func (t *ActivateKillSwitchTool) Name() string {
	return "activate_kill_switch"
}

// Description returns the tool description
func (t *ActivateKillSwitchTool) Description() string {
	return "Activate the emergency kill switch to immediately stop all trading operations"
}

// InputSchema returns the input schema
func (t *ActivateKillSwitchTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"reason": map[string]interface{}{
				"type":        "string",
				"description": "Reason for activating the kill switch",
			},
		},
		"required": []string{"reason"},
	}
}

// Execute executes the tool
func (t *ActivateKillSwitchTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	if t.riskSentinel == nil {
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Internal error",
			Data:    "Risk Sentinel not initialized",
		}
	}

	// Extract reason
	reason, ok := args["reason"].(string)
	if !ok || reason == "" {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "Reason is required and must be a non-empty string",
		}
	}

	// Activate kill switch
	err := t.riskSentinel.ActivateKillSwitch(ctx, reason)
	if err != nil {
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Failed to activate kill switch",
			Data:    err.Error(),
		}
	}

	return map[string]interface{}{
		"success": true,
		"message": "🚨 **KILL SWITCH ACTIVATED** 🚨\n\n" +
			"All trading operations have been immediately stopped.\n" +
			"Reason: " + reason + "\n\n" +
			"To resume trading, you must manually deactivate the kill switch.",
		"reason": reason,
	}, nil
}
