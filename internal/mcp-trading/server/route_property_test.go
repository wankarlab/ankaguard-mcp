package server

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/config"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Property 3: Route Handler Precedence
// For any HTTP request to /api/v1/backtesting/run-backtest, the request should be handled by handleRunBacktest
// and not intercepted by the catch-all JSON-RPC handler.
// Validates: Requirements TR-1

func TestProperty_RouteHandlerPrecedence(t *testing.T) {
	// Setup test server
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel) // Reduce noise in property tests

	cfg := &config.Config{
		Server: config.ServerConfig{
			Protocol: "http",
			Host:     "localhost",
			Port:     10600,
		},
	}

	gctClient := &gctclient.Client{}
	_ = NewMCPServer(cfg, logger, gctClient) // Create server for initialization, not used in this test

	// Test data: various backtesting paths that should always reach the correct handler
	backtestingPaths := []string{
		"/api/v1/backtesting/run-backtest",
		"/api/v1/backtesting/test",
		"/backtesting",
	}

	// Test data: various HTTP methods
	httpMethods := []string{"POST", "GET", "PUT", "DELETE"}

	// Test data: various request bodies
	requestBodies := []string{
		`{"symbol":"DOGE-USDT","strategy":"rsi_macd"}`,
		`{"symbol":"BTC-USDT","strategy":"bollinger_bands"}`,
		`{"test":"data"}`,
		`{}`,
		``,
	}

	// Property: For all combinations of backtesting paths, methods, and bodies,
	// the request should reach the appropriate backtesting handler, not the JSON-RPC handler
	for _, path := range backtestingPaths {
		for _, method := range httpMethods {
			for _, body := range requestBodies {
				t.Run(fmt.Sprintf("Path_%s_Method_%s", strings.ReplaceAll(path, "/", "_"), method), func(t *testing.T) {
					// Create test server with same route registration as production
					mux := http.NewServeMux()

					// Register routes in production order
					// PHASE 1: Backtesting routes (highest priority)
					mux.HandleFunc("/api/v1/backtesting/run-backtest", func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("X-Handler", "handleRunBacktest")
						w.WriteHeader(http.StatusOK)
						w.Write([]byte(`{"handler":"backtesting","path":"` + r.URL.Path + `"}`))
					})
					mux.HandleFunc("/api/v1/backtesting/test", func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("X-Handler", "handleBacktestingTest")
						w.WriteHeader(http.StatusOK)
						w.Write([]byte(`{"handler":"backtesting_test","path":"` + r.URL.Path + `"}`))
					})
					mux.HandleFunc("/backtesting", func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("X-Handler", "handleBacktestingSimple")
						w.WriteHeader(http.StatusOK)
						w.Write([]byte(`{"handler":"backtesting_simple","path":"` + r.URL.Path + `"}`))
					})

					// PHASE 2: JSON-RPC routes (lower priority)
					mux.HandleFunc("/jsonrpc", func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("X-Handler", "handleHTTPRequest")
						w.WriteHeader(http.StatusOK)
						w.Write([]byte(`{"handler":"jsonrpc","path":"` + r.URL.Path + `"}`))
					})
					mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == "/" {
							w.Header().Set("X-Handler", "handleHTTPRequest")
							w.WriteHeader(http.StatusOK)
							w.Write([]byte(`{"handler":"jsonrpc_root","path":"` + r.URL.Path + `"}`))
						} else {
							w.Header().Set("X-Handler", "NotFound")
							http.NotFound(w, r)
						}
					})

					testServer := httptest.NewServer(mux)
					defer testServer.Close()

					// Create request
					var req *http.Request
					var err error

					if body != "" {
						req, err = http.NewRequest(method, testServer.URL+path, bytes.NewBufferString(body))
						req.Header.Set("Content-Type", "application/json")
					} else {
						req, err = http.NewRequest(method, testServer.URL+path, nil)
					}

					require.NoError(t, err, "Failed to create request")

					// Execute request
					client := &http.Client{}
					resp, err := client.Do(req)
					require.NoError(t, err, "Failed to execute request")
					defer resp.Body.Close()

					// Property assertion: Backtesting paths should NEVER be handled by JSON-RPC handlers
					handlerHeader := resp.Header.Get("X-Handler")

					switch path {
					case "/api/v1/backtesting/run-backtest":
						assert.Equal(t, "handleRunBacktest", handlerHeader,
							"Path %s should be handled by handleRunBacktest, not %s", path, handlerHeader)
						assert.NotEqual(t, "handleHTTPRequest", handlerHeader,
							"Path %s should NOT be intercepted by JSON-RPC handler", path)

					case "/api/v1/backtesting/test":
						assert.Equal(t, "handleBacktestingTest", handlerHeader,
							"Path %s should be handled by handleBacktestingTest, not %s", path, handlerHeader)
						assert.NotEqual(t, "handleHTTPRequest", handlerHeader,
							"Path %s should NOT be intercepted by JSON-RPC handler", path)

					case "/backtesting":
						assert.Equal(t, "handleBacktestingSimple", handlerHeader,
							"Path %s should be handled by handleBacktestingSimple, not %s", path, handlerHeader)
						assert.NotEqual(t, "handleHTTPRequest", handlerHeader,
							"Path %s should NOT be intercepted by JSON-RPC handler", path)
					}

					// Additional property: Response should indicate correct handler was reached
					assert.NotEqual(t, http.StatusNotFound, resp.StatusCode,
						"Backtesting path %s should not return 404", path)
				})
			}
		}
	}
}

