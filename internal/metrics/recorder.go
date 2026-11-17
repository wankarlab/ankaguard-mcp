// Package metrics provides Prometheus metrics recording helpers for the trading-mcp service.
package metrics

import (
	"sync"
	"time"
)

// MetricsRecorder defines the interface for recording metrics
// This interface allows for easy mocking in tests and potential alternative implementations
type MetricsRecorder interface {
	// HTTP Metrics
	RecordHTTPRequest(method, endpoint, status string, duration time.Duration)
	IncrementHTTPInFlight()
	DecrementHTTPInFlight()

	// MCP Tool Metrics
	RecordMCPToolCall(tool, status string, duration time.Duration)
	RecordMCPToolError(tool, errorType string)
	SetMCPActiveConnections(count float64)

	// Trading/Order Metrics
	RecordOrder(exchange, pair, side, orderType, status string, duration time.Duration)
	RecordOrderFill(exchange, pair, side string)
	RecordOrderCancel(exchange, pair, reason string)
	RecordOrderValue(exchange, side string, valueUSD float64)
	SetOpenOrders(exchange, pair string, count float64)
	SetPosition(exchange, pair, side string, size float64)
	SetPositionValue(exchange, pair string, valueUSD float64)

	// GCT Metrics
	RecordGCTCall(method, status string, duration time.Duration)
	RecordGCTError(method, errorType string)
	SetGCTConnectionStatus(connType string, connected bool)
	IncrementGCTReconnects()

	// Market Data Metrics
	RecordTickerUpdate(exchange, pair string)
	RecordOrderbookUpdate(exchange, pair string)
	RecordCandleUpdate(exchange, pair, interval string)
	RecordMarketDataLatency(exchange, dataType string, duration time.Duration)
	RecordMarketDataError(exchange, dataType, errorType string)

	// Risk Metrics
	RecordRiskLimitCheck(checkType, result string)
	RecordRiskLimitRejection(checkType, reason string)
	SetRiskExposure(exchange, exposureType string, valueUSD float64)
	SetRiskDrawdown(exchange, period string, percent float64)
	SetRiskLeverage(exchange, pair string, leverage float64)

	// Portfolio Metrics
	SetPortfolioValue(exchange string, valueUSD float64)
	SetPortfolioPnL(exchange, period string, pnlUSD float64)
	SetPortfolioBalance(exchange, asset, balanceType string, amount float64)

	// WebSocket Metrics
	RecordWSConnection(status string)
	SetWSActiveConnections(count float64)
	RecordWSMessage(direction, messageType string)
	RecordWSMessageLatency(messageType string, duration time.Duration)

	// System Metrics
	SetSystemUptime(seconds float64)
	SetSystemGoroutines(count float64)
	SetSystemMemory(bytes float64)
	SetSystemCPU(percent float64)

	// Bot Metrics
	RecordBotTrade(tenantID, botID, strategy, side, result string)
	SetBotActiveCount(tenantID, strategy, exchange string, count float64)
	SetBotPnL(tenantID, botID, strategy string, pnlUSD float64)
	RecordBotPnLRealized(tenantID, botID, strategy string, pnlUSD float64)
	SetBotWinRate(tenantID, botID, strategy string, winRatePercent float64)
	RecordBotError(tenantID, botID, errorType string)
	RecordBotLatency(tenantID, botID, decisionType string, duration time.Duration)
	RecordBotStateChange(tenantID, botID, fromState, toState string)

	// Subscription Metrics
	SetActiveSubscriptions(tier string, count float64)
	RecordSubscriptionChange(fromTier, toTier, changeType string)
	RecordFeatureUsage(tier, feature string)
	RecordFeatureLimitHit(tier, feature, limitType string)
	RecordAPICallByTier(tier, endpoint string)
	RecordBillingEvent(eventType, tier, status string)
	IncrementTrialConversions()

	// Circuit Breaker Metrics
	SetCircuitBreakerState(service, endpoint string, state int)
	RecordCircuitBreakerChange(service, fromState, toState string)
	RecordCircuitBreakerFailure(service, errorType string)
	RecordCircuitBreakerSuccess(service string)

	// Connection Pool Metrics
	SetDBConnections(database, pool string, active, idle int)
	RecordDBConnectionWait(database string, duration time.Duration)
	SetRedisConnections(pool, status string, count float64)

	// Tenant Metrics
	SetTenantActiveUsers(tenantID string, count float64)
	RecordTenantAPIRequest(tenantID, method, endpoint string)
	RecordTenantTradeVolume(tenantID, exchange string, volumeUSD float64)
}

