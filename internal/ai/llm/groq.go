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

// GroqProvider implements the Provider interface for Groq AI
// Groq uses an OpenAI-compatible API with blazing fast inference
type GroqProvider struct {
	*BaseProvider
	httpClient *http.Client
}

// NewGroqProvider creates a new Groq provider
func NewGroqProvider(config *ProviderConfig) *GroqProvider {
	if config.Endpoint == "" {
		config.Endpoint = "https://api.groq.com/openai/v1/chat/completions"
	}

	if config.CostPerInputK == 0 {
		// Groq pricing: llama-3.3-70b-versatile is free/very cheap
		config.CostPerInputK = 0.00059  // $0.59 per 1M input tokens
		config.CostPerOutputK = 0.00079 // $0.79 per 1M output tokens
	}

	return &GroqProvider{
		BaseProvider: NewBaseProvider(config),
		httpClient: &http.Client{
			Timeout: time.Duration(config.TimeoutSeconds) * time.Second,
		},
	}
}

// Call makes a call to Groq API
func (p *GroqProvider) Call(ctx context.Context, request *LLMRequest) (*LLMResponse, error) {
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

// callAPI makes the actual API call to Groq
func (p *GroqProvider) callAPI(ctx context.Context, prompt string, request *LLMRequest) (*LLMResponse, error) {
	// Build request body — same format as OpenAI
	reqBody := map[string]interface{}{
		"model": p.config.Model,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "You are an expert cryptocurrency trading analyst. Provide clear, actionable analysis based on technical indicators and market data. Always respond in valid JSON.",
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

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.config.APIKey)

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
		return nil, fmt.Errorf("Groq API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response — same structure as OpenAI
	var apiResp GroqResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Extract content
	if len(apiResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in Groq response")
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

// GroqResponse represents the response from Groq API (OpenAI-compatible format)
type GroqResponse struct {
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
