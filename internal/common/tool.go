package common

import "context"

// Tool define la interfaz estándar que deben implementar todos los tools MCP.
// Esta interfaz es compartida entre todos los paquetes del sistema para
// garantizar consistencia y reutilización.
type Tool interface {
	// Name retorna el nombre único del tool
	Name() string

	// Description retorna una descripción legible del tool
	Description() string

	// InputSchema retorna el JSON Schema que define los parámetros de entrada
	InputSchema() map[string]interface{}

	// Execute ejecuta el tool con los parámetros proporcionados
	Execute(ctx context.Context, params map[string]interface{}) (interface{}, error)
}

// ToolRegistry define la interfaz para registros de tools.
// Permite diferentes implementaciones manteniendo compatibilidad.
type ToolRegistry interface {
	// GetTool obtiene un tool por nombre
	GetTool(name string) (Tool, bool)

	// ListTools retorna información de todos los tools registrados
	ListTools() []ToolInfo
}

// ToolInfo contiene información básica de un tool
type ToolInfo struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}
