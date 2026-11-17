package llm

import (
	"context"
	"fmt"
	"time"
)

// Provider defines the interface for LLM providers
type Provider interface {
	// Name returns the provider name
	Name() string

	// Call makes a call to the LLM
	Call(ctx context.Context, request *LLMRequest) (*LLMResponse, error)

	// GetCost calculates the cost of a call
	GetCost(inputTokens, outputTokens int) float64

	// IsAvailable checks if the provider is available
	IsAvailable(ctx context.Context) bool

	// GetStats returns provider statistics
	GetStats() *ProviderStats
}

// BaseProvider provides common functionality for all providers
type BaseProvider struct {
	config         *ProviderConfig
	stats          *ProviderStats
	circuitBreaker *CircuitBreaker
}

// NewBaseProvider creates a new base provider
func NewBaseProvider(config *ProviderConfig) *BaseProvider {
	return &BaseProvider{
		config: config,
		stats: &ProviderStats{
			Provider: config.Name,
		},
		circuitBreaker: NewCircuitBreaker(10, 30*time.Second), // 10 failures before open, 30s recovery
	}
}

// Name returns the provider name
func (bp *BaseProvider) Name() string {
	return bp.config.Name
}

// GetCost calculates the cost based on token usage
func (bp *BaseProvider) GetCost(inputTokens, outputTokens int) float64 {
	inputCost := float64(inputTokens) / 1000.0 * bp.config.CostPerInputK
	outputCost := float64(outputTokens) / 1000.0 * bp.config.CostPerOutputK
	return inputCost + outputCost
}

// GetStats returns provider statistics
func (bp *BaseProvider) GetStats() *ProviderStats {
	return bp.stats
}

// UpdateStats updates provider statistics
func (bp *BaseProvider) UpdateStats(success bool, cost float64, tokens int64, latency time.Duration, err error) {
	bp.stats.TotalCalls++
	if success {
		bp.stats.SuccessfulCalls++
	} else {
		bp.stats.FailedCalls++
		if err != nil {
			bp.stats.LastError = err.Error()
		}
	}
	bp.stats.TotalCost += cost
	bp.stats.TotalTokens += tokens

	// Update average latency
	if bp.stats.TotalCalls > 0 {
		totalLatency := bp.stats.AvgLatency * time.Duration(bp.stats.TotalCalls-1)
		bp.stats.AvgLatency = (totalLatency + latency) / time.Duration(bp.stats.TotalCalls)
	}

	bp.stats.LastCall = time.Now()
}

// IsAvailable checks if the provider is available (circuit breaker check)
func (bp *BaseProvider) IsAvailable(ctx context.Context) bool {
	return bp.circuitBreaker.IsAvailable()
}

// ExecuteWithRetry executes a function with retry logic
func (bp *BaseProvider) ExecuteWithRetry(ctx context.Context, fn func() (*LLMResponse, error)) (*LLMResponse, error) {
	var lastErr error

	for attempt := 0; attempt <= bp.config.MaxRetries; attempt++ {
		if attempt > 0 {
			// Wait before retry
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(bp.config.RetryDelay * time.Duration(attempt)):
			}
		}

		// Check circuit breaker
		if !bp.circuitBreaker.IsAvailable() {
			return nil, fmt.Errorf("circuit breaker is open for provider %s", bp.config.Name)
		}

		// Execute function
		response, err := fn()
		if err == nil {
			bp.circuitBreaker.RecordSuccess()
			return response, nil
		}

		lastErr = err
		bp.circuitBreaker.RecordFailure()

		// Check if error is retryable
		if !isRetryableError(err) {
			break
		}
	}

	return nil, fmt.Errorf("max retries exceeded for provider %s: %w", bp.config.Name, lastErr)
}

// isRetryableError checks if an error is retryable
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Add logic to determine if error is retryable
	// For now, consider most errors retryable except context errors
	errStr := err.Error()

	// Don't retry context errors
	if errStr == "context canceled" || errStr == "context deadline exceeded" {
		return false
	}

	// Don't retry authentication errors
	if contains(errStr, "unauthorized") || contains(errStr, "forbidden") || contains(errStr, "invalid api key") {
		return false
	}

	// Retry rate limit, timeout, and server errors
	return true
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || containsMiddle(s, substr)))
}

func containsMiddle(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// ValidateRequest validates an LLM request
func ValidateRequest(request *LLMRequest) error {
	if request == nil {
		return fmt.Errorf("request cannot be nil")
	}

	if request.Prompt == "" {
		return fmt.Errorf("prompt cannot be empty")
	}

	if request.Temperature < 0 || request.Temperature > 2 {
		return fmt.Errorf("temperature must be between 0 and 2")
	}

	if request.MaxTokens < 1 {
		return fmt.Errorf("max_tokens must be positive")
	}

	return nil
}

// SanitizePrompt sanitizes a prompt to prevent injection attacks
func SanitizePrompt(prompt string) string {
	// Remove potentially dangerous patterns
	// This is a basic implementation - enhance as needed

	// For now, just return the prompt
	// In production, you might want to:
	// - Remove or escape special characters
	// - Check for prompt injection patterns
	// - Limit prompt length

	return prompt
}
