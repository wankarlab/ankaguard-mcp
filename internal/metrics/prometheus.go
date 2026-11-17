// Package metrics provides Prometheus metrics for the trading-mcp service.
// This file contains comprehensive metrics definitions for monitoring:
// - HTTP request metrics (total, latency, status codes)
// - MCP tool metrics (calls per tool, latency, errors)
// - Trading metrics (orders, fills, cancellations)
// - GCT connection metrics (connected, latency, errors)
// - Market data metrics (ticker updates, orderbook updates)
// - Risk metrics (limit checks, rejections)
// - Bot metrics (active bots, trades, PnL)
// - Subscription metrics (active subscriptions by tier, feature usage)
// - Circuit breaker metrics (state changes, failures)
package metrics

import (
	"runtime"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	// Namespace for all trading-mcp metrics
	namespace = "trading_mcp"
)

// SubscriptionTier represents subscription tier levels
type SubscriptionTier string

const (
	TierFree       SubscriptionTier = "free"
	TierBasic      SubscriptionTier = "basic"
	TierPro        SubscriptionTier = "pro"
	TierEnterprise SubscriptionTier = "enterprise"
)

// TradingMetrics holds all Prometheus metrics for the trading-mcp service
type TradingMetrics struct {
	// HTTP Request Metrics
	HTTPRequestsTotal    *prometheus.CounterVec
	HTTPRequestDuration  *prometheus.HistogramVec
	HTTPRequestsInFlight prometheus.Gauge

	// MCP Tool Metrics
	MCPToolCallsTotal    *prometheus.CounterVec
	MCPToolDuration      *prometheus.HistogramVec
	MCPActiveConnections prometheus.Gauge
	MCPToolErrors        *prometheus.CounterVec

	// Trading/Order Metrics
	OrdersTotal         *prometheus.CounterVec
	OrderDuration       *prometheus.HistogramVec
	OrderFillsTotal     *prometheus.CounterVec
	OrderCancelsTotal   *prometheus.CounterVec
	OrderValue          *prometheus.HistogramVec
	OpenOrdersGauge     *prometheus.GaugeVec
	PositionsGauge      *prometheus.GaugeVec
	PositionValueGauge  *prometheus.GaugeVec

	// GCT (GoCryptoTrader) Connection Metrics
	GCTCallsTotal       *prometheus.CounterVec
	GCTLatency          *prometheus.HistogramVec
	GCTConnectionStatus *prometheus.GaugeVec
	GCTErrorsTotal      *prometheus.CounterVec
	GCTReconnectsTotal  prometheus.Counter

	// Market Data Metrics
	TickerUpdatesTotal    *prometheus.CounterVec
	OrderbookUpdatesTotal *prometheus.CounterVec
	CandleUpdatesTotal    *prometheus.CounterVec
	MarketDataLatency     *prometheus.HistogramVec
	MarketDataErrors      *prometheus.CounterVec

	// Risk Metrics
	RiskLimitChecksTotal     *prometheus.CounterVec
	RiskLimitRejectionsTotal *prometheus.CounterVec
	RiskExposureGauge        *prometheus.GaugeVec
	RiskDrawdownGauge        *prometheus.GaugeVec
	RiskLeverageGauge        *prometheus.GaugeVec

	// Portfolio Metrics
	PortfolioValueGauge   *prometheus.GaugeVec
	PortfolioPnLGauge     *prometheus.GaugeVec
	PortfolioBalanceGauge *prometheus.GaugeVec

	// WebSocket Metrics
	WSConnectionsTotal  *prometheus.CounterVec
	WSActiveConnections prometheus.Gauge
	WSMessagesTotal     *prometheus.CounterVec
	WSMessageLatency    *prometheus.HistogramVec

	// System Metrics
	SystemUptimeGauge    prometheus.Gauge
	SystemGoroutines     prometheus.Gauge
	SystemMemoryBytes    prometheus.Gauge
	SystemCPUPercent     prometheus.Gauge

	// Bot Metrics
	BotActiveCount    *prometheus.GaugeVec     // Number of active trading bots
	BotTradesTotal    *prometheus.CounterVec   // Total trades executed by bots
	BotPnLGauge       *prometheus.GaugeVec     // Current PnL per bot
	BotPnLTotal       *prometheus.CounterVec   // Total realized PnL (cumulative)
	BotWinRate        *prometheus.GaugeVec     // Win rate percentage per bot
	BotErrors         *prometheus.CounterVec   // Bot error count by type
	BotLatency        *prometheus.HistogramVec // Bot decision latency
	BotStateChanges   *prometheus.CounterVec   // Bot state transitions

	// Subscription Metrics
	ActiveSubscriptions  *prometheus.GaugeVec   // Active subscriptions by tier
	SubscriptionChanges  *prometheus.CounterVec // Subscription changes (upgrade/downgrade/cancel)
	FeatureUsage         *prometheus.CounterVec // Feature usage by tier and feature name
	FeatureLimitHits     *prometheus.CounterVec // Times a feature limit was hit
	APICallsByTier       *prometheus.CounterVec // API calls by subscription tier
	BillingEvents        *prometheus.CounterVec // Billing events (invoice, payment, refund)
	TrialConversions     prometheus.Counter     // Free trial to paid conversions

	// Circuit Breaker Metrics
	CircuitBreakerState    *prometheus.GaugeVec   // Current state (0=closed, 1=open, 2=half-open)
	CircuitBreakerChanges  *prometheus.CounterVec // State change events
	CircuitBreakerFailures *prometheus.CounterVec // Failure count by service
	CircuitBreakerSuccess  *prometheus.CounterVec // Success count by service

	// Connection Pool Metrics
	DBConnectionsActive *prometheus.GaugeVec // Active database connections
	DBConnectionsIdle   *prometheus.GaugeVec // Idle database connections
	DBConnectionWait    *prometheus.HistogramVec // Time spent waiting for connection
	RedisConnections    *prometheus.GaugeVec // Valkey connection pool status

	// Tenant Metrics
	TenantActiveUsers *prometheus.GaugeVec   // Active users per tenant
	TenantAPIRequests *prometheus.CounterVec // API requests per tenant
	TenantTradeVolume *prometheus.CounterVec // Trade volume per tenant (in USD)
}

