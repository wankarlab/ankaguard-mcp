package hft

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// PrometheusMetrics holds Prometheus metrics for HFT system
type PrometheusMetrics struct {
	// Order metrics
	ordersSubmitted prometheus.Counter
	ordersExecuted  prometheus.Counter
	ordersSucceeded prometheus.Counter
	ordersFailed    prometheus.Counter

	// Latency metrics
	orderLatency prometheus.Histogram

	// Throughput metrics
	orderThroughput prometheus.Gauge

	// Circuit breaker metrics
	circuitBreakerState *prometheus.GaugeVec
	circuitBreakerTrips prometheus.Counter

	// Worker pool metrics
	workerPoolQueueSize     prometheus.Gauge
	workerPoolActiveWorkers prometheus.Gauge
	workerPoolTasksTotal    prometheus.Counter

	// Error metrics
	errorsByType *prometheus.CounterVec

	// System metrics
	activeGoroutines prometheus.Gauge
	memoryUsageMB    prometheus.Gauge
}

// NewPrometheusMetrics creates a new Prometheus metrics instance
func NewPrometheusMetrics(namespace string) *PrometheusMetrics {
	return &PrometheusMetrics{
		ordersSubmitted: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "orders_submitted_total",
			Help:      "Total number of orders submitted",
		}),

		ordersExecuted: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "orders_executed_total",
			Help:      "Total number of orders executed",
		}),

		ordersSucceeded: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "orders_succeeded_total",
			Help:      "Total number of orders that succeeded",
		}),

		ordersFailed: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "orders_failed_total",
			Help:      "Total number of orders that failed",
		}),

		orderLatency: promauto.NewHistogram(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "order_execution_latency_microseconds",
			Help:      "Order execution latency in microseconds",
			Buckets:   []float64{10, 50, 100, 500, 1000, 5000, 10000, 50000, 100000},
		}),

		orderThroughput: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "order_throughput_per_second",
			Help:      "Current order throughput per second",
		}),

		circuitBreakerState: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "circuit_breaker_state",
			Help:      "Circuit breaker state (0=closed, 1=open, 2=half-open)",
		}, []string{"name"}),

		circuitBreakerTrips: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "circuit_breaker_trips_total",
			Help:      "Total number of circuit breaker trips",
		}),

		workerPoolQueueSize: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "worker_pool_queue_size",
			Help:      "Current worker pool queue size",
		}),

		workerPoolActiveWorkers: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "worker_pool_active_workers",
			Help:      "Number of active workers",
		}),

		workerPoolTasksTotal: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "worker_pool_tasks_total",
			Help:      "Total number of tasks processed by worker pool",
		}),

		errorsByType: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "errors_total",
			Help:      "Total number of errors by type",
		}, []string{"type"}),

		activeGoroutines: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "active_goroutines",
			Help:      "Number of active goroutines",
		}),

		memoryUsageMB: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "memory_usage_mb",
			Help:      "Memory usage in megabytes",
		}),
	}
}

// RecordOrderSubmitted records an order submission
func (pm *PrometheusMetrics) RecordOrderSubmitted() {
	pm.ordersSubmitted.Inc()
}

// RecordOrderExecuted records an order execution
func (pm *PrometheusMetrics) RecordOrderExecuted(latencyUs uint64, success bool) {
	pm.ordersExecuted.Inc()
	pm.orderLatency.Observe(float64(latencyUs))

	if success {
		pm.ordersSucceeded.Inc()
	} else {
		pm.ordersFailed.Inc()
	}
}

// RecordError records an error by type
func (pm *PrometheusMetrics) RecordError(errorType string) {
	pm.errorsByType.WithLabelValues(errorType).Inc()
}

// UpdateCircuitBreakerState updates circuit breaker state
func (pm *PrometheusMetrics) UpdateCircuitBreakerState(name string, state CircuitState) {
	pm.circuitBreakerState.WithLabelValues(name).Set(float64(state))
}

// RecordCircuitBreakerTrip records a circuit breaker trip
func (pm *PrometheusMetrics) RecordCircuitBreakerTrip() {
	pm.circuitBreakerTrips.Inc()
}

// UpdateWorkerPoolMetrics updates worker pool metrics
func (pm *PrometheusMetrics) UpdateWorkerPoolMetrics(stats WorkerPoolStats) {
	pm.workerPoolQueueSize.Set(float64(stats.QueueSize))
	pm.workerPoolActiveWorkers.Set(float64(stats.Workers))
	pm.workerPoolTasksTotal.Add(float64(stats.TasksCompleted))
}

// UpdateThroughput updates throughput metric
func (pm *PrometheusMetrics) UpdateThroughput(ordersPerSecond float64) {
	pm.orderThroughput.Set(ordersPerSecond)
}

// UpdateSystemMetrics updates system-level metrics
func (pm *PrometheusMetrics) UpdateSystemMetrics(goroutines uint64, memoryMB uint64) {
	pm.activeGoroutines.Set(float64(goroutines))
	pm.memoryUsageMB.Set(float64(memoryMB))
}

// UpdateFromSnapshot updates all metrics from a metrics snapshot
func (pm *PrometheusMetrics) UpdateFromSnapshot(snapshot MetricsSnapshot) {
	// Throughput
	pm.UpdateThroughput(snapshot.ThroughputPerSecond())

	// System metrics
	pm.UpdateSystemMetrics(snapshot.ActiveGoroutines, snapshot.MemoryAllocMB)
}
