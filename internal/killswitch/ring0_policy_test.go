package killswitch

import (
	"testing"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/toolpolicy"
	"github.com/stretchr/testify/assert"
)

// legacyRiskIncreasingTools is the hand-written list that existed before the
// set was derived from the classification. It is pinned here on purpose: if a
// future change drops one of these from trade_increase, the kill switch would
// silently start letting it through.
var legacyRiskIncreasingTools = []string{
	"submit_order",
	"submit_market_order",
	"submit_limit_order",
	"submit_stop_order",
	"modify_order",
	"submit_futures_order_direct",
	"execute_scalping_trade",
	"start_scalping_bot",
}

func TestRiskIncreasingToolsKeepsLegacyEight(t *testing.T) {
	for _, name := range legacyRiskIncreasingTools {
		assert.True(t, RiskIncreasingTools[name], "%s must stay risk-increasing", name)
		c, ok := toolpolicy.ClassOf(name)
		assert.True(t, ok, name)
		assert.Equal(t, toolpolicy.ClassTradeIncrease, c, name)
	}
	assert.Len(t, RiskIncreasingTools, len(legacyRiskIncreasingTools),
		"a new trade_increase tool is fine, but then update this pinned list consciously")
}

func TestBlockedByRing0(t *testing.T) {
	prev := Ring0Active()
	t.Cleanup(func() { SetRing0Active(prev) })

	SetRing0Active(false)
	assert.False(t, BlockedByRing0("submit_order", nil), "ring 0 inactive blocks nothing")

	SetRing0Active(true)
	for _, name := range legacyRiskIncreasingTools[:5] {
		assert.True(t, BlockedByRing0(name, nil), name)
	}
	// Opening via the direct tool is blocked; reduce-only / close is not.
	assert.True(t, BlockedByRing0("submit_futures_order_direct", nil))
	assert.True(t, BlockedByRing0("submit_futures_order_direct", map[string]interface{}{"reduce_only": false}))
	assert.False(t, BlockedByRing0("submit_futures_order_direct", map[string]interface{}{"reduce_only": true}))
	assert.False(t, BlockedByRing0("submit_futures_order_direct", map[string]interface{}{"close_order": true}))
	// De-risking, safety and read tools stay available.
	for _, name := range []string{"cancel_order", "cancel_all_orders", "close_position_direct", "close_futures_position", "activate_kill_switch", "get_ticker", "stop_scalping_bot"} {
		assert.False(t, BlockedByRing0(name, nil), name)
	}
	// Unknown tools fail closed.
	assert.True(t, BlockedByRing0("not_a_real_tool", nil))
}
