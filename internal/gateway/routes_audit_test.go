package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/routepolicy"
	"github.com/sirupsen/logrus"
)

// TestGatewayRoutesRequireAuthUnlessListed sends an unauthenticated request to
// every route of the gateway (task 6.16).
func TestGatewayRoutesRequireAuthUnlessListed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := logrus.New()
	logger.SetLevel(logrus.PanicLevel)
	gw, err := NewGateway(&GatewayConfig{
		Port:                 9090,
		MCPServerURL:         "http://127.0.0.1:1",
		ScalpingAPIURL:       "http://127.0.0.1:1",
		PrometheusURL:        "http://127.0.0.1:1",
		EnableCORS:           true,
		EnableJWTAuth:        true,
		JWTSecret:            "gateway-audit-secret-not-a-real-key",
		InternalGatewayToken: "gateway-audit-token",
	}, logger)
	if err != nil {
		t.Fatal(err)
	}

	routes := gw.router.Routes()
	if len(routes) < 20 {
		t.Fatalf("only %d routes, the audit would prove nothing", len(routes))
	}
	mounted := map[string]bool{}
	for _, ri := range routes {
		mounted[ri.Method+" "+ri.Path] = true
		switch ri.Method {
		case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch:
		default:
			continue
		}
		if _, ok := routepolicy.Find("gateway", ri.Method, ri.Path); ok {
			continue
		}
		path := strings.NewReplacer("*path", "x", "*filepath", "x").Replace(ri.Path)
		w := httptest.NewRecorder()
		gw.router.ServeHTTP(w, httptest.NewRequest(ri.Method, path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s answered %d without a token and is not in routepolicy.Open", ri.Method, ri.Path, w.Code)
		}
	}
	for _, r := range routepolicy.Open {
		if r.Listener == "gateway" && !mounted[r.Method+" "+r.Path] {
			t.Errorf("routepolicy lists gateway %s %s but it is not mounted", r.Method, r.Path)
		}
	}
}
