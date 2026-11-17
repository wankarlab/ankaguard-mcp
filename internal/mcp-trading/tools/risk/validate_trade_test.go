package risk

import (
	"context"
	"errors"
	"testing"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/config"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	internalRisk "github.com/rantipay/trading-mcp/internal/risk"
	"github.com/rantipay/trading-mcp/internal/trading"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockRiskSentinel implements the internalRisk.RiskSentinel interface for testing
type mockRiskSentinel struct {
	riskState         *internalRisk.RiskState
	evaluateDecision  *internalRisk.RiskDecision
	evaluateError     error
	updateStateError  error
	killSwitchError   error
	resetDrawdownErr  error
}

func newMockRiskSentinel() *mockRiskSentinel {
	return &mockRiskSentinel{
		riskState: &internalRisk.RiskState{
			RiskMode:             internalRisk.Normal,
			CurrentDrawdownDaily: 0,
			ExposureBySymbol:     make(map[string]float64),
		},
		evaluateDecision: &internalRisk.RiskDecision{
			Allowed:     true,
			MaxSize:     10.0,
			MaxLeverage: 2.0,
			Warnings:    []string{},
		},
	}
}

func (m *mockRiskSentinel) EvaluateAction(ctx context.Context, action *internalRisk.TradingAction) (*internalRisk.RiskDecision, error) {
	if m.evaluateError != nil {
		return nil, m.evaluateError
	}
	return m.evaluateDecision, nil
}

func (m *mockRiskSentinel) UpdateState(ctx context.Context, trade *internalRisk.Trade) error {
	return m.updateStateError
}

func (m *mockRiskSentinel) GetRiskState(ctx context.Context) (*internalRisk.RiskState, error) {
	return m.riskState, nil
}

func (m *mockRiskSentinel) ActivateKillSwitch(ctx context.Context, reason string) error {
	return m.killSwitchError
}

func (m *mockRiskSentinel) DeactivateKillSwitch(ctx context.Context) error {
	return m.killSwitchError
}

func (m *mockRiskSentinel) ResetDrawdown(ctx context.Context, period string) error {
	return m.resetDrawdownErr
}

func (m *mockRiskSentinel) ActivateRing1(_ context.Context, _, _ string) error  { return nil }
func (m *mockRiskSentinel) DeactivateRing1(_ context.Context, _ string) error   { return nil }
func (m *mockRiskSentinel) IsSpecFrozen(_ string) bool                           { return false }
func (m *mockRiskSentinel) ActivateRing2(_ context.Context, _, _ string) error  { return nil }
func (m *mockRiskSentinel) DeactivateRing2(_ context.Context, _ string) error   { return nil }
func (m *mockRiskSentinel) IsTenantIsolated(_ string) bool                      { return false }
func (m *mockRiskSentinel) FrozenSpecs() map[string]internalRisk.RingEntry      { return nil }
func (m *mockRiskSentinel) IsolatedTenants() map[string]internalRisk.RingEntry  { return nil }

// mockPositionManager implements the trading.PositionTracker interface for testing
type mockPositionManager struct {
	exposureBySymbol map[string]float64
	totalExposure    float64
	openPositions    []*trading.Position
	positionsCount   int
	exposureError    error
}

func newMockPositionManager() *mockPositionManager {
	return &mockPositionManager{
		exposureBySymbol: make(map[string]float64),
		openPositions:    make([]*trading.Position, 0),
	}
}

func (m *mockPositionManager) OpenPosition(ctx context.Context, pos *trading.Position) error {
	return nil
}

func (m *mockPositionManager) ClosePosition(ctx context.Context, positionID string, reason string) error {
	return nil
}

func (m *mockPositionManager) PartialClosePosition(ctx context.Context, positionID string, sizeToClose float64, reason string) error {
	return nil
}

func (m *mockPositionManager) UpdatePosition(ctx context.Context, pos *trading.Position) error {
	return nil
}

func (m *mockPositionManager) GetPosition(ctx context.Context, positionID string) (*trading.Position, error) {
	return nil, nil
}

func (m *mockPositionManager) GetOpenPositions(ctx context.Context) ([]*trading.Position, error) {
	return m.openPositions, nil
}

func (m *mockPositionManager) GetOpenPositionsCount(ctx context.Context) int {
	return m.positionsCount
}

func (m *mockPositionManager) GetTotalExposure(ctx context.Context) (float64, error) {
	if m.exposureError != nil {
		return 0, m.exposureError
	}
	return m.totalExposure, nil
}

func (m *mockPositionManager) GetExposureBySymbol(ctx context.Context, symbol string) (float64, error) {
	if m.exposureError != nil {
		return 0, m.exposureError
	}
	if exposure, ok := m.exposureBySymbol[symbol]; ok {
		return exposure, nil
	}
	return 0, nil
}

func (m *mockPositionManager) CheckConflict(ctx context.Context, newPos *trading.Position) (bool, string, error) {
	return false, "", nil
}

func (m *mockPositionManager) GetPositionsByStrategy(ctx context.Context, strategy string) ([]*trading.Position, error) {
	return nil, nil
}

func (m *mockPositionManager) GetPositionHistory(ctx context.Context, limit int) ([]*trading.Position, error) {
	return nil, nil
}

// createTestConfig creates a test configuration with default risk limits
func createTestConfig() *config.Config {
	return &config.Config{
		RiskLimits: config.RiskLimitsConfig{
			MaxOrderSizeUSD:       10000.0,
			MaxDailyLossUSD:       500.0,
			MaxPositionSizeUSD:    50000.0,
			MaxOpenOrders:         10,
			MaxExposurePerPairUSD: 20000.0,
		},
	}
}

// TestValidateTradeTool_Name verifies the tool name
func TestValidateTradeTool_Name(t *testing.T) {
	cfg := createTestConfig()
	tool := NewValidateTradeTool(nil, cfg, nil, nil)

	assert.Equal(t, "validate_trade", tool.Name())
}

// TestValidateTradeTool_InputSchema verifies the input schema structure
func TestValidateTradeTool_InputSchema(t *testing.T) {
	cfg := createTestConfig()
	tool := NewValidateTradeTool(nil, cfg, nil, nil)

	schema := tool.InputSchema()

	// Verify schema type
	assert.Equal(t, "object", schema["type"])

	// Verify required fields
	required, ok := schema["required"].([]string)
	require.True(t, ok)
	assert.Contains(t, required, "exchange")
	assert.Contains(t, required, "pair")
	assert.Contains(t, required, "side")
	assert.Contains(t, required, "amount")
	assert.Contains(t, required, "price")

	// Verify properties exist
	properties, ok := schema["properties"].(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, properties, "exchange")
	assert.Contains(t, properties, "pair")
	assert.Contains(t, properties, "side")
	assert.Contains(t, properties, "amount")
	assert.Contains(t, properties, "price")
	assert.Contains(t, properties, "market_type")
	assert.Contains(t, properties, "leverage")
	assert.Contains(t, properties, "order_type")

	// Verify side enum
	sideProps, ok := properties["side"].(map[string]interface{})
	require.True(t, ok)
	sideEnum, ok := sideProps["enum"].([]string)
	require.True(t, ok)
	assert.Contains(t, sideEnum, "buy")
	assert.Contains(t, sideEnum, "sell")

	// Verify market_type enum
	marketProps, ok := properties["market_type"].(map[string]interface{})
	require.True(t, ok)
	marketEnum, ok := marketProps["enum"].([]string)
	require.True(t, ok)
	assert.Contains(t, marketEnum, "spot")
	assert.Contains(t, marketEnum, "futures")
	assert.Contains(t, marketEnum, "margin")
}

// TestValidateTrade_ValidOrder tests a valid order that passes all validations
func TestValidateTrade_ValidOrder(t *testing.T) {
	cfg := createTestConfig()
	riskSentinel := newMockRiskSentinel()
	positionManager := newMockPositionManager()

	tool := NewValidateTradeTool(nil, cfg, riskSentinel, positionManager)

	args := map[string]interface{}{
		"exchange":    "kucoin",
		"pair":        "BTC-USDT",
		"side":        "buy",
		"amount":      0.01,
		"price":       50000.0,
		"market_type": "spot",
		"leverage":    1.0,
		"order_type":  "limit",
	}

	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)

	validationResult, ok := result.(*ValidationResult)
	require.True(t, ok)

	assert.True(t, validationResult.Valid)
	assert.Equal(t, "kucoin", validationResult.Order.Exchange)
	assert.Equal(t, "BTC-USDT", validationResult.Order.Pair)
	assert.Equal(t, "buy", validationResult.Order.Side)
	assert.Equal(t, 0.01, validationResult.Order.Amount)
	assert.Equal(t, 50000.0, validationResult.Order.Price)
	assert.Equal(t, 500.0, validationResult.Order.ValueUSD) // 0.01 * 50000
	assert.Empty(t, validationResult.Errors)
}

