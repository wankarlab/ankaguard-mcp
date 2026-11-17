package orders

import (
	"context"
	"testing"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockGCTClient is a mock implementation for testing
type MockGCTClient struct {
	mock.Mock
}

func (m *MockGCTClient) GetTicker(ctx context.Context, exchange, pair string) (*models.Ticker, error) {
	args := m.Called(ctx, exchange, pair)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Ticker), args.Error(1)
}

func (m *MockGCTClient) IsConnected() bool {
	args := m.Called()
	return args.Bool(0)
}

// createTestSimulateOrderTool creates a SimulateOrderTool with a nil client for unit testing
// Tests that don't call Execute can use this directly
func createTestSimulateOrderTool() *SimulateOrderTool {
	return &SimulateOrderTool{
		BaseTool: BaseTool{client: nil},
	}
}

// TestSimulateOrderTool_Name verifies the tool returns the correct name
func TestSimulateOrderTool_Name(t *testing.T) {
	tool := createTestSimulateOrderTool()

	assert.Equal(t, "simulate_order", tool.Name())
}

// TestSimulateOrderTool_Description verifies the description is not empty
func TestSimulateOrderTool_Description(t *testing.T) {
	tool := createTestSimulateOrderTool()

	desc := tool.Description()
	assert.NotEmpty(t, desc)
	assert.Contains(t, desc, "Simulate")
}

// TestSimulateOrderTool_InputSchema verifies the schema has all required fields
func TestSimulateOrderTool_InputSchema(t *testing.T) {
	tool := createTestSimulateOrderTool()

	schema := tool.InputSchema()

	// Verify schema type
	assert.Equal(t, "object", schema["type"])

	// Verify properties exist
	properties, ok := schema["properties"].(map[string]interface{})
	require.True(t, ok, "properties should be a map")

	// Check required fields exist in properties
	requiredFields := []string{"exchange", "pair", "side", "type", "amount"}
	for _, field := range requiredFields {
		_, exists := properties[field]
		assert.True(t, exists, "property %s should exist", field)
	}

	// Check optional fields exist
	optionalFields := []string{"price", "market_type", "slippage_percent", "maker_fee_percent", "taker_fee_percent"}
	for _, field := range optionalFields {
		_, exists := properties[field]
		assert.True(t, exists, "optional property %s should exist", field)
	}

	// Verify required array
	required, ok := schema["required"].([]string)
	require.True(t, ok, "required should be a string array")
	assert.ElementsMatch(t, requiredFields, required)

	// Verify side enum
	sideProps, ok := properties["side"].(map[string]interface{})
	require.True(t, ok)
	sideEnum, ok := sideProps["enum"].([]string)
	require.True(t, ok)
	assert.ElementsMatch(t, []string{"buy", "sell"}, sideEnum)

	// Verify type enum
	typeProps, ok := properties["type"].(map[string]interface{})
	require.True(t, ok)
	typeEnum, ok := typeProps["enum"].([]string)
	require.True(t, ok)
	assert.ElementsMatch(t, []string{"market", "limit", "stop"}, typeEnum)
}

