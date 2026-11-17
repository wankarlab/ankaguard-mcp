package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

type upstreamCall struct {
	path, token, body string
}

func newSessionProxy(t *testing.T) (*gin.Engine, *[]upstreamCall) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	calls := &[]upstreamCall{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*calls = append(*calls, upstreamCall{r.URL.Path, r.Header.Get("X-Auth-Session-Token"), string(b)})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"screen":"ENTER_OTP"}`))
	}))
	t.Cleanup(upstream.Close)

	log := logrus.New()
	log.SetOutput(io.Discard)
	h := NewAuthProxyHandler(upstream.URL, log)
	r := gin.New()
	r.POST("/api/v1/auth/session", h.SessionCreate)
	r.POST("/api/v1/auth/session/:id/answer", h.SessionAnswer)
	r.POST("/api/v1/auth/session/:id/resend", h.SessionResend)
	return r, calls
}

func post(r *gin.Engine, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Auth-Session-Token", token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestAuthProxy_SessionFlowForwardsPathTokenAndBody(t *testing.T) {
	r, calls := newSessionProxy(t)

	steps := []struct{ path, token, body, wantUpstream string }{
		{"/api/v1/auth/session", "", `{"intent":"login_or_register"}`, "/api/v1/auth/session"},
		{"/api/v1/auth/session/63d300fb-d88f-427a/answer", "tok-1", `{"challenge":"identifier"}`, "/api/v1/auth/session/63d300fb-d88f-427a/answer"},
		{"/api/v1/auth/session/63d300fb-d88f-427a/resend", "tok-1", `{"channel":"email"}`, "/api/v1/auth/session/63d300fb-d88f-427a/resend"},
	}
	for i, s := range steps {
		w := post(r, s.path, s.token, s.body)
		if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), "ENTER_OTP") {
			t.Fatalf("step %d: got %d %s, want upstream status and body relayed", i, w.Code, w.Body.String())
		}
		got := (*calls)[i]
		if got.path != s.wantUpstream || got.token != s.token || got.body != s.body {
			t.Errorf("step %d: upstream saw %+v", i, got)
		}
	}
}

func TestAuthProxy_SessionRejectsUnsafeIDs(t *testing.T) {
	r, calls := newSessionProxy(t)
	for _, id := range []string{"..", "a.b", "%2e%2e", "x%2Fy", strings.Repeat("a", 65)} {
		w := post(r, "/api/v1/auth/session/"+id+"/answer", "tok", `{}`)
		// 400 from the id check, or 404 when an encoded slash splits the route.
		if w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
			t.Errorf("id %q: got %d, want 400 or 404", id, w.Code)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("unsafe ids reached the upstream: %+v", *calls)
	}
}
