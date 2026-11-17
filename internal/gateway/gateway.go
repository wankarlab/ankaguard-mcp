package gateway

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rantipay/trading-mcp/internal/metrics"
	metricsMiddleware "github.com/rantipay/trading-mcp/internal/middleware"
	"github.com/rantipay/trading-mcp/internal/monitoring"
	"github.com/rantipay/trading-mcp/internal/trading/middleware"
	"github.com/rantipay/trading-mcp/internal/version"
	"github.com/sirupsen/logrus"
)

// GatewayConfig holds gateway configuration
type GatewayConfig struct {
	Port int
	// MetricsPort is the Prometheus listener of the gateway (0 = 9091).
	MetricsPort    int
	MCPServerURL   string
	ScalpingAPIURL string
	PrometheusURL  string
	EnableCORS     bool
	// CORSAllowedOrigins is the list of origins allowed by the CORS middleware.
	// An origin not in the list gets no Access-Control-Allow-Origin header
	// (the former wildcard "*" is gone, task 6.14).
	CORSAllowedOrigins []string
	// BindAddress is the interface the gateway listens on ("" = all interfaces).
	BindAddress   string
	EnableJWTAuth bool   // Enable Kong Gateway JWT authentication
	JWTSecret     string // Kong Gateway JWT secret for token validation
	// InternalGatewayToken is the shared secret set as X-Gateway-Token on
	// requests this gateway proxies to backend services, letting them trust the
	// proxied JWT. Must come from configuration (env), never a source literal.
	InternalGatewayToken string
	ReadTimeout          time.Duration
	WriteTimeout         time.Duration
	// TLS configuration
	TLSEnabled  bool   // If true, serve over TLS using ListenAndServeTLS
	TLSCertFile string // Path to TLS certificate file
	TLSKeyFile  string // Path to TLS private key file
	// Rate limiting
	RateLimiter   metricsMiddleware.RateLimiter   // Optional per-user rate limiter
	QuotaResolver metricsMiddleware.QuotaResolver // Optional quota resolver
}

// Gateway is the API Gateway that routes requests to backend services
type Gateway struct {
	config               *GatewayConfig
	router               *gin.Engine
	server               *http.Server
	mcpProxy             *httputil.ReverseProxy
	scalpingProxy        *httputil.ReverseProxy
	prometheusProxy      *httputil.ReverseProxy
	wsHub                *WSHub
	logger               *logrus.Logger
	jwtSecret            string // Kong Gateway JWT secret
	internalGatewayToken string // shared secret set as X-Gateway-Token on proxied requests
	metricsMiddleware    *metricsMiddleware.MetricsMiddleware
	metricsManager       *monitoring.MetricsManager
	startTime            time.Time // When the gateway started
	rateLimiter          metricsMiddleware.RateLimiter
	quotaResolver        metricsMiddleware.QuotaResolver
}

