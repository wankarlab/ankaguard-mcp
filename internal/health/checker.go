package health

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// Status represents the health status of a component
type Status string

const (
	StatusHealthy   Status = "healthy"
	StatusDegraded  Status = "degraded"
	StatusUnhealthy Status = "unhealthy"
	StatusUnknown   Status = "unknown"
)

// ComponentHealth represents the health of a single component
type ComponentHealth struct {
	Name      string                 `json:"name"`
	Status    Status                 `json:"status"`
	Message   string                 `json:"message,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// SystemHealth represents the overall system health
type SystemHealth struct {
	Status     Status                     `json:"status"`
	Components map[string]ComponentHealth `json:"components"`
	Timestamp  time.Time                  `json:"timestamp"`
	Uptime     time.Duration              `json:"uptime"`
}

// Checker interface for health checks
type Checker interface {
	Check(ctx context.Context) ComponentHealth
	Name() string
}

// Manager manages health checks for all components
type Manager struct {
	checkers  map[string]Checker
	mu        sync.RWMutex
	log       *logrus.Logger
	startTime time.Time
}

// NewManager creates a new health check manager
func NewManager(log *logrus.Logger) *Manager {
	return &Manager{
		checkers:  make(map[string]Checker),
		log:       log,
		startTime: time.Now(),
	}
}

// RegisterChecker registers a new health checker
func (m *Manager) RegisterChecker(checker Checker) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checkers[checker.Name()] = checker
	m.log.WithField("component", checker.Name()).Info("Health checker registered")
}

// CheckAll performs health checks on all registered components
func (m *Manager) CheckAll(ctx context.Context) interface{} {
	m.mu.RLock()
	checkers := make(map[string]Checker, len(m.checkers))
	for k, v := range m.checkers {
		checkers[k] = v
	}
	m.mu.RUnlock()

	components := make(map[string]ComponentHealth)
	overallStatus := StatusHealthy

	// Run all checks in parallel
	var wg sync.WaitGroup
	resultChan := make(chan ComponentHealth, len(checkers))

	for _, checker := range checkers {
		wg.Add(1)
		go func(c Checker) {
			defer wg.Done()
			checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			resultChan <- c.Check(checkCtx)
		}(checker)
	}

	// Wait for all checks to complete
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Collect results
	for health := range resultChan {
		components[health.Name] = health

		// Determine overall status
		switch health.Status {
		case StatusUnhealthy:
			overallStatus = StatusUnhealthy
		case StatusDegraded:
			if overallStatus == StatusHealthy {
				overallStatus = StatusDegraded
			}
		}
	}

	uptime := time.Since(m.startTime)

	// Return as map for JSON serialization
	return map[string]interface{}{
		"status":     string(overallStatus),
		"components": components,
		"timestamp":  time.Now().Format(time.RFC3339),
		"uptime":     uptime.String(),
	}
}

// GCTChecker checks GoCryptoTrader connectivity
type GCTChecker struct {
	client interface {
		Ping(ctx context.Context) error
	}
	log *logrus.Logger
}

// NewGCTChecker creates a new GoCryptoTrader health checker
func NewGCTChecker(client interface {
	Ping(ctx context.Context) error
}, log *logrus.Logger) *GCTChecker {
	return &GCTChecker{
		client: client,
		log:    log,
	}
}

// Name returns the checker name
func (c *GCTChecker) Name() string {
	return "gocryptotrader"
}

// Check performs the health check
func (c *GCTChecker) Check(ctx context.Context) ComponentHealth {
	health := ComponentHealth{
		Name:      c.Name(),
		Timestamp: time.Now(),
		Metadata:  make(map[string]interface{}),
	}

	start := time.Now()
	err := c.client.Ping(ctx)
	latency := time.Since(start)

	health.Metadata["latency_ms"] = latency.Milliseconds()

	if err != nil {
		health.Status = StatusUnhealthy
		health.Message = fmt.Sprintf("Failed to ping GoCryptoTrader: %v", err)
		return health
	}

	if latency > 1*time.Second {
		health.Status = StatusDegraded
		health.Message = fmt.Sprintf("High latency: %v", latency)
		return health
	}

	health.Status = StatusHealthy
	health.Message = "Connected"
	return health
}

// ExchangeChecker checks exchange connectivity
type ExchangeChecker struct {
	name     string
	exchange string
	client   interface {
		GetTicker(ctx context.Context, exchange, pair string) (interface{}, error)
	}
	log *logrus.Logger
}

// NewExchangeChecker creates a new exchange health checker
func NewExchangeChecker(name, exchange string, client interface {
	GetTicker(ctx context.Context, exchange, pair string) (interface{}, error)
}, log *logrus.Logger) *ExchangeChecker {
	return &ExchangeChecker{
		name:     name,
		exchange: exchange,
		client:   client,
		log:      log,
	}
}

// Name returns the checker name
func (c *ExchangeChecker) Name() string {
	return c.name
}

// Check performs the health check
func (c *ExchangeChecker) Check(ctx context.Context) ComponentHealth {
	health := ComponentHealth{
		Name:      c.Name(),
		Timestamp: time.Now(),
		Metadata:  make(map[string]interface{}),
	}

	health.Metadata["exchange"] = c.exchange

	start := time.Now()
	_, err := c.client.GetTicker(ctx, c.exchange, "BTC-USDT")
	latency := time.Since(start)

	health.Metadata["latency_ms"] = latency.Milliseconds()

	if err != nil {
		health.Status = StatusUnhealthy
		health.Message = fmt.Sprintf("Failed to get ticker: %v", err)
		return health
	}

	if latency > 2*time.Second {
		health.Status = StatusDegraded
		health.Message = fmt.Sprintf("High latency: %v", latency)
		return health
	}

	health.Status = StatusHealthy
	health.Message = "Connected"
	return health
}

// MemoryChecker checks memory usage
type MemoryChecker struct {
	maxMemoryMB uint64
	log         *logrus.Logger
}

// NewMemoryChecker creates a new memory health checker
func NewMemoryChecker(maxMemoryMB uint64, log *logrus.Logger) *MemoryChecker {
	return &MemoryChecker{
		maxMemoryMB: maxMemoryMB,
		log:         log,
	}
}

// Name returns the checker name
func (c *MemoryChecker) Name() string {
	return "memory"
}

// Check performs the health check
func (c *MemoryChecker) Check(ctx context.Context) ComponentHealth {
	health := ComponentHealth{
		Name:      c.Name(),
		Timestamp: time.Now(),
		Metadata:  make(map[string]interface{}),
	}

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	allocMB := memStats.Alloc / 1024 / 1024
	health.Metadata["alloc_mb"] = allocMB
	health.Metadata["total_alloc_mb"] = memStats.TotalAlloc / 1024 / 1024
	health.Metadata["sys_mb"] = memStats.Sys / 1024 / 1024
	health.Metadata["num_gc"] = memStats.NumGC
	health.Metadata["goroutines"] = runtime.NumGoroutine()

	if allocMB > c.maxMemoryMB {
		health.Status = StatusUnhealthy
		health.Message = fmt.Sprintf("Memory usage %dMB exceeds limit %dMB", allocMB, c.maxMemoryMB)
		return health
	}

	if allocMB > c.maxMemoryMB*80/100 {
		health.Status = StatusDegraded
		health.Message = fmt.Sprintf("Memory usage %dMB approaching limit %dMB", allocMB, c.maxMemoryMB)
		return health
	}

	health.Status = StatusHealthy
	health.Message = fmt.Sprintf("Memory usage %dMB within limits", allocMB)
	return health
}

// DatabaseChecker checks PostgreSQL database connectivity
type DatabaseChecker struct {
	name string
	db   *gorm.DB
	log  *logrus.Logger
}

// NewDatabaseChecker creates a new database health checker
func NewDatabaseChecker(name string, db *gorm.DB, log *logrus.Logger) *DatabaseChecker {
	return &DatabaseChecker{
		name: name,
		db:   db,
		log:  log,
	}
}

// Name returns the checker name
func (c *DatabaseChecker) Name() string {
	return c.name
}

// Check performs the database health check
func (c *DatabaseChecker) Check(ctx context.Context) ComponentHealth {
	health := ComponentHealth{
		Name:      c.Name(),
		Timestamp: time.Now(),
		Metadata:  make(map[string]interface{}),
	}

	if c.db == nil {
		health.Status = StatusUnknown
		health.Message = "Database not configured"
		return health
	}

	start := time.Now()

	// Get underlying SQL DB
	sqlDB, err := c.db.DB()
	if err != nil {
		health.Status = StatusUnhealthy
		health.Message = fmt.Sprintf("Failed to get database connection: %v", err)
		return health
	}

	// Ping database
	if err := sqlDB.PingContext(ctx); err != nil {
		health.Status = StatusUnhealthy
		health.Message = fmt.Sprintf("Database ping failed: %v", err)
		health.Metadata["latency_ms"] = time.Since(start).Milliseconds()
		return health
	}

	latency := time.Since(start)
	health.Metadata["latency_ms"] = latency.Milliseconds()

	// Get connection pool stats
	stats := sqlDB.Stats()
	health.Metadata["open_connections"] = stats.OpenConnections
	health.Metadata["in_use"] = stats.InUse
	health.Metadata["idle"] = stats.Idle
	health.Metadata["wait_count"] = stats.WaitCount
	health.Metadata["wait_duration_ms"] = stats.WaitDuration.Milliseconds()

	if latency > 2*time.Second {
		health.Status = StatusDegraded
		health.Message = fmt.Sprintf("High database latency: %v", latency)
		return health
	}

	health.Status = StatusHealthy
	health.Message = "Database connected"
	return health
}

// DatabaseSQLChecker checks database using sql.DB directly
type DatabaseSQLChecker struct {
	name string
	db   *sql.DB
	log  *logrus.Logger
}

// NewDatabaseSQLChecker creates a new database health checker using sql.DB
func NewDatabaseSQLChecker(name string, db *sql.DB, log *logrus.Logger) *DatabaseSQLChecker {
	return &DatabaseSQLChecker{
		name: name,
		db:   db,
		log:  log,
	}
}

// Name returns the checker name
func (c *DatabaseSQLChecker) Name() string {
	return c.name
}

// Check performs the database health check
func (c *DatabaseSQLChecker) Check(ctx context.Context) ComponentHealth {
	health := ComponentHealth{
		Name:      c.Name(),
		Timestamp: time.Now(),
		Metadata:  make(map[string]interface{}),
	}

	if c.db == nil {
		health.Status = StatusUnknown
		health.Message = "Database not configured"
		return health
	}

	start := time.Now()

	if err := c.db.PingContext(ctx); err != nil {
		health.Status = StatusUnhealthy
		health.Message = fmt.Sprintf("Database ping failed: %v", err)
		health.Metadata["latency_ms"] = time.Since(start).Milliseconds()
		return health
	}

	latency := time.Since(start)
	health.Metadata["latency_ms"] = latency.Milliseconds()

	stats := c.db.Stats()
	health.Metadata["open_connections"] = stats.OpenConnections
	health.Metadata["in_use"] = stats.InUse
	health.Metadata["idle"] = stats.Idle

	if latency > 2*time.Second {
		health.Status = StatusDegraded
		health.Message = fmt.Sprintf("High database latency: %v", latency)
		return health
	}

	health.Status = StatusHealthy
	health.Message = "Database connected"
	return health
}

// ValkeyChecker checks Valkey connectivity
type RedisChecker struct {
	name   string
	client ValkeyClient
	log    *logrus.Logger
}

// ValkeyClient interface for Valkey health checks
type ValkeyClient interface {
	Ping(ctx context.Context) error
	PoolStats() (active, idle int)
}

// NewRedisChecker creates a new Valkey health checker
func NewRedisChecker(name string, client ValkeyClient, log *logrus.Logger) *RedisChecker {
	return &RedisChecker{
		name:   name,
		client: client,
		log:    log,
	}
}

// Name returns the checker name
func (c *RedisChecker) Name() string {
	return c.name
}

// Check performs the Valkey health check
func (c *RedisChecker) Check(ctx context.Context) ComponentHealth {
	health := ComponentHealth{
		Name:      c.Name(),
		Timestamp: time.Now(),
		Metadata:  make(map[string]interface{}),
	}

	if c.client == nil {
		health.Status = StatusUnknown
		health.Message = "Valkey not configured"
		return health
	}

	start := time.Now()

	if err := c.client.Ping(ctx); err != nil {
		health.Status = StatusUnhealthy
		health.Message = fmt.Sprintf("Valkey ping failed: %v", err)
		health.Metadata["latency_ms"] = time.Since(start).Milliseconds()
		return health
	}

	latency := time.Since(start)
	health.Metadata["latency_ms"] = latency.Milliseconds()

	active, idle := c.client.PoolStats()
	health.Metadata["active_connections"] = active
	health.Metadata["idle_connections"] = idle

	if latency > 500*time.Millisecond {
		health.Status = StatusDegraded
		health.Message = fmt.Sprintf("High Valkey latency: %v", latency)
		return health
	}

	health.Status = StatusHealthy
	health.Message = "Valkey connected"
	return health
}

// HTTPServiceChecker checks an HTTP service health endpoint
type HTTPServiceChecker struct {
	name       string
	url        string
	httpClient *http.Client
	log        *logrus.Logger
}

// NewHTTPServiceChecker creates a new HTTP service health checker
func NewHTTPServiceChecker(name, url string, timeout time.Duration, log *logrus.Logger) *HTTPServiceChecker {
	return &HTTPServiceChecker{
		name: name,
		url:  url,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		log: log,
	}
}

// Name returns the checker name
func (c *HTTPServiceChecker) Name() string {
	return c.name
}

// Check performs the HTTP service health check
func (c *HTTPServiceChecker) Check(ctx context.Context) ComponentHealth {
	health := ComponentHealth{
		Name:      c.Name(),
		Timestamp: time.Now(),
		Metadata:  make(map[string]interface{}),
	}

	health.Metadata["url"] = c.url

	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		health.Status = StatusUnhealthy
		health.Message = fmt.Sprintf("Failed to create request: %v", err)
		return health
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		health.Status = StatusUnhealthy
		health.Message = fmt.Sprintf("Request failed: %v", err)
		health.Metadata["latency_ms"] = time.Since(start).Milliseconds()
		return health
	}
	defer resp.Body.Close()

	latency := time.Since(start)
	health.Metadata["latency_ms"] = latency.Milliseconds()
	health.Metadata["status_code"] = resp.StatusCode

	if resp.StatusCode >= 500 {
		health.Status = StatusUnhealthy
		health.Message = fmt.Sprintf("Service returned status %d", resp.StatusCode)
		return health
	}

	if resp.StatusCode >= 400 {
		health.Status = StatusDegraded
		health.Message = fmt.Sprintf("Service returned status %d", resp.StatusCode)
		return health
	}

	if latency > 2*time.Second {
		health.Status = StatusDegraded
		health.Message = fmt.Sprintf("High latency: %v", latency)
		return health
	}

	health.Status = StatusHealthy
	health.Message = "Service healthy"
	return health
}

// OdooChecker checks Odoo API connectivity
type OdooChecker struct {
	name       string
	baseURL    string
	apiKey     string
	httpClient *http.Client
	log        *logrus.Logger
}

// NewOdooChecker creates a new Odoo health checker
func NewOdooChecker(name, baseURL, apiKey string, log *logrus.Logger) *OdooChecker {
	return &OdooChecker{
		name:    name,
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		log: log,
	}
}

// Name returns the checker name
func (c *OdooChecker) Name() string {
	return c.name
}

// Check performs the Odoo health check
func (c *OdooChecker) Check(ctx context.Context) ComponentHealth {
	health := ComponentHealth{
		Name:      c.Name(),
		Timestamp: time.Now(),
		Metadata:  make(map[string]interface{}),
	}

	if c.baseURL == "" {
		health.Status = StatusUnknown
		health.Message = "Odoo not configured"
		return health
	}

	health.Metadata["base_url"] = c.baseURL

	start := time.Now()

	// Try to access Odoo version endpoint (usually publicly accessible)
	url := c.baseURL + "/web/webclient/version_info"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		health.Status = StatusUnhealthy
		health.Message = fmt.Sprintf("Failed to create request: %v", err)
		return health
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		health.Status = StatusUnhealthy
		health.Message = fmt.Sprintf("Odoo request failed: %v", err)
		health.Metadata["latency_ms"] = time.Since(start).Milliseconds()
		return health
	}
	defer resp.Body.Close()

	latency := time.Since(start)
	health.Metadata["latency_ms"] = latency.Milliseconds()
	health.Metadata["status_code"] = resp.StatusCode

	if resp.StatusCode >= 500 {
		health.Status = StatusUnhealthy
		health.Message = fmt.Sprintf("Odoo returned status %d", resp.StatusCode)
		return health
	}

	if latency > 3*time.Second {
		health.Status = StatusDegraded
		health.Message = fmt.Sprintf("High Odoo latency: %v", latency)
		return health
	}

	health.Status = StatusHealthy
	health.Message = "Odoo connected"
	return health
}

// CircuitBreakerChecker checks circuit breaker status
type CircuitBreakerChecker struct {
	name       string
	getBreaker func() (state string, failures int, lastFailure time.Time)
	log        *logrus.Logger
}

// NewCircuitBreakerChecker creates a new circuit breaker health checker
func NewCircuitBreakerChecker(name string, getBreaker func() (state string, failures int, lastFailure time.Time), log *logrus.Logger) *CircuitBreakerChecker {
	return &CircuitBreakerChecker{
		name:       name,
		getBreaker: getBreaker,
		log:        log,
	}
}

// Name returns the checker name
func (c *CircuitBreakerChecker) Name() string {
	return c.name
}

// Check performs the circuit breaker health check
func (c *CircuitBreakerChecker) Check(ctx context.Context) ComponentHealth {
	health := ComponentHealth{
		Name:      c.Name(),
		Timestamp: time.Now(),
		Metadata:  make(map[string]interface{}),
	}

	state, failures, lastFailure := c.getBreaker()
	health.Metadata["state"] = state
	health.Metadata["failures"] = failures
	if !lastFailure.IsZero() {
		health.Metadata["last_failure"] = lastFailure.Format(time.RFC3339)
	}

	switch state {
	case "open":
		health.Status = StatusUnhealthy
		health.Message = fmt.Sprintf("Circuit breaker is open with %d failures", failures)
	case "half_open":
		health.Status = StatusDegraded
		health.Message = "Circuit breaker is testing recovery"
	default:
		health.Status = StatusHealthy
		health.Message = "Circuit breaker is closed"
	}

	return health
}

// HealthResponse is the JSON response for health endpoints
type HealthResponse struct {
	Status     string                     `json:"status"`
	Components map[string]ComponentHealth `json:"components,omitempty"`
	Timestamp  string                     `json:"timestamp"`
	Uptime     string                     `json:"uptime,omitempty"`
	Version    string                     `json:"version,omitempty"`
}

// ToJSON serializes the health response
func (h *HealthResponse) ToJSON() ([]byte, error) {
	return json.Marshal(h)
}

// ReadyResponse is the JSON response for readiness endpoints
type ReadyResponse struct {
	Ready     bool                       `json:"ready"`
	Checks    map[string]ComponentHealth `json:"checks"`
	Timestamp string                     `json:"timestamp"`
}

// GetReadyResponse creates a readiness response from health check results
func (m *Manager) GetReadyResponse(ctx context.Context) *ReadyResponse {
	result := m.CheckAll(ctx)
	resultMap, ok := result.(map[string]interface{})
	if !ok {
		return &ReadyResponse{
			Ready:     false,
			Checks:    make(map[string]ComponentHealth),
			Timestamp: time.Now().Format(time.RFC3339),
		}
	}

	components, _ := resultMap["components"].(map[string]ComponentHealth)
	status, _ := resultMap["status"].(string)

	return &ReadyResponse{
		Ready:     status == string(StatusHealthy),
		Checks:    components,
		Timestamp: time.Now().Format(time.RFC3339),
	}
}

// IsReady returns true if all health checks pass
func (m *Manager) IsReady(ctx context.Context) bool {
	result := m.CheckAll(ctx)
	resultMap, ok := result.(map[string]interface{})
	if !ok {
		return false
	}
	status, _ := resultMap["status"].(string)
	return status == string(StatusHealthy)
}
