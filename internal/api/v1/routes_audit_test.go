package v1

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/localauth"
	"github.com/rantipay/trading-mcp/internal/productapi"
	"github.com/rantipay/trading-mcp/internal/routepolicy"
	"github.com/sirupsen/logrus"
)

// TestRESTRoutesRequireAuthUnlessListed sends an unauthenticated request to
// every route this router mounts, plus the product and local-auth routes main.go
// mounts through its hooks (task 6.16).
func TestRESTRoutesRequireAuthUnlessListed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := logrus.New()
	logger.SetLevel(logrus.PanicLevel)

	auth, err := localauth.New(localauth.Config{Mode: localauth.ModeExternal, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	prod := productapi.New(productapi.Config{AuthMode: "external", Build: "standard", Log: logger})

	r, err := NewRouter(&RouterConfig{
		JWTSecret:            "rest-audit-secret-not-a-real-key",
		InternalGatewayToken: "rest-audit-gateway-token",
		Logger:               logger,
		PublicRoutes: func(e *gin.Engine) {
			auth.RegisterRoutes(e.Group("/api/v1/auth/local"))
		},
		ProductRoutes: prod.RegisterRoutes,
	})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	engine.Use(gin.Recovery())
	r.RegisterRoutes(engine)

	routes := engine.Routes()
	if len(routes) < 40 {
		t.Fatalf("only %d routes, the audit would prove nothing", len(routes))
	}
	sawProduct := false
	mounted := map[string]bool{}
	for _, ri := range routes {
		mounted[ri.Method+" "+ri.Path] = true
		if strings.HasPrefix(ri.Path, "/api/v1/paper/") || ri.Path == "/api/v1/product/status" {
			sawProduct = true
		}
		switch ri.Method {
		case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch:
		default:
			continue
		}
		if _, ok := routepolicy.Find("rest", ri.Method, ri.Path); ok {
			continue
		}
		path := strings.NewReplacer("*path", "x", ":holder", "h", ":id", "1", ":pair", "BTC-USDT").Replace(ri.Path)
		// remaining :params
		for _, seg := range strings.Split(path, "/") {
			if strings.HasPrefix(seg, ":") {
				path = strings.Replace(path, seg, "x", 1)
			}
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest(ri.Method, path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s answered %d without a token and is not in routepolicy.Open", ri.Method, ri.Path, w.Code)
		}
	}
	if !sawProduct {
		t.Error("the product routes were not mounted through ProductRoutes")
	}
	// The local login routes must be reachable without a token (they are the way in).
	if !mounted["POST /api/v1/auth/local/login"] {
		t.Error("the local login route was not mounted through PublicRoutes")
	}
	for _, rule := range routepolicy.Open {
		if rule.Listener == "rest" && rule.Class != routepolicy.Pending && strings.HasPrefix(rule.Path, "/api/v1/auth/local") && !mounted[rule.Method+" "+rule.Path] {
			t.Errorf("routepolicy lists %s %s but it is not mounted", rule.Method, rule.Path)
		}
	}
}
