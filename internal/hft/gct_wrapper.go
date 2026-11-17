package hft

import (
	"context"
	"fmt"
	"time"

	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/sirupsen/logrus"
)

// GCTClientInterface defines the interface for GCT client operations
// This avoids import cycles by using an interface
type GCTClientInterface interface {
	SubmitOrder(ctx context.Context, req interface{}) (interface{}, error)
	SubmitFuturesOrder(ctx context.Context, req interface{}) (interface{}, error)
}

// HFTTradingSystem wraps the GCT client with HFT optimizations
type HFTTradingSystem struct {
	gctClient      GCTClientInterface
	orderPool      *OrderPool
	workerPool     *WorkerPool
	circuitBreaker *CircuitBreaker
	metrics        *Metrics
	promMetrics    *PrometheusMetrics
	reconciliation *OrderReconciliationService
	logger         *logrus.Logger

	// Configuration
	config HFTConfig

	// State
	running bool
}

// HFTConfig holds configuration for the HFT system
type HFTConfig struct {
	Workers              int
	QueueSize            int
	EnableMetrics        bool
	EnableCircuitBreaker bool
	EnableRetry          bool
	MetricsNamespace     string
}

// DefaultHFTConfig returns default configuration
func DefaultHFTConfig() HFTConfig {
	return HFTConfig{
		Workers:              100,
		QueueSize:            10000,
		EnableMetrics:        true,
		EnableCircuitBreaker: true, // Circuit breaker enabled by default — real-money order path
		EnableRetry:          true,
		MetricsNamespace:     "hft",
	}
}

// NewHFTTradingSystem creates a new HFT trading system.
//
// checkFunc (QUANT-05) is used by the order reconciliation service to query
// the exchange for an order's real status after a network/timeout error on
// submission — a real fill after a client-side timeout would otherwise never
// be detected. Pass nil to disable reconciliation (matches prior behaviour);
// callers with a real exchange connection should build one from their
// concrete gct client (see gctclient.NewClient).
func NewHFTTradingSystem(gctClient GCTClientInterface, logger *logrus.Logger, config HFTConfig, checkFunc OrderCheckFunc) (*HFTTradingSystem, error) {
	if gctClient == nil {
		return nil, fmt.Errorf("gct client is nil")
	}
	if logger == nil {
		return nil, fmt.Errorf("logger is nil")
	}

	system := &HFTTradingSystem{
		gctClient: gctClient,
		orderPool: NewOrderPool(),
		metrics:   NewMetrics(),
		logger:    logger,
		config:    config,
		running:   false,
	}

	// Create worker pool
	system.workerPool = NewWorkerPool(WorkerPoolConfig{
		Workers:      config.Workers,
		QueueSize:    config.QueueSize,
		ResultBuffer: 1000,
	})

	// Create circuit breaker if enabled
	if config.EnableCircuitBreaker {
		cbConfig := DefaultCircuitBreakerConfig("hft_orders")
		cbConfig.OnStateChange = func(name string, from, to CircuitState) {
			logger.WithFields(logrus.Fields{
				"circuit": name,
				"from":    from.String(),
				"to":      to.String(),
			}).Warn("Circuit breaker state changed")

			if system.promMetrics != nil {
				system.promMetrics.UpdateCircuitBreakerState(name, to)
				if to == StateOpen {
					system.promMetrics.RecordCircuitBreakerTrip()
				}
			}
		}
		system.circuitBreaker = NewCircuitBreaker(cbConfig)
	}

	// Create Prometheus metrics if enabled
	if config.EnableMetrics {
		system.promMetrics = NewPrometheusMetrics(config.MetricsNamespace)
	}

	// Create order reconciliation service, wired with the real checkFunc
	// (QUANT-05) instead of nil so an uncertain order after a timeout can
	// actually be verified against the exchange.
	system.reconciliation = NewOrderReconciliationService(logger, checkFunc)

	return system, nil
}

// Start starts the HFT system
func (s *HFTTradingSystem) Start() error {
	if s.running {
		return fmt.Errorf("HFT system already running")
	}

	// Start worker pool
	if err := s.workerPool.Start(); err != nil {
		return fmt.Errorf("failed to start worker pool: %w", err)
	}

	// Start metrics collector
	if s.config.EnableMetrics {
		go s.metricsCollector()
	}

	// Start order reconciliation service
	if s.reconciliation != nil {
		s.reconciliation.Start()
	}

	s.running = true
	s.logger.Info("✓ HFT Trading System started")

	return nil
}