// TestValidateTrade_InvalidParams tests various invalid parameter scenarios
func TestValidateTrade_InvalidParams(t *testing.T) {
	cfg := createTestConfig()
	tool := NewValidateTradeTool(nil, cfg, nil, nil)

	tests := []struct {
		name        string
		args        map[string]interface{}
		expectedErr string
	}{
		{
			name: "missing exchange",
			args: map[string]interface{}{
				"pair":   "BTC-USDT",
				"side":   "buy",
				"amount": 0.01,
				"price":  50000.0,
			},
			expectedErr: "exchange is required",
		},
		{
			name: "empty exchange",
			args: map[string]interface{}{
				"exchange": "",
				"pair":     "BTC-USDT",
				"side":     "buy",
				"amount":   0.01,
				"price":    50000.0,
			},
			expectedErr: "exchange is required",
		},
		{
			name: "missing pair",
			args: map[string]interface{}{
				"exchange": "kucoin",
				"side":     "buy",
				"amount":   0.01,
				"price":    50000.0,
			},
			expectedErr: "pair is required",
		},
		{
			name: "invalid side",
			args: map[string]interface{}{
				"exchange": "kucoin",
				"pair":     "BTC-USDT",
				"side":     "invalid",
				"amount":   0.01,
				"price":    50000.0,
			},
			expectedErr: "side must be 'buy' or 'sell'",
		},
		{
			name: "zero amount",
			args: map[string]interface{}{
				"exchange": "kucoin",
				"pair":     "BTC-USDT",
				"side":     "buy",
				"amount":   0.0,
				"price":    50000.0,
			},
			expectedErr: "amount must be a positive number",
		},
		{
			name: "negative amount",
			args: map[string]interface{}{
				"exchange": "kucoin",
				"pair":     "BTC-USDT",
				"side":     "buy",
				"amount":   -0.01,
				"price":    50000.0,
			},
			expectedErr: "amount must be a positive number",
		},
		{
			name: "zero price",
			args: map[string]interface{}{
				"exchange": "kucoin",
				"pair":     "BTC-USDT",
				"side":     "buy",
				"amount":   0.01,
				"price":    0.0,
			},
			expectedErr: "price must be a positive number",
		},
		{
			name: "negative price",
			args: map[string]interface{}{
				"exchange": "kucoin",
				"pair":     "BTC-USDT",
				"side":     "buy",
				"amount":   0.01,
				"price":    -50000.0,
			},
			expectedErr: "price must be a positive number",
		},
		{
			name: "wrong type for exchange",
			args: map[string]interface{}{
				"exchange": 123,
				"pair":     "BTC-USDT",
				"side":     "buy",
				"amount":   0.01,
				"price":    50000.0,
			},
			expectedErr: "exchange is required",
		},
		{
			name: "wrong type for amount",
			args: map[string]interface{}{
				"exchange": "kucoin",
				"pair":     "BTC-USDT",
				"side":     "buy",
				"amount":   "invalid",
				"price":    50000.0,
			},
			expectedErr: "amount must be a positive number",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tool.Execute(context.Background(), tt.args)
			require.Error(t, err)

			mcpErr, ok := err.(*models.MCPError)
			require.True(t, ok)
			assert.Equal(t, -32602, mcpErr.Code)
			assert.Contains(t, mcpErr.Data.(string), tt.expectedErr)
		})
	}
}

