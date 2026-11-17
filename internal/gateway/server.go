package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

// APIGateway represents the API Gateway server
type APIGateway interface {
	// Start starts the API Gateway server
	Start(ctx context.Context) error

	// Stop stops the API Gateway server
	Stop(ctx context.Context) error
}

// HealthManager interface for health checks
type HealthManager interface {
	CheckAll(ctx context.Context) interface{}
}

// apiGateway is the concrete implementation
type apiGateway struct {
	config        *GatewayConfig
	server        *http.Server
	logger        *logrus.Logger
	healthManager HealthManager
}

// NewAPIGateway creates a new API Gateway
func NewAPIGateway(config *GatewayConfig, logger *logrus.Logger) APIGateway {
	if logger == nil {
		logger = logrus.New()
	}

	if config == nil {
		config = &GatewayConfig{
			Port:           9090,
			MCPServerURL:   "http://localhost:10600",
			ScalpingAPIURL: "http://localhost:8081",
		}
	}

	return &apiGateway{
		config:        config,
		logger:        logger,
		healthManager: nil, // Will be set via SetHealthManager
	}
}

// SetHealthManager sets the health manager for the gateway
func (gw *apiGateway) SetHealthManager(hm HealthManager) {
	gw.healthManager = hm
}

// Start starts the API Gateway server
func (gw *apiGateway) Start(ctx context.Context) error {
	// Set Gin mode
	gin.SetMode(gin.ReleaseMode)

	router := gin.New()

	// Add middleware
	router.Use(gin.Logger())
	router.Use(gin.Recovery())

	// CORS configuration for Flutter
	corsConfig := cors.DefaultConfig()
	corsConfig.AllowAllOrigins = true
	corsConfig.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	corsConfig.AllowHeaders = []string{"Origin", "Content-Length", "Content-Type", "Authorization"}
	router.Use(cors.New(corsConfig))

	// Health check endpoint
	router.GET("/health", gw.handleHealth)

	// API Gateway info endpoint
	router.GET("/gateway/info", gw.handleGatewayInfo)

	// Prometheus metrics endpoint
	router.GET("/metrics", gw.handleMetrics)

	// MCP Server proxy routes
	mcpGroup := router.Group("/mcp")
	{
		mcpProxy := gw.createReverseProxy(gw.config.MCPServerURL)
		mcpGroup.Any("/*path", gin.WrapH(mcpProxy))
	}

	// Scalping API proxy routes
	scalpingGroup := router.Group("/scalping")
	{
		scalpingProxy := gw.createReverseProxy(gw.config.ScalpingAPIURL)
		scalpingGroup.Any("/*path", gin.WrapH(scalpingProxy))
	}

	// Trading API routes (unified interface)
	tradingGroup := router.Group("/api/v1/trading")
	{
		// Portfolio endpoints (proxy to MCP)
		portfolioGroup := tradingGroup.Group("/portfolio")
		{
			portfolioProxy := gw.createReverseProxy(gw.config.MCPServerURL)
			portfolioGroup.Any("/*path", gin.WrapH(http.StripPrefix("/api/v1/trading/portfolio", portfolioProxy)))
		}

		// Scalping endpoints (proxy to Scalping API)
		scalpingTradingGroup := tradingGroup.Group("/scalping")
		{
			scalpingTradingProxy := gw.createReverseProxy(gw.config.ScalpingAPIURL + "/api/v1/scalping")
			scalpingTradingGroup.Any("/*path", gin.WrapH(http.StripPrefix("/api/v1/trading/scalping", scalpingTradingProxy)))
		}

		// Orders endpoints (proxy to MCP)
		ordersGroup := tradingGroup.Group("/orders")
		{
			ordersProxy := gw.createReverseProxy(gw.config.MCPServerURL)
			ordersGroup.Any("/*path", gin.WrapH(http.StripPrefix("/api/v1/trading/orders", ordersProxy)))
		}

		// Market data endpoints (proxy to MCP)
		marketGroup := tradingGroup.Group("/market")
		{
			marketProxy := gw.createReverseProxy(gw.config.MCPServerURL)
			marketGroup.Any("/*path", gin.WrapH(http.StripPrefix("/api/v1/trading/market", marketProxy)))
		}
	}

	gw.server = &http.Server{
		Addr:         fmt.Sprintf("0.0.0.0:%d", gw.config.Port),
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	gw.logger.WithFields(logrus.Fields{
		"port":         gw.config.Port,
		"mcp_server":   gw.config.MCPServerURL,
		"scalping_api": gw.config.ScalpingAPIURL,
		"address":      fmt.Sprintf("0.0.0.0:%d", gw.config.Port),
	}).Info("Starting API Gateway")

	go func() {
		var serveErr error
		if gw.config.TLSEnabled && gw.config.TLSCertFile != "" && gw.config.TLSKeyFile != "" {
			gw.logger.WithFields(logrus.Fields{
				"cert_file": gw.config.TLSCertFile,
				"key_file":  gw.config.TLSKeyFile,
			}).Info("API Gateway starting with TLS")
			serveErr = gw.server.ListenAndServeTLS(gw.config.TLSCertFile, gw.config.TLSKeyFile)
		} else {
			serveErr = gw.server.ListenAndServe()
		}
		if serveErr != nil && serveErr != http.ErrServerClosed {
			gw.logger.WithError(serveErr).Error("API Gateway server error")
		}
	}()

	return nil
}

// Stop stops the API Gateway server
func (gw *apiGateway) Stop(ctx context.Context) error {
	if gw.server == nil {
		return nil
	}

	gw.logger.Info("Stopping API Gateway")

	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	return gw.server.Shutdown(shutdownCtx)
}

// createReverseProxy creates a reverse proxy for the given target URL
func (gw *apiGateway) createReverseProxy(targetURL string) *httputil.ReverseProxy {
	target, err := url.Parse(targetURL)
	if err != nil {
		gw.logger.WithError(err).Errorf("Failed to parse target URL: %s", targetURL)
		return nil
	}

	proxy := httputil.NewSingleHostReverseProxy(target)

	// Custom director to modify requests
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		// Add custom headers if needed
		req.Header.Set("X-Forwarded-By", "TradingGateway")
	}

	// Custom error handler
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		gw.logger.WithError(err).Errorf("Proxy error for %s", r.URL.Path)
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":"Service temporarily unavailable","code":502}`))
	}

	return proxy
}

