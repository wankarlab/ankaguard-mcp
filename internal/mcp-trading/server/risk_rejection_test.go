package server

import (
	"context"
	"fmt"
	"testing"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/rantipay/trading-mcp/internal/risk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rejectingTool fails like an order tool whose order the sentinel blocked.
type rejectingTool struct{ fakeTool }

func (r *rejectingTool) Execute(_ context.Context, _ map[string]interface{}) (interface{}, error) {
	return nil, fmt.Errorf("submit order: %w", risk.NewRejectedError(&risk.RiskDecision{
		Code: risk.CodeOrderCapExceeded, Reason: "ORDER_CAP_EXCEEDED: order notional $64.68 exceeds the per-order cap $2.00",
	}))
}

// wrappedRejectingTool reproduces the order tools: the rejection is wrapped in a
// generic -32603 MCPError with the text in Data.
type wrappedRejectingTool struct{ fakeTool }

func (r *wrappedRejectingTool) Execute(_ context.Context, _ map[string]interface{}) (interface{}, error) {
	return nil, &models.MCPError{Code: -32603, Message: "Internal error",
		Data: "order rejected by risk sentinel: ORDER_CAP_EXCEEDED: order notional $64.64 exceeds the per-order cap $2.00"}
}

func TestWrappedRiskRejectionKeepsItsCode(t *testing.T) {
	h := newPermHarness(t)
	h.srv.SetPaperBroker(stubPaperBroker{})
	require.NoError(t, h.srv.RegisterTool(&wrappedRejectingTool{fakeTool{name: "submit_market_order"}}))
	resp := h.call(t, mintToken(t, 1, "paper", "jti-wrapped"), "submit_market_order", nil)
	require.NotNil(t, resp.Error)
	assert.Equal(t, ErrCodeRiskRejected, resp.Error.Code)
	assert.Equal(t, risk.CodeOrderCapExceeded, resp.Error.Message)
}

// A sentinel rejection reaches the agent with its own code, not "Internal error",
// for the live and the paper profile alike.
func TestRiskRejectionKeepsItsCode(t *testing.T) {
	for _, profile := range []string{"live", "paper"} {
		t.Run(profile, func(t *testing.T) {
			h := newPermHarness(t)
			h.srv.SetPaperBroker(stubPaperBroker{})
			require.NoError(t, h.srv.RegisterTool(&rejectingTool{fakeTool{name: "submit_market_order"}}))
			resp := h.call(t, mintToken(t, 1, profile, "jti-rej-"+profile), "submit_market_order", nil)
			require.NotNil(t, resp.Error)
			assert.Equal(t, ErrCodeRiskRejected, resp.Error.Code)
			assert.Equal(t, risk.CodeOrderCapExceeded, resp.Error.Message)
			assert.Equal(t, risk.CodeOrderCapExceeded, dataCode(t, resp))
		})
	}
}
