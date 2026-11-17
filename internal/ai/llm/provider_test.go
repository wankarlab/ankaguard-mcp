package llm

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateRequest(t *testing.T) {
	tests := []struct {
		name    string
		request *LLMRequest
		wantErr bool
		errMsg  string
	}{
		{
			name:    "nil request",
			request: nil,
			wantErr: true,
			errMsg:  "request cannot be nil",
		},
		{
			name: "empty prompt",
			request: &LLMRequest{
				Prompt:      "",
				Temperature: 0.7,
				MaxTokens:   1000,
			},
			wantErr: true,
			errMsg:  "prompt cannot be empty",
		},
		{
			name: "invalid temperature - too low",
			request: &LLMRequest{
				Prompt:      "test prompt",
				Temperature: -0.1,
				MaxTokens:   1000,
			},
			wantErr: true,
			errMsg:  "temperature must be between 0 and 2",
		},
		{
			name: "invalid temperature - too high",
			request: &LLMRequest{
				Prompt:      "test prompt",
				Temperature: 2.1,
				MaxTokens:   1000,
			},
			wantErr: true,
			errMsg:  "temperature must be between 0 and 2",
		},
		{
			name: "invalid max tokens",
			request: &LLMRequest{
				Prompt:      "test prompt",
				Temperature: 0.7,
				MaxTokens:   0,
			},
			wantErr: true,
			errMsg:  "max_tokens must be positive",
		},
		{
			name: "valid request",
			request: &LLMRequest{
				Prompt:      "test prompt",
				Temperature: 0.7,
				MaxTokens:   1000,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRequest(tt.request)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestSanitizePrompt(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
		want   string
	}{
		{
			name:   "normal prompt",
			prompt: "Analyze this market data",
			want:   "Analyze this market data",
		},
		{
			name:   "empty prompt",
			prompt: "",
			want:   "",
		},
		{
			name:   "prompt with special characters",
			prompt: "Price: $100.50, Volume: 1,000",
			want:   "Price: $100.50, Volume: 1,000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizePrompt(tt.prompt)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBaseProvider_GetCost(t *testing.T) {
	config := &ProviderConfig{
		Name:           "test",
		CostPerInputK:  0.03, // $0.03 per 1K input tokens
		CostPerOutputK: 0.06, // $0.06 per 1K output tokens
		TimeoutSeconds: 10,
		MaxRetries:     3,
		RetryDelay:     1 * time.Second,
	}

	provider := NewBaseProvider(config)

	tests := []struct {
		name         string
		inputTokens  int
		outputTokens int
		wantCost     float64
	}{
		{
			name:         "zero tokens",
			inputTokens:  0,
			outputTokens: 0,
			wantCost:     0.0,
		},
		{
			name:         "1000 input tokens",
			inputTokens:  1000,
			outputTokens: 0,
			wantCost:     0.03,
		},
		{
			name:         "1000 output tokens",
			inputTokens:  0,
			outputTokens: 1000,
			wantCost:     0.06,
		},
		{
			name:         "500 input + 200 output tokens",
			inputTokens:  500,
			outputTokens: 200,
			wantCost:     0.027, // (500/1000 * 0.03) + (200/1000 * 0.06)
		},
		{
			name:         "typical call",
			inputTokens:  750,
			outputTokens: 250,
			wantCost:     0.0375, // (750/1000 * 0.03) + (250/1000 * 0.06)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := provider.GetCost(tt.inputTokens, tt.outputTokens)
			assert.InDelta(t, tt.wantCost, got, 0.0001)
		})
	}
}

func TestBaseProvider_UpdateStats(t *testing.T) {
	config := &ProviderConfig{
		Name:           "test",
		TimeoutSeconds: 10,
		MaxRetries:     3,
		RetryDelay:     1 * time.Second,
	}

	provider := NewBaseProvider(config)

	// Test successful call
	provider.UpdateStats(true, 0.05, 1000, 2*time.Second, nil)

	stats := provider.GetStats()
	assert.Equal(t, int64(1), stats.TotalCalls)
	assert.Equal(t, int64(1), stats.SuccessfulCalls)
	assert.Equal(t, int64(0), stats.FailedCalls)
	assert.Equal(t, 0.05, stats.TotalCost)
	assert.Equal(t, int64(1000), stats.TotalTokens)
	assert.Equal(t, 2*time.Second, stats.AvgLatency)

	// Test failed call
	provider.UpdateStats(false, 0.0, 0, 1*time.Second, assert.AnError)

	stats = provider.GetStats()
	assert.Equal(t, int64(2), stats.TotalCalls)
	assert.Equal(t, int64(1), stats.SuccessfulCalls)
	assert.Equal(t, int64(1), stats.FailedCalls)
	assert.Equal(t, 0.05, stats.TotalCost)
	assert.Equal(t, int64(1000), stats.TotalTokens)
	assert.Equal(t, 1500*time.Millisecond, stats.AvgLatency) // Average of 2s and 1s
	assert.NotEmpty(t, stats.LastError)
}

func TestBaseProvider_IsAvailable(t *testing.T) {
	config := &ProviderConfig{
		Name:           "test",
		TimeoutSeconds: 10,
		MaxRetries:     3,
		RetryDelay:     1 * time.Second,
	}

	provider := NewBaseProvider(config)
	ctx := context.Background()

	// Initially available
	assert.True(t, provider.IsAvailable(ctx))

	// Record failures to trip circuit breaker (threshold = 10 per NewCircuitBreaker default)
	for i := 0; i < 10; i++ {
		provider.circuitBreaker.RecordFailure()
	}

	// Should be unavailable after maxFailures (10) failures
	assert.False(t, provider.IsAvailable(ctx))
}

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "context canceled",
			err:  context.Canceled,
			want: false,
		},
		{
			name: "context deadline exceeded",
			err:  context.DeadlineExceeded,
			want: false,
		},
		{
			name: "unauthorized error",
			err:  assert.AnError,
			want: true,
		},
		{
			name: "rate limit error",
			err:  assert.AnError,
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isRetryableError(tt.err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCircuitBreaker(t *testing.T) {
	cb := NewCircuitBreaker(3, 100*time.Millisecond)

	// Initially available
	assert.True(t, cb.IsAvailable())

	// Record 2 failures - should still be available
	cb.RecordFailure()
	cb.RecordFailure()
	assert.True(t, cb.IsAvailable())

	// Record 3rd failure - should trip
	cb.RecordFailure()
	assert.False(t, cb.IsAvailable())

	// Wait for timeout
	time.Sleep(150 * time.Millisecond)

	// Should be available again after timeout
	assert.True(t, cb.IsAvailable())

	// Record success - should reset
	cb.RecordSuccess()
	assert.True(t, cb.IsAvailable())
}