// Recorder provides convenient methods for recording metrics
// Implements the MetricsRecorder interface
type Recorder struct {
	metrics *TradingMetrics
	mu      sync.RWMutex
}

// Ensure Recorder implements MetricsRecorder
var _ MetricsRecorder = (*Recorder)(nil)

// NewRecorder creates a new metrics recorder
func NewRecorder() *Recorder {
	return &Recorder{
		metrics: GetTradingMetrics(),
	}
}

// Global recorder instance
var (
	globalRecorder *Recorder
	recorderOnce   sync.Once
)

// GetRecorder returns the global metrics recorder (thread-safe singleton)
func GetRecorder() *Recorder {
	recorderOnce.Do(func() {
		globalRecorder = NewRecorder()
	})
	return globalRecorder
}

// ============================================
// HTTP Request Metrics
// ============================================

// RecordHTTPRequest records an HTTP request with its duration and status
func (r *Recorder) RecordHTTPRequest(method, endpoint, status string, duration time.Duration) {
	r.metrics.HTTPRequestsTotal.WithLabelValues(method, endpoint, status).Inc()
	r.metrics.HTTPRequestDuration.WithLabelValues(method, endpoint).Observe(duration.Seconds())
}

// IncrementHTTPInFlight increments the in-flight request counter
func (r *Recorder) IncrementHTTPInFlight() {
	r.metrics.HTTPRequestsInFlight.Inc()
}

// DecrementHTTPInFlight decrements the in-flight request counter
func (r *Recorder) DecrementHTTPInFlight() {
	r.metrics.HTTPRequestsInFlight.Dec()
}

// ============================================
// MCP Tool Metrics
// ============================================

// RecordMCPToolCall records an MCP tool call with its status and duration
func (r *Recorder) RecordMCPToolCall(tool, status string, duration time.Duration) {
	r.metrics.MCPToolCallsTotal.WithLabelValues(tool, status).Inc()
	r.metrics.MCPToolDuration.WithLabelValues(tool).Observe(duration.Seconds())
}

// RecordMCPToolError records an MCP tool error
func (r *Recorder) RecordMCPToolError(tool, errorType string) {
	r.metrics.MCPToolErrors.WithLabelValues(tool, errorType).Inc()
}

// SetMCPActiveConnections sets the number of active MCP connections
func (r *Recorder) SetMCPActiveConnections(count float64) {
	r.metrics.MCPActiveConnections.Set(count)
}

// ============================================
// Trading/Order Metrics
// ============================================

// RecordOrder records an order submission
func (r *Recorder) RecordOrder(exchange, pair, side, orderType, status string, duration time.Duration) {
	r.metrics.OrdersTotal.WithLabelValues(exchange, pair, side, orderType, status).Inc()
	r.metrics.OrderDuration.WithLabelValues(exchange, orderType).Observe(duration.Seconds())
}

// RecordOrderFill records an order fill
func (r *Recorder) RecordOrderFill(exchange, pair, side string) {
	r.metrics.OrderFillsTotal.WithLabelValues(exchange, pair, side).Inc()
}

// RecordOrderCancel records an order cancellation
func (r *Recorder) RecordOrderCancel(exchange, pair, reason string) {
	r.metrics.OrderCancelsTotal.WithLabelValues(exchange, pair, reason).Inc()
}

// RecordOrderValue records the value of an order
func (r *Recorder) RecordOrderValue(exchange, side string, valueUSD float64) {
	r.metrics.OrderValue.WithLabelValues(exchange, side).Observe(valueUSD)
}

// SetOpenOrders sets the current number of open orders
func (r *Recorder) SetOpenOrders(exchange, pair string, count float64) {
	r.metrics.OpenOrdersGauge.WithLabelValues(exchange, pair).Set(count)
}

// SetPosition sets the current position size
func (r *Recorder) SetPosition(exchange, pair, side string, size float64) {
	r.metrics.PositionsGauge.WithLabelValues(exchange, pair, side).Set(size)
}

