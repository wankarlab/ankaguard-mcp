package middleware

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/metrics"
	"github.com/rantipay/trading-mcp/internal/monitoring"
)

// MetricsMiddleware provides HTTP metrics collection for Gin handlers
type MetricsMiddleware struct {
	multiTenantMetrics *metrics.MultiTenantMetrics
	metricsManager     *monitoring.MetricsManager
}

// NewMetricsMiddleware creates a new metrics middleware
func NewMetricsMiddleware(multiTenantMetrics *metrics.MultiTenantMetrics, metricsManager *monitoring.MetricsManager) *MetricsMiddleware {
	return &MetricsMiddleware{
		multiTenantMetrics: multiTenantMetrics,
		metricsManager:     metricsManager,
	}
}

// HTTPMetrics returns a Gin middleware that records HTTP request metrics
func (m *MetricsMiddleware) HTTPMetrics() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		// Record request start (in-flight)
		if m.metricsManager != nil {
			m.metricsManager.RecordHTTPRequestInFlight(1)
		}

		// Process request
		c.Next()

		// Calculate duration
		duration := time.Since(start)
		statusCode := c.Writer.Status()
		statusCodeStr := strconv.Itoa(statusCode)
		method := c.Request.Method
		endpoint := c.FullPath()
		if endpoint == "" {
			endpoint = c.Request.URL.Path
		}

		// Record request completion (in-flight)
		if m.metricsManager != nil {
			m.metricsManager.RecordHTTPRequestInFlight(-1)
			m.metricsManager.RecordHTTPRequest(method, endpoint, statusCodeStr, duration)
		}

		// Record multi-tenant metrics if tenant context is available
		if m.multiTenantMetrics != nil {
			tenantID := c.GetString("tenant_id")
			userID := c.GetString("user_id")
			authLevel := c.GetInt("user_level")

			if tenantID == "" {
				tenantID = "default"
			}
			if userID == "" {
				userID = "anonymous"
			}

			// Get response size from writer
			responseSize := int64(c.Writer.Size())
			if responseSize < 0 {
				responseSize = 0
			}

			// Record request with multi-tenant context
			m.multiTenantMetrics.RecordRequest(
				tenantID,
				userID,
				authLevel,
				method,
				endpoint,
				statusCodeStr,
				duration,
				responseSize,
			)

			// Record errors for non-2xx status codes
			if statusCode >= 400 {
				errorType := "client_error"
				if statusCode >= 500 {
					errorType = "server_error"
				}
				m.multiTenantMetrics.RecordError(tenantID, userID, authLevel, errorType, "http")
			}
		}
	}
}

// MCPMetrics returns a function to record MCP request metrics
func (m *MetricsMiddleware) RecordMCPRequest(method, tool, status string, duration time.Duration) {
	if m.metricsManager != nil {
		m.metricsManager.RecordMCPRequest(method, tool, status, duration)
	}
}

// RecordMCPToolExecution records an MCP tool execution
func (m *MetricsMiddleware) RecordMCPToolExecution(tool, status string) {
	if m.metricsManager != nil {
		m.metricsManager.RecordMCPToolExecution(tool, status)
	}
}

// RecordMCPActiveConnections sets the number of active MCP connections
func (m *MetricsMiddleware) SetMCPActiveConnections(count float64) {
	if m.metricsManager != nil {
		m.metricsManager.SetMCPActiveConnections(count)
	}
}

// RecordWebSocketConnection records WebSocket connection metrics
func (m *MetricsMiddleware) RecordWebSocketConnection(status string) {
	if m.metricsManager != nil {
		m.metricsManager.RecordWebSocketConnection(status)
	}
}

// SetWebSocketActiveConnections sets the number of active WebSocket connections
func (m *MetricsMiddleware) SetWebSocketActiveConnections(count float64) {
	if m.metricsManager != nil {
		m.metricsManager.SetWebSocketActiveConnections(count)
	}
}

// RecordWebSocketMessage records WebSocket message metrics
func (m *MetricsMiddleware) RecordWebSocketMessage(direction, messageType string, duration time.Duration) {
	if m.metricsManager != nil {
		m.metricsManager.RecordWebSocketMessage(direction, messageType, duration)
	}
}

