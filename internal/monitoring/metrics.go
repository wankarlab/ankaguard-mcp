package monitoring

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

// MetricsManager manages Prometheus metrics
type MetricsManager struct {
	logger   *logrus.Logger
	registry *prometheus.Registry
	server   *http.Server

	// HTTP metrics
	httpRequestsTotal    *prometheus.CounterVec
	httpRequestDuration  *prometheus.HistogramVec
	httpRequestsInFlight prometheus.Gauge

	// MCP metrics
	mcpRequestsTotal     *prometheus.CounterVec
	mcpRequestDuration   *prometheus.HistogramVec
	mcpToolExecutions    *prometheus.CounterVec
	mcpActiveConnections prometheus.Gauge

	// Trading metrics
	tradingOrdersTotal   *prometheus.CounterVec
	tradingOrderDuration *prometheus.HistogramVec
	tradingBalance       *prometheus.GaugeVec
	tradingPositions     *prometheus.GaugeVec

	// GoCryptoTrader metrics
	gctRequestsTotal    *prometheus.CounterVec
	gctRequestDuration  *prometheus.HistogramVec
	gctConnectionStatus *prometheus.GaugeVec

	// WebSocket metrics
	wsConnectionsTotal  *prometheus.CounterVec
	wsActiveConnections prometheus.Gauge
	wsMessagesTotal     *prometheus.CounterVec
	wsMessageDuration   *prometheus.HistogramVec

	// Notification metrics
	notificationsTotal *prometheus.CounterVec
	alertsTotal        *prometheus.CounterVec
	alertsActive       prometheus.Gauge

	// System metrics
	systemUptime      prometheus.Gauge
	systemMemoryUsage prometheus.Gauge
	systemCPUUsage    prometheus.Gauge
	systemGoroutines  prometheus.Gauge
}

// NewMetricsManager creates a new metrics manager
func NewMetricsManager(logger *logrus.Logger, port int) *MetricsManager {
	registry := prometheus.NewRegistry()

	mm := &MetricsManager{
		logger:   logger,
		registry: registry,
	}

	// Initialize metrics
	mm.initMetrics()

	// Register metrics with registry
	mm.registerMetrics()

	// Setup HTTP server
	mm.setupServer(port)

	return mm
}

// initMetrics initializes all Prometheus metrics
func (mm *MetricsManager) initMetrics() {
	// HTTP metrics
	mm.httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "endpoint", "status_code"},
	)

	mm.httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "endpoint"},
	)

	mm.httpRequestsInFlight = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "http_requests_in_flight",
			Help: "Number of HTTP requests currently being processed",
		},
	)

	// MCP metrics
	mm.mcpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mcp_requests_total",
			Help: "Total number of MCP requests",
		},
		[]string{"method", "tool", "status"},
	)

	mm.mcpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "mcp_request_duration_seconds",
			Help:    "MCP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "tool"},
	)

	mm.mcpToolExecutions = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mcp_tool_executions_total",
			Help: "Total number of MCP tool executions",
		},
		[]string{"tool", "status"},
	)

	mm.mcpActiveConnections = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "mcp_active_connections",
			Help: "Number of active MCP connections",
		},
	)

	// Trading metrics
	mm.tradingOrdersTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "trading_orders_total",
			Help: "Total number of trading orders",
		},
		[]string{"exchange", "symbol", "side", "type", "status"},
	)

	mm.tradingOrderDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "trading_order_duration_seconds",
			Help:    "Trading order execution duration in seconds",
			Buckets: []float64{0.1, 0.5, 1.0, 2.0, 5.0, 10.0, 30.0},
		},
		[]string{"exchange", "type"},
	)

	mm.tradingBalance = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "trading_balance",
			Help: "Current trading balance by asset",
		},
		[]string{"exchange", "asset", "type"},
	)

	mm.tradingPositions = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "trading_positions",
			Help: "Current trading positions",
		},
		[]string{"exchange", "symbol", "side"},
	)

	// GoCryptoTrader metrics
	mm.gctRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gct_requests_total",
			Help: "Total number of GoCryptoTrader requests",
		},
		[]string{"method", "endpoint", "status"},
	)

	mm.gctRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gct_request_duration_seconds",
			Help:    "GoCryptoTrader request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "endpoint"},
	)

	mm.gctConnectionStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gct_connection_status",
			Help: "GoCryptoTrader connection status (1=connected, 0=disconnected)",
		},
		[]string{"type"}, // "http" or "grpc"
	)

	// WebSocket metrics
	mm.wsConnectionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "websocket_connections_total",
			Help: "Total number of WebSocket connections",
		},
		[]string{"status"}, // "opened", "closed", "failed"
	)

	mm.wsActiveConnections = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "websocket_active_connections",
			Help: "Number of active WebSocket connections",
		},
	)

	mm.wsMessagesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "websocket_messages_total",
			Help: "Total number of WebSocket messages",
		},
		[]string{"direction", "type"}, // direction: "inbound"/"outbound", type: message type
	)

	mm.wsMessageDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "websocket_message_duration_seconds",
			Help:    "WebSocket message processing duration in seconds",
			Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0},
		},
		[]string{"type"},
	)

	// Notification metrics
	mm.notificationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "notifications_total",
			Help: "Total number of notifications sent",
		},
		[]string{"type", "channel", "status"}, // type: "alert"/"custom", channel: "websocket"/"push"/"slack"
	)

	mm.alertsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "alerts_total",
			Help: "Total number of alerts triggered",
		},
		[]string{"type", "severity", "status"},
	)

	mm.alertsActive = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "alerts_active",
			Help: "Number of active alerts",
		},
	)

	// System metrics
	mm.systemUptime = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "system_uptime_seconds",
			Help: "System uptime in seconds",
		},
	)

	mm.systemMemoryUsage = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "system_memory_usage_bytes",
			Help: "System memory usage in bytes",
		},
	)

	mm.systemCPUUsage = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "system_cpu_usage_percent",
			Help: "System CPU usage percentage",
		},
	)

	mm.systemGoroutines = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "system_goroutines",
			Help: "Number of goroutines",
		},
	)
}