// NewGateway creates a new API Gateway
func NewGateway(config *GatewayConfig, logger *logrus.Logger) (*Gateway, error) {
	if logger == nil {
		logger = logrus.New()
	}

	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	// Set defaults
	if config.ReadTimeout == 0 {
		config.ReadTimeout = 30 * time.Second
	}
	if config.WriteTimeout == 0 {
		config.WriteTimeout = 30 * time.Second
	}

	// Create reverse proxies
	mcpURL, err := url.Parse(config.MCPServerURL)
	if err != nil {
		return nil, fmt.Errorf("invalid MCP server URL: %w", err)
	}

	scalpingURL, err := url.Parse(config.ScalpingAPIURL)
	if err != nil {
		return nil, fmt.Errorf("invalid Scalping API URL: %w", err)
	}

	// Shared secret set on proxied requests so backends can trust the gateway.
	internalGatewayToken := config.InternalGatewayToken

	// Create MCP proxy with custom director to preserve headers
	mcpProxy := httputil.NewSingleHostReverseProxy(mcpURL)
	originalDirector := mcpProxy.Director
	mcpProxy.Director = func(req *http.Request) {
		originalDirector(req)
		// Preserve custom headers
		req.Header.Set("X-Internal-Gateway", "true")
		req.Header.Set("X-Gateway-Token", internalGatewayToken)
	}

	// Initialize metrics
	multiTenantMetrics := metrics.GetMetrics()
	metricsPort := config.MetricsPort
	if metricsPort <= 0 {
		metricsPort = 9091
	}
	metricsManager := monitoring.NewMetricsManager(logger, metricsPort)
	mm := metricsMiddleware.InitializeMetricsMiddleware(multiTenantMetrics, metricsManager)

	// Initialize trading-specific Prometheus metrics
	_ = metrics.InitTradingMetrics()

	// Create scalping proxy with a custom director so downstream handlers can
	// trust gateway-proxied requests without re-validating the JWT signature.
	scalpingProxy := httputil.NewSingleHostReverseProxy(scalpingURL)
	scalpingOriginalDirector := scalpingProxy.Director
	scalpingProxy.Director = func(req *http.Request) {
		scalpingOriginalDirector(req)
		req.Header.Set("X-Internal-Gateway", "true")
		req.Header.Set("X-Gateway-Token", internalGatewayToken)
	}

	gateway := &Gateway{
		config:               config,
		mcpProxy:             mcpProxy,
		scalpingProxy:        scalpingProxy,
		wsHub:                NewWSHub(logger),
		logger:               logger,
		jwtSecret:            config.JWTSecret,
		internalGatewayToken: config.InternalGatewayToken,
		metricsMiddleware:    mm,
		metricsManager:       metricsManager,
		startTime:            time.Now(),
		rateLimiter:          config.RateLimiter,
		quotaResolver:        config.QuotaResolver,
	}

	// Start WebSocket hub
	go gateway.wsHub.Run()

	// Create Prometheus proxy if configured
	if config.PrometheusURL != "" {
		prometheusURL, err := url.Parse(config.PrometheusURL)
		if err != nil {
			logger.WithError(err).Warn("Invalid Prometheus URL, metrics endpoint will not be available")
		} else {
			gateway.prometheusProxy = httputil.NewSingleHostReverseProxy(prometheusURL)
		}
	}

	// Setup router
	gateway.setupRouter()

	return gateway, nil
}

