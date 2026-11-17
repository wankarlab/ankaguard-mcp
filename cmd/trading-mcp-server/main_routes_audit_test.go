package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	apiv1handlers "github.com/rantipay/trading-mcp/internal/api/v1/handlers"
	"github.com/rantipay/trading-mcp/internal/handlers"
	"github.com/rantipay/trading-mcp/internal/routepolicy"
	tradingmw "github.com/rantipay/trading-mcp/internal/trading/middleware"
)

// M2: audit of the routes main.go mounts directly on the REST engine
// (legacy handlers, fee ledger, strategy validation), with the same helpers
// main.go uses. Product: legacy handlers absent, the rest behind the JWT.
// Standard: unchanged (the routes are listed as Pending in routepolicy).
func TestMainMountedRESTRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	jwt := tradingmw.KongJWTMiddleware("main-audit-secret-not-a-real-key", "", log)

	build := func(p productProfile) *gin.Engine {
		e := gin.New()
		e.Use(gin.Recovery())
		mountLegacyRoutes(e, p,
			handlers.NewAnalysisHandler(nil, nil, nil, log),
			handlers.NewMonitoringHandler(nil, log),
			handlers.NewKnowledgeHandler(nil, log),
			handlers.NewValidationHandler(nil, log),
			handlers.NewStrategyHandler(nil, log),
			handlers.NewMetricsHandler(nil, log),
		)
		handlers.NewStrategyValidationHandler(nil, nil).RegisterRoutesGin(productGuarded(e, p, jwt))
		apiv1handlers.NewFeeLedgerHandler(nil, log).RegisterRoutes(productGuarded(e, p, jwt).Group("/api/v1"))
		return e
	}

	// Product.
	prod := build(newProductProfile(true))
	routes := prod.Routes()
	if len(routes) != 5 {
		t.Fatalf("product mounts %d routes, want 5 (fees x3, strategy-validation x2): %v", len(routes), routes)
	}
	for _, r := range routes {
		w := httptest.NewRecorder()
		prod.ServeHTTP(w, httptest.NewRequest(r.Method, strings.ReplaceAll(r.Path, ":", "x"), nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("product %s %s answered %d without a token", r.Method, r.Path, w.Code)
		}
	}

	// Standard: every mounted route is in the allow-list (known, unchanged).
	std := build(newProductProfile(false))
	for _, r := range std.Routes() {
		if _, ok := routepolicy.Find("rest", r.Method, r.Path); !ok {
			t.Errorf("standard %s %s is mounted without a token and missing from routepolicy.Open", r.Method, r.Path)
		}
	}
	if len(std.Routes()) <= len(routes) {
		t.Fatal("standard build lost its legacy routes")
	}
}