// RecordTradingOrder records trading order metrics
func (m *MetricsMiddleware) RecordTradingOrder(exchange, symbol, side, orderType, status string, duration time.Duration) {
	if m.metricsManager != nil {
		m.metricsManager.RecordTradingOrder(exchange, symbol, side, orderType, status, duration)
	}
}

// SetTradingBalance sets trading balance metrics
func (m *MetricsMiddleware) SetTradingBalance(exchange, asset, balanceType string, amount float64) {
	if m.metricsManager != nil {
		m.metricsManager.SetTradingBalance(exchange, asset, balanceType, amount)
	}
}

// SetTradingPosition sets trading position metrics
func (m *MetricsMiddleware) SetTradingPosition(exchange, symbol, side string, size float64) {
	if m.metricsManager != nil {
		m.metricsManager.SetTradingPosition(exchange, symbol, side, size)
	}
}

// RecordGCTRequest records a GoCryptoTrader request metric
func (m *MetricsMiddleware) RecordGCTRequest(method, endpoint, status string, duration time.Duration) {
	if m.metricsManager != nil {
		m.metricsManager.RecordGCTRequest(method, endpoint, status, duration)
	}
}

// SetGCTConnectionStatus sets GoCryptoTrader connection status
func (m *MetricsMiddleware) SetGCTConnectionStatus(connType string, connected bool) {
	if m.metricsManager != nil {
		m.metricsManager.SetGCTConnectionStatus(connType, connected)
	}
}

// RecordFeatureUsage records feature usage by tenant and auth level
func (m *MetricsMiddleware) RecordFeatureUsage(tenantID string, authLevel int, feature, usageType string) {
	if m.multiTenantMetrics != nil {
		m.multiTenantMetrics.RecordFeatureUsage(tenantID, authLevel, feature, usageType)
	}
}

// RecordRateLimitHit records a rate limit hit
func (m *MetricsMiddleware) RecordRateLimitHit(tenantID string, authLevel int, limitType string, remaining int) {
	if m.multiTenantMetrics != nil {
		m.multiTenantMetrics.RecordRateLimitHit(tenantID, authLevel, limitType, remaining)
	}
}

// UpdateCacheMetrics updates cache performance metrics
func (m *MetricsMiddleware) UpdateCacheMetrics(tenantID, cacheType string, hitRate float64) {
	if m.multiTenantMetrics != nil {
		m.multiTenantMetrics.UpdateCacheMetrics(tenantID, cacheType, hitRate)
	}
}

// UpdateSystemHealth updates system health metrics
func (m *MetricsMiddleware) UpdateSystemHealth(component, tenantID string, healthScore float64, isHealthy bool) {
	if m.multiTenantMetrics != nil {
		m.multiTenantMetrics.UpdateSystemHealth(component, tenantID, healthScore, isHealthy)
	}
}

// RecordNotification records notification metrics
func (m *MetricsMiddleware) RecordNotification(notificationType, channel, status string) {
	if m.metricsManager != nil {
		m.metricsManager.RecordNotification(notificationType, channel, status)
	}
}

// RecordAlert records alert metrics
func (m *MetricsMiddleware) RecordAlert(alertType, severity, status string) {
	if m.metricsManager != nil {
		m.metricsManager.RecordAlert(alertType, severity, status)
	}
}

// SetActiveAlerts sets the number of active alerts
func (m *MetricsMiddleware) SetActiveAlerts(count float64) {
	if m.metricsManager != nil {
		m.metricsManager.SetActiveAlerts(count)
	}
}

// Global metrics middleware instance for easy access
var globalMetricsMiddleware *MetricsMiddleware

// InitializeMetricsMiddleware initializes the global metrics middleware
func InitializeMetricsMiddleware(multiTenantMetrics *metrics.MultiTenantMetrics, metricsManager *monitoring.MetricsManager) *MetricsMiddleware {
	globalMetricsMiddleware = NewMetricsMiddleware(multiTenantMetrics, metricsManager)
	return globalMetricsMiddleware
}

// GetMetricsMiddleware returns the global metrics middleware instance
func GetMetricsMiddleware() *MetricsMiddleware {
	return globalMetricsMiddleware
}