// TestCalculateExecution_MarketOrder verifies slippage is applied for market orders
func TestCalculateExecution_MarketOrder(t *testing.T) {
	tool := createTestSimulateOrderTool()

	testCases := []struct {
		name            string
		side            string
		amount          float64
		marketPrice     float64
		slippagePercent float64
		expectedPrice   float64
		takerFee        float64
	}{
		{
			name:            "buy market order with slippage",
			side:            "buy",
			amount:          1.0,
			marketPrice:     50000.0,
			slippagePercent: 0.05,
			expectedPrice:   50025.0, // 50000 + (50000 * 0.05/100) = 50025
			takerFee:        0.10,
		},
		{
			name:            "sell market order with slippage",
			side:            "sell",
			amount:          1.0,
			marketPrice:     50000.0,
			slippagePercent: 0.05,
			expectedPrice:   49975.0, // 50000 - (50000 * 0.05/100) = 49975
			takerFee:        0.10,
		},
		{
			name:            "buy with higher slippage",
			side:            "buy",
			amount:          2.0,
			marketPrice:     100.0,
			slippagePercent: 0.10,
			expectedPrice:   100.10, // 100 + (100 * 0.10/100) = 100.10
			takerFee:        0.10,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			execution := tool.calculateExecution(
				tc.side,
				"market",
				tc.amount,
				0, // no limit price for market orders
				tc.marketPrice,
				tc.slippagePercent,
				DefaultMakerFeePercent,
				tc.takerFee,
			)

			// Verify execution price includes slippage
			assert.InDelta(t, tc.expectedPrice, execution.ExecutionPrice, 0.00000001)

			// Verify slippage percent is recorded
			assert.Equal(t, tc.slippagePercent, execution.SlippagePercent)

			// Verify slippage amount is calculated correctly
			expectedSlippageAmount := tc.marketPrice * (tc.slippagePercent / 100)
			assert.InDelta(t, expectedSlippageAmount, execution.SlippageAmount, 0.00000001)

			// Verify fee percent is taker fee for market orders
			assert.Equal(t, tc.takerFee, execution.FeePercent)

			// Verify total cost/proceeds calculation
			orderValue := tc.amount * execution.ExecutionPrice
			fee := orderValue * (tc.takerFee / 100)

			if tc.side == "buy" {
				expectedTotalCost := orderValue + fee
				assert.InDelta(t, expectedTotalCost, execution.TotalCost, 0.01)
				assert.Equal(t, 0.0, execution.TotalProceeds)
			} else {
				expectedProceeds := orderValue - fee
				assert.InDelta(t, expectedProceeds, execution.TotalProceeds, 0.01)
				assert.Equal(t, 0.0, execution.TotalCost)
			}

			// Verify estimated fill time
			assert.Equal(t, EstimatedFillTimeMs, execution.EstFillTimeMs)
		})
	}
}

// TestCalculateExecution_LimitOrder verifies no slippage for limit orders
func TestCalculateExecution_LimitOrder(t *testing.T) {
	tool := createTestSimulateOrderTool()

	testCases := []struct {
		name          string
		side          string
		amount        float64
		limitPrice    float64
		marketPrice   float64
		expectedPrice float64
		makerFee      float64
	}{
		{
			name:          "buy limit order at limit price",
			side:          "buy",
			amount:        1.0,
			limitPrice:    49000.0,
			marketPrice:   50000.0,
			expectedPrice: 49000.0, // executes at limit price
			makerFee:      0.10,
		},
		{
			name:          "sell limit order at limit price",
			side:          "sell",
			amount:        1.0,
			limitPrice:    51000.0,
			marketPrice:   50000.0,
			expectedPrice: 51000.0, // executes at limit price
			makerFee:      0.10,
		},
		{
			name:          "limit order with no limit price falls back to market",
			side:          "buy",
			amount:        1.0,
			limitPrice:    0,
			marketPrice:   50000.0,
			expectedPrice: 50000.0, // falls back to market price
			makerFee:      0.10,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			execution := tool.calculateExecution(
				tc.side,
				"limit",
				tc.amount,
				tc.limitPrice,
				tc.marketPrice,
				DefaultSlippagePercent,
				tc.makerFee,
				DefaultTakerFeePercent,
			)

			// Verify execution price is the limit price (no slippage)
			assert.InDelta(t, tc.expectedPrice, execution.ExecutionPrice, 0.00000001)

			// Verify no slippage for limit orders with valid limit price
			if tc.limitPrice > 0 {
				assert.Equal(t, 0.0, execution.SlippageAmount)
				// Maker fee for limit orders
				assert.Equal(t, tc.makerFee, execution.FeePercent)
			}

			// Verify total cost/proceeds calculation
			orderValue := tc.amount * execution.ExecutionPrice
			fee := orderValue * (execution.FeePercent / 100)

			if tc.side == "buy" {
				expectedTotalCost := orderValue + fee
				assert.InDelta(t, expectedTotalCost, execution.TotalCost, 0.01)
			} else {
				expectedProceeds := orderValue - fee
				assert.InDelta(t, expectedProceeds, execution.TotalProceeds, 0.01)
			}
		})
	}
}

