package gct

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRetryLogic_5xxErrors tests retry on 5xx errors
func TestRetryLogic_5xxErrors(t *testing.T) {
	testCases := []struct {
		name            string
		statusCode      int
		expectedRetries int
	}{
		{
			name:            "500_InternalServerError",
			statusCode:      500,
			expectedRetries: 3, // Initial + 2 retries
		},
		{
			name:            "502_BadGateway",
			statusCode:      502,
			expectedRetries: 3,
		},
		{
			name:            "503_ServiceUnavailable",
			statusCode:      503,
			expectedRetries: 3,
		},
		{
			name:            "504_GatewayTimeout",
			statusCode:      504,
			expectedRetries: 3,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var requestCount int32

			// Create test server that returns error
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&requestCount, 1)
				w.WriteHeader(tc.statusCode)
				w.Write([]byte(`{"code":"500000","msg":"Internal server error"}`))
			}))
			defer server.Close()

			// Create client
			client := NewKuCoinRESTClient()
			client.futuresURL = server.URL
			client.SetCredentials("test-key", "test-secret", "test-password")

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			// Try to get positions (will fail and retry)
			_, err := client.GetPositions(ctx)

			// Should have retried
			assert.Error(t, err, "Should return error after retries")
			assert.GreaterOrEqual(t, int(atomic.LoadInt32(&requestCount)), tc.expectedRetries,
				"Should have retried at least %d times", tc.expectedRetries)

			t.Logf("Request count: %d (expected at least %d)", requestCount, tc.expectedRetries)
		})
	}
}

// TestRetryLogic_ExponentialBackoff tests exponential backoff timing
func TestRetryLogic_ExponentialBackoff(t *testing.T) {
	var requestCount int32
	var requestTimes []time.Time

	// Create test server that always returns 500
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		requestTimes = append(requestTimes, time.Now())
		w.WriteHeader(500)
		w.Write([]byte(`{"code":"500000","msg":"Internal server error"}`))
	}))
	defer server.Close()

	// Create client
	client := NewKuCoinRESTClient()
	client.futuresURL = server.URL
	client.SetCredentials("test-key", "test-secret", "test-password")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	startTime := time.Now()

	// Try to get positions (will fail and retry with backoff)
	_, err := client.GetPositions(ctx)

	totalDuration := time.Since(startTime)

	// Should have retried
	assert.Error(t, err, "Should return error after retries")
	assert.GreaterOrEqual(t, int(atomic.LoadInt32(&requestCount)), 3, "Should have retried at least 3 times")

	// Check backoff timing (should be approximately 1s + 2s + 4s = 7s minimum)
	// We allow some tolerance for execution time
	assert.Greater(t, totalDuration, 5*time.Second, "Should have waited for backoff")

	t.Logf("Total duration: %v", totalDuration)
	t.Logf("Request count: %d", requestCount)

	// Log time between requests
	for i := 1; i < len(requestTimes); i++ {
		delay := requestTimes[i].Sub(requestTimes[i-1])
		t.Logf("Delay between request %d and %d: %v", i, i+1, delay)
	}
}

// TestRetryLogic_RateLimiting tests rate limiting with Retry-After header
func TestRetryLogic_RateLimiting(t *testing.T) {
	var requestCount int32
	retryAfterSeconds := 2

	// Create test server that returns 429 with Retry-After header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&requestCount, 1)

		if count == 1 {
			// First request: return 429 with Retry-After
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(429)
			w.Write([]byte(`{"code":"429000","msg":"Too many requests"}`))
		} else {
			// Subsequent requests: return success
			w.WriteHeader(200)
			w.Write([]byte(`{"code":"200000","data":[]}`))
		}
	}))
	defer server.Close()

	// Create client
	client := NewKuCoinRESTClient()
	client.futuresURL = server.URL
	client.SetCredentials("test-key", "test-secret", "test-password")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	startTime := time.Now()

	// Try to get positions (should retry after rate limit)
	_, err := client.GetPositions(ctx)

	duration := time.Since(startTime)

	// Should succeed after retry
	assert.NoError(t, err, "Should succeed after respecting Retry-After")
	assert.Equal(t, int32(2), atomic.LoadInt32(&requestCount), "Should have made 2 requests")

	// Should have waited at least the Retry-After duration
	assert.GreaterOrEqual(t, duration, time.Duration(retryAfterSeconds)*time.Second,
		"Should have waited for Retry-After duration")

	t.Logf("Duration: %v (expected at least %ds)", duration, retryAfterSeconds)
	t.Logf("Request count: %d", requestCount)
}

