package orders

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The protective stop follows the real last price: another price, another stop.
func TestTrailingStopPriceFollowsLastPrice(t *testing.T) {
	assert.InDelta(t, 98, trailingStopPrice(100, "sell", "percentage", 2), 1e-9)
	assert.InDelta(t, 196, trailingStopPrice(200, "sell", "percentage", 2), 1e-9)
	assert.InDelta(t, 102, trailingStopPrice(100, "buy", "percentage", 2), 1e-9)
	assert.InDelta(t, 95, trailingStopPrice(100, "sell", "fixed", 5), 1e-9)
	assert.InDelta(t, 105, trailingStopPrice(100, "buy", "fixed", 5), 1e-9)
}