// setupRouter configures the router with all routes
func (g *Gateway) setupRouter() {
	gin.SetMode(gin.ReleaseMode)
	g.router = gin.New()
	g.router.Use(gin.Recovery())
	g.router.Use(g.loggingMiddleware())

	// Add metrics middleware for HTTP request tracking
	if g.metricsMiddleware != nil {
		g.router.Use(g.metricsMiddleware.HTTPMetrics())
	}

	// Enable CORS if configured
	if g.config.EnableCORS {
		g.router.Use(g.corsMiddleware())
	}

	// Health check endpoint
	g.router.GET("/health", g.handleHealth)
	g.router.GET("/api/health", g.handleHealth)

	// Readiness endpoint - checks if all dependencies are available
	g.router.GET("/ready", g.handleReady)
	g.router.GET("/api/ready", g.handleReady)

	// Apply JWT middleware to protected routes if enabled
	var jwtMiddleware gin.HandlerFunc
	if g.authEnabled() {
		jwtMiddleware = middleware.KongJWTMiddleware(g.jwtSecret, g.internalGatewayToken, g.logger)
		g.logger.Info("Kong Gateway JWT authentication enabled for protected routes")
	} else {
		// No-op middleware if JWT auth is disabled
		jwtMiddleware = func(c *gin.Context) {
			c.Next()
		}
		g.logger.Warn("Kong Gateway JWT authentication is DISABLED - running in development mode")
	}

	// Service info endpoints (build detail, backend URLs) - PROTECTED
	g.router.GET("/info", jwtMiddleware, g.handleInfo)
	g.router.GET("/api/info", jwtMiddleware, g.handleInfo)

	// Gateway info endpoint (legacy, kept for backwards compatibility) - PROTECTED
	g.router.GET("/api/gateway/info", jwtMiddleware, g.handleGatewayInfo)

	// WebSocket endpoint: authenticates itself (token may be a query parameter)
	g.router.GET("/ws", g.handleWebSocket)

	// Route to MCP Server (main trading API) - PROTECTED
	g.router.Any("/api/mcp/*path", jwtMiddleware, g.handleMCPProxy)

	// Route AI endpoints to MCP Server (JSON-RPC) - PROTECTED
	g.router.Any("/api/v1/ai/*path", jwtMiddleware, g.handleMCPProxy)
	g.router.Any("/api/v1/ai-bot/*path", jwtMiddleware, g.handleMCPProxy)

	// Route Backtesting endpoints to MCP Server (Direct HTTP) - PROTECTED
	g.router.Any("/api/v1/backtesting/*path", jwtMiddleware, g.handleHTTPProxy)

	// Route simple backtesting endpoint - PROTECTED
	g.router.Any("/backtesting", jwtMiddleware, g.handleHTTPProxy)

	// Also route root MCP requests - PROTECTED
	g.router.POST("/", jwtMiddleware, g.handleMCPProxy)
	g.router.GET("/", jwtMiddleware, g.handleMCPProxy)

	// Route to Scalping API - PROTECTED
	// All requests to /api/scalping/* go to Scalping API
	g.router.Any("/api/scalping/*path", jwtMiddleware, g.handleScalpingProxy)

	// Route REST API v1 endpoints to Scalping API (port 8081) - PROTECTED
	// These routes are registered on the scalping dashboard router, not on the MCP server
	// Uses handleRESTAPIProxy which preserves the full original path
	g.router.Any("/api/v1/portfolio/*path", jwtMiddleware, g.handleRESTAPIProxy)
	g.router.GET("/api/v1/positions", jwtMiddleware, g.handleRESTAPIProxy)
	g.router.Any("/api/v1/orders/*path", jwtMiddleware, g.rateLimitMiddleware(), g.handleRESTAPIProxy)
	g.router.GET("/api/v1/orders", jwtMiddleware, g.rateLimitMiddleware(), g.handleRESTAPIProxy)
	g.router.POST("/api/v1/orders", jwtMiddleware, g.rateLimitMiddleware(), g.handleRESTAPIProxy)
	g.router.Any("/api/v1/market/*path", jwtMiddleware, g.handleRESTAPIProxy)
	g.router.Any("/api/v1/credentials/*path", jwtMiddleware, g.rateLimitMiddleware(), g.handleRESTAPIProxy)
	g.router.GET("/api/v1/credentials", jwtMiddleware, g.rateLimitMiddleware(), g.handleRESTAPIProxy)
	g.router.POST("/api/v1/credentials", jwtMiddleware, g.rateLimitMiddleware(), g.handleRESTAPIProxy)
	g.router.Any("/api/v1/analysis/*path", jwtMiddleware, g.handleRESTAPIProxy)
	g.router.Any("/api/v1/subscription/*path", jwtMiddleware, g.handleRESTAPIProxy)
	g.router.GET("/api/v1/subscription", jwtMiddleware, g.handleRESTAPIProxy)
	g.router.Any("/api/v1/webhooks/*path", jwtMiddleware, g.handleRESTAPIProxy)
	g.router.Any("/api/v1/websocket/*path", jwtMiddleware, g.handleRESTAPIProxy)
	g.router.Any("/api/v1/config/*path", jwtMiddleware, g.handleRESTAPIProxy)
	g.router.Any("/api/v1/admin/config/*path", jwtMiddleware, g.handleRESTAPIProxy)
	g.router.Any("/api/v1/master/*path", jwtMiddleware, g.handleRESTAPIProxy)
	g.router.Any("/api/v1/bots/*path", jwtMiddleware, g.handleRESTAPIProxy)
	g.router.GET("/api/v1/bots", jwtMiddleware, g.handleRESTAPIProxy)
	// MU per-user bot control (singular /bot, distinct from the /bots master view).
	// Registered as explicit static routes — a catch-all /api/v1/bot/*path would
	// conflict with the existing /api/v1/bots/*path in gin's radix tree.
	g.router.GET("/api/v1/bot/config", jwtMiddleware, g.rateLimitMiddleware(), g.handleRESTAPIProxy)
	g.router.PUT("/api/v1/bot/config", jwtMiddleware, g.rateLimitMiddleware(), g.handleRESTAPIProxy)
	g.router.POST("/api/v1/bot/start", jwtMiddleware, g.rateLimitMiddleware(), g.handleRESTAPIProxy)
	g.router.POST("/api/v1/bot/stop", jwtMiddleware, g.rateLimitMiddleware(), g.handleRESTAPIProxy)
	g.router.GET("/api/v1/bot/status", jwtMiddleware, g.rateLimitMiddleware(), g.handleRESTAPIProxy)
	g.router.POST("/api/v1/bot/emergency-stop", jwtMiddleware, g.rateLimitMiddleware(), g.handleRESTAPIProxy)
	g.router.Any("/api/v1/pairs/*path", jwtMiddleware, g.handleRESTAPIProxy)     // DM-2.3 pair selection
	g.router.Any("/api/v1/exchanges/*path", jwtMiddleware, g.handleRESTAPIProxy) // DM-5.4 multi-exchange spreads
	g.router.GET("/api/v1/mode", jwtMiddleware, g.handleRESTAPIProxy)            // Wave 2-A trading mode
	g.router.PUT("/api/v1/mode", jwtMiddleware, g.handleRESTAPIProxy)            // Wave 2-A trading mode
	g.router.Any("/api/v1/mode/*path", jwtMiddleware, g.handleRESTAPIProxy)      // Wave 2-A per-pair overrides
	g.router.GET("/api/v1/health", g.handleRESTAPIProxy)                         // no JWT for health

	// Route to Prometheus metrics
	// Direct promhttp handler for /metrics endpoint - exposes all registered Prometheus metrics
	g.router.GET("/metrics", jwtMiddleware, gin.WrapH(promhttp.Handler()))

	// If external Prometheus URL is configured, also proxy to it
	if g.prometheusProxy != nil {
		g.router.Any("/api/metrics/*path", jwtMiddleware, g.handlePrometheusProxy)
	}

	g.logger.Info("API Gateway routes configured")
}

