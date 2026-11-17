package hft

import (
	"sync/atomic"
	"time"
)

// Metrics tracks HFT system metrics using atomic operations for lock-free updates
type Metrics struct {
	// Order metrics
	ordersSubmitted uint64
	ordersExecuted  uint64
	ordersSucceeded uint64
	ordersFailed    uint64
	ordersCancelled uint64

	// Latency metrics (in microseconds)
	totalLatencyUs uint64
	minLatencyUs   uint64
	maxLatencyUs   uint64

	// Throughput metrics
	lastSecondCount uint64
	lastMinuteCount uint64

	// Error metrics
	networkErrors    uint64
	timeoutErrors    uint64
	balanceErrors    uint64
	validationErrors uint64

	// System metrics
	activeGoroutines uint64
	memoryAllocMB    uint64

	// Timestamps
	startTime time.Time
	lastReset time.Time
}

// NewMetrics creates a new metrics instance
func NewMetrics() *Metrics {
	now := time.Now()
	return &Metrics{
		startTime:    now,
		lastReset:    now,
		minLatencyUs: ^uint64(0), // Max uint64
	}
}

// RecordOrderSubmitted records an order submission
func (m *Metrics) RecordOrderSubmitted() {
	atomic.AddUint64(&m.ordersSubmitted, 1)
}

// RecordOrderExecuted records an order execution with latency
func (m *Metrics) RecordOrderExecuted(latencyUs uint64, success bool) {
	atomic.AddUint64(&m.ordersExecuted, 1)
	atomic.AddUint64(&m.totalLatencyUs, latencyUs)
	atomic.AddUint64(&m.lastSecondCount, 1)
	atomic.AddUint64(&m.lastMinuteCount, 1)

	if success {
		atomic.AddUint64(&m.ordersSucceeded, 1)
	} else {
		atomic.AddUint64(&m.ordersFailed, 1)
	}

	// Update min latency
	for {
		current := atomic.LoadUint64(&m.minLatencyUs)
		if latencyUs >= current {
			break
		}
		if atomic.CompareAndSwapUint64(&m.minLatencyUs, current, latencyUs) {
			break
		}
	}

	// Update max latency
	for {
		current := atomic.LoadUint64(&m.maxLatencyUs)
		if latencyUs <= current {
			break
		}
		if atomic.CompareAndSwapUint64(&m.maxLatencyUs, current, latencyUs) {
			break
		}
	}
}

// RecordOrderCancelled records an order cancellation
func (m *Metrics) RecordOrderCancelled() {
	atomic.AddUint64(&m.ordersCancelled, 1)
}

// RecordNetworkError records a network error
func (m *Metrics) RecordNetworkError() {
	atomic.AddUint64(&m.networkErrors, 1)
}

// RecordTimeoutError records a timeout error
func (m *Metrics) RecordTimeoutError() {
	atomic.AddUint64(&m.timeoutErrors, 1)
}

// RecordBalanceError records a balance error
func (m *Metrics) RecordBalanceError() {
	atomic.AddUint64(&m.balanceErrors, 1)
}

// RecordValidationError records a validation error
func (m *Metrics) RecordValidationError() {
	atomic.AddUint64(&m.validationErrors, 1)
}

// UpdateSystemMetrics updates system-level metrics
func (m *Metrics) UpdateSystemMetrics(goroutines uint64, memoryMB uint64) {
	atomic.StoreUint64(&m.activeGoroutines, goroutines)
	atomic.StoreUint64(&m.memoryAllocMB, memoryMB)
}

// ResetPerSecondCounters resets per-second counters (call every second)
func (m *Metrics) ResetPerSecondCounters() {
	atomic.StoreUint64(&m.lastSecondCount, 0)
}

// ResetPerMinuteCounters resets per-minute counters (call every minute)
func (m *Metrics) ResetPerMinuteCounters() {
	atomic.StoreUint64(&m.lastMinuteCount, 0)
}