// Stop stops the HFT system gracefully
func (s *HFTTradingSystem) Stop() error {
	if !s.running {
		return fmt.Errorf("HFT system not running")
	}

	// Stop worker pool
	if err := s.workerPool.Stop(); err != nil {
		return fmt.Errorf("failed to stop worker pool: %w", err)
	}

	// Stop reconciliation service
	if s.reconciliation != nil {
		s.reconciliation.Stop()
	}

	s.running = false
	s.logger.Info("✓ HFT Trading System stopped")

	return nil
}

// reconciliationRecordFor builds an OrderRecord from req if it's a
// *gct.SubmitOrderRequest with a non-empty ClientID (QUANT-05: reconciliation
// is keyed by ClientOrderID, so a request without one can't be tracked).
func reconciliationRecordFor(req interface{}) (*OrderRecord, bool) {
	sReq, ok := req.(*gct.SubmitOrderRequest)
	if !ok || sReq.ClientID == "" {
		return nil, false
	}
	return &OrderRecord{
		ClientOrderID: sReq.ClientID,
		Exchange:      sReq.Exchange,
		Symbol:        sReq.Pair,
		Side:          sReq.Side,
		Amount:        sReq.Amount,
		Price:         sReq.Price,
	}, true
}

// orderIDOf best-efforts extracts the exchange order ID from a submission
// result, for MarkConfirmed.
func orderIDOf(result interface{}) string {
	if o, ok := result.(*gct.Order); ok && o != nil {
		return o.ID
	}
	return ""
}

// isNetworkOrTimeoutError classifies an error the same way the metrics
// classification below does, for QUANT-05's MarkUncertain wiring.
func isNetworkOrTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	return contains(err.Error(), "network") || contains(err.Error(), "connection") || contains(err.Error(), "timeout")
}

// SubmitOrder submits an order with HFT optimizations
func (s *HFTTradingSystem) SubmitOrder(ctx context.Context, req interface{}) (interface{}, error) {
	if !s.running {
		return nil, fmt.Errorf("HFT system not running")
	}

	// Record submission
	s.metrics.RecordOrderSubmitted()
	if s.promMetrics != nil {
		s.promMetrics.RecordOrderSubmitted()
	}

	// QUANT-05: track this order for reconciliation BEFORE submitting, so a
	// network/timeout error below can be marked uncertain and independently
	// verified against the exchange instead of just being logged as a metric.
	var reconcileRecord *OrderRecord
	if s.reconciliation != nil {
		if rec, ok := reconciliationRecordFor(req); ok {
			reconcileRecord = rec
			s.reconciliation.RecordOrder(rec)
		}
	}

	start := time.Now()

	// Execute with circuit breaker if enabled
	var result interface{}
	var err error

	if s.config.EnableCircuitBreaker && s.circuitBreaker != nil {
		err = s.circuitBreaker.ExecuteContext(ctx, func(ctx context.Context) error {
			result, err = s.gctClient.SubmitOrder(ctx, req)
			return err
		})
	} else {
		result, err = s.gctClient.SubmitOrder(ctx, req)
	}

	// Record execution
	latencyUs := uint64(time.Since(start).Microseconds())
	success := err == nil

	s.metrics.RecordOrderExecuted(latencyUs, success)
	if s.promMetrics != nil {
		s.promMetrics.RecordOrderExecuted(latencyUs, success)
	}

	if err != nil {
		// Classify error
		if contains(err.Error(), "network") || contains(err.Error(), "connection") {
			s.metrics.RecordNetworkError()
			if s.promMetrics != nil {
				s.promMetrics.RecordError("network")
			}
		} else if contains(err.Error(), "timeout") {
			s.metrics.RecordTimeoutError()
			if s.promMetrics != nil {
				s.promMetrics.RecordError("timeout")
			}
		} else if contains(err.Error(), "balance") {
			s.metrics.RecordBalanceError()
			if s.promMetrics != nil {
				s.promMetrics.RecordError("balance")
			}
		}

		if reconcileRecord != nil {
			if isNetworkOrTimeoutError(err) {
				s.reconciliation.MarkUncertain(reconcileRecord.ClientOrderID, err)
			} else {
				s.reconciliation.MarkFailed(reconcileRecord.ClientOrderID, err)
			}
		}

		return nil, err
	}

	if reconcileRecord != nil {
		s.reconciliation.MarkConfirmed(reconcileRecord.ClientOrderID, orderIDOf(result))
	}

	return result, nil
}