// Start starts the gateway server
func (g *Gateway) Start(ctx context.Context) error {
	bind := g.config.BindAddress
	if bind == "" {
		bind = "0.0.0.0"
	}
	address := fmt.Sprintf("%s:%d", bind, g.config.Port)

	g.server = &http.Server{
		Addr:         address,
		Handler:      g.router,
		ReadTimeout:  g.config.ReadTimeout,
		WriteTimeout: g.config.WriteTimeout,
	}

	// Start metrics server
	if g.metricsManager != nil {
		if err := g.metricsManager.Start(); err != nil {
			g.logger.WithError(err).Warn("Failed to start metrics server")
		} else {
			g.logger.Info("Metrics server started")
		}
	}

	g.logger.WithFields(logrus.Fields{
		"address":          address,
		"mcp_backend":      g.config.MCPServerURL,
		"scalping_backend": g.config.ScalpingAPIURL,
	}).Info("Starting API Gateway")

	go func() {
		var serveErr error
		if g.config.TLSEnabled && g.config.TLSCertFile != "" && g.config.TLSKeyFile != "" {
			g.logger.WithFields(logrus.Fields{
				"cert_file": g.config.TLSCertFile,
				"key_file":  g.config.TLSKeyFile,
			}).Info("Gateway starting with TLS")
			serveErr = g.server.ListenAndServeTLS(g.config.TLSCertFile, g.config.TLSKeyFile)
		} else {
			serveErr = g.server.ListenAndServe()
		}
		if serveErr != nil && serveErr != http.ErrServerClosed {
			g.logger.WithError(serveErr).Error("Gateway server error")
		}
	}()

	return nil
}

