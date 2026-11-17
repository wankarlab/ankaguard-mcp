package server

import (
	"github.com/rantipay/trading-mcp/internal/common"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
)

// Tool es un alias de la interfaz común para mantener compatibilidad
type Tool = common.Tool

// ToolRegistry mantiene el registro de todos los tools disponibles
type ToolRegistry struct {
	tools map[string]Tool
}

// NewToolRegistry crea un nuevo registro de tools
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]Tool),
	}
}

// Register registra un nuevo tool
func (r *ToolRegistry) Register(tool Tool) error {
	name := tool.Name()
	if _, exists := r.tools[name]; exists {
		return &models.MCPError{
			Code:    -32600,
			Message: "Tool already registered",
			Data:    map[string]interface{}{"tool": name},
		}
	}

	r.tools[name] = tool
	return nil
}

// Get obtiene un tool por nombre
func (r *ToolRegistry) Get(name string) (Tool, bool) {
	tool, exists := r.tools[name]
	return tool, exists
}

// GetTool implementa common.ToolRegistry
func (r *ToolRegistry) GetTool(name string) (common.Tool, bool) {
	return r.Get(name)
}

// List retorna la lista de todos los tools registrados
func (r *ToolRegistry) List() []models.MCPToolInfo {
	tools := make([]models.MCPToolInfo, 0, len(r.tools))
	for _, tool := range r.tools {
		tools = append(tools, models.MCPToolInfo{
			Name:        tool.Name(),
			Description: tool.Description(),
			InputSchema: tool.InputSchema(),
		})
	}
	return tools
}

// ListTools implementa common.ToolRegistry
func (r *ToolRegistry) ListTools() []common.ToolInfo {
	tools := make([]common.ToolInfo, 0, len(r.tools))
	for _, tool := range r.tools {
		tools = append(tools, common.ToolInfo{
			Name:        tool.Name(),
			Description: tool.Description(),
			InputSchema: tool.InputSchema(),
		})
	}
	return tools
}

// Count retorna el número de tools registrados
func (r *ToolRegistry) Count() int {
	return len(r.tools)
}
