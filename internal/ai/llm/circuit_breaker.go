package llm

import (
	"sync"
	"time"
)

// CircuitBreakerState represents the state of a circuit breaker
type CircuitBreakerState string

const (
	StateClosed   CircuitBreakerState = "closed"    // Normal operation
	StateOpen     CircuitBreakerState = "open"      // Failing, reject requests
	StateHalfOpen CircuitBreakerState = "half_open" // Testing if service recovered
)

// CircuitBreaker implements the circuit breaker pattern
type CircuitBreaker struct {
	maxFailures  int
	resetTimeout time.Duration

	mu          sync.RWMutex
	state       CircuitBreakerState
	failures    int
	lastFailure time.Time
	lastSuccess time.Time
}

// NewCircuitBreaker creates a new circuit breaker
func NewCircuitBreaker(maxFailures int, resetTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		maxFailures:  maxFailures,
		resetTimeout: resetTimeout,
		state:        StateClosed,
	}
}

// IsAvailable checks if the circuit breaker allows requests
func (cb *CircuitBreaker) IsAvailable() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	switch cb.state {
	case StateClosed:
		return true
	case StateOpen:
		// Check if we should transition to half-open
		if time.Since(cb.lastFailure) > cb.resetTimeout {
			return true // Will transition to half-open on next call
		}
		return false
	case StateHalfOpen:
		return true
	default:
		return false
	}
}

// RecordSuccess records a successful call
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.lastSuccess = time.Now()
	cb.failures = 0

	if cb.state == StateHalfOpen {
		cb.state = StateClosed
	}
}

// RecordFailure records a failed call
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures++
	cb.lastFailure = time.Now()

	if cb.state == StateHalfOpen {
		// Failed in half-open state, go back to open
		cb.state = StateOpen
		return
	}

	if cb.failures >= cb.maxFailures {
		cb.state = StateOpen
	}
}

// GetState returns the current state
func (cb *CircuitBreaker) GetState() CircuitBreakerState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	// Check if we should transition from open to half-open
	if cb.state == StateOpen && time.Since(cb.lastFailure) > cb.resetTimeout {
		cb.mu.RUnlock()
		cb.mu.Lock()
		cb.state = StateHalfOpen
		cb.mu.Unlock()
		cb.mu.RLock()
	}

	return cb.state
}

// Reset resets the circuit breaker to closed state
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.state = StateClosed
	cb.failures = 0
}

// GetFailures returns the current failure count
func (cb *CircuitBreaker) GetFailures() int {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.failures
}

// GetLastFailure returns the time of the last failure
func (cb *CircuitBreaker) GetLastFailure() time.Time {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.lastFailure
}

// GetLastSuccess returns the time of the last success
func (cb *CircuitBreaker) GetLastSuccess() time.Time {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.lastSuccess
}