// NewTradingMetrics creates and registers all trading metrics
func NewTradingMetrics() *TradingMetrics {
	return &TradingMetrics{
		// HTTP Request Metrics
		HTTPRequestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "http_requests_total",
				Help:      "Total number of HTTP requests processed",
			},
			[]string{"method", "endpoint", "status"},
		),

		HTTPRequestDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "http_request_duration_seconds",
				Help:      "HTTP request duration in seconds",
				Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
			},
			[]string{"method", "endpoint"},
		),

		HTTPRequestsInFlight: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "http_requests_in_flight",
				Help:      "Number of HTTP requests currently being processed",
			},
		),

		// MCP Tool Metrics
		MCPToolCallsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "mcp_tool_calls_total",
				Help:      "Total number of MCP tool calls",
			},
			[]string{"tool", "status"},
		),

		MCPToolDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "mcp_tool_duration_seconds",
				Help:      "MCP tool execution duration in seconds",
				Buckets:   []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10, 30},
			},
			[]string{"tool"},
		),

		MCPActiveConnections: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "mcp_active_connections",
				Help:      "Number of active MCP connections",
			},
		),

		MCPToolErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "mcp_tool_errors_total",
				Help:      "Total number of MCP tool execution errors",
			},
			[]string{"tool", "error_type"},
		),

		// Trading/Order Metrics (using gateway_ prefix to avoid conflict with server/metrics.go)
		OrdersTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "gateway_orders_submitted",
				Help:      "Total number of orders submitted via gateway",
			},
			[]string{"exchange", "pair", "side", "type", "status"},
		),

		OrderDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "order_duration_seconds",
				Help:      "Order submission duration in seconds",
				Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
			},
			[]string{"exchange", "type"},
		),

		OrderFillsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "order_fills_total",
				Help:      "Total number of order fills",
			},
			[]string{"exchange", "pair", "side"},
		),

		OrderCancelsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "order_cancels_total",
				Help:      "Total number of order cancellations",
			},
			[]string{"exchange", "pair", "reason"},
		),

		OrderValue: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "order_value_usd",
				Help:      "Order value in USD",
				Buckets:   []float64{10, 50, 100, 500, 1000, 5000, 10000, 50000, 100000},
			},
			[]string{"exchange", "side"},
		),

		OpenOrdersGauge: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "open_orders",
				Help:      "Current number of open orders",
			},
			[]string{"exchange", "pair"},
		),

		PositionsGauge: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "positions",
				Help:      "Current position size",
			},
			[]string{"exchange", "pair", "side"},
		),

		PositionValueGauge: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "position_value_usd",
				Help:      "Current position value in USD",
			},
			[]string{"exchange", "pair"},
		),

		// GCT Connection Metrics
		GCTCallsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "gct_calls_total",
				Help:      "Total number of GoCryptoTrader gRPC calls",
			},
			[]string{"method", "status"},
		),

		GCTLatency: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "gct_latency_seconds",
				Help:      "GoCryptoTrader gRPC call latency in seconds",
				Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
			},
			[]string{"method"},
		),

		GCTConnectionStatus: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "gct_connection_status",
				Help:      "GoCryptoTrader connection status (1=connected, 0=disconnected)",
			},
			[]string{"type"},
		),

		GCTErrorsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "gct_errors_total",
				Help:      "Total number of GoCryptoTrader errors",
			},
			[]string{"method", "error_type"},
		),

		GCTReconnectsTotal: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "gct_reconnects_total",
				Help:      "Total number of GoCryptoTrader reconnection attempts",
			},
		),

		// Market Data Metrics
		TickerUpdatesTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "ticker_updates_total",
				Help:      "Total number of ticker updates received",
			},
			[]string{"exchange", "pair"},
		),

		OrderbookUpdatesTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "orderbook_updates_total",
				Help:      "Total number of orderbook updates received",
			},
			[]string{"exchange", "pair"},
		),

		CandleUpdatesTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "candle_updates_total",
				Help:      "Total number of candle/OHLCV updates received",
			},
			[]string{"exchange", "pair", "interval"},
		),

		MarketDataLatency: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "market_data_latency_seconds",
				Help:      "Market data fetch latency in seconds",
				Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
			},
			[]string{"exchange", "data_type"},
		),

		MarketDataErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "market_data_errors_total",
				Help:      "Total number of market data fetch errors",
			},
			[]string{"exchange", "data_type", "error_type"},
		),

		// Risk Metrics
		RiskLimitChecksTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "risk_limit_checks_total",
				Help:      "Total number of risk limit checks performed",
			},
			[]string{"check_type", "result"},
		),

		RiskLimitRejectionsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "risk_limit_rejections_total",
				Help:      "Total number of risk limit rejections",
			},
			[]string{"check_type", "reason"},
		),

		RiskExposureGauge: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "risk_exposure_usd",
				Help:      "Current risk exposure in USD",
			},
			[]string{"exchange", "type"},
		),

		RiskDrawdownGauge: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "risk_drawdown_percent",
				Help:      "Current drawdown percentage",
			},
			[]string{"exchange", "period"},
		),

		RiskLeverageGauge: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "risk_leverage",
				Help:      "Current leverage ratio",
			},
			[]string{"exchange", "pair"},
		),

		// Portfolio Metrics
		PortfolioValueGauge: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "portfolio_value_usd",
				Help:      "Total portfolio value in USD",
			},
			[]string{"exchange"},
		),

		PortfolioPnLGauge: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "portfolio_pnl_usd",
				Help:      "Portfolio profit/loss in USD",
			},
			[]string{"exchange", "period"},
		),

		PortfolioBalanceGauge: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "portfolio_balance",
				Help:      "Portfolio balance by asset",
			},
			[]string{"exchange", "asset", "type"},
		),

		// WebSocket Metrics
		WSConnectionsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "websocket_connections_total",
				Help:      "Total number of WebSocket connections",
			},
			[]string{"status"},
		),

		WSActiveConnections: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "websocket_active_connections",
				Help:      "Number of active WebSocket connections",
			},
		),

		WSMessagesTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "websocket_messages_total",
				Help:      "Total number of WebSocket messages",
			},
			[]string{"direction", "type"},
		),

		WSMessageLatency: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "websocket_message_latency_seconds",
				Help:      "WebSocket message processing latency in seconds",
				Buckets:   []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1},
			},
			[]string{"type"},
		),

		// System Metrics
		SystemUptimeGauge: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "system_uptime_seconds",
				Help:      "System uptime in seconds",
			},
		),

		SystemGoroutines: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "system_goroutines",
				Help:      "Number of goroutines",
			},
		),

		SystemMemoryBytes: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "system_memory_bytes",
				Help:      "System memory usage in bytes",
			},
		),

		SystemCPUPercent: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "system_cpu_percent",
				Help:      "System CPU usage percentage",
			},
		),

		// Bot Metrics
		BotActiveCount: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "bot_active_count",
				Help:      "Number of active trading bots",
			},
			[]string{"tenant_id", "strategy", "exchange"},
		),

		BotTradesTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "bot_trades_total",
				Help:      "Total number of trades executed by bots",
			},
			[]string{"tenant_id", "bot_id", "strategy", "side", "result"},
		),

		BotPnLGauge: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "bot_pnl_current",
				Help:      "Current unrealized PnL per bot in USD",
			},
			[]string{"tenant_id", "bot_id", "strategy"},
		),

		BotPnLTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "bot_pnl_realized_total",
				Help:      "Total realized PnL by bots in USD",
			},
			[]string{"tenant_id", "bot_id", "strategy"},
		),

		BotWinRate: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "bot_win_rate",
				Help:      "Win rate percentage per bot (0-100)",
			},
			[]string{"tenant_id", "bot_id", "strategy"},
		),

		BotErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "bot_errors_total",
				Help:      "Total number of bot errors by type",
			},
			[]string{"tenant_id", "bot_id", "error_type"},
		),

		BotLatency: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "bot_decision_latency_seconds",
				Help:      "Bot decision-making latency in seconds",
				Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
			},
			[]string{"tenant_id", "bot_id", "decision_type"},
		),

		BotStateChanges: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "bot_state_changes_total",
				Help:      "Bot state transition events",
			},
			[]string{"tenant_id", "bot_id", "from_state", "to_state"},
		),

		// Subscription Metrics
		ActiveSubscriptions: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "subscriptions_active",
				Help:      "Number of active subscriptions by tier",
			},
			[]string{"tier"},
		),

		SubscriptionChanges: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "subscription_changes_total",
				Help:      "Subscription change events",
			},
			[]string{"from_tier", "to_tier", "change_type"},
		),

		FeatureUsage: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "feature_usage_total",
				Help:      "Feature usage count by tier and feature",
			},
			[]string{"tier", "feature"},
		),

		FeatureLimitHits: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "feature_limit_hits_total",
				Help:      "Number of times a feature limit was reached",
			},
			[]string{"tier", "feature", "limit_type"},
		),

		APICallsByTier: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "api_calls_by_tier_total",
				Help:      "API calls by subscription tier",
			},
			[]string{"tier", "endpoint"},
		),

		BillingEvents: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "billing_events_total",
				Help:      "Billing events (invoice, payment, refund, failed)",
			},
			[]string{"event_type", "tier", "status"},
		),

		TrialConversions: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "trial_conversions_total",
				Help:      "Total free trial to paid conversions",
			},
		),

		// Circuit Breaker Metrics
		CircuitBreakerState: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "circuit_breaker_state",
				Help:      "Current circuit breaker state (0=closed, 1=open, 2=half_open)",
			},
			[]string{"service", "endpoint"},
		),

		CircuitBreakerChanges: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "circuit_breaker_state_changes_total",
				Help:      "Circuit breaker state change events",
			},
			[]string{"service", "from_state", "to_state"},
		),

		CircuitBreakerFailures: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "circuit_breaker_failures_total",
				Help:      "Circuit breaker failure count",
			},
			[]string{"service", "error_type"},
		),

		CircuitBreakerSuccess: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "circuit_breaker_success_total",
				Help:      "Circuit breaker success count",
			},
			[]string{"service"},
		),

		// Connection Pool Metrics
		DBConnectionsActive: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "db_connections_active",
				Help:      "Number of active database connections",
			},
			[]string{"database", "pool"},
		),

		DBConnectionsIdle: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "db_connections_idle",
				Help:      "Number of idle database connections",
			},
			[]string{"database", "pool"},
		),

		DBConnectionWait: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "db_connection_wait_seconds",
				Help:      "Time spent waiting for database connection",
				Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5},
			},
			[]string{"database"},
		),

		RedisConnections: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "redis_connections",
				Help:      "Valkey connection pool status",
			},
			[]string{"pool", "status"},
		),

		// Tenant Metrics
		TenantActiveUsers: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "tenant_active_users",
				Help:      "Number of active users per tenant",
			},
			[]string{"tenant_id"},
		),

		TenantAPIRequests: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "tenant_api_requests_total",
				Help:      "API requests per tenant",
			},
			[]string{"tenant_id", "method", "endpoint"},
		),

		TenantTradeVolume: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "tenant_trade_volume_usd_total",
				Help:      "Total trade volume per tenant in USD",
			},
			[]string{"tenant_id", "exchange"},
		),
	}
}

// StartSystemMetricsCollector starts a goroutine that periodically collects system metrics
func (tm *TradingMetrics) StartSystemMetricsCollector(interval time.Duration) {
	if interval <= 0 {
		interval = 15 * time.Second
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for range ticker.C {
			tm.collectSystemMetrics()
		}
	}()
}

// collectSystemMetrics collects current system metrics
func (tm *TradingMetrics) collectSystemMetrics() {
	// Goroutines
	tm.SystemGoroutines.Set(float64(runtime.NumGoroutine()))

	// Memory stats
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	tm.SystemMemoryBytes.Set(float64(memStats.Alloc))
}

// Global instance of trading metrics with sync.Once for thread-safe initialization
var (
	tradingMetrics *TradingMetrics
	metricsOnce    sync.Once
)

// GetTradingMetrics returns the global trading metrics instance (thread-safe singleton)
func GetTradingMetrics() *TradingMetrics {
	metricsOnce.Do(func() {
		tradingMetrics = NewTradingMetrics()
	})
	return tradingMetrics
}

// InitTradingMetrics initializes the global trading metrics instance
// This is now an alias for GetTradingMetrics to prevent duplicate registration
func InitTradingMetrics() *TradingMetrics {
	return GetTradingMetrics()
}
