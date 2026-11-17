package localauth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	tradingmw "github.com/rantipay/trading-mcp/internal/trading/middleware"
)

// The Authority adapter is what the mux, the gin middleware, the gateway and the
// WebSocket handlers call in standalone mode (task 6.3): every layer shares one
// secret, read per request, plus the revocation/epoch rules.
func TestAuthorityDrivesTheSharedValidators(t *testing.T) {
	e := newEnv(t, ModeStandalone)
	_ = e.svc.SetPassword(goodPassword)
	tradingmw.SetLocalAuthority(e.svc.Authority())
	t.Cleanup(func() { tradingmw.SetLocalAuthority(nil) })

	op := e.login(t)
	agent, id := e.mkToken(t, op, "paper")
	const unrelatedStaticSecret = "kong-secret-that-must-not-matter"

	gin.SetMode(gin.TestMode)
	logger := logrus.New()
	logger.SetLevel(logrus.PanicLevel)
	statusAt := func(path, token string) (int, *gin.Context) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", path, nil)
		c.Request.Header.Set("Authorization", "Bearer "+token)
		tradingmw.KongJWTMiddleware(unrelatedStaticSecret, "", logger)(c)
		if c.IsAborted() {
			return w.Code, c
		}
		return http.StatusOK, c
	}
	status := func(token string) (int, *gin.Context) { return statusAt("/x", token) }

	// Gin middleware layer.
	if code, c := status(op); code != 200 || c.GetString("token_use") != "operator_session" {
		t.Fatalf("operator session: %d token_use=%q", code, c.GetString("token_use"))
	}
	// Agent tokens reach the MCP endpoints only (the gateway exposes them as
	// "/" and "/api/mcp"); every REST path answers 403 OPERATOR_REQUIRED.
	for _, p := range []string{"/mcp", "/jsonrpc", "/", "/api/mcp"} {
		if code, c := statusAt(p, agent); code != 200 || c.GetString("mcp_profile") != "paper" {
			t.Fatalf("agent token on %s: %d profile=%q", p, code, c.GetString("mcp_profile"))
		}
	}
	for _, p := range []string{"/x", "/api/v1/orders", "/api/v1/admin/config/params/bulk", "/api/v1/ai-bot/start", "/mcp/extra"} {
		if code, _ := statusAt(p, agent); code != http.StatusForbidden {
			t.Fatalf("agent token on REST path %s: %d, want 403", p, code)
		}
	}
	// Mux layer (requireJWTAuth uses AuthenticateHTTPRequest).
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+agent)
	claims, level, err := tradingmw.AuthenticateHTTPRequest(req, unrelatedStaticSecret, "")
	if err != nil || claims.UserID == "" || level < 1 {
		t.Fatalf("mux layer rejected an agent token: %v", err)
	}
	req = httptest.NewRequest("POST", "/api/v1/ai-bot/start", nil)
	req.Header.Set("Authorization", "Bearer "+agent)
	if _, _, err := tradingmw.AuthenticateHTTPRequest(req, unrelatedStaticSecret, ""); !errors.Is(err, tradingmw.ErrOperatorRequired) {
		t.Fatalf("mux layer accepted an agent token on a REST path: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+op)
	if _, _, err := tradingmw.AuthenticateHTTPRequest(req, unrelatedStaticSecret, ""); err != nil {
		t.Fatalf("mux layer rejected the operator session on a REST path: %v", err)
	}
	// WebSocket layers.
	if _, err := tradingmw.ValidateToken(agent, unrelatedStaticSecret); err != nil {
		t.Fatalf("websocket layer rejected an agent token: %v", err)
	}

	// Revoking the agent token applies to every layer at once.
	if w, _ := e.do("DELETE", BasePath+"/tokens/"+id, op, "", ""); w.Code != 200 {
		t.Fatalf("revoke: %d", w.Code)
	}
	if code, _ := statusAt("/mcp", agent); code != http.StatusUnauthorized {
		t.Fatalf("revoked token still accepted by the gin layer: %d", code)
	}
	if _, err := tradingmw.ValidateToken(agent, unrelatedStaticSecret); err == nil {
		t.Fatal("revoked token still accepted by the websocket layer")
	}

	// Rotating the secret invalidates the operator session everywhere, without
	// rebuilding any middleware.
	if err := RotateSecret(e.dir); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Refresh(); err != nil {
		t.Fatal(err)
	}
	if code, _ := status(op); code != http.StatusUnauthorized {
		t.Fatalf("session still accepted after the rotation: %d", code)
	}
}

// With no authority installed (auth.mode=external) a local token is worthless.
func TestExternalModeIgnoresLocalTokens(t *testing.T) {
	e := newEnv(t, ModeStandalone)
	_ = e.svc.SetPassword(goodPassword)
	op := e.login(t)
	tradingmw.SetLocalAuthority(nil)
	if _, err := tradingmw.ValidateToken(op, "kong-secret"); err == nil {
		t.Fatal("a local token must not validate against the static Kong secret")
	}
}

// The middleware keeps its own copy of the agent token_use value (import
// cycle); both must stay equal or the agent scope check would never match.
func TestAgentTokenUseMatchesMiddleware(t *testing.T) {
	if TokenUseAgent != tradingmw.TokenUseAgent {
		t.Fatalf("localauth %q != middleware %q", TokenUseAgent, tradingmw.TokenUseAgent)
	}
}