// TestValidateTrade_InsufficientBalance tests detection of insufficient balance
func TestValidateTrade_InsufficientBalance(t *testing.T) {
	// This test verifies the balance validation logic
	// Since gctclient is nil, the client won't be connected and balance check will add a warning
	cfg := createTestConfig()
	riskSentinel := newMockRiskSentinel()
	positionManager := newMockPositionManager()

	tool := NewValidateTradeTool(nil, cfg, riskSentinel, positionManager)

	args := map[string]interface{}{
		"exchange": "kucoin",
		"pair":     "BTC-USDT",
		"side":     "buy",
		"amount":   0.01,
		"price":    50000.0,
	}

	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)

	validationResult, ok := result.(*ValidationResult)
	require.True(t, ok)

	// With no client connected, we should have a warning about balance check
	balanceCheck, exists := validationResult.Validations["balance_check"]
	require.True(t, exists)
	assert.Contains(t, balanceCheck.Message, "Cannot verify balance")
}

// TestValidateTrade_ExceedsOrderSize tests detection of order size exceeding limits
func TestValidateTrade_ExceedsOrderSize(t *testing.T) {
	cfg := createTestConfig()
	cfg.RiskLimits.MaxOrderSizeUSD = 100.0 // Set a low limit

	riskSentinel := newMockRiskSentinel()
	positionManager := newMockPositionManager()

	tool := NewValidateTradeTool(nil, cfg, riskSentinel, positionManager)

	// Order value: 1 BTC * 50000 = 50000 USD, exceeds limit of 100 USD
	args := map[string]interface{}{
		"exchange": "kucoin",
		"pair":     "BTC-USDT",
		"side":     "buy",
		"amount":   1.0,
		"price":    50000.0,
	}

	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)

	validationResult, ok := result.(*ValidationResult)
	require.True(t, ok)

	assert.False(t, validationResult.Valid)
	assert.NotEmpty(t, validationResult.Errors)

	// Check that order size error is present
	foundOrderSizeError := false
	for _, errMsg := range validationResult.Errors {
		if contains(errMsg, "Order value") && contains(errMsg, "exceeds max order size") {
			foundOrderSizeError = true
			break
		}
	}
	assert.True(t, foundOrderSizeError, "Expected order size error not found")
}