// SetPositionValue sets the current position value in USD
func (r *Recorder) SetPositionValue(exchange, pair string, valueUSD float64) {
	r.metrics.PositionValueGauge.WithLabelValues(exchange, pair).Set(valueUSD)
}

// ============================================
// GCT (GoCryptoTrader) Metrics
// ============================================

// RecordGCTCall records a GoCryptoTrader gRPC call
func (r *Recorder) RecordGCTCall(method, status string, duration time.Duration) {
	r.metrics.GCTCallsTotal.WithLabelValues(method, status).Inc()
	r.metrics.GCTLatency.WithLabelValues(method).Observe(duration.Seconds())
}

// RecordGCTError records a GoCryptoTrader error
func (r *Recorder) RecordGCTError(method, errorType string) {
	r.metrics.GCTErrorsTotal.WithLabelValues(method, errorType).Inc()
}

// SetGCTConnectionStatus sets the GoCryptoTrader connection status
func (r *Recorder) SetGCTConnectionStatus(connType string, connected bool) {
	status := 0.0
	if connected {
		status = 1.0
	}
	r.metrics.GCTConnectionStatus.WithLabelValues(connType).Set(status)
}

// IncrementGCTReconnects increments the reconnection counter
func (r *Recorder) IncrementGCTReconnects() {
	r.metrics.GCTReconnectsTotal.Inc()
}

// ============================================
// Market Data Metrics
// ============================================

// RecordTickerUpdate records a ticker update
func (r *Recorder) RecordTickerUpdate(exchange, pair string) {
	r.metrics.TickerUpdatesTotal.WithLabelValues(exchange, pair).Inc()
}

// RecordOrderbookUpdate records an orderbook update
func (r *Recorder) RecordOrderbookUpdate(exchange, pair string) {
	r.metrics.OrderbookUpdatesTotal.WithLabelValues(exchange, pair).Inc()
}

// RecordCandleUpdate records a candle update
func (r *Recorder) RecordCandleUpdate(exchange, pair, interval string) {
	r.metrics.CandleUpdatesTotal.WithLabelValues(exchange, pair, interval).Inc()
}

// RecordMarketDataLatency records market data fetch latency
func (r *Recorder) RecordMarketDataLatency(exchange, dataType string, duration time.Duration) {
	r.metrics.MarketDataLatency.WithLabelValues(exchange, dataType).Observe(duration.Seconds())
}

// RecordMarketDataError records a market data fetch error
func (r *Recorder) RecordMarketDataError(exchange, dataType, errorType string) {
	r.metrics.MarketDataErrors.WithLabelValues(exchange, dataType, errorType).Inc()
}

// ============================================
// Risk Metrics
// ============================================

// RecordRiskLimitCheck records a risk limit check
func (r *Recorder) RecordRiskLimitCheck(checkType, result string) {
	r.metrics.RiskLimitChecksTotal.WithLabelValues(checkType, result).Inc()
}

// RecordRiskLimitRejection records a risk limit rejection
func (r *Recorder) RecordRiskLimitRejection(checkType, reason string) {
	r.metrics.RiskLimitRejectionsTotal.WithLabelValues(checkType, reason).Inc()
}

// SetRiskExposure sets the current risk exposure
func (r *Recorder) SetRiskExposure(exchange, exposureType string, valueUSD float64) {
	r.metrics.RiskExposureGauge.WithLabelValues(exchange, exposureType).Set(valueUSD)
}

// SetRiskDrawdown sets the current drawdown percentage
func (r *Recorder) SetRiskDrawdown(exchange, period string, percent float64) {
	r.metrics.RiskDrawdownGauge.WithLabelValues(exchange, period).Set(percent)
}

// SetRiskLeverage sets the current leverage ratio
func (r *Recorder) SetRiskLeverage(exchange, pair string, leverage float64) {
	r.metrics.RiskLeverageGauge.WithLabelValues(exchange, pair).Set(leverage)
}

// ============================================
// Portfolio Metrics
// ============================================

// SetPortfolioValue sets the total portfolio value in USD
func (r *Recorder) SetPortfolioValue(exchange string, valueUSD float64) {
	r.metrics.PortfolioValueGauge.WithLabelValues(exchange).Set(valueUSD)
}

