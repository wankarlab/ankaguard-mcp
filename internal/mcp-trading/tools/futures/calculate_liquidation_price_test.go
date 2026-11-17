package futures

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLiquidationPriceIsLabeledEstimate(t *testing.T) {
	tool := &CalculateLiquidationPriceTool{}
	run := func(extra map[string]interface{}) map[string]interface{} {
		args := map[string]interface{}{"exchange": "kucoin", "pair": "BTC-USDT", "side": "long", "entry_price": 100.0, "leverage": 10.0, "position_size": 1.0}
		for k, v := range extra {
			args[k] = v
		}
		res, err := tool.Execute(context.Background(), args)
		require.NoError(t, err)
		return res.(map[string]interface{})
	}
	a := run(nil)
	assert.Equal(t, true, a["estimate"])
	assert.Contains(t, a, "assumptions")
	assert.InDelta(t, 90.5, a["liquidation_price"], 1e-9)
	assert.NotEqual(t, a["liquidation_price"], run(map[string]interface{}{"maintenance_margin_rate": 0.01})["liquidation_price"])
}
