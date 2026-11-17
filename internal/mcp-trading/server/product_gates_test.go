package server

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/sirupsen/logrus"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubPaperBroker struct{}

func (stubPaperBroker) Assumptions() map[string]interface{} {
	return map[string]interface{}{"slippage_pct": 0.05}
}

// Task 6.12: trading.live_enabled=false rejects trade_increase and control with
// the live profile, and nothing else.
func TestLiveGate(t *testing.T) {
	type want string
	const (
		executed want = "executed"
		blocked  want = want(LiveTradingDisabledCode)
	)
	cases := []struct {
		name    string
		tool    string
		profile string
		enabled bool
		want    want
	}{
		{"live increase, enabled", toolIncrease, "live", true, executed},
		{"live increase, disabled", toolIncrease, "live", false, blocked},
		{"live control, disabled", toolControl, "live", false, blocked},
		{"live direct order, disabled", toolDirect, "live", false, blocked},
		{"live reduce, disabled: unwinding stays possible", toolReduce, "live", false, executed},
		{"live safety, disabled: the kill switch stays reachable", toolSafety, "live", false, executed},
		{"live read, disabled", toolRead, "live", false, executed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newPermHarness(t)
			h.srv.SetLiveEnabledFunc(func() bool { return tc.enabled })
			resp := h.call(t, mintToken(t, 1, tc.profile, "jti-live"), tc.tool, nil)
			if tc.want == executed {
				require.Nil(t, resp.Error, "%+v", resp.Error)
				assert.EqualValues(t, 1, h.tools[tc.tool].calls.Load())
				return
			}
			require.NotNil(t, resp.Error)
			assert.Equal(t, ErrCodeLiveTradingDisabled, resp.Error.Code)
			assert.Equal(t, LiveTradingDisabledCode, dataCode(t, resp))
			assert.Zero(t, h.tools[tc.tool].calls.Load(), "a blocked call must never execute the tool")
		})
	}

	t.Run("the paper profile is never blocked by the live switch", func(t *testing.T) {
		h := newPermHarness(t)
		h.srv.SetPaperBroker(stubPaperBroker{})
		h.srv.SetLiveEnabledFunc(func() bool { return false })
		resp := h.call(t, mintToken(t, 1, "paper", "jti-paper"), toolIncrease, nil)
		require.Nil(t, resp.Error, "%+v", resp.Error)
	})

	t.Run("never set: open, like before the parameter existed", func(t *testing.T) {
		h := newPermHarness(t)
		resp := h.call(t, mintToken(t, 1, "live", "jti-open"), toolIncrease, nil)
		require.Nil(t, resp.Error, "%+v", resp.Error)
		assert.True(t, h.srv.LiveEnabled())
	})
}

// Paper envelope: the broker must be ready, the call carries the holder, the
// unsupported tools are refused and served responses are marked.
func TestPaperEnvelope(t *testing.T) {
	t.Run("unsupported tool answers PAPER_UNSUPPORTED and never executes", func(t *testing.T) {
		h := newPermHarness(t)
		h.srv.SetPaperBroker(stubPaperBroker{})
		ft := &fakeTool{name: "execute_scalping_trade"}
		require.NoError(t, h.srv.RegisterTool(ft))
		resp := h.call(t, mintToken(t, 1, "paper", "jti-1"), "execute_scalping_trade", nil)
		require.NotNil(t, resp.Error)
		assert.Equal(t, ErrCodePaperUnsupported, resp.Error.Code)
		assert.Equal(t, "PAPER_UNSUPPORTED", dataCode(t, resp))
		assert.Zero(t, ft.calls.Load())
	})

	t.Run("served call is marked paper with the assumptions", func(t *testing.T) {
		h := newPermHarness(t)
		h.srv.SetPaperBroker(stubPaperBroker{})
		resp := h.call(t, mintToken(t, 1, "paper", "jti-2"), toolIncrease, nil)
		require.Nil(t, resp.Error, "%+v", resp.Error)
		raw, _ := json.Marshal(resp.Result)
		var out map[string]interface{}
		require.NoError(t, json.Unmarshal(raw, &out))
		assert.Equal(t, true, out["paper"])
		assert.Equal(t, map[string]interface{}{"slippage_pct": 0.05}, out["paper_assumptions"])
		// The paper broker never touched the exchange: no real/sandbox exchange warning.
		assert.NotContains(t, out, "_warning")
		assert.NotContains(t, out, "_sandbox_mode")
	})

	t.Run("a live response is never marked paper", func(t *testing.T) {
		h := newPermHarness(t)
		h.srv.SetPaperBroker(stubPaperBroker{})
		resp := h.call(t, mintToken(t, 1, "live", "jti-3"), toolIncrease, nil)
		require.Nil(t, resp.Error)
		raw, _ := json.Marshal(resp.Result)
		assert.NotContains(t, string(raw), "paper")
		assert.Contains(t, string(raw), "_warning", "a live order keeps the exchange warning")
	})

	t.Run("broker not ready: paper trade is PAPER_UNAVAILABLE, never the real account", func(t *testing.T) {
		h := newPermHarness(t)
		resp := h.call(t, mintToken(t, 1, "paper", "jti-4"), toolIncrease, nil)
		assert.Equal(t, PaperUnavailableCode, dataCode(t, resp))
		assert.Zero(t, h.tools[toolIncrease].calls.Load())
	})
}

