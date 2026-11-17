package services

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	botMetricsOnce sync.Once

	botPnLGauge     *prometheus.GaugeVec
	botTradesTotal  *prometheus.CounterVec
	botWinRate      *prometheus.GaugeVec
	botLLMCallsTotal *prometheus.CounterVec
	botOpenPositions *prometheus.GaugeVec
	botDailyLoss    *prometheus.GaugeVec
)

func initBotMetrics() {
	botMetricsOnce.Do(func() {
		botPnLGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "trading_bot_pnl_usd",
			Help: "Total P&L in USD for the trading bot",
		}, []string{"pair"})

		botTradesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "trading_bot_trades_total",
			Help: "Total number of trades executed",
		}, []string{"pair", "result"}) // result: "win", "loss", "unknown"

		botWinRate = prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "trading_bot_win_rate",
			Help: "Win rate (0-1) for the trading bot",
		}, []string{"pair"})

		botLLMCallsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "trading_bot_llm_calls_total",
			Help: "Total LLM API calls",
		}, []string{"provider", "status"}) // status: "success", "error", "rate_limit", "cache_hit"

		botOpenPositions = prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "trading_bot_open_positions",
			Help: "Number of currently open positions",
		}, []string{"pair"})

		botDailyLoss = prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "trading_bot_daily_loss_usd",
			Help: "Current daily loss in USD",
		}, []string{"pair"})

		prometheus.MustRegister(
			botPnLGauge,
			botTradesTotal,
			botWinRate,
			botLLMCallsTotal,
			botOpenPositions,
			botDailyLoss,
		)
	})
}

// RecordTrade records a completed trade in metrics.
// pnlUSD > 0 = win, < 0 = loss.
func (bot *AITradingBot) RecordTrade(pnlUSD float64) {
	initBotMetrics()

	bot.mu.RLock()
	pair := bot.config.Pair
	bot.mu.RUnlock()

	result := "unknown"
	if pnlUSD > 0 {
		result = "win"
	} else if pnlUSD < 0 {
		result = "loss"
	}

	botTradesTotal.WithLabelValues(pair, result).Inc()
	botPnLGauge.WithLabelValues(pair).Add(pnlUSD)
}

// RecordLLMCall records an LLM API call result.
func (bot *AITradingBot) RecordLLMCall(provider, status string) {
	initBotMetrics()
	botLLMCallsTotal.WithLabelValues(provider, status).Inc()
}

// UpdatePositionMetrics updates the open positions and daily loss gauges.
func (bot *AITradingBot) UpdatePositionMetrics() {
	initBotMetrics()

	bot.mu.RLock()
	pair := bot.config.Pair
	dailyLoss := bot.dailyLoss
	openPos := len(bot.positionManager.GetAllPositions())
	bot.mu.RUnlock()

	botOpenPositions.WithLabelValues(pair).Set(float64(openPos))
	botDailyLoss.WithLabelValues(pair).Set(dailyLoss)
}

// UpdateWinRate recalculates and records the win rate.
// wins and total are the counts passed by the caller.
func (bot *AITradingBot) UpdateWinRate(wins, total int) {
	initBotMetrics()

	bot.mu.RLock()
	pair := bot.config.Pair
	bot.mu.RUnlock()

	if total > 0 {
		botWinRate.WithLabelValues(pair).Set(float64(wins) / float64(total))
	}
}
