package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// closeNotifyRecorder wraps httptest.ResponseRecorder to implement http.CloseNotifier.
// net/http/httputil.ReverseProxy attempts a type assertion to CloseNotifier; the
// standard ResponseRecorder does not satisfy it, causing a panic in tests.
// This wrapper returns a permanently-open channel (client never disconnects).
type closeNotifyRecorder struct {
	*httptest.ResponseRecorder
	closeNotify chan bool
}

func newCloseNotifyRecorder() *closeNotifyRecorder {
	return &closeNotifyRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		closeNotify:      make(chan bool, 1),
	}
}

func (r *closeNotifyRecorder) CloseNotify() <-chan bool { return r.closeNotify }

// TestGatewayRoutingPreservation verifies that the gateway preserves backtesting request paths
// and routes them correctly to the MCP server
func TestGatewayRoutingPreservation(t *testing.T) {
	// Create a mock MCP server to capture requests
	mockMCPServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo back the request details for verification
		response := map[string]interface{}{
			"received_path":   r.URL.Path,
			"received_method": r.Method,
			"headers": map[string]string{
				"X-Internal-Gateway": r.Header.Get("X-Internal-Gateway"),
				"X-Gateway-Token":    r.Header.Get("X-Gateway-Token"),
				"Content-Type":       r.Header.Get("Content-Type"),
			},
		}

		// If it's a backtesting request, return mock data
		if strings.Contains(r.URL.Path, "backtesting") {
			response["performance"] = map[string]interface{}{
				"roi":          15.5,
				"total_trades": 45,
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer mockMCPServer.Close()

	// Create gateway with mock MCP server
	logger := logrus.New()
	logger.SetLevel(logrus.WarnLevel) // Reduce noise in tests

	config := &GatewayConfig{
		Port:                 0, // Use random port for testing
		MCPServerURL:         mockMCPServer.URL,
		ScalpingAPIURL:       "http://localhost:8081", // Not used in this test
		EnableCORS:           true,
		InternalGatewayToken: "internal-gateway-token",
		ReadTimeout:          5 * time.Second,
		WriteTimeout:         5 * time.Second,
	}

	gateway, err := NewGateway(config, logger)
	require.NoError(t, err)

	// Test cases for different backtesting routes
	testCases := []struct {
		name         string
		requestPath  string
		expectedPath string
		method       string
		body         map[string]interface{}
	}{
		{
			name:         "API v1 backtesting run-backtest",
			requestPath:  "/api/v1/backtesting/run-backtest",
			expectedPath: "/api/v1/backtesting/run-backtest",
			method:       "POST",
			body: map[string]interface{}{
				"symbol":     "DOGE-USDT",
				"strategy":   "rsi_macd",
				"start_date": "2024-11-01",
				"end_date":   "2024-12-13",
			},
		},
		{
			name:         "Simple backtesting endpoint",
			requestPath:  "/backtesting",
			expectedPath: "/backtesting",
			method:       "POST",
			body: map[string]interface{}{
				"symbol":     "BTC-USDT",
				"strategy":   "bollinger_bands",
				"start_date": "2024-10-01",
				"end_date":   "2024-11-01",
			},
		},
		{
			name:         "API v1 backtesting test endpoint",
			requestPath:  "/api/v1/backtesting/test",
			expectedPath: "/api/v1/backtesting/test",
			method:       "GET",
			body:         nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Prepare request body
			var requestBody []byte
			if tc.body != nil {
				var err error
				requestBody, err = json.Marshal(tc.body)
				require.NoError(t, err)
			}

			// Create HTTP request
			req := httptest.NewRequest(tc.method, tc.requestPath, bytes.NewReader(requestBody))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("User-Agent", "test-client")

			// Create response recorder
			w := newCloseNotifyRecorder()

			// Process request through gateway
			gateway.router.ServeHTTP(w, req)

			// Verify response
			assert.Equal(t, http.StatusOK, w.Code, "Gateway should return 200 OK")

			// Parse response
			var response map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err, "Response should be valid JSON")

			// Verify path preservation
			receivedPath, ok := response["received_path"].(string)
			require.True(t, ok, "Response should contain received_path")
			assert.Equal(t, tc.expectedPath, receivedPath, "Gateway should preserve the request path")

			// Verify method preservation
			receivedMethod, ok := response["received_method"].(string)
			require.True(t, ok, "Response should contain received_method")
			assert.Equal(t, tc.method, receivedMethod, "Gateway should preserve the request method")

			// Verify gateway headers are added
			headers, ok := response["headers"].(map[string]interface{})
			require.True(t, ok, "Response should contain headers")

			gatewayHeader, ok := headers["X-Internal-Gateway"].(string)
			require.True(t, ok, "Should have X-Internal-Gateway header")
			assert.Equal(t, "true", gatewayHeader, "Gateway should add internal header")

			tokenHeader, ok := headers["X-Gateway-Token"].(string)
			require.True(t, ok, "Should have X-Gateway-Token header")
			assert.Equal(t, "internal-gateway-token", tokenHeader, "Gateway should add token header")

			// Verify Content-Type is preserved for POST requests
			if tc.method == "POST" {
				contentType, ok := headers["Content-Type"].(string)
				require.True(t, ok, "Should have Content-Type header for POST")
				assert.Equal(t, "application/json", contentType, "Content-Type should be preserved")
			}

			t.Logf("✅ Gateway routing verified for %s: %s -> %s", tc.name, tc.requestPath, receivedPath)
		})
	}
}

