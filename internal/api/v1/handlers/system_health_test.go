package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	tradingMiddleware "github.com/rantipay/trading-mcp/internal/trading/middleware"
)

const healthTestSecret = "test-secret-for-system-health"

func fakeMCP(t *testing.T, gctConnected bool) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"status":"ok","gct_connected":%v,"sandbox":{"engine_real_money":true}}`, gctConnected)
	}))
	t.Cleanup(s.Close)
	return s
}

func fakeOK(t *testing.T) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"status":"ok"}`)
	}))
	t.Cleanup(s.Close)
	return s
}

func fakeSlow(t *testing.T) *httptest.Server {
	t.Helper()
	done := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-done:
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	t.Cleanup(func() { close(done); s.Close() })
	return s
}

func newHealthTestRouter(cfg SystemHealthConfig) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := NewSystemHealthHandler(cfg, logrus.New())
	r := gin.New()
	g := r.Group("/api/v1")
	g.Use(tradingMiddleware.KongJWTMiddleware(healthTestSecret, "", logrus.New()))
	g.GET("/system/health", h.GetSystemHealth)
	return r
}

type healthBody struct {
	Data struct {
		Status     string `json:"status"`
		Components []struct {
			Name    string `json:"name"`
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"components"`
	} `json:"data"`
}

func getHealth(t *testing.T, r *gin.Engine, auth string) (int, healthBody, time.Duration) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/health", nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	start := time.Now()
	r.ServeHTTP(w, req)
	elapsed := time.Since(start)
	var b healthBody
	_ = json.Unmarshal(w.Body.Bytes(), &b)
	return w.Code, b, elapsed
}

func (b healthBody) component(name string) (status, msg string) {
	for _, c := range b.Data.Components {
		if c.Name == name {
			return c.Status, c.Message
		}
	}
	return "", ""
}

func healthyCfg(t *testing.T) SystemHealthConfig {
	return SystemHealthConfig{
		MCPHealthURL:  fakeMCP(t, true).URL,
		MLHealthURL:   fakeOK(t).URL,
		PushHealthURL: fakeOK(t).URL,
		ConfigDBPing:  func(context.Context) error { return nil },
	}
}

func authHeader(t *testing.T) string {
	return "Bearer " + mintStatusJWT(t, healthTestSecret, time.Hour)
}

func TestSystemHealthAllOK(t *testing.T) {
	r := newHealthTestRouter(healthyCfg(t))
	code, b, _ := getHealth(t, r, authHeader(t))
	if code != http.StatusOK || b.Data.Status != "ok" {
		t.Fatalf("code=%d status=%q body=%+v", code, b.Data.Status, b)
	}
}

func TestSystemHealthMLDownDegraded(t *testing.T) {
	cfg := healthyCfg(t)
	ml := fakeOK(t)
	cfg.MLHealthURL = ml.URL
	ml.Close()
	r := newHealthTestRouter(cfg)
	_, b, _ := getHealth(t, r, authHeader(t))
	if b.Data.Status != "degraded" {
		t.Fatalf("want degraded, got %q", b.Data.Status)
	}
	if st, _ := b.component("ml_server"); st != "down" {
		t.Fatalf("ml_server want down, got %q", st)
	}
}

func TestSystemHealthGCTDisconnectedDown(t *testing.T) {
	cfg := healthyCfg(t)
	cfg.MCPHealthURL = fakeMCP(t, false).URL
	r := newHealthTestRouter(cfg)
	_, b, _ := getHealth(t, r, authHeader(t))
	if b.Data.Status != "down" {
		t.Fatalf("want down, got %q", b.Data.Status)
	}
}

func TestSystemHealthConfigDBDown(t *testing.T) {
	cfg := healthyCfg(t)
	cfg.ConfigDBPing = func(context.Context) error { return fmt.Errorf("conn refused") }
	r := newHealthTestRouter(cfg)
	_, b, _ := getHealth(t, r, authHeader(t))
	if b.Data.Status != "down" {
		t.Fatalf("want down, got %q", b.Data.Status)
	}
}

func TestSystemHealthSlowProbeTimesOut(t *testing.T) {
	cfg := healthyCfg(t)
	cfg.PushHealthURL = fakeSlow(t).URL
	r := newHealthTestRouter(cfg)
	code, b, elapsed := getHealth(t, r, authHeader(t))
	if code != http.StatusOK {
		t.Fatalf("code %d", code)
	}
	if elapsed >= 3*time.Second {
		t.Fatalf("response took %v, want < 3s", elapsed)
	}
	st, msg := b.component("notificationpush")
	if st != "down" || msg != "timeout" {
		t.Fatalf("notificationpush want down/timeout, got %q/%q", st, msg)
	}
	if b.Data.Status != "degraded" {
		t.Fatalf("slow non-critical probe: want degraded, got %q", b.Data.Status)
	}
}

func TestSystemHealthSlowCriticalProbeDown(t *testing.T) {
	cfg := healthyCfg(t)
	cfg.MCPHealthURL = fakeSlow(t).URL
	r := newHealthTestRouter(cfg)
	_, b, elapsed := getHealth(t, r, authHeader(t))
	if elapsed >= 3*time.Second || b.Data.Status != "down" {
		t.Fatalf("elapsed=%v status=%q", elapsed, b.Data.Status)
	}
	if _, msg := b.component("gocryptotrader"); msg != "mcp unreachable: timeout" {
		t.Fatalf("unexpected message %q", msg)
	}
}

func TestSystemHealthAnonymous401(t *testing.T) {
	r := newHealthTestRouter(healthyCfg(t))
	if code, _, _ := getHealth(t, r, ""); code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", code)
	}
}

// Task 6.9: the public /health no longer carries gct_connected, so the
// aggregator reads the same data in-process and must not need the URL.
func TestSystemHealthInProcessMCP(t *testing.T) {
	for _, connected := range []bool{true, false} {
		cfg := healthyCfg(t)
		cfg.MCPHealthURL = "" // would be "not configured" without the in-process hook
		cfg.MCPHealthFunc = func() (bool, bool) { return connected, false }
		r := newHealthTestRouter(cfg)
		_, b, _ := getHealth(t, r, authHeader(t))
		st, _ := b.component("gocryptotrader")
		want := "down"
		if connected {
			want = "ok"
		}
		if st != want {
			t.Errorf("connected=%v: gocryptotrader = %q, want %q", connected, st, want)
		}
	}
}

// The product edition ships no ML server or notificationpush: with
// OmitAuxiliary they are not listed and cannot degrade the global status.
func TestSystemHealthOmitAuxiliary(t *testing.T) {
	cfg := healthyCfg(t)
	cfg.MLHealthURL, cfg.PushHealthURL = "", ""
	cfg.OmitAuxiliary = true
	r := newHealthTestRouter(cfg)
	_, b, _ := getHealth(t, r, authHeader(t))
	if b.Data.Status != "ok" {
		t.Fatalf("want ok, got %q", b.Data.Status)
	}
	for _, name := range []string{"ml_server", "notificationpush"} {
		if st, _ := b.component(name); st != "" {
			t.Fatalf("%s must not be listed, got %q", name, st)
		}
	}
	if len(b.Data.Components) != 3 {
		t.Fatalf("want 3 components, got %d", len(b.Data.Components))
	}
}
