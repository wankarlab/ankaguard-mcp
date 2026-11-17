package storage

import (
	"testing"
	"time"

	"github.com/rantipay/trading-mcp/internal/ai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTimeframeDataCache_SaveAndLoad(t *testing.T) {
	manager := setupTestManager(t)
	defer manager.Close()

	// Create test candles
	now := time.Now()
	candles := []ai.Candle{
		{Timestamp: now.Add(-3 * time.Minute), Open: 100, High: 102, Low: 99, Close: 101, Volume: 1000},
		{Timestamp: now.Add(-2 * time.Minute), Open: 101, High: 103, Low: 100, Close: 102, Volume: 1100},
		{Timestamp: now.Add(-1 * time.Minute), Open: 102, High: 104, Low: 101, Close: 103, Volume: 1200},
	}

	// Save candles
	err := manager.TimeframeCache.SaveCandles("BTC-USDT", "5m", candles)
	assert.NoError(t, err)

	// Load candles
	loaded, err := manager.TimeframeCache.LoadCandles("BTC-USDT", "5m", 10)
	assert.NoError(t, err)
	assert.Len(t, loaded, 3)
	assert.Equal(t, candles[0].Close, loaded[0].Close)
}

func TestTimeframeDataCache_TTL(t *testing.T) {
	manager := setupTestManager(t)
	defer manager.Close()

	cache := manager.TimeframeCache

	// Test different timeframe TTLs
	tests := []struct {
		timeframe string
		expected  time.Duration
	}{
		{"1m", 30 * time.Second},
		{"5m", 2 * time.Minute},
		{"15m", 5 * time.Minute},
		{"1h", 15 * time.Minute},
		{"4h", 1 * time.Hour},
		{"1d", 4 * time.Hour},
		{"1w", 24 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.timeframe, func(t *testing.T) {
			ttl := cache.getTTL(tt.timeframe)
			assert.Equal(t, tt.expected, ttl)
		})
	}
}

func TestTimeframeDataCache_CleanExpired(t *testing.T) {
	manager := setupTestManager(t)
	defer manager.Close()

	// This test would need to manipulate time or wait
	// For now, just verify it doesn't error
	err := manager.TimeframeCache.CleanExpired()
	assert.NoError(t, err)
}

func TestTimeframeDataCache_GetCacheStats(t *testing.T) {
	manager := setupTestManager(t)
	defer manager.Close()

	// Save some candles
	candles := []ai.Candle{
		{Timestamp: time.Now(), Open: 100, High: 102, Low: 99, Close: 101, Volume: 1000},
	}

	err := manager.TimeframeCache.SaveCandles("BTC-USDT", "5m", candles)
	require.NoError(t, err)

	// Get stats
	stats, err := manager.TimeframeCache.GetCacheStats()
	assert.NoError(t, err)
	assert.NotNil(t, stats)
	assert.Contains(t, stats, "total_entries")
	assert.Contains(t, stats, "by_timeframe")
}

func TestTimeframeDataCache_MultipleTimeframes(t *testing.T) {
	manager := setupTestManager(t)
	defer manager.Close()

	now := time.Now()
	candles5m := []ai.Candle{
		{Timestamp: now, Open: 100, High: 102, Low: 99, Close: 101, Volume: 1000},
	}
	candles15m := []ai.Candle{
		{Timestamp: now, Open: 100, High: 105, Low: 98, Close: 103, Volume: 3000},
	}

	// Save different timeframes
	err := manager.TimeframeCache.SaveCandles("BTC-USDT", "5m", candles5m)
	require.NoError(t, err)

	err = manager.TimeframeCache.SaveCandles("BTC-USDT", "15m", candles15m)
	require.NoError(t, err)

	// Load each
	loaded5m, err := manager.TimeframeCache.LoadCandles("BTC-USDT", "5m", 10)
	assert.NoError(t, err)
	assert.Len(t, loaded5m, 1)

	loaded15m, err := manager.TimeframeCache.LoadCandles("BTC-USDT", "15m", 10)
	assert.NoError(t, err)
	assert.Len(t, loaded15m, 1)
}