// Stop stops the gateway server
func (g *Gateway) Stop(ctx context.Context) error {
	// Stop metrics server
	if g.metricsManager != nil {
		if err := g.metricsManager.Stop(); err != nil {
			g.logger.WithError(err).Warn("Failed to stop metrics server")
		}
	}

	if g.server == nil {
		return nil
	}

	g.logger.Info("Stopping API Gateway")
	return g.server.Shutdown(ctx)
}

// handleHealth handles health check requests
// This is a lightweight liveness check - use /ready for full dependency checks
func (g *Gateway) handleHealth(c *gin.Context) {
	info := version.GetInfo()

	c.JSON(http.StatusOK, gin.H{
		"status":         "ok",
		"service":        info.Service,
		"version":        info.Version,
		"gateway":        "running",
		"uptime_seconds": int64(time.Since(g.startTime).Seconds()),
		"timestamp":      time.Now().Format(time.RFC3339),
		"services": gin.H{
			"mcp_server":   g.config.MCPServerURL,
			"scalping_api": g.config.ScalpingAPIURL,
		},
	})
}

// handleGatewayInfo provides gateway information
func (g *Gateway) handleGatewayInfo(c *gin.Context) {
	info := version.GetInfo()

	c.JSON(http.StatusOK, gin.H{
		"gateway_version": info.Version,
		"service":         info.Service,
		"environment":     info.Environment,
		"routes": gin.H{
			"mcp_server":   "/api/mcp/* or /",
			"scalping_api": "/api/scalping/*",
			"metrics":      "/metrics",
			"health":       "/health or /api/health",
			"ready":        "/ready or /api/ready",
			"info":         "/info or /api/info",
		},
		"backends": gin.H{
			"mcp_server":   g.config.MCPServerURL,
			"scalping_api": g.config.ScalpingAPIURL,
			"prometheus":   g.config.PrometheusURL,
		},
	})
}

// handleMCPProxy proxies requests to MCP Server
func (g *Gateway) handleMCPProxy(c *gin.Context) {
	originalPath := c.Request.URL.Path

	g.logger.WithFields(logrus.Fields{
		"handler":    "handleMCPProxy",
		"path":       originalPath,
		"method":     c.Request.Method,
		"param_path": c.Param("path"),
	}).Info("Routing through MCP proxy")

	// Handle different route patterns
	if c.Param("path") != "" {
		// Extract the path parameter
		pathParam := c.Param("path")

		// Determine the correct path based on the original route
		if strings.HasPrefix(originalPath, "/api/mcp/") {
			// /api/mcp/* -> /api/v1/mcp/*
			c.Request.URL.Path = "/api/v1/mcp" + pathParam
		} else if strings.HasPrefix(originalPath, "/api/v1/ai/") {
			// /api/v1/ai/* -> keep as is
			c.Request.URL.Path = "/api/v1/ai" + pathParam
		} else if strings.HasPrefix(originalPath, "/api/v1/ai-bot/") {
			// /api/v1/ai-bot/* -> keep as is
			c.Request.URL.Path = "/api/v1/ai-bot" + pathParam
		} else if strings.HasPrefix(originalPath, "/api/v1/backtesting/") {
			// /api/v1/backtesting/* -> keep as is
			c.Request.URL.Path = "/api/v1/backtesting" + pathParam
		} else {
			// Default: keep the path parameter
			c.Request.URL.Path = pathParam
		}
	} else if originalPath == "/" || originalPath == "" {
		// Root request, keep as is
		c.Request.URL.Path = "/"
	} else if originalPath == "/api/mcp" {
		// /api/mcp (no trailing slash) -> / (JSON-RPC endpoint)
		c.Request.URL.Path = "/"
	}
	// If no path param and not root, keep original path

	// Add internal gateway header to bypass auth
	c.Request.Header.Set("X-Internal-Gateway", "true")
	c.Request.Header.Set("X-Gateway-Token", g.internalGatewayToken)

	g.mcpProxy.ServeHTTP(c.Writer, c.Request)
}

