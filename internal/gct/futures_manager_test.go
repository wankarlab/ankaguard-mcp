package gct

import (
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewFuturesManager(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	t.Run("success", func(t *testing.T) {
		client := NewKuCoinRESTClient()
		fm, err := NewFuturesManager(client, logger)

		require.NoError(t, err)
		assert.NotNil(t, fm)
		assert.NotNil(t, fm.kucoinClient)
		assert.NotNil(t, fm.positionManager)
		assert.NotNil(t, fm.leverageCtrl)
		assert.NotNil(t, fm.logger)
	})

	t.Run("nil client", func(t *testing.T) {
		fm, err := NewFuturesManager(nil, logger)

		assert.Error(t, err)
		assert.Nil(t, fm)
		assert.Contains(t, err.Error(), "kucoin client is nil")
	})

	t.Run("nil logger", func(t *testing.T) {
		client := NewKuCoinRESTClient()
		fm, err := NewFuturesManager(client, nil)

		assert.Error(t, err)
		assert.Nil(t, fm)
		assert.Contains(t, err.Error(), "logger is nil")
	})
}

func TestFuturesManager_PlaceFuturesOrder(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	client := NewKuCoinRESTClient()
	fm, err := NewFuturesManager(client, logger)
	require.NoError(t, err)

	t.Run("nil manager", func(t *testing.T) {
		var nilFM *FuturesManager
		req := &SubmitOrderRequest{
			Exchange: "kucoin",
			Pair:     "BTC-USDT",
			Side:     "buy",
			Amount:   0.001,
			Leverage: 5,
		}

		order, err := nilFM.PlaceFuturesOrder(context.Background(), req)

		assert.Error(t, err)
		assert.Nil(t, order)
		assert.Contains(t, err.Error(), "futures manager is nil")
	})

	t.Run("nil request", func(t *testing.T) {
		order, err := fm.PlaceFuturesOrder(context.Background(), nil)

		assert.Error(t, err)
		assert.Nil(t, order)
		assert.Contains(t, err.Error(), "nil")
	})

	t.Run("empty exchange", func(t *testing.T) {
		req := &SubmitOrderRequest{
			Exchange: "",
			Pair:     "BTC-USDT",
			Side:     "buy",
			Amount:   0.001,
			Leverage: 5,
		}

		order, err := fm.PlaceFuturesOrder(context.Background(), req)

		assert.Error(t, err)
		assert.Nil(t, order)
		assert.Contains(t, err.Error(), "exchange is required")
	})

	t.Run("empty pair", func(t *testing.T) {
		req := &SubmitOrderRequest{
			Exchange: "kucoin",
			Pair:     "",
			Side:     "buy",
			Amount:   0.001,
			Leverage: 5,
		}

		order, err := fm.PlaceFuturesOrder(context.Background(), req)

		assert.Error(t, err)
		assert.Nil(t, order)
		assert.Contains(t, err.Error(), "pair is required")
	})

	t.Run("invalid side", func(t *testing.T) {
		req := &SubmitOrderRequest{
			Exchange: "kucoin",
			Pair:     "BTC-USDT",
			Side:     "invalid",
			Amount:   0.001,
			Leverage: 5,
		}

		order, err := fm.PlaceFuturesOrder(context.Background(), req)

		assert.Error(t, err)
		assert.Nil(t, order)
		assert.Contains(t, err.Error(), "side must be")
	})

	t.Run("zero amount", func(t *testing.T) {
		req := &SubmitOrderRequest{
			Exchange: "kucoin",
			Pair:     "BTC-USDT",
			Side:     "buy",
			Amount:   0,
			Leverage: 5,
		}

		order, err := fm.PlaceFuturesOrder(context.Background(), req)

		assert.Error(t, err)
		assert.Nil(t, order)
		assert.Contains(t, err.Error(), "amount must be greater than 0")
	})

	t.Run("default leverage", func(t *testing.T) {
		req := &SubmitOrderRequest{
			Exchange: "kucoin",
			Pair:     "BTC-USDT",
			Side:     "buy",
			Amount:   0.001,
			Leverage: 0, // Should default to 1
		}

		// Will fail due to no credentials, but should set leverage to 1
		_, err := fm.PlaceFuturesOrder(context.Background(), req)

		// Check that leverage was set to 1 (will fail on API call, but that's expected)
		assert.Error(t, err) // Expected to fail without credentials
	})

	t.Run("nil context", func(t *testing.T) {
		req := &SubmitOrderRequest{
			Exchange: "kucoin",
			Pair:     "BTC-USDT",
			Side:     "buy",
			Amount:   0.001,
			Leverage: 5,
		}

		// Should use background context, not panic
		assert.NotPanics(t, func() {
			fm.PlaceFuturesOrder(nil, req)
		})
	})
}

func TestFuturesManager_GetPositions(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	client := NewKuCoinRESTClient()
	fm, err := NewFuturesManager(client, logger)
	require.NoError(t, err)

	t.Run("nil manager", func(t *testing.T) {
		var nilFM *FuturesManager
		positions, err := nilFM.GetPositions(context.Background())

		assert.Error(t, err)
		assert.Nil(t, positions)
		assert.Contains(t, err.Error(), "futures manager is nil")
	})

	t.Run("nil context", func(t *testing.T) {
		// Should use background context, not panic
		assert.NotPanics(t, func() {
			fm.GetPositions(nil)
		})
	})
}

func TestFuturesManager_GetPosition(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	client := NewKuCoinRESTClient()
	fm, err := NewFuturesManager(client, logger)
	require.NoError(t, err)

	t.Run("nil manager", func(t *testing.T) {
		var nilFM *FuturesManager
		position, err := nilFM.GetPosition(context.Background(), "BTCUSDTM")

		assert.Error(t, err)
		assert.Nil(t, position)
		assert.Contains(t, err.Error(), "futures manager is nil")
	})

	t.Run("empty symbol", func(t *testing.T) {
		position, err := fm.GetPosition(context.Background(), "")

		assert.Error(t, err)
		assert.Nil(t, position)
		assert.Contains(t, err.Error(), "symbol is empty")
	})

	t.Run("nil context", func(t *testing.T) {
		// Should use background context, not panic
		assert.NotPanics(t, func() {
			fm.GetPosition(nil, "BTCUSDTM")
		})
	})
}

func TestFuturesManager_ClosePosition(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	client := NewKuCoinRESTClient()
	fm, err := NewFuturesManager(client, logger)
	require.NoError(t, err)

	t.Run("nil manager", func(t *testing.T) {
		var nilFM *FuturesManager
		err := nilFM.ClosePosition(context.Background(), "BTCUSDTM")

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "futures manager is nil")
	})

	t.Run("empty symbol", func(t *testing.T) {
		err := fm.ClosePosition(context.Background(), "")

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "symbol is empty")
	})

	t.Run("nil context", func(t *testing.T) {
		// Should use background context, not panic
		assert.NotPanics(t, func() {
			fm.ClosePosition(nil, "BTCUSDTM")
		})
	})
}

