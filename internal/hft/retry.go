package hft

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"time"
)

// RetryPolicy defines retry behavior
type RetryPolicy struct {
	MaxAttempts  int
	InitialDelay time.Duration
	MaxDelay     time.Duration
	Multiplier   float64
	RandomFactor float64
	ShouldRetry  func(error) bool
	OnRetry      func(attempt int, err error, delay time.Duration)
}

// DefaultRetryPolicy returns default retry policy
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts:  3,
		InitialDelay: time.Millisecond * 100,
		MaxDelay:     time.Second * 30,
		Multiplier:   2.0,
		RandomFactor: 0.1,
		ShouldRetry:  DefaultShouldRetry,
	}
}

// DefaultShouldRetry determines if error is retryable
func DefaultShouldRetry(err error) bool {
	if err == nil {
		return false
	}

	// Don't retry circuit breaker errors
	if errors.Is(err, ErrCircuitOpen) || errors.Is(err, ErrTooManyRequests) {
		return false
	}

	errStr := err.Error()

	// Retry network errors
	if contains(errStr, "network") || contains(errStr, "connection") {
		return true
	}

	// Retry timeouts
	if contains(errStr, "timeout") || contains(errStr, "deadline") {
		return true
	}

	// Retry rate limits
	if contains(errStr, "rate limit") || contains(errStr, "too many requests") {
		return true
	}

	// Retry temporary errors
	if contains(errStr, "temporary") || contains(errStr, "unavailable") {
		return true
	}

	// Don't retry validation errors
	if contains(errStr, "invalid") || contains(errStr, "validation") {
		return false
	}

	// Don't retry balance errors
	if contains(errStr, "balance") || contains(errStr, "insufficient") {
		return false
	}

	// Default: don't retry
	return false
}

// RetryWithPolicy executes function with retry policy
func RetryWithPolicy(ctx context.Context, policy RetryPolicy, fn func() error) error {
	var lastErr error

	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		// Execute function
		err := fn()

		// Success
		if err == nil {
			return nil
		}

		lastErr = err

		// Check if should retry
		if !policy.ShouldRetry(err) {
			return err
		}

		// Last attempt
		if attempt == policy.MaxAttempts {
			return fmt.Errorf("max retry attempts (%d) exceeded: %w", policy.MaxAttempts, err)
		}

		// Calculate delay with exponential backoff
		delay := calculateBackoff(attempt, policy)

		// Callback
		if policy.OnRetry != nil {
			policy.OnRetry(attempt, err, delay)
		}

		// Wait with context cancellation support
		select {
		case <-time.After(delay):
			// Continue to next attempt
		case <-ctx.Done():
			return fmt.Errorf("retry cancelled: %w", ctx.Err())
		}
	}

	return lastErr
}

// RetryWithPolicyContext executes function with context and retry policy
func RetryWithPolicyContext(ctx context.Context, policy RetryPolicy, fn func(context.Context) error) error {
	var lastErr error

	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		// Execute function
		err := fn(ctx)

		// Success
		if err == nil {
			return nil
		}

		lastErr = err

		// Check if should retry
		if !policy.ShouldRetry(err) {
			return err
		}

		// Last attempt
		if attempt == policy.MaxAttempts {
			return fmt.Errorf("max retry attempts (%d) exceeded: %w", policy.MaxAttempts, err)
		}

		// Calculate delay with exponential backoff
		delay := calculateBackoff(attempt, policy)

		// Callback
		if policy.OnRetry != nil {
			policy.OnRetry(attempt, err, delay)
		}

		// Wait with context cancellation support
		select {
		case <-time.After(delay):
			// Continue to next attempt
		case <-ctx.Done():
			return fmt.Errorf("retry cancelled: %w", ctx.Err())
		}
	}

	return lastErr
}

// calculateBackoff calculates exponential backoff with jitter
func calculateBackoff(attempt int, policy RetryPolicy) time.Duration {
	// Exponential backoff: initialDelay * multiplier^(attempt-1)
	delay := float64(policy.InitialDelay) * math.Pow(policy.Multiplier, float64(attempt-1))

	// Cap at max delay
	if delay > float64(policy.MaxDelay) {
		delay = float64(policy.MaxDelay)
	}

	// Add jitter (random factor)
	if policy.RandomFactor > 0 {
		jitter := delay * policy.RandomFactor
		delay = delay - jitter + (rand.Float64() * jitter * 2)
	}

	return time.Duration(delay)
}

// Retry executes function with default retry policy
func Retry(ctx context.Context, fn func() error) error {
	return RetryWithPolicy(ctx, DefaultRetryPolicy(), fn)
}

// RetryContext executes function with context and default retry policy
func RetryContext(ctx context.Context, fn func(context.Context) error) error {
	return RetryWithPolicyContext(ctx, DefaultRetryPolicy(), fn)
}

// ResilientExecutor combines circuit breaker and retry
type ResilientExecutor struct {
	circuitBreaker *CircuitBreaker
	retryPolicy    RetryPolicy
}

// NewResilientExecutor creates a new resilient executor
func NewResilientExecutor(cb *CircuitBreaker, policy RetryPolicy) *ResilientExecutor {
	return &ResilientExecutor{
		circuitBreaker: cb,
		retryPolicy:    policy,
	}
}

// Execute executes function with circuit breaker and retry
func (r *ResilientExecutor) Execute(ctx context.Context, fn func() error) error {
	return RetryWithPolicyContext(ctx, r.retryPolicy, func(ctx context.Context) error {
		return r.circuitBreaker.Execute(fn)
	})
}

// ExecuteContext executes function with context, circuit breaker and retry
func (r *ResilientExecutor) ExecuteContext(ctx context.Context, fn func(context.Context) error) error {
	return RetryWithPolicyContext(ctx, r.retryPolicy, func(ctx context.Context) error {
		return r.circuitBreaker.ExecuteContext(ctx, fn)
	})
}

// Stats returns circuit breaker statistics
func (r *ResilientExecutor) Stats() CircuitBreakerStats {
	return r.circuitBreaker.Stats()
}

// Reset resets the circuit breaker
func (r *ResilientExecutor) Reset() {
	r.circuitBreaker.Reset()
}
