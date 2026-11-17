package server

import (
	"context"
	"fmt"
	"testing"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/config"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMCPToolExecutionConsistency verifies that both HTTP and JSON-RPC endpoints
// call the same MCP tool with identical parameters and return consistent results
func TestMCPToolExecutionConsistency(t *testing.T) {
	// Setup test server
	cfg := &config.Config{
		Server: config.ServerConfig{
			Protocol: "http",
			Host:     "localhost",
			Port:     0, // Use random port for testing
		},
	}

	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	// Mock GCT client
	gctClient := &gctclient.Client{}

	server := NewMCPServer(cfg, logger, gctClient)

	// Register a mock backtest tool for testing
	mockTool := &MockBacktestTool{
		callCount:  0,
		lastParams: nil,
	}

	err := server.RegisterTool(mockTool)
	require.NoError(t, err)

	ctx := context.Background()

	// Test cases with different parameter combinations
	testCases := []struct {
		name   string
		params map[string]interface{}
	}{
		{
			name: "DOGE-USDT RSI_MACD",
			params: map[string]interface{}{
				"exchange":   "kucoin",
				"pair":       "DOGE-USDT",
				"strategy":   "rsi_macd",
				"start_date": "2024-11-01",
				"end_date":   "2024-12-13",
			},
		},
		{
			name: "BTC-USDT Bollinger Bands",
			params: map[string]interface{}{
				"exchange":   "binance",
				"pair":       "BTC-USDT",
				"strategy":   "bollinger_bands",
				"start_date": "2024-10-01",
				"end_date":   "2024-11-01",
			},
		},
		{
			name: "ETH-USDT Moving Average",
			params: map[string]interface{}{
				"exchange":   "kucoin",
				"pair":       "ETH-USDT",
				"strategy":   "moving_average",
				"start_date": "2024-09-01",
				"end_date":   "2024-10-01",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset mock tool state
			mockTool.callCount = 0
			mockTool.lastParams = nil

			// Test 1: Call via shared callBacktestTool method (used by HTTP endpoint)
			result1, err1 := server.callBacktestTool(ctx, tc.params)
			require.NoError(t, err1)
			require.NotNil(t, result1)

			// Capture first call parameters
			firstCallParams := mockTool.lastParams
			firstCallCount := mockTool.callCount

			// Reset mock tool state
			mockTool.callCount = 0
			mockTool.lastParams = nil

			// Test 2: Call via JSON-RPC method (used by JSON-RPC endpoint)
			mcpReq := &models.MCPRequest{
				JSONRPC: "2.0",
				Method:  "tools/call",
				Params: &models.MCPParams{
					Name:      "backtest_strategy",
					Arguments: tc.params,
				},
				ID: 1,
			}

			mcpResp := server.HandleRequest(ctx, mcpReq)
			require.Nil(t, mcpResp.Error, "JSON-RPC call should not return error")
			require.NotNil(t, mcpResp.Result)

			// Capture second call parameters
			secondCallParams := mockTool.lastParams
			secondCallCount := mockTool.callCount

			// Verify consistency
			assert.Equal(t, firstCallCount, secondCallCount, "Both methods should call the tool exactly once")
			assert.Equal(t, firstCallParams, secondCallParams, "Both methods should pass identical parameters")

			// Verify results are consistent (both should return the same mock data)
			result2, ok := mcpResp.Result.(map[string]interface{})
			require.True(t, ok, "JSON-RPC result should be a map")

			assert.Equal(t, result1, result2, "Both methods should return identical results")

			// Verify all required parameters were passed
			requiredParams := []string{"exchange", "pair", "strategy", "start_date", "end_date"}
			for _, param := range requiredParams {
				assert.Contains(t, firstCallParams, param, "Required parameter %s should be present", param)
				assert.Contains(t, secondCallParams, param, "Required parameter %s should be present", param)
			}

			t.Logf("✅ Consistency verified for %s: both methods called tool %d times with identical parameters", tc.name, firstCallCount)
		})
	}
}

// TestBacktestToolParameterValidation tests parameter validation in the shared method
func TestBacktestToolParameterValidation(t *testing.T) {
	// Setup test server
	cfg := &config.Config{}
	logger := logrus.New()
	gctClient := &gctclient.Client{}
	server := NewMCPServer(cfg, logger, gctClient)

	ctx := context.Background()

	// Test cases for parameter validation
	testCases := []struct {
		name        string
		params      map[string]interface{}
		expectError bool
		errorMsg    string
	}{
		{
			name: "Valid parameters",
			params: map[string]interface{}{
				"exchange":   "kucoin",
				"pair":       "DOGE-USDT",
				"strategy":   "rsi_macd",
				"start_date": "2024-11-01",
				"end_date":   "2024-12-13",
			},
			expectError: false,
		},
		{
			name: "Missing exchange",
			params: map[string]interface{}{
				"pair":       "DOGE-USDT",
				"strategy":   "rsi_macd",
				"start_date": "2024-11-01",
				"end_date":   "2024-12-13",
			},
			expectError: true,
			errorMsg:    "missing required parameter: exchange",
		},
		{
			name: "Missing pair",
			params: map[string]interface{}{
				"exchange":   "kucoin",
				"strategy":   "rsi_macd",
				"start_date": "2024-11-01",
				"end_date":   "2024-12-13",
			},
			expectError: true,
			errorMsg:    "missing required parameter: pair",
		},
		{
			name: "Missing strategy",
			params: map[string]interface{}{
				"exchange":   "kucoin",
				"pair":       "DOGE-USDT",
				"start_date": "2024-11-01",
				"end_date":   "2024-12-13",
			},
			expectError: true,
			errorMsg:    "missing required parameter: strategy",
		},
		{
			name: "Missing start_date",
			params: map[string]interface{}{
				"exchange": "kucoin",
				"pair":     "DOGE-USDT",
				"strategy": "rsi_macd",
				"end_date": "2024-12-13",
			},
			expectError: true,
			errorMsg:    "missing required parameter: start_date",
		},
		{
			name: "Missing end_date",
			params: map[string]interface{}{
				"exchange":   "kucoin",
				"pair":       "DOGE-USDT",
				"strategy":   "rsi_macd",
				"start_date": "2024-11-01",
			},
			expectError: true,
			errorMsg:    "missing required parameter: end_date",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := server.callBacktestTool(ctx, tc.params)

			if tc.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.errorMsg)
				assert.Nil(t, result)
				t.Logf("✅ Expected error caught: %v", err)
			} else {
				// For valid parameters, we expect a "tool not found" error since we haven't registered the real tool
				// This is expected behavior in the test environment
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "tool 'backtest_strategy' not found")
				t.Logf("✅ Parameter validation passed, tool not found as expected in test environment")
			}
		})
	}
}

