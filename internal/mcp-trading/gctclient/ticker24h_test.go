package gctclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withStatsServer(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	prev := kucoinStatsURL
	kucoinStatsURL = srv.URL
	t.Cleanup(func() { kucoinStatsURL = prev; srv.Close() })
}

func TestNeeds24hStats(t *testing.T) {
	empty := &models.Ticker{Last: 100}
	full := &models.Ticker{Last: 100, High24h: 110, Low24h: 90, Change24h: 1.5}
	assert.True(t, needs24hStats("kucoin", "BTC-USDT", empty))
	assert.True(t, needs24hStats("KuCoin", "BTC-USDT", empty))
	assert.False(t, needs24hStats("kucoin", "BTC-USDT", full))
	assert.False(t, needs24hStats("kucoin", "XBTUSDTM", empty), "futures symbols are not spot")
	assert.False(t, needs24hStats("binance", "BTC-USDT", empty))
	assert.False(t, needs24hStats("kucoin", "BTC-USDT", nil))
}

func TestFetchKuCoinStats(t *testing.T) {
	t.Run("fills volume, high, low and change in percent", func(t *testing.T) {
		withStatsServer(t, func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "BTC-USDT", r.URL.Query().Get("symbol"))
			_, _ = w.Write([]byte(`{"code":"200000","data":{"vol":"15400.8","high":"65527.6","low":"64089.4","changeRate":"0.0123"}}`))
		})
		s, err := fetchKuCoinStats(context.Background(), "btc-usdt")
		require.NoError(t, err)
		tk := &models.Ticker{Last: 65000, Volume: 0.0025}
		apply24hStats(tk, s)
		assert.Equal(t, 15400.8, tk.Volume)
		assert.Equal(t, 65527.6, tk.High24h)
		assert.Equal(t, 64089.4, tk.Low24h)
		assert.InDelta(t, 1.23, tk.Change24h, 1e-9)
	})

	t.Run("unknown symbol is an error, not zeros", func(t *testing.T) {
		withStatsServer(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"code":"200000","data":{"vol":"","high":"","low":"","changeRate":""}}`))
		})
		_, err := fetchKuCoinStats(context.Background(), "NOPE-USDT")
		assert.Error(t, err)
	})

	t.Run("HTTP and API errors are reported", func(t *testing.T) {
		withStatsServer(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTooManyRequests) })
		_, err := fetchKuCoinStats(context.Background(), "BTC-USDT")
		assert.Error(t, err)
	})
}
