package hft

import (
	"sync"
	"time"
)

// OrderPool manages a pool of reusable Order objects to reduce allocations
type OrderPool struct {
	pool *sync.Pool
}

// NewOrderPool creates a new order pool
func NewOrderPool() *OrderPool {
	return &OrderPool{
		pool: &sync.Pool{
			New: func() interface{} {
				return &PooledOrder{
					Tags:      make([]string, 0, 10),
					Metadata:  make(map[string]interface{}, 5),
					CreatedAt: time.Now(),
				}
			},
		},
	}
}

// Acquire gets an order from the pool
func (p *OrderPool) Acquire() *PooledOrder {
	return p.pool.Get().(*PooledOrder)
}

// Release returns an order to the pool after resetting it
func (p *OrderPool) Release(order *PooledOrder) {
	if order != nil {
		order.Reset()
		p.pool.Put(order)
	}
}

// PooledOrder represents a reusable order object
type PooledOrder struct {
	// Core fields
	ID            string
	ClientOrderID string
	Exchange      string
	Symbol        string
	Side          string // "buy" or "sell"
	Type          string // "market" or "limit"
	Amount        float64
	Price         float64
	Leverage      float64

	// Status
	Status       string
	FilledAmount float64
	AvgPrice     float64

	// Futures specific
	ReduceOnly  bool
	PostOnly    bool
	TimeInForce string

	// Metadata
	Tags      []string
	Metadata  map[string]interface{}
	CreatedAt time.Time
	UpdatedAt time.Time

	// Performance tracking
	SubmittedAt time.Time
	ExecutedAt  time.Time
	LatencyUs   int64 // Latency in microseconds
}

// Reset clears the order for reuse
func (o *PooledOrder) Reset() {
	o.ID = ""
	o.ClientOrderID = ""
	o.Exchange = ""
	o.Symbol = ""
	o.Side = ""
	o.Type = ""
	o.Amount = 0
	o.Price = 0
	o.Leverage = 0
	o.Status = ""
	o.FilledAmount = 0
	o.AvgPrice = 0
	o.ReduceOnly = false
	o.PostOnly = false
	o.TimeInForce = ""

	// Keep capacity, just reset length
	o.Tags = o.Tags[:0]

	// Clear map but keep capacity
	for k := range o.Metadata {
		delete(o.Metadata, k)
	}

	o.CreatedAt = time.Time{}
	o.UpdatedAt = time.Time{}
	o.SubmittedAt = time.Time{}
	o.ExecutedAt = time.Time{}
	o.LatencyUs = 0
}

// Clone creates a copy of the order (for logging/history)
func (o *PooledOrder) Clone() *PooledOrder {
	clone := &PooledOrder{
		ID:            o.ID,
		ClientOrderID: o.ClientOrderID,
		Exchange:      o.Exchange,
		Symbol:        o.Symbol,
		Side:          o.Side,
		Type:          o.Type,
		Amount:        o.Amount,
		Price:         o.Price,
		Leverage:      o.Leverage,
		Status:        o.Status,
		FilledAmount:  o.FilledAmount,
		AvgPrice:      o.AvgPrice,
		ReduceOnly:    o.ReduceOnly,
		PostOnly:      o.PostOnly,
		TimeInForce:   o.TimeInForce,
		Tags:          make([]string, len(o.Tags)),
		Metadata:      make(map[string]interface{}, len(o.Metadata)),
		CreatedAt:     o.CreatedAt,
		UpdatedAt:     o.UpdatedAt,
		SubmittedAt:   o.SubmittedAt,
		ExecutedAt:    o.ExecutedAt,
		LatencyUs:     o.LatencyUs,
	}

	copy(clone.Tags, o.Tags)
	for k, v := range o.Metadata {
		clone.Metadata[k] = v
	}

	return clone
}

// CalculateLatency calculates and stores the execution latency
func (o *PooledOrder) CalculateLatency() {
	if !o.SubmittedAt.IsZero() && !o.ExecutedAt.IsZero() {
		o.LatencyUs = o.ExecutedAt.Sub(o.SubmittedAt).Microseconds()
	}
}
