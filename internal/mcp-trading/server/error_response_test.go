package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/config"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestErrorResponseFormatConsistency verifies that error responses have consistent format
// across different error scenarios and endpoints
func TestErrorResponseFormatConsistency(t *testing.T) {
	// Setup test server
	cfg := &config.Config{}
	logger := logrus.New()
	logger.SetLevel(logrus.WarnLevel) // Reduce noise in tests
	gctClient := &gctclient.Client{}
	server := NewMCPServer(cfg, logger, gctClient)

	// Test cases for different error scenarios
	testCases := []struct {
		name           string
		endpoint       string
		requestBody    interface{}
		expectedCode   string
		expectedStatus int
	}{
		{
			name:     "Missing symbol validation error",
			endpoint: "/api/v1/backtesting/run-backtest",
			requestBody: map[string]interface{}{
				"strategy":   "rsi_macd",
				"start_date": "2024-11-01",
				"end_date":   "2024-12-13",
			},
			expectedCode:   ErrorCodeValidation,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:     "Invalid date format validation error",
			endpoint: "/api/v1/backtesting/run-backtest",
			requestBody: map[string]interface{}{
				"symbol":     "DOGE-USDT",
				"strategy":   "rsi_macd",
				"start_date": "invalid-date",
				"end_date":   "2024-12-13",
			},
			expectedCode:   ErrorCodeValidation,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:     "Invalid exchange validation error",
			endpoint: "/api/v1/backtesting/run-backtest",
			requestBody: map[string]interface{}{
				"symbol":     "DOGE-USDT",
				"exchange":   "invalid_exchange",
				"strategy":   "rsi_macd",
				"start_date": "2024-11-01",
				"end_date":   "2024-12-13",
			},
			expectedCode:   ErrorCodeValidation,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:     "Future date validation error",
			endpoint: "/api/v1/backtesting/run-backtest",
			requestBody: map[string]interface{}{
				"symbol":     "DOGE-USDT",
				"strategy":   "rsi_macd",
				"start_date": "2099-01-01",
				"end_date":   "2099-01-31",
			},
			expectedCode:   ErrorCodeValidation,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Invalid JSON request error",
			endpoint:       "/api/v1/backtesting/run-backtest",
			requestBody:    "invalid json",
			expectedCode:   ErrorCodeInvalidRequest,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:     "Simple endpoint missing symbol",
			endpoint: "/backtesting",
			requestBody: map[string]interface{}{
				"strategy":   "rsi_macd",
				"start_date": "2024-11-01",
				"end_date":   "2024-12-13",
			},
			expectedCode:   ErrorCodeValidation,
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Prepare request body
			var requestBody []byte
			var err error

			if str, ok := tc.requestBody.(string); ok {
				requestBody = []byte(str)
			} else {
				requestBody, err = json.Marshal(tc.requestBody)
				require.NoError(t, err)
			}

			// Create HTTP request
			req := httptest.NewRequest(http.MethodPost, tc.endpoint, bytes.NewReader(requestBody))
			req.Header.Set("Content-Type", "application/json")

			// Create response recorder
			w := httptest.NewRecorder()

			// Route request to appropriate handler
			if tc.endpoint == "/api/v1/backtesting/run-backtest" {
				server.handleRunBacktest(w, req)
			} else if tc.endpoint == "/backtesting" {
				server.handleBacktestingSimple(w, req)
			}

			// Verify response status
			assert.Equal(t, tc.expectedStatus, w.Code, "HTTP status code should match expected")

			// Parse error response
			var errorResp BacktestError
			err = json.Unmarshal(w.Body.Bytes(), &errorResp)
			require.NoError(t, err, "Error response should be valid JSON")

			// Verify error response structure
			assert.Equal(t, tc.expectedCode, errorResp.Code, "Error code should match expected")
			assert.NotEmpty(t, errorResp.Message, "Error message should not be empty")
			assert.NotEmpty(t, errorResp.Timestamp, "Timestamp should not be empty")

			// Verify timestamp format
			_, err = time.Parse(time.RFC3339, errorResp.Timestamp)
			assert.NoError(t, err, "Timestamp should be in RFC3339 format")

			// Verify request ID format (if present)
			if errorResp.RequestID != "" {
				assert.True(t, strings.HasPrefix(errorResp.RequestID, "REQ_"), "Request ID should have REQ_ prefix")
			}

			t.Logf("✅ Error response format verified for %s: code=%s, status=%d", tc.name, errorResp.Code, w.Code)
		})
	}
}

