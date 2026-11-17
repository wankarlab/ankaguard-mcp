package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rantipay/trading-mcp/internal/ai/llm"
	"github.com/rantipay/trading-mcp/internal/config"
	"github.com/sirupsen/logrus"
)

// GetProviderStatusTool provides provider health and statistics via MCP
type GetProviderStatusTool struct {
	analyzer *llm.Analyzer
	log      *logrus.Logger
}

// NewGetProviderStatusTool creates a new provider status tool
func NewGetProviderStatusTool(
	cfg *config.AIConfig,
	log *logrus.Logger,
) (*GetProviderStatusTool, error) {
	// Create analyzer without cost repository (optional)
	analyzer, err := llm.NewAnalyzer(cfg, nil, log)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM analyzer: %w", err)
	}

	return &GetProviderStatusTool{
		analyzer: analyzer,
		log:      log,
	}, nil
}

// Name returns the tool name
func (t *GetProviderStatusTool) Name() string {
	return "llm_get_provider_status"
}

// Description returns the tool description
func (t *GetProviderStatusTool) Description() string {
	return "Get health status and statistics for all configured LLM providers (Gemini, OpenAI, Anthropic, OpenRouter)"
}

// InputSchema returns the JSON schema for the tool's input
func (t *GetProviderStatusTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
		"required":   []string{},
	}
}

// Call executes the tool
func (t *GetProviderStatusTool) Call(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	// Get provider statistics
	providerStats := t.analyzer.GetProviderStats()

	// Get cache statistics
	cacheStats := t.analyzer.GetCacheStats()

	// Get daily call count
	dailyCallCount := t.analyzer.GetDailyCallCount()

	// Get primary provider
	primaryProvider := t.analyzer.GetPrimaryProvider()

	// Build response
	response := map[string]interface{}{
		"primary_provider": primaryProvider.Name(),
		"daily_call_count": dailyCallCount,
		"providers":        make(map[string]interface{}),
	}

	// Add provider statistics
	for providerName, stats := range providerStats {
		providerInfo := map[string]interface{}{
			"total_calls":      stats.TotalCalls,
			"successful_calls": stats.SuccessfulCalls,
			"failed_calls":     stats.FailedCalls,
			"total_cost":       stats.TotalCost,
			"total_tokens":     stats.TotalTokens,
			"avg_latency_ms":   stats.AvgLatency.Milliseconds(),
			"last_call":        stats.LastCall,
		}

		// Calculate success rate
		if stats.TotalCalls > 0 {
			successRate := float64(stats.SuccessfulCalls) / float64(stats.TotalCalls)
			providerInfo["success_rate"] = successRate
			providerInfo["status"] = getProviderStatus(successRate, stats.FailedCalls)
		} else {
			providerInfo["success_rate"] = 0.0
			providerInfo["status"] = "not_used"
		}

		if stats.LastError != "" {
			providerInfo["last_error"] = stats.LastError
		}

		response["providers"].(map[string]interface{})[providerName] = providerInfo
	}

	// Add cache statistics
	if cacheStats != nil {
		response["cache"] = map[string]interface{}{
			"hits":        cacheStats.Hits,
			"misses":      cacheStats.Misses,
			"entries":     cacheStats.Entries,
			"hit_rate":    cacheStats.HitRate,
			"total_saved": cacheStats.TotalSaved,
		}
	}

	return response, nil
}

// getProviderStatus determines provider status based on success rate
func getProviderStatus(successRate float64, failedCalls int64) string {
	if successRate >= 0.95 {
		return "healthy"
	} else if successRate >= 0.80 {
		return "degraded"
	} else if failedCalls > 5 {
		return "unhealthy"
	}
	return "unknown"
}

// Execute implements the common.Tool interface
func (t *GetProviderStatusTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	return t.Call(ctx, params)
}

// MarshalJSON implements json.Marshaler for tool registration
func (t *GetProviderStatusTool) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"name":        t.Name(),
		"description": t.Description(),
		"inputSchema": t.InputSchema(),
	})
}
