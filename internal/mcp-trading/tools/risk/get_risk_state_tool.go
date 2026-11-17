package risk

import (
	"context"
	"fmt"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	internalRisk "github.com/rantipay/trading-mcp/internal/risk"
)

type GetRiskStateTool struct {
	client   *gctclient.Client
	sentinel internalRisk.RiskSentinel
}

func NewGetRiskStateTool(client *gctclient.Client, sentinel internalRisk.RiskSentinel) *GetRiskStateTool {
	return &GetRiskStateTool{client: client, sentinel: sentinel}
}

func (t *GetRiskStateTool) Name() string {
	return "get_risk_state"
}

func (t *GetRiskStateTool) Description() string {
	return "Get current risk management state"
}

func (t *GetRiskStateTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

func (t *GetRiskStateTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	if t.sentinel == nil {
		return nil, fmt.Errorf("risk sentinel not available")
	}

	state, err := t.sentinel.GetRiskState(ctx)
	if err != nil {
		return nil, fmt.Errorf("get risk state: %w", err)
	}

	return map[string]interface{}{
		"risk_state": state,
		"summary": map[string]interface{}{
			"kill_switch_active": state.KillSwitchActive,
			"status":             "active",
		},
	}, nil
}
