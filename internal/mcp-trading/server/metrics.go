package server

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Métricas de Prometheus
var (
	// Contador de requests totales
	requestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "trading_mcp_requests_total",
			Help: "Total number of MCP requests",
		},
		[]string{"method", "status"},
	)

	// Histograma de duración de requests
	requestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "trading_mcp_request_duration_seconds",
			Help:    "Duration of MCP requests in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method"},
	)

	// Contador de ejecuciones de tools
	toolExecutions = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "trading_mcp_tool_executions_total",
			Help: "Total number of tool executions",
		},
		[]string{"tool", "status"},
	)

	// Histograma de duración de tools
	toolDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "trading_mcp_tool_duration_seconds",
			Help:    "Duration of tool executions in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"tool"},
	)

	// Contador de órdenes
	ordersTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "trading_mcp_orders_total",
			Help: "Total number of orders submitted",
		},
		[]string{"exchange", "pair", "side", "type", "status"},
	)

	// Gauge de conexión con GoCryptoTrader
	gctConnected = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "trading_mcp_gct_connected",
			Help: "Whether connected to GoCryptoTrader (1=connected, 0=disconnected)",
		},
	)

	// Gauge de tools registrados
	toolsRegistered = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "trading_mcp_tools_registered",
			Help: "Number of tools registered",
		},
	)

	// Contador de errores
	errorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "trading_mcp_errors_total",
			Help: "Total number of errors",
		},
		[]string{"type", "tool"},
	)
)

// recordRequest registra una métrica de request
func recordRequest(method string, status string, duration float64) {
	requestsTotal.WithLabelValues(method, status).Inc()
	requestDuration.WithLabelValues(method).Observe(duration)
}

// recordToolExecution registra una métrica de ejecución de tool
func recordToolExecution(tool string, status string, duration float64) {
	toolExecutions.WithLabelValues(tool, status).Inc()
	toolDuration.WithLabelValues(tool).Observe(duration)
}

// recordOrder registra una métrica de orden
func recordOrder(exchange, pair, side, orderType, status string) {
	ordersTotal.WithLabelValues(exchange, pair, side, orderType, status).Inc()
}

// updateGCTConnection actualiza el estado de conexión con GoCryptoTrader
func updateGCTConnection(connected bool) {
	if connected {
		gctConnected.Set(1)
	} else {
		gctConnected.Set(0)
	}
}

// updateToolsCount actualiza el número de tools registrados
func updateToolsCount(count int) {
	toolsRegistered.Set(float64(count))
}

// recordError registra un error
func recordError(errorType string, tool string) {
	errorsTotal.WithLabelValues(errorType, tool).Inc()
}