// SubmitFuturesOrder submits a futures order with HFT optimizations
func (s *HFTTradingSystem) SubmitFuturesOrder(ctx context.Context, req interface{}) (interface{}, error) {
	if !s.running {
		return nil, fmt.Errorf("HFT system not running")
	}

	// Record submission
	s.metrics.RecordOrderSubmitted()
	if s.promMetrics != nil {
		s.promMetrics.RecordOrderSubmitted()
	}

	// QUANT-05: see SubmitOrder above — track for reconciliation before submit.
	var reconcileRecord *OrderRecord
	if s.reconciliation != nil {
		if rec, ok := reconciliationRecordFor(req); ok {
			reconcileRecord = rec
			s.reconciliation.RecordOrder(rec)
		}
	}

	start := time.Now()

	// Execute with circuit breaker if enabled
	var result interface{}
	var err error

	if s.config.EnableCircuitBreaker && s.circuitBreaker != nil {
		err = s.circuitBreaker.ExecuteContext(ctx, func(ctx context.Context) error {
			result, err = s.gctClient.SubmitFuturesOrder(ctx, req)
			return err
		})
	} else {
		result, err = s.gctClient.SubmitFuturesOrder(ctx, req)
	}

	// Record execution
	latencyUs := uint64(time.Since(start).Microseconds())
	success := err == nil

	s.metrics.RecordOrderExecuted(latencyUs, success)
	if s.promMetrics != nil {
		s.promMetrics.RecordOrderExecuted(latencyUs, success)
	}

	if err != nil {
		// Classify error
		if contains(err.Error(), "network") || contains(err.Error(), "connection") {
			s.metrics.RecordNetworkError()
			if s.promMetrics != nil {
				s.promMetrics.RecordError("network")
			}
		} else if contains(err.Error(), "timeout") {
			s.metrics.RecordTimeoutError()
			if s.promMetrics != nil {
				s.promMetrics.RecordError("timeout")
			}
		} else if contains(err.Error(), "balance") {
			s.metrics.RecordBalanceError()
			if s.promMetrics != nil {
				s.promMetrics.RecordError("balance")
			}
		}

		if reconcileRecord != nil {
			if isNetworkOrTimeoutError(err) {
				s.reconciliation.MarkUncertain(reconcileRecord.ClientOrderID, err)
			} else {
				s.reconciliation.MarkFailed(reconcileRecord.ClientOrderID, err)
			}
		}

		return nil, err
	}

	if reconcileRecord != nil {
		s.reconciliation.MarkConfirmed(reconcileRecord.ClientOrderID, orderIDOf(result))
	}

	return result, nil
}

// GetMetrics returns current metrics snapshot
func (s *HFTTradingSystem) GetMetrics() MetricsSnapshot {
	return s.metrics.Snapshot()
}

// GetWorkerPoolStats returns worker pool statistics
func (s *HFTTradingSystem) GetWorkerPoolStats() WorkerPoolStats {
	return s.workerPool.Stats()
}

// GetCircuitBreakerStats returns circuit breaker statistics
func (s *HFTTradingSystem) GetCircuitBreakerStats() *CircuitBreakerStats {
	if s.circuitBreaker == nil {
		return nil
	}
	stats := s.circuitBreaker.Stats()
	return &stats
}

// metricsCollector periodically collects and updates metrics
func (s *HFTTradingSystem) metricsCollector() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if !s.running {
				return
			}

			// Update Prometheus metrics
			if s.promMetrics != nil {
				snapshot := s.metrics.Snapshot()
				s.promMetrics.UpdateFromSnapshot(snapshot)

				// Update worker pool metrics
				poolStats := s.workerPool.Stats()
				s.promMetrics.UpdateWorkerPoolMetrics(poolStats)
			}
		}
	}
}

// IsRunning returns true if the HFT system is running
func (s *HFTTradingSystem) IsRunning() bool {
	return s.running
}

// GetCircuitBreakerState returns the current circuit breaker state as a string
func (s *HFTTradingSystem) GetCircuitBreakerState() string {
	if s.circuitBreaker == nil {
		return "disabled"
	}
	return s.circuitBreaker.State().String()
}

// ResetCircuitBreaker manually resets the circuit breaker
func (s *HFTTradingSystem) ResetCircuitBreaker() {
	if s.circuitBreaker != nil {
		s.circuitBreaker.Reset()
	}
}

// GetReconciliationStats returns order reconciliation statistics
func (s *HFTTradingSystem) GetReconciliationStats() map[string]interface{} {
	if s.reconciliation != nil {
		return s.reconciliation.GetStats()
	}
	return map[string]interface{}{"enabled": false}
}

// GetOrderState returns the state of a specific order
func (s *HFTTradingSystem) GetOrderState(clientOrderID string) (*OrderRecord, error) {
	if s.reconciliation != nil {
		return s.reconciliation.GetOrderState(clientOrderID)
	}
	return nil, fmt.Errorf("reconciliation service not enabled")
}
