package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// OpenRouterProvider implements the Provider interface for OpenRouter
// OpenRouter provides unified access to multiple LLM providers
type OpenRouterProvider struct {
	*BaseProvider
	httpClient *http.Client
}

// NewOpenRouterProvider creates a new OpenRouter provider
func NewOpenRouterProvider(config *ProviderConfig) *OpenRouterProvider {
	if config.Endpoint == "" {
		config.Endpoint = "https://openrouter.ai/api/v1/chat/completions"
	}

	if config.CostPerInputK == 0 {
		// Default costs - varies by model selected via OpenRouter
		// These are approximate costs for google/gemini-2.0-flash-001
		config.CostPerInputK = 0.000075
		config.CostPerOutputK = 0.0003
	}

	if config.Model == "" {
		// google/gemini-2.0-flash-001 returns 404 (no endpoints) with current keys.
		// meta-llama/llama-3.3-70b-instruct is verified working and OpenAI-compatible.
		config.Model = "meta-llama/llama-3.3-70b-instruct"
	}

	return &OpenRouterProvider{
		BaseProvider: NewBaseProvider(config),
		httpClient: &http.Client{
			Timeout: time.Duration(config.TimeoutSeconds) * time.Second,
		},
	}
}

// Name returns the provider name
func (p *OpenRouterProvider) Name() string {
	return "openrouter"
}

// Call makes a call to OpenRouter API
func (p *OpenRouterProvider) Call(ctx context.Context, request *LLMRequest) (*LLMResponse, error) {
	startTime := time.Now()

	// Validate request
	if err := ValidateRequest(request); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Sanitize prompt
	sanitizedPrompt := SanitizePrompt(request.Prompt)

	// Execute with retry
	response, err := p.ExecuteWithRetry(ctx, func() (*LLMResponse, error) {
		return p.callAPI(ctx, sanitizedPrompt, request)
	})

	latency := time.Since(startTime)

	// Update stats
	if response != nil {
		totalTokens := int64(response.InputTokens + response.OutputTokens)
		p.UpdateStats(true, response.Cost, totalTokens, latency, nil)
	} else {
		p.UpdateStats(false, 0, 0, latency, err)
	}

	return response, err
}

// callAPI makes the actual API call to OpenRouter
func (p *OpenRouterProvider) callAPI(ctx context.Context, prompt string, request *LLMRequest) (*LLMResponse, error) {
	// OpenRouter uses the OpenAI-compatible API format
	reqBody := map[string]interface{}{
		"model": p.config.Model,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "You are an expert cryptocurrency trading analyst. Provide clear, actionable analysis based on technical indicators and market data. Always respond in valid JSON format.",
			},
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"temperature": request.Temperature,
		"max_tokens":  request.MaxTokens,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.config.Endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers - OpenRouter specific
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	httpReq.Header.Set("HTTP-Referer", "https://trading-mcp.rantipay.com")
	httpReq.Header.Set("X-Title", "RantiPay Trading MCP")

	// Make request
	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response (OpenAI-compatible format)
	var apiResp OpenRouterResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Extract content
	if len(apiResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	content := apiResp.Choices[0].Message.Content
	inputTokens := apiResp.Usage.PromptTokens
	outputTokens := apiResp.Usage.CompletionTokens

	// Calculate cost
	cost := p.GetCost(inputTokens, outputTokens)

	return &LLMResponse{
		Content:      content,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		Model:        apiResp.Model,
		Provider:     p.Name(),
		Cost:         cost,
		Cached:       false,
		Timestamp:    time.Now(),
	}, nil
}

// OpenRouterResponse represents the response from OpenRouter API
// Uses OpenAI-compatible format
type OpenRouterResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// OpenRouter model options for reference
// These are some popular models available through OpenRouter:
//
// Google Models:
// - google/gemini-2.0-flash-001 (fast, cost-effective)
// - google/gemini-2.0-flash-thinking-exp (reasoning)
// - google/gemini-2.5-flash-preview (latest)
//
// OpenAI Models:
// - openai/gpt-4o-mini (cost-effective)
// - openai/gpt-4o (powerful)
// - openai/gpt-4-turbo (fast, powerful)
//
// Anthropic Models:
// - anthropic/claude-3-haiku (fast, cost-effective)
// - anthropic/claude-3-sonnet (balanced)
// - anthropic/claude-3-opus (most powerful)
// - anthropic/claude-3.5-sonnet (latest)
//
// Meta Models:
// - meta-llama/llama-3.1-70b-instruct (powerful, open)
// - meta-llama/llama-3.1-8b-instruct (fast, open)
//
// Mistral Models:
// - mistralai/mixtral-8x7b-instruct (balanced)
// - mistralai/mistral-large (powerful)
