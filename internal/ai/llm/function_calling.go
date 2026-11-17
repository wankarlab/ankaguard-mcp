package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/server"
	"github.com/sirupsen/logrus"
)

// ToolDefinition represents a tool that can be called by the LLM
type ToolDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// ToolCall represents a tool call requested by the LLM
type ToolCall struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
	Result    interface{}            `json:"result,omitempty"`
	Error     string                 `json:"error,omitempty"`
	Executed  bool                   `json:"executed"`
	Timestamp time.Time              `json:"timestamp"`
}

// FunctionCallingRequest extends LLMRequest with tool support
type FunctionCallingRequest struct {
	*LLMRequest
	Tools          []ToolDefinition `json:"tools"`
	AllowExecution bool             `json:"allow_execution"` // If true, execute tools automatically
	MaxIterations  int              `json:"max_iterations"`  // Max tool call iterations
}

// FunctionCallingResponse extends LLMResponse with tool calls
type FunctionCallingResponse struct {
	*LLMResponse
	ToolCalls      []ToolCall `json:"tool_calls"`
	FinalAnswer    string     `json:"final_answer"`
	TotalCost      float64    `json:"total_cost"`
	Iterations     int        `json:"iterations"`
	ExecutionTrace []string   `json:"execution_trace"`
}

// FunctionCallingAnalyzer handles LLM with function calling capabilities
type FunctionCallingAnalyzer struct {
	analyzer     *Analyzer
	toolRegistry *server.ToolRegistry
	log          *logrus.Logger
}

// NewFunctionCallingAnalyzer creates a new function calling analyzer
func NewFunctionCallingAnalyzer(
	analyzer *Analyzer,
	toolRegistry *server.ToolRegistry,
	log *logrus.Logger,
) *FunctionCallingAnalyzer {
	if log == nil {
		log = logrus.New()
	}

	return &FunctionCallingAnalyzer{
		analyzer:     analyzer,
		toolRegistry: toolRegistry,
		log:          log,
	}
}

// AnalyzeWithTools performs analysis with tool calling capabilities
func (fca *FunctionCallingAnalyzer) AnalyzeWithTools(
	ctx context.Context,
	request *FunctionCallingRequest,
) (*FunctionCallingResponse, error) {
	if request.MaxIterations == 0 {
		request.MaxIterations = 5 // Default max iterations
	}

	response := &FunctionCallingResponse{
		ToolCalls:      []ToolCall{},
		ExecutionTrace: []string{},
		Iterations:     0,
	}

	// Build initial prompt with tool descriptions
	enhancedPrompt := fca.buildPromptWithTools(request.Prompt, request.Tools)

	currentPrompt := enhancedPrompt
	totalCost := 0.0

	// Iterative tool calling loop
	for i := 0; i < request.MaxIterations; i++ {
		response.Iterations = i + 1

		fca.log.WithFields(logrus.Fields{
			"iteration":       i + 1,
			"max_iterations":  request.MaxIterations,
			"allow_execution": request.AllowExecution,
		}).Info("🔄 Function calling iteration")

		// Call LLM
		llmReq := &LLMRequest{
			Prompt:      currentPrompt,
			Temperature: request.Temperature,
			MaxTokens:   request.MaxTokens,
			Model:       request.Model,
		}

		llmResp, err := fca.analyzer.primaryProvider.Call(ctx, llmReq)
		if err != nil {
			return nil, fmt.Errorf("LLM call failed: %w", err)
		}

		totalCost += llmResp.Cost
		response.LLMResponse = llmResp

		// Parse response for tool calls
		toolCalls, finalAnswer, err := fca.parseToolCalls(llmResp.Content)
		if err != nil {
			fca.log.WithError(err).Warn("Failed to parse tool calls, treating as final answer")
			response.FinalAnswer = llmResp.Content
			break
		}

		// If no tool calls, we have the final answer
		if len(toolCalls) == 0 {
			response.FinalAnswer = finalAnswer
			response.ExecutionTrace = append(response.ExecutionTrace,
				fmt.Sprintf("Iteration %d: Final answer received", i+1))
			break
		}

		// Execute tool calls if allowed
		if request.AllowExecution {
			for idx := range toolCalls {
				if err := fca.executeTool(ctx, &toolCalls[idx]); err != nil {
					fca.log.WithError(err).WithField("tool", toolCalls[idx].Name).Error("Tool execution failed")
					toolCalls[idx].Error = err.Error()
				}
				response.ToolCalls = append(response.ToolCalls, toolCalls[idx])
			}
		} else {
			// Just record the tool calls without executing
			for _, tc := range toolCalls {
				tc.Executed = false
				response.ToolCalls = append(response.ToolCalls, tc)
			}
			response.FinalAnswer = "Tool execution not allowed. Tools requested: " + fca.formatToolCallsSummary(toolCalls)
			break
		}

		// Build next prompt with tool results
		currentPrompt = fca.buildPromptWithToolResults(enhancedPrompt, toolCalls)
		response.ExecutionTrace = append(response.ExecutionTrace,
			fmt.Sprintf("Iteration %d: Executed %d tools", i+1, len(toolCalls)))

		// Check if we've reached max iterations
		if i == request.MaxIterations-1 {
			response.FinalAnswer = "Max iterations reached. Last response: " + llmResp.Content
			response.ExecutionTrace = append(response.ExecutionTrace,
				"Max iterations reached without final answer")
		}
	}

	response.TotalCost = totalCost

	fca.log.WithFields(logrus.Fields{
		"iterations": response.Iterations,
		"tool_calls": len(response.ToolCalls),
		"total_cost": fmt.Sprintf("$%.6f", totalCost),
		"has_answer": response.FinalAnswer != "",
	}).Info("✅ Function calling completed")

	return response, nil
}