// TestValidateTrade_ExceedsPositionLimit tests detection of position limit exceeded
func TestValidateTrade_ExceedsPositionLimit(t *testing.T) {
	cfg := createTestConfig()
	cfg.RiskLimits.MaxPositionSizeUSD = 1000.0 // Set a low limit

	riskSentinel := newMockRiskSentinel()
	positionManager := newMockPositionManager()
	// Set existing exposure close to limit
	positionManager.exposureBySymbol["BTC-USDT"] = 900.0

	tool := NewValidateTradeTool(nil, cfg, riskSentinel, positionManager)

	// Order value: 0.01 BTC * 50000 = 500 USD
	// Current: 900 + 500 = 1400 > 1000 limit
	args := map[string]interface{}{
		"exchange": "kucoin",
		"pair":     "BTC-USDT",
		"side":     "buy",
		"amount":   0.01,
		"price":    50000.0,
	}

	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)

	validationResult, ok := result.(*ValidationResult)
	require.True(t, ok)

	assert.False(t, validationResult.Valid)

	// Check for position limit error
	posCheck, exists := validationResult.Validations["position_limit"]
	require.True(t, exists)
	assert.False(t, posCheck.Passed)
	assert.Contains(t, posCheck.Message, "Position would exceed limit")
}

// TestValidateTrade_ExceedsLeverage tests detection of excessive leverage
func TestValidateTrade_ExceedsLeverage(t *testing.T) {
	cfg := createTestConfig()
	riskSentinel := newMockRiskSentinel()
	// Set risk mode to Conservative (max leverage 1.0)
	riskSentinel.riskState.RiskMode = internalRisk.Conservative

	positionManager := newMockPositionManager()

	tool := NewValidateTradeTool(nil, cfg, riskSentinel, positionManager)

	// Request leverage 5x while in Conservative mode (max 1x)
	args := map[string]interface{}{
		"exchange":    "kucoin",
		"pair":        "BTC-USDT",
		"side":        "buy",
		"amount":      0.01,
		"price":       50000.0,
		"market_type": "futures",
		"leverage":    5.0,
	}

	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)

	validationResult, ok := result.(*ValidationResult)
	require.True(t, ok)

	assert.False(t, validationResult.Valid)

	// Check for leverage limit error
	leverageCheck, exists := validationResult.Validations["leverage_limit"]
	require.True(t, exists)
	assert.False(t, leverageCheck.Passed)
	assert.Contains(t, leverageCheck.Message, "Leverage")
	assert.Contains(t, leverageCheck.Message, "exceeds maximum")
}

// TestValidateTrade_RiskSentinelRejects tests when risk sentinel rejects the trade
func TestValidateTrade_RiskSentinelRejects(t *testing.T) {
	cfg := createTestConfig()
	riskSentinel := newMockRiskSentinel()
	riskSentinel.evaluateDecision = &internalRisk.RiskDecision{
		Allowed:     false,
		Reason:      "Daily drawdown limit exceeded",
		MaxSize:     0.0,
		MaxLeverage: 0.0,
		Warnings:    []string{},
	}

	positionManager := newMockPositionManager()

	tool := NewValidateTradeTool(nil, cfg, riskSentinel, positionManager)

	args := map[string]interface{}{
		"exchange": "kucoin",
		"pair":     "BTC-USDT",
		"side":     "buy",
		"amount":   0.01,
		"price":    50000.0,
	}

	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)

	validationResult, ok := result.(*ValidationResult)
	require.True(t, ok)

	assert.False(t, validationResult.Valid)

	// Check for risk sentinel rejection
	riskCheck, exists := validationResult.Validations["risk_sentinel"]
	require.True(t, exists)
	assert.False(t, riskCheck.Passed)
	assert.Contains(t, riskCheck.Message, "Risk sentinel rejected")
}

// TestValidateTrade_RiskSentinelWithWarnings tests when risk sentinel approves with warnings
func TestValidateTrade_RiskSentinelWithWarnings(t *testing.T) {
	cfg := createTestConfig()
	riskSentinel := newMockRiskSentinel()
	riskSentinel.evaluateDecision = &internalRisk.RiskDecision{
		Allowed:     true,
		MaxSize:     10.0,
		MaxLeverage: 2.0,
		Warnings:    []string{"Approaching daily loss limit", "High volatility detected"},
	}

	positionManager := newMockPositionManager()

	tool := NewValidateTradeTool(nil, cfg, riskSentinel, positionManager)

	args := map[string]interface{}{
		"exchange": "kucoin",
		"pair":     "BTC-USDT",
		"side":     "buy",
		"amount":   0.01,
		"price":    50000.0,
	}

	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)

	validationResult, ok := result.(*ValidationResult)
	require.True(t, ok)

	assert.True(t, validationResult.Valid)
	assert.Contains(t, validationResult.Warnings, "Approaching daily loss limit")
	assert.Contains(t, validationResult.Warnings, "High volatility detected")
}

