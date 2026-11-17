package hft

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// OrderState represents the state of an order
type OrderState string

const (
	OrderStatePending     OrderState = "pending"     // Order submitted, awaiting confirmation
	OrderStateConfirmed   OrderState = "confirmed"   // Order confirmed by exchange
	OrderStateFailed      OrderState = "failed"      // Order definitively failed
	OrderStateUncertain   OrderState = "uncertain"   // Network error, state unknown
	OrderStateReconciling OrderState = "reconciling" // Checking actual state
	OrderStateReconciled  OrderState = "reconciled"  // State verified after uncertainty
)

// OrderRecord tracks an order through its lifecycle
type OrderRecord struct {
	ClientOrderID  string
	ExchangeID     string
	Exchange       string
	Symbol         string
	Side           string
	Amount         float64
	Price          float64
	State          OrderState
	SubmittedAt    time.Time
	ConfirmedAt    *time.Time
	ReconciledAt   *time.Time
	Attempts       int
	LastError      string
	ActuallyFilled bool // True if order was actually executed despite error
}

// OrderReconciliationService handles uncertain order states
type OrderReconciliationService struct {
	orders    map[string]*OrderRecord
	mu        sync.RWMutex
	logger    *logrus.Logger
	checkFunc OrderCheckFunc // Function to check order status with exchange

	// Configuration
	reconciliationInterval time.Duration
	maxReconciliationTime  time.Duration

	// Channels
	uncertainOrders chan *OrderRecord
	stopChan        chan struct{}

	// Metrics
	totalUncertain      int64
	totalReconciled     int64
	totalActuallyFilled int64
}

// OrderCheckFunc is a function that checks order status with the exchange
type OrderCheckFunc func(ctx context.Context, exchange, orderID string) (*OrderStatus, error)

// OrderStatus represents the actual status from exchange
type OrderStatus struct {
	OrderID    string
	State      string // "filled", "cancelled", "active", "not_found"
	FilledSize float64
	FilledAt   *time.Time
}

// NewOrderReconciliationService creates a new reconciliation service
func NewOrderReconciliationService(logger *logrus.Logger, checkFunc OrderCheckFunc) *OrderReconciliationService {
	return &OrderReconciliationService{
		orders:                 make(map[string]*OrderRecord),
		logger:                 logger,
		checkFunc:              checkFunc,
		reconciliationInterval: 2 * time.Second,
		maxReconciliationTime:  30 * time.Second,
		uncertainOrders:        make(chan *OrderRecord, 1000),
		stopChan:               make(chan struct{}),
	}
}

// Start starts the reconciliation service
func (s *OrderReconciliationService) Start() {
	go s.reconciliationLoop()
	s.logger.Info("✓ Order Reconciliation Service started")
}

// Stop stops the reconciliation service
func (s *OrderReconciliationService) Stop() {
	close(s.stopChan)
	s.logger.Info("✓ Order Reconciliation Service stopped")
}

// RecordOrder records a new order submission
func (s *OrderReconciliationService) RecordOrder(record *OrderRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()

	record.SubmittedAt = time.Now()
	record.State = OrderStatePending
	s.orders[record.ClientOrderID] = record

	s.logger.WithFields(logrus.Fields{
		"client_order_id": record.ClientOrderID,
		"exchange":        record.Exchange,
		"symbol":          record.Symbol,
		"side":            record.Side,
		"amount":          record.Amount,
	}).Info("📝 Order recorded for tracking")
}

// MarkConfirmed marks an order as confirmed
func (s *OrderReconciliationService) MarkConfirmed(clientOrderID, exchangeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if record, exists := s.orders[clientOrderID]; exists {
		now := time.Now()
		record.State = OrderStateConfirmed
		record.ExchangeID = exchangeID
		record.ConfirmedAt = &now

		s.logger.WithFields(logrus.Fields{
			"client_order_id": clientOrderID,
			"exchange_id":     exchangeID,
			"latency_ms":      now.Sub(record.SubmittedAt).Milliseconds(),
		}).Info("✓ Order confirmed")
	}
}

// MarkUncertain marks an order as uncertain (network error)
func (s *OrderReconciliationService) MarkUncertain(clientOrderID string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if record, exists := s.orders[clientOrderID]; exists {
		record.State = OrderStateUncertain
		record.LastError = err.Error()
		s.totalUncertain++

		s.logger.WithFields(logrus.Fields{
			"client_order_id": clientOrderID,
			"error":           err.Error(),
			"attempts":        record.Attempts,
		}).Warn("⚠️  Order state UNCERTAIN - initiating reconciliation")

		// Send to reconciliation queue
		select {
		case s.uncertainOrders <- record:
		default:
			s.logger.Error("Reconciliation queue full!")
		}
	}
}

// MarkFailed marks an order as definitively failed
func (s *OrderReconciliationService) MarkFailed(clientOrderID string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if record, exists := s.orders[clientOrderID]; exists {
		record.State = OrderStateFailed
		record.LastError = err.Error()

		s.logger.WithFields(logrus.Fields{
			"client_order_id": clientOrderID,
			"error":           err.Error(),
		}).Error("✗ Order definitively failed")
	}
}