// buildPromptWithTools enhances the prompt with tool descriptions
func (fca *FunctionCallingAnalyzer) buildPromptWithTools(prompt string, tools []ToolDefinition) string {
	toolsJSON, _ := json.MarshalIndent(tools, "", "  ")

	enhanced := fmt.Sprintf(`You are an autonomous trading AI with access to the following tools:

%s

You can call these tools by responding with JSON in this format:
{
  "tool_calls": [
    {
      "name": "tool_name",
      "arguments": {"param1": "value1"}
    }
  ],
  "reasoning": "Why you're calling these tools"
}

Or provide a final answer:
{
  "final_answer": "Your response",
  "reasoning": "Your analysis"
}

User request: %s

Think step by step:
1. What information do you need?
2. Which tools can provide that information?
3. What's your trading decision?

Respond in JSON format.`, string(toolsJSON), prompt)

	return enhanced
}

// buildPromptWithToolResults builds prompt with previous tool results
func (fca *FunctionCallingAnalyzer) buildPromptWithToolResults(originalPrompt string, toolCalls []ToolCall) string {
	resultsText := "\n\nTool execution results:\n"
	for _, tc := range toolCalls {
		if tc.Error != "" {
			resultsText += fmt.Sprintf("- %s: ERROR - %s\n", tc.Name, tc.Error)
		} else {
			resultJSON, _ := json.Marshal(tc.Result)
			resultsText += fmt.Sprintf("- %s: %s\n", tc.Name, string(resultJSON))
		}
	}

	return originalPrompt + resultsText + "\n\nBased on these results, provide your final trading decision in JSON format."
}

// parseToolCalls parses LLM response for tool calls or final answer
func (fca *FunctionCallingAnalyzer) parseToolCalls(content string) ([]ToolCall, string, error) {
	var response struct {
		ToolCalls []struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		} `json:"tool_calls"`
		FinalAnswer string `json:"final_answer"`
		Reasoning   string `json:"reasoning"`
	}

	if err := json.Unmarshal([]byte(content), &response); err != nil {
		// Try to extract JSON from markdown code blocks
		content = extractJSONFromMarkdown(content)
		if err := json.Unmarshal([]byte(content), &response); err != nil {
			return nil, "", fmt.Errorf("failed to parse JSON: %w", err)
		}
	}

	// If final answer is provided, return it
	if response.FinalAnswer != "" {
		return nil, response.FinalAnswer, nil
	}

	// Convert to ToolCall structs
	toolCalls := make([]ToolCall, len(response.ToolCalls))
	for i, tc := range response.ToolCalls {
		toolCalls[i] = ToolCall{
			ID:        fmt.Sprintf("call_%d_%d", time.Now().Unix(), i),
			Name:      tc.Name,
			Arguments: tc.Arguments,
			Timestamp: time.Now(),
		}
	}

	return toolCalls, "", nil
}

