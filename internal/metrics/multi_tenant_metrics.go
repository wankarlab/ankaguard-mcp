package metrics

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// MultiTenantMetrics provides comprehensive metrics collection for multi-tenant MATP platform
type MultiTenantMetrics struct {
	// Request metrics by tenant and user
	requestsTotal      *prometheus.CounterVec
	requestDuration    *prometheus.HistogramVec
	activeUsers        *prometheus.GaugeVec
	concurrentRequests *prometheus.GaugeVec

	// Kong Auth Level metrics
	authLevelDistribution *prometheus.GaugeVec
	authLevelRequests     *prometheus.CounterVec
	authLevelErrors       *prometheus.CounterVec

	// API usage metrics by Kong Auth Level
	apiUsageByLevel    *prometheus.CounterVec
	rateLimitHits      *prometheus.CounterVec
	rateLimitRemaining *prometheus.GaugeVec

	// Tenant-specific metrics
	tenantDataSize    *prometheus.GaugeVec
	tenantConnections *prometheus.GaugeVec
	tenantErrors      *prometheus.CounterVec

	// Performance metrics
	cacheHitRate        *prometheus.GaugeVec
	databaseConnections *prometheus.GaugeVec
	responseSize        *prometheus.HistogramVec

	// Business metrics
	subscriptionChanges *prometheus.CounterVec
	billingEvents       *prometheus.CounterVec
	featureUsage        *prometheus.CounterVec

	// System health metrics
	systemHealth    *prometheus.GaugeVec
	componentStatus *prometheus.GaugeVec

	mu          sync.RWMutex
	tenantStats map[string]*TenantStats
	userStats   map[string]*UserStats
}

// TenantStats tracks statistics for a specific tenant
type TenantStats struct {
	TenantID      string
	ActiveUsers   int64
	TotalRequests int64
	ErrorCount    int64
	DataSize      int64
	LastActivity  time.Time
	CreatedAt     time.Time
}

// UserStats tracks statistics for a specific user
type UserStats struct {
	UserID       string
	TenantID     string
	AuthLevel    int
	RequestCount int64
	ErrorCount   int64
	LastActivity time.Time
	SessionStart time.Time
}