// TestRetryLogic_NoRetryOn4xx tests that 4xx errors are not retried (except 429)
func TestRetryLogic_NoRetryOn4xx(t *testing.T) {
	testCases := []struct {
		name        string
		statusCode  int
		shouldRetry bool
	}{
		{
			name:        "400_BadRequest",
			statusCode:  400,
			shouldRetry: false,
		},
		{
			name:        "401_Unauthorized",
			statusCode:  401,
			shouldRetry: false,
		},
		{
			name:        "403_Forbidden",
			statusCode:  403,
			shouldRetry: false,
		},
		{
			name:        "404_NotFound",
			statusCode:  404,
			shouldRetry: false,
		},
		{
			name:        "429_TooManyRequests",
			statusCode:  429,
			shouldRetry: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var requestCount int32

			// Create test server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&requestCount, 1)
				w.WriteHeader(tc.statusCode)
				w.Write([]byte(`{"code":"400000","msg":"Client error"}`))
			}))
			defer server.Close()

			// Create client
			client := NewKuCoinRESTClient()
			client.futuresURL = server.URL
			client.SetCredentials("test-key", "test-secret", "test-password")

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			// Try to get positions
			_, err := client.GetPositions(ctx)

			// Should return error
			assert.Error(t, err, "Should return error")

			if tc.shouldRetry {
				// 429 should be retried
				assert.GreaterOrEqual(t, int(atomic.LoadInt32(&requestCount)), 2,
					"Should have retried for 429")
			} else {
				// Other 4xx should not be retried
				assert.Equal(t, int32(1), atomic.LoadInt32(&requestCount),
					"Should not retry for %d", tc.statusCode)
			}

			t.Logf("Request count: %d (retry: %v)", requestCount, tc.shouldRetry)
		})
	}
}

// TestRetryLogic_MaxRetries tests that retries stop after max attempts
func TestRetryLogic_MaxRetries(t *testing.T) {
	var requestCount int32
	maxRetries := 3

	// Create test server that always fails
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		w.WriteHeader(500)
		w.Write([]byte(`{"code":"500000","msg":"Internal server error"}`))
	}))
	defer server.Close()

	// Create client
	client := NewKuCoinRESTClient()
	client.futuresURL = server.URL
	client.SetCredentials("test-key", "test-secret", "test-password")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Try to get positions
	_, err := client.GetPositions(ctx)

	// Should return error after max retries
	assert.Error(t, err, "Should return error after max retries")
	assert.Contains(t, err.Error(), "max retries", "Error should mention max retries")

	// Should have made exactly maxRetries + 1 requests (initial + retries)
	assert.Equal(t, int32(maxRetries+1), atomic.LoadInt32(&requestCount),
		"Should have made exactly %d requests", maxRetries+1)

	t.Logf("Request count: %d (expected %d)", requestCount, maxRetries+1)
}

// TestRetryLogic_ContextCancellation tests that retries stop on context cancellation
func TestRetryLogic_ContextCancellation(t *testing.T) {
	var requestCount int32

	// Create test server that always fails
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		time.Sleep(100 * time.Millisecond) // Simulate slow response
		w.WriteHeader(500)
		w.Write([]byte(`{"code":"500000","msg":"Internal server error"}`))
	}))
	defer server.Close()

	// Create client
	client := NewKuCoinRESTClient()
	client.futuresURL = server.URL
	client.SetCredentials("test-key", "test-secret", "test-password")

	// Create context that will be cancelled
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel context after first request
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	// Try to get positions
	_, err := client.GetPositions(ctx)

	// Should return error due to context cancellation
	assert.Error(t, err, "Should return error")

	// Should not have made many retries (context was cancelled)
	assert.LessOrEqual(t, int(atomic.LoadInt32(&requestCount)), 2,
		"Should have stopped retrying after context cancellation")

	t.Logf("Request count: %d (stopped due to context cancellation)", requestCount)
}

// TestRetryLogic_SuccessAfterRetry tests successful request after retry
func TestRetryLogic_SuccessAfterRetry(t *testing.T) {
	var requestCount int32

	// Create test server that fails first time, then succeeds
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&requestCount, 1)

		if count <= 2 {
			// First two requests fail
			w.WriteHeader(500)
			w.Write([]byte(`{"code":"500000","msg":"Internal server error"}`))
		} else {
			// Third request succeeds
			w.WriteHeader(200)
			w.Write([]byte(`{"code":"200000","data":[]}`))
		}
	}))
	defer server.Close()

	// Create client
	client := NewKuCoinRESTClient()
	client.futuresURL = server.URL
	client.SetCredentials("test-key", "test-secret", "test-password")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Try to get positions
	positions, err := client.GetPositions(ctx)

	// Should succeed after retries
	require.NoError(t, err, "Should succeed after retries")
	assert.NotNil(t, positions, "Positions should not be nil")
	assert.Equal(t, int32(3), atomic.LoadInt32(&requestCount), "Should have made 3 requests")

	t.Logf("Request count: %d (succeeded on attempt 3)", requestCount)
}
