package monitoring

import (
	"context"
	"runtime"
	"sync"
	"time"

	"github.com/rantipay/trading-mcp/internal/metrics"
	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/gct"
)

// HealthStatus represents the health status of a component
type HealthStatus string

const (
	HealthStatusHealthy   HealthStatus = "healthy"
	HealthStatusUnhealthy HealthStatus = "unhealthy"
	HealthStatusDegraded  HealthStatus = "degraded"
	HealthStatusUnknown   HealthStatus = "unknown"
)

// HealthCheck represents a health check result
type HealthCheck struct {
	Name        string                 `json:"name"`
	Status      HealthStatus           `json:"status"`
	Message     string                 `json:"message,omitempty"`
	Error       string                 `json:"error,omitempty"`
	Duration    time.Duration          `json:"duration"`
	Timestamp   time.Time              `json:"timestamp"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
	LastSuccess *time.Time             `json:"last_success,omitempty"`
	LastFailure *time.Time             `json:"last_failure,omitempty"`
}

// HealthChecker interface for health check implementations
type HealthChecker interface {
	Name() string
	Check(ctx context.Context) *HealthCheck
}

// HealthManager manages health checks for the system
type HealthManager struct {
	logger   *logrus.Logger
	checkers map[string]HealthChecker
	results  map[string]*HealthCheck
	mu       sync.RWMutex
	ctx      context.Context
	cancel   context.CancelFunc
}

// NewHealthManager creates a new health manager
func NewHealthManager(logger *logrus.Logger) *HealthManager {
	ctx, cancel := context.WithCancel(context.Background())

	return &HealthManager{
		logger:   logger,
		checkers: make(map[string]HealthChecker),
		results:  make(map[string]*HealthCheck),
		ctx:      ctx,
		cancel:   cancel,
	}
}

// RegisterChecker registers a health checker
func (hm *HealthManager) RegisterChecker(checker HealthChecker) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	hm.checkers[checker.Name()] = checker
	hm.logger.WithField("checker", checker.Name()).Info("Registered health checker")
}

// Start starts the health monitoring
func (hm *HealthManager) Start() error {
	hm.logger.Info("Starting health manager")

	// Run initial health checks
	hm.RunAllChecks()

	// Start periodic health checks
	go hm.periodicHealthChecks()

	return nil
}

// Stop stops the health monitoring
func (hm *HealthManager) Stop() error {
	hm.logger.Info("Stopping health manager")
	hm.cancel()
	return nil
}

// RunAllChecks runs all registered health checks
func (hm *HealthManager) RunAllChecks() map[string]*HealthCheck {
	hm.mu.RLock()
	checkers := make([]HealthChecker, 0, len(hm.checkers))
	for _, checker := range hm.checkers {
		checkers = append(checkers, checker)
	}
	hm.mu.RUnlock()

	results := make(map[string]*HealthCheck)
	var wg sync.WaitGroup
	var resultsMu sync.Mutex

	for _, checker := range checkers {
		wg.Add(1)
		go func(c HealthChecker) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(hm.ctx, 30*time.Second)
			defer cancel()

			result := c.Check(ctx)

			resultsMu.Lock()
			results[c.Name()] = result
			resultsMu.Unlock()
		}(checker)
	}

	wg.Wait()

	// Update stored results
	hm.mu.Lock()
	for name, result := range results {
		hm.results[name] = result
	}
	hm.mu.Unlock()

	return results
}

// GetHealthStatus returns the overall health status
func (hm *HealthManager) GetHealthStatus() *OverallHealth {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	overall := &OverallHealth{
		Status:    HealthStatusHealthy,
		Timestamp: time.Now(),
		Checks:    make(map[string]*HealthCheck),
		Summary:   make(map[string]int),
	}

	// Initialize summary counters
	overall.Summary["healthy"] = 0
	overall.Summary["unhealthy"] = 0
	overall.Summary["degraded"] = 0
	overall.Summary["unknown"] = 0

	// Collect all health check results
	for name, result := range hm.results {
		overall.Checks[name] = result
		overall.Summary[string(result.Status)]++

		// Determine overall status
		switch result.Status {
		case HealthStatusUnhealthy:
			overall.Status = HealthStatusUnhealthy
		case HealthStatusDegraded:
			if overall.Status == HealthStatusHealthy {
				overall.Status = HealthStatusDegraded
			}
		}
	}

	// Calculate uptime and other metrics
	overall.Uptime = hm.calculateUptime()
	overall.Version = "1.0.0" // Would come from build info

	return overall
}

// GetCheckerResult returns the result of a specific health checker
func (hm *HealthManager) GetCheckerResult(name string) (*HealthCheck, bool) {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	result, exists := hm.results[name]
	return result, exists
}

// periodicHealthChecks runs health checks periodically
func (hm *HealthManager) periodicHealthChecks() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-hm.ctx.Done():
			return
		case <-ticker.C:
			hm.RunAllChecks()
		}
	}
}

// calculateUptime calculates system uptime
func (hm *HealthManager) calculateUptime() time.Duration {
	// This would typically track actual start time
	// For now, return a placeholder
	return time.Since(time.Now().Add(-1 * time.Hour))
}

// OverallHealth represents the overall system health
type OverallHealth struct {
	Status    HealthStatus            `json:"status"`
	Timestamp time.Time               `json:"timestamp"`
	Uptime    time.Duration           `json:"uptime"`
	Version   string                  `json:"version"`
	Checks    map[string]*HealthCheck `json:"checks"`
	Summary   map[string]int          `json:"summary"`
}

// GoCryptoTraderHealthChecker checks GoCryptoTrader connectivity
type GoCryptoTraderHealthChecker struct {
	gctClient gct.GCTClient
}

// NewGoCryptoTraderHealthChecker creates a new GoCryptoTrader health checker
func NewGoCryptoTraderHealthChecker(gctClient gct.GCTClient) *GoCryptoTraderHealthChecker {
	return &GoCryptoTraderHealthChecker{
		gctClient: gctClient,
	}
}

func (c *GoCryptoTraderHealthChecker) Name() string {
	return "gocryptotrader"
}

func (c *GoCryptoTraderHealthChecker) Check(ctx context.Context) *HealthCheck {
	start := time.Now()
	check := &HealthCheck{
		Name:      c.Name(),
		Timestamp: start,
		Metadata:  make(map[string]interface{}),
	}

	// Test GoCryptoTrader connectivity
	err := c.gctClient.HealthCheck(ctx)
	duration := time.Since(start)
	check.Duration = duration

	// Get multi-tenant metrics
	mtMetrics := metrics.GetMetrics()

	if err != nil {
		check.Status = HealthStatusUnhealthy
		check.Error = err.Error()
		check.Message = "Failed to connect to GoCryptoTrader"

		// Record GCT connection failure metrics
		if mtMetrics != nil {
			mtMetrics.UpdateSystemHealth("gocryptotrader", "default", 0.0, false)
		}
		return check
	}

	// Check if client is connected
	if !c.gctClient.IsConnected() {
		check.Status = HealthStatusDegraded
		check.Message = "GoCryptoTrader client not connected"

		// Record disconnected state
		if mtMetrics != nil {
			mtMetrics.UpdateSystemHealth("gocryptotrader", "default", 50.0, false)
		}
	} else {
		check.Status = HealthStatusHealthy
		check.Message = "GoCryptoTrader connected successfully"

		// Record connected state
		if mtMetrics != nil {
			mtMetrics.UpdateSystemHealth("gocryptotrader", "default", 100.0, true)
		}
	}

	check.Metadata["response_time_ms"] = duration.Milliseconds()

	return check
}

// DatabasePinger interface for database connectivity check
type DatabasePinger interface {
	PingContext(ctx context.Context) error
}

// DatabaseHealthChecker checks database connectivity
type DatabaseHealthChecker struct {
	name   string
	db     DatabasePinger
	logger *logrus.Logger
}

// NewDatabaseHealthChecker creates a new database health checker
func NewDatabaseHealthChecker() *DatabaseHealthChecker {
	return &DatabaseHealthChecker{
		name: "database",
	}
}

// NewDatabaseHealthCheckerWithDB creates a database health checker with actual DB connection
func NewDatabaseHealthCheckerWithDB(db DatabasePinger, logger *logrus.Logger) *DatabaseHealthChecker {
	return &DatabaseHealthChecker{
		name:   "database",
		db:     db,
		logger: logger,
	}
}

func (c *DatabaseHealthChecker) Name() string {
	return c.name
}

func (c *DatabaseHealthChecker) Check(ctx context.Context) *HealthCheck {
	start := time.Now()
	check := &HealthCheck{
		Name:      c.Name(),
		Timestamp: start,
		Metadata:  make(map[string]interface{}),
	}

	// Check if database connection is configured
	if c.db == nil {
		duration := time.Since(start)
		check.Duration = duration
		check.Status = HealthStatusUnknown
		check.Message = "Database connection not configured - health check unavailable"
		check.Metadata["response_time_ms"] = duration.Milliseconds()
		check.Metadata["configured"] = false
		return check
	}

	// Perform actual database ping
	err := c.db.PingContext(ctx)
	duration := time.Since(start)
	check.Duration = duration
	check.Metadata["response_time_ms"] = duration.Milliseconds()
	check.Metadata["configured"] = true

	if err != nil {
		check.Status = HealthStatusUnhealthy
		check.Error = err.Error()
		check.Message = "Database connection failed"
		now := time.Now()
		check.LastFailure = &now
		if c.logger != nil {
			c.logger.WithError(err).Error("Database health check failed")
		}
		return check
	}

	check.Status = HealthStatusHealthy
	check.Message = "Database connection healthy"
	now := time.Now()
	check.LastSuccess = &now

	return check
}

// ValkeyPinger interface for Valkey/Valkey connectivity check
type RedisPinger interface {
	Ping(ctx context.Context) error
}

// ValkeyHealthChecker checks Valkey/Valkey connectivity
type RedisHealthChecker struct {
	name   string
	client RedisPinger
	logger *logrus.Logger
}

// NewRedisHealthChecker creates a new Valkey health checker (unconfigured)
func NewRedisHealthChecker() *RedisHealthChecker {
	return &RedisHealthChecker{
		name: "redis",
	}
}

// NewRedisHealthCheckerWithClient creates a Valkey health checker with actual client
func NewRedisHealthCheckerWithClient(client RedisPinger, logger *logrus.Logger) *RedisHealthChecker {
	return &RedisHealthChecker{
		name:   "redis",
		client: client,
		logger: logger,
	}
}

func (c *RedisHealthChecker) Name() string {
	return c.name
}

func (c *RedisHealthChecker) Check(ctx context.Context) *HealthCheck {
	start := time.Now()
	check := &HealthCheck{
		Name:      c.Name(),
		Timestamp: start,
		Metadata:  make(map[string]interface{}),
	}

	// Check if Valkey client is configured
	if c.client == nil {
		duration := time.Since(start)
		check.Duration = duration
		check.Status = HealthStatusUnknown
		check.Message = "Valkey/Valkey client not configured - health check unavailable"
		check.Metadata["response_time_ms"] = duration.Milliseconds()
		check.Metadata["configured"] = false
		return check
	}

	// Perform actual Valkey ping
	err := c.client.Ping(ctx)
	duration := time.Since(start)
	check.Duration = duration
	check.Metadata["response_time_ms"] = duration.Milliseconds()
	check.Metadata["configured"] = true

	if err != nil {
		check.Status = HealthStatusUnhealthy
		check.Error = err.Error()
		check.Message = "Valkey/Valkey connection failed"
		now := time.Now()
		check.LastFailure = &now
		if c.logger != nil {
			c.logger.WithError(err).Error("Valkey health check failed")
		}
		return check
	}

	check.Status = HealthStatusHealthy
	check.Message = "Valkey/Valkey connection healthy"
	now := time.Now()
	check.LastSuccess = &now

	return check
}

// WebSocketManager interface for WebSocket health checks
type WebSocketManager interface {
	GetConnectionCount() int
	IsRunning() bool
}

// WebSocketHealthChecker checks WebSocket service health
type WebSocketHealthChecker struct {
	name    string
	manager WebSocketManager
	logger  *logrus.Logger
}

// NewWebSocketHealthChecker creates a new WebSocket health checker (unconfigured)
func NewWebSocketHealthChecker() *WebSocketHealthChecker {
	return &WebSocketHealthChecker{
		name: "websocket",
	}
}

// NewWebSocketHealthCheckerWithManager creates a WebSocket health checker with actual manager
func NewWebSocketHealthCheckerWithManager(manager WebSocketManager, logger *logrus.Logger) *WebSocketHealthChecker {
	return &WebSocketHealthChecker{
		name:    "websocket",
		manager: manager,
		logger:  logger,
	}
}

func (c *WebSocketHealthChecker) Name() string {
	return c.name
}

func (c *WebSocketHealthChecker) Check(ctx context.Context) *HealthCheck {
	start := time.Now()
	check := &HealthCheck{
		Name:      c.Name(),
		Timestamp: start,
		Metadata:  make(map[string]interface{}),
	}

	// Check if WebSocket manager is configured
	if c.manager == nil {
		duration := time.Since(start)
		check.Duration = duration
		check.Status = HealthStatusUnknown
		check.Message = "WebSocket manager not configured - health check unavailable"
		check.Metadata["response_time_ms"] = duration.Milliseconds()
		check.Metadata["configured"] = false
		return check
	}

	duration := time.Since(start)
	check.Duration = duration
	check.Metadata["response_time_ms"] = duration.Milliseconds()
	check.Metadata["configured"] = true
	check.Metadata["active_connections"] = c.manager.GetConnectionCount()

	if !c.manager.IsRunning() {
		check.Status = HealthStatusUnhealthy
		check.Message = "WebSocket service is not running"
		now := time.Now()
		check.LastFailure = &now
		return check
	}

	check.Status = HealthStatusHealthy
	check.Message = "WebSocket service healthy"
	now := time.Now()
	check.LastSuccess = &now

	return check
}

// SystemHealthChecker checks system resources using runtime.MemStats
type SystemHealthChecker struct {
	name string
	// Thresholds for degraded/unhealthy status
	memoryWarningPercent float64
	memoryCriticalMB     uint64
}

// NewSystemHealthChecker creates a new system health checker
func NewSystemHealthChecker() *SystemHealthChecker {
	return &SystemHealthChecker{
		name:                 "system",
		memoryWarningPercent: 80.0, // Warn at 80% memory usage
		memoryCriticalMB:     100,  // Critical if less than 100MB available
	}
}

func (c *SystemHealthChecker) Name() string {
	return c.name
}

func (c *SystemHealthChecker) Check(ctx context.Context) *HealthCheck {
	start := time.Now()
	check := &HealthCheck{
		Name:      c.Name(),
		Timestamp: start,
		Metadata:  make(map[string]interface{}),
	}

	// Get actual memory statistics from Go runtime
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	duration := time.Since(start)
	check.Duration = duration
	check.Metadata["response_time_ms"] = duration.Milliseconds()

	// Calculate memory metrics
	allocMB := float64(memStats.Alloc) / 1024 / 1024
	totalAllocMB := float64(memStats.TotalAlloc) / 1024 / 1024
	sysMB := float64(memStats.Sys) / 1024 / 1024
	numGC := memStats.NumGC
	numGoroutines := runtime.NumGoroutine()

	check.Metadata["alloc_mb"] = allocMB
	check.Metadata["total_alloc_mb"] = totalAllocMB
	check.Metadata["sys_mb"] = sysMB
	check.Metadata["num_gc"] = numGC
	check.Metadata["goroutines"] = numGoroutines
	check.Metadata["heap_objects"] = memStats.HeapObjects

	// Get multi-tenant metrics and record system metrics
	mtMetrics := metrics.GetMetrics()
	if mtMetrics != nil {
		// Update system health metrics
		mtMetrics.UpdateSystemHealth("system", "default", 100.0, true)
	}

	// Determine health status based on actual metrics
	// Note: CPU usage requires OS-specific calls or external libraries,
	// so we focus on what Go runtime provides reliably

	// Check for concerning conditions
	var concerns []string

	// High goroutine count could indicate leaks
	if numGoroutines > 10000 {
		concerns = append(concerns, "high goroutine count")
	}

	// Very high memory allocation
	if sysMB > 4096 { // More than 4GB system memory used
		concerns = append(concerns, "high memory usage")
	}

	// Determine overall status
	if len(concerns) > 0 {
		if sysMB > 8192 || numGoroutines > 50000 {
			check.Status = HealthStatusUnhealthy
			check.Message = "System resources critical: " + concerns[0]
			now := time.Now()
			check.LastFailure = &now
			if mtMetrics != nil {
				mtMetrics.UpdateSystemHealth("system", "default", 0.0, false)
			}
		} else {
			check.Status = HealthStatusDegraded
			check.Message = "System resources degraded: " + concerns[0]
			if mtMetrics != nil {
				mtMetrics.UpdateSystemHealth("system", "default", 50.0, false)
			}
		}
	} else {
		check.Status = HealthStatusHealthy
		check.Message = "System resources healthy"
		now := time.Now()
		check.LastSuccess = &now
		if mtMetrics != nil {
			mtMetrics.UpdateSystemHealth("system", "default", 100.0, true)
		}
	}

	return check
}