// TestErrorResponseConsistencyBetweenEndpoints verifies that both endpoints return
// consistent error formats for the same validation errors
func TestErrorResponseConsistencyBetweenEndpoints(t *testing.T) {
	// Setup test server
	cfg := &config.Config{}
	logger := logrus.New()
	logger.SetLevel(logrus.WarnLevel)
	gctClient := &gctclient.Client{}
	server := NewMCPServer(cfg, logger, gctClient)

	// Test cases that should produce identical errors on both endpoints
	testCases := []struct {
		name         string
		requestBody  map[string]interface{}
		expectedCode string
	}{
		{
			name: "Missing symbol",
			requestBody: map[string]interface{}{
				"strategy":   "rsi_macd",
				"start_date": "2024-11-01",
				"end_date":   "2024-12-13",
			},
			expectedCode: ErrorCodeValidation,
		},
		{
			name: "Invalid date format",
			requestBody: map[string]interface{}{
				"symbol":     "DOGE-USDT",
				"strategy":   "rsi_macd",
				"start_date": "invalid-date",
				"end_date":   "2024-12-13",
			},
			expectedCode: ErrorCodeValidation,
		},
		{
			name: "Invalid strategy",
			requestBody: map[string]interface{}{
				"symbol":     "DOGE-USDT",
				"strategy":   "invalid_strategy",
				"start_date": "2024-11-01",
				"end_date":   "2024-12-13",
			},
			expectedCode: ErrorCodeValidation,
		},
	}

	endpoints := []struct {
		name    string
		path    string
		handler func(http.ResponseWriter, *http.Request)
	}{
		{"Consolidated", "/api/v1/backtesting/run-backtest", server.handleRunBacktest},
		{"Simple", "/backtesting", server.handleBacktestingSimple},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var responses []BacktestError

			// Test both endpoints with the same invalid request
			for _, endpoint := range endpoints {
				requestBody, err := json.Marshal(tc.requestBody)
				require.NoError(t, err)

				req := httptest.NewRequest(http.MethodPost, endpoint.path, bytes.NewReader(requestBody))
				req.Header.Set("Content-Type", "application/json")

				w := httptest.NewRecorder()
				endpoint.handler(w, req)

				// Parse error response
				var errorResp BacktestError
				err = json.Unmarshal(w.Body.Bytes(), &errorResp)
				require.NoError(t, err, "Error response should be valid JSON for %s endpoint", endpoint.name)

				responses = append(responses, errorResp)
			}

			// Verify consistency between endpoints
			require.Len(t, responses, 2, "Should have responses from both endpoints")

			consolidated := responses[0]
			simple := responses[1]

			// Error codes should be identical
			assert.Equal(t, consolidated.Code, simple.Code, "Error codes should be identical between endpoints")

			// Error messages should be identical (they use the same validation logic)
			assert.Equal(t, consolidated.Message, simple.Message, "Error messages should be identical between endpoints")

			// Both should have timestamps
			assert.NotEmpty(t, consolidated.Timestamp, "Consolidated endpoint should have timestamp")
			assert.NotEmpty(t, simple.Timestamp, "Simple endpoint should have timestamp")

			t.Logf("✅ Error consistency verified for %s: both endpoints returned code=%s", tc.name, consolidated.Code)
		})
	}
}

