package hft

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// CircuitState represents the state of the circuit breaker
type CircuitState int32

const (
	// StateClosed allows requests to pass through
	StateClosed CircuitState = iota
	// StateOpen blocks requests
	StateOpen
	// StateHalfOpen allows limited requests to test recovery
	StateHalfOpen
)

func (s CircuitState) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

var (
	// ErrCircuitOpen is returned when circuit is open
	ErrCircuitOpen = errors.New("circuit breaker is open")
	// ErrTooManyRequests is returned when too many requests in half-open state
	ErrTooManyRequests = errors.New("too many requests")
)

// CircuitBreaker implements the circuit breaker pattern
type CircuitBreaker struct {
	name string

	// Configuration
	maxRequests      uint32        // Max requests in half-open state
	interval         time.Duration // Interval to reset counts
	timeout          time.Duration // Timeout before trying half-open
	failureThreshold float64       // Failure ratio to trip (0.0-1.0)
	minRequests      uint32        // Min requests before checking threshold

	// State
	state      atomic.Value // CircuitState
	generation atomic.Uint64
	counts     atomic.Value // *Counts
	expiry     atomic.Value // time.Time

	// Callbacks
	onStateChange func(name string, from, to CircuitState)

	mu sync.Mutex
}

// Counts holds request statistics
type Counts struct {
	Requests             uint32
	TotalSuccesses       uint32
	TotalFailures        uint32
	ConsecutiveSuccesses uint32
	ConsecutiveFailures  uint32
}

// CircuitBreakerConfig holds circuit breaker configuration
type CircuitBreakerConfig struct {
	Name             string
	MaxRequests      uint32
	Interval         time.Duration
	Timeout          time.Duration
	FailureThreshold float64
	MinRequests      uint32
	OnStateChange    func(name string, from, to CircuitState)
}

// DefaultCircuitBreakerConfig returns default configuration
func DefaultCircuitBreakerConfig(name string) CircuitBreakerConfig {
	return CircuitBreakerConfig{
		Name:             name,
		MaxRequests:      3,
		Interval:         time.Second * 10,
		Timeout:          time.Second * 60,
		FailureThreshold: 0.6,
		MinRequests:      3,
	}
}

// NewCircuitBreaker creates a new circuit breaker
func NewCircuitBreaker(config CircuitBreakerConfig) *CircuitBreaker {
	cb := &CircuitBreaker{
		name:             config.Name,
		maxRequests:      config.MaxRequests,
		interval:         config.Interval,
		timeout:          config.Timeout,
		failureThreshold: config.FailureThreshold,
		minRequests:      config.MinRequests,
		onStateChange:    config.OnStateChange,
	}

	cb.state.Store(StateClosed)
	cb.counts.Store(&Counts{})
	cb.expiry.Store(time.Now().Add(cb.interval))

	return cb
}

// Execute executes the given function with circuit breaker protection
func (cb *CircuitBreaker) Execute(fn func() error) error {
	generation, err := cb.beforeRequest()
	if err != nil {
		return err
	}

	defer func() {
		if r := recover(); r != nil {
			cb.afterRequest(generation, false)
			panic(r)
		}
	}()

	err = fn()
	cb.afterRequest(generation, err == nil)

	return err
}

// ExecuteContext executes the given function with context and circuit breaker protection
func (cb *CircuitBreaker) ExecuteContext(ctx context.Context, fn func(context.Context) error) error {
	generation, err := cb.beforeRequest()
	if err != nil {
		return err
	}

	defer func() {
		if r := recover(); r != nil {
			cb.afterRequest(generation, false)
			panic(r)
		}
	}()

	err = fn(ctx)
	cb.afterRequest(generation, err == nil)

	return err
}

// beforeRequest checks if request can proceed
func (cb *CircuitBreaker) beforeRequest() (uint64, error) {
	state := cb.State()
	generation := cb.generation.Load()

	if state == StateOpen {
		return generation, ErrCircuitOpen
	}

	if state == StateHalfOpen {
		counts := cb.counts.Load().(*Counts)
		if counts.Requests >= cb.maxRequests {
			return generation, ErrTooManyRequests
		}
	}

	cb.incrementRequests()

	return generation, nil
}

// afterRequest updates state after request completion
func (cb *CircuitBreaker) afterRequest(generation uint64, success bool) {
	currentGeneration := cb.generation.Load()
	if generation != currentGeneration {
		return
	}

	if success {
		cb.onSuccess()
	} else {
		cb.onFailure()
	}
}

// incrementRequests increments request count
func (cb *CircuitBreaker) incrementRequests() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	counts := cb.counts.Load().(*Counts)
	newCounts := &Counts{
		Requests:             counts.Requests + 1,
		TotalSuccesses:       counts.TotalSuccesses,
		TotalFailures:        counts.TotalFailures,
		ConsecutiveSuccesses: counts.ConsecutiveSuccesses,
		ConsecutiveFailures:  counts.ConsecutiveFailures,
	}
	cb.counts.Store(newCounts)
}

