package trading

import (
	"context"
	"testing"

	"github.com/rantipay/trading-mcp/internal/risk"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mock Risk Sentinel for testing
type mockRiskSentinel struct {
	shouldAllow bool
	maxSize     float64
	maxLeverage float64
}

func (m *mockRiskSentinel) EvaluateAction(ctx context.Context, action *risk.TradingAction) (*risk.RiskDecision, error) {
	return &risk.RiskDecision{
		Allowed:     m.shouldAllow,
		Reason:      "Test reason",
		MaxSize:     m.maxSize,
		MaxLeverage: m.maxLeverage,
		Warnings:    []string{},
	}, nil
}

func (m *mockRiskSentinel) UpdateState(ctx context.Context, trade *risk.Trade) error {
	return nil
}

func (m *mockRiskSentinel) GetRiskState(ctx context.Context) (*risk.RiskState, error) {
	return &risk.RiskState{}, nil
}

func (m *mockRiskSentinel) ActivateKillSwitch(ctx context.Context, reason string) error {
	return nil
}

func (m *mockRiskSentinel) DeactivateKillSwitch(ctx context.Context) error {
	return nil
}

func (m *mockRiskSentinel) ResetDrawdown(ctx context.Context, period string) error { return nil }
func (m *mockRiskSentinel) ActivateRing1(_ context.Context, _, _ string) error    { return nil }
func (m *mockRiskSentinel) DeactivateRing1(_ context.Context, _ string) error     { return nil }
func (m *mockRiskSentinel) IsSpecFrozen(_ string) bool                            { return false }
func (m *mockRiskSentinel) ActivateRing2(_ context.Context, _, _ string) error    { return nil }
func (m *mockRiskSentinel) DeactivateRing2(_ context.Context, _ string) error     { return nil }
func (m *mockRiskSentinel) IsTenantIsolated(_ string) bool                        { return false }
func (m *mockRiskSentinel) FrozenSpecs() map[string]risk.RingEntry                { return nil }
func (m *mockRiskSentinel) IsolatedTenants() map[string]risk.RingEntry            { return nil }

func TestNewPositionManager(t *testing.T) {
	riskSentinel := &mockRiskSentinel{shouldAllow: true, maxSize: 100, maxLeverage: 5}
	pm := NewPositionManager(nil, riskSentinel, logrus.New())

	assert.NotNil(t, pm)
	count := pm.GetOpenPositionsCount(context.Background())
	assert.Equal(t, 0, count)
}

func TestOpenPosition_Success(t *testing.T) {
	riskSentinel := &mockRiskSentinel{shouldAllow: true, maxSize: 100, maxLeverage: 5}
	pm := NewPositionManager(nil, riskSentinel, logrus.New())

	pos := &Position{
		Symbol:     "BTC-USDT",
		Side:       "long",
		EntryPrice: 50000.0,
		Size:       0.1,
		Leverage:   2.0,
		StopLoss:   49000.0,
		TakeProfit: 51000.0,
	}

	err := pm.OpenPosition(context.Background(), pos)
	require.NoError(t, err)
	assert.NotEmpty(t, pos.ID)
	assert.Equal(t, PositionOpen, pos.Status)
}

func TestOpenPosition_RiskRejection(t *testing.T) {
	riskSentinel := &mockRiskSentinel{shouldAllow: false}
	pm := NewPositionManager(nil, riskSentinel, logrus.New())

	pos := &Position{
		Symbol:     "BTC-USDT",
		Side:       "long",
		EntryPrice: 50000.0,
		Size:       0.1,
		Leverage:   2.0,
	}

	err := pm.OpenPosition(context.Background(), pos)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "risk sentinel rejected")
}

func TestClosePosition_Success(t *testing.T) {
	riskSentinel := &mockRiskSentinel{shouldAllow: true, maxSize: 100, maxLeverage: 5}
	pm := NewPositionManager(nil, riskSentinel, logrus.New())

	pos := &Position{
		Symbol:     "BTC-USDT",
		Side:       "long",
		EntryPrice: 50000.0,
		Size:       0.1,
		Leverage:   2.0,
	}

	err := pm.OpenPosition(context.Background(), pos)
	require.NoError(t, err)

	err = pm.ClosePosition(context.Background(), pos.ID, "test")
	require.NoError(t, err)

	count := pm.GetOpenPositionsCount(context.Background())
	assert.Equal(t, 0, count)
}

func TestGetTotalExposure(t *testing.T) {
	riskSentinel := &mockRiskSentinel{shouldAllow: true, maxSize: 100, maxLeverage: 5}
	pm := NewPositionManager(nil, riskSentinel, logrus.New())

	pos := &Position{
		Symbol:     "BTC-USDT",
		Side:       "long",
		EntryPrice: 50000.0,
		Size:       0.1,
		Leverage:   2.0,
	}

	err := pm.OpenPosition(context.Background(), pos)
	require.NoError(t, err)

	// Expected: 0.1 * 50000 * 2 = 10000
	exposure, err := pm.GetTotalExposure(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 10000.0, exposure)
}