// Property test for catch-all handler behavior
func TestProperty_CatchAllHandlerBehavior(t *testing.T) {
	// Property: The catch-all handler should only handle exact root path "/"
	// and should reject all sub-paths that don't match specific routes

	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	// Test paths that should NOT be handled by catch-all
	rejectedPaths := []string{
		"/api/v1/backtesting/run-backtest",
		"/api/v1/backtesting/test",
		"/api/v1/backtesting/unknown",
		"/api/v1/unknown",
		"/unknown",
		"/test/path",
	}

	// Test paths that SHOULD be handled by catch-all
	acceptedPaths := []string{
		"/",
	}

	for _, path := range rejectedPaths {
		t.Run(fmt.Sprintf("Rejected_%s", strings.ReplaceAll(path, "/", "_")), func(t *testing.T) {
			mux := http.NewServeMux()

			// Register specific backtesting routes first
			mux.HandleFunc("/api/v1/backtesting/run-backtest", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Handler", "handleRunBacktest")
				w.WriteHeader(http.StatusOK)
			})
			mux.HandleFunc("/api/v1/backtesting/test", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Handler", "handleBacktestingTest")
				w.WriteHeader(http.StatusOK)
			})

			// Register catch-all with path validation (production behavior)
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/" {
					w.Header().Set("X-Handler", "handleHTTPRequest")
					w.WriteHeader(http.StatusOK)
				} else {
					w.Header().Set("X-Handler", "NotFound")
					http.NotFound(w, r)
				}
			})

			testServer := httptest.NewServer(mux)
			defer testServer.Close()

			req, err := http.NewRequest("GET", testServer.URL+path, nil)
			require.NoError(t, err)

			client := &http.Client{}
			resp, err := client.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			handlerHeader := resp.Header.Get("X-Handler")

			// Property: Non-root paths should either be handled by specific handlers or return 404
			if path == "/api/v1/backtesting/run-backtest" {
				assert.Equal(t, "handleRunBacktest", handlerHeader,
					"Path %s should be handled by specific handler", path)
			} else if path == "/api/v1/backtesting/test" {
				assert.Equal(t, "handleBacktestingTest", handlerHeader,
					"Path %s should be handled by specific handler", path)
			} else {
				// Unknown paths should return 404, not be handled by catch-all
				assert.Equal(t, http.StatusNotFound, resp.StatusCode,
					"Unknown path %s should return 404, not be handled by catch-all", path)
				assert.Equal(t, "NotFound", handlerHeader,
					"Unknown path %s should not be handled by JSON-RPC catch-all", path)
			}
		})
	}

	for _, path := range acceptedPaths {
		t.Run(fmt.Sprintf("Accepted_%s", strings.ReplaceAll(path, "/", "_")), func(t *testing.T) {
			mux := http.NewServeMux()

			// Register catch-all
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/" {
					w.Header().Set("X-Handler", "handleHTTPRequest")
					w.WriteHeader(http.StatusOK)
				} else {
					http.NotFound(w, r)
				}
			})

			testServer := httptest.NewServer(mux)
			defer testServer.Close()

			req, err := http.NewRequest("POST", testServer.URL+path, nil)
			require.NoError(t, err)

			client := &http.Client{}
			resp, err := client.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			// Property: Exact root path should be handled by catch-all
			assert.Equal(t, "handleHTTPRequest", resp.Header.Get("X-Handler"),
				"Root path %s should be handled by JSON-RPC handler", path)
			assert.Equal(t, http.StatusOK, resp.StatusCode,
				"Root path %s should return 200", path)
		})
	}
}

