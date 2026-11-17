package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/config"
	tradingmw "github.com/rantipay/trading-mcp/internal/trading/middleware"
)

// fakeAuthority stands in for the localauth authority (standalone mode).
type fakeAuthority struct{}

func (fakeAuthority) Secret() string                           { return permTestSecret }
func (fakeAuthority) Authorize(*tradingmw.KongJWTClaims) error { return nil }

func mintUseToken(t *testing.T, level int, profile, use string) string {
	t.Helper()
	claims := &tradingmw.KongJWTClaims{
		UserID: "agent:1", UserLevel: float64(level), MCPProfile: profile, TokenUse: use,
		RegisteredClaims: jwt.RegisteredClaims{ID: "j1", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(permTestSecret))
	require.NoError(t, err)
	return tok
}

// A1: in standalone mode an agent token reaches the JSON-RPC endpoints of the
// :10600 mux only; every REST route answers 403 OPERATOR_REQUIRED. External
// mode (no local authority) is unchanged.
func TestAgentTokenOnlyReachesMCP(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	srv := NewMCPServerForTesting(&config.Config{}, log, &TestGCTClient{Connected: true})
	srv.SetJWTAuth(permTestSecret, "", true)
	srv.aiBotHandler = struct{}{}
	srv.tuningHandler = struct{}{}
	h, _ := srv.HTTPHandler()

	agent := mintUseToken(t, 3, "live", tradingmw.TokenUseAgent)
	operator := mintUseToken(t, 4, "live", "operator_session")
	do := func(method, path, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	restPaths := []string{"/api/v1/ai-bot/start", "/api/v1/ai-bot/close-all-positions", "/api/v1/bot/tuning", "/tools", "/health/detail", "/metrics", "/api/v1/backtesting/run-backtest"}

	tradingmw.SetLocalAuthority(fakeAuthority{})
	t.Cleanup(func() { tradingmw.SetLocalAuthority(nil) })
	for _, p := range restPaths {
		w := do(http.MethodPost, p, agent)
		require.Equal(t, http.StatusForbidden, w.Code, "%s: %s", p, w.Body.String())
		var body map[string]string
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		require.Equal(t, tradingmw.OperatorRequiredCode, body["code"], p)
	}
	// The operator session is not affected.
	require.Equal(t, http.StatusOK, do(http.MethodGet, "/tools", operator).Code)
	for _, p := range []string{"/mcp", "/jsonrpc", "/"} {
		w := do(http.MethodPost, p, agent)
		require.Equal(t, http.StatusOK, w.Code, "%s: %s", p, w.Body.String())
	}

	// External mode: no token_use rule at all.
	tradingmw.SetLocalAuthority(nil)
	w := do(http.MethodGet, "/tools", agent)
	require.NotEqual(t, http.StatusForbidden, w.Code, w.Body.String())
}