// NewMultiTenantMetrics creates a new multi-tenant metrics collector
func NewMultiTenantMetrics() *MultiTenantMetrics {
	return &MultiTenantMetrics{
		// Request metrics with tenant and user dimensions
		requestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "matp_requests_total",
				Help: "Total number of requests by tenant, user, and Kong Auth level",
			},
			[]string{"tenant_id", "user_id", "auth_level", "method", "endpoint", "status"},
		),

		requestDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "matp_request_duration_seconds",
				Help:    "Request duration by tenant and Kong Auth level",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"tenant_id", "auth_level", "endpoint"},
		),

		activeUsers: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "matp_active_users",
				Help: "Number of active users by tenant and Kong Auth level",
			},
			[]string{"tenant_id", "auth_level"},
		),

		concurrentRequests: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "matp_concurrent_requests",
				Help: "Number of concurrent requests by tenant",
			},
			[]string{"tenant_id"},
		),

		// Kong Auth Level metrics
		authLevelDistribution: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "matp_auth_level_distribution",
				Help: "Distribution of users by Kong Auth level",
			},
			[]string{"auth_level", "tenant_id"},
		),

		authLevelRequests: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "matp_auth_level_requests_total",
				Help: "Total requests by Kong Auth level",
			},
			[]string{"auth_level", "feature", "status"},
		),

		authLevelErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "matp_auth_level_errors_total",
				Help: "Total errors by Kong Auth level",
			},
			[]string{"auth_level", "error_type", "tenant_id"},
		),

		// API usage metrics
		apiUsageByLevel: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "matp_api_usage_by_level_total",
				Help: "API usage by Kong Auth level and feature",
			},
			[]string{"auth_level", "api_endpoint", "tenant_id"},
		),

		rateLimitHits: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "matp_rate_limit_hits_total",
				Help: "Rate limit hits by Kong Auth level",
			},
			[]string{"auth_level", "tenant_id", "limit_type"},
		),

		rateLimitRemaining: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "matp_rate_limit_remaining",
				Help: "Remaining rate limit by Kong Auth level",
			},
			[]string{"auth_level", "tenant_id", "limit_type"},
		),

		// Tenant-specific metrics
		tenantDataSize: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "matp_tenant_data_size_bytes",
				Help: "Data size per tenant in bytes",
			},
			[]string{"tenant_id", "data_type"},
		),

		tenantConnections: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "matp_tenant_connections",
				Help: "Active connections per tenant",
			},
			[]string{"tenant_id", "connection_type"},
		),

		tenantErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "matp_tenant_errors_total",
				Help: "Total errors per tenant",
			},
			[]string{"tenant_id", "error_type", "component"},
		),

		// Performance metrics
		cacheHitRate: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "matp_cache_hit_rate",
				Help: "Cache hit rate by tenant and cache type",
			},
			[]string{"tenant_id", "cache_type"},
		),

		databaseConnections: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "matp_database_connections",
				Help: "Database connections by tenant",
			},
			[]string{"tenant_id", "database_type"},
		),

		responseSize: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "matp_response_size_bytes",
				Help:    "Response size by tenant and endpoint",
				Buckets: []float64{100, 1000, 10000, 100000, 1000000},
			},
			[]string{"tenant_id", "endpoint"},
		),

		// Business metrics
		subscriptionChanges: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "matp_subscription_changes_total",
				Help: "Subscription changes by tenant and type",
			},
			[]string{"tenant_id", "change_type", "from_level", "to_level"},
		),

		billingEvents: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "matp_billing_events_total",
				Help: "Billing events by tenant and event type",
			},
			[]string{"tenant_id", "event_type", "auth_level"},
		),

		featureUsage: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "matp_feature_usage_total",
				Help: "Feature usage by tenant and Kong Auth level",
			},
			[]string{"tenant_id", "auth_level", "feature", "usage_type"},
		),

		// System health metrics
		systemHealth: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "matp_system_health",
				Help: "System health score by component and tenant",
			},
			[]string{"component", "tenant_id"},
		),

		componentStatus: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "matp_component_status",
				Help: "Component status (1=healthy, 0=unhealthy)",
			},
			[]string{"component", "tenant_id"},
		),

		tenantStats: make(map[string]*TenantStats),
		userStats:   make(map[string]*UserStats),
	}
}

// RecordRequest records a request with tenant and user context
func (m *MultiTenantMetrics) RecordRequest(tenantID, userID string, authLevel int, method, endpoint, status string, duration time.Duration, responseSize int64) {
	authLevelStr := strconv.Itoa(authLevel)

	// Record basic request metrics
	m.requestsTotal.WithLabelValues(tenantID, userID, authLevelStr, method, endpoint, status).Inc()
	m.requestDuration.WithLabelValues(tenantID, authLevelStr, endpoint).Observe(duration.Seconds())
	m.responseSize.WithLabelValues(tenantID, endpoint).Observe(float64(responseSize))

	// Update user and tenant stats
	m.updateUserStats(userID, tenantID, authLevel)
	m.updateTenantStats(tenantID)

	// Record Kong Auth level specific metrics
	m.authLevelRequests.WithLabelValues(authLevelStr, endpoint, status).Inc()
	m.apiUsageByLevel.WithLabelValues(authLevelStr, endpoint, tenantID).Inc()
}

// RecordError records an error with tenant context
func (m *MultiTenantMetrics) RecordError(tenantID, userID string, authLevel int, errorType, component string) {
	authLevelStr := strconv.Itoa(authLevel)

	m.tenantErrors.WithLabelValues(tenantID, errorType, component).Inc()
	m.authLevelErrors.WithLabelValues(authLevelStr, errorType, tenantID).Inc()

	// Update user error stats
	m.mu.Lock()
	if userStats, exists := m.userStats[userID]; exists {
		userStats.ErrorCount++
	}
	if tenantStats, exists := m.tenantStats[tenantID]; exists {
		tenantStats.ErrorCount++
	}
	m.mu.Unlock()
}

// RecordRateLimitHit records a rate limit hit
func (m *MultiTenantMetrics) RecordRateLimitHit(tenantID string, authLevel int, limitType string, remaining int) {
	authLevelStr := strconv.Itoa(authLevel)

	m.rateLimitHits.WithLabelValues(authLevelStr, tenantID, limitType).Inc()
	m.rateLimitRemaining.WithLabelValues(authLevelStr, tenantID, limitType).Set(float64(remaining))
}