// Snapshot returns a snapshot of current metrics
func (m *Metrics) Snapshot() MetricsSnapshot {
	return MetricsSnapshot{
		OrdersSubmitted:  atomic.LoadUint64(&m.ordersSubmitted),
		OrdersExecuted:   atomic.LoadUint64(&m.ordersExecuted),
		OrdersSucceeded:  atomic.LoadUint64(&m.ordersSucceeded),
		OrdersFailed:     atomic.LoadUint64(&m.ordersFailed),
		OrdersCancelled:  atomic.LoadUint64(&m.ordersCancelled),
		TotalLatencyUs:   atomic.LoadUint64(&m.totalLatencyUs),
		MinLatencyUs:     atomic.LoadUint64(&m.minLatencyUs),
		MaxLatencyUs:     atomic.LoadUint64(&m.maxLatencyUs),
		LastSecondCount:  atomic.LoadUint64(&m.lastSecondCount),
		LastMinuteCount:  atomic.LoadUint64(&m.lastMinuteCount),
		NetworkErrors:    atomic.LoadUint64(&m.networkErrors),
		TimeoutErrors:    atomic.LoadUint64(&m.timeoutErrors),
		BalanceErrors:    atomic.LoadUint64(&m.balanceErrors),
		ValidationErrors: atomic.LoadUint64(&m.validationErrors),
		ActiveGoroutines: atomic.LoadUint64(&m.activeGoroutines),
		MemoryAllocMB:    atomic.LoadUint64(&m.memoryAllocMB),
		Uptime:           time.Since(m.startTime),
		TimeSinceReset:   time.Since(m.lastReset),
	}
}

// Reset resets all metrics
func (m *Metrics) Reset() {
	atomic.StoreUint64(&m.ordersSubmitted, 0)
	atomic.StoreUint64(&m.ordersExecuted, 0)
	atomic.StoreUint64(&m.ordersSucceeded, 0)
	atomic.StoreUint64(&m.ordersFailed, 0)
	atomic.StoreUint64(&m.ordersCancelled, 0)
	atomic.StoreUint64(&m.totalLatencyUs, 0)
	atomic.StoreUint64(&m.minLatencyUs, ^uint64(0))
	atomic.StoreUint64(&m.maxLatencyUs, 0)
	atomic.StoreUint64(&m.lastSecondCount, 0)
	atomic.StoreUint64(&m.lastMinuteCount, 0)
	atomic.StoreUint64(&m.networkErrors, 0)
	atomic.StoreUint64(&m.timeoutErrors, 0)
	atomic.StoreUint64(&m.balanceErrors, 0)
	atomic.StoreUint64(&m.validationErrors, 0)
	m.lastReset = time.Now()
}

// MetricsSnapshot represents a point-in-time snapshot of metrics
type MetricsSnapshot struct {
	OrdersSubmitted  uint64
	OrdersExecuted   uint64
	OrdersSucceeded  uint64
	OrdersFailed     uint64
	OrdersCancelled  uint64
	TotalLatencyUs   uint64
	MinLatencyUs     uint64
	MaxLatencyUs     uint64
	LastSecondCount  uint64
	LastMinuteCount  uint64
	NetworkErrors    uint64
	TimeoutErrors    uint64
	BalanceErrors    uint64
	ValidationErrors uint64
	ActiveGoroutines uint64
	MemoryAllocMB    uint64
	Uptime           time.Duration
	TimeSinceReset   time.Duration
}

// AvgLatencyUs calculates average latency in microseconds
func (s MetricsSnapshot) AvgLatencyUs() uint64 {
	if s.OrdersExecuted == 0 {
		return 0
	}
	return s.TotalLatencyUs / s.OrdersExecuted
}

// SuccessRate calculates success rate as percentage
func (s MetricsSnapshot) SuccessRate() float64 {
	if s.OrdersExecuted == 0 {
		return 0
	}
	return float64(s.OrdersSucceeded) / float64(s.OrdersExecuted) * 100
}

// ThroughputPerSecond calculates throughput per second
func (s MetricsSnapshot) ThroughputPerSecond() float64 {
	if s.Uptime.Seconds() == 0 {
		return 0
	}
	return float64(s.OrdersExecuted) / s.Uptime.Seconds()
}

// ErrorRate calculates error rate as percentage
func (s MetricsSnapshot) ErrorRate() float64 {
	if s.OrdersExecuted == 0 {
		return 0
	}
	return float64(s.OrdersFailed) / float64(s.OrdersExecuted) * 100
}

// TotalErrors returns total number of errors
func (s MetricsSnapshot) TotalErrors() uint64 {
	return s.NetworkErrors + s.TimeoutErrors + s.BalanceErrors + s.ValidationErrors
}