// SetPortfolioPnL sets the portfolio profit/loss in USD
func (r *Recorder) SetPortfolioPnL(exchange, period string, pnlUSD float64) {
	r.metrics.PortfolioPnLGauge.WithLabelValues(exchange, period).Set(pnlUSD)
}

// SetPortfolioBalance sets the portfolio balance for a specific asset
func (r *Recorder) SetPortfolioBalance(exchange, asset, balanceType string, amount float64) {
	r.metrics.PortfolioBalanceGauge.WithLabelValues(exchange, asset, balanceType).Set(amount)
}

// ============================================
// WebSocket Metrics
// ============================================

// RecordWSConnection records a WebSocket connection event
func (r *Recorder) RecordWSConnection(status string) {
	r.metrics.WSConnectionsTotal.WithLabelValues(status).Inc()
}

// SetWSActiveConnections sets the number of active WebSocket connections
func (r *Recorder) SetWSActiveConnections(count float64) {
	r.metrics.WSActiveConnections.Set(count)
}

// RecordWSMessage records a WebSocket message
func (r *Recorder) RecordWSMessage(direction, messageType string) {
	r.metrics.WSMessagesTotal.WithLabelValues(direction, messageType).Inc()
}

// RecordWSMessageLatency records WebSocket message processing latency
func (r *Recorder) RecordWSMessageLatency(messageType string, duration time.Duration) {
	r.metrics.WSMessageLatency.WithLabelValues(messageType).Observe(duration.Seconds())
}

// ============================================
// System Metrics
// ============================================

// SetSystemUptime sets the system uptime in seconds
func (r *Recorder) SetSystemUptime(seconds float64) {
	r.metrics.SystemUptimeGauge.Set(seconds)
}

// SetSystemGoroutines sets the number of goroutines
func (r *Recorder) SetSystemGoroutines(count float64) {
	r.metrics.SystemGoroutines.Set(count)
}

// SetSystemMemory sets the system memory usage in bytes
func (r *Recorder) SetSystemMemory(bytes float64) {
	r.metrics.SystemMemoryBytes.Set(bytes)
}

// SetSystemCPU sets the system CPU usage percentage
func (r *Recorder) SetSystemCPU(percent float64) {
	r.metrics.SystemCPUPercent.Set(percent)
}

// ============================================
// Convenience Functions (Package-level)
// ============================================

// RecordHTTPRequest is a convenience function for recording HTTP requests
func RecordHTTPRequest(method, endpoint, status string, duration time.Duration) {
	GetRecorder().RecordHTTPRequest(method, endpoint, status, duration)
}

// RecordMCPToolCall is a convenience function for recording MCP tool calls
func RecordMCPToolCall(tool, status string, duration time.Duration) {
	GetRecorder().RecordMCPToolCall(tool, status, duration)
}

// RecordOrder is a convenience function for recording orders
func RecordOrder(exchange, pair, side, orderType, status string, duration time.Duration) {
	GetRecorder().RecordOrder(exchange, pair, side, orderType, status, duration)
}

// RecordGCTCall is a convenience function for recording GCT calls
func RecordGCTCall(method, status string, duration time.Duration) {
	GetRecorder().RecordGCTCall(method, status, duration)
}

// RecordTickerUpdate is a convenience function for recording ticker updates
func RecordTickerUpdate(exchange, pair string) {
	GetRecorder().RecordTickerUpdate(exchange, pair)
}

// RecordOrderbookUpdate is a convenience function for recording orderbook updates
func RecordOrderbookUpdate(exchange, pair string) {
	GetRecorder().RecordOrderbookUpdate(exchange, pair)
}

// RecordRiskLimitCheck is a convenience function for recording risk limit checks
func RecordRiskLimitCheck(checkType, result string) {
	GetRecorder().RecordRiskLimitCheck(checkType, result)
}

// ============================================
// Bot Metrics
// ============================================

// RecordBotTrade records a trade executed by a bot
func (r *Recorder) RecordBotTrade(tenantID, botID, strategy, side, result string) {
	r.metrics.BotTradesTotal.WithLabelValues(tenantID, botID, strategy, side, result).Inc()
}

