package gct

import (
	"context"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLeverageController(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	t.Run("success", func(t *testing.T) {
		client := NewKuCoinRESTClient()
		lc, err := NewLeverageController(client, logger)

		require.NoError(t, err)
		assert.NotNil(t, lc)
		assert.NotNil(t, lc.client)
		assert.NotNil(t, lc.logger)
		assert.NotNil(t, lc.cache)
		assert.NotNil(t, lc.marginCache)
	})

	t.Run("nil client", func(t *testing.T) {
		lc, err := NewLeverageController(nil, logger)

		assert.Error(t, err)
		assert.Nil(t, lc)
		assert.Contains(t, err.Error(), "kucoin client is nil")
	})

	t.Run("nil logger", func(t *testing.T) {
		client := NewKuCoinRESTClient()
		lc, err := NewLeverageController(client, nil)

		assert.Error(t, err)
		assert.Nil(t, lc)
		assert.Contains(t, err.Error(), "logger is nil")
	})
}

func TestLeverageController_ValidateLeverage(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	client := NewKuCoinRESTClient()
	lc, err := NewLeverageController(client, logger)
	require.NoError(t, err)

	tests := []struct {
		name     string
		symbol   string
		leverage int
		wantErr  bool
	}{
		{
			name:     "valid leverage 1",
			symbol:   "BTCUSDTM",
			leverage: 1,
			wantErr:  false,
		},
		{
			name:     "valid leverage 20",
			symbol:   "BTCUSDTM",
			leverage: 20,
			wantErr:  false,
		},
		{
			name:     "valid leverage 100",
			symbol:   "BTCUSDTM",
			leverage: 100,
			wantErr:  false,
		},
		{
			name:     "invalid leverage 0",
			symbol:   "BTCUSDTM",
			leverage: 0,
			wantErr:  true,
		},
		{
			name:     "invalid leverage 101",
			symbol:   "BTCUSDTM",
			leverage: 101,
			wantErr:  true,
		},
		{
			name:     "invalid leverage negative",
			symbol:   "BTCUSDTM",
			leverage: -5,
			wantErr:  true,
		},
		{
			name:     "empty symbol",
			symbol:   "",
			leverage: 10,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := lc.ValidateLeverage(tt.symbol, tt.leverage)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestLeverageController_CacheOperations(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	client := NewKuCoinRESTClient()
	lc, err := NewLeverageController(client, logger)
	require.NoError(t, err)

	t.Run("clear cache", func(t *testing.T) {
		// Add some data to cache
		lc.cache["BTCUSDTM"] = 10
		lc.marginCache["BTCUSDTM"] = "CROSSED"

		assert.Len(t, lc.cache, 1)
		assert.Len(t, lc.marginCache, 1)

		// Clear cache
		lc.ClearCache()

		assert.Len(t, lc.cache, 0)
		assert.Len(t, lc.marginCache, 0)
	})

	t.Run("clear cache for symbol", func(t *testing.T) {
		// Add data for multiple symbols
		lc.cache["BTCUSDTM"] = 10
		lc.cache["ETHUSDTM"] = 5
		lc.marginCache["BTCUSDTM"] = "CROSSED"
		lc.marginCache["ETHUSDTM"] = "ISOLATED"

		assert.Len(t, lc.cache, 2)
		assert.Len(t, lc.marginCache, 2)

		// Clear cache for one symbol
		lc.ClearCacheForSymbol("BTCUSDTM")

		assert.Len(t, lc.cache, 1)
		assert.Len(t, lc.marginCache, 1)
		assert.NotContains(t, lc.cache, "BTCUSDTM")
		assert.Contains(t, lc.cache, "ETHUSDTM")
	})

	t.Run("clear cache with nil controller", func(t *testing.T) {
		var nilLC *LeverageController
		nilLC.ClearCache() // Should not panic
	})

	t.Run("clear cache for symbol with empty symbol", func(t *testing.T) {
		lc.ClearCacheForSymbol("") // Should not panic
	})
}

func TestLeverageController_GetMarginMode(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	client := NewKuCoinRESTClient()
	lc, err := NewLeverageController(client, logger)
	require.NoError(t, err)

	t.Run("nil controller", func(t *testing.T) {
		var nilLC *LeverageController
		mode, err := nilLC.GetMarginMode(context.Background(), "BTCUSDTM")

		assert.Error(t, err)
		assert.Empty(t, mode)
		assert.Contains(t, err.Error(), "leverage controller is nil")
	})

	t.Run("empty symbol", func(t *testing.T) {
		mode, err := lc.GetMarginMode(context.Background(), "")

		assert.Error(t, err)
		assert.Empty(t, mode)
		assert.Contains(t, err.Error(), "symbol is empty")
	})

	t.Run("nil context", func(t *testing.T) {
		// Should use background context, not error
		mode, err := lc.GetMarginMode(nil, "BTCUSDTM")

		// Will fail because no credentials, but shouldn't panic
		assert.NotPanics(t, func() {
			lc.GetMarginMode(nil, "BTCUSDTM")
		})
		_ = mode
		_ = err
	})
}

func TestLeverageController_SetLeverage(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	client := NewKuCoinRESTClient()
	lc, err := NewLeverageController(client, logger)
	require.NoError(t, err)

	t.Run("nil controller", func(t *testing.T) {
		var nilLC *LeverageController
		err := nilLC.SetLeverage(context.Background(), "BTCUSDTM", 10)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "leverage controller is nil")
	})

	t.Run("empty symbol", func(t *testing.T) {
		err := lc.SetLeverage(context.Background(), "", 10)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "symbol is empty")
	})

	t.Run("invalid leverage", func(t *testing.T) {
		err := lc.SetLeverage(context.Background(), "BTCUSDTM", 0)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "leverage validation failed")
	})

	t.Run("nil context", func(t *testing.T) {
		// Should use background context, not panic
		assert.NotPanics(t, func() {
			lc.SetLeverage(nil, "BTCUSDTM", 10)
		})
	})
}

func TestLeverageController_ThreadSafety(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	client := NewKuCoinRESTClient()
	lc, err := NewLeverageController(client, logger)
	require.NoError(t, err)

	// Test concurrent cache operations
	t.Run("concurrent cache writes", func(t *testing.T) {
		done := make(chan bool)

		// Write to cache concurrently
		for i := 0; i < 10; i++ {
			go func(idx int) {
				lc.cache[string(rune('A'+idx))] = idx
				lc.marginCache[string(rune('A'+idx))] = "CROSSED"
				done <- true
			}(i)
		}

		// Wait for all goroutines
		for i := 0; i < 10; i++ {
			<-done
		}

		// Should not panic and should have data
		assert.NotEmpty(t, lc.cache)
	})

	t.Run("concurrent clear operations", func(t *testing.T) {
		// Add initial data
		lc.cache["TEST"] = 10
		lc.marginCache["TEST"] = "CROSSED"

		done := make(chan bool)

		// Clear concurrently
		for i := 0; i < 5; i++ {
			go func() {
				lc.ClearCache()
				done <- true
			}()
		}

		// Wait for all goroutines
		for i := 0; i < 5; i++ {
			<-done
		}

		// Should not panic
		assert.Len(t, lc.cache, 0)
	})
}