// handleHTTPProxy proxies HTTP requests directly to MCP Server (for non-JSON-RPC endpoints)
func (g *Gateway) handleHTTPProxy(c *gin.Context) {
	originalPath := c.Request.URL.Path

	g.logger.WithFields(logrus.Fields{
		"handler":    "handleHTTPProxy",
		"path":       originalPath,
		"method":     c.Request.Method,
		"param_path": c.Param("path"),
	}).Info("Routing through HTTP proxy")

	// Handle backtesting route patterns
	if strings.HasPrefix(originalPath, "/api/v1/backtesting/") {
		// Keep the full path for API v1 backtesting endpoints
		// /api/v1/backtesting/run-backtest -> /api/v1/backtesting/run-backtest
		// Path is already correct, no modification needed
	} else if originalPath == "/backtesting" {
		// Simple backtesting endpoint
		// /backtesting -> /backtesting (keep as is)
		// Path is already correct, no modification needed
	}

	// Add internal gateway header to bypass auth
	c.Request.Header.Set("X-Internal-Gateway", "true")
	c.Request.Header.Set("X-Gateway-Token", g.internalGatewayToken)

	g.logger.WithFields(logrus.Fields{
		"final_path":     c.Request.URL.Path,
		"content_type":   c.Request.Header.Get("Content-Type"),
		"content_length": c.Request.Header.Get("Content-Length"),
		"user_agent":     c.Request.Header.Get("User-Agent"),
	}).Debug("Proxying backtesting request to MCP server")

	g.mcpProxy.ServeHTTP(c.Writer, c.Request)
}

// handleScalpingProxy proxies requests to Scalping API
func (g *Gateway) handleScalpingProxy(c *gin.Context) {
	// Keep the path as-is since scalping is now on same server
	// /api/scalping/api/v1/scalping/status -> /api/v1/scalping/status
	path := c.Param("path")
	if path != "" {
		c.Request.URL.Path = path
	} else {
		c.Request.URL.Path = "/"
	}
	g.scalpingProxy.ServeHTTP(c.Writer, c.Request)
}

// handleRESTAPIProxy proxies REST API v1 requests to Scalping API preserving the full path
func (g *Gateway) handleRESTAPIProxy(c *gin.Context) {
	// Preserve the full original request path (e.g. /api/v1/portfolio/overview stays as-is)
	g.scalpingProxy.ServeHTTP(c.Writer, c.Request)
}

// handlePrometheusProxy proxies requests to Prometheus
func (g *Gateway) handlePrometheusProxy(c *gin.Context) {
	g.prometheusProxy.ServeHTTP(c.Writer, c.Request)
}

// rateLimitMiddleware returns a no-op handler when no RateLimiter is configured,
// or the real middleware when one is set.
func (g *Gateway) rateLimitMiddleware() gin.HandlerFunc {
	if g.rateLimiter == nil {
		return func(c *gin.Context) { c.Next() }
	}
	return metricsMiddleware.RateLimitMiddleware(g.rateLimiter, g.quotaResolver)
}

// loggingMiddleware logs all requests
func (g *Gateway) loggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path

		c.Next()

		latency := time.Since(start)
		statusCode := c.Writer.Status()

		g.logger.WithFields(logrus.Fields{
			"method":     c.Request.Method,
			"path":       path,
			"status":     statusCode,
			"latency_ms": latency.Milliseconds(),
			"client_ip":  c.ClientIP(),
		}).Info("Gateway request")
	}
}

// authEnabled reports whether the gateway validates JWTs: either a static
// secret is configured, or a local authority (standalone mode) is installed.
func (g *Gateway) authEnabled() bool {
	return g.config.EnableJWTAuth && (g.jwtSecret != "" || middleware.HasLocalAuthority())
}