// onSuccess handles successful request
func (cb *CircuitBreaker) onSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	counts := cb.counts.Load().(*Counts)
	newCounts := &Counts{
		Requests:             counts.Requests,
		TotalSuccesses:       counts.TotalSuccesses + 1,
		TotalFailures:        counts.TotalFailures,
		ConsecutiveSuccesses: counts.ConsecutiveSuccesses + 1,
		ConsecutiveFailures:  0,
	}
	cb.counts.Store(newCounts)

	state := cb.State()
	if state == StateHalfOpen {
		// Check if we should close the circuit
		if newCounts.ConsecutiveSuccesses >= cb.maxRequests {
			cb.setState(StateClosed)
		}
	}
}

// onFailure handles failed request
func (cb *CircuitBreaker) onFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	counts := cb.counts.Load().(*Counts)
	newCounts := &Counts{
		Requests:             counts.Requests,
		TotalSuccesses:       counts.TotalSuccesses,
		TotalFailures:        counts.TotalFailures + 1,
		ConsecutiveSuccesses: 0,
		ConsecutiveFailures:  counts.ConsecutiveFailures + 1,
	}
	cb.counts.Store(newCounts)

	state := cb.State()
	if state == StateHalfOpen {
		// Any failure in half-open state opens the circuit
		cb.setState(StateOpen)
	} else if state == StateClosed {
		// Check if we should open the circuit
		if cb.shouldTrip(newCounts) {
			cb.setState(StateOpen)
		}
	}
}

// shouldTrip determines if circuit should trip based on counts
func (cb *CircuitBreaker) shouldTrip(counts *Counts) bool {
	if counts.Requests < cb.minRequests {
		return false
	}

	failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
	return failureRatio >= cb.failureThreshold
}

// setState changes the circuit breaker state
func (cb *CircuitBreaker) setState(newState CircuitState) {
	oldState := cb.state.Load().(CircuitState)
	if oldState == newState {
		return
	}

	cb.state.Store(newState)
	cb.generation.Add(1)

	now := time.Now()
	switch newState {
	case StateClosed:
		cb.expiry.Store(now.Add(cb.interval))
	case StateOpen:
		cb.expiry.Store(now.Add(cb.timeout))
	case StateHalfOpen:
		cb.expiry.Store(now.Add(cb.interval))
	}

	// Reset counts
	cb.counts.Store(&Counts{})

	// Callback
	if cb.onStateChange != nil {
		cb.onStateChange(cb.name, oldState, newState)
	}
}

// State returns current circuit breaker state
func (cb *CircuitBreaker) State() CircuitState {
	// Check if we need to transition
	now := time.Now()
	expiry := cb.expiry.Load().(time.Time)

	if now.After(expiry) {
		cb.mu.Lock()
		defer cb.mu.Unlock()

		// Double-check after acquiring lock
		expiry = cb.expiry.Load().(time.Time)
		if now.After(expiry) {
			state := cb.state.Load().(CircuitState)
			if state == StateOpen {
				cb.setState(StateHalfOpen)
			} else if state == StateClosed {
				// Reset counts after interval
				cb.counts.Store(&Counts{})
				cb.expiry.Store(now.Add(cb.interval))
			}
		}
	}

	return cb.state.Load().(CircuitState)
}

// Counts returns current counts
func (cb *CircuitBreaker) Counts() Counts {
	return *cb.counts.Load().(*Counts)
}

// Name returns circuit breaker name
func (cb *CircuitBreaker) Name() string {
	return cb.name
}

// Reset resets the circuit breaker to closed state
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.setState(StateClosed)
}

// Stats returns circuit breaker statistics
func (cb *CircuitBreaker) Stats() CircuitBreakerStats {
	counts := cb.Counts()
	state := cb.State()

	successRate := 0.0
	if counts.Requests > 0 {
		successRate = float64(counts.TotalSuccesses) / float64(counts.Requests) * 100
	}

	return CircuitBreakerStats{
		Name:                 cb.name,
		State:                state.String(),
		Requests:             counts.Requests,
		TotalSuccesses:       counts.TotalSuccesses,
		TotalFailures:        counts.TotalFailures,
		ConsecutiveSuccesses: counts.ConsecutiveSuccesses,
		ConsecutiveFailures:  counts.ConsecutiveFailures,
		SuccessRate:          successRate,
	}
}

// CircuitBreakerStats holds circuit breaker statistics
type CircuitBreakerStats struct {
	Name                 string
	State                string
	Requests             uint32
	TotalSuccesses       uint32
	TotalFailures        uint32
	ConsecutiveSuccesses uint32
	ConsecutiveFailures  uint32
	SuccessRate          float64
}

// String returns string representation of stats
func (s CircuitBreakerStats) String() string {
	return fmt.Sprintf(
		"CircuitBreaker[%s] state=%s requests=%d successes=%d failures=%d success_rate=%.2f%%",
		s.Name, s.State, s.Requests, s.TotalSuccesses, s.TotalFailures, s.SuccessRate,
	)
}
