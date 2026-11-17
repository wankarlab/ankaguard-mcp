package ai

import (
	"context"
	"fmt"

	"github.com/rantipay/trading-mcp/internal/common"
	"github.com/sirupsen/logrus"
)

// MCPClient wraps MCP tools for AI usage
type MCPClient struct {
	toolRegistry common.ToolRegistry
	log          *logrus.Logger
}

// NewMCPClient creates a new MCP client for AI
func NewMCPClient(toolRegistry common.ToolRegistry, log *logrus.Logger) *MCPClient {
	return &MCPClient{
		toolRegistry: toolRegistry,
		log:          log,
	}
}

// GetCandles gets candles using MCP tool
func (m *MCPClient) GetCandles(ctx context.Context, exchange, pair, interval string, limit int) ([]interface{}, error) {
	tool, exists := m.toolRegistry.GetTool("get_candles")
	if !exists {
		return nil, fmt.Errorf("get_candles tool not found")
	}

	// Prepare arguments
	args := map[string]interface{}{
		"exchange": exchange,
		"pair":     pair,
		"interval": interval,
		"limit":    limit,
	}

	// Execute tool
	result, err := tool.Execute(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("failed to execute get_candles: %w", err)
	}

	// Convert result to []interface{} regardless of type
	// The tool returns []models.Candle which we need to convert
	var candles []interface{}

	// Try to convert using reflection
	switch v := result.(type) {
	case []interface{}:
		candles = v
	default:
		// Use reflection to convert any slice to []interface{}
		// This handles []models.Candle and other typed slices
		if slice, ok := convertToInterfaceSlice(result); ok {
			candles = slice
		} else {
			return nil, fmt.Errorf("unexpected result format from get_candles: %T", result)
		}
	}

	return candles, nil
}

// GetTicker gets ticker using MCP tool
func (m *MCPClient) GetTicker(ctx context.Context, exchange, pair string) (interface{}, error) {
	tool, exists := m.toolRegistry.GetTool("get_ticker")
	if !exists {
		return nil, fmt.Errorf("get_ticker tool not found")
	}

	args := map[string]interface{}{
		"exchange": exchange,
		"pair":     pair,
	}

	result, err := tool.Execute(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("failed to execute get_ticker: %w", err)
	}

	return result, nil
}

// CalculateRSI calculates RSI using MCP tool
func (m *MCPClient) CalculateRSI(ctx context.Context, exchange, pair string, period int) (float64, error) {
	tool, exists := m.toolRegistry.GetTool("calculate_rsi")
	if !exists {
		return 0, fmt.Errorf("calculate_rsi tool not found")
	}

	args := map[string]interface{}{
		"exchange": exchange,
		"pair":     pair,
		"period":   period,
	}

	result, err := tool.Execute(ctx, args)
	if err != nil {
		return 0, fmt.Errorf("failed to execute calculate_rsi: %w", err)
	}

	// Parse result
	if resultMap, ok := result.(map[string]interface{}); ok {
		if rsi, ok := resultMap["rsi"].(float64); ok {
			return rsi, nil
		}
	}

	return 0, fmt.Errorf("unexpected result format from calculate_rsi")
}

// CalculateMACD calculates MACD using MCP tool
func (m *MCPClient) CalculateMACD(ctx context.Context, exchange, pair string, fast, slow, signal int) (interface{}, error) {
	tool, exists := m.toolRegistry.GetTool("calculate_macd")
	if !exists {
		return nil, fmt.Errorf("calculate_macd tool not found")
	}

	args := map[string]interface{}{
		"exchange":      exchange,
		"pair":          pair,
		"fast_period":   fast,
		"slow_period":   slow,
		"signal_period": signal,
	}

	result, err := tool.Execute(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("failed to execute calculate_macd: %w", err)
	}

	return result, nil
}

// CalculateBollingerBands calculates Bollinger Bands using MCP tool
func (m *MCPClient) CalculateBollingerBands(ctx context.Context, exchange, pair string, period int, stdDev float64) (interface{}, error) {
	tool, exists := m.toolRegistry.GetTool("calculate_bollinger")
	if !exists {
		return nil, fmt.Errorf("calculate_bollinger tool not found")
	}

	args := map[string]interface{}{
		"exchange": exchange,
		"pair":     pair,
		"period":   period,
		"std_dev":  stdDev,
	}

	result, err := tool.Execute(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("failed to execute calculate_bollinger: %w", err)
	}

	return result, nil
}