// TestGetOrderSizeLimits tests order size limit calculations for different currencies
func TestGetOrderSizeLimits(t *testing.T) {
	cfg := createTestConfig()
	tool := NewValidateTradeTool(nil, cfg, nil, nil)

	tests := []struct {
		name         string
		exchange     string
		pair         string
		marketType   string
		expectedMin  float64
		expectedMax  float64
	}{
		{
			name:         "BTC spot",
			exchange:     "kucoin",
			pair:         "BTC-USDT",
			marketType:   "spot",
			expectedMin:  0.0001,
			expectedMax:  100.0,
		},
		{
			name:         "ETH spot",
			exchange:     "kucoin",
			pair:         "ETH-USDT",
			marketType:   "spot",
			expectedMin:  0.001,
			expectedMax:  1000.0,
		},
		{
			name:         "XRP spot",
			exchange:     "kucoin",
			pair:         "XRP-USDT",
			marketType:   "spot",
			expectedMin:  1.0,
			expectedMax:  10000000.0,
		},
		{
			name:         "DOGE spot",
			exchange:     "kucoin",
			pair:         "DOGE-USDT",
			marketType:   "spot",
			expectedMin:  1.0,
			expectedMax:  10000000.0,
		},
		{
			name:         "SHIB spot",
			exchange:     "binance",
			pair:         "SHIB-USDT",
			marketType:   "spot",
			expectedMin:  1.0,
			expectedMax:  10000000.0,
		},
		{
			name:         "SOL spot",
			exchange:     "kucoin",
			pair:         "SOL-USDT",
			marketType:   "spot",
			expectedMin:  0.01,
			expectedMax:  10000.0,
		},
		{
			name:         "DOT spot",
			exchange:     "binance",
			pair:         "DOT-USDT",
			marketType:   "spot",
			expectedMin:  0.01,
			expectedMax:  10000.0,
		},
		{
			name:         "LINK spot",
			exchange:     "kucoin",
			pair:         "LINK-USDT",
			marketType:   "spot",
			expectedMin:  0.01,
			expectedMax:  10000.0,
		},
		{
			name:         "Unknown token",
			exchange:     "kucoin",
			pair:         "UNKNOWN-USDT",
			marketType:   "spot",
			expectedMin:  0.001,
			expectedMax:  10000.0,
		},
		{
			name:         "BTC futures (adjusted limits)",
			exchange:     "kucoin",
			pair:         "BTC-USDT",
			marketType:   "futures",
			expectedMin:  0.00001, // 0.0001 * 0.1
			expectedMax:  1000.0,  // 100 * 10
		},
		{
			name:         "ETH futures (adjusted limits)",
			exchange:     "kucoin",
			pair:         "ETH-USDT",
			marketType:   "futures",
			expectedMin:  0.0001, // 0.001 * 0.1
			expectedMax:  10000.0, // 1000 * 10
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limits := tool.getOrderSizeLimits(tt.exchange, tt.pair, tt.marketType)

			assert.InDelta(t, tt.expectedMin, limits.MinOrderSize, 0.00001, "MinOrderSize mismatch")
			assert.InDelta(t, tt.expectedMax, limits.MaxOrderSize, 0.001, "MaxOrderSize mismatch")
		})
	}
}

// TestValidateTrade_DefaultOptionalParams tests that optional parameters have correct defaults
func TestValidateTrade_DefaultOptionalParams(t *testing.T) {
	cfg := createTestConfig()
	riskSentinel := newMockRiskSentinel()
	positionManager := newMockPositionManager()

	tool := NewValidateTradeTool(nil, cfg, riskSentinel, positionManager)

	// Only provide required parameters
	args := map[string]interface{}{
		"exchange": "kucoin",
		"pair":     "BTC-USDT",
		"side":     "buy",
		"amount":   0.01,
		"price":    50000.0,
	}

	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)

	validationResult, ok := result.(*ValidationResult)
	require.True(t, ok)

	// Verify defaults
	assert.Equal(t, "spot", validationResult.Order.MarketType)
	assert.Equal(t, 1.0, validationResult.Order.Leverage)
	assert.Equal(t, "limit", validationResult.Order.OrderType)
}