// TestCalculateExecution_StopOrder verifies slippage is applied for stop orders
func TestCalculateExecution_StopOrder(t *testing.T) {
	tool := createTestSimulateOrderTool()

	testCases := []struct {
		name            string
		side            string
		amount          float64
		marketPrice     float64
		slippagePercent float64
		expectedPrice   float64
	}{
		{
			name:            "buy stop order with slippage",
			side:            "buy",
			amount:          1.0,
			marketPrice:     50000.0,
			slippagePercent: 0.05,
			expectedPrice:   50025.0, // same as market order
		},
		{
			name:            "sell stop order with slippage",
			side:            "sell",
			amount:          1.0,
			marketPrice:     50000.0,
			slippagePercent: 0.05,
			expectedPrice:   49975.0, // same as market order
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			execution := tool.calculateExecution(
				tc.side,
				"stop",
				tc.amount,
				0,
				tc.marketPrice,
				tc.slippagePercent,
				DefaultMakerFeePercent,
				DefaultTakerFeePercent,
			)

			// Verify execution price includes slippage (same behavior as market orders)
			assert.InDelta(t, tc.expectedPrice, execution.ExecutionPrice, 0.00000001)

			// Verify slippage is applied
			expectedSlippageAmount := tc.marketPrice * (tc.slippagePercent / 100)
			assert.InDelta(t, expectedSlippageAmount, execution.SlippageAmount, 0.00000001)

			// Verify taker fee for stop orders
			assert.Equal(t, DefaultTakerFeePercent, execution.FeePercent)
		})
	}
}

// TestPerformRiskCheck_PassesWithinLimits verifies small orders pass risk checks
func TestPerformRiskCheck_PassesWithinLimits(t *testing.T) {
	tool := createTestSimulateOrderTool()

	testCases := []struct {
		name           string
		amount         float64
		executionPrice float64
		expectPassed   bool
		expectWarnings int
	}{
		{
			name:           "small order within limits",
			amount:         0.01,
			executionPrice: 50000.0,
			expectPassed:   true,
			expectWarnings: 0, // $500 is within limits
		},
		{
			name:           "medium order within limits",
			amount:         0.1,
			executionPrice: 50000.0,
			expectPassed:   true,
			expectWarnings: 0, // $5000 is within limits
		},
		{
			name:           "order at recommended minimum",
			amount:         0.00002,
			executionPrice: 50000.0,
			expectPassed:   true,
			expectWarnings: 0, // $1 is at minimum
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := tool.performRiskCheck(
				"kucoin",
				"BTC-USDT",
				"buy",
				tc.amount,
				tc.executionPrice,
			)

			assert.Equal(t, tc.expectPassed, result.Passed)
			assert.Len(t, result.Warnings, tc.expectWarnings)

			// Verify order value is in details
			orderValue, ok := result.Details["order_value_usd"].(float64)
			assert.True(t, ok)
			assert.InDelta(t, tc.amount*tc.executionPrice, orderValue, 0.01)
		})
	}
}