// reconciliationLoop continuously reconciles uncertain orders
func (s *OrderReconciliationService) reconciliationLoop() {
	ticker := time.NewTicker(s.reconciliationInterval)
	defer ticker.Stop()

	for {
		select {
		case record := <-s.uncertainOrders:
			s.reconcileOrder(record)

		case <-ticker.C:
			// Periodic check of all uncertain orders
			s.checkUncertainOrders()

		case <-s.stopChan:
			return
		}
	}
}

// reconcileOrder attempts to determine the actual state of an order
func (s *OrderReconciliationService) reconcileOrder(record *OrderRecord) {
	s.mu.Lock()
	record.State = OrderStateReconciling
	s.mu.Unlock()

	s.logger.WithFields(logrus.Fields{
		"client_order_id": record.ClientOrderID,
		"exchange":        record.Exchange,
		"symbol":          record.Symbol,
	}).Info("🔍 Reconciling order state...")

	// QUANT-05: guard against a nil checkFunc instead of panicking. This can
	// happen if the service was constructed without a real check function
	// (e.g. in a test harness, or a caller that hasn't wired one). Mark the
	// order failed immediately rather than crashing the reconciliation loop
	// (which would silently stop reconciling every other uncertain order too).
	if s.checkFunc == nil {
		s.mu.Lock()
		record.State = OrderStateFailed
		record.LastError = "reconciliation: no checkFunc configured, cannot verify order state"
		s.mu.Unlock()
		s.logger.WithFields(logrus.Fields{
			"client_order_id": record.ClientOrderID,
		}).Error("❌ Reconciliation checkFunc is nil — cannot verify order, marking as failed")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Check with exchange
	status, err := s.checkFunc(ctx, record.Exchange, record.ClientOrderID)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err != nil {
		// Still can't determine state
		if time.Since(record.SubmittedAt) > s.maxReconciliationTime {
			// Give up after max time
			record.State = OrderStateFailed
			s.logger.WithFields(logrus.Fields{
				"client_order_id": record.ClientOrderID,
				"elapsed":         time.Since(record.SubmittedAt),
			}).Error("❌ Reconciliation timeout - marking as failed")
		} else {
			// Try again later
			go func() {
				time.Sleep(s.reconciliationInterval)
				select {
				case s.uncertainOrders <- record:
				case <-s.stopChan:
				}
			}()
		}
		return
	}

	// Successfully determined state
	now := time.Now()
	record.ReconciledAt = &now
	record.State = OrderStateReconciled
	s.totalReconciled++

	if status.State == "filled" || status.State == "active" {
		// Order was actually executed!
		record.ActuallyFilled = true
		record.ExchangeID = status.OrderID
		s.totalActuallyFilled++

		s.logger.WithFields(logrus.Fields{
			"client_order_id": record.ClientOrderID,
			"exchange_id":     status.OrderID,
			"filled_size":     status.FilledSize,
			"reconcile_time":  now.Sub(record.SubmittedAt).Milliseconds(),
		}).Warn("⚠️  Order WAS EXECUTED despite network error!")

	} else {
		// Order was not executed
		s.logger.WithFields(logrus.Fields{
			"client_order_id": record.ClientOrderID,
			"status":          status.State,
		}).Info("✓ Order was NOT executed - safe to retry")
	}
}

// checkUncertainOrders periodically checks all uncertain orders
func (s *OrderReconciliationService) checkUncertainOrders() {
	s.mu.RLock()
	var uncertainOrders []*OrderRecord
	for _, record := range s.orders {
		if record.State == OrderStateUncertain || record.State == OrderStateReconciling {
			uncertainOrders = append(uncertainOrders, record)
		}
	}
	s.mu.RUnlock()

	for _, record := range uncertainOrders {
		if time.Since(record.SubmittedAt) < s.maxReconciliationTime {
			select {
			case s.uncertainOrders <- record:
			default:
			}
		}
	}
}

// GetOrderState returns the current state of an order
func (s *OrderReconciliationService) GetOrderState(clientOrderID string) (*OrderRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	record, exists := s.orders[clientOrderID]
	if !exists {
		return nil, fmt.Errorf("order not found: %s", clientOrderID)
	}

	return record, nil
}

// GetStats returns reconciliation statistics
func (s *OrderReconciliationService) GetStats() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := map[string]interface{}{
		"total_orders":          len(s.orders),
		"total_uncertain":       s.totalUncertain,
		"total_reconciled":      s.totalReconciled,
		"total_actually_filled": s.totalActuallyFilled,
		"false_negative_rate":   0.0,
	}

	if s.totalUncertain > 0 {
		stats["false_negative_rate"] = float64(s.totalActuallyFilled) / float64(s.totalUncertain) * 100
	}

	// Count by state
	stateCount := make(map[OrderState]int)
	for _, record := range s.orders {
		stateCount[record.State]++
	}
	stats["by_state"] = stateCount

	return stats
}

// CleanupOldOrders removes old completed orders from memory
func (s *OrderReconciliationService) CleanupOldOrders(maxAge time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for id, record := range s.orders {
		if record.State == OrderStateConfirmed || record.State == OrderStateFailed || record.State == OrderStateReconciled {
			if now.Sub(record.SubmittedAt) > maxAge {
				delete(s.orders, id)
			}
		}
	}
}
