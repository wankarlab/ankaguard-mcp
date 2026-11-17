package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

// GeminiProvider implements the Provider interface for Google Gemini
type GeminiProvider struct {
	*BaseProvider
	httpClient *http.Client
	logger     *logrus.Logger
}

// NewGeminiProvider creates a new Gemini provider
func NewGeminiProvider(config *ProviderConfig) *GeminiProvider {
	if config.Endpoint == "" {
		// Gemini API endpoint - will be appended with model and API key
		config.Endpoint = "https://generativelanguage.googleapis.com/v1beta/models"
	}

	if config.CostPerInputK == 0 {
		// Gemini Pro pricing (much cheaper than GPT-4!)
		config.CostPerInputK = 0.00125  // $0.00125 per 1K input tokens
		config.CostPerOutputK = 0.00375 // $0.00375 per 1K output tokens
	}

	if config.Model == "" {
		config.Model = "gemini-2.0-flash"
	}

	return &GeminiProvider{
		BaseProvider: NewBaseProvider(config),
		httpClient: &http.Client{
			Timeout: time.Duration(config.TimeoutSeconds) * time.Second,
		},
		logger: logrus.New(),
	}
}

// Call makes a call to Gemini API
func (p *GeminiProvider) Call(ctx context.Context, request *LLMRequest) (*LLMResponse, error) {
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

// callAPI makes the actual API call to Gemini
func (p *GeminiProvider) callAPI(ctx context.Context, prompt string, request *LLMRequest) (*LLMResponse, error) {
	// Build request body for Gemini
	// Combine system message and user prompt into a single message
	fullPrompt := "You are an expert cryptocurrency trading analyst. Provide clear, actionable analysis based on technical indicators and market data. Always respond in valid JSON format.\n\n" + prompt

	reqBody := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"role": "user",
				"parts": []map[string]interface{}{
					{
						"text": fullPrompt,
					},
				},
			},
		},
		"generationConfig": map[string]interface{}{
			"temperature":     request.Temperature,
			"maxOutputTokens": request.MaxTokens,
			"topP":            0.8,
			"topK":            10,
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Build URL with model (API key goes in a header, not the query string, so it
	// never leaks into *url.Error messages on network/TLS failures).
	url := fmt.Sprintf("%s/%s:generateContent", p.config.Endpoint, p.config.Model)

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", p.config.APIKey)

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
	var apiResp GeminiResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Extract content
	if len(apiResp.Candidates) == 0 {
		return nil, fmt.Errorf("no candidates in response")
	}

	candidate := apiResp.Candidates[0]

	// Check if content was blocked by safety filters
	if candidate.FinishReason == "SAFETY" {
		return nil, fmt.Errorf("content blocked by safety filters")
	}

	// Check if we have parts
	if len(candidate.Content.Parts) == 0 {
		// Log the full response for debugging
		p.logger.WithField("response", string(body)).Warn("No parts in candidate content")

		// Check finish reason
		if candidate.FinishReason != "" {
			return nil, fmt.Errorf("no content generated, finish reason: %s", candidate.FinishReason)
		}

		return nil, fmt.Errorf("no parts in candidate content")
	}

	content := candidate.Content.Parts[0].Text

	// Extract token usage
	inputTokens := 0
	outputTokens := 0
	if apiResp.UsageMetadata != nil {
		inputTokens = apiResp.UsageMetadata.PromptTokenCount
		outputTokens = apiResp.UsageMetadata.CandidatesTokenCount
	}

	// Calculate cost
	cost := p.GetCost(inputTokens, outputTokens)

	return &LLMResponse{
		Content:      content,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		Model:        p.config.Model,
		Provider:     p.Name(),
		Cost:         cost,
		Cached:       false,
		Timestamp:    time.Now(),
	}, nil
}

// GeminiResponse represents the response from Gemini API
type GeminiResponse struct {
	Candidates []struct {
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
	} `json:"candidates"`
	UsageMetadata *struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
	} `json:"usageMetadata,omitempty"`
}