// registerMetrics registers all metrics with the Prometheus registry
func (mm *MetricsManager) registerMetrics() {
	// HTTP metrics
	mm.registry.MustRegister(mm.httpRequestsTotal)
	mm.registry.MustRegister(mm.httpRequestDuration)
	mm.registry.MustRegister(mm.httpRequestsInFlight)

	// MCP metrics
	mm.registry.MustRegister(mm.mcpRequestsTotal)
	mm.registry.MustRegister(mm.mcpRequestDuration)
	mm.registry.MustRegister(mm.mcpToolExecutions)
	mm.registry.MustRegister(mm.mcpActiveConnections)

	// Trading metrics
	mm.registry.MustRegister(mm.tradingOrdersTotal)
	mm.registry.MustRegister(mm.tradingOrderDuration)
	mm.registry.MustRegister(mm.tradingBalance)
	mm.registry.MustRegister(mm.tradingPositions)

	// GoCryptoTrader metrics
	mm.registry.MustRegister(mm.gctRequestsTotal)
	mm.registry.MustRegister(mm.gctRequestDuration)
	mm.registry.MustRegister(mm.gctConnectionStatus)

	// WebSocket metrics
	mm.registry.MustRegister(mm.wsConnectionsTotal)
	mm.registry.MustRegister(mm.wsActiveConnections)
	mm.registry.MustRegister(mm.wsMessagesTotal)
	mm.registry.MustRegister(mm.wsMessageDuration)

	// Notification metrics
	mm.registry.MustRegister(mm.notificationsTotal)
	mm.registry.MustRegister(mm.alertsTotal)
	mm.registry.MustRegister(mm.alertsActive)

	// System metrics
	mm.registry.MustRegister(mm.systemUptime)
	mm.registry.MustRegister(mm.systemMemoryUsage)
	mm.registry.MustRegister(mm.systemCPUUsage)
	mm.registry.MustRegister(mm.systemGoroutines)
}

// setupServer sets up the Prometheus metrics HTTP server
func (mm *MetricsManager) setupServer(port int) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(mm.registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("/health", mm.healthHandler)

	mm.server = &http.Server{
		Addr:    ":" + strconv.Itoa(port),
		Handler: mux,
	}
}

// Start starts the metrics server
func (mm *MetricsManager) Start() error {
	mm.logger.WithField("port", mm.server.Addr).Info("Starting metrics server")

	go func() {
		if err := mm.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			mm.logger.WithError(err).Error("Metrics server failed")
		}
	}()

	return nil
}

// Stop stops the metrics server
func (mm *MetricsManager) Stop() error {
	mm.logger.Info("Stopping metrics server")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return mm.server.Shutdown(ctx)
}

// healthHandler handles health check requests for the metrics server
func (mm *MetricsManager) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// Metric recording methods

// RecordHTTPRequest records an HTTP request metric
func (mm *MetricsManager) RecordHTTPRequest(method, endpoint, statusCode string, duration time.Duration) {
	mm.httpRequestsTotal.WithLabelValues(method, endpoint, statusCode).Inc()
	mm.httpRequestDuration.WithLabelValues(method, endpoint).Observe(duration.Seconds())
}

