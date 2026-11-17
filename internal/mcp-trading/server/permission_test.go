package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/rantipay/trading-mcp/internal/killswitch"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/config"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/toolpolicy"
	tradingmw "github.com/rantipay/trading-mcp/internal/trading/middleware"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const permTestSecret = "permission-test-secret-not-a-real-key"

// fakeTool counts executions so tests can prove a rejected call never ran.
type fakeTool struct {
	name  string
	calls atomic.Int32
}

func (f *fakeTool) Name() string                        { return f.name }
func (f *fakeTool) Description() string                 { return "fake " + f.name }
func (f *fakeTool) InputSchema() map[string]interface{} { return map[string]interface{}{"type": "object"} }
func (f *fakeTool) Execute(_ context.Context, _ map[string]interface{}) (interface{}, error) {
	f.calls.Add(1)
	return map[string]interface{}{"ok": true}, nil
}

// representative real tool names, one per class, plus the argument-dependent one.
var (
	toolRead     = "get_ticker"
	toolIncrease = "submit_order"
	toolReduce   = "cancel_order"
	toolControl  = "set_trading_mode"
	toolSafety   = "activate_kill_switch"
	toolDirect   = "submit_futures_order_direct"
)

type permHarness struct {
	srv   *MCPServer
	tools map[string]*fakeTool
	mux   http.Handler
}

func newPermHarness(t *testing.T) *permHarness {
	t.Helper()
	log := logrus.New()
	log.SetOutput(os.Stderr)
	log.SetLevel(logrus.PanicLevel)
	srv := NewMCPServerForTesting(&config.Config{}, log, &TestGCTClient{Connected: true})
	srv.SetJWTAuth(permTestSecret, "", true)

	h := &permHarness{srv: srv, tools: map[string]*fakeTool{}}
	for _, n := range []string{toolRead, toolIncrease, toolReduce, toolControl, toolSafety, toolDirect, "mystery_tool"} {
		ft := &fakeTool{name: n}
		h.tools[n] = ft
		require.NoError(t, srv.RegisterTool(ft))
	}
	h.mux = http.HandlerFunc(srv.requireJWTAuth(srv.handleHTTPRequest))

	prev := killswitch.Ring0Active()
	killswitch.SetRing0Active(false)
	t.Cleanup(func() { killswitch.SetRing0Active(prev) })
	return h
}

// mintToken signs a JWT. profile "" omits the claim.
func mintToken(t *testing.T, level int, profile, jti string) string {
	t.Helper()
	claims := &tradingmw.KongJWTClaims{
		UserID:     "user-1",
		UserLevel:  float64(level),
		MCPProfile: profile,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        jti,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(permTestSecret))
	require.NoError(t, err)
	return tok
}

func (h *permHarness) rpc(t *testing.T, token string, req models.MCPRequest) models.MCPResponse {
	t.Helper()
	req.JSONRPC = "2.0"
	req.ID = 1
	body, err := json.Marshal(req)
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.mux.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp models.MCPResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp
}

func (h *permHarness) call(t *testing.T, token, tool string, args map[string]interface{}) models.MCPResponse {
	return h.rpc(t, token, models.MCPRequest{Method: "tools/call", Params: &models.MCPParams{Name: tool, Arguments: args}})
}

func (h *permHarness) list(t *testing.T, token string) []string {
	t.Helper()
	body, err := json.Marshal(h.rpc(t, token, models.MCPRequest{Method: "tools/list"}).Result)
	require.NoError(t, err)
	var out models.MCPToolsListResponse
	require.NoError(t, json.Unmarshal(body, &out))
	names := make([]string, 0, len(out.Tools))
	for _, ti := range out.Tools {
		names = append(names, ti.Name)
	}
	sort.Strings(names)
	return names
}

func dataCode(t *testing.T, resp models.MCPResponse) string {
	t.Helper()
	require.NotNil(t, resp.Error)
	data, ok := resp.Error.Data.(map[string]interface{})
	if !ok {
		return ""
	}
	code, _ := data["code"].(string)
	return code
}