// MockBacktestTool is a mock implementation of the backtest tool for testing
type MockBacktestTool struct {
	callCount  int
	lastParams map[string]interface{}
}

func (m *MockBacktestTool) Name() string {
	return "backtest_strategy"
}

func (m *MockBacktestTool) Description() string {
	return "Mock backtest strategy tool for testing"
}

func (m *MockBacktestTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name",
			},
			"pair": map[string]interface{}{
				"type":        "string",
				"description": "Trading pair",
			},
			"strategy": map[string]interface{}{
				"type":        "string",
				"description": "Strategy name",
			},
			"start_date": map[string]interface{}{
				"type":        "string",
				"description": "Start date",
			},
			"end_date": map[string]interface{}{
				"type":        "string",
				"description": "End date",
			},
		},
		"required": []string{"exchange", "pair", "strategy", "start_date", "end_date"},
	}
}

func (m *MockBacktestTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	m.callCount++
	m.lastParams = make(map[string]interface{})

	// Deep copy parameters to avoid reference issues
	for k, v := range params {
		m.lastParams[k] = v
	}

	// Return consistent mock data
	return map[string]interface{}{
		"roi":            15.5,
		"sharpe_ratio":   1.8,
		"max_drawdown":   -8.2,
		"total_trades":   45,
		"winning_trades": 28,
		"losing_trades":  17,
		"trades":         []interface{}{},
	}, nil
}

