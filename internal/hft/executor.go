package hft

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/sirupsen/logrus"
)

// OptimizedExecutor is a high-performance order executor
type OptimizedExecutor struct {
	gctClient  gct.GCTClient
	orderPool  *OrderPool
	workerPool *WorkerPool
	metrics    *Metrics
	logger     *logrus.Logger

	// Configuration
	config ExecutorConfig

	// State
	running bool
	mu      sync.RWMutex
}

// ExecutorConfig holds configuration for the executor
type ExecutorConfig struct {
	Workers         int
	QueueSize       int
	EnableMetrics   bool
	MetricsInterval time.Duration
	MaxRetries      int
	RetryDelay      time.Duration
}

// DefaultExecutorConfig returns default configuration
func DefaultExecutorConfig() ExecutorConfig {
	return ExecutorConfig{
		Workers:         100,
		QueueSize:       10000,
		EnableMetrics:   true,
		MetricsInterval: time.Second,
		MaxRetries:      3,
		RetryDelay:      time.Millisecond * 100,
	}
}

// NewOptimizedExecutor creates a new optimized executor
func NewOptimizedExecutor(gctClient gct.GCTClient, logger *logrus.Logger, config ExecutorConfig) (*OptimizedExecutor, error) {
	if gctClient == nil {
		return nil, fmt.Errorf("gct client is nil")
	}
	if logger == nil {
		return nil, fmt.Errorf("logger is nil")
	}

	executor := &OptimizedExecutor{
		gctClient: gctClient,
		orderPool: NewOrderPool(),
		workerPool: NewWorkerPool(WorkerPoolConfig{
			Workers:      config.Workers,
			QueueSize:    config.QueueSize,
			ResultBuffer: 1000,
		}),
		metrics: NewMetrics(),
		logger:  logger,
		config:  config,
		running: false,
	}

	return executor, nil
}

// Start starts the executor
func (e *OptimizedExecutor) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.running {
		return fmt.Errorf("executor already running")
	}

	// Start worker pool
	if err := e.workerPool.Start(); err != nil {
		return fmt.Errorf("failed to start worker pool: %w", err)
	}

	// Start metrics collector if enabled
	if e.config.EnableMetrics {
		go e.metricsCollector()
	}

	// Start result processor
	go e.resultProcessor()

	e.running = true
	e.logger.Info("✓ Optimized executor started")

	return nil
}

// Stop stops the executor gracefully
func (e *OptimizedExecutor) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.running {
		return fmt.Errorf("executor not running")
	}

	// Stop worker pool
	if err := e.workerPool.Stop(); err != nil {
		return fmt.Errorf("failed to stop worker pool: %w", err)
	}

	e.running = false
	e.logger.Info("✓ Optimized executor stopped")

	return nil
}

// ExecuteOrder executes an order using the optimized pipeline
func (e *OptimizedExecutor) ExecuteOrder(ctx context.Context, req *gct.SubmitOrderRequest) (*gct.Order, error) {
	if !e.running {
		return nil, fmt.Errorf("executor not running")
	}

	// Acquire order from pool
	order := e.orderPool.Acquire()
	defer e.orderPool.Release(order)

	// Populate order
	order.Exchange = req.Exchange
	order.Symbol = req.Pair
	order.Side = req.Side
	order.Type = req.Type
	order.Amount = req.Amount
	order.Price = req.Price
	order.Leverage = req.Leverage
	order.ClientOrderID = req.ClientID
	order.SubmittedAt = time.Now()

	// Record submission
	e.metrics.RecordOrderSubmitted()

	// Create task
	task := NewOrderTask(
		order.ClientOrderID,
		order,
		func(ctx context.Context, o *PooledOrder) error {
			return e.executeOrderInternal(ctx, req, o)
		},
	)

	// Submit to worker pool
	resultChan, err := e.workerPool.SubmitAsync(task)
	if err != nil {
		e.metrics.RecordValidationError()
		return nil, fmt.Errorf("failed to submit task: %w", err)
	}

	// Wait for result
	select {
	case result := <-resultChan:
		if result.Error != nil {
			return nil, result.Error
		}

		// Build response
		return &gct.Order{
			ID:        order.ID,
			Exchange:  order.Exchange,
			Pair:      order.Symbol,
			Side:      order.Side,
			Type:      order.Type,
			Amount:    order.Amount,
			Price:     order.Price,
			Status:    order.Status,
			CreatedAt: order.CreatedAt,
			UpdatedAt: order.UpdatedAt,
		}, nil

	case <-ctx.Done():
		e.metrics.RecordTimeoutError()
		return nil, fmt.Errorf("execution timeout: %w", ctx.Err())
	}
}