// SetBotActiveCount sets the number of active bots
func (r *Recorder) SetBotActiveCount(tenantID, strategy, exchange string, count float64) {
	r.metrics.BotActiveCount.WithLabelValues(tenantID, strategy, exchange).Set(count)
}

// SetBotPnL sets the current unrealized PnL for a bot
func (r *Recorder) SetBotPnL(tenantID, botID, strategy string, pnlUSD float64) {
	r.metrics.BotPnLGauge.WithLabelValues(tenantID, botID, strategy).Set(pnlUSD)
}

// RecordBotPnLRealized records realized PnL (adds to counter)
func (r *Recorder) RecordBotPnLRealized(tenantID, botID, strategy string, pnlUSD float64) {
	if pnlUSD > 0 {
		r.metrics.BotPnLTotal.WithLabelValues(tenantID, botID, strategy).Add(pnlUSD)
	}
}

// SetBotWinRate sets the win rate for a bot
func (r *Recorder) SetBotWinRate(tenantID, botID, strategy string, winRatePercent float64) {
	r.metrics.BotWinRate.WithLabelValues(tenantID, botID, strategy).Set(winRatePercent)
}

// RecordBotError records a bot error
func (r *Recorder) RecordBotError(tenantID, botID, errorType string) {
	r.metrics.BotErrors.WithLabelValues(tenantID, botID, errorType).Inc()
}

// RecordBotLatency records bot decision latency
func (r *Recorder) RecordBotLatency(tenantID, botID, decisionType string, duration time.Duration) {
	r.metrics.BotLatency.WithLabelValues(tenantID, botID, decisionType).Observe(duration.Seconds())
}

// RecordBotStateChange records a bot state transition
func (r *Recorder) RecordBotStateChange(tenantID, botID, fromState, toState string) {
	r.metrics.BotStateChanges.WithLabelValues(tenantID, botID, fromState, toState).Inc()
}

// ============================================
// Subscription Metrics
// ============================================

// SetActiveSubscriptions sets the count of active subscriptions for a tier
func (r *Recorder) SetActiveSubscriptions(tier string, count float64) {
	r.metrics.ActiveSubscriptions.WithLabelValues(tier).Set(count)
}

// RecordSubscriptionChange records a subscription change event
func (r *Recorder) RecordSubscriptionChange(fromTier, toTier, changeType string) {
	r.metrics.SubscriptionChanges.WithLabelValues(fromTier, toTier, changeType).Inc()
}

// RecordFeatureUsage records feature usage
func (r *Recorder) RecordFeatureUsage(tier, feature string) {
	r.metrics.FeatureUsage.WithLabelValues(tier, feature).Inc()
}

// RecordFeatureLimitHit records when a feature limit is reached
func (r *Recorder) RecordFeatureLimitHit(tier, feature, limitType string) {
	r.metrics.FeatureLimitHits.WithLabelValues(tier, feature, limitType).Inc()
}

// RecordAPICallByTier records API calls by subscription tier
func (r *Recorder) RecordAPICallByTier(tier, endpoint string) {
	r.metrics.APICallsByTier.WithLabelValues(tier, endpoint).Inc()
}

// RecordBillingEvent records a billing event
func (r *Recorder) RecordBillingEvent(eventType, tier, status string) {
	r.metrics.BillingEvents.WithLabelValues(eventType, tier, status).Inc()
}

// IncrementTrialConversions increments the trial conversion counter
func (r *Recorder) IncrementTrialConversions() {
	r.metrics.TrialConversions.Inc()
}

// ============================================
// Circuit Breaker Metrics
// ============================================

// CircuitBreakerStateValue constants for metric values
const (
	CircuitBreakerClosed   = 0
	CircuitBreakerOpen     = 1
	CircuitBreakerHalfOpen = 2
)

// SetCircuitBreakerState sets the current circuit breaker state
func (r *Recorder) SetCircuitBreakerState(service, endpoint string, state int) {
	r.metrics.CircuitBreakerState.WithLabelValues(service, endpoint).Set(float64(state))
}

// RecordCircuitBreakerChange records a circuit breaker state change
func (r *Recorder) RecordCircuitBreakerChange(service, fromState, toState string) {
	r.metrics.CircuitBreakerChanges.WithLabelValues(service, fromState, toState).Inc()
}

