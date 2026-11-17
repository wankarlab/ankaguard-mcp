package risk

import (
	"context"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRiskSentinel(t *testing.T) {
	config := &RiskConfig{
		MaxDrawdownDaily:     5.0,
		MaxDrawdownWeekly:    10.0,
		MaxDrawdownMonthly:   20.0,
		MaxConsecutiveLosses: 3,
		MaxExposurePerSymbol: 50.0,
		MaxExposureTotal:     80.0,
	}

	sentinel := NewRiskSentinel(config, 1000.0, logrus.New())

	assert.NotNil(t, sentinel)

	state, err := sentinel.GetRiskState(context.Background())
	require.NoError(t, err)
	assert.Equal(t, Normal, state.RiskMode)
	assert.False(t, state.KillSwitchActive)
	assert.Equal(t, 0, state.ConsecutiveLosses)
}

func TestEvaluateAction_KillSwitchActive(t *testing.T) {
	config := &RiskConfig{
		MaxDrawdownDaily: 5.0,
	}

	sentinel := NewRiskSentinel(config, 1000.0, logrus.New())

	// Activate kill switch
	err := sentinel.ActivateKillSwitch(context.Background(), "Test")
	require.NoError(t, err)

	// Try to evaluate action
	action := &TradingAction{
		Type:     ActionOpenPosition,
		Symbol:   "BTC-USDT",
		Side:     "long",
		Size:     0.1,
		Leverage: 2.0,
		Price:    50000.0,
	}

	decision, err := sentinel.EvaluateAction(context.Background(), action)
	require.NoError(t, err)
	assert.False(t, decision.Allowed)
	assert.Contains(t, decision.Reason, "kill switch")
}

func TestEvaluateAction_DrawdownLimitExceeded(t *testing.T) {
	config := &RiskConfig{
		MaxDrawdownDaily:     5.0,
		MaxDrawdownWeekly:    10.0,
		MaxDrawdownMonthly:   20.0,
		MaxConsecutiveLosses: 3,
	}

	sentinel := NewRiskSentinel(config, 1000.0, logrus.New()).(*sentinel)

	// Simulate a loss that exceeds daily drawdown
	trade := &Trade{
		Symbol:     "BTC-USDT",
		Side:       "long",
		Size:       0.1,
		EntryPrice: 50000.0,
		ExitPrice:  47000.0,
		PnL:        -60.0, // 6% loss
		Timestamp:  time.Now(),
	}

	err := sentinel.UpdateState(context.Background(), trade)
	require.NoError(t, err)

	// Try to open new position
	action := &TradingAction{
		Type:     ActionOpenPosition,
		Symbol:   "ETH-USDT",
		Side:     "long",
		Size:     1.0,
		Leverage: 2.0,
		Price:    3000.0,
	}

	decision, err := sentinel.EvaluateAction(context.Background(), action)
	require.NoError(t, err)
	assert.False(t, decision.Allowed)
	assert.Contains(t, decision.Reason, "Daily drawdown limit exceeded")
}

func TestEvaluateAction_ConsecutiveLossesExceeded(t *testing.T) {
	config := &RiskConfig{
		MaxDrawdownDaily:     50.0, // High to not trigger
		MaxDrawdownWeekly:    50.0, // High to not trigger
		MaxDrawdownMonthly:   50.0, // High to not trigger
		MaxConsecutiveLosses: 3,
	}

	sentinel := NewRiskSentinel(config, 1000.0, logrus.New())

	// Simulate 3 consecutive losses (small losses to not trigger drawdown)
	for i := 0; i < 3; i++ {
		trade := &Trade{
			Symbol:     "BTC-USDT",
			Side:       "long",
			Size:       0.001,
			EntryPrice: 50000.0,
			ExitPrice:  49900.0,
			PnL:        -0.1, // Very small loss
			Timestamp:  time.Now(),
		}

		err := sentinel.UpdateState(context.Background(), trade)
		require.NoError(t, err)
	}

	// Try to open new position
	action := &TradingAction{
		Type:     ActionOpenPosition,
		Symbol:   "ETH-USDT",
		Side:     "long",
		Size:     1.0,
		Leverage: 2.0,
		Price:    3000.0,
	}

	decision, err := sentinel.EvaluateAction(context.Background(), action)
	require.NoError(t, err)
	assert.False(t, decision.Allowed)
	assert.Contains(t, decision.Reason, "Max consecutive losses reached")
}

func TestEvaluateAction_ExposureLimitExceeded(t *testing.T) {
	config := &RiskConfig{
		MaxDrawdownDaily:     50.0,
		MaxConsecutiveLosses: 10,
		MaxExposureTotal:     50.0, // 50% of capital
	}

	sentinel := NewRiskSentinel(config, 1000.0, logrus.New())

	// Try to open position that exceeds exposure limit
	action := &TradingAction{
		Type:     ActionOpenPosition,
		Symbol:   "BTC-USDT",
		Side:     "long",
		Size:     0.02,
		Leverage: 1.0,
		Price:    50000.0, // 0.02 * 50000 = 1000 (100% of capital)
	}

	decision, err := sentinel.EvaluateAction(context.Background(), action)
	require.NoError(t, err)
	assert.False(t, decision.Allowed)
	assert.Contains(t, decision.Reason, "Total exposure limit exceeded")
}

func TestEvaluateAction_SymbolExposureLimitExceeded(t *testing.T) {
	config := &RiskConfig{
		MaxDrawdownDaily:     50.0,
		MaxConsecutiveLosses: 10,
		MaxExposureTotal:     80.0,
		MaxExposurePerSymbol: 30.0, // 30% per symbol
	}

	sentinel := NewRiskSentinel(config, 1000.0, logrus.New()).(*sentinel)

	// Add existing exposure for BTC-USDT
	sentinel.UpdateExposure("BTC-USDT", 0.005, 50000.0) // 250 USD

	// Try to add more exposure to same symbol
	action := &TradingAction{
		Type:     ActionOpenPosition,
		Symbol:   "BTC-USDT",
		Side:     "long",
		Size:     0.002,
		Leverage: 1.0,
		Price:    50000.0, // 100 USD, total would be 350 (35%)
	}

	decision, err := sentinel.EvaluateAction(context.Background(), action)
	require.NoError(t, err)
	assert.False(t, decision.Allowed)
	assert.Contains(t, decision.Reason, "Symbol exposure limit exceeded")
}

func TestEvaluateAction_LeverageWarning(t *testing.T) {
	config := &RiskConfig{
		MaxDrawdownDaily:     50.0,
		MaxConsecutiveLosses: 10,
		MaxExposureTotal:     80.0,
		MaxExposurePerSymbol: 50.0,
	}

	sentinel := NewRiskSentinel(config, 1000.0, logrus.New())

	// Try to use high leverage (Normal mode allows 2x)
	action := &TradingAction{
		Type:     ActionOpenPosition,
		Symbol:   "BTC-USDT",
		Side:     "long",
		Size:     0.001,
		Leverage: 5.0, // Exceeds Normal mode limit
		Price:    50000.0,
	}

	decision, err := sentinel.EvaluateAction(context.Background(), action)
	require.NoError(t, err)
	assert.True(t, decision.Allowed) // Still allowed but with warning
	assert.NotEmpty(t, decision.Warnings)
	assert.Contains(t, decision.Warnings[0], "Leverage")
}

func TestUpdateState_ConsecutiveLossesReset(t *testing.T) {
	config := &RiskConfig{
		MaxDrawdownDaily:     50.0,
		MaxConsecutiveLosses: 3,
	}

	sentinel := NewRiskSentinel(config, 1000.0, logrus.New())

	// Simulate 2 losses
	for i := 0; i < 2; i++ {
		trade := &Trade{
			Symbol:     "BTC-USDT",
			Side:       "long",
			Size:       0.01,
			EntryPrice: 50000.0,
			ExitPrice:  49000.0,
			PnL:        -10.0,
			Timestamp:  time.Now(),
		}

		err := sentinel.UpdateState(context.Background(), trade)
		require.NoError(t, err)
	}

	state, _ := sentinel.GetRiskState(context.Background())
	assert.Equal(t, 2, state.ConsecutiveLosses)

	// Simulate a profit
	profitTrade := &Trade{
		Symbol:     "BTC-USDT",
		Side:       "long",
		Size:       0.01,
		EntryPrice: 50000.0,
		ExitPrice:  51000.0,
		PnL:        10.0,
		Timestamp:  time.Now(),
	}

	err := sentinel.UpdateState(context.Background(), profitTrade)
	require.NoError(t, err)

	state, _ = sentinel.GetRiskState(context.Background())
	assert.Equal(t, 0, state.ConsecutiveLosses) // Should be reset
}

func TestCircuitBreaker_ReduceRiskMode(t *testing.T) {
	config := &RiskConfig{
		MaxDrawdownDaily:     50.0,
		MaxConsecutiveLosses: 10,
		CircuitBreakers: []CircuitBreakerConfig{
			{
				Trigger:   "consecutive_losses",
				Threshold: 2,
				Action:    "reduce_risk_mode",
			},
		},
	}

	sentinel := NewRiskSentinel(config, 1000.0, logrus.New()).(*sentinel)

	// Set to Aggressive mode
	sentinel.SetRiskMode(Aggressive)

	// Simulate 2 losses to trigger circuit breaker
	for i := 0; i < 2; i++ {
		trade := &Trade{
			Symbol:     "BTC-USDT",
			Side:       "long",
			Size:       0.01,
			EntryPrice: 50000.0,
			ExitPrice:  49000.0,
			PnL:        -10.0,
			Timestamp:  time.Now(),
		}

		err := sentinel.UpdateState(context.Background(), trade)
		require.NoError(t, err)
	}

	state, _ := sentinel.GetRiskState(context.Background())
	assert.Equal(t, Normal, state.RiskMode) // Should be reduced from Aggressive to Normal
}

func TestCircuitBreaker_PauseTrading(t *testing.T) {
	config := &RiskConfig{
		MaxDrawdownDaily:     50.0,
		MaxConsecutiveLosses: 10,
		CircuitBreakers: []CircuitBreakerConfig{
			{
				Trigger:   "daily_drawdown",
				Threshold: 3.0,
				Action:    "pause_trading",
			},
		},
	}

	sentinel := NewRiskSentinel(config, 1000.0, logrus.New())

	// Simulate a loss that triggers circuit breaker
	trade := &Trade{
		Symbol:     "BTC-USDT",
		Side:       "long",
		Size:       0.1,
		EntryPrice: 50000.0,
		ExitPrice:  47000.0,
		PnL:        -40.0, // 4% loss
		Timestamp:  time.Now(),
	}

	err := sentinel.UpdateState(context.Background(), trade)
	require.NoError(t, err)

	state, _ := sentinel.GetRiskState(context.Background())
	assert.True(t, state.KillSwitchActive) // Should activate kill switch
	assert.Contains(t, state.KillSwitchReason, "Circuit breaker")
}

func TestKillSwitch_ActivateDeactivate(t *testing.T) {
	config := &RiskConfig{
		MaxDrawdownDaily: 5.0,
	}

	sentinel := NewRiskSentinel(config, 1000.0, logrus.New())

	// Activate
	err := sentinel.ActivateKillSwitch(context.Background(), "Manual test")
	require.NoError(t, err)

	state, _ := sentinel.GetRiskState(context.Background())
	assert.True(t, state.KillSwitchActive)
	assert.Equal(t, "Manual test", state.KillSwitchReason)

	// Try to activate again (should fail)
	err = sentinel.ActivateKillSwitch(context.Background(), "Another reason")
	assert.Error(t, err)

	// Deactivate
	err = sentinel.DeactivateKillSwitch(context.Background())
	require.NoError(t, err)

	state, _ = sentinel.GetRiskState(context.Background())
	assert.False(t, state.KillSwitchActive)
	assert.Empty(t, state.KillSwitchReason)
}

func TestResetDrawdown(t *testing.T) {
	config := &RiskConfig{
		MaxDrawdownDaily: 50.0,
	}

	sentinel := NewRiskSentinel(config, 1000.0, logrus.New()).(*sentinel)

	// Set some drawdown
	sentinel.state.CurrentDrawdownDaily = 5.0
	sentinel.state.CurrentDrawdownWeekly = 8.0
	sentinel.state.CurrentDrawdownMonthly = 10.0

	// Reset daily
	err := sentinel.ResetDrawdown(context.Background(), "daily")
	require.NoError(t, err)

	state, _ := sentinel.GetRiskState(context.Background())
	assert.Equal(t, 0.0, state.CurrentDrawdownDaily)
	assert.Equal(t, 8.0, state.CurrentDrawdownWeekly)
	assert.Equal(t, 10.0, state.CurrentDrawdownMonthly)

	// Reset all
	err = sentinel.ResetDrawdown(context.Background(), "all")
	require.NoError(t, err)

	state, _ = sentinel.GetRiskState(context.Background())
	assert.Equal(t, 0.0, state.CurrentDrawdownDaily)
	assert.Equal(t, 0.0, state.CurrentDrawdownWeekly)
	assert.Equal(t, 0.0, state.CurrentDrawdownMonthly)
}

func TestSetRiskMode(t *testing.T) {
	config := &RiskConfig{
		MaxDrawdownDaily: 5.0,
	}

	sentinel := NewRiskSentinel(config, 1000.0, logrus.New()).(*sentinel)

	// Set to Aggressive
	err := sentinel.SetRiskMode(Aggressive)
	require.NoError(t, err)

	state, _ := sentinel.GetRiskState(context.Background())
	assert.Equal(t, Aggressive, state.RiskMode)

	// Try invalid mode
	err = sentinel.SetRiskMode(RiskMode(999))
	assert.Error(t, err)
}

func TestGetMaxLeverageByRiskMode(t *testing.T) {
	config := &RiskConfig{
		MaxDrawdownDaily: 5.0,
	}

	sentinel := NewRiskSentinel(config, 1000.0, logrus.New()).(*sentinel)

	tests := []struct {
		mode             RiskMode
		expectedLeverage float64
	}{
		{Conservative, 1.0},
		{Normal, 2.0},
		{Aggressive, 3.0},
		{ControlledCrazy, 5.0},
	}

	for _, tt := range tests {
		t.Run(tt.mode.String(), func(t *testing.T) {
			sentinel.SetRiskMode(tt.mode)
			leverage := sentinel.getMaxLeverageByRiskMode()
			assert.Equal(t, tt.expectedLeverage, leverage)
		})
	}
}