// RecordSubscriptionChange records a subscription level change
func (m *MultiTenantMetrics) RecordSubscriptionChange(tenantID, changeType string, fromLevel, toLevel int) {
	fromLevelStr := strconv.Itoa(fromLevel)
	toLevelStr := strconv.Itoa(toLevel)

	m.subscriptionChanges.WithLabelValues(tenantID, changeType, fromLevelStr, toLevelStr).Inc()
}

// RecordBillingEvent records a billing event
func (m *MultiTenantMetrics) RecordBillingEvent(tenantID, eventType string, authLevel int) {
	authLevelStr := strconv.Itoa(authLevel)

	m.billingEvents.WithLabelValues(tenantID, eventType, authLevelStr).Inc()
}

// RecordFeatureUsage records feature usage by tenant and auth level
func (m *MultiTenantMetrics) RecordFeatureUsage(tenantID string, authLevel int, feature, usageType string) {
	authLevelStr := strconv.Itoa(authLevel)

	m.featureUsage.WithLabelValues(tenantID, authLevelStr, feature, usageType).Inc()
}

// UpdateCacheMetrics updates cache performance metrics
func (m *MultiTenantMetrics) UpdateCacheMetrics(tenantID, cacheType string, hitRate float64) {
	m.cacheHitRate.WithLabelValues(tenantID, cacheType).Set(hitRate)
}

// UpdateDatabaseConnections updates database connection metrics
func (m *MultiTenantMetrics) UpdateDatabaseConnections(tenantID, dbType string, connections int) {
	m.databaseConnections.WithLabelValues(tenantID, dbType).Set(float64(connections))
}

// UpdateTenantDataSize updates tenant data size metrics
func (m *MultiTenantMetrics) UpdateTenantDataSize(tenantID, dataType string, sizeBytes int64) {
	m.tenantDataSize.WithLabelValues(tenantID, dataType).Set(float64(sizeBytes))
}

// UpdateSystemHealth updates system health metrics
func (m *MultiTenantMetrics) UpdateSystemHealth(component, tenantID string, healthScore float64, isHealthy bool) {
	m.systemHealth.WithLabelValues(component, tenantID).Set(healthScore)

	status := 0.0
	if isHealthy {
		status = 1.0
	}
	m.componentStatus.WithLabelValues(component, tenantID).Set(status)
}

// UpdateActiveUsers updates active user count for a tenant and auth level
func (m *MultiTenantMetrics) UpdateActiveUsers(tenantID string, authLevel int, count int) {
	authLevelStr := strconv.Itoa(authLevel)
	m.activeUsers.WithLabelValues(tenantID, authLevelStr).Set(float64(count))
}

// UpdateConcurrentRequests updates concurrent request count for a tenant
func (m *MultiTenantMetrics) UpdateConcurrentRequests(tenantID string, count int) {
	m.concurrentRequests.WithLabelValues(tenantID).Set(float64(count))
}

// GetTenantStats returns statistics for a specific tenant
func (m *MultiTenantMetrics) GetTenantStats(tenantID string) *TenantStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if stats, exists := m.tenantStats[tenantID]; exists {
		// Return a copy to avoid race conditions
		return &TenantStats{
			TenantID:      stats.TenantID,
			ActiveUsers:   stats.ActiveUsers,
			TotalRequests: stats.TotalRequests,
			ErrorCount:    stats.ErrorCount,
			DataSize:      stats.DataSize,
			LastActivity:  stats.LastActivity,
			CreatedAt:     stats.CreatedAt,
		}
	}

	return nil
}

// GetUserStats returns statistics for a specific user
func (m *MultiTenantMetrics) GetUserStats(userID string) *UserStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if stats, exists := m.userStats[userID]; exists {
		// Return a copy to avoid race conditions
		return &UserStats{
			UserID:       stats.UserID,
			TenantID:     stats.TenantID,
			AuthLevel:    stats.AuthLevel,
			RequestCount: stats.RequestCount,
			ErrorCount:   stats.ErrorCount,
			LastActivity: stats.LastActivity,
			SessionStart: stats.SessionStart,
		}
	}

	return nil
}

