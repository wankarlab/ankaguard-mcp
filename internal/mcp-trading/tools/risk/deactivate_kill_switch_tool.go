package risk

import (
	"context"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/rantipay/trading-mcp/internal/risk"
)

// DeactivateKillSwitchTool provides the deactivate_kill_switch MCP tool
type DeactivateKillSwitchTool struct {
	client       *gctclient.Client
	riskSentinel risk.RiskSentinel
}

// NewDeactivateKillSwitchTool creates a new DeactivateKillSwitchTool
func NewDeactivateKillSwitchTool(client *gctclient.Client, sentinel risk.RiskSentinel) *DeactivateKillSwitchTool {
	return &DeactivateKillSwitchTool{
		client:       client,
		riskSentinel: sentinel,
	}
}

// Name returns the tool name
func (t *DeactivateKillSwitchTool) Name() string {
	return "deactivate_kill_switch"
}

// Description returns the tool description
func (t *DeactivateKillSwitchTool) Description() string {
	return "Deactivate the emergency kill switch to resume trading operations"
}

// InputSchema returns the input schema
func (t *DeactivateKillSwitchTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
		"required":   []string{},
	}
}

// Execute executes the tool
func (t *DeactivateKillSwitchTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	if t.riskSentinel == nil {
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Internal error",
			Data:    "Risk Sentinel not initialized",
		}
	}

	// Deactivate kill switch
	err := t.riskSentinel.DeactivateKillSwitch(ctx)
	if err != nil {
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Failed to deactivate kill switch",
			Data:    err.Error(),
		}
	}

	return map[string]interface{}{
		"success": true,
		"message": "✅ **KILL SWITCH DEACTIVATED**\n\n" +
			"Trading operations have been resumed.\n" +
			"Please monitor the system carefully.",
	}, nil
}