// TestProperty_GatewayRoutingPreservation is a property-based test that verifies
// the gateway preserves request paths and adds required headers for all backtesting routes
func TestProperty_GatewayRoutingPreservation(t *testing.T) {
	// This test verifies the property: "Gateway should preserve request paths and add
	// internal headers for all backtesting requests regardless of specific route"

	// Create mock MCP server
	mockMCPServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"received_path":   r.URL.Path,
			"received_method": r.Method,
			"headers": map[string]string{
				"X-Internal-Gateway": r.Header.Get("X-Internal-Gateway"),
				"X-Gateway-Token":    r.Header.Get("X-Gateway-Token"),
				"Content-Type":       r.Header.Get("Content-Type"),
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer mockMCPServer.Close()

	// Create gateway
	logger := logrus.New()
	logger.SetLevel(logrus.WarnLevel)

	config := &GatewayConfig{
		Port:                 0,
		MCPServerURL:         mockMCPServer.URL,
		ScalpingAPIURL:       "http://localhost:8081",
		EnableCORS:           true,
		InternalGatewayToken: "internal-gateway-token",
	}

	gateway, err := NewGateway(config, logger)
	require.NoError(t, err)

	// Generate test cases for various backtesting route patterns
	routePatterns := []struct {
		pattern string
		paths   []string
	}{
		{
			pattern: "API v1 backtesting routes",
			paths: []string{
				"/api/v1/backtesting/run-backtest",
				"/api/v1/backtesting/test",
				"/api/v1/backtesting/status",
				"/api/v1/backtesting/health",
			},
		},
		{
			pattern: "Simple backtesting routes",
			paths: []string{
				"/backtesting",
			},
		},
	}

	methods := []string{"GET", "POST", "PUT", "DELETE"}

	testCount := 0
	for _, routePattern := range routePatterns {
		for _, path := range routePattern.paths {
			for _, method := range methods {
				testCount++
				t.Run(fmt.Sprintf("Property_Test_%d_%s_%s", testCount, method, strings.ReplaceAll(path, "/", "_")), func(t *testing.T) {
					// Create request
					var requestBody []byte
					if method == "POST" || method == "PUT" {
						body := map[string]interface{}{
							"symbol":     "TEST-USDT",
							"strategy":   "test_strategy",
							"start_date": "2024-01-01",
							"end_date":   "2024-01-31",
						}
						requestBody, _ = json.Marshal(body)
					}

					req := httptest.NewRequest(method, path, bytes.NewReader(requestBody))
					req.Header.Set("Content-Type", "application/json")

					w := newCloseNotifyRecorder()

					// Process through gateway
					gateway.router.ServeHTTP(w, req)

					// Should return 200 (mock server always returns 200)
					assert.Equal(t, http.StatusOK, w.Code, "Gateway should route request successfully")

					// Parse response
					var response map[string]interface{}
					err := json.Unmarshal(w.Body.Bytes(), &response)
					require.NoError(t, err, "Response should be valid JSON")

					// Property: Path should be preserved exactly
					receivedPath, ok := response["received_path"].(string)
					require.True(t, ok, "Response should contain received_path")
					assert.Equal(t, path, receivedPath, "Gateway must preserve request path exactly")

					// Property: Method should be preserved exactly
					receivedMethod, ok := response["received_method"].(string)
					require.True(t, ok, "Response should contain received_method")
					assert.Equal(t, method, receivedMethod, "Gateway must preserve request method exactly")

					// Property: Internal gateway headers must be added
					headers, ok := response["headers"].(map[string]interface{})
					require.True(t, ok, "Response should contain headers")

					gatewayHeader, ok := headers["X-Internal-Gateway"].(string)
					require.True(t, ok, "Must have X-Internal-Gateway header")
					assert.Equal(t, "true", gatewayHeader, "X-Internal-Gateway must be 'true'")

					tokenHeader, ok := headers["X-Gateway-Token"].(string)
					require.True(t, ok, "Must have X-Gateway-Token header")
					assert.Equal(t, "internal-gateway-token", tokenHeader, "X-Gateway-Token must be correct")

					// Property: Content-Type should be preserved for requests with body
					if method == "POST" || method == "PUT" {
						contentType, ok := headers["Content-Type"].(string)
						require.True(t, ok, "Must have Content-Type header for requests with body")
						assert.Equal(t, "application/json", contentType, "Content-Type must be preserved")
					}

					t.Logf("✅ Property verified for %s %s", method, path)
				})
			}
		}
	}

	t.Logf("🎯 Property-based test completed: %d test cases verified gateway routing preservation", testCount)
}

// TestGatewayRoutingConsistency verifies that both backtesting endpoints
// are routed consistently through the gateway
func TestGatewayRoutingConsistency(t *testing.T) {
	// Track requests received by mock server
	var receivedRequests []map[string]interface{}

	mockMCPServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Record request details
		requestInfo := map[string]interface{}{
			"path":   r.URL.Path,
			"method": r.Method,
			"headers": map[string]string{
				"X-Internal-Gateway": r.Header.Get("X-Internal-Gateway"),
				"X-Gateway-Token":    r.Header.Get("X-Gateway-Token"),
			},
		}
		receivedRequests = append(receivedRequests, requestInfo)

		// Return mock backtesting response
		response := map[string]interface{}{
			"performance": map[string]interface{}{
				"roi":          15.5,
				"total_trades": 45,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer mockMCPServer.Close()

	// Create gateway
	logger := logrus.New()
	logger.SetLevel(logrus.WarnLevel)

	config := &GatewayConfig{
		Port:                 0,
		MCPServerURL:         mockMCPServer.URL,
		ScalpingAPIURL:       "http://localhost:8081",
		EnableCORS:           true,
		InternalGatewayToken: "internal-gateway-token",
	}

	gateway, err := NewGateway(config, logger)
	require.NoError(t, err)

	// Test both backtesting endpoints with same request
	requestBody := map[string]interface{}{
		"symbol":     "DOGE-USDT",
		"strategy":   "rsi_macd",
		"start_date": "2024-11-01",
		"end_date":   "2024-12-13",
	}

	endpoints := []struct {
		name string
		path string
	}{
		{"API v1 endpoint", "/api/v1/backtesting/run-backtest"},
		{"Simple endpoint", "/backtesting"},
	}

	// Clear received requests
	receivedRequests = []map[string]interface{}{}

	for _, endpoint := range endpoints {
		t.Run(endpoint.name, func(t *testing.T) {
			bodyBytes, err := json.Marshal(requestBody)
			require.NoError(t, err)

			req := httptest.NewRequest("POST", endpoint.path, bytes.NewReader(bodyBytes))
			req.Header.Set("Content-Type", "application/json")

			w := newCloseNotifyRecorder()
			gateway.router.ServeHTTP(w, req)

			// Verify successful routing
			assert.Equal(t, http.StatusOK, w.Code, "Request should be routed successfully")

			// Verify response contains expected data
			var response map[string]interface{}
			err = json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			performance, ok := response["performance"].(map[string]interface{})
			require.True(t, ok, "Response should contain performance data")

			roi, ok := performance["roi"].(float64)
			require.True(t, ok, "Performance should contain ROI")
			assert.Equal(t, 15.5, roi, "ROI should match expected value")
		})
	}

	// Verify both requests were received by mock server
	require.Len(t, receivedRequests, 2, "Mock server should have received 2 requests")

	// Verify consistency of gateway headers
	for i, req := range receivedRequests {
		headers, ok := req["headers"].(map[string]string)
		require.True(t, ok, "Request %d should have headers", i+1)

		assert.Equal(t, "true", headers["X-Internal-Gateway"], "Request %d should have gateway header", i+1)
		assert.Equal(t, "internal-gateway-token", headers["X-Gateway-Token"], "Request %d should have token header", i+1)
	}

	// Verify paths were preserved correctly
	expectedPaths := []string{"/api/v1/backtesting/run-backtest", "/backtesting"}
	for i, req := range receivedRequests {
		path, ok := req["path"].(string)
		require.True(t, ok, "Request %d should have path", i+1)
		assert.Equal(t, expectedPaths[i], path, "Request %d path should be preserved", i+1)
	}

	t.Log("✅ Gateway routing consistency verified for both backtesting endpoints")
}

// TestGatewayErrorHandling verifies that the gateway properly handles errors
// and preserves error responses from the MCP server
func TestGatewayErrorHandling(t *testing.T) {
	// Create mock MCP server that returns errors
	mockMCPServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return validation error for requests without symbol
		errorResponse := map[string]interface{}{
			"code":      "VALIDATION_ERROR",
			"message":   "Request validation failed",
			"details":   "symbol is required",
			"timestamp": time.Now().Format(time.RFC3339),
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(errorResponse)
	}))
	defer mockMCPServer.Close()

	// Create gateway
	logger := logrus.New()
	logger.SetLevel(logrus.WarnLevel)

	config := &GatewayConfig{
		Port:                 0,
		MCPServerURL:         mockMCPServer.URL,
		ScalpingAPIURL:       "http://localhost:8081",
		EnableCORS:           true,
		InternalGatewayToken: "internal-gateway-token",
	}

	gateway, err := NewGateway(config, logger)
	require.NoError(t, err)

	// Test error handling for both endpoints
	endpoints := []string{"/api/v1/backtesting/run-backtest", "/backtesting"}

	for _, endpoint := range endpoints {
		t.Run(fmt.Sprintf("Error_handling_%s", strings.ReplaceAll(endpoint, "/", "_")), func(t *testing.T) {
			// Send invalid request (missing symbol)
			invalidRequest := map[string]interface{}{
				"strategy":   "rsi_macd",
				"start_date": "2024-11-01",
				"end_date":   "2024-12-13",
			}

			bodyBytes, err := json.Marshal(invalidRequest)
			require.NoError(t, err)

			req := httptest.NewRequest("POST", endpoint, bytes.NewReader(bodyBytes))
			req.Header.Set("Content-Type", "application/json")

			w := newCloseNotifyRecorder()
			gateway.router.ServeHTTP(w, req)

			// Verify error status is preserved
			assert.Equal(t, http.StatusBadRequest, w.Code, "Gateway should preserve error status code")

			// Verify error response is preserved
			var errorResponse map[string]interface{}
			err = json.Unmarshal(w.Body.Bytes(), &errorResponse)
			require.NoError(t, err, "Error response should be valid JSON")

			code, ok := errorResponse["code"].(string)
			require.True(t, ok, "Error response should have code")
			assert.Equal(t, "VALIDATION_ERROR", code, "Error code should be preserved")

			message, ok := errorResponse["message"].(string)
			require.True(t, ok, "Error response should have message")
			assert.Equal(t, "Request validation failed", message, "Error message should be preserved")

			t.Logf("✅ Error handling verified for %s", endpoint)
		})
	}
}
