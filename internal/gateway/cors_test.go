package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func newCORSGateway(t *testing.T, origins []string) *Gateway {
	t.Helper()
	gin.SetMode(gin.TestMode)
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	gw, err := NewGateway(&GatewayConfig{
		Port:               9090,
		MCPServerURL:       "http://localhost:10600",
		ScalpingAPIURL:     "http://localhost:8081",
		EnableCORS:         true,
		CORSAllowedOrigins: origins,
	}, logger)
	assert.NoError(t, err)
	return gw
}

func TestGatewayCORSUsesConfiguredList(t *testing.T) {
	gw := newCORSGateway(t, []string{"http://localhost:3002"})

	t.Run("listed origin is echoed", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/health", nil)
		req.Header.Set("Origin", "http://localhost:3002")
		w := httptest.NewRecorder()
		gw.router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "http://localhost:3002", w.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("unlisted origin gets no CORS headers", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/health", nil)
		req.Header.Set("Origin", "https://evil.example")
		w := httptest.NewRecorder()
		gw.router.ServeHTTP(w, req)
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Credentials"))
	})

	t.Run("empty list never sends a wildcard", func(t *testing.T) {
		open := newCORSGateway(t, nil)
		req := httptest.NewRequest("GET", "/health", nil)
		req.Header.Set("Origin", "https://evil.example")
		w := httptest.NewRecorder()
		open.router.ServeHTTP(w, req)
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
	})
}
