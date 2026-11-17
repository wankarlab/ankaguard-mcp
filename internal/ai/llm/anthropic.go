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

// AnthropicProvider implements the Provider interface for Anthropic Claude
type AnthropicProvider struct {
	*BaseProvider
	httpClient *http.Client
}

// NewAnthropicProvider creates a new Anthropic provider
func NewAnthropicProvider(config *ProviderConfig) *AnthropicProvider {
	if config.Endpoint == "" {
		config.Endpoint = "https://api.anthropic.com/v1/messages"
	}

	if config.CostPerInputK == 0 {
		// Default costs for Claude 3 Opus (update based on actual pricing)
		// Claude 3 Opus: $15 per 1M input tokens, $75 per 1M output tokens
		config.CostPerInputK = 0.015  // $0.015 per 1K input tokens
		config.CostPerOutputK = 0.075 // $0.075 per 1K output tokens
	}

	return &AnthropicProvider{
		BaseProvider: NewBaseProvider(config),
		httpClient: &http.Client{
			Timeout: time.Duration(config.TimeoutSeconds) * time.Second,
		},
	}
}

// Name returns the provider name
func (p *AnthropicProvider) Name() string {
	return "anthropic"
}

// Call makes a call to Anthropic API
func (p *AnthropicProvider) Call(ctx context.Context, request *LLMRequest) (*LLMResponse, error) {
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

// callAPI makes the actual API call to Anthropic
func (p *AnthropicProvider) callAPI(ctx context.Context, prompt string, request *LLMRequest) (*LLMResponse, error) {
	// Build request body for Claude API
	reqBody := map[string]interface{}{
		"model": p.config.Model,
		"messages": []map[string]string{
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"max_tokens": request.MaxTokens,
		"system":     "You are an expert cryptocurrency trading analyst. Provide clear, actionable analysis based on technical indicators and market data.",
	}

	// Add temperature if specified
	if request.Temperature > 0 {
		reqBody["temperature"] = request.Temperature
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

	// Set headers for Anthropic API
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.config.APIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

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

	// Parse response
	var apiResp AnthropicResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Extract content
	if len(apiResp.Content) == 0 {
		return nil, fmt.Errorf("no content in response")
	}

	content := apiResp.Content[0].Text
	inputTokens := apiResp.Usage.InputTokens
	outputTokens := apiResp.Usage.OutputTokens

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

// AnthropicResponse represents the response from Anthropic API
type AnthropicResponse struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Role    string `json:"role"`
	Model   string `json:"model"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}
