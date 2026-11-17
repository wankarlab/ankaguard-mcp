package websocket

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"

	tradingmw "github.com/rantipay/trading-mcp/internal/trading/middleware"
)

const wsTestSecret = "ws-auth-test-secret-not-a-real-key"

func newAuthServer(t *testing.T, require bool) *httptest.Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := NewHub(logger)
	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)
	h := NewHandler(hub, logger)
	h.SetJWTSecret(wsTestSecret)
	h.SetRequireAuth(require)
	srv := httptest.NewServer(http.HandlerFunc(h.HandleWebSocketHTTP))
	t.Cleanup(func() { srv.Close(); cancel() })
	return srv
}

func wsToken(t *testing.T, secret string) string {
	t.Helper()
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "user-1", "user_id": "user-1", "exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func dial(srv *httptest.Server, query string, header http.Header) (*websocket.Conn, *http.Response, error) {
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws" + query
	return websocket.DefaultDialer.Dial(u, header)
}

// Task 6.8: /ws validates the JWT for real before upgrading.
func TestWebSocketRequiresAValidTokenWhenAuthIsOn(t *testing.T) {
	srv := newAuthServer(t, true)

	if c, resp, err := dial(srv, "", nil); err == nil {
		c.Close()
		t.Fatal("anonymous connection accepted")
	} else if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous: %v %v", err, resp)
	}
	if c, _, err := dial(srv, "?token=not-a-jwt", nil); err == nil {
		c.Close()
		t.Fatal("garbage token accepted")
	}
	if c, _, err := dial(srv, "?token="+wsToken(t, "another-secret"), nil); err == nil {
		c.Close()
		t.Fatal("token signed with another secret accepted")
	}

	// The console passes the token in the query string (browsers cannot set headers).
	c, _, err := dial(srv, "?token="+wsToken(t, wsTestSecret), nil)
	if err != nil {
		t.Fatalf("valid token in the query rejected: %v", err)
	}
	c.Close()

	// Programmatic clients may use the header.
	c, _, err = dial(srv, "", http.Header{"Authorization": []string{"Bearer " + wsToken(t, wsTestSecret)}})
	if err != nil {
		t.Fatalf("valid token in the header rejected: %v", err)
	}
	c.Close()
}

func TestWebSocketStaysOpenWhenAuthIsOff(t *testing.T) {
	srv := newAuthServer(t, false)
	c, _, err := dial(srv, "", nil)
	if err != nil {
		t.Fatalf("with ENABLE_JWT_AUTH=false the previous behaviour must remain: %v", err)
	}
	c.Close()
}

// Standalone mode: the local authority's secret decides, not the static one.
func TestWebSocketUsesTheLocalAuthoritySecret(t *testing.T) {
	tradingmw.SetLocalAuthority(testAuthority{secret: "local-ws-secret"})
	t.Cleanup(func() { tradingmw.SetLocalAuthority(nil) })
	srv := newAuthServer(t, true)

	if c, _, err := dial(srv, "?token="+wsToken(t, wsTestSecret), nil); err == nil {
		c.Close()
		t.Fatal("static-secret token accepted in standalone mode")
	}
	c, _, err := dial(srv, "?token="+wsToken(t, "local-ws-secret"), nil)
	if err != nil {
		t.Fatalf("local-secret token rejected: %v", err)
	}
	c.Close()
}

type testAuthority struct{ secret string }

func (a testAuthority) Secret() string                           { return a.secret }
func (a testAuthority) Authorize(*tradingmw.KongJWTClaims) error { return nil }