// executeOrderInternal executes the order using GCT client
func (e *OptimizedExecutor) executeOrderInternal(ctx context.Context, req *gct.SubmitOrderRequest, order *PooledOrder) error {
	start := time.Now()

	// Execute order
	result, err := e.gctClient.SubmitOrder(ctx, req)

	// Calculate latency
	order.ExecutedAt = time.Now()
	order.CalculateLatency()

	// Record metrics
	e.metrics.RecordOrderExecuted(uint64(order.LatencyUs), err == nil)

	if err != nil {
		// Classify error
		errStr := err.Error()
		if contains(errStr, "network") || contains(errStr, "connection") {
			e.metrics.RecordNetworkError()
		} else if contains(errStr, "timeout") {
			e.metrics.RecordTimeoutError()
		} else if contains(errStr, "balance") || contains(errStr, "insufficient") {
			e.metrics.RecordBalanceError()
		}

		e.logger.WithFields(logrus.Fields{
			"order_id":   order.ClientOrderID,
			"symbol":     order.Symbol,
			"latency_us": order.LatencyUs,
			"error":      err.Error(),
		}).Error("Order execution failed")

		return err
	}

	// Update order with result
	order.ID = result.ID
	order.Status = result.Status
	order.CreatedAt = result.CreatedAt
	order.UpdatedAt = result.UpdatedAt

	e.logger.WithFields(logrus.Fields{
		"order_id":   result.ID,
		"symbol":     order.Symbol,
		"side":       order.Side,
		"amount":     order.Amount,
		"latency_us": order.LatencyUs,
		"duration":   time.Since(start),
	}).Info("✓ Order executed successfully")

	return nil
}

// resultProcessor processes results from worker pool
func (e *OptimizedExecutor) resultProcessor() {
	for result := range e.workerPool.Results() {
		// Log result
		if result.Success {
			e.logger.WithFields(logrus.Fields{
				"task_id":  result.TaskID,
				"duration": result.Duration,
			}).Debug("Task completed successfully")
		} else {
			e.logger.WithFields(logrus.Fields{
				"task_id":  result.TaskID,
				"duration": result.Duration,
				"error":    result.Error,
			}).Warn("Task failed")
		}
	}
}

// metricsCollector periodically collects and logs metrics
func (e *OptimizedExecutor) metricsCollector() {
	ticker := time.NewTicker(e.config.MetricsInterval)
	defer ticker.Stop()

	secondTicker := time.NewTicker(time.Second)
	defer secondTicker.Stop()

	minuteTicker := time.NewTicker(time.Minute)
	defer minuteTicker.Stop()

	for {
		select {
		case <-ticker.C:
			snapshot := e.metrics.Snapshot()
			poolStats := e.workerPool.Stats()

			e.logger.WithFields(logrus.Fields{
				"orders_submitted": snapshot.OrdersSubmitted,
				"orders_executed":  snapshot.OrdersExecuted,
				"orders_succeeded": snapshot.OrdersSucceeded,
				"orders_failed":    snapshot.OrdersFailed,
				"success_rate":     fmt.Sprintf("%.2f%%", snapshot.SuccessRate()),
				"avg_latency_us":   snapshot.AvgLatencyUs(),
				"min_latency_us":   snapshot.MinLatencyUs,
				"max_latency_us":   snapshot.MaxLatencyUs,
				"throughput_per_s": fmt.Sprintf("%.2f", snapshot.ThroughputPerSecond()),
				"queue_size":       poolStats.QueueSize,
				"queue_capacity":   poolStats.QueueCapacity,
				"tasks_pending":    poolStats.TasksPending,
			}).Info("HFT Metrics")

		case <-secondTicker.C:
			e.metrics.ResetPerSecondCounters()

		case <-minuteTicker.C:
			e.metrics.ResetPerMinuteCounters()
		}

		if !e.running {
			return
		}
	}
}

// GetMetrics returns current metrics snapshot
func (e *OptimizedExecutor) GetMetrics() MetricsSnapshot {
	return e.metrics.Snapshot()
}

// GetWorkerPoolStats returns worker pool statistics
func (e *OptimizedExecutor) GetWorkerPoolStats() WorkerPoolStats {
	return e.workerPool.Stats()
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) &&
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
			findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
