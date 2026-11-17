package portfolio

import (
	"fmt"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
)

// BaseTool provides common functionality for portfolio tools
type BaseTool struct {
	client *gctclient.Client
}

// convertSymbol converts symbol using market type if needed
func (t *BaseTool) convertSymbol(symbol, marketType, exchange string) (string, error) {
	if marketType == "" {
		return symbol, nil
	}

	converted, err := t.client.ConvertSymbol(symbol, marketType, exchange)
	if err != nil {
		return "", fmt.Errorf("failed to convert symbol %s for %s market on %s: %w", symbol, marketType, exchange, err)
	}

	return converted, nil
}
