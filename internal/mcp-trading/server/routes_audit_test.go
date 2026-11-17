package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/config"
	"github.com/rantipay/trading-mcp/internal/routepolicy"
	"github.com/sirupsen/logrus"
)

// TestMuxRoutesRequireAuthUnlessListed sends an unauthenticated request to
// EVERY route mounted on the :10600 mux (task 6.16). A route that answers
// without a token and is not in routepolicy.Open fails the test.
func TestMuxRoutesRequireAuthUnlessListed(t *testing.T) {
	log := logrus.New()
	log.SetOutput(os.Stderr)
	log.SetLevel(logrus.PanicLevel)
	srv := NewMCPServerForTesting(&config.Config{}, log, &TestGCTClient{Connected: true})
	srv.SetJWTAuth(permTestSecret, "", true)
	// Mount the optional route groups too, so the audit sees them.
	srv.SetWebSocketHub(struct{}{})
	srv.aiBotHandler = struct{}{}
	srv.tuningHandler = struct{}{}

	h, patterns := srv.HTTPHandler()
	if len(patterns) < 20 {
		t.Fatalf("only %d routes mounted, the audit would prove nothing", len(patterns))
	}

	mounted := map[string]bool{}
	for _, p := range patterns {
		mounted[p] = true
		path := p
		if strings.HasSuffix(path, "/") && path != "/" {
			path += "x"
		}
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			if _, ok := routepolicy.Find("mcp", method, p); ok {
				continue
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
			if w.Code != http.StatusUnauthorized {
				t.Errorf("%s %s answered %d without a token and is not in routepolicy.Open: protect it or classify it", method, p, w.Code)
			}
		}
	}

	// A stale allow-list entry hides a removed route.
	for _, r := range routepolicy.Open {
		if r.Listener == "mcp" && !mounted[r.Path] {
			t.Errorf("routepolicy lists %s but the mux does not mount it", r.Path)
		}
	}
}

// TestDebugRoutesAreGone proves the header-echoing debug routes were removed.
func TestDebugRoutesAreGone(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	srv := NewMCPServerForTesting(&config.Config{}, log, &TestGCTClient{Connected: true})
	srv.SetJWTAuth(permTestSecret, "", true)
	h, _ := srv.HTTPHandler()
	for _, p := range []string{"/debug", "/test", "/simple-test"} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		req.Header.Set("Authorization", "Bearer secret-token-value")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s answered %d, want 404", p, w.Code)
		}
		if strings.Contains(w.Body.String(), "secret-token-value") {
			t.Errorf("%s echoed the Authorization header", p)
		}
	}
}

// TestPublicHealthExposesOnlyBuildFields: GET /health carries status, version,
// commit and edition and nothing else; the detail is behind authentication.
func TestPublicHealthExposesOnlyBuildFields(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	srv := NewMCPServerForTesting(&config.Config{}, log, &TestGCTClient{Connected: true})
	srv.SetJWTAuth(permTestSecret, "", true)
	srv.SetPublicHealth(func() (string, string, string) { return "1.2.3", "abc123", "community" })
	h, _ := srv.HTTPHandler()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("/health = %d", w.Code)
	}
	var body map[string]interface{}
	if err := jsonDecode(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]interface{}{"status": "ok", "version": "1.2.3", "commit": "abc123", "edition": "community"}
	if len(body) != len(want) {
		t.Fatalf("/health fields = %v, want exactly %v", body, want)
	}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("/health %s = %v, want %v", k, body[k], v)
		}
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health/detail", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("/health/detail without a token = %d, want 401", w.Code)
	}
}

func jsonDecode(b []byte, v interface{}) error { return json.Unmarshal(b, v) }