// RecordHTTPRequestInFlight records in-flight HTTP requests
func (mm *MetricsManager) RecordHTTPRequestInFlight(delta float64) {
	mm.httpRequestsInFlight.Add(delta)
}

// RecordMCPRequest records an MCP request metric
func (mm *MetricsManager) RecordMCPRequest(method, tool, status string, duration time.Duration) {
	mm.mcpRequestsTotal.WithLabelValues(method, tool, status).Inc()
	mm.mcpRequestDuration.WithLabelValues(method, tool).Observe(duration.Seconds())
}

// RecordMCPToolExecution records an MCP tool execution
func (mm *MetricsManager) RecordMCPToolExecution(tool, status string) {
	mm.mcpToolExecutions.WithLabelValues(tool, status).Inc()
}

// SetMCPActiveConnections sets the number of active MCP connections
func (mm *MetricsManager) SetMCPActiveConnections(count float64) {
	mm.mcpActiveConnections.Set(count)
}

// RecordTradingOrder records a trading order metric
func (mm *MetricsManager) RecordTradingOrder(exchange, symbol, side, orderType, status string, duration time.Duration) {
	mm.tradingOrdersTotal.WithLabelValues(exchange, symbol, side, orderType, status).Inc()
	mm.tradingOrderDuration.WithLabelValues(exchange, orderType).Observe(duration.Seconds())
}

// SetTradingBalance sets trading balance metrics
func (mm *MetricsManager) SetTradingBalance(exchange, asset, balanceType string, amount float64) {
	mm.tradingBalance.WithLabelValues(exchange, asset, balanceType).Set(amount)
}

// SetTradingPosition sets trading position metrics
func (mm *MetricsManager) SetTradingPosition(exchange, symbol, side string, size float64) {
	mm.tradingPositions.WithLabelValues(exchange, symbol, side).Set(size)
}

// RecordGCTRequest records a GoCryptoTrader request metric
func (mm *MetricsManager) RecordGCTRequest(method, endpoint, status string, duration time.Duration) {
	mm.gctRequestsTotal.WithLabelValues(method, endpoint, status).Inc()
	mm.gctRequestDuration.WithLabelValues(method, endpoint).Observe(duration.Seconds())
}

// SetGCTConnectionStatus sets GoCryptoTrader connection status
func (mm *MetricsManager) SetGCTConnectionStatus(connType string, connected bool) {
	status := 0.0
	if connected {
		status = 1.0
	}
	mm.gctConnectionStatus.WithLabelValues(connType).Set(status)
}

// RecordWebSocketConnection records WebSocket connection metrics
func (mm *MetricsManager) RecordWebSocketConnection(status string) {
	mm.wsConnectionsTotal.WithLabelValues(status).Inc()
}

// SetWebSocketActiveConnections sets the number of active WebSocket connections
func (mm *MetricsManager) SetWebSocketActiveConnections(count float64) {
	mm.wsActiveConnections.Set(count)
}

// RecordWebSocketMessage records WebSocket message metrics
func (mm *MetricsManager) RecordWebSocketMessage(direction, messageType string, duration time.Duration) {
	mm.wsMessagesTotal.WithLabelValues(direction, messageType).Inc()
	mm.wsMessageDuration.WithLabelValues(messageType).Observe(duration.Seconds())
}

// RecordNotification records notification metrics
func (mm *MetricsManager) RecordNotification(notificationType, channel, status string) {
	mm.notificationsTotal.WithLabelValues(notificationType, channel, status).Inc()
}

// RecordAlert records alert metrics
func (mm *MetricsManager) RecordAlert(alertType, severity, status string) {
	mm.alertsTotal.WithLabelValues(alertType, severity, status).Inc()
}

// SetActiveAlerts sets the number of active alerts
func (mm *MetricsManager) SetActiveAlerts(count float64) {
	mm.alertsActive.Set(count)
}

// UpdateSystemMetrics updates system-level metrics
func (mm *MetricsManager) UpdateSystemMetrics() {
	// TODO: Implement actual system metrics collection
	// For now, set placeholder values
	mm.systemUptime.Set(time.Since(time.Now().Add(-1 * time.Hour)).Seconds())
	mm.systemMemoryUsage.Set(1024 * 1024 * 512) // 512MB placeholder
	mm.systemCPUUsage.Set(25.5)                 // 25.5% placeholder
	mm.systemGoroutines.Set(50)                 // 50 goroutines placeholder
}

// GetRegistry returns the Prometheus registry
func (mm *MetricsManager) GetRegistry() *prometheus.Registry {
	return mm.registry
}
