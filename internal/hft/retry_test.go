package hft

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetrySuccess(t *testing.T) {
	attempts := 0

	err := Retry(context.Background(), func() error {
		attempts++
		return nil
	})

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if attempts != 1 {
		t.Errorf("Expected 1 attempt, got %d", attempts)
	}
}

func TestRetryEventualSuccess(t *testing.T) {
	attempts := 0

	err := Retry(context.Background(), func() error {
		attempts++
		if attempts < 3 {
			return errors.New("temporary error")
		}
		return nil
	})

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if attempts != 3 {
		t.Errorf("Expected 3 attempts, got %d", attempts)
	}
}

func TestRetryMaxAttempts(t *testing.T) {
	attempts := 0
	testErr := errors.New("network error")

	err := Retry(context.Background(), func() error {
		attempts++
		return testErr
	})

	if err == nil {
		t.Error("Expected error after max attempts")
	}

	policy := DefaultRetryPolicy()
	if attempts != policy.MaxAttempts {
		t.Errorf("Expected %d attempts, got %d", policy.MaxAttempts, attempts)
	}
}

func TestRetryNonRetryableError(t *testing.T) {
	attempts := 0

	err := Retry(context.Background(), func() error {
		attempts++
		return errors.New("invalid request")
	})

	if err == nil {
		t.Error("Expected error")
	}

	if attempts != 1 {
		t.Errorf("Expected 1 attempt for non-retryable error, got %d", attempts)
	}
}

func TestRetryContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0

	// Cancel after first attempt
	go func() {
		time.Sleep(time.Millisecond * 50)
		cancel()
	}()

	err := Retry(ctx, func() error {
		attempts++
		return errors.New("network error")
	})

	if err == nil {
		t.Error("Expected error from context cancellation")
	}

	if attempts > 2 {
		t.Errorf("Expected few attempts due to cancellation, got %d", attempts)
	}
}

func TestRetryWithPolicy(t *testing.T) {
	policy := RetryPolicy{
		MaxAttempts:  5,
		InitialDelay: time.Millisecond,
		MaxDelay:     time.Millisecond * 100,
		Multiplier:   2.0,
		RandomFactor: 0.0,
		ShouldRetry:  DefaultShouldRetry,
	}

	attempts := 0
	err := RetryWithPolicy(context.Background(), policy, func() error {
		attempts++
		if attempts < 4 {
			return errors.New("timeout")
		}
		return nil
	})

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if attempts != 4 {
		t.Errorf("Expected 4 attempts, got %d", attempts)
	}
}

func TestDefaultShouldRetry(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"network error", errors.New("network error"), true},
		{"connection error", errors.New("connection refused"), true},
		{"timeout error", errors.New("timeout"), true},
		{"rate limit", errors.New("rate limit exceeded"), true},
		{"temporary error", errors.New("temporary failure"), true},
		{"validation error", errors.New("invalid input"), false},
		{"balance error", errors.New("insufficient balance"), false},
		{"circuit open", ErrCircuitOpen, false},
		{"too many requests", ErrTooManyRequests, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DefaultShouldRetry(tt.err)
			if result != tt.expected {
				t.Errorf("Expected %v for %v, got %v", tt.expected, tt.err, result)
			}
		})
	}
}

func BenchmarkRetrySuccess(b *testing.B) {
	for i := 0; i < b.N; i++ {
		Retry(context.Background(), func() error {
			return nil
		})
	}
}

func BenchmarkRetryWithBackoff(b *testing.B) {
	policy := RetryPolicy{
		MaxAttempts:  3,
		InitialDelay: time.Nanosecond,
		MaxDelay:     time.Microsecond,
		Multiplier:   2.0,
		RandomFactor: 0.0,
		ShouldRetry:  DefaultShouldRetry,
	}

	attempts := 0
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		RetryWithPolicy(context.Background(), policy, func() error {
			attempts++
			if attempts%2 == 0 {
				return nil
			}
			return errors.New("network error")
		})
	}
}
