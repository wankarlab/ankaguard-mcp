package marketdata

import (
	"context"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/tools/common"
)

// BaseTool contiene funcionalidad común para todos los tools de market data
type BaseTool struct {
	common.BaseTool
	client *gctclient.Client
}

// Wrapper methods for backward compatibility
func (b *BaseTool) validateExchangeAndPair(params map[string]interface{}) (string, string, error) {
	return common.ValidateExchangeAndPair(params)
}

func getIntParam(params map[string]interface{}, key string, defaultValue int) int {
	return common.GetIntParam(params, key, defaultValue)
}

func getStringParam(params map[string]interface{}, key string, defaultValue string) string {
	return common.GetStringParam(params, key, defaultValue)
}

func (b *BaseTool) validateMarketType(marketType string) error {
	return common.ValidateMarketType(marketType)
}

func (b *BaseTool) convertSymbol(symbol, marketType, exchange string) (string, error) {
	b.BaseTool.Client = b.client
	return b.BaseTool.ConvertSymbol(symbol, marketType, exchange)
}

func executeWithContext(ctx context.Context, fn func(context.Context) (interface{}, error)) (interface{}, error) {
	return common.ExecuteWithContext(ctx, fn)
}