// Task 6.15: secrets in tool arguments never reach the logs.
func TestSecretsAreRedactedInLogs(t *testing.T) {
	h := newPermHarness(t)
	var buf bytes.Buffer
	h.srv.log.SetOutput(&buf)
	h.srv.log.SetLevel(4 /* info */)

	const secret = "SUPER-SECRET-VALUE-12345"
	resp := h.call(t, mintToken(t, 1, "live", "jti-log"), toolRead, map[string]interface{}{
		"api_secret": secret, "passphrase": secret, "password": secret,
		"nested": map[string]interface{}{"token": secret}, "amount": 7,
	})
	require.Nil(t, resp.Error, "%+v", resp.Error)
	assert.NotContains(t, buf.String(), secret, "a secret reached the log")
	assert.Contains(t, buf.String(), "Executing tool", "the log line the test is about did not happen")
	assert.Contains(t, buf.String(), "[REDACTED]")
	assert.Contains(t, redactParams(map[string]interface{}{"amount": 7}), "amount")
}

func TestAuditLogRedactsSecrets(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "audit-*.log")
	require.NoError(t, err)
	defer f.Close()
	a := &AuditLogger{file: f, config: AuditConfig{Enabled: true, LogAllRequests: true, LogAllOrders: true}, log: logrus.New()}

	const secret = "AUDIT-SECRET-98765"
	a.LogRequest(&models.MCPRequest{Method: "tools/call", Params: &models.MCPParams{
		Name: "submit_order", Arguments: map[string]interface{}{"api_key": secret, "amount": 1},
	}}, nil, nil, time.Millisecond)
	a.LogOrder("submit_order", map[string]interface{}{"passphrase": secret, "amount": 1}, nil, nil)

	raw, err := os.ReadFile(f.Name())
	require.NoError(t, err)
	assert.NotEmpty(t, raw)
	assert.NotContains(t, string(raw), secret)
	assert.Contains(t, string(raw), "[REDACTED]")
}

// Task 7.6: in the product edition a live profile needs a Pro license for
// trade_increase and control; reductions, closes, safety and reads never.
func TestLicenseGate(t *testing.T) {
	cases := []struct {
		name    string
		tool    string
		profile string
		allowed bool
		blocked bool
	}{
		{"live increase, licensed", toolIncrease, "live", true, false},
		{"live increase, no license", toolIncrease, "live", false, true},
		{"live control, no license", toolControl, "live", false, true},
		{"live direct order, no license", toolDirect, "live", false, true},
		{"live reduce, no license: closing stays possible", toolReduce, "live", false, false},
		{"live safety, no license", toolSafety, "live", false, false},
		{"live read, no license", toolRead, "live", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newPermHarness(t)
			h.srv.SetLicenseGate(func() bool { return tc.allowed })
			resp := h.call(t, mintToken(t, 1, tc.profile, "jti-lic"), tc.tool, nil)
			if !tc.blocked {
				require.Nil(t, resp.Error, "%+v", resp.Error)
				assert.EqualValues(t, 1, h.tools[tc.tool].calls.Load())
				return
			}
			require.NotNil(t, resp.Error)
			assert.Equal(t, ErrCodeLicenseRequired, resp.Error.Code)
			assert.Equal(t, LicenseRequiredCode, dataCode(t, resp))
			assert.Zero(t, h.tools[tc.tool].calls.Load())
		})
	}

	t.Run("paper is never gated by the license", func(t *testing.T) {
		h := newPermHarness(t)
		h.srv.SetPaperBroker(stubPaperBroker{})
		h.srv.SetLicenseGate(func() bool { return false })
		resp := h.call(t, mintToken(t, 1, "paper", "jti-paper-lic"), toolIncrease, nil)
		require.Nil(t, resp.Error, "%+v", resp.Error)
	})

	t.Run("license checked before live switch", func(t *testing.T) {
		h := newPermHarness(t)
		h.srv.SetLicenseGate(func() bool { return false })
		h.srv.SetLiveEnabledFunc(func() bool { return false })
		resp := h.call(t, mintToken(t, 1, "live", "jti-order"), toolIncrease, nil)
		require.NotNil(t, resp.Error)
		assert.Equal(t, LicenseRequiredCode, dataCode(t, resp))
	})
}