// TestProperty_ErrorResponseStructure is a property-based test that verifies
// all error responses follow the same structure regardless of error type
func TestProperty_ErrorResponseStructure(t *testing.T) {
	// This test verifies the property: "All error responses should have consistent structure
	// with required fields: code, message, timestamp, and optional details/request_id"

	cfg := &config.Config{}
	logger := logrus.New()
	logger.SetLevel(logrus.WarnLevel)
	gctClient := &gctclient.Client{}
	server := NewMCPServer(cfg, logger, gctClient)

	// Generate various invalid requests to trigger different error types
	invalidRequests := []struct {
		name string
		body interface{}
		desc string
	}{
		{"empty_body", map[string]interface{}{}, "Empty request body"},
		{"missing_symbol", map[string]interface{}{"strategy": "rsi_macd"}, "Missing required symbol"},
		{"invalid_json", "{ invalid json", "Malformed JSON"},
		{"future_dates", map[string]interface{}{
			"symbol": "DOGE-USDT", "start_date": "2025-01-01", "end_date": "2025-01-31",
		}, "Future dates"},
		{"invalid_exchange", map[string]interface{}{
			"symbol": "DOGE-USDT", "exchange": "fake_exchange", "start_date": "2024-11-01", "end_date": "2024-12-13",
		}, "Invalid exchange"},
		{"invalid_symbol_format", map[string]interface{}{
			"symbol": "INVALID", "start_date": "2024-11-01", "end_date": "2024-12-13",
		}, "Invalid symbol format"},
		{"date_range_too_long", map[string]interface{}{
			"symbol": "DOGE-USDT", "start_date": "2023-01-01", "end_date": "2024-12-31",
		}, "Date range too long"},
	}

	endpoints := []string{"/api/v1/backtesting/run-backtest", "/backtesting"}

	testCount := 0
	for _, endpoint := range endpoints {
		for _, invalidReq := range invalidRequests {
			testCount++
			t.Run(fmt.Sprintf("Property_Test_%d_%s_%s", testCount, strings.TrimPrefix(endpoint, "/"), invalidReq.name), func(t *testing.T) {
				// Prepare request
				var requestBody []byte
				var err error

				if str, ok := invalidReq.body.(string); ok {
					requestBody = []byte(str)
				} else {
					requestBody, err = json.Marshal(invalidReq.body)
					require.NoError(t, err)
				}

				req := httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(requestBody))
				req.Header.Set("Content-Type", "application/json")

				w := httptest.NewRecorder()

				// Route to appropriate handler
				if endpoint == "/api/v1/backtesting/run-backtest" {
					server.handleRunBacktest(w, req)
				} else {
					server.handleBacktestingSimple(w, req)
				}

				// Should return an error status (4xx or 5xx)
				assert.True(t, w.Code >= 400, "Should return error status code for invalid request")

				// Parse response as error
				var errorResp BacktestError
				err = json.Unmarshal(w.Body.Bytes(), &errorResp)
				require.NoError(t, err, "Error response should be valid JSON")

				// Property: All error responses must have these required fields
				assert.NotEmpty(t, errorResp.Code, "Error code is required")
				assert.NotEmpty(t, errorResp.Message, "Error message is required")
				assert.NotEmpty(t, errorResp.Timestamp, "Timestamp is required")

				// Property: Error code must be one of the defined constants
				validCodes := []string{
					ErrorCodeValidation,
					ErrorCodeMCPTool,
					ErrorCodeInternal,
					ErrorCodeTimeout,
					ErrorCodeRateLimit,
					ErrorCodeInvalidRequest,
				}
				assert.Contains(t, validCodes, errorResp.Code, "Error code must be one of the defined constants")

				// Property: Timestamp must be valid RFC3339 format
				_, err = time.Parse(time.RFC3339, errorResp.Timestamp)
				assert.NoError(t, err, "Timestamp must be in RFC3339 format")

				// Property: Request ID (if present) must follow format
				if errorResp.RequestID != "" {
					assert.True(t, strings.HasPrefix(errorResp.RequestID, "REQ_"), "Request ID must have REQ_ prefix")
				}

				// Property: Message should be descriptive (not empty or generic)
				assert.True(t, len(errorResp.Message) > 5, "Error message should be descriptive")
				assert.NotEqual(t, "error", strings.ToLower(errorResp.Message), "Error message should not be generic")

				t.Logf("✅ Property verified for %s/%s: code=%s, message=%s", endpoint, invalidReq.name, errorResp.Code, errorResp.Message)
			})
		}
	}

	t.Logf("🎯 Property-based test completed: %d test cases verified error response structure consistency", testCount)
}

// TestValidationErrorDetails verifies that validation errors include helpful details
func TestValidationErrorDetails(t *testing.T) {
	cfg := &config.Config{}
	logger := logrus.New()
	logger.SetLevel(logrus.WarnLevel)
	gctClient := &gctclient.Client{}
	server := NewMCPServer(cfg, logger, gctClient)

	testCases := []struct {
		name           string
		requestBody    map[string]interface{}
		expectedDetail string
	}{
		{
			name: "Missing symbol should mention symbol requirement",
			requestBody: map[string]interface{}{
				"strategy":   "rsi_macd",
				"start_date": "2024-11-01",
				"end_date":   "2024-12-13",
			},
			expectedDetail: "symbol is required",
		},
		{
			name: "Invalid exchange should list valid exchanges",
			requestBody: map[string]interface{}{
				"symbol":     "DOGE-USDT",
				"exchange":   "invalid_exchange",
				"strategy":   "rsi_macd",
				"start_date": "2024-11-01",
				"end_date":   "2024-12-13",
			},
			expectedDetail: "supported: kucoin, binance, bybit",
		},
		{
			name: "Invalid strategy should list valid strategies",
			requestBody: map[string]interface{}{
				"symbol":     "DOGE-USDT",
				"strategy":   "invalid_strategy",
				"start_date": "2024-11-01",
				"end_date":   "2024-12-13",
			},
			expectedDetail: "supported: rsi_macd, bollinger_bands, moving_average, momentum",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			requestBody, err := json.Marshal(tc.requestBody)
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPost, "/api/v1/backtesting/run-backtest", bytes.NewReader(requestBody))
			req.Header.Set("Content-Type", "application/json")

			w := httptest.NewRecorder()
			server.handleRunBacktest(w, req)

			var errorResp BacktestError
			err = json.Unmarshal(w.Body.Bytes(), &errorResp)
			require.NoError(t, err)

			assert.Equal(t, ErrorCodeValidation, errorResp.Code)
			assert.Contains(t, errorResp.Details, tc.expectedDetail, "Error details should contain helpful information")

			t.Logf("✅ Validation error details verified: %s", errorResp.Details)
		})
	}
}
