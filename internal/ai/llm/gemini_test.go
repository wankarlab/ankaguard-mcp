package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewGeminiProvider(t *testing.T) {
	config := &ProviderConfig{
		Name:           "gemini",
		APIKey:         "test-key",
		TimeoutSeconds: 10,
		MaxRetries:     3,
		RetryDelay:     1 * time.Second,
		Temperature:    0.7,
		MaxTokens:      1000,
	}

	provider := NewGeminiProvider(config)

	assert.NotNil(t, provider)
	assert.Equal(t, "gemini", provider.Name())
	assert.NotEmpty(t, provider.config.Endpoint)
	assert.Equal(t, "gemini-2.0-flash", provider.config.Model)
	assert.Greater(t, provider.config.CostPerInputK, 0.0)
	assert.Greater(t, provider.config.CostPerOutputK, 0.0)
	// Verify Gemini is cheaper than OpenAI
	assert.Less(t, provider.config.CostPerInputK, 0.01)
}

func TestGeminiProvider_Call_Success(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.NotContains(t, r.URL.String(), "key=", "API key must not leak into the URL/query string")
		assert.NotEmpty(t, r.Header.Get("x-goog-api-key"), "API key must be sent via the x-goog-api-key header")

		// Return mock response
		response := GeminiResponse{
			Candidates: []struct {
				Content struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
					Role string `json:"role"`
				} `json:"content"`
				FinishReason  string `json:"finishReason"`
				Index         int    `json:"index"`
				SafetyRatings []struct {
					Category    string `json:"category"`
					Probability string `json:"probability"`
				} `json:"safetyRatings"`
			}{
				{
					Content: struct {
						Parts []struct {
							Text string `json:"text"`
						} `json:"parts"`
						Role string `json:"role"`
					}{
						Parts: []struct {
							Text string `json:"text"`
						}{
							{
								Text: `{"action":"BUY","confidence":0.80,"reasoning":"Bullish momentum detected"}`,
							},
						},
						Role: "model",
					},
					FinishReason: "STOP",
					Index:        0,
				},
			},
			UsageMetadata: &struct {
				PromptTokenCount     int `json:"promptTokenCount"`
				CandidatesTokenCount int `json:"candidatesTokenCount"`
				TotalTokenCount      int `json:"totalTokenCount"`
			}{
				PromptTokenCount:     120,
				CandidatesTokenCount: 45,
				TotalTokenCount:      165,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	// Create provider with mock server
	config := &ProviderConfig{
		Name:           "gemini",
		APIKey:         "test-key",
		Model:          "gemini-2.0-flash",
		Endpoint:       server.URL,
		TimeoutSeconds: 10,
		MaxRetries:     1,
		RetryDelay:     100 * time.Millisecond,
		Temperature:    0.7,
		MaxTokens:      1000,
	}

	provider := NewGeminiProvider(config)

	// Make call
	ctx := context.Background()
	request := &LLMRequest{
		Prompt:      "Test prompt",
		Temperature: 0.7,
		MaxTokens:   1000,
		Model:       "gemini-2.0-flash",
	}

	response, err := provider.Call(ctx, request)

	require.NoError(t, err)
	assert.NotNil(t, response)
	assert.Equal(t, "gemini", response.Provider)
	assert.Equal(t, "gemini-2.0-flash", response.Model)
	assert.Equal(t, 120, response.InputTokens)
	assert.Equal(t, 45, response.OutputTokens)
	assert.Greater(t, response.Cost, 0.0)
	// Verify cost is low (Gemini is cheap)
	assert.Less(t, response.Cost, 0.01)
	assert.Contains(t, response.Content, "BUY")
}

func TestGeminiProvider_Call_InvalidRequest(t *testing.T) {
	config := &ProviderConfig{
		Name:           "gemini",
		APIKey:         "test-key",
		Model:          "gemini-2.0-flash",
		TimeoutSeconds: 10,
	}

	provider := NewGeminiProvider(config)
	ctx := context.Background()

	// Test with nil request
	_, err := provider.Call(ctx, nil)
	assert.Error(t, err)

	// Test with empty prompt
	request := &LLMRequest{
		Prompt:      "",
		Temperature: 0.7,
		MaxTokens:   1000,
	}
	_, err = provider.Call(ctx, request)
	assert.Error(t, err)
}

func TestGeminiProvider_Call_APIError(t *testing.T) {
	// Create mock server that returns error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"Invalid API key"}}`))
	}))
	defer server.Close()

	config := &ProviderConfig{
		Name:           "gemini",
		APIKey:         "invalid-key",
		Model:          "gemini-2.0-flash",
		Endpoint:       server.URL,
		TimeoutSeconds: 10,
		MaxRetries:     1,
		RetryDelay:     100 * time.Millisecond,
	}

	provider := NewGeminiProvider(config)
	ctx := context.Background()

	request := &LLMRequest{
		Prompt:      "Test prompt",
		Temperature: 0.7,
		MaxTokens:   1000,
	}

	_, err := provider.Call(ctx, request)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "400")
}

