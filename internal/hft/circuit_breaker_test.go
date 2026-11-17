package hft

import (
	"errors"
	"testing"
	"time"
)

func TestCircuitBreakerClosed(t *testing.T) {
	config := DefaultCircuitBreakerConfig("test")
	cb := NewCircuitBreaker(config)

	// Should be closed initially
	if cb.State() != StateClosed {
		t.Errorf("Expected state closed, got %s", cb.State())
	}

	// Successful requests should keep it closed
	for i := 0; i < 10; i++ {
		err := cb.Execute(func() error {
			return nil
		})
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
	}

	if cb.State() != StateClosed {
		t.Errorf("Expected state closed after successes, got %s", cb.State())
	}
}

func TestCircuitBreakerOpens(t *testing.T) {
	config := DefaultCircuitBreakerConfig("test")
	config.MinRequests = 3
	config.FailureThreshold = 0.6
	cb := NewCircuitBreaker(config)

	// Generate failures to trip circuit
	testErr := errors.New("test error")
	for i := 0; i < 5; i++ {
		cb.Execute(func() error {
			return testErr
		})
	}

	// Circuit should be open
	if cb.State() != StateOpen {
		t.Errorf("Expected state open after failures, got %s", cb.State())
	}

	// Requests should be rejected
	err := cb.Execute(func() error {
		return nil
	})

	if !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("Expected ErrCircuitOpen, got %v", err)
	}
}

func TestCircuitBreakerHalfOpen(t *testing.T) {
	config := DefaultCircuitBreakerConfig("test")
	config.Timeout = time.Millisecond * 100
	config.MinRequests = 2
	config.FailureThreshold = 0.6
	cb := NewCircuitBreaker(config)

	// Trip circuit
	testErr := errors.New("test error")
	for i := 0; i < 3; i++ {
		cb.Execute(func() error {
			return testErr
		})
	}

	if cb.State() != StateOpen {
		t.Fatal("Circuit should be open")
	}

	// Wait for timeout
	time.Sleep(time.Millisecond * 150)

	// Should transition to half-open
	if cb.State() != StateHalfOpen {
		t.Errorf("Expected state half-open after timeout, got %s", cb.State())
	}

	// Successful requests should close circuit
	for i := 0; i < int(config.MaxRequests); i++ {
		err := cb.Execute(func() error {
			return nil
		})
		if err != nil {
			t.Errorf("Unexpected error in half-open: %v", err)
		}
	}

	if cb.State() != StateClosed {
		t.Errorf("Expected state closed after successes in half-open, got %s", cb.State())
	}
}

func TestCircuitBreakerStats(t *testing.T) {
	config := DefaultCircuitBreakerConfig("test")
	cb := NewCircuitBreaker(config)

	// Execute some requests
	for i := 0; i < 5; i++ {
		cb.Execute(func() error {
			return nil
		})
	}

	for i := 0; i < 3; i++ {
		cb.Execute(func() error {
			return errors.New("error")
		})
	}

	stats := cb.Stats()

	if stats.Requests != 8 {
		t.Errorf("Expected 8 requests, got %d", stats.Requests)
	}

	if stats.TotalSuccesses != 5 {
		t.Errorf("Expected 5 successes, got %d", stats.TotalSuccesses)
	}

	if stats.TotalFailures != 3 {
		t.Errorf("Expected 3 failures, got %d", stats.TotalFailures)
	}
}

func BenchmarkCircuitBreakerClosed(b *testing.B) {
	config := DefaultCircuitBreakerConfig("bench")
	cb := NewCircuitBreaker(config)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cb.Execute(func() error {
			return nil
		})
	}
}

func BenchmarkCircuitBreakerOpen(b *testing.B) {
	config := DefaultCircuitBreakerConfig("bench")
	config.MinRequests = 1
	cb := NewCircuitBreaker(config)

	// Trip circuit
	for i := 0; i < 3; i++ {
		cb.Execute(func() error {
			return errors.New("error")
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cb.Execute(func() error {
			return nil
		})
	}
}