// TestPermissionMatrix covers every (class, profile) cell through the real
// auth middleware and dispatcher.
func TestPermissionMatrix(t *testing.T) {
	type expect string
	const (
		executed expect = "executed"
		denied   expect = PermissionDeniedCode
		paperOff expect = PaperUnavailableCode
	)
	cases := []struct {
		tool    string
		profile string
		want    expect
	}{
		{toolRead, "read", executed}, {toolRead, "paper", executed}, {toolRead, "live", executed},
		{toolIncrease, "read", denied}, {toolIncrease, "paper", paperOff}, {toolIncrease, "live", executed},
		{toolReduce, "read", denied}, {toolReduce, "paper", paperOff}, {toolReduce, "live", executed},
		{toolControl, "read", denied}, {toolControl, "paper", denied}, {toolControl, "live", executed},
		{toolSafety, "read", denied}, {toolSafety, "paper", executed}, {toolSafety, "live", executed},
	}
	for _, tc := range cases {
		t.Run(tc.tool+"/"+tc.profile, func(t *testing.T) {
			h := newPermHarness(t)
			// Level 1: the profile claim alone decides.
			resp := h.call(t, mintToken(t, 1, tc.profile, "jti-matrix"), tc.tool, nil)
			if tc.want == executed {
				require.Nil(t, resp.Error, "unexpected error: %+v", resp.Error)
				assert.EqualValues(t, 1, h.tools[tc.tool].calls.Load())
				return
			}
			assert.Equal(t, string(tc.want), dataCode(t, resp))
			assert.Zero(t, h.tools[tc.tool].calls.Load(), "a rejected call must never execute the tool")
		})
	}
}

func TestPermissionDeniedPayload(t *testing.T) {
	h := newPermHarness(t)
	resp := h.call(t, mintToken(t, 1, "read", "jti-1"), toolIncrease, nil)
	require.NotNil(t, resp.Error)
	assert.Equal(t, ErrCodePermissionDenied, resp.Error.Code)
	data := resp.Error.Data.(map[string]interface{})
	assert.Equal(t, "PERMISSION_DENIED", data["code"])
	assert.Equal(t, toolIncrease, data["tool"])
	assert.Equal(t, "trade_increase", data["class"])
	assert.Equal(t, "read", data["profile"])
}

func TestProfilelessTokens(t *testing.T) {
	cases := []struct {
		name    string
		level   int
		profile string
		allowed bool
	}{
		{"level 2 without profile is read", 2, "", false},
		{"level 3 without profile is read", 3, "", false},
		{"level 4 without profile is live", 4, "", true},
		{"unknown profile is read even at level 4", 4, "superuser", false},
		{"explicit read at level 4 stays read", 4, "read", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newPermHarness(t)
			resp := h.call(t, mintToken(t, tc.level, tc.profile, "jti-2"), toolIncrease, nil)
			if tc.allowed {
				require.Nil(t, resp.Error, "%+v", resp.Error)
				assert.EqualValues(t, 1, h.tools[toolIncrease].calls.Load())
			} else {
				assert.Equal(t, PermissionDeniedCode, dataCode(t, resp))
				assert.Zero(t, h.tools[toolIncrease].calls.Load())
			}
		})
	}
}

func TestUnclassifiedToolIsNeverListedNorExecuted(t *testing.T) {
	h := newPermHarness(t)
	assert.Equal(t, []string{"mystery_tool"}, h.srv.UnclassifiedTools())
	assert.Equal(t, []string{"mystery_tool"}, h.srv.AuditToolClassification())

	for _, profile := range []string{"read", "paper", "live"} {
		token := mintToken(t, 1, profile, "jti-3")
		assert.NotContains(t, h.list(t, token), "mystery_tool", profile)
		resp := h.call(t, token, "mystery_tool", nil)
		assert.Equal(t, PermissionDeniedCode, dataCode(t, resp), profile)
		assert.Equal(t, "unclassified", resp.Error.Data.(map[string]interface{})["class"])
	}
	assert.Zero(t, h.tools["mystery_tool"].calls.Load())
}

func TestToolsListFilteredByProfile(t *testing.T) {
	h := newPermHarness(t)
	all := func(extra ...string) []string {
		s := append([]string{toolRead}, extra...)
		sort.Strings(s)
		return s
	}
	assert.Equal(t, all(), h.list(t, mintToken(t, 1, "read", "j")))
	// paper: read + trade_* + safety, never control.
	assert.Equal(t, all(toolIncrease, toolReduce, toolSafety, toolDirect), h.list(t, mintToken(t, 1, "paper", "j")))
	assert.Equal(t, all(toolIncrease, toolReduce, toolSafety, toolDirect, toolControl), h.list(t, mintToken(t, 1, "live", "j")))
	// Profile derived from level.
	assert.Equal(t, all(), h.list(t, mintToken(t, 2, "", "j")))
	assert.Equal(t, all(toolIncrease, toolReduce, toolSafety, toolDirect, toolControl), h.list(t, mintToken(t, 4, "", "j")))
}