// TestValidateTrade_SellOrderBalanceCheck tests balance check for sell orders
func TestValidateTrade_SellOrderBalanceCheck(t *testing.T) {
	cfg := createTestConfig()
	riskSentinel := newMockRiskSentinel()
	positionManager := newMockPositionManager()

	tool := NewValidateTradeTool(nil, cfg, riskSentinel, positionManager)

	args := map[string]interface{}{
		"exchange": "kucoin",
		"pair":     "BTC-USDT",
		"side":     "sell",
		"amount":   0.5,
		"price":    50000.0,
	}

	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)

	validationResult, ok := result.(*ValidationResult)
	require.True(t, ok)

	// For sell orders, the required currency should be the base currency (BTC)
	balanceCheck := validationResult.Validations["balance_check"]
	details, ok := balanceCheck.Details.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "BTC", details["currency"])
	assert.Equal(t, 0.5, details["required"]) // For sell, required = amount
}

// TestValidateTrade_BuyOrderBalanceCheck tests balance check for buy orders
func TestValidateTrade_BuyOrderBalanceCheck(t *testing.T) {
	cfg := createTestConfig()
	riskSentinel := newMockRiskSentinel()
	positionManager := newMockPositionManager()

	tool := NewValidateTradeTool(nil, cfg, riskSentinel, positionManager)

	args := map[string]interface{}{
		"exchange": "kucoin",
		"pair":     "BTC-USDT",
		"side":     "buy",
		"amount":   0.5,
		"price":    50000.0,
	}

	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)

	validationResult, ok := result.(*ValidationResult)
	require.True(t, ok)

	// For buy orders, the required currency should be the quote currency (USDT)
	balanceCheck := validationResult.Validations["balance_check"]
	details, ok := balanceCheck.Details.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "USDT", details["currency"])
	assert.Equal(t, 25000.0, details["required"]) // For buy, required = amount * price
}

// TestValidateTrade_OrderBelowMinSize tests detection of orders below minimum size
func TestValidateTrade_OrderBelowMinSize(t *testing.T) {
	cfg := createTestConfig()
	riskSentinel := newMockRiskSentinel()
	positionManager := newMockPositionManager()

	tool := NewValidateTradeTool(nil, cfg, riskSentinel, positionManager)

	// BTC min is 0.0001, try with 0.00001
	args := map[string]interface{}{
		"exchange": "kucoin",
		"pair":     "BTC-USDT",
		"side":     "buy",
		"amount":   0.00001,
		"price":    50000.0,
	}

	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)

	validationResult, ok := result.(*ValidationResult)
	require.True(t, ok)

	assert.False(t, validationResult.Valid)

	orderSizeCheck := validationResult.Validations["order_size"]
	assert.False(t, orderSizeCheck.Passed)
	assert.Contains(t, orderSizeCheck.Message, "below minimum")
}

// TestValidateTrade_OrderAboveMaxSize tests detection of orders above maximum size
func TestValidateTrade_OrderAboveMaxSize(t *testing.T) {
	cfg := createTestConfig()
	riskSentinel := newMockRiskSentinel()
	positionManager := newMockPositionManager()

	tool := NewValidateTradeTool(nil, cfg, riskSentinel, positionManager)

	// BTC max is 100, try with 150
	args := map[string]interface{}{
		"exchange": "kucoin",
		"pair":     "BTC-USDT",
		"side":     "buy",
		"amount":   150.0,
		"price":    50000.0,
	}

	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)

	validationResult, ok := result.(*ValidationResult)
	require.True(t, ok)

	assert.False(t, validationResult.Valid)

	orderSizeCheck := validationResult.Validations["order_size"]
	assert.False(t, orderSizeCheck.Passed)
	assert.Contains(t, orderSizeCheck.Message, "exceeds maximum")
}

// TestValidateTrade_NilRiskSentinel tests validation when risk sentinel is nil
func TestValidateTrade_NilRiskSentinel(t *testing.T) {
	cfg := createTestConfig()
	positionManager := newMockPositionManager()

	tool := NewValidateTradeTool(nil, cfg, nil, positionManager)

	args := map[string]interface{}{
		"exchange": "kucoin",
		"pair":     "BTC-USDT",
		"side":     "buy",
		"amount":   0.01,
		"price":    50000.0,
	}

	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)

	validationResult, ok := result.(*ValidationResult)
	require.True(t, ok)

	// Should have a warning about risk sentinel not available
	riskCheck, exists := validationResult.Validations["risk_sentinel"]
	require.True(t, exists)
	assert.Contains(t, riskCheck.Message, "Risk sentinel not available")
}

// TestValidateTrade_NilPositionManager tests validation when position manager is nil
func TestValidateTrade_NilPositionManager(t *testing.T) {
	cfg := createTestConfig()
	riskSentinel := newMockRiskSentinel()

	tool := NewValidateTradeTool(nil, cfg, riskSentinel, nil)

	args := map[string]interface{}{
		"exchange": "kucoin",
		"pair":     "BTC-USDT",
		"side":     "buy",
		"amount":   0.01,
		"price":    50000.0,
	}

	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)

	validationResult, ok := result.(*ValidationResult)
	require.True(t, ok)

	// Position limit check should still work (assumes 0 current exposure)
	posCheck, exists := validationResult.Validations["position_limit"]
	require.True(t, exists)
	assert.True(t, posCheck.Passed)
}