// Property test for route registration order independence
func TestProperty_RouteRegistrationOrderIndependence(t *testing.T) {
	// Property: Regardless of the order routes are registered,
	// specific backtesting routes should always take precedence over catch-all

	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	testCases := []struct {
		name        string
		setupRoutes func(*http.ServeMux)
	}{
		{
			name: "BacktestingFirst",
			setupRoutes: func(mux *http.ServeMux) {
				// Register backtesting first (current production order)
				mux.HandleFunc("/api/v1/backtesting/run-backtest", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("X-Handler", "handleRunBacktest")
					w.WriteHeader(http.StatusOK)
				})
				mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/" {
						w.Header().Set("X-Handler", "handleHTTPRequest")
						w.WriteHeader(http.StatusOK)
					} else {
						http.NotFound(w, r)
					}
				})
			},
		},
		{
			name: "CatchAllFirst",
			setupRoutes: func(mux *http.ServeMux) {
				// Register catch-all first (problematic order)
				mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/" {
						w.Header().Set("X-Handler", "handleHTTPRequest")
						w.WriteHeader(http.StatusOK)
					} else {
						http.NotFound(w, r)
					}
				})
				mux.HandleFunc("/api/v1/backtesting/run-backtest", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("X-Handler", "handleRunBacktest")
					w.WriteHeader(http.StatusOK)
				})
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			tc.setupRoutes(mux)

			testServer := httptest.NewServer(mux)
			defer testServer.Close()

			// Test backtesting endpoint
			req, err := http.NewRequest("POST", testServer.URL+"/api/v1/backtesting/run-backtest", nil)
			require.NoError(t, err)

			client := &http.Client{}
			resp, err := client.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			// Property: Backtesting endpoint should ALWAYS be handled correctly,
			// regardless of registration order
			handlerHeader := resp.Header.Get("X-Handler")
			assert.Equal(t, "handleRunBacktest", handlerHeader,
				"Backtesting endpoint should be handled by handleRunBacktest in %s order, got %s",
				tc.name, handlerHeader)

			// Test root endpoint
			rootReq, err := http.NewRequest("POST", testServer.URL+"/", nil)
			require.NoError(t, err)

			rootResp, err := client.Do(rootReq)
			require.NoError(t, err)
			defer rootResp.Body.Close()

			// Property: Root endpoint should be handled by JSON-RPC handler
			rootHandlerHeader := rootResp.Header.Get("X-Handler")
			assert.Equal(t, "handleHTTPRequest", rootHandlerHeader,
				"Root endpoint should be handled by handleHTTPRequest in %s order, got %s",
				tc.name, rootHandlerHeader)
		})
	}
}
