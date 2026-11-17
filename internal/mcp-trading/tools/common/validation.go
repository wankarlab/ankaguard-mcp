package common

import (
	"context"
	"fmt"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/rantipay/trading-mcp/internal/paper"
)

// BaseTool contiene funcionalidad común para todos los tools
type BaseTool struct {
	Client *gctclient.Client
}

// ValidateExchangeAndPair valida que exchange y pair estén presentes
func ValidateExchangeAndPair(params map[string]interface{}) (string, string, error) {
	exchange, ok := params["exchange"].(string)
	if !ok || exchange == "" {
		return "", "", &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "exchange is required and must be a string",
		}
	}

	pair, ok := params["pair"].(string)
	if !ok || pair == "" {
		return "", "", &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "pair is required and must be a string",
		}
	}

	return exchange, pair, nil
}

// GetIntParam obtiene un parámetro entero con valor por defecto
func GetIntParam(params map[string]interface{}, key string, defaultValue int) int {
	if val, ok := params[key]; ok {
		switch v := val.(type) {
		case int:
			return v
		case float64:
			return int(v)
		case string:
			var intVal int
			fmt.Sscanf(v, "%d", &intVal)
			return intVal
		}
	}
	return defaultValue
}

// GetFloatParam obtiene un parámetro float con valor por defecto
func GetFloatParam(params map[string]interface{}, key string, defaultValue float64) float64 {
	if val, ok := params[key]; ok {
		switch v := val.(type) {
		case float64:
			return v
		case int:
			return float64(v)
		case string:
			var floatVal float64
			fmt.Sscanf(v, "%f", &floatVal)
			return floatVal
		}
	}
	return defaultValue
}

// GetStringParam obtiene un parámetro string con valor por defecto
func GetStringParam(params map[string]interface{}, key string, defaultValue string) string {
	if val, ok := params[key].(string); ok {
		return val
	}
	return defaultValue
}

// ExecuteWithContext ejecuta una función con contexto y manejo de errores
func ExecuteWithContext(ctx context.Context, fn func(context.Context) (interface{}, error)) (interface{}, error) {
	result, err := fn(ctx)
	if err != nil {
		// Errors of the paper broker keep their symbolic code and typed data
		// (INSUFFICIENT_FUNDS with available/required, PAPER_UNSUPPORTED...):
		// the dispatcher maps them to their own JSON-RPC error.
		if paper.CodeOf(err) != "" {
			return nil, err
		}
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Internal error",
			Data:    err.Error(),
		}
	}
	return result, nil
}

// ValidateMarketType validates the market_type parameter
func ValidateMarketType(marketType string) error {
	validTypes := map[string]bool{
		"spot":    true,
		"futures": true,
		"margin":  true,
		"options": true,
	}

	if !validTypes[marketType] {
		return &models.MCPError{
			Code:    -32602,
			Message: "Invalid market_type",
			Data:    "market_type must be one of: spot, futures, margin, options",
		}
	}

	return nil
}

// ConvertSymbol converts a symbol using the GCT client's symbol converter
func (b *BaseTool) ConvertSymbol(symbol, marketType, exchange string) (string, error) {
	converted, err := b.Client.ConvertSymbol(symbol, marketType, exchange)
	if err != nil {
		return "", &models.MCPError{
			Code:    -32603,
			Message: "Symbol conversion failed",
			Data:    err.Error(),
		}
	}
	return converted, nil
}
