package risk

import (
	"context"
	"testing"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/risk"
	"github.com/rantipay/trading-mcp/internal/trading"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createTestRiskSentinel() risk.RiskSentinel {
	config := &risk.RiskConfig{
		MaxDrawdownDaily:     5.0,
		MaxDrawdownWeekly:    10.0,
		MaxDrawdownMonthly:   20.0,
		MaxConsecutiveLosses: 3,
		MaxExposurePerSymbol: 50.0,
		MaxExposureTotal:     80.0,
	}

	return risk.NewRiskSentinel(config, 1000.0, logrus.New())
}

// Test GetRiskStateTool
func TestGetRiskStateTool_Name(t *testing.T) {
	client := &gctclient.Client{}
	sentinel := createTestRiskSentinel()
	tool := NewGetRiskStateTool(client, sentinel)

	assert.Equal(t, "get_risk_state", tool.Name())
	assert.NotEmpty(t, tool.Description())
	assert.NotNil(t, tool.InputSchema())
}

func TestGetRiskStateTool_Execute(t *testing.T) {
	client := &gctclient.Client{}
	sentinel := createTestRiskSentinel()
	tool := NewGetRiskStateTool(client, sentinel)

	// Execute tool
	result, err := tool.Execute(context.Background(), map[string]interface{}{})
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify result structure
	resultMap, ok := result.(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, resultMap, "summary")
	assert.Contains(t, resultMap, "risk_state")
}

func TestGetRiskStateTool_NilSentinel(t *testing.T) {
	client := &gctclient.Client{}
	tool := NewGetRiskStateTool(client, nil)

	result, err := tool.Execute(context.Background(), map[string]interface{}{})
	assert.Error(t, err)
	assert.Nil(t, result)
}

// Test ActivateKillSwitchTool
func TestActivateKillSwitchTool_Name(t *testing.T) {
	client := &gctclient.Client{}
	sentinel := createTestRiskSentinel()
	tool := NewActivateKillSwitchTool(client, sentinel)

	assert.Equal(t, "activate_kill_switch", tool.Name())
	assert.NotEmpty(t, tool.Description())
	assert.NotNil(t, tool.InputSchema())
}

func TestActivateKillSwitchTool_Execute(t *testing.T) {
	client := &gctclient.Client{}
	sentinel := createTestRiskSentinel()
	tool := NewActivateKillSwitchTool(client, sentinel)

	// Execute tool
	args := map[string]interface{}{
		"reason": "Test activation",
	}
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify result
	resultMap, ok := result.(map[string]interface{})
	require.True(t, ok)
	assert.True(t, resultMap["success"].(bool))
	assert.Contains(t, resultMap["message"], "KILL SWITCH ACTIVATED")

	// Verify kill switch is active
	state, err := sentinel.GetRiskState(context.Background())
	require.NoError(t, err)
	assert.True(t, state.KillSwitchActive)
}

func TestActivateKillSwitchTool_MissingReason(t *testing.T) {
	client := &gctclient.Client{}
	sentinel := createTestRiskSentinel()
	tool := NewActivateKillSwitchTool(client, sentinel)

	result, err := tool.Execute(context.Background(), map[string]interface{}{})
	assert.Error(t, err)
	assert.Nil(t, result)
}

// Test DeactivateKillSwitchTool
func TestDeactivateKillSwitchTool_Name(t *testing.T) {
	client := &gctclient.Client{}
	sentinel := createTestRiskSentinel()
	tool := NewDeactivateKillSwitchTool(client, sentinel)

	assert.Equal(t, "deactivate_kill_switch", tool.Name())
	assert.NotEmpty(t, tool.Description())
	assert.NotNil(t, tool.InputSchema())
}

func TestDeactivateKillSwitchTool_Execute(t *testing.T) {
	client := &gctclient.Client{}
	sentinel := createTestRiskSentinel()
	tool := NewDeactivateKillSwitchTool(client, sentinel)

	// First activate kill switch
	err := sentinel.ActivateKillSwitch(context.Background(), "Test")
	require.NoError(t, err)

	// Then deactivate
	result, err := tool.Execute(context.Background(), map[string]interface{}{})
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify result
	resultMap, ok := result.(map[string]interface{})
	require.True(t, ok)
	assert.True(t, resultMap["success"].(bool))
	assert.Contains(t, resultMap["message"], "DEACTIVATED")

	// Verify kill switch is inactive
	state, err := sentinel.GetRiskState(context.Background())
	require.NoError(t, err)
	assert.False(t, state.KillSwitchActive)
}

// Test GetPositionsTool and UpdateSLTPTool - basic structure tests only
func TestGetPositionsTool_Name(t *testing.T) {
	client := &gctclient.Client{}
	tool := NewGetPositionsTool(client, nil)

	assert.Equal(t, "get_tracked_positions", tool.Name())
	assert.NotEmpty(t, tool.Description())
	assert.NotNil(t, tool.InputSchema())
}

func TestGetPositionsTool_NilManager(t *testing.T) {
	client := &gctclient.Client{}
	tool := NewGetPositionsTool(client, nil)

	result, err := tool.Execute(context.Background(), map[string]interface{}{})
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestUpdateSLTPTool_Name(t *testing.T) {
	client := &gctclient.Client{}
	tool := NewUpdateSLTPTool(client, nil)

	assert.Equal(t, "update_sltp", tool.Name())
	assert.NotEmpty(t, tool.Description())
	assert.NotNil(t, tool.InputSchema())
}

func TestUpdateSLTPTool_NilExecutor(t *testing.T) {
	client := &gctclient.Client{}
	tool := NewUpdateSLTPTool(client, nil)

	args := map[string]interface{}{
		"position_id": "test_id",
		"stop_loss":   49500.0,
	}
	result, err := tool.Execute(context.Background(), args)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestUpdateSLTPTool_MissingPositionID(t *testing.T) {
	client := &gctclient.Client{}
	tool := NewUpdateSLTPTool(client, nil)

	result, err := tool.Execute(context.Background(), map[string]interface{}{})
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestUpdateSLTPTool_NoSLOrTP(t *testing.T) {
	client := &gctclient.Client{}
	var mockExecutor trading.SLTPExecutor // nil interface
	tool := NewUpdateSLTPTool(client, mockExecutor)

	args := map[string]interface{}{
		"position_id": "test_id",
	}
	result, err := tool.Execute(context.Background(), args)
	assert.Error(t, err)
	assert.Nil(t, result)
}
