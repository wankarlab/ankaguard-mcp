package models

// MCPRequest representa un request MCP según JSON-RPC 2.0
type MCPRequest struct {
	JSONRPC string      `json:"jsonrpc"` // Debe ser "2.0"
	Method  string      `json:"method"`  // e.g., "tools/call", "tools/list"
	Params  *MCPParams  `json:"params,omitempty"`
	ID      interface{} `json:"id"`
}

// MCPParams contiene los parámetros del request
type MCPParams struct {
	Name      string                 `json:"name"`                // Nombre del tool
	Arguments map[string]interface{} `json:"arguments,omitempty"` // Argumentos del tool
}

// MCPResponse representa una respuesta MCP
type MCPResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	Error   *MCPError   `json:"error,omitempty"`
	ID      interface{} `json:"id"`
}

// MCPError representa un error MCP
type MCPError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// Error implementa la interfaz error
func (e *MCPError) Error() string {
	return e.Message
}

// MCPToolInfo representa información de un tool MCP
type MCPToolInfo struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// MCPToolsListResponse es la respuesta para tools/list
type MCPToolsListResponse struct {
	Tools []MCPToolInfo `json:"tools"`
}