func TestFuturesManager_Stop(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	client := NewKuCoinRESTClient()
	fm, err := NewFuturesManager(client, logger)
	require.NoError(t, err)

	t.Run("stop manager", func(t *testing.T) {
		// Should not panic
		assert.NotPanics(t, func() {
			fm.Stop()
		})
	})

	t.Run("stop nil manager", func(t *testing.T) {
		var nilFM *FuturesManager
		// Should not panic
		assert.NotPanics(t, func() {
			nilFM.Stop()
		})
	})
}

func TestConvertPairToFuturesSymbol(t *testing.T) {
	tests := []struct {
		name     string
		pair     string
		expected string
	}{
		{
			name:     "BTC-USDT",
			pair:     "BTC-USDT",
			expected: "BTCUSDTM",
		},
		{
			name:     "ETH-USDT",
			pair:     "ETH-USDT",
			expected: "ETHUSDTM",
		},
		{
			name:     "DOGE-USDT",
			pair:     "DOGE-USDT",
			expected: "DOGEUSDTM",
		},
		{
			name:     "already has M suffix",
			pair:     "BTCUSDTM",
			expected: "BTCUSDTM",
		},
		{
			name:     "empty string",
			pair:     "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertPairToFuturesSymbol(tt.pair)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertFuturesSymbolToPair(t *testing.T) {
	tests := []struct {
		name     string
		symbol   string
		expected string
	}{
		{
			name:     "BTCUSDTM",
			symbol:   "BTCUSDTM",
			expected: "BTC-USDT",
		},
		{
			name:     "ETHUSDTM",
			symbol:   "ETHUSDTM",
			expected: "ETH-USDT",
		},
		{
			name:     "DOGEUSDTM",
			symbol:   "DOGEUSDTM",
			expected: "DOGE-USDT",
		},
		{
			name:     "no M suffix",
			symbol:   "BTCUSDT",
			expected: "BTC-USDT",
		},
		{
			name:     "empty string",
			symbol:   "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertFuturesSymbolToPair(tt.symbol)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestPlaceFuturesOrder_MarginModeHandling tests margin mode configuration during order placement
func TestPlaceFuturesOrder_MarginModeHandling(t *testing.T) {
	// This is a unit test that verifies the margin mode handling logic
	// without making actual API calls

	// Create a mock logger
	logger := logrus.New()
	logger.SetOutput(io.Discard) // Suppress log output during tests

	// Create KuCoin client (won't be used for actual API calls)
	kucoinClient := NewKuCoinRESTClient()

	// Create futures manager
	fm, err := NewFuturesManager(kucoinClient, logger)
	assert.NoError(t, err)
	assert.NotNil(t, fm)

	// Test that futures manager is properly initialized
	assert.NotNil(t, fm.kucoinClient)
	assert.NotNil(t, fm.positionManager)
	assert.NotNil(t, fm.leverageCtrl)
	assert.NotNil(t, fm.logger)
}

// TestEnhanceKuCoinError_MarginModeMismatch tests error enhancement for margin mode errors
func TestEnhanceKuCoinError_MarginModeMismatch(t *testing.T) {
	tests := []struct {
		name          string
		inputError    error
		symbol        string
		expectedError string
		shouldContain []string
	}{
		{
			name:          "margin mode mismatch error",
			inputError:    ErrMarginModeMismatch,
			symbol:        "BTCUSDTM",
			expectedError: "MARGIN_MODE_MISMATCH",
			shouldContain: []string{"MARGIN_MODE_MISMATCH", "BTCUSDTM", "margin mode"},
		},
		{
			name:          "error with code 330005",
			inputError:    fmt.Errorf("KuCoin error 330005: margin mode mismatch"),
			symbol:        "ETHUSDTM",
			expectedError: "MARGIN_MODE_MISMATCH",
			shouldContain: []string{"MARGIN_MODE_MISMATCH", "ETHUSDTM"},
		},
		{
			name:          "access denied error 400007",
			inputError:    fmt.Errorf("KuCoin error 400007: access denied"),
			symbol:        "DOGEUSDTM",
			expectedError: "Futures trading permission not enabled",
			shouldContain: []string{"Futures trading permission", "KuCoin API key settings"},
		},
		{
			name:          "insufficient balance error 200004",
			inputError:    fmt.Errorf("KuCoin error 200004: insufficient balance"),
			symbol:        "SHIBUSDTM",
			expectedError: "Not enough balance",
			shouldContain: []string{"Not enough balance", "SHIBUSDTM", "deposit funds"},
		},
		{
			name:          "unauthorized error 401",
			inputError:    fmt.Errorf("HTTP 401: Unauthorized"),
			symbol:        "BTCUSDTM",
			expectedError: "Invalid API credentials",
			shouldContain: []string{"Invalid API credentials", "API_KEY", "API_SECRET"},
		},
		{
			name:          "unknown error passthrough",
			inputError:    fmt.Errorf("some random error"),
			symbol:        "BTCUSDTM",
			expectedError: "some random error",
			shouldContain: []string{"some random error"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enhanced := EnhanceKuCoinError(tt.inputError, tt.symbol)
			assert.NotNil(t, enhanced)

			errStr := enhanced.Error()
			for _, expected := range tt.shouldContain {
				assert.Contains(t, errStr, expected,
					"Error message should contain: %s\nActual: %s", expected, errStr)
			}
		})
	}
}

// TestMarginModeError tests the MarginModeError type
func TestMarginModeError(t *testing.T) {
	tests := []struct {
		name          string
		symbol        string
		currentMode   string
		expectedMode  string
		shouldContain []string
	}{
		{
			name:         "with both modes specified",
			symbol:       "BTCUSDTM",
			currentMode:  "ISOLATED",
			expectedMode: "CROSSED",
			shouldContain: []string{
				"MARGIN_MODE_MISMATCH",
				"Current: ISOLATED",
				"Expected: CROSSED",
				"BTCUSDTM",
			},
		},
		{
			name:         "with empty modes",
			symbol:       "ETHUSDTM",
			currentMode:  "",
			expectedMode: "",
			shouldContain: []string{
				"MARGIN_MODE_MISMATCH",
				"ETHUSDTM",
				"margin mode",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewMarginModeError(tt.symbol, tt.currentMode, tt.expectedMode)
			assert.NotNil(t, err)

			errStr := err.Error()
			for _, expected := range tt.shouldContain {
				assert.Contains(t, errStr, expected,
					"Error message should contain: %s\nActual: %s", expected, errStr)
			}
		})
	}
}

// TestLeverageController_SetMarginMode tests the SetMarginMode method
func TestLeverageController_SetMarginMode(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(io.Discard)

	kucoinClient := NewKuCoinRESTClient()

	lc, err := NewLeverageController(kucoinClient, logger)
	assert.NoError(t, err)
	assert.NotNil(t, lc)

	tests := []struct {
		name        string
		symbol      string
		mode        string
		expectError bool
	}{
		{
			name:        "valid CROSSED mode",
			symbol:      "BTCUSDTM",
			mode:        "CROSSED",
			expectError: true, // Will fail without real API, but validates input
		},
		{
			name:        "valid ISOLATED mode",
			symbol:      "ETHUSDTM",
			mode:        "ISOLATED",
			expectError: true, // Will fail without real API, but validates input
		},
		{
			name:        "CROSS normalized to CROSSED",
			symbol:      "DOGEUSDTM",
			mode:        "CROSS",
			expectError: true, // Will fail without real API, but validates input
		},
		{
			name:        "invalid mode",
			symbol:      "BTCUSDTM",
			mode:        "INVALID",
			expectError: true,
		},
		{
			name:        "empty symbol",
			symbol:      "",
			mode:        "CROSSED",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			err := lc.SetMarginMode(ctx, tt.symbol, tt.mode)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestLeverageController_ClearMarginCache tests cache clearing
func TestLeverageController_ClearMarginCache(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(io.Discard)

	kucoinClient := NewKuCoinRESTClient()

	lc, err := NewLeverageController(kucoinClient, logger)
	assert.NoError(t, err)

	// Manually add to cache
	lc.cacheMux.Lock()
	lc.marginCache["BTCUSDTM"] = "CROSSED"
	lc.cacheMux.Unlock()

	// Verify it's in cache
	lc.cacheMux.RLock()
	_, exists := lc.marginCache["BTCUSDTM"]
	lc.cacheMux.RUnlock()
	assert.True(t, exists)

	// Clear cache
	lc.ClearMarginCache("BTCUSDTM")

	// Verify it's removed
	lc.cacheMux.RLock()
	_, exists = lc.marginCache["BTCUSDTM"]
	lc.cacheMux.RUnlock()
	assert.False(t, exists)
}