// RecordCircuitBreakerFailure records a circuit breaker failure
func (r *Recorder) RecordCircuitBreakerFailure(service, errorType string) {
	r.metrics.CircuitBreakerFailures.WithLabelValues(service, errorType).Inc()
}

// RecordCircuitBreakerSuccess records a circuit breaker success
func (r *Recorder) RecordCircuitBreakerSuccess(service string) {
	r.metrics.CircuitBreakerSuccess.WithLabelValues(service).Inc()
}

// ============================================
// Connection Pool Metrics
// ============================================

// SetDBConnections sets database connection counts
func (r *Recorder) SetDBConnections(database, pool string, active, idle int) {
	r.metrics.DBConnectionsActive.WithLabelValues(database, pool).Set(float64(active))
	r.metrics.DBConnectionsIdle.WithLabelValues(database, pool).Set(float64(idle))
}

// RecordDBConnectionWait records time spent waiting for a database connection
func (r *Recorder) RecordDBConnectionWait(database string, duration time.Duration) {
	r.metrics.DBConnectionWait.WithLabelValues(database).Observe(duration.Seconds())
}

// SetRedisConnections sets Valkey connection pool status
func (r *Recorder) SetRedisConnections(pool, status string, count float64) {
	r.metrics.RedisConnections.WithLabelValues(pool, status).Set(count)
}

// ============================================
// Tenant Metrics
// ============================================

// SetTenantActiveUsers sets the active user count for a tenant
func (r *Recorder) SetTenantActiveUsers(tenantID string, count float64) {
	r.metrics.TenantActiveUsers.WithLabelValues(tenantID).Set(count)
}

// RecordTenantAPIRequest records an API request for a tenant
func (r *Recorder) RecordTenantAPIRequest(tenantID, method, endpoint string) {
	r.metrics.TenantAPIRequests.WithLabelValues(tenantID, method, endpoint).Inc()
}

// RecordTenantTradeVolume records trade volume for a tenant
func (r *Recorder) RecordTenantTradeVolume(tenantID, exchange string, volumeUSD float64) {
	r.metrics.TenantTradeVolume.WithLabelValues(tenantID, exchange).Add(volumeUSD)
}

// ============================================
// Package-level Convenience Functions for New Metrics
// ============================================

// RecordBotTrade is a convenience function for recording bot trades
func RecordBotTrade(tenantID, botID, strategy, side, result string) {
	GetRecorder().RecordBotTrade(tenantID, botID, strategy, side, result)
}

// SetBotActiveCount is a convenience function for setting active bot count
func SetBotActiveCount(tenantID, strategy, exchange string, count float64) {
	GetRecorder().SetBotActiveCount(tenantID, strategy, exchange, count)
}

// RecordSubscriptionChange is a convenience function for recording subscription changes
func RecordSubscriptionChange(fromTier, toTier, changeType string) {
	GetRecorder().RecordSubscriptionChange(fromTier, toTier, changeType)
}

// RecordFeatureUsage is a convenience function for recording feature usage
func RecordFeatureUsage(tier, feature string) {
	GetRecorder().RecordFeatureUsage(tier, feature)
}

// SetCircuitBreakerState is a convenience function for setting circuit breaker state
func SetCircuitBreakerState(service, endpoint string, state int) {
	GetRecorder().SetCircuitBreakerState(service, endpoint, state)
}

// RecordCircuitBreakerChange is a convenience function for recording circuit breaker changes
func RecordCircuitBreakerChange(service, fromState, toState string) {
	GetRecorder().RecordCircuitBreakerChange(service, fromState, toState)
}

// RecordTenantAPIRequest is a convenience function for recording tenant API requests
func RecordTenantAPIRequest(tenantID, method, endpoint string) {
	GetRecorder().RecordTenantAPIRequest(tenantID, method, endpoint)
}

// RecordTenantTradeVolume is a convenience function for recording tenant trade volume
func RecordTenantTradeVolume(tenantID, exchange string, volumeUSD float64) {
	GetRecorder().RecordTenantTradeVolume(tenantID, exchange, volumeUSD)
}

// ============================================
// Metrics Timer Helper
// ============================================