// Property-based test for MCP tool execution consistency
func TestProperty_MCPToolExecutionConsistency(t *testing.T) {
	// This test verifies the property: "Both HTTP and JSON-RPC endpoints should call
	// the same MCP tool with identical parameters and return consistent results"

	cfg := &config.Config{}
	logger := logrus.New()
	logger.SetLevel(logrus.WarnLevel) // Reduce noise in tests
	gctClient := &gctclient.Client{}
	server := NewMCPServer(cfg, logger, gctClient)

	// Register mock tool
	mockTool := &MockBacktestTool{}
	err := server.RegisterTool(mockTool)
	require.NoError(t, err)

	ctx := context.Background()

	// Generate test cases with various parameter combinations
	exchanges := []string{"kucoin", "binance", "bybit"}
	pairs := []string{"DOGE-USDT", "BTC-USDT", "ETH-USDT"}
	strategies := []string{"rsi_macd", "bollinger_bands", "moving_average"}

	testCount := 0
	maxTests := 10 // Limit for reasonable test execution time

	for _, exchange := range exchanges {
		for _, pair := range pairs {
			for _, strategy := range strategies {
				if testCount >= maxTests {
					break
				}

				params := map[string]interface{}{
					"exchange":   exchange,
					"pair":       pair,
					"strategy":   strategy,
					"start_date": "2024-11-01",
					"end_date":   "2024-12-13",
				}

				t.Run(fmt.Sprintf("Property_Test_%d_%s_%s_%s", testCount+1, exchange, pair, strategy), func(t *testing.T) {
					// Reset mock state
					mockTool.callCount = 0
					mockTool.lastParams = nil

					// Call via HTTP method (callBacktestTool)
					result1, err1 := server.callBacktestTool(ctx, params)
					httpParams := make(map[string]interface{})
					for k, v := range mockTool.lastParams {
						httpParams[k] = v
					}
					httpCallCount := mockTool.callCount

					// Reset mock state
					mockTool.callCount = 0
					mockTool.lastParams = nil

					// Call via JSON-RPC method
					mcpReq := &models.MCPRequest{
						JSONRPC: "2.0",
						Method:  "tools/call",
						Params: &models.MCPParams{
							Name:      "backtest_strategy",
							Arguments: params,
						},
						ID: 1,
					}

					mcpResp := server.HandleRequest(ctx, mcpReq)
					jsonrpcParams := make(map[string]interface{})
					for k, v := range mockTool.lastParams {
						jsonrpcParams[k] = v
					}
					jsonrpcCallCount := mockTool.callCount

					// Verify the property holds
					require.NoError(t, err1)
					require.Nil(t, mcpResp.Error)
					require.NotNil(t, result1)
					require.NotNil(t, mcpResp.Result)

					// Property: Both methods should call the tool exactly once
					assert.Equal(t, 1, httpCallCount, "HTTP method should call tool exactly once")
					assert.Equal(t, 1, jsonrpcCallCount, "JSON-RPC method should call tool exactly once")

					// Property: Both methods should pass identical parameters
					assert.Equal(t, httpParams, jsonrpcParams, "Both methods should pass identical parameters")

					// Property: Both methods should return consistent results
					result2, ok := mcpResp.Result.(map[string]interface{})
					require.True(t, ok)
					assert.Equal(t, result1, result2, "Both methods should return identical results")

					t.Logf("✅ Property verified for %s/%s/%s", exchange, pair, strategy)
				})

				testCount++
			}
		}
	}

	t.Logf("🎯 Property-based test completed: %d test cases verified MCP tool execution consistency", testCount)
}