// TestPerformRiskCheck_WarnsLargeOrder verifies large orders generate warnings
func TestPerformRiskCheck_WarnsLargeOrder(t *testing.T) {
	tool := createTestSimulateOrderTool()

	testCases := []struct {
		name               string
		amount             float64
		executionPrice     float64
		expectPassed       bool
		minExpectedWarns   int
		expectMaxSizeWarn  bool
		expectLargeAmtWarn bool
	}{
		{
			name:              "order exceeds max size",
			amount:            0.5,
			executionPrice:    50000.0,
			expectPassed:      true,
			minExpectedWarns:  1,
			expectMaxSizeWarn: true, // $25,000 > $10,000 max
		},
		{
			name:               "very large amount",
			amount:             2000000.0,
			executionPrice:     1.0,
			expectPassed:       false,
			minExpectedWarns:   1,
			expectLargeAmtWarn: true, // amount > 1,000,000
		},
		{
			name:             "order below minimum",
			amount:           0.0000001,
			executionPrice:   50000.0,
			expectPassed:     true,
			minExpectedWarns: 1, // $0.005 < $1 minimum
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := tool.performRiskCheck(
				"kucoin",
				"BTC-USDT",
				"buy",
				tc.amount,
				tc.executionPrice,
			)

			assert.Equal(t, tc.expectPassed, result.Passed)
			assert.GreaterOrEqual(t, len(result.Warnings), tc.minExpectedWarns)

			// Check for specific warnings
			if tc.expectMaxSizeWarn {
				found := false
				for _, w := range result.Warnings {
					if contains(w, "exceeds recommended maximum") {
						found = true
						break
					}
				}
				assert.True(t, found, "should contain max size warning")
			}

			if tc.expectLargeAmtWarn {
				found := false
				for _, w := range result.Warnings {
					if contains(w, "Large order amount") {
						found = true
						break
					}
				}
				assert.True(t, found, "should contain large amount warning")
			}
		})
	}
}

// TestRoundTo8Decimals verifies proper rounding to 8 decimal places
func TestRoundTo8Decimals(t *testing.T) {
	testCases := []struct {
		input    float64
		expected float64
	}{
		{input: 0.123456789, expected: 0.12345679},
		{input: 1.0, expected: 1.0},
		{input: 0.00000001, expected: 0.00000001},
		{input: 0.000000001, expected: 0.0},
		{input: 50000.12345678, expected: 50000.12345678},
		{input: 0.999999999, expected: 1.0},
		{input: 0.123456784, expected: 0.12345678},
		{input: 0.123456785, expected: 0.12345679}, // rounds up at 5
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			result := roundTo8Decimals(tc.input)
			assert.InDelta(t, tc.expected, result, 0.000000001)
		})
	}
}

// TestRoundTo2Decimals verifies proper rounding to 2 decimal places
func TestRoundTo2Decimals(t *testing.T) {
	testCases := []struct {
		input    float64
		expected float64
	}{
		{input: 100.456, expected: 100.46},
		{input: 100.454, expected: 100.45},
		{input: 100.455, expected: 100.46}, // rounds up at 5
		{input: 1.0, expected: 1.0},
		{input: 0.01, expected: 0.01},
		{input: 0.001, expected: 0.0},
		{input: 99999.999, expected: 100000.0},
		{input: 50000.125, expected: 50000.13},
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			result := roundTo2Decimals(tc.input)
			assert.InDelta(t, tc.expected, result, 0.001)
		})
	}
}