// Timer is a helper for timing operations and recording to histograms
type Timer struct {
	start    time.Time
	recorder *Recorder
}

// NewTimer creates a new timer
func NewTimer() *Timer {
	return &Timer{
		start:    time.Now(),
		recorder: GetRecorder(),
	}
}

// Elapsed returns the elapsed duration since timer creation
func (t *Timer) Elapsed() time.Duration {
	return time.Since(t.start)
}

// RecordHTTPRequest records the elapsed time as an HTTP request metric
func (t *Timer) RecordHTTPRequest(method, endpoint, status string) {
	t.recorder.RecordHTTPRequest(method, endpoint, status, t.Elapsed())
}

// RecordMCPToolCall records the elapsed time as an MCP tool call metric
func (t *Timer) RecordMCPToolCall(tool, status string) {
	t.recorder.RecordMCPToolCall(tool, status, t.Elapsed())
}

// RecordOrder records the elapsed time as an order metric
func (t *Timer) RecordOrder(exchange, pair, side, orderType, status string) {
	t.recorder.RecordOrder(exchange, pair, side, orderType, status, t.Elapsed())
}

// RecordGCTCall records the elapsed time as a GCT call metric
func (t *Timer) RecordGCTCall(method, status string) {
	t.recorder.RecordGCTCall(method, status, t.Elapsed())
}

// RecordBotLatency records the elapsed time as bot decision latency
func (t *Timer) RecordBotLatency(tenantID, botID, decisionType string) {
	t.recorder.RecordBotLatency(tenantID, botID, decisionType, t.Elapsed())
}

// RecordDBConnectionWait records the elapsed time as DB connection wait
func (t *Timer) RecordDBConnectionWait(database string) {
	t.recorder.RecordDBConnectionWait(database, t.Elapsed())
}

// ============================================
// NoOp Recorder for Testing
// ============================================

// NoOpRecorder is a no-operation recorder for testing
type NoOpRecorder struct{}

// Ensure NoOpRecorder implements MetricsRecorder
var _ MetricsRecorder = (*NoOpRecorder)(nil)

// NewNoOpRecorder creates a new no-op recorder
func NewNoOpRecorder() *NoOpRecorder {
	return &NoOpRecorder{}
}