// TestValidateTrade_ApproachingPositionLimit tests warning when approaching position limit
func TestValidateTrade_ApproachingPositionLimit(t *testing.T) {
	cfg := createTestConfig()
	cfg.RiskLimits.MaxPositionSizeUSD = 1000.0

	riskSentinel := newMockRiskSentinel()
	positionManager := newMockPositionManager()
	// Set exposure at 75% of limit
	positionManager.exposureBySymbol["BTC-USDT"] = 750.0

	tool := NewValidateTradeTool(nil, cfg, riskSentinel, positionManager)

	// Adding 100 USD brings total to 850 (85% of limit) - should trigger warning
	args := map[string]interface{}{
		"exchange": "kucoin",
		"pair":     "BTC-USDT",
		"side":     "buy",
		"amount":   0.002,
		"price":    50000.0, // 100 USD
	}

	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)

	validationResult, ok := result.(*ValidationResult)
	require.True(t, ok)

	assert.True(t, validationResult.Valid)

	// Should have a warning about approaching limit
	posCheck := validationResult.Validations["position_limit"]
	assert.True(t, posCheck.Passed)
	assert.Contains(t, posCheck.Message, "approaching threshold")
}

// TestValidateTrade_RiskSentinelError tests handling of risk sentinel evaluation error
func TestValidateTrade_RiskSentinelError(t *testing.T) {
	cfg := createTestConfig()
	riskSentinel := newMockRiskSentinel()
	riskSentinel.evaluateError = errors.New("connection timeout")

	positionManager := newMockPositionManager()

	tool := NewValidateTradeTool(nil, cfg, riskSentinel, positionManager)

	args := map[string]interface{}{
		"exchange": "kucoin",
		"pair":     "BTC-USDT",
		"side":     "buy",
		"amount":   0.01,
		"price":    50000.0,
	}

	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)

	validationResult, ok := result.(*ValidationResult)
	require.True(t, ok)

	// Should have a warning about evaluation failure
	riskCheck := validationResult.Validations["risk_sentinel"]
	assert.Contains(t, riskCheck.Message, "Risk evaluation failed")
}

// TestValidateTrade_LeverageLimitsPerRiskMode tests leverage limits for different risk modes
func TestValidateTrade_LeverageLimitsPerRiskMode(t *testing.T) {
	tests := []struct {
		name        string
		riskMode    internalRisk.RiskMode
		leverage    float64
		marketType  string
		shouldPass  bool
	}{
		{
			name:       "Conservative mode allows 1x",
			riskMode:   internalRisk.Conservative,
			leverage:   1.0,
			marketType: "futures",
			shouldPass: true,
		},
		{
			name:       "Conservative mode rejects 2x",
			riskMode:   internalRisk.Conservative,
			leverage:   2.0,
			marketType: "futures",
			shouldPass: false,
		},
		{
			name:       "Normal mode allows 2x",
			riskMode:   internalRisk.Normal,
			leverage:   2.0,
			marketType: "futures",
			shouldPass: true,
		},
		{
			name:       "Normal mode rejects 3x",
			riskMode:   internalRisk.Normal,
			leverage:   3.0,
			marketType: "futures",
			shouldPass: false,
		},
		{
			name:       "Aggressive mode allows 3x",
			riskMode:   internalRisk.Aggressive,
			leverage:   3.0,
			marketType: "futures",
			shouldPass: true,
		},
		{
			name:       "ControlledCrazy mode allows 5x",
			riskMode:   internalRisk.ControlledCrazy,
			leverage:   5.0,
			marketType: "futures",
			shouldPass: true,
		},
		{
			name:       "Spot trading ignores leverage",
			riskMode:   internalRisk.Conservative,
			leverage:   1.0,
			marketType: "spot",
			shouldPass: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := createTestConfig()
			riskSentinel := newMockRiskSentinel()
			riskSentinel.riskState.RiskMode = tt.riskMode

			positionManager := newMockPositionManager()

			tool := NewValidateTradeTool(nil, cfg, riskSentinel, positionManager)

			args := map[string]interface{}{
				"exchange":    "kucoin",
				"pair":        "BTC-USDT",
				"side":        "buy",
				"amount":      0.01,
				"price":       50000.0,
				"market_type": tt.marketType,
				"leverage":    tt.leverage,
			}

			result, err := tool.Execute(context.Background(), args)
			require.NoError(t, err)

			validationResult, ok := result.(*ValidationResult)
			require.True(t, ok)

			leverageCheck := validationResult.Validations["leverage_limit"]
			assert.Equal(t, tt.shouldPass, leverageCheck.Passed,
				"Expected leverage check to pass: %v, but got: %v", tt.shouldPass, leverageCheck.Passed)
		})
	}
}

