package handlers

import (
	"context"
	"fmt"

	"github.com/rantipay/trading-mcp/internal/common"
	"github.com/sirupsen/logrus"
)

// MCPClientWrapper wraps the MCP ToolRegistry to implement the MCPClient interface
// required by BacktestingHandler
type MCPClientWrapper struct {
	toolRegistry common.ToolRegistry
	log          *logrus.Logger
}

// NewMCPClientWrapper creates a new MCP client wrapper
func NewMCPClientWrapper(toolRegistry common.ToolRegistry, log *logrus.Logger) *MCPClientWrapper {
	return &MCPClientWrapper{
		toolRegistry: toolRegistry,
		log:          log,
	}
}

// CallTool implements the MCPClient interface for BacktestingHandler
func (m *MCPClientWrapper) CallTool(ctx context.Context, toolName string, params map[string]interface{}) (map[string]interface{}, error) {
	m.log.WithFields(logrus.Fields{
		"tool":   toolName,
		"params": params,
	}).Debug("MCPClientWrapper: Calling MCP tool")

	// Get the tool from registry
	tool, exists := m.toolRegistry.GetTool(toolName)
	if !exists {
		return nil, fmt.Errorf("tool '%s' not found in registry", toolName)
	}

	// Execute the tool
	result, err := tool.Execute(ctx, params)
	if err != nil {
		m.log.WithError(err).WithField("tool", toolName).Error("MCPClientWrapper: Tool execution failed")
		return nil, err
	}

	// Convert result to map[string]interface{}
	if resultMap, ok := result.(map[string]interface{}); ok {
		return resultMap, nil
	}

	// If not a map, wrap in a map
	return map[string]interface{}{
		"result": result,
	}, nil
}
