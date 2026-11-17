package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAIProvider implements the Provider interface for OpenAI
type OpenAIProvider struct {
	*BaseProvider
	httpClient *http.Client
}

// NewOpenAIProvider creates a new OpenAI provider
func NewOpenAIProvider(config *ProviderConfig) *OpenAIProvider {
	if config.Endpoint == "" {
		config.Endpoint = "https://api.openai.com/v1/chat/completions"
	}

	if config.CostPerInputK == 0 {
		// Default costs for GPT-4 (update based on actual pricing)
		config.CostPerInputK = 0.03  // $0.03 per 1K input tokens
		config.CostPerOutputK = 0.06 // $0.06 per 1K output tokens
	}

	return &OpenAIProvider{
		BaseProvider: NewBaseProvider(config),
		httpClient: &http.Client{
			Timeout: time.Duration(config.TimeoutSeconds) * time.Second,
		},
	}
}

// Call makes a call to OpenAI API
func (p *OpenAIProvider) Call(ctx context.Context, request *LLMRequest) (*LLMResponse, error) {
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

// callAPI makes the actual API call to OpenAI
func (p *OpenAIProvider) callAPI(ctx context.Context, prompt string, request *LLMRequest) (*LLMResponse, error) {
	// Build request body
	reqBody := map[string]interface{}{
		"model": p.config.Model,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "You are an expert cryptocurrency trading analyst. Provide clear, actionable analysis based on technical indicators and market data.",
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
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var apiResp OpenAIResponse
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

// OpenAIResponse represents the response from OpenAI API
type OpenAIResponse struct {
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

// ParseMarketAnalysis parses the LLM response into a structured analysis
func ParseMarketAnalysis(response *LLMResponse) (*LLMAnalysis, error) {
	if response == nil {
		return nil, fmt.Errorf("response is nil")
	}

	// Try to parse as JSON
	var analysis LLMAnalysis

	// Extract JSON from response (it might be wrapped in markdown code blocks)
	content := response.Content

	content = extractJSONContent(content)

	// Parse JSON
	var jsonData map[string]interface{}
	if err := json.Unmarshal([]byte(content), &jsonData); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	// Extract fields
	analysis.Action = getString(jsonData, "action")
	analysis.Confidence = getFloat(jsonData, "confidence")
	analysis.Reasoning = getString(jsonData, "reasoning")
	analysis.EntryPrice = getFloat(jsonData, "entry_price")
	analysis.StopLoss = getFloat(jsonData, "stop_loss")
	analysis.TakeProfit = getFloat(jsonData, "take_profit")
	analysis.RiskAssessment = getString(jsonData, "risk_assessment")
	analysis.MarketCondition = getString(jsonData, "market_condition")
	analysis.KeyFactors = getStringArray(jsonData, "key_factors")
	analysis.Warnings = getStringArray(jsonData, "warnings")
	analysis.Response = response
	analysis.Timestamp = time.Now()

	return &analysis, nil
}

// extractJSONContent strips markdown fences and extracts the first JSON object.
// Handles ```json...```, ```...```, single backtick spans, and bare JSON in prose.
func extractJSONContent(content string) string {
	content = strings.TrimSpace(content)
	if len(content) == 0 {
		return content
	}

	// Strip triple-backtick fences (```json...``` or ```...```)
	if start := bytes.Index([]byte(content), []byte("```json")); start != -1 {
		inner := content[start+7:]
		if end := bytes.Index([]byte(inner), []byte("```")); end != -1 {
			content = strings.TrimSpace(inner[:end])
		}
	} else if start := bytes.Index([]byte(content), []byte("```")); start != -1 {
		inner := content[start+3:]
		if end := bytes.Index([]byte(inner), []byte("```")); end != -1 {
			content = strings.TrimSpace(inner[:end])
		}
	}

	// As a final safety net, scan for first '{' and last '}' to isolate JSON object
	if first := strings.Index(content, "{"); first != -1 {
		if last := strings.LastIndex(content, "}"); last >= first {
			return content[first : last+1]
		}
	}

	return content
}

// Helper functions for JSON parsing
func getString(data map[string]interface{}, key string) string {
	if val, ok := data[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

func getFloat(data map[string]interface{}, key string) float64 {
	if val, ok := data[key]; ok {
		switch v := val.(type) {
		case float64:
			return v
		case int:
			return float64(v)
		case string:
			// Try to parse string as float
			var f float64
			fmt.Sscanf(v, "%f", &f)
			return f
		}
	}
	return 0
}

func getStringArray(data map[string]interface{}, key string) []string {
	if val, ok := data[key]; ok {
		if arr, ok := val.([]interface{}); ok {
			result := make([]string, 0, len(arr))
			for _, item := range arr {
				if str, ok := item.(string); ok {
					result = append(result, str)
				}
			}
			return result
		}
	}
	return []string{}
}