// TestGetFloatParamWithDefault verifies default value handling
func TestGetFloatParamWithDefault(t *testing.T) {
	testCases := []struct {
		name         string
		params       map[string]interface{}
		key          string
		defaultValue float64
		expected     float64
	}{
		{
			name:         "float64 value present",
			params:       map[string]interface{}{"slippage": 0.10},
			key:          "slippage",
			defaultValue: 0.05,
			expected:     0.10,
		},
		{
			name:         "int value present",
			params:       map[string]interface{}{"slippage": 1},
			key:          "slippage",
			defaultValue: 0.05,
			expected:     1.0,
		},
		{
			name:         "int64 value present",
			params:       map[string]interface{}{"slippage": int64(2)},
			key:          "slippage",
			defaultValue: 0.05,
			expected:     2.0,
		},
		{
			name:         "key not present returns default",
			params:       map[string]interface{}{},
			key:          "slippage",
			defaultValue: 0.05,
			expected:     0.05,
		},
		{
			name:         "invalid type returns default",
			params:       map[string]interface{}{"slippage": "invalid"},
			key:          "slippage",
			defaultValue: 0.05,
			expected:     0.05,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := getFloatParamWithDefault(tc.params, tc.key, tc.defaultValue)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// TestSimulationResult_Structure verifies the structure returned by calculateExecution
func TestSimulationResult_Structure(t *testing.T) {
	tool := createTestSimulateOrderTool()

	execution := tool.calculateExecution(
		"buy",
		"market",
		1.0,
		0,
		50000.0,
		DefaultSlippagePercent,
		DefaultMakerFeePercent,
		DefaultTakerFeePercent,
	)

	// Verify all fields are populated
	assert.Greater(t, execution.MarketPrice, 0.0)
	assert.Greater(t, execution.ExecutionPrice, 0.0)
	assert.GreaterOrEqual(t, execution.SlippagePercent, 0.0)
	assert.GreaterOrEqual(t, execution.SlippageAmount, 0.0)
	assert.GreaterOrEqual(t, execution.Fee, 0.0)
	assert.GreaterOrEqual(t, execution.FeePercent, 0.0)
	assert.Greater(t, execution.TotalCost, 0.0) // buy order has cost
	assert.Equal(t, 0.0, execution.TotalProceeds) // buy order has no proceeds
	assert.Equal(t, EstimatedFillTimeMs, execution.EstFillTimeMs)
}

// TestRiskCheckResult_Structure verifies the structure returned by performRiskCheck
func TestRiskCheckResult_Structure(t *testing.T) {
	tool := createTestSimulateOrderTool()

	result := tool.performRiskCheck(
		"kucoin",
		"BTC-USDT",
		"buy",
		0.1,
		50000.0,
	)

	// Verify all fields are accessible
	assert.IsType(t, true, result.Passed)
	assert.IsType(t, []string{}, result.Warnings)
	assert.NotNil(t, result.Details)

	// Verify order_value_usd is always present in details
	_, ok := result.Details["order_value_usd"]
	assert.True(t, ok, "order_value_usd should be in details")
}

// TestCalculateExecution_DefaultOrderType verifies default case behavior
func TestCalculateExecution_DefaultOrderType(t *testing.T) {
	tool := createTestSimulateOrderTool()

	// Unknown order type should default to market price with taker fee
	execution := tool.calculateExecution(
		"buy",
		"unknown_type",
		1.0,
		0,
		50000.0,
		DefaultSlippagePercent,
		DefaultMakerFeePercent,
		DefaultTakerFeePercent,
	)

	// Should use market price for unknown types
	assert.Equal(t, 50000.0, execution.ExecutionPrice)
	assert.Equal(t, DefaultTakerFeePercent, execution.FeePercent)
}

// TestConstants verifies the default constants are set correctly
func TestConstants(t *testing.T) {
	assert.Equal(t, 0.05, DefaultSlippagePercent)
	assert.Equal(t, 0.10, DefaultMakerFeePercent)
	assert.Equal(t, 0.10, DefaultTakerFeePercent)
	assert.Equal(t, 50, EstimatedFillTimeMs)
}

// Helper function to check if string contains substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}


type fakeTicker struct{ last float64 }

func (f fakeTicker) GetTicker(context.Context, string, string) (*models.Ticker, error) {
	return &models.Ticker{Last: f.last}, nil
}

// The simulation is an estimate, lists its assumptions and follows the real price.
func TestSimulateOrderResultIsLabeledEstimate(t *testing.T) {
	args := map[string]interface{}{"exchange": "kucoin", "pair": "BTC-USDT", "side": "buy", "type": "market", "amount": 1.0, "slippage_percent": 0.2}
	run := func(price float64) map[string]interface{} {
		tool := &SimulateOrderTool{ticker: fakeTicker{last: price}}
		res, err := tool.Execute(context.Background(), args)
		require.NoError(t, err)
		return res.(map[string]interface{})
	}
	r100, r200 := run(100), run(200)
	assert.Equal(t, true, r100["estimate"])
	a := r100["assumptions"].(map[string]interface{})
	assert.Equal(t, 0.2, a["slippage_percent"])
	assert.Equal(t, DefaultTakerFeePercent, a["taker_fee_percent"])
	assert.Contains(t, a, "price_source")
	assert.InDelta(t, 100.2, r100["execution"].(map[string]interface{})["execution_price"], 1e-9)
	assert.InDelta(t, 200.4, r200["execution"].(map[string]interface{})["execution_price"], 1e-9)
}