// executeTool executes a single tool call.
//
// AI-04 (2026-09-10 audit): AnalyzeWithTools/executeTool have zero callers
// today (dead code), but IF ever activated this would let the LLM call
// order-submission tools (submit_market_order, submit_limit_order, etc.)
// directly. Verified (not assumed) that this already flows through the
// Pattern-2 risk chokepoint: fca.toolRegistry is the same *server.ToolRegistry
// populated by main.go, so tool.Execute() below resolves to the same
// SubmitMarketOrderTool/SubmitLimitOrderTool/etc. in
// internal/mcp-trading/tools/orders, whose Execute() calls
// t.client.SubmitOrder — t.client is the *gctclient.Client wrapper
// constructed once in main.go (orders.RegisterAll(mcpServer, gctClient)).
// gctclient.Client.SubmitOrder delegates to either the HFT circuit-breaker
// path or the direct fallback path, but BOTH ultimately call
// baseClient.SubmitOrder (internal/gct/client.go), which is the exact
// gct.GCTClient instance risk.RegisterAll/main.go calls SetRiskSentinel on
// (TOOL-01 fix) — its SubmitOrder evaluates the RiskSentinel gate
// (evaluateOrderRisk) before submitting, same as every other order path. No
// additional gate needed here; if this ever gets a real caller, re-verify
// this chain hasn't changed (e.g. a new tool bypassing t.client).
func (fca *FunctionCallingAnalyzer) executeTool(ctx context.Context, toolCall *ToolCall) error {
	fca.log.WithFields(logrus.Fields{
		"tool":      toolCall.Name,
		"arguments": toolCall.Arguments,
	}).Info("🔧 Executing tool")

	// Get tool from registry
	tool, exists := fca.toolRegistry.Get(toolCall.Name)
	if !exists {
		return fmt.Errorf("tool not found: %s", toolCall.Name)
	}

	// Normalize 'side' parameter for order tools (convert BUY/SELL to lowercase)
	if toolCall.Name == "submit_market_order" || toolCall.Name == "submit_limit_order" ||
		toolCall.Name == "submit_stop_order" || toolCall.Name == "submit_order" {
		if side, ok := toolCall.Arguments["side"].(string); ok {
			toolCall.Arguments["side"] = strings.ToLower(side)
		}
	}

	// Execute tool
	result, err := tool.Execute(ctx, toolCall.Arguments)
	if err != nil {
		return fmt.Errorf("tool execution failed: %w", err)
	}

	toolCall.Result = result
	toolCall.Executed = true

	fca.log.WithField("tool", toolCall.Name).Info("✅ Tool executed successfully")

	return nil
}

// formatToolCallsSummary creates a summary of tool calls
func (fca *FunctionCallingAnalyzer) formatToolCallsSummary(toolCalls []ToolCall) string {
	summary := ""
	for i, tc := range toolCalls {
		if i > 0 {
			summary += ", "
		}
		summary += tc.Name
	}
	return summary
}

// extractJSONFromMarkdown extracts JSON from markdown code blocks
func extractJSONFromMarkdown(content string) string {
	// Simple extraction: look for ```json ... ``` or ``` ... ```
	start := -1
	end := -1

	// Find ```json or ```
	if idx := findIndex(content, "```json"); idx != -1 {
		start = idx + 7
	} else if idx := findIndex(content, "```"); idx != -1 {
		start = idx + 3
	}

	if start != -1 {
		// Find closing ```
		if idx := findIndex(content[start:], "```"); idx != -1 {
			end = start + idx
			return content[start:end]
		}
	}

	return content
}

// findIndex finds the index of a substring
func findIndex(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// LoadToolsFromRegistry loads tool definitions from MCP tool registry
func LoadToolsFromRegistry(registry *server.ToolRegistry) []ToolDefinition {
	tools := []ToolDefinition{}

	for _, mcpTool := range registry.List() {
		toolDef := ToolDefinition{
			Name:        mcpTool.Name,
			Description: mcpTool.Description,
			Parameters:  convertInputSchema(mcpTool.InputSchema),
		}
		tools = append(tools, toolDef)
	}

	return tools
}

// convertInputSchema converts MCP input schema to function calling format
func convertInputSchema(schema map[string]interface{}) map[string]interface{} {
	// MCP schema is already in JSON Schema format, so we can use it directly
	return schema
}
