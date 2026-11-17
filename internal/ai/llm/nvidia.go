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

// NVIDIAProvider implements the Provider interface for NVIDIA NIM.
// NVIDIA's hosted inference API (build.nvidia.com / integrate.api.nvidia.com)
// is OpenAI-compatible, so this mirrors the OpenRouter provider: same request
// and response schema, only the endpoint, auth key and default model differ.
type NVIDIAProvider struct {
	*BaseProvider
	httpClient *http.Client
}

// NewNVIDIAProvider creates a new NVIDIA NIM provider
func NewNVIDIAProvider(config *ProviderConfig) *NVIDIAProvider {
	if config.Endpoint == "" {
		config.Endpoint = "https://integrate.api.nvidia.com/v1/chat/completions"
	}

	if config.Model == "" {
		// meta/llama-3.3-70b-instruct is hosted on NVIDIA NIM and OpenAI-compatible.
		config.Model = "meta/llama-3.3-70b-instruct"
	}

	// NVIDIA's hosted catalog (build.nvidia.com) is free within the credit tier;
	// leave the cost at zero unless explicitly configured so the cost monitor
	// does not fabricate charges.

	return &NVIDIAProvider{
		BaseProvider: NewBaseProvider(config),
		httpClient: &http.Client{
			Timeout: time.Duration(config.TimeoutSeconds) * time.Second,
		},
	}
}

// Name returns the provider name
func (p *NVIDIAProvider) Name() string {
	return "nvidia"
}

// Call makes a call to the NVIDIA NIM API
func (p *NVIDIAProvider) Call(ctx context.Context, request *LLMRequest) (*LLMResponse, error) {
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

// callAPI makes the actual API call to NVIDIA NIM (OpenAI-compatible format)
func (p *NVIDIAProvider) callAPI(ctx context.Context, prompt string, request *LLMRequest) (*LLMResponse, error) {
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

	// Set headers — NVIDIA NIM uses the OpenAI-compatible Bearer scheme
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
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
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response (OpenAI-compatible format)
	var apiResp NVIDIAResponse
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

	// Calculate cost (zero on the free tier unless costs are configured)
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

// NVIDIAResponse represents the response from the NVIDIA NIM API.
// Uses the OpenAI-compatible chat-completions schema.
type NVIDIAResponse struct {
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

// NVIDIA NIM model options for reference (build.nvidia.com catalog):
//   - meta/llama-3.3-70b-instruct           (balanced, default)
//   - meta/llama-3.1-405b-instruct          (most powerful)
//   - nvidia/llama-3.1-nemotron-70b-instruct
//   - deepseek-ai/deepseek-r1               (reasoning)
//   - mistralai/mixtral-8x22b-instruct-v0.1
// Override via the NVIDIA_MODEL environment variable.