func TestGeminiProvider_GetCost(t *testing.T) {
	config := &ProviderConfig{
		Name:           "gemini",
		CostPerInputK:  0.00125,
		CostPerOutputK: 0.00375,
	}

	provider := NewGeminiProvider(config)

	// Test cost calculation
	cost := provider.GetCost(1000, 500)
	expected := (1000.0 / 1000.0 * 0.00125) + (500.0 / 1000.0 * 0.00375)
	assert.Equal(t, expected, cost)

	// Verify it's much cheaper than OpenAI
	assert.Less(t, cost, 0.01)
}

func TestGeminiProvider_CostComparison(t *testing.T) {
	// Compare Gemini vs OpenAI costs
	geminiConfig := &ProviderConfig{
		Name:           "gemini",
		CostPerInputK:  0.00125,
		CostPerOutputK: 0.00375,
	}

	openaiConfig := &ProviderConfig{
		Name:           "openai",
		CostPerInputK:  0.03,
		CostPerOutputK: 0.06,
	}

	geminiProvider := NewGeminiProvider(geminiConfig)
	openaiProvider := NewOpenAIProvider(openaiConfig)

	// Same token usage
	inputTokens := 500
	outputTokens := 200

	geminiCost := geminiProvider.GetCost(inputTokens, outputTokens)
	openaiCost := openaiProvider.GetCost(inputTokens, outputTokens)

	// Gemini should be significantly cheaper
	assert.Less(t, geminiCost, openaiCost)

	// Calculate savings
	savings := ((openaiCost - geminiCost) / openaiCost) * 100
	assert.Greater(t, savings, 90.0) // At least 90% cheaper

	t.Logf("Cost comparison for %d input + %d output tokens:", inputTokens, outputTokens)
	t.Logf("  Gemini: $%.6f", geminiCost)
	t.Logf("  OpenAI: $%.6f", openaiCost)
	t.Logf("  Savings: %.1f%%", savings)
}

func TestGeminiProvider_Stats(t *testing.T) {
	config := &ProviderConfig{
		Name:           "gemini",
		APIKey:         "test-key",
		Model:          "gemini-2.0-flash",
		TimeoutSeconds: 10,
	}

	provider := NewGeminiProvider(config)

	// Initial stats
	stats := provider.GetStats()
	assert.Equal(t, "gemini", stats.Provider)
	assert.Equal(t, int64(0), stats.TotalCalls)

	// Update stats
	provider.UpdateStats(true, 0.002, 165, 800*time.Millisecond, nil)

	stats = provider.GetStats()
	assert.Equal(t, int64(1), stats.TotalCalls)
	assert.Equal(t, int64(1), stats.SuccessfulCalls)
	assert.Equal(t, 0.002, stats.TotalCost)
	assert.Equal(t, int64(165), stats.TotalTokens)
}

func TestGeminiProvider_EmptyResponse(t *testing.T) {
	// Create mock server with empty candidates
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := GeminiResponse{
			Candidates: []struct {
				Content struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
					Role string `json:"role"`
				} `json:"content"`
				FinishReason  string `json:"finishReason"`
				Index         int    `json:"index"`
				SafetyRatings []struct {
					Category    string `json:"category"`
					Probability string `json:"probability"`
				} `json:"safetyRatings"`
			}{},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	config := &ProviderConfig{
		Name:           "gemini",
		APIKey:         "test-key",
		Endpoint:       server.URL,
		TimeoutSeconds: 10,
		MaxRetries:     1,
	}

	provider := NewGeminiProvider(config)
	ctx := context.Background()

	request := &LLMRequest{
		Prompt:      "Test",
		Temperature: 0.7,
		MaxTokens:   100,
	}

	_, err := provider.Call(ctx, request)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no candidates")
}
