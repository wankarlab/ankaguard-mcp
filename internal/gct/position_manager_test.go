package gct

import (
	"context"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPositionManager(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	t.Run("success", func(t *testing.T) {
		client := NewKuCoinRESTClient()
		pm, err := NewPositionCache(client, logger)

		require.NoError(t, err)
		assert.NotNil(t, pm)
		assert.NotNil(t, pm.client)
		assert.NotNil(t, pm.logger)
		assert.NotNil(t, pm.cache)
		assert.NotNil(t, pm.stopChan)
	})

	t.Run("nil client", func(t *testing.T) {
		pm, err := NewPositionCache(nil, logger)

		assert.Error(t, err)
		assert.Nil(t, pm)
		assert.Contains(t, err.Error(), "kucoin client is nil")
	})

	t.Run("nil logger", func(t *testing.T) {
		client := NewKuCoinRESTClient()
		pm, err := NewPositionCache(client, nil)

		assert.Error(t, err)
		assert.Nil(t, pm)
		assert.Contains(t, err.Error(), "logger is nil")
	})
}

func TestPositionManager_ParsePosition(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	client := NewKuCoinRESTClient()
	pm, err := NewPositionCache(client, logger)
	require.NoError(t, err)

	t.Run("valid long position", func(t *testing.T) {
		raw := map[string]interface{}{
			"symbol":           "BTCUSDTM",
			"currentQty":       10.0,
			"avgEntryPrice":    50000.0,
			"markPrice":        51000.0,
			"unrealisedPnl":    1000.0,
			"realisedPnl":      500.0,
			"realLeverage":     5.0,
			"crossMode":        true,
			"liquidationPrice": 45000.0,
			"currentCost":      10000.0,
		}

		position, err := pm.parsePosition(raw)

		require.NoError(t, err)
		assert.NotNil(t, position)
		assert.Equal(t, "BTCUSDTM", position.Symbol)
		assert.Equal(t, "long", position.Side)
		assert.Equal(t, 10.0, position.Size)
		assert.Equal(t, 50000.0, position.EntryPrice)
		assert.Equal(t, 51000.0, position.CurrentPrice)
		assert.Equal(t, 1000.0, position.UnrealizedPnL)
		assert.Equal(t, 500.0, position.RealizedPnL)
		assert.Equal(t, 5, position.Leverage)
		assert.Equal(t, "CROSSED", position.MarginMode)
		assert.Equal(t, 45000.0, position.LiquidationPrice)
		assert.Equal(t, 10000.0, position.Margin)
		assert.True(t, position.IsValid())
	})

	t.Run("valid short position", func(t *testing.T) {
		raw := map[string]interface{}{
			"symbol":           "ETHUSDTM",
			"currentQty":       -5.0,
			"avgEntryPrice":    3000.0,
			"markPrice":        2900.0,
			"unrealisedPnl":    500.0,
			"realisedPnl":      100.0,
			"realLeverage":     10.0,
			"crossMode":        false,
			"liquidationPrice": 3500.0,
			"currentCost":      1500.0,
		}

		position, err := pm.parsePosition(raw)

		require.NoError(t, err)
		assert.NotNil(t, position)
		assert.Equal(t, "ETHUSDTM", position.Symbol)
		assert.Equal(t, "short", position.Side)
		assert.Equal(t, 5.0, position.Size)
		assert.Equal(t, "ISOLATED", position.MarginMode)
	})

	t.Run("zero quantity", func(t *testing.T) {
		raw := map[string]interface{}{
			"symbol":     "BTCUSDTM",
			"currentQty": 0.0,
			"crossMode":  true,
		}

		position, err := pm.parsePosition(raw)

		assert.NoError(t, err)
		assert.Nil(t, position)
	})

	t.Run("nil raw data", func(t *testing.T) {
		position, err := pm.parsePosition(nil)

		assert.Error(t, err)
		assert.Nil(t, position)
		assert.Contains(t, err.Error(), "raw position data is nil")
	})

	t.Run("missing symbol", func(t *testing.T) {
		raw := map[string]interface{}{
			"currentQty": 10.0,
		}

		position, err := pm.parsePosition(raw)

		assert.Error(t, err)
		assert.Nil(t, position)
		assert.Contains(t, err.Error(), "invalid or missing symbol")
	})

	t.Run("missing currentQty", func(t *testing.T) {
		raw := map[string]interface{}{
			"symbol": "BTCUSDTM",
		}

		position, err := pm.parsePosition(raw)

		assert.Error(t, err)
		assert.Nil(t, position)
		assert.Contains(t, err.Error(), "invalid or missing currentQty")
	})

	t.Run("nil position manager", func(t *testing.T) {
		var nilPM *PositionCache
		raw := map[string]interface{}{
			"symbol":     "BTCUSDTM",
			"currentQty": 10.0,
		}

		position, err := nilPM.parsePosition(raw)

		assert.Error(t, err)
		assert.Nil(t, position)
		assert.Contains(t, err.Error(), "position manager is nil")
	})
}

func TestPositionManager_CalculatePnL(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	client := NewKuCoinRESTClient()
	pm, err := NewPositionCache(client, logger)
	require.NoError(t, err)

	t.Run("long position profit", func(t *testing.T) {
		position := &Position{
			Symbol:     "BTCUSDTM",
			Side:       "long",
			Size:       10.0,
			EntryPrice: 50000.0,
			Leverage:   5,
			MarginMode: "CROSSED",
		}

		pnl := pm.CalculatePnL(position, 51000.0)

		assert.Equal(t, 10000.0, pnl) // (51000 - 50000) * 10
	})

	t.Run("long position loss", func(t *testing.T) {
		position := &Position{
			Symbol:     "BTCUSDTM",
			Side:       "long",
			Size:       10.0,
			EntryPrice: 50000.0,
			Leverage:   5,
			MarginMode: "CROSSED",
		}

		pnl := pm.CalculatePnL(position, 49000.0)

		assert.Equal(t, -10000.0, pnl) // (49000 - 50000) * 10
	})

	t.Run("short position profit", func(t *testing.T) {
		position := &Position{
			Symbol:     "ETHUSDTM",
			Side:       "short",
			Size:       5.0,
			EntryPrice: 3000.0,
			Leverage:   10,
			MarginMode: "ISOLATED",
		}

		pnl := pm.CalculatePnL(position, 2900.0)

		assert.Equal(t, 500.0, pnl) // (3000 - 2900) * 5
	})

	t.Run("short position loss", func(t *testing.T) {
		position := &Position{
			Symbol:     "ETHUSDTM",
			Side:       "short",
			Size:       5.0,
			EntryPrice: 3000.0,
			Leverage:   10,
			MarginMode: "ISOLATED",
		}

		pnl := pm.CalculatePnL(position, 3100.0)

		assert.Equal(t, -500.0, pnl) // (3000 - 3100) * 5
	})

	t.Run("nil position", func(t *testing.T) {
		pnl := pm.CalculatePnL(nil, 50000.0)

		assert.Equal(t, 0.0, pnl)
	})

	t.Run("nil position manager", func(t *testing.T) {
		var nilPM *PositionCache
		position := &Position{
			Symbol:     "BTCUSDTM",
			Side:       "long",
			Size:       10.0,
			EntryPrice: 50000.0,
		}

		pnl := nilPM.CalculatePnL(position, 51000.0)

		assert.Equal(t, 0.0, pnl)
	})

	t.Run("zero current price", func(t *testing.T) {
		position := &Position{
			Symbol:     "BTCUSDTM",
			Side:       "long",
			Size:       10.0,
			EntryPrice: 50000.0,
		}

		pnl := pm.CalculatePnL(position, 0.0)

		assert.Equal(t, 0.0, pnl)
	})
}

func TestPositionManager_CacheOperations(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	client := NewKuCoinRESTClient()
	pm, err := NewPositionCache(client, logger)
	require.NoError(t, err)

	t.Run("clear cache", func(t *testing.T) {
		// Add some data to cache
		pm.cache["BTCUSDTM"] = &Position{Symbol: "BTCUSDTM"}

		assert.Len(t, pm.cache, 1)

		// Clear cache
		pm.ClearCache()

		assert.Len(t, pm.cache, 0)
	})

	t.Run("clear cache for symbol", func(t *testing.T) {
		// Add data for multiple symbols
		pm.cache["BTCUSDTM"] = &Position{Symbol: "BTCUSDTM"}
		pm.cache["ETHUSDTM"] = &Position{Symbol: "ETHUSDTM"}

		assert.Len(t, pm.cache, 2)

		// Clear cache for one symbol
		pm.ClearCacheForSymbol("BTCUSDTM")

		assert.Len(t, pm.cache, 1)
		assert.NotContains(t, pm.cache, "BTCUSDTM")
		assert.Contains(t, pm.cache, "ETHUSDTM")
	})

	t.Run("clear cache with nil manager", func(t *testing.T) {
		var nilPM *PositionCache
		nilPM.ClearCache() // Should not panic
	})
}

func TestPositionManager_GetPositions(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	client := NewKuCoinRESTClient()
	pm, err := NewPositionCache(client, logger)
	require.NoError(t, err)

	t.Run("nil manager", func(t *testing.T) {
		var nilPM *PositionCache
		positions, err := nilPM.GetPositions(context.Background())

		assert.Error(t, err)
		assert.Nil(t, positions)
		assert.Contains(t, err.Error(), "position manager is nil")
	})

	t.Run("nil context", func(t *testing.T) {
		// Should use background context, not panic
		assert.NotPanics(t, func() {
			pm.GetPositions(nil)
		})
	})
}

func TestPositionManager_GetPosition(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	client := NewKuCoinRESTClient()
	pm, err := NewPositionCache(client, logger)
	require.NoError(t, err)

	t.Run("nil manager", func(t *testing.T) {
		var nilPM *PositionCache
		position, err := nilPM.GetPosition(context.Background(), "BTCUSDTM")

		assert.Error(t, err)
		assert.Nil(t, position)
		assert.Contains(t, err.Error(), "position manager is nil")
	})

	t.Run("empty symbol", func(t *testing.T) {
		position, err := pm.GetPosition(context.Background(), "")

		assert.Error(t, err)
		assert.Nil(t, position)
		assert.Contains(t, err.Error(), "symbol is empty")
	})

	t.Run("cache hit", func(t *testing.T) {
		// Add to cache
		cachedPosition := &Position{
			Symbol:    "BTCUSDTM",
			Side:      "long",
			Size:      10.0,
			UpdatedAt: time.Now(),
		}
		pm.cache["BTCUSDTM"] = cachedPosition

		// Get from cache
		position, err := pm.GetPosition(context.Background(), "BTCUSDTM")

		assert.NoError(t, err)
		assert.NotNil(t, position)
		assert.Equal(t, "BTCUSDTM", position.Symbol)
	})
}

func TestPositionManager_Stop(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	client := NewKuCoinRESTClient()
	pm, err := NewPositionCache(client, logger)
	require.NoError(t, err)

	t.Run("stop manager", func(t *testing.T) {
		// Should not panic
		assert.NotPanics(t, func() {
			pm.Stop()
		})
	})

	t.Run("stop nil manager", func(t *testing.T) {
		var nilPM *PositionCache
		// Should not panic
		assert.NotPanics(t, func() {
			nilPM.Stop()
		})
	})
}