// TestValidateTrade_MarketHoursWarnings tests market hours warning for specific exchanges
func TestValidateTrade_MarketHoursWarnings(t *testing.T) {
	cfg := createTestConfig()
	riskSentinel := newMockRiskSentinel()
	positionManager := newMockPositionManager()

	tool := NewValidateTradeTool(nil, cfg, riskSentinel, positionManager)

	// Test that market hours check always passes for crypto (24/7)
	args := map[string]interface{}{
		"exchange": "binance",
		"pair":     "BTC-USDT",
		"side":     "buy",
		"amount":   0.01,
		"price":    50000.0,
	}

	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)

	validationResult, ok := result.(*ValidationResult)
	require.True(t, ok)

	marketCheck := validationResult.Validations["market_hours"]
	assert.True(t, marketCheck.Passed)
	assert.Contains(t, marketCheck.Message, "24/7")
}

// TestValidateTrade_SummaryGeneration tests that summary is correctly generated
func TestValidateTrade_SummaryGeneration(t *testing.T) {
	cfg := createTestConfig()
	cfg.RiskLimits.MaxOrderSizeUSD = 100000.0 // Increase limit to allow order
	riskSentinel := newMockRiskSentinel()
	positionManager := newMockPositionManager()

	tool := NewValidateTradeTool(nil, cfg, riskSentinel, positionManager)

	args := map[string]interface{}{
		"exchange":    "kucoin",
		"pair":        "BTC-USDT",
		"side":        "buy",
		"amount":      0.01, // Smaller amount: 0.01 * 50000 = 500 USD
		"price":       50000.0,
		"market_type": "spot",
		"order_type":  "limit",
	}

	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)

	validationResult, ok := result.(*ValidationResult)
	require.True(t, ok)

	// Check summary contains key information
	assert.Contains(t, validationResult.Summary, "VALIDATION PASSED")
	assert.Contains(t, validationResult.Summary, "BUY")
	assert.Contains(t, validationResult.Summary, "BTC-USDT")
	assert.Contains(t, validationResult.Summary, "kucoin")
	assert.Contains(t, validationResult.Summary, "limit")
	assert.Contains(t, validationResult.Summary, "spot")
	assert.Contains(t, validationResult.Summary, "Checks:")
}

// TestValidateTrade_FailedValidationSummary tests summary for failed validation
func TestValidateTrade_FailedValidationSummary(t *testing.T) {
	cfg := createTestConfig()
	cfg.RiskLimits.MaxPositionSizeUSD = 100.0 // Very low limit

	riskSentinel := newMockRiskSentinel()
	positionManager := newMockPositionManager()
	positionManager.exposureBySymbol["BTC-USDT"] = 90.0 // Already near limit

	tool := NewValidateTradeTool(nil, cfg, riskSentinel, positionManager)

	args := map[string]interface{}{
		"exchange": "kucoin",
		"pair":     "BTC-USDT",
		"side":     "buy",
		"amount":   0.01,
		"price":    50000.0, // 500 USD, will exceed limit
	}

	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)

	validationResult, ok := result.(*ValidationResult)
	require.True(t, ok)

	assert.Contains(t, validationResult.Summary, "VALIDATION FAILED")
	assert.Contains(t, validationResult.Summary, "Errors:")
}

// TestValidateTrade_PairFormattingNormalization tests that pairs are normalized
func TestValidateTrade_PairFormattingNormalization(t *testing.T) {
	cfg := createTestConfig()
	riskSentinel := newMockRiskSentinel()
	positionManager := newMockPositionManager()

	tool := NewValidateTradeTool(nil, cfg, riskSentinel, positionManager)

	// Use lowercase pair
	args := map[string]interface{}{
		"exchange": "KUCOIN",
		"pair":     "btc-usdt",
		"side":     "buy",
		"amount":   0.01,
		"price":    50000.0,
	}

	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)

	validationResult, ok := result.(*ValidationResult)
	require.True(t, ok)

	// Exchange should be lowercased, pair should be uppercased
	assert.Equal(t, "kucoin", validationResult.Order.Exchange)
	assert.Equal(t, "BTC-USDT", validationResult.Order.Pair)
}

// TestValidateTradeTool_Description tests tool description
func TestValidateTradeTool_Description(t *testing.T) {
	cfg := createTestConfig()
	tool := NewValidateTradeTool(nil, cfg, nil, nil)

	description := tool.Description()

	assert.NotEmpty(t, description)
	assert.Contains(t, description, "Validate")
	assert.Contains(t, description, "trade")
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