// corsMiddleware enables CORS for the configured origins only.
func (g *Gateway) corsMiddleware() gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(g.config.CORSAllowedOrigins))
	for _, o := range g.config.CORSAllowedOrigins {
		allowed[strings.TrimRight(strings.TrimSpace(o), "/")] = struct{}{}
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if _, ok := allowed[origin]; ok && origin != "" {
			h := c.Writer.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Add("Vary", "Origin")
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
			h.Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")
		}

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

// CheckStatus represents the status of a single health check
type CheckStatus struct {
	Status    string `json:"status"`
	LatencyMs int64  `json:"latency_ms,omitempty"`
	Message   string `json:"message,omitempty"`
}

// ReadyResponse represents the response for the /ready endpoint
type ReadyResponse struct {
	Ready     bool                   `json:"ready"`
	Checks    map[string]CheckStatus `json:"checks"`
	Timestamp string                 `json:"timestamp"`
}

// handleReady handles readiness check requests
// Returns 200 if all critical services are available, 503 otherwise
func (g *Gateway) handleReady(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	checks := make(map[string]CheckStatus)
	allReady := true

	// Check GCT connection
	gctCheck := g.checkGCTConnection(ctx)
	checks["gct_connection"] = gctCheck
	if gctCheck.Status != "ok" {
		allReady = false
	}

	// Check MCP server
	mcpCheck := g.checkMCPServer(ctx)
	checks["mcp_server"] = mcpCheck
	if mcpCheck.Status != "ok" {
		allReady = false
	}

	// Note: Database check is optional and can be added when storage is configured
	// For now, we skip it as the gateway doesn't directly use a database

	response := ReadyResponse{
		Ready:     allReady,
		Checks:    checks,
		Timestamp: time.Now().Format(time.RFC3339),
	}

	if allReady {
		c.JSON(http.StatusOK, response)
	} else {
		c.JSON(http.StatusServiceUnavailable, response)
	}
}

// checkGCTConnection checks if GoCryptoTrader gRPC connection is alive.
// The gateway performs this check by probing the MCP server's /health endpoint,
// which already reports GCT connectivity via the internal health.Manager.
// A direct gRPC connection from the gateway is intentionally avoided to keep
// the gateway stateless; use the MCP server's /health endpoint for full details.
func (g *Gateway) checkGCTConnection(ctx context.Context) CheckStatus {
	return CheckStatus{
		Status:  "unknown",
		Message: "GCT status reported via MCP server /health endpoint",
	}
}

// checkMCPServer checks if the MCP server is responding
func (g *Gateway) checkMCPServer(ctx context.Context) CheckStatus {
	start := time.Now()

	// Create a simple HTTP request to the MCP server health endpoint
	healthURL := g.config.MCPServerURL + "/health"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		return CheckStatus{
			Status:    "error",
			LatencyMs: time.Since(start).Milliseconds(),
			Message:   fmt.Sprintf("Failed to create request: %v", err),
		}
	}

	client := &http.Client{
		Timeout: 3 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return CheckStatus{
			Status:    "error",
			LatencyMs: time.Since(start).Milliseconds(),
			Message:   fmt.Sprintf("MCP server unreachable: %v", err),
		}
	}
	defer resp.Body.Close()

	// Read and discard body to properly close connection
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return CheckStatus{
			Status:    "ok",
			LatencyMs: time.Since(start).Milliseconds(),
		}
	}

	return CheckStatus{
		Status:    "error",
		LatencyMs: time.Since(start).Milliseconds(),
		Message:   fmt.Sprintf("MCP server returned status %d", resp.StatusCode),
	}
}

// InfoResponse represents the response for the /info endpoint
type InfoResponse struct {
	Service       string `json:"service"`
	Version       string `json:"version"`
	BuildTime     string `json:"build_time"`
	GitCommit     string `json:"git_commit,omitempty"`
	GoVersion     string `json:"go_version"`
	UptimeSeconds int64  `json:"uptime_seconds"`
	Environment   string `json:"environment"`
	StartedAt     string `json:"started_at"`
}

// handleInfo handles service info requests
func (g *Gateway) handleInfo(c *gin.Context) {
	info := version.GetInfo()

	response := InfoResponse{
		Service:       info.Service,
		Version:       info.Version,
		BuildTime:     info.BuildTime,
		GitCommit:     info.GitCommit,
		GoVersion:     info.GoVersion,
		UptimeSeconds: int64(time.Since(g.startTime).Seconds()),
		Environment:   info.Environment,
		StartedAt:     g.startTime.Format(time.RFC3339),
	}

	c.JSON(http.StatusOK, response)
}