// GetAllTenantStats returns statistics for all tenants
func (m *MultiTenantMetrics) GetAllTenantStats() map[string]*TenantStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]*TenantStats)
	for tenantID, stats := range m.tenantStats {
		result[tenantID] = &TenantStats{
			TenantID:      stats.TenantID,
			ActiveUsers:   stats.ActiveUsers,
			TotalRequests: stats.TotalRequests,
			ErrorCount:    stats.ErrorCount,
			DataSize:      stats.DataSize,
			LastActivity:  stats.LastActivity,
			CreatedAt:     stats.CreatedAt,
		}
	}

	return result
}

// StartMetricsCollection starts background metrics collection
func (m *MultiTenantMetrics) StartMetricsCollection(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.collectAuthLevelDistribution()
			m.collectTenantConnectionMetrics()
			m.cleanupStaleStats()
		}
	}
}

// Helper methods

func (m *MultiTenantMetrics) updateUserStats(userID, tenantID string, authLevel int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()

	if userStats, exists := m.userStats[userID]; exists {
		userStats.RequestCount++
		userStats.LastActivity = now
	} else {
		m.userStats[userID] = &UserStats{
			UserID:       userID,
			TenantID:     tenantID,
			AuthLevel:    authLevel,
			RequestCount: 1,
			ErrorCount:   0,
			LastActivity: now,
			SessionStart: now,
		}
	}
}

func (m *MultiTenantMetrics) updateTenantStats(tenantID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()

	if tenantStats, exists := m.tenantStats[tenantID]; exists {
		tenantStats.TotalRequests++
		tenantStats.LastActivity = now
	} else {
		m.tenantStats[tenantID] = &TenantStats{
			TenantID:      tenantID,
			ActiveUsers:   0,
			TotalRequests: 1,
			ErrorCount:    0,
			DataSize:      0,
			LastActivity:  now,
			CreatedAt:     now,
		}
	}
}

func (m *MultiTenantMetrics) collectAuthLevelDistribution() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Count users by auth level and tenant
	distribution := make(map[string]map[int]int)

	for _, userStats := range m.userStats {
		if distribution[userStats.TenantID] == nil {
			distribution[userStats.TenantID] = make(map[int]int)
		}
		distribution[userStats.TenantID][userStats.AuthLevel]++
	}

	// Update metrics
	for tenantID, levels := range distribution {
		for authLevel, count := range levels {
			authLevelStr := strconv.Itoa(authLevel)
			m.authLevelDistribution.WithLabelValues(authLevelStr, tenantID).Set(float64(count))
		}
	}
}

func (m *MultiTenantMetrics) collectTenantConnectionMetrics() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Count active users per tenant
	activeUsers := make(map[string]int)
	cutoff := time.Now().Add(-5 * time.Minute) // Consider users active if they had activity in last 5 minutes

	for _, userStats := range m.userStats {
		if userStats.LastActivity.After(cutoff) {
			activeUsers[userStats.TenantID]++
		}
	}

	// Update tenant active user counts
	for tenantID, count := range activeUsers {
		m.tenantConnections.WithLabelValues(tenantID, "active_users").Set(float64(count))

		// Update tenant stats
		if tenantStats, exists := m.tenantStats[tenantID]; exists {
			tenantStats.ActiveUsers = int64(count)
		}
	}
}

func (m *MultiTenantMetrics) cleanupStaleStats() {
	m.mu.Lock()
	defer m.mu.Unlock()

	cutoff := time.Now().Add(-24 * time.Hour) // Remove stats older than 24 hours

	// Clean up stale user stats
	for userID, userStats := range m.userStats {
		if userStats.LastActivity.Before(cutoff) {
			delete(m.userStats, userID)
		}
	}

	// Clean up stale tenant stats (keep them longer)
	tenantCutoff := time.Now().Add(-7 * 24 * time.Hour) // 7 days
	for tenantID, tenantStats := range m.tenantStats {
		if tenantStats.LastActivity.Before(tenantCutoff) {
			delete(m.tenantStats, tenantID)
		}
	}
}

// Global metrics instance
var globalMetrics *MultiTenantMetrics

// InitializeMetrics initializes the global metrics instance
func InitializeMetrics() {
	globalMetrics = NewMultiTenantMetrics()
}

// GetMetrics returns the global metrics instance
func GetMetrics() *MultiTenantMetrics {
	if globalMetrics == nil {
		InitializeMetrics()
	}
	return globalMetrics
}