func TestKillSwitchOnlyBlocksTradeIncrease(t *testing.T) {
	h := newPermHarness(t)
	live := mintToken(t, 4, "live", "jti-ks")
	killswitch.SetRing0Active(true)

	// Opening is refused by the stop.
	resp := h.call(t, live, toolIncrease, nil)
	require.NotNil(t, resp.Error)
	assert.Equal(t, killswitch.ErrorCode, resp.Error.Message)
	assert.Zero(t, h.tools[toolIncrease].calls.Load())

	// Direct futures order: opening refused, reduce-only / close executes.
	resp = h.call(t, live, toolDirect, map[string]interface{}{"side": "buy"})
	require.NotNil(t, resp.Error)
	assert.Equal(t, killswitch.ErrorCode, resp.Error.Message)
	require.Nil(t, h.call(t, live, toolDirect, map[string]interface{}{"reduce_only": true}).Error)
	require.Nil(t, h.call(t, live, toolDirect, map[string]interface{}{"close_order": true}).Error)
	assert.EqualValues(t, 2, h.tools[toolDirect].calls.Load())

	// Closing, safety and read stay available.
	for _, n := range []string{toolReduce, toolSafety, toolRead} {
		require.Nil(t, h.call(t, live, n, nil).Error, n)
	}
}

// Permission is checked before the kill switch: a read token must learn it is
// not allowed, not that the stop is active.
func TestPermissionCheckedBeforeKillSwitch(t *testing.T) {
	h := newPermHarness(t)
	killswitch.SetRing0Active(true)
	resp := h.call(t, mintToken(t, 1, "read", "jti-order"), toolIncrease, nil)
	assert.Equal(t, PermissionDeniedCode, dataCode(t, resp))
}

// A paper token must be rejected before the tool runs, with or without the
// global stop, until the paper broker exists (hook: paperBrokerReady).
func TestPaperTradeNeverExecutesWithoutBroker(t *testing.T) {
	h := newPermHarness(t)
	paper := mintToken(t, 1, "paper", "jti-paper")
	for _, n := range []string{toolIncrease, toolReduce} {
		resp := h.call(t, paper, n, nil)
		assert.Equal(t, PaperUnavailableCode, dataCode(t, resp), n)
		assert.Equal(t, ErrCodePaperUnavailable, resp.Error.Code)
		assert.Zero(t, h.tools[n].calls.Load(), n)
	}
	resp := h.call(t, paper, toolDirect, map[string]interface{}{"reduce_only": true})
	assert.Equal(t, PaperUnavailableCode, dataCode(t, resp))
	assert.Zero(t, h.tools[toolDirect].calls.Load())
}

// Calls with no identity: stdio and ENABLE_JWT_AUTH=false keep the pre-profile
// behaviour (live); an enabled-auth server without identity fails closed.
func TestCallerWithoutIdentity(t *testing.T) {
	h := newPermHarness(t)

	p, id := h.srv.callerFromContext(context.Background())
	assert.Equal(t, toolpolicy.ProfileRead, p, "auth enabled and no identity: fail closed")
	assert.Equal(t, "unauthenticated", id)

	p, _ = h.srv.callerFromContext(withStdioIdentity(context.Background()))
	assert.Equal(t, toolpolicy.ProfileLive, p)

	h.srv.SetJWTAuth("", "", false)
	p, id = h.srv.callerFromContext(context.Background())
	assert.Equal(t, toolpolicy.ProfileLive, p)
	assert.Equal(t, "auth-disabled", id)
}

// TestDenialIsAuditedWithTokenIdentifier proves the denial reaches the audit
// log with the jti (or subject) and never the token itself.
func TestDenialIsAuditedWithTokenIdentifier(t *testing.T) {
	h := newPermHarness(t)
	path := filepath.Join(t.TempDir(), "audit.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	h.srv.auditLogger = &AuditLogger{file: f, config: AuditConfig{Enabled: true}, log: logrus.New()}

	token := mintToken(t, 1, "read", "jti-audit-7")
	require.NotNil(t, h.call(t, token, toolIncrease, nil).Error)
	// Token without jti falls back to the subject.
	noJTI := mintToken(t, 1, "read", "")
	require.NotNil(t, h.call(t, noJTI, toolControl, nil).Error)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	text := string(raw)
	lines := strings.Split(strings.TrimSpace(text), "\n")
	require.Len(t, lines, 2)

	var e1, e2 AuditEntry
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &e1))
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &e2))
	assert.Equal(t, "permission_denied", e1.Type)
	assert.Equal(t, "jti:jti-audit-7", e1.TokenID)
	assert.Equal(t, toolIncrease, e1.ToolName)
	assert.Equal(t, "trade_increase", e1.Class)
	assert.Equal(t, "read", e1.Profile)
	assert.False(t, e1.Timestamp.IsZero())
	assert.Equal(t, "sub:user-1", e2.TokenID)

	assert.NotContains(t, text, token)
	assert.NotContains(t, text, noJTI)
}
