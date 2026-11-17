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

func TestNewOpenAIProvider(t *testing.T) {
	config := &ProviderConfig{
		Name:           "openai",
		APIKey:         "test-key",
		Model:          "gpt-4",
		TimeoutSeconds: 10,
		MaxRetries:     3,
		RetryDelay:     1 * time.Second,
		Temperature:    0.7,
		MaxTokens:      1000,
	}

	provider := NewOpenAIProvider(config)

	assert.NotNil(t, provider)
	assert.Equal(t, "openai", provider.Name())
	assert.NotEmpty(t, provider.config.Endpoint)
	assert.Greater(t, provider.config.CostPerInputK, 0.0)
	assert.Greater(t, provider.config.CostPerOutputK, 0.0)
}

func TestOpenAIProvider_Call_Success(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Contains(t, r.Header.Get("Authorization"), "Bearer")

		// Return mock response
		response := OpenAIResponse{
			ID:      "test-id",
			Model:   "gpt-4",
			Created: time.Now().Unix(),
			Choices: []struct {
				Index   int `json:"index"`
				Message struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			}{
				{
					Index: 0,
					Message: struct {
						Role    string `json:"role"`
						Content string `json:"content"`
					}{
						Role:    "assistant",
						Content: `{"action":"BUY","confidence":0.75,"reasoning":"Strong bullish signals"}`,
					},
					FinishReason: "stop",
				},
			},
			Usage: struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				TotalTokens      int `json:"total_tokens"`
			}{
				PromptTokens:     100,
				CompletionTokens: 50,
				TotalTokens:      150,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	// Create provider with mock server
	config := &ProviderConfig{
		Name:           "openai",
		APIKey:         "test-key",
		Model:          "gpt-4",
		Endpoint:       server.URL,
		TimeoutSeconds: 10,
		MaxRetries:     1,
		RetryDelay:     100 * time.Millisecond,
		Temperature:    0.7,
		MaxTokens:      1000,
	}

	provider := NewOpenAIProvider(config)

	// Make call
	ctx := context.Background()
	request := &LLMRequest{
		Prompt:      "Test prompt",
		Temperature: 0.7,
		MaxTokens:   1000,
		Model:       "gpt-4",
	}

	response, err := provider.Call(ctx, request)

	require.NoError(t, err)
	assert.NotNil(t, response)
	assert.Equal(t, "openai", response.Provider)
	assert.Equal(t, "gpt-4", response.Model)
	assert.Equal(t, 100, response.InputTokens)
	assert.Equal(t, 50, response.OutputTokens)
	assert.Greater(t, response.Cost, 0.0)
	assert.Contains(t, response.Content, "BUY")
}

func TestOpenAIProvider_Call_InvalidRequest(t *testing.T) {
	config := &ProviderConfig{
		Name:           "openai",
		APIKey:         "test-key",
		Model:          "gpt-4",
		TimeoutSeconds: 10,
	}

	provider := NewOpenAIProvider(config)
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

func TestOpenAIProvider_Call_APIError(t *testing.T) {
	// Create mock server that returns error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"Invalid API key"}}`))
	}))
	defer server.Close()

	config := &ProviderConfig{
		Name:           "openai",
		APIKey:         "invalid-key",
		Model:          "gpt-4",
		Endpoint:       server.URL,
		TimeoutSeconds: 10,
		MaxRetries:     1,
		RetryDelay:     100 * time.Millisecond,
	}

	provider := NewOpenAIProvider(config)
	ctx := context.Background()

	request := &LLMRequest{
		Prompt:      "Test prompt",
		Temperature: 0.7,
		MaxTokens:   1000,
	}

	_, err := provider.Call(ctx, request)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

func TestParseMarketAnalysis_Success(t *testing.T) {
	response := &LLMResponse{
		Content: `{
			"action": "BUY",
			"confidence": 0.85,
			"reasoning": "Strong bullish momentum with RSI oversold",
			"entry_price": 100.50,
			"stop_loss": 98.00,
			"take_profit": 105.00,
			"risk_assessment": "medium",
			"market_condition": "bullish trend",
			"key_factors": ["RSI oversold", "MACD bullish crossover"],
			"warnings": ["High volatility"]
		}`,
		Provider: "openai",
		Model:    "gpt-4",
	}

	analysis, err := ParseMarketAnalysis(response)

	require.NoError(t, err)
	assert.Equal(t, "BUY", analysis.Action)
	assert.Equal(t, 0.85, analysis.Confidence)
	assert.Equal(t, "Strong bullish momentum with RSI oversold", analysis.Reasoning)
	assert.Equal(t, 100.50, analysis.EntryPrice)
	assert.Equal(t, 98.00, analysis.StopLoss)
	assert.Equal(t, 105.00, analysis.TakeProfit)
	assert.Equal(t, "medium", analysis.RiskAssessment)
	assert.Equal(t, "bullish trend", analysis.MarketCondition)
	assert.Len(t, analysis.KeyFactors, 2)
	assert.Len(t, analysis.Warnings, 1)
}

func TestParseMarketAnalysis_WithMarkdown(t *testing.T) {
	response := &LLMResponse{
		Content: "```json\n" + `{
			"action": "SELL",
			"confidence": 0.70,
			"reasoning": "Bearish signals detected"
		}` + "\n```",
		Provider: "openai",
		Model:    "gpt-4",
	}

	analysis, err := ParseMarketAnalysis(response)

	require.NoError(t, err)
	assert.Equal(t, "SELL", analysis.Action)
	assert.Equal(t, 0.70, analysis.Confidence)
}

func TestParseMarketAnalysis_InvalidJSON(t *testing.T) {
	response := &LLMResponse{
		Content:  "This is not JSON",
		Provider: "openai",
		Model:    "gpt-4",
	}

	_, err := ParseMarketAnalysis(response)
	assert.Error(t, err)
}

func TestOpenAIProvider_GetCost(t *testing.T) {
	config := &ProviderConfig{
		Name:           "openai",
		CostPerInputK:  0.03,
		CostPerOutputK: 0.06,
	}

	provider := NewOpenAIProvider(config)

	// Test cost calculation
	cost := provider.GetCost(1000, 500)
	expected := (1000.0 / 1000.0 * 0.03) + (500.0 / 1000.0 * 0.06)
	assert.Equal(t, expected, cost)
}

func TestOpenAIProvider_Stats(t *testing.T) {
	config := &ProviderConfig{
		Name:           "openai",
		APIKey:         "test-key",
		Model:          "gpt-4",
		TimeoutSeconds: 10,
	}

	provider := NewOpenAIProvider(config)

	// Initial stats
	stats := provider.GetStats()
	assert.Equal(t, "openai", stats.Provider)
	assert.Equal(t, int64(0), stats.TotalCalls)

	// Update stats
	provider.UpdateStats(true, 0.05, 150, 1*time.Second, nil)

	stats = provider.GetStats()
	assert.Equal(t, int64(1), stats.TotalCalls)
	assert.Equal(t, int64(1), stats.SuccessfulCalls)
	assert.Equal(t, 0.05, stats.TotalCost)
	assert.Equal(t, int64(150), stats.TotalTokens)
}