// handleHealth handles health check requests
func (gw *apiGateway) handleHealth(c *gin.Context) {
	if gw.healthManager == nil {
		// Fallback to simple health check
		c.JSON(http.StatusOK, gin.H{
			"status":    "healthy",
			"service":   "api-gateway",
			"timestamp": time.Now().Format(time.RFC3339),
			"version":   "1.0.0",
		})
		return
	}

	// Use health manager for comprehensive check
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	systemHealth := gw.healthManager.CheckAll(ctx)

	// Determine HTTP status code based on health
	statusCode := http.StatusOK
	if health, ok := systemHealth.(map[string]interface{}); ok {
		if status, ok := health["status"].(string); ok {
			switch status {
			case "unhealthy":
				statusCode = http.StatusServiceUnavailable
			case "degraded":
				statusCode = http.StatusOK // Still return 200 for degraded
			}
		}
	}

	c.JSON(statusCode, systemHealth)
}

// handleMetrics handles Prometheus metrics requests
func (gw *apiGateway) handleMetrics(c *gin.Context) {
	// Prometheus metrics are automatically registered via promauto
	// This endpoint is handled by promhttp.Handler()
	promhttp.Handler().ServeHTTP(c.Writer, c.Request)
}

// handleGatewayInfo handles gateway info requests
func (gw *apiGateway) handleGatewayInfo(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"gateway": gin.H{
			"port":         gw.config.Port,
			"mcp_server":   gw.config.MCPServerURL,
			"scalping_api": gw.config.ScalpingAPIURL,
		},
		"routes": gin.H{
			"health":         "/health",
			"gateway_info":   "/gateway/info",
			"metrics":        "/metrics",
			"mcp_proxy":      "/mcp/*",
			"scalping_proxy": "/scalping/*",
			"trading_api":    "/api/v1/trading/*",
		},
		"endpoints": gin.H{
			"portfolio": "/api/v1/trading/portfolio/*",
			"scalping":  "/api/v1/trading/scalping/*",
			"orders":    "/api/v1/trading/orders/*",
			"market":    "/api/v1/trading/market/*",
		},
	})
}
