package technical

import (
	"context"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
)

// CalculateFibonacciTool - Fibonacci Retracements
type CalculateFibonacciTool struct{ BaseTool }

func NewCalculateFibonacciTool(c *gctclient.Client) *CalculateFibonacciTool {
	return &CalculateFibonacciTool{BaseTool{client: c}}
}
func (t *CalculateFibonacciTool) Name() string { return "calculate_fibonacci" }
func (t *CalculateFibonacciTool) Description() string {
	return "Calculate Fibonacci retracement levels - key support/resistance levels"
}
func (t *CalculateFibonacciTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{"exchange": map[string]interface{}{"type": "string"}, "pair": map[string]interface{}{"type": "string"}, "high": map[string]interface{}{"type": "number"}, "low": map[string]interface{}{"type": "number"}}, "required": []string{"exchange", "pair", "high", "low"}}
}
func (t *CalculateFibonacciTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, pair, err := t.validateExchangeAndPair(params)
	if err != nil {
		return nil, err
	}
	high := getFloatParam(params, "high", 0)
	low := getFloatParam(params, "low", 0)
	diff := high - low
	return map[string]interface{}{
		"exchange": exchange,
		"pair":     pair,
		"levels": map[string]float64{
			"0.0":   low,
			"23.6":  low + diff*0.236,
			"38.2":  low + diff*0.382,
			"50.0":  low + diff*0.5,
			"61.8":  low + diff*0.618,
			"100.0": high,
		},
	}, nil
}
