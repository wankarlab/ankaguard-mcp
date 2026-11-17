package technical

import (
	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/server"
)

// RegisterAll registra todos los tools de technical analysis
func RegisterAll(srv *server.MCPServer, client *gctclient.Client) error {
	tools := []server.Tool{
		// Indicadores principales (implementados)
		NewCalculateRSITool(client),
		NewCalculateMACDTool(client),
		NewCalculateBollingerTool(client),
		NewCalculateEMATool(client),
		NewCalculateSMATool(client),

		// Indicadores específicos para Futures
		NewCalculateRSIFuturesTool(client),
		NewCalculateMACDFuturesTool(client),

		// Fibonacci: pure calculation over caller-supplied high and low
		NewCalculateFibonacciTool(client),

		// Análisis avanzado
		NewGetCorrelationTool(client),
	}

	// Indicators computed over real exchange candles (see candle_indicators.go).
	for _, tool := range newCandleIndicatorTools(client) {
		tools = append(tools, tool)
	}

	for _, tool := range tools {
		if err := srv.RegisterTool(tool); err != nil {
			return err
		}
	}

	return nil
}