// All NoOpRecorder methods do nothing
func (r *NoOpRecorder) RecordHTTPRequest(method, endpoint, status string, duration time.Duration) {}
func (r *NoOpRecorder) IncrementHTTPInFlight()                                                    {}
func (r *NoOpRecorder) DecrementHTTPInFlight()                                                    {}
func (r *NoOpRecorder) RecordMCPToolCall(tool, status string, duration time.Duration)             {}
func (r *NoOpRecorder) RecordMCPToolError(tool, errorType string)                                 {}
func (r *NoOpRecorder) SetMCPActiveConnections(count float64)                                     {}
func (r *NoOpRecorder) RecordOrder(exchange, pair, side, orderType, status string, duration time.Duration) {
}
func (r *NoOpRecorder) RecordOrderFill(exchange, pair, side string)               {}
func (r *NoOpRecorder) RecordOrderCancel(exchange, pair, reason string)           {}
func (r *NoOpRecorder) RecordOrderValue(exchange, side string, valueUSD float64)  {}
func (r *NoOpRecorder) SetOpenOrders(exchange, pair string, count float64)        {}
func (r *NoOpRecorder) SetPosition(exchange, pair, side string, size float64)     {}
func (r *NoOpRecorder) SetPositionValue(exchange, pair string, valueUSD float64)  {}
func (r *NoOpRecorder) RecordGCTCall(method, status string, duration time.Duration) {
}
func (r *NoOpRecorder) RecordGCTError(method, errorType string)                                {}
func (r *NoOpRecorder) SetGCTConnectionStatus(connType string, connected bool)                {}
func (r *NoOpRecorder) IncrementGCTReconnects()                                                {}
func (r *NoOpRecorder) RecordTickerUpdate(exchange, pair string)                               {}
func (r *NoOpRecorder) RecordOrderbookUpdate(exchange, pair string)                            {}
func (r *NoOpRecorder) RecordCandleUpdate(exchange, pair, interval string)                     {}
func (r *NoOpRecorder) RecordMarketDataLatency(exchange, dataType string, duration time.Duration) {
}
func (r *NoOpRecorder) RecordMarketDataError(exchange, dataType, errorType string)         {}
func (r *NoOpRecorder) RecordRiskLimitCheck(checkType, result string)                      {}
func (r *NoOpRecorder) RecordRiskLimitRejection(checkType, reason string)                  {}
func (r *NoOpRecorder) SetRiskExposure(exchange, exposureType string, valueUSD float64)    {}
func (r *NoOpRecorder) SetRiskDrawdown(exchange, period string, percent float64)           {}
func (r *NoOpRecorder) SetRiskLeverage(exchange, pair string, leverage float64)            {}
func (r *NoOpRecorder) SetPortfolioValue(exchange string, valueUSD float64)                {}
func (r *NoOpRecorder) SetPortfolioPnL(exchange, period string, pnlUSD float64)            {}
func (r *NoOpRecorder) SetPortfolioBalance(exchange, asset, balanceType string, amount float64) {
}
func (r *NoOpRecorder) RecordWSConnection(status string)                                 {}
func (r *NoOpRecorder) SetWSActiveConnections(count float64)                             {}
func (r *NoOpRecorder) RecordWSMessage(direction, messageType string)                    {}
func (r *NoOpRecorder) RecordWSMessageLatency(messageType string, duration time.Duration) {
}
func (r *NoOpRecorder) SetSystemUptime(seconds float64)                                           {}
func (r *NoOpRecorder) SetSystemGoroutines(count float64)                                         {}
func (r *NoOpRecorder) SetSystemMemory(bytes float64)                                             {}
func (r *NoOpRecorder) SetSystemCPU(percent float64)                                              {}
func (r *NoOpRecorder) RecordBotTrade(tenantID, botID, strategy, side, result string)             {}
func (r *NoOpRecorder) SetBotActiveCount(tenantID, strategy, exchange string, count float64)     {}
func (r *NoOpRecorder) SetBotPnL(tenantID, botID, strategy string, pnlUSD float64)                {}
func (r *NoOpRecorder) RecordBotPnLRealized(tenantID, botID, strategy string, pnlUSD float64)     {}
func (r *NoOpRecorder) SetBotWinRate(tenantID, botID, strategy string, winRatePercent float64)    {}
func (r *NoOpRecorder) RecordBotError(tenantID, botID, errorType string)                          {}
func (r *NoOpRecorder) RecordBotLatency(tenantID, botID, decisionType string, duration time.Duration) {
}
func (r *NoOpRecorder) RecordBotStateChange(tenantID, botID, fromState, toState string)   {}
func (r *NoOpRecorder) SetActiveSubscriptions(tier string, count float64)                {}
func (r *NoOpRecorder) RecordSubscriptionChange(fromTier, toTier, changeType string)     {}
func (r *NoOpRecorder) RecordFeatureUsage(tier, feature string)                          {}
func (r *NoOpRecorder) RecordFeatureLimitHit(tier, feature, limitType string)            {}
func (r *NoOpRecorder) RecordAPICallByTier(tier, endpoint string)                        {}
func (r *NoOpRecorder) RecordBillingEvent(eventType, tier, status string)                {}
func (r *NoOpRecorder) IncrementTrialConversions()                                       {}
func (r *NoOpRecorder) SetCircuitBreakerState(service, endpoint string, state int)       {}
func (r *NoOpRecorder) RecordCircuitBreakerChange(service, fromState, toState string)    {}
func (r *NoOpRecorder) RecordCircuitBreakerFailure(service, errorType string)            {}
func (r *NoOpRecorder) RecordCircuitBreakerSuccess(service string)                       {}
func (r *NoOpRecorder) SetDBConnections(database, pool string, active, idle int)         {}
func (r *NoOpRecorder) RecordDBConnectionWait(database string, duration time.Duration)   {}
func (r *NoOpRecorder) SetRedisConnections(pool, status string, count float64)           {}
func (r *NoOpRecorder) SetTenantActiveUsers(tenantID string, count float64)              {}
func (r *NoOpRecorder) RecordTenantAPIRequest(tenantID, method, endpoint string)         {}
func (r *NoOpRecorder) RecordTenantTradeVolume(tenantID, exchange string, volumeUSD float64) {
}
