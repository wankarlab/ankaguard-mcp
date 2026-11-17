package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rantipay/trading-mcp/internal/killswitch"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/config"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/toolpolicy"
	"github.com/rantipay/trading-mcp/internal/metrics"
	"github.com/rantipay/trading-mcp/internal/paper"
	"github.com/rantipay/trading-mcp/internal/risk"
	"github.com/sirupsen/logrus"
)

// MCPServer es el servidor MCP principal
type MCPServer struct {
	config         *config.Config
	log            *logrus.Logger
	gctClient      *gctclient.Client
	registry       *ToolRegistry
	httpServer     *http.Server
	auditLogger    *AuditLogger
	sandboxMode    *SandboxMode
	running        bool
	mu             sync.RWMutex
	requestCount   int64
	errorCount     int64
	aiBotHandler   any                // Handler for AI bot endpoints
	tuningHandler  any                // Handler for bot tuning parameter endpoints
	testMockClient GCTClientInterface // Mock client for testing (nil in production)
	wsHub          any                // WebSocket Hub (optional, nil if not initialized)
	rankProvider   RankedPairProvider // wired via SetRankedPairProvider (optional)
	tradeRepo      TradeHistoryReader // wired via SetTradeRepo (optional)
	xsecDB         *sql.DB            // wired via SetXSecDB (optional)
	startTime      time.Time          // process start, for health uptime

	// JWT auth for this server's own raw HTTP mux (port 10600). Wired via
	// SetJWTAuth from main.go using the same KONG_JWT_SECRET/
	// INTERNAL_GATEWAY_TOKEN/ENABLE_JWT_AUTH the REST v1 router and API
	// Gateway already use (CORE-01: this mux previously trusted the API
	// Gateway to be the only reachable entrypoint and enforced nothing
	// itself).
	jwtSecret       string
	jwtGatewayToken string
	jwtAuthEnabled  bool

	// publicHealth feeds the public GET /health (SetPublicHealth).
	publicHealth func() (version, commit, edition string)

	// paperBroker serves the order and account calls of paper-profile tokens
	// (SetPaperBroker). nil = PAPER_UNAVAILABLE for paper trade calls.
	paperMu     sync.RWMutex
	paperBroker PaperBroker

	// liveEnabled is trading.live_enabled (SetLiveEnabledFunc); nil = open.
	liveMu      sync.RWMutex
	liveEnabled func() bool
	// licenseAllowsLive is the product license gate (SetLicenseGate); nil = open.
	licenseAllowsLive func() bool
	// omitDeviceRoutes leaves /api/v1/user/fcm-token unmounted (product).
	omitDeviceRoutes bool
}

// NewMCPServer crea un nuevo servidor MCP
func NewMCPServer(cfg *config.Config, log *logrus.Logger, gctClient *gctclient.Client) *MCPServer {
	// Inicializar audit logger
	auditConfig := AuditConfig{
		Enabled:        cfg.Audit.Enabled,
		LogFile:        cfg.Audit.LogFile,
		LogAllRequests: cfg.Audit.LogAllRequests,
		LogAllOrders:   cfg.Audit.LogAllOrders,
	}

	auditLogger, err := NewAuditLogger(auditConfig, log)
	if err != nil {
		log.WithError(err).Warn("Failed to initialize audit logger")
	}

	// Inicializar sandbox mode reporter — CORE-02: driven by the REAL
	// exchanges[].sandbox config, not the cosmetic server.mode flag (which
	// never controlled where orders actually route).
	sandboxMode := NewSandboxMode(cfg, log)

	if sandboxMode.IsEnabled() {
		log.Warn("⚠️  SANDBOX MODE — all enabled exchanges are testnet (exchanges[].sandbox=true)")
	} else {
		log.Warn("🔴 PRODUCTION MODE - Orders will be executed with REAL MONEY (at least one enabled exchange has sandbox=false)")
	}

	return &MCPServer{
		config:      cfg,
		log:         log,
		gctClient:   gctClient,
		registry:    NewToolRegistry(),
		auditLogger: auditLogger,
		sandboxMode: sandboxMode,
		startTime:   time.Now(),
	}
}

// RegisterTool registra un nuevo tool
func (s *MCPServer) RegisterTool(tool Tool) error {
	err := s.registry.Register(tool)
	if err == nil {
		updateToolsCount(s.registry.Count())
	}
	return err
}

// RegisterAIBotHandler registra el handler del AI bot
func (s *MCPServer) RegisterAIBotHandler(handler any) {
	s.aiBotHandler = handler
	s.log.Info("AI Bot handler registered")
}

// RegisterTuningHandler registers the bot tuning parameter handler.
func (s *MCPServer) RegisterTuningHandler(handler any) {
	s.tuningHandler = handler
	s.log.Info("Tuning handler registered")
}

// SetWebSocketHub sets the WebSocket Hub for real-time communication
func (s *MCPServer) SetWebSocketHub(hub any) {
	s.wsHub = hub
	s.log.Info("WebSocket Hub registered with MCP Server")
}

// Start inicia el servidor MCP
func (s *MCPServer) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("server already running")
	}
	s.running = true
	s.mu.Unlock()

	s.log.WithFields(logrus.Fields{
		"protocol": s.config.Server.Protocol,
		"port":     s.config.Server.Port,
		"mode":     s.config.Server.Mode,
		"tools":    s.registry.Count(),
	}).Info("Starting MCP Server")

	switch s.config.Server.Protocol {
	case "stdio":
		return s.startStdio(ctx)
	case "http":
		return s.startHTTP(ctx)
	default:
		return fmt.Errorf("unsupported protocol: %s", s.config.Server.Protocol)
	}
}

// Stop detiene el servidor
func (s *MCPServer) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}

	s.log.Info("Stopping MCP Server")

	if s.httpServer != nil {
		if err := s.httpServer.Shutdown(ctx); err != nil {
			return fmt.Errorf("failed to shutdown HTTP server: %w", err)
		}
	}

	// Cerrar audit logger
	if s.auditLogger != nil {
		if err := s.auditLogger.Close(); err != nil {
			s.log.WithError(err).Error("Failed to close audit logger")
		}
	}

	s.running = false
	return nil
}

// startStdio inicia el servidor en modo stdio
func (s *MCPServer) startStdio(ctx context.Context) error {
	s.log.Info("MCP Server running in stdio mode")
	s.log.Info("Reading requests from stdin, writing responses to stdout")

	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			var req models.MCPRequest
			if err := decoder.Decode(&req); err != nil {
				if err == io.EOF {
					s.log.Info("stdin closed, shutting down")
					return nil
				}
				s.log.WithError(err).Error("Failed to decode request")
				continue
			}

			resp := s.HandleRequest(withStdioIdentity(ctx), &req)
			if err := encoder.Encode(resp); err != nil {
				s.log.WithError(err).Error("Failed to encode response")
			}
		}
	}
}

// recordingMux is an http.ServeMux that remembers the patterns mounted on it,
// so the route table (docs/product/routes.md) can be checked by tests.
type recordingMux struct {
	*http.ServeMux
	patterns []string
}

func (m *recordingMux) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	m.patterns = append(m.patterns, pattern)
	m.ServeMux.HandleFunc(pattern, h)
}

func (m *recordingMux) Handle(pattern string, h http.Handler) {
	m.patterns = append(m.patterns, pattern)
	m.ServeMux.Handle(pattern, h)
}

// buildMux assembles every route of the MCP listener (:10600).
func (s *MCPServer) buildMux() *recordingMux {
	mux := &recordingMux{ServeMux: http.NewServeMux()}

	// PHASE 1: Register specific backtesting routes FIRST (highest priority)
	s.log.Info("🔧 Registering backtesting endpoints with highest priority")
	mux.HandleFunc("/api/v1/backtesting/run-backtest", s.requireJWTAuth(s.handleRunBacktest))
	mux.HandleFunc("/api/v1/backtesting/test", s.requireJWTAuth(func(w http.ResponseWriter, r *http.Request) {
		s.log.Info("🧪 BACKTESTING TEST ENDPOINT CALLED - METHOD: " + r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"backtesting test endpoint working","method":"` + r.Method + `","path":"` + r.URL.Path + `"}`))
	}))
	mux.HandleFunc("/backtesting", s.requireJWTAuth(s.handleBacktestingSimple))
	s.log.Info("✅ Backtesting endpoints registered successfully")

	// PHASE 2: Register system endpoints
	s.log.Info("🔧 Registering system endpoints")
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/health/detail", s.requireJWTAuth(s.handleHealthDetail))
	mux.HandleFunc("/tools", s.requireJWTAuth(s.handleToolsList))
	mux.Handle("/metrics", s.requireJWTHandler(promhttp.Handler()))

	// PHASE 2.5: Register WebSocket endpoint (if Hub is available)
	if s.wsHub != nil {
		s.log.Info("🔧 Registering WebSocket endpoint")
		// /ws authenticates itself (the token may come in the "token" query
		// parameter because browsers cannot set headers on a WebSocket).
		mux.HandleFunc("/ws", s.handleWebSocket)
		mux.HandleFunc("/api/v1/ws/stats", s.requireJWTAuth(s.handleWebSocketStats))
		mux.HandleFunc("/api/v1/ws/channels", s.requireJWTAuth(s.handleWebSocketChannels))
		mux.HandleFunc("/api/v1/ws/rooms", s.requireJWTAuth(s.handleWebSocketRooms))
		s.log.Info("✅ WebSocket endpoints registered successfully (/ws, /api/v1/ws/*)")
	} else {
		s.log.Warn("⚠️  WebSocket Hub not initialized, /ws endpoint not available")
	}

	// PHASE 3: the former /test, /debug and /simple-test endpoints echoed the
	// request headers (including Authorization) and were removed (task 6.8).

	// PHASE 4: Register AI Bot endpoints (if handler is registered)
	if s.aiBotHandler != nil {
		s.log.Info("🔧 Registering AI Bot endpoints")
		mux.HandleFunc("/api/v1/ai-bot/analyze", s.requireJWTAuth(s.handleAIAnalyze))
		mux.HandleFunc("/api/v1/ai-bot/positions", s.requireJWTAuth(s.handleAIPositions))
		mux.HandleFunc("/api/v1/ai-bot/status", s.requireJWTAuth(s.handleAIStatus))
		mux.HandleFunc("/api/v1/ai-bot/start", s.requireJWTAuth(s.handleAIStart))
		mux.HandleFunc("/api/v1/ai-bot/stop", s.requireJWTAuth(s.handleAIStop))
		mux.HandleFunc("/api/v1/ai-bot/pause", s.requireJWTAuth(s.handleAIPause))
		mux.HandleFunc("/api/v1/ai-bot/resume", s.requireJWTAuth(s.handleAIResume))
		mux.HandleFunc("/api/v1/ai-bot/emergency-stop", s.requireJWTAuth(s.handleAIEmergencyStop))
		mux.HandleFunc("/api/v1/ai-bot/reset-state", s.requireJWTAuth(s.handleAIResetState))
		mux.HandleFunc("/api/v1/ai-bot/close-all-positions", s.requireJWTAuth(s.handleAICloseAllPositions))
		mux.HandleFunc("/api/v1/ai-bot/diagnostics", s.requireJWTAuth(s.handleAIDiagnostics))
		mux.HandleFunc("/api/v1/ai-bot/config", s.requireJWTAuth(s.handleAIConfig))
		mux.HandleFunc("/api/v1/ai-bot/performance", s.requireJWTAuth(s.handleAIPerformance))
		mux.HandleFunc("/api/v1/ai-bot/comprehensive-analysis", s.requireJWTAuth(s.handleComprehensiveAnalysis))

		// Autonomous mode endpoints (NEW)
		mux.HandleFunc("/api/v1/ai-bot/autonomous/enable", s.requireJWTAuth(s.handleAutonomousEnable))
		mux.HandleFunc("/api/v1/ai-bot/autonomous/status", s.requireJWTAuth(s.handleAutonomousStatus))

		// Operator manual entry/close (level 4): end-to-end verification of the
		// real execution path, see services/ai_trading_bot_manual.go.
		mux.HandleFunc("/api/v1/ai-bot/manual-entry", s.requireJWTAuth(s.requireMinLevel(4, s.handleAIManualEntry)))
		mux.HandleFunc("/api/v1/ai-bot/manual-close", s.requireJWTAuth(s.requireMinLevel(4, s.handleAIManualClose)))

		// Order-flow and feedback endpoints
		mux.HandleFunc("/api/v1/ai-bot/orderflow", s.requireJWTAuth(s.handleAIOrderFlow))
		mux.HandleFunc("/api/v1/ai-bot/feedback", s.requireJWTAuth(s.handleAIFeedback))

		// AI Analysis endpoints (NEW - Task 9.1, 9.2, 9.3, 9.4, 9.5)
		mux.HandleFunc("/api/v1/ai/llm-analysis", s.requireJWTAuth(s.handleLLMAnalysis))
		mux.HandleFunc("/api/v1/ai/sentiment-analysis", s.requireJWTAuth(s.handleSentimentAnalysis))
		mux.HandleFunc("/api/v1/ai/strategy-performance", s.requireJWTAuth(s.handleStrategyPerformance))
		mux.HandleFunc("/api/v1/ai/costs", s.requireJWTAuth(s.handleCostTracking))
		mux.HandleFunc("/api/v1/ai/config", s.requireJWTAuth(s.handleAIConfiguration))

		// Complete Analysis endpoint (NEW - Unified analysis endpoint)
		mux.HandleFunc("/api/v1/ai/complete-analysis", s.requireJWTAuth(s.handleCompleteAnalysis))
		s.log.Info("✅ AI Bot endpoints registered successfully")
	}

	// Bot tuning parameter endpoints
	if s.tuningHandler != nil {
		s.log.Info("Registering bot tuning endpoints")
		mux.HandleFunc("/api/v1/bot/tuning", s.requireJWTAuth(s.handleBotTuning))
		mux.HandleFunc("/api/v1/bot/tuning/", s.requireJWTAuth(s.handleBotTuningOne))
		s.log.Info("Bot tuning endpoints registered successfully")
	}

	// User device management (push notifications through RantiPay services;
	// not mounted when the product edition omits them, see OmitDeviceRoutes).
	if !s.omitDeviceRoutes {
		mux.HandleFunc("/api/v1/user/fcm-token", s.requireJWTAuth(s.handleRegisterFCMToken))
	}

	// PHASE 5: Register JSON-RPC endpoints (LAST - lowest priority)
	s.log.Info("🔧 Registering JSON-RPC endpoints")
	mux.HandleFunc("/mcp", s.requireJWTAuth(s.handleHTTPRequest))
	mux.HandleFunc("/jsonrpc", s.requireJWTAuth(s.handleHTTPRequest))

	// Register root handler for JSON-RPC with path validation
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.log.WithFields(logrus.Fields{
			"method": r.Method,
			"path":   r.URL.Path,
		}).Info("🔍 Root handler called - checking path")

		// Only handle exact root path for JSON-RPC, reject sub-paths
		if r.URL.Path == "/" {
			s.log.Info("✅ Root path - routing to JSON-RPC handler")
			s.requireJWTAuth(s.handleHTTPRequest)(w, r)
		} else {
			s.log.WithField("path", r.URL.Path).Warn("❌ Sub-path rejected by root handler")
			http.NotFound(w, r)
		}
	})
	s.log.Info("✅ JSON-RPC endpoints registered successfully")

	// Log final route registration summary
	s.log.WithFields(logrus.Fields{
		"backtesting_routes": []string{"/api/v1/backtesting/run-backtest", "/backtesting", "/api/v1/backtesting/test"},
		"jsonrpc_routes":     []string{"/", "/mcp", "/jsonrpc"},
		"system_routes":      []string{"/health", "/tools", "/metrics"},
	}).Info("🎯 All HTTP routes registered - backtesting has highest priority")

	return mux
}

// HTTPHandler returns the handler of the MCP listener and the list of the
// patterns mounted on it. It is what startHTTP serves; tests use it to audit
// the authentication of every route.
func (s *MCPServer) HTTPHandler() (http.Handler, []string) {
	m := s.buildMux()
	return m, m.patterns
}

// startHTTP inicia el servidor en modo HTTP
func (s *MCPServer) startHTTP(ctx context.Context) error {
	listenHost := s.config.ListenHost()
	addr := fmt.Sprintf("%s:%d", listenHost, s.config.Server.Port)
	if !config.IsLocalBind(listenHost) {
		s.log.WithFields(logrus.Fields{"port": s.config.Server.Port, "bind": listenHost}).
			Warn("MCP/REST port is exposed beyond this machine (bind address is not loopback); set server.bind_address: 127.0.0.1 to restrict it")
	}

	mux := s.buildMux()

	// Get timeout values from config with defaults
	readTimeout := 30
	if s.config.Server.ReadTimeout > 0 {
		readTimeout = s.config.Server.ReadTimeout
	}
	writeTimeout := 120
	if s.config.Server.WriteTimeout > 0 {
		writeTimeout = s.config.Server.WriteTimeout
	}
	idleTimeout := 120
	if s.config.Server.IdleTimeout > 0 {
		idleTimeout = s.config.Server.IdleTimeout
	}
	maxHeaderBytes := 1 << 20 // 1 MB default
	if s.config.Server.MaxHeaderBytes > 0 {
		maxHeaderBytes = s.config.Server.MaxHeaderBytes
	}

	s.httpServer = &http.Server{
		Addr:           addr,
		Handler:        mux,
		ReadTimeout:    time.Duration(readTimeout) * time.Second,
		WriteTimeout:   time.Duration(writeTimeout) * time.Second,
		IdleTimeout:    time.Duration(idleTimeout) * time.Second,
		MaxHeaderBytes: maxHeaderBytes,
	}

	s.log.WithFields(logrus.Fields{
		"address":          addr,
		"read_timeout":     readTimeout,
		"write_timeout":    writeTimeout,
		"idle_timeout":     idleTimeout,
		"max_header_bytes": maxHeaderBytes,
	}).Info("MCP Server HTTP configuration")

	s.log.WithField("address", addr).Info("MCP Server listening on HTTP")

	errChan := make(chan error, 1)
	go func() {
		var serveErr error
		tlsCfg := s.config.Server.TLS
		if tlsCfg.Enabled && tlsCfg.CertFile != "" && tlsCfg.KeyFile != "" {
			s.log.WithFields(logrus.Fields{
				"cert_file": tlsCfg.CertFile,
				"key_file":  tlsCfg.KeyFile,
			}).Info("MCP Server starting with TLS")
			serveErr = s.httpServer.ListenAndServeTLS(tlsCfg.CertFile, tlsCfg.KeyFile)
		} else {
			serveErr = s.httpServer.ListenAndServe()
		}
		if serveErr != nil && serveErr != http.ErrServerClosed {
			errChan <- serveErr
		}
	}()

	select {
	case err := <-errChan:
		return err
	case <-ctx.Done():
		return s.Stop(context.Background())
	}
}

// handleHTTPRequest maneja requests HTTP
func (s *MCPServer) handleHTTPRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req models.MCPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.WithError(err).Error("Failed to decode HTTP request")
		resp := &models.MCPResponse{
			JSONRPC: "2.0",
			Error: &models.MCPError{
				Code:    -32700,
				Message: "Parse error",
				Data:    err.Error(),
			},
			ID: nil,
		}
		s.writeJSONResponse(w, resp)
		return
	}

	resp := s.HandleRequest(r.Context(), &req)
	s.writeJSONResponse(w, resp)
}

// handleHealth maneja el health check endpoint
func (s *MCPServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	// Public: only what is needed to know the process is up and which build it is.
	version, commit, edition := "dev", "unknown", "community"
	if fn := s.publicHealth; fn != nil {
		version, commit, edition = fn()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"version": version,
		"commit":  commit,
		"edition": edition,
	})
}

// handleHealthDetail serves the detailed health payload that /health used to
// expose publicly. It requires authentication.
func (s *MCPServer) handleHealthDetail(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.HealthDetail())
}

// tradingModeInfo reports the ACTUAL money-at-risk status of the bot. There are two
// independent execution paths and they can disagree: the MCP order tools honour
// Server.Mode (the sandbox gate, driven by MCP_SERVER_MODE / config server.mode),
// while the autonomous trading engine executes DIRECTLY against the exchange per
// exchanges[].sandbox. So a "sandbox" MCP mode does NOT mean the engine is safe —
// the previous health report read only the MCP gate and could claim "no real money
// at risk" while the engine traded live. This reports the engine reality first.
func (s *MCPServer) tradingModeInfo() map[string]interface{} {
	engineRealMoney := false
	for _, ex := range s.config.GetEnabledExchanges() {
		if !ex.Sandbox {
			engineRealMoney = true
			break
		}
	}
	msg := "SANDBOX: all enabled exchanges are testnet. No real money at risk."
	if engineRealMoney {
		msg = "PRODUCTION: the trading engine executes with REAL MONEY. Use with caution."
	}
	return map[string]interface{}{
		// Legacy fields kept for consumers: true ONLY when no real money is at risk.
		"enabled":      !engineRealMoney,
		"testnet_only": !engineRealMoney,
		// Explicit, unambiguous fields.
		"engine_real_money":       engineRealMoney,
		"mcp_order_tools_testnet": s.sandboxMode.IsEnabled(),
		"server_mode":             s.config.Server.Mode,
		"message":                 msg,
	}
}

// handleWebSocket handles WebSocket upgrade requests
func (s *MCPServer) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Type assert wsHub to the WebSocket Hub interface
	// We use interface{} to avoid circular dependencies
	type WebSocketHub interface {
		HandleWebSocketHTTP(w http.ResponseWriter, r *http.Request)
	}

	if hub, ok := s.wsHub.(WebSocketHub); ok {
		hub.HandleWebSocketHTTP(w, r)
	} else {
		s.log.Error("WebSocket Hub does not implement required interface")
		http.Error(w, "WebSocket not available", http.StatusServiceUnavailable)
	}
}

// handleWebSocketStats handles WebSocket statistics requests
func (s *MCPServer) handleWebSocketStats(w http.ResponseWriter, r *http.Request) {
	type WebSocketStatsHandler interface {
		HandleStatsHTTP(w http.ResponseWriter, r *http.Request)
	}

	if handler, ok := s.wsHub.(WebSocketStatsHandler); ok {
		handler.HandleStatsHTTP(w, r)
	} else {
		http.Error(w, "WebSocket stats not available", http.StatusServiceUnavailable)
	}
}

// handleWebSocketChannels handles WebSocket channels list requests
func (s *MCPServer) handleWebSocketChannels(w http.ResponseWriter, r *http.Request) {
	type WebSocketChannelsHandler interface {
		HandleChannelsHTTP(w http.ResponseWriter, r *http.Request)
	}

	if handler, ok := s.wsHub.(WebSocketChannelsHandler); ok {
		handler.HandleChannelsHTTP(w, r)
	} else {
		http.Error(w, "WebSocket channels not available", http.StatusServiceUnavailable)
	}
}

// handleWebSocketRooms handles WebSocket rooms list requests
func (s *MCPServer) handleWebSocketRooms(w http.ResponseWriter, r *http.Request) {
	type WebSocketRoomsHandler interface {
		HandleRoomsHTTP(w http.ResponseWriter, r *http.Request)
	}

	if handler, ok := s.wsHub.(WebSocketRoomsHandler); ok {
		handler.HandleRoomsHTTP(w, r)
	} else {
		http.Error(w, "WebSocket rooms not available", http.StatusServiceUnavailable)
	}
}

// handleToolsList maneja el endpoint de lista de tools
func (s *MCPServer) handleToolsList(w http.ResponseWriter, r *http.Request) {
	// Unclassified tools are never listed. This endpoint has no token, so it
	// is not filtered by profile (its authentication is a separate change).
	all := s.registry.List()
	tools := make([]models.MCPToolInfo, 0, len(all))
	for _, info := range all {
		if _, ok := toolpolicy.ClassOf(info.Name); ok {
			tools = append(tools, info)
		}
	}
	response := models.MCPToolsListResponse{
		Tools: tools,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// writeJSONResponse escribe una respuesta JSON
func (s *MCPServer) writeJSONResponse(w http.ResponseWriter, resp *models.MCPResponse) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		s.log.WithError(err).Error("Failed to encode response")
	}
}

// HandleRequest maneja un request MCP
func (s *MCPServer) HandleRequest(ctx context.Context, req *models.MCPRequest) *models.MCPResponse {
	startTime := time.Now()

	s.mu.Lock()
	s.requestCount++
	s.mu.Unlock()

	// Log request
	s.log.WithFields(logrus.Fields{
		"method": req.Method,
		"id":     req.ID,
	}).Debug("Handling MCP request")

	// Validar request
	if err := s.validateRequest(req); err != nil {
		s.mu.Lock()
		s.errorCount++
		s.mu.Unlock()
		return &models.MCPResponse{
			JSONRPC: "2.0",
			Error:   err,
			ID:      req.ID,
		}
	}

	// Enrutar según método
	var resp *models.MCPResponse
	switch req.Method {
	case "tools/list":
		resp = s.handleToolsList_MCP(ctx, req)
	case "tools/call":
		resp = s.handleToolCall(ctx, req)
	default:
		s.mu.Lock()
		s.errorCount++
		s.mu.Unlock()
		resp = &models.MCPResponse{
			JSONRPC: "2.0",
			Error: &models.MCPError{
				Code:    -32601,
				Message: "Method not found",
				Data:    req.Method,
			},
			ID: req.ID,
		}
	}

	// Audit log y métricas
	duration := time.Since(startTime)
	var err error
	status := "success"
	if resp.Error != nil {
		err = resp.Error
		status = "error"
	}

	// Registrar métricas
	recordRequest(req.Method, status, duration.Seconds())
	// Use test mock client if available, otherwise use real gctClient
	if s.testMockClient != nil {
		updateGCTConnection(s.testMockClient.IsConnected())
	} else if s.gctClient != nil {
		updateGCTConnection(s.gctClient.IsConnected())
	}

	if s.auditLogger != nil {
		s.auditLogger.LogRequest(req, resp.Result, err, duration)
	}

	return resp
}

// validateRequest valida un request MCP
func (s *MCPServer) validateRequest(req *models.MCPRequest) *models.MCPError {
	if req.JSONRPC != "2.0" {
		return &models.MCPError{
			Code:    -32600,
			Message: "Invalid Request",
			Data:    "jsonrpc must be '2.0'",
		}
	}

	if req.Method == "" {
		return &models.MCPError{
			Code:    -32600,
			Message: "Invalid Request",
			Data:    "method is required",
		}
	}

	return nil
}

// handleToolsList_MCP maneja el método tools/list
func (s *MCPServer) handleToolsList_MCP(ctx context.Context, req *models.MCPRequest) *models.MCPResponse {
	// Only tools the caller's profile can execute are listed; unclassified
	// tools are never listed.
	profile, _ := s.callerFromContext(ctx)
	all := s.registry.List()
	tools := make([]models.MCPToolInfo, 0, len(all))
	for _, info := range all {
		if toolListedFor(info.Name, profile) {
			tools = append(tools, info)
		}
	}

	return &models.MCPResponse{
		JSONRPC: "2.0",
		Result: models.MCPToolsListResponse{
			Tools: tools,
		},
		ID: req.ID,
	}
}

// handleToolCall maneja el método tools/call
func (s *MCPServer) handleToolCall(ctx context.Context, req *models.MCPRequest) *models.MCPResponse {
	// Incrementar contador de requests en vuelo
	metrics.GetRecorder().IncrementHTTPInFlight()
	defer metrics.GetRecorder().DecrementHTTPInFlight()

	if req.Params == nil {
		s.mu.Lock()
		s.errorCount++
		s.mu.Unlock()
		return &models.MCPResponse{
			JSONRPC: "2.0",
			Error: &models.MCPError{
				Code:    -32602,
				Message: "Invalid params",
				Data:    "params is required for tools/call",
			},
			ID: req.ID,
		}
	}

	toolName := req.Params.Name
	if toolName == "" {
		s.mu.Lock()
		s.errorCount++
		s.mu.Unlock()
		metrics.GetRecorder().RecordMCPToolError("unknown", "invalid_params")
		return &models.MCPResponse{
			JSONRPC: "2.0",
			Error: &models.MCPError{
				Code:    -32602,
				Message: "Invalid params",
				Data:    "tool name is required",
			},
			ID: req.ID,
		}
	}

	// Buscar tool
	tool, exists := s.registry.Get(toolName)
	if !exists {
		s.mu.Lock()
		s.errorCount++
		s.mu.Unlock()
		metrics.GetRecorder().RecordMCPToolError(toolName, "tool_not_found")
		return &models.MCPResponse{
			JSONRPC: "2.0",
			Error: &models.MCPError{
				Code:    -32602,
				Message: "Tool not found",
				Data:    toolName,
			},
			ID: req.ID,
		}
	}

	// Dispatch order: auth (middleware) -> class -> permission -> kill switch
	// -> execution. Class and permission are resolved from the tool
	// classification and the token profile.
	class, permErr := s.checkToolPermission(ctx, toolName, req.Params.Arguments)
	if permErr != nil {
		s.mu.Lock()
		s.errorCount++
		s.mu.Unlock()
		reason := "permission_denied"
		if permErr.Code == ErrCodePaperUnavailable {
			reason = "paper_unavailable"
		}
		metrics.GetRecorder().RecordMCPToolError(toolName, reason)
		return &models.MCPResponse{
			JSONRPC: "2.0",
			Error:   permErr,
			ID:      req.ID,
		}
	}

	// Paper profile: reject tools with no paper equivalent and bind the call to
	// the paper account of the token, so the order and account methods are
	// served by the broker and never by the exchange.
	ctx, paperErr := s.paperEnvelope(ctx, toolName, class)
	if paperErr != nil {
		s.mu.Lock()
		s.errorCount++
		s.mu.Unlock()
		metrics.GetRecorder().RecordMCPToolError(toolName, "paper_rejected")
		return &models.MCPResponse{JSONRPC: "2.0", Error: paperErr, ID: req.ID}
	}

	// Ring 0 (global kill switch): refuse exactly the calls classified as
	// trade_increase. Cancels, closes (including reduce-only orders) and
	// read-only tools stay available so the operator can unwind.
	if killswitch.BlockedByRing0(toolName, req.Params.Arguments) {
		s.mu.Lock()
		s.errorCount++
		s.mu.Unlock()
		metrics.GetRecorder().RecordMCPToolError(toolName, "killswitch_active")
		return &models.MCPResponse{
			JSONRPC: "2.0",
			Error: &models.MCPError{
				Code:    -32001,
				Message: killswitch.ErrorCode,
				Data:    killswitch.ErrActive.Error(),
			},
			ID: req.ID,
		}
	}

	// Product edition: a live profile needs a Pro license to open exposure or
	// change configuration (D8). Reducing and closing are never blocked.
	if licErr := s.checkLicense(ctx, toolName, class); licErr != nil {
		s.mu.Lock()
		s.errorCount++
		s.mu.Unlock()
		metrics.GetRecorder().RecordMCPToolError(toolName, "license_required")
		return &models.MCPResponse{JSONRPC: "2.0", Error: licErr, ID: req.ID}
	}

	// Real money must be explicitly enabled (trading.live_enabled): a live
	// profile cannot open exposure or change configuration while it is off.
	if liveErr := s.checkLiveEnabled(ctx, toolName, class); liveErr != nil {
		s.mu.Lock()
		s.errorCount++
		s.mu.Unlock()
		metrics.GetRecorder().RecordMCPToolError(toolName, "live_trading_disabled")
		return &models.MCPResponse{JSONRPC: "2.0", Error: liveErr, ID: req.ID}
	}

	// Validar sandbox mode
	if err := s.sandboxMode.ValidateOrderTool(toolName, req.Params.Arguments); err != nil {
		s.mu.Lock()
		s.errorCount++
		s.mu.Unlock()
		metrics.GetRecorder().RecordMCPToolError(toolName, "sandbox_validation_failed")
		return &models.MCPResponse{
			JSONRPC: "2.0",
			Error: &models.MCPError{
				Code:    -32000,
				Message: "Sandbox validation failed",
				Data:    err.Error(),
			},
			ID: req.ID,
		}
	}

	// Ejecutar tool
	s.log.WithFields(logrus.Fields{
		"tool":         toolName,
		"params":       redactParams(req.Params.Arguments),
		"sandbox_mode": s.sandboxMode.IsEnabled(),
	}).Info("Executing tool")

	toolStart := time.Now()
	result, err := tool.Execute(ctx, req.Params.Arguments)
	toolDuration := time.Since(toolStart)

	// Registrar métricas de tool (metricas locales)
	toolStatus := "success"
	if err != nil {
		toolStatus = "error"
	}
	recordToolExecution(toolName, toolStatus, toolDuration.Seconds())

	// Registrar métricas en el recorder centralizado
	metrics.RecordMCPToolCall(toolName, toolStatus, toolDuration)

	// Envolver resultado con info de sandbox
	if err == nil {
		result = s.sandboxMode.WrapResult(toolName, result)
		if _, isPaper := paper.HolderFromContext(ctx); isPaper && toolpolicy.ServedByPaper(toolName, class) {
			result = s.markPaperResult(result)
		}
	}

	// Audit log para órdenes
	if s.auditLogger != nil && isOrderTool(toolName) {
		s.auditLogger.LogOrder(toolName, req.Params.Arguments, result, err)
	}

	if err != nil {
		s.mu.Lock()
		s.errorCount++
		s.mu.Unlock()

		s.log.WithError(err).WithField("tool", toolName).Error("Tool execution failed")

		// Registrar error en métricas centralizadas
		metrics.GetRecorder().RecordMCPToolError(toolName, "execution_failed")

		// Risk sentinel rejections keep their stable code (ORDER_CAP_EXCEEDED,
		// ORDER_NOTIONAL_UNKNOWN, RISK_REJECTED) instead of "Internal error".
		if rej, ok := riskRejectionFrom(err); ok {
			return &models.MCPResponse{JSONRPC: "2.0", Error: &models.MCPError{
				Code:    ErrCodeRiskRejected,
				Message: rej.Code,
				Data:    map[string]interface{}{"code": rej.Code, "message": rej.Reason},
			}, ID: req.ID}
		}

		// Errors of the paper broker keep their symbolic code
		if paperErr, ok := paperErrorFrom(err); ok {
			return &models.MCPResponse{JSONRPC: "2.0", Error: paperErr, ID: req.ID}
		}

		// Convertir error a MCPError si es posible
		if mcpErr, ok := err.(*models.MCPError); ok {
			return &models.MCPResponse{
				JSONRPC: "2.0",
				Error:   mcpErr,
				ID:      req.ID,
			}
		}

		return &models.MCPResponse{
			JSONRPC: "2.0",
			Error: &models.MCPError{
				Code:    -32603,
				Message: "Internal error",
				Data:    err.Error(),
			},
			ID: req.ID,
		}
	}

	return &models.MCPResponse{
		JSONRPC: "2.0",
		Result:  result,
		ID:      req.ID,
	}
}

// GetStats retorna estadísticas del servidor
func (s *MCPServer) GetStats() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return map[string]interface{}{
		"running":       s.running,
		"tools_count":   s.registry.Count(),
		"request_count": s.requestCount,
		"error_count":   s.errorCount,
		"gct_connected": s.gctClient.IsConnected(),
	}
}

// GetToolRegistry retorna el registro de tools para uso externo
func (s *MCPServer) GetToolRegistry() *ToolRegistry {
	return s.registry
}

// isOrderTool verifica si un tool es de gestión de órdenes.
//
// CORE-03: this hand-maintained map gates BOTH audit logging (LogOrder) and
// sandbox validation/wrapping (ValidateOrderTool/WrapResult in sandbox.go) —
// every real order-mutating tool MUST be listed here or it silently skips the
// audit trail. submit_futures_order_direct and close_position_direct were
// missing, so direct futures orders/closes (real money, leveraged) were never
// audit-logged. A registry-driven approach (marker interface or naming
// convention on ToolRegistry) would remove the need to keep this list in
// sync by hand, but that refactor touches the tool registration path broadly
// and is left as a follow-up; every known order-mutating tool name must be
// added here in the meantime.
func isOrderTool(toolName string) bool {
	orderTools := map[string]bool{
		"submit_order":                true,
		"submit_market_order":         true,
		"submit_limit_order":          true,
		"submit_stop_order":           true,
		"cancel_order":                true,
		"cancel_all_orders":           true,
		"modify_order":                true,
		"submit_futures_order_direct": true,
		"close_position_direct":       true,
	}
	return orderTools[toolName]
}

// handleAIAnalyze maneja requests de análisis AI
func (s *MCPServer) handleAIAnalyze(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.aiBotHandler == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "AI Bot handler not initialized",
		})
		return
	}

	// Parse request body
	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Invalid JSON",
		})
		return
	}

	// Extract symbol/pair
	symbol, ok := req["pair"].(string)
	if !ok {
		symbol, ok = req["symbol"].(string)
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "Missing 'pair' or 'symbol' field",
			})
			return
		}
	}

	// Call AnalyzePositionHTTP via reflection
	handlerValue := reflect.ValueOf(s.aiBotHandler)
	method := handlerValue.MethodByName("AnalyzePositionHTTP")

	if method.IsValid() {
		method.Call([]reflect.Value{
			reflect.ValueOf(w),
			reflect.ValueOf(r),
		})
	} else {
		// Fallback response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"symbol":     symbol,
			"action":     "WAIT",
			"confidence": 0.0,
			"message":    "AI analysis method not found",
		})
	}
}

// handleAIPositions maneja requests de posiciones
func (s *MCPServer) handleAIPositions(w http.ResponseWriter, r *http.Request) {
	if s.aiBotHandler == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "AI Bot handler not initialized",
		})
		return
	}

	handlerValue := reflect.ValueOf(s.aiBotHandler)
	method := handlerValue.MethodByName("GetPositionsHTTP")
	if method.IsValid() {
		method.Call([]reflect.Value{
			reflect.ValueOf(w),
			reflect.ValueOf(r),
		})
		return
	}

	// Fallback: handler exists but method not found
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	json.NewEncoder(w).Encode(map[string]string{"error": "GetPositionsHTTP not found"})
}

// handleAIStatus maneja requests de status
func (s *MCPServer) handleAIStatus(w http.ResponseWriter, r *http.Request) {
	if s.aiBotHandler == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"ai_enabled": false,
			"status":     "stopped",
			"is_running": false,
		})
		return
	}

	handlerValue := reflect.ValueOf(s.aiBotHandler)
	method := handlerValue.MethodByName("GetStatusHTTP")

	if method.IsValid() {
		method.Call([]reflect.Value{
			reflect.ValueOf(w),
			reflect.ValueOf(r),
		})
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "GetStatusHTTP method not found",
		})
	}
}

// handleComprehensiveAnalysis maneja requests de análisis completo
func (s *MCPServer) handleComprehensiveAnalysis(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.aiBotHandler == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "AI Bot handler not initialized",
		})
		return
	}

	// Call handler via type assertion
	type ComprehensiveAnalysisHandler interface {
		GetComprehensiveAnalysis(w http.ResponseWriter, r *http.Request)
	}

	if handler, ok := s.aiBotHandler.(ComprehensiveAnalysisHandler); ok {
		handler.GetComprehensiveAnalysis(w, r)
	} else {
		// Fallback: return basic response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"message": "Comprehensive analysis endpoint registered but handler not fully implemented",
		})
	}
}

// handleAIStart maneja POST /api/v1/ai-bot/start
func (s *MCPServer) handleAIStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if killswitch.Ring0Active() {
		killswitch.WriteLocked(w)
		return
	}

	if s.aiBotHandler == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "AI Bot handler not initialized",
		})
		return
	}

	// Use reflection to call the method
	handlerValue := reflect.ValueOf(s.aiBotHandler)
	method := handlerValue.MethodByName("StartHTTP")

	if method.IsValid() {
		// Call StartHTTP(w, r)
		method.Call([]reflect.Value{
			reflect.ValueOf(w),
			reflect.ValueOf(r),
		})
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "StartHTTP method not found",
		})
	}
}

// handleAIStop maneja POST /api/v1/ai-bot/stop
func (s *MCPServer) handleAIStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.aiBotHandler == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "AI Bot handler not initialized",
		})
		return
	}

	handlerValue := reflect.ValueOf(s.aiBotHandler)
	method := handlerValue.MethodByName("StopHTTP")

	if method.IsValid() {
		method.Call([]reflect.Value{
			reflect.ValueOf(w),
			reflect.ValueOf(r),
		})
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "StopHTTP method not found",
		})
	}
}

// handleAIPause maneja POST /api/v1/ai-bot/pause
func (s *MCPServer) handleAIPause(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.aiBotHandler == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "AI Bot handler not initialized",
		})
		return
	}

	handlerValue := reflect.ValueOf(s.aiBotHandler)
	method := handlerValue.MethodByName("PauseHTTP")

	if method.IsValid() {
		method.Call([]reflect.Value{
			reflect.ValueOf(w),
			reflect.ValueOf(r),
		})
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "PauseHTTP method not found",
		})
	}
}

// handleAIResume maneja POST /api/v1/ai-bot/resume
func (s *MCPServer) handleAIResume(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if killswitch.Ring0Active() {
		killswitch.WriteLocked(w)
		return
	}

	if s.aiBotHandler == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "AI Bot handler not initialized",
		})
		return
	}

	handlerValue := reflect.ValueOf(s.aiBotHandler)
	method := handlerValue.MethodByName("ResumeHTTP")

	if method.IsValid() {
		method.Call([]reflect.Value{
			reflect.ValueOf(w),
			reflect.ValueOf(r),
		})
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "ResumeHTTP method not found",
		})
	}
}

// handleAIResetState handles POST /api/v1/ai-bot/reset-state
func (s *MCPServer) handleAIResetState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.aiBotHandler == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "AI Bot handler not initialized"})
		return
	}

	handlerValue := reflect.ValueOf(s.aiBotHandler)
	method := handlerValue.MethodByName("ResetStateHTTP")

	if method.IsValid() {
		method.Call([]reflect.Value{
			reflect.ValueOf(w),
			reflect.ValueOf(r),
		})
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]string{"error": "ResetStateHTTP method not found"})
	}
}

// handleAIEmergencyStop maneja POST /api/v1/ai-bot/emergency-stop
func (s *MCPServer) handleAIEmergencyStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.aiBotHandler == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "AI Bot handler not initialized",
		})
		return
	}

	handlerValue := reflect.ValueOf(s.aiBotHandler)
	method := handlerValue.MethodByName("EmergencyStopHTTP")

	if method.IsValid() {
		method.Call([]reflect.Value{
			reflect.ValueOf(w),
			reflect.ValueOf(r),
		})
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "EmergencyStopHTTP method not found",
		})
	}
}

// handleAICloseAllPositions handles POST /api/v1/ai-bot/close-all-positions
func (s *MCPServer) handleAICloseAllPositions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.aiBotHandler == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "AI Bot handler not initialized",
		})
		return
	}

	handlerValue := reflect.ValueOf(s.aiBotHandler)
	method := handlerValue.MethodByName("CloseAllPositionsHTTP")

	if method.IsValid() {
		method.Call([]reflect.Value{
			reflect.ValueOf(w),
			reflect.ValueOf(r),
		})
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "CloseAllPositionsHTTP method not found",
		})
	}
}

// handleAIPerformance handles GET /api/v1/ai-bot/performance
func (s *MCPServer) handleAIPerformance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.aiBotHandler == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "AI Bot handler not initialized",
		})
		return
	}

	handlerValue := reflect.ValueOf(s.aiBotHandler)
	method := handlerValue.MethodByName("GetPerformanceHTTP")

	if method.IsValid() {
		method.Call([]reflect.Value{
			reflect.ValueOf(w),
			reflect.ValueOf(r),
		})
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "GetPerformanceHTTP method not found",
		})
	}
}

// handleAIDiagnostics handles GET /api/v1/ai-bot/diagnostics
func (s *MCPServer) handleAIDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.aiBotHandler == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "AI Bot handler not initialized",
		})
		return
	}

	handlerValue := reflect.ValueOf(s.aiBotHandler)
	method := handlerValue.MethodByName("GetDiagnosticsHTTP")

	if method.IsValid() {
		method.Call([]reflect.Value{
			reflect.ValueOf(w),
			reflect.ValueOf(r),
		})
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "GetDiagnosticsHTTP method not found",
		})
	}
}

// handleAIConfig handles GET/POST/PATCH /api/v1/ai-bot/config
func (s *MCPServer) handleAIConfig(w http.ResponseWriter, r *http.Request) {
	if s.aiBotHandler == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "AI Bot handler not initialized",
		})
		return
	}

	handlerValue := reflect.ValueOf(s.aiBotHandler)

	var method reflect.Value
	if r.Method == http.MethodGet {
		method = handlerValue.MethodByName("GetConfigHTTP")
	} else if r.Method == http.MethodPost || r.Method == http.MethodPatch {
		method = handlerValue.MethodByName("UpdateConfigHTTP")
	} else {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if method.IsValid() {
		method.Call([]reflect.Value{
			reflect.ValueOf(w),
			reflect.ValueOf(r),
		})
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Config method not found",
		})
	}
}

// handleAutonomousEnable maneja POST /api/v1/ai-bot/autonomous/enable
func (s *MCPServer) handleAutonomousEnable(w http.ResponseWriter, r *http.Request) {
	if s.aiBotHandler == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "AI Bot handler not initialized",
		})
		return
	}

	handlerValue := reflect.ValueOf(s.aiBotHandler)
	method := handlerValue.MethodByName("EnableAutonomousModeHTTP")

	if method.IsValid() {
		method.Call([]reflect.Value{
			reflect.ValueOf(w),
			reflect.ValueOf(r),
		})
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Autonomous mode not available",
		})
	}
}

// handleAutonomousStatus maneja GET /api/v1/ai-bot/autonomous/status
func (s *MCPServer) handleAutonomousStatus(w http.ResponseWriter, r *http.Request) {
	if s.aiBotHandler == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "AI Bot handler not initialized",
		})
		return
	}

	handlerValue := reflect.ValueOf(s.aiBotHandler)
	method := handlerValue.MethodByName("GetAutonomousStatusHTTP")

	if method.IsValid() {
		method.Call([]reflect.Value{
			reflect.ValueOf(w),
			reflect.ValueOf(r),
		})
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Autonomous mode not available",
		})
	}
}

// handleLLMAnalysis maneja POST /api/v1/ai/llm-analysis (Task 9.1)
func (s *MCPServer) handleLLMAnalysis(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.aiBotHandler == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "AI Bot handler not initialized",
		})
		return
	}

	// Use reflection to call LLMAnalysisHTTP method
	handlerValue := reflect.ValueOf(s.aiBotHandler)
	method := handlerValue.MethodByName("LLMAnalysisHTTP")

	if method.IsValid() {
		method.Call([]reflect.Value{
			reflect.ValueOf(w),
			reflect.ValueOf(r),
		})
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "LLM analysis endpoint not available",
		})
	}
}

// handleSentimentAnalysis maneja POST /api/v1/ai/sentiment-analysis (Task 9.2)
func (s *MCPServer) handleSentimentAnalysis(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.aiBotHandler == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "AI Bot handler not initialized",
		})
		return
	}

	// Use reflection to call SentimentAnalysisHTTP method
	handlerValue := reflect.ValueOf(s.aiBotHandler)
	method := handlerValue.MethodByName("SentimentAnalysisHTTP")

	if method.IsValid() {
		method.Call([]reflect.Value{
			reflect.ValueOf(w),
			reflect.ValueOf(r),
		})
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Sentiment analysis endpoint not available",
		})
	}
}

// handleStrategyPerformance maneja GET /api/v1/ai/strategy-performance (Task 9.3)
func (s *MCPServer) handleStrategyPerformance(w http.ResponseWriter, r *http.Request) {
	if s.aiBotHandler == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "AI Bot handler not initialized",
		})
		return
	}

	// Use reflection to call StrategyPerformanceHTTP method
	handlerValue := reflect.ValueOf(s.aiBotHandler)
	method := handlerValue.MethodByName("StrategyPerformanceHTTP")

	if method.IsValid() {
		method.Call([]reflect.Value{
			reflect.ValueOf(w),
			reflect.ValueOf(r),
		})
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Strategy performance endpoint not available",
		})
	}
}

// handleCostTracking maneja GET /api/v1/ai/costs (Task 9.4)
func (s *MCPServer) handleCostTracking(w http.ResponseWriter, r *http.Request) {
	if s.aiBotHandler == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "AI Bot handler not initialized",
		})
		return
	}

	// Use reflection to call CostTrackingHTTP method
	handlerValue := reflect.ValueOf(s.aiBotHandler)
	method := handlerValue.MethodByName("CostTrackingHTTP")

	if method.IsValid() {
		method.Call([]reflect.Value{
			reflect.ValueOf(w),
			reflect.ValueOf(r),
		})
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Cost tracking endpoint not available",
		})
	}
}

// handleAIConfiguration maneja GET/PUT /api/v1/ai/config (Task 9.5)
func (s *MCPServer) handleAIConfiguration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.aiBotHandler == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "AI Bot handler not initialized",
		})
		return
	}

	// Use reflection to call AIConfigurationHTTP method
	handlerValue := reflect.ValueOf(s.aiBotHandler)
	method := handlerValue.MethodByName("AIConfigurationHTTP")

	if method.IsValid() {
		method.Call([]reflect.Value{
			reflect.ValueOf(w),
			reflect.ValueOf(r),
		})
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "AI configuration endpoint not available",
		})
	}
}

// handleCompleteAnalysis maneja POST /api/v1/ai/complete-analysis (NEW - Unified analysis endpoint)
func (s *MCPServer) handleCompleteAnalysis(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.aiBotHandler == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "AI Bot handler not initialized",
		})
		return
	}

	// Use reflection to call CompleteAnalysisHTTP method
	handlerValue := reflect.ValueOf(s.aiBotHandler)
	method := handlerValue.MethodByName("CompleteAnalysisHTTP")

	if method.IsValid() {
		method.Call([]reflect.Value{
			reflect.ValueOf(w),
			reflect.ValueOf(r),
		})
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Complete analysis endpoint not available",
		})
	}
}

// handleBacktestingSimple maneja POST /backtesting (Simple working endpoint)
func (s *MCPServer) handleBacktestingSimple(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.log.Info("🚀 SIMPLE BACKTESTING ENDPOINT CALLED")

	// Parse request
	var req BacktestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.WithError(err).Error("Error parsing backtesting request")
		s.writeErrorResponse(w, ErrorCodeInvalidRequest, "Invalid JSON in request body", err.Error(), http.StatusBadRequest)
		return
	}

	// Validate request and set defaults
	if err := req.Validate(); err != nil {
		s.log.WithError(err).Error("Validation failed for simple backtesting request")
		s.writeValidationError(w, err)
		return
	}

	s.log.WithFields(logrus.Fields{
		"symbol":   req.Symbol,
		"strategy": req.Strategy,
		"period":   fmt.Sprintf("%s to %s", req.StartDate, req.EndDate),
	}).Info("Executing simple backtesting")

	// Use shared backtest tool method for consistency
	params := map[string]interface{}{
		"exchange":   req.Exchange,
		"pair":       req.Symbol,
		"strategy":   req.Strategy,
		"start_date": req.StartDate,
		"end_date":   req.EndDate,
	}

	backtestResult, err := s.callBacktestTool(r.Context(), params)
	if err != nil {
		s.log.WithError(err).Error("Shared backtest tool call failed")
		s.writeMCPToolError(w, err)
		return
	}

	// Build response
	response := s.buildBacktestResponse(req, backtestResult, time.Now())

	// Send response
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.log.WithError(err).Error("Error sending response")
		s.writeInternalError(w, fmt.Errorf("failed to encode response: %w", err))
		return
	}

	s.log.WithFields(logrus.Fields{
		"roi":          response.Performance.ROI,
		"total_trades": response.Performance.TotalTrades,
	}).Info("Simple backtesting completed successfully")
}

// handleRunBacktest maneja POST /api/v1/backtesting/run-backtest (NEW - Consolidated backtesting endpoint)
func (s *MCPServer) handleRunBacktest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.log.WithFields(logrus.Fields{
			"method":      r.Method,
			"path":        r.URL.Path,
			"remote_addr": r.RemoteAddr,
		}).Warn("❌ Invalid method for backtesting endpoint")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	startTime := time.Now()
	s.log.WithFields(logrus.Fields{
		"method":       r.Method,
		"path":         r.URL.Path,
		"remote_addr":  r.RemoteAddr,
		"user_agent":   r.Header.Get("User-Agent"),
		"content_type": r.Header.Get("Content-Type"),
	}).Info("🚀 BACKTESTING HTTP ENDPOINT CALLED - Route handler working correctly")
	s.log.Info("✅ handleRunBacktest function reached - routing fix successful")

	// Parsear solicitud
	var req BacktestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.WithError(err).Error("Error al parsear solicitud de backtesting")
		s.writeErrorResponse(w, ErrorCodeInvalidRequest, "Invalid JSON in request body", err.Error(), http.StatusBadRequest)
		return
	}

	// Validar solicitud y establecer valores por defecto
	if err := req.Validate(); err != nil {
		s.log.WithError(err).Error("Validation failed for backtesting request")
		s.writeValidationError(w, err)
		return
	}

	s.log.WithFields(logrus.Fields{
		"symbol":   req.Symbol,
		"strategy": req.Strategy,
		"period":   fmt.Sprintf("%s to %s", req.StartDate, req.EndDate),
	}).Info("Ejecutando backtesting")

	ctx := r.Context()

	// 1. Ejecutar backtesting principal
	s.log.Info("🔧 STEP 1: About to call runBacktestStrategy")
	backtestResult, err := s.runBacktestStrategy(ctx, req)
	if err != nil {
		s.log.WithError(err).Error("Error en backtesting principal")
		s.writeMCPToolError(w, err)
		return
	}
	s.log.WithFields(logrus.Fields{
		"result":     backtestResult,
		"result_nil": backtestResult == nil,
		"result_len": len(backtestResult),
	}).Info("runBacktestStrategy completed")

	// 2. Construir respuesta consolidada
	s.log.WithField("backtestResult", backtestResult).Info("Raw backtest result before building response")
	response := s.buildBacktestResponse(req, backtestResult, startTime)

	// 3. Enviar respuesta
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.log.WithError(err).Error("Error al enviar respuesta")
		s.writeInternalError(w, fmt.Errorf("failed to encode response: %w", err))
		return
	}

	s.log.WithFields(logrus.Fields{
		"execution_time_ms": time.Since(startTime).Milliseconds(),
		"roi":               response.Performance.ROI,
		"total_trades":      response.Performance.TotalTrades,
	}).Info("Backtesting completado exitosamente")
}

// MCPClientAdapter adapta el ToolRegistry para ser usado como MCPClient
type MCPClientAdapter struct {
	toolRegistry *ToolRegistry
	logger       *logrus.Logger
}

// CallTool implementa la interfaz MCPClient
func (m *MCPClientAdapter) CallTool(ctx context.Context, toolName string, params map[string]interface{}) (map[string]interface{}, error) {
	m.logger.WithFields(logrus.Fields{
		"tool":   toolName,
		"params": params,
	}).Debug("Calling MCP tool")

	// Buscar la herramienta en el registry
	tool, exists := m.toolRegistry.Get(toolName)
	if !exists {
		return nil, fmt.Errorf("tool '%s' not found", toolName)
	}

	// Ejecutar la herramienta
	result, err := tool.Execute(ctx, params)
	if err != nil {
		m.logger.WithError(err).WithField("tool", toolName).Error("Tool execution failed")
		return nil, err
	}

	// Convertir resultado a map[string]interface{}
	if resultMap, ok := result.(map[string]interface{}); ok {
		return resultMap, nil
	}

	// Si no es un map, envolver en un map
	return map[string]interface{}{
		"result": result,
	}, nil
}

// Estructuras para el backtesting consolidado
type BacktestRequest struct {
	Symbol     string                 `json:"symbol"`
	Exchange   string                 `json:"exchange"`
	Strategy   string                 `json:"strategy"`
	StartDate  string                 `json:"start_date"`
	EndDate    string                 `json:"end_date"`
	Parameters map[string]interface{} `json:"parameters"`
	Optimize   bool                   `json:"optimize"`
	Language   string                 `json:"language"`
}

// Validate validates the BacktestRequest and sets appropriate defaults
func (r *BacktestRequest) Validate() error {
	// Required fields validation
	if r.Symbol == "" {
		return fmt.Errorf("symbol is required")
	}

	if r.StartDate == "" {
		return fmt.Errorf("start_date is required")
	}

	if r.EndDate == "" {
		return fmt.Errorf("end_date is required")
	}

	// Date format validation
	startTime, err := time.Parse("2006-01-02", r.StartDate)
	if err != nil {
		return fmt.Errorf("invalid start_date format, expected YYYY-MM-DD: %w", err)
	}

	endTime, err := time.Parse("2006-01-02", r.EndDate)
	if err != nil {
		return fmt.Errorf("invalid end_date format, expected YYYY-MM-DD: %w", err)
	}

	// Date range validation
	if endTime.Before(startTime) {
		return fmt.Errorf("end_date must be after start_date")
	}

	if endTime.Equal(startTime) {
		return fmt.Errorf("end_date must be different from start_date")
	}

	// Check if date range is too long (more than 1 year)
	if endTime.Sub(startTime) > 365*24*time.Hour {
		return fmt.Errorf("date range cannot exceed 365 days")
	}

	// Check if dates are not too far in the future
	now := time.Now()
	if startTime.After(now) {
		return fmt.Errorf("start_date cannot be in the future")
	}
	if endTime.After(now) {
		return fmt.Errorf("end_date cannot be in the future")
	}

	// Set defaults for optional fields
	if r.Exchange == "" {
		r.Exchange = "kucoin"
	}

	if r.Strategy == "" {
		r.Strategy = "rsi_macd"
	}

	if r.Language == "" {
		r.Language = "es"
	}

	// Validate exchange
	validExchanges := map[string]bool{
		"kucoin":  true,
		"binance": true,
		"bybit":   true,
	}
	if !validExchanges[r.Exchange] {
		return fmt.Errorf("unsupported exchange: %s, supported: kucoin, binance, bybit", r.Exchange)
	}

	// Validate strategy
	validStrategies := map[string]bool{
		"rsi_macd":        true,
		"bollinger_bands": true,
		"moving_average":  true,
		"momentum":        true,
	}
	if !validStrategies[r.Strategy] {
		return fmt.Errorf("unsupported strategy: %s, supported: rsi_macd, bollinger_bands, moving_average, momentum", r.Strategy)
	}

	// Validate language
	validLanguages := map[string]bool{
		"es": true,
		"en": true,
	}
	if !validLanguages[r.Language] {
		return fmt.Errorf("unsupported language: %s, supported: es, en", r.Language)
	}

	// Validate symbol format (basic check)
	if !strings.Contains(r.Symbol, "-") {
		return fmt.Errorf("invalid symbol format, expected format: BASE-QUOTE (e.g., DOGE-USDT)")
	}

	return nil
}

type BacktestResponse struct {
	BacktestID      string                  `json:"backtest_id"`
	Symbol          string                  `json:"symbol"`
	Strategy        string                  `json:"strategy"`
	Period          BacktestPeriod          `json:"period"`
	Performance     BacktestPerformance     `json:"performance"`
	Trades          []BacktestTrade         `json:"trades"`
	FormattedReport BacktestFormattedReport `json:"formatted_report"`
	Metadata        BacktestMetadata        `json:"metadata"`
}

type BacktestPeriod struct {
	StartDate    string `json:"start_date"`
	EndDate      string `json:"end_date"`
	DurationDays int    `json:"duration_days"`
}

type BacktestPerformance struct {
	ROI              float64 `json:"roi"`
	SharpeRatio      float64 `json:"sharpe_ratio"`
	MaxDrawdown      float64 `json:"max_drawdown"`
	TotalTrades      int     `json:"total_trades"`
	WinningTrades    int     `json:"winning_trades"`
	LosingTrades     int     `json:"losing_trades"`
	WinRate          float64 `json:"win_rate"`
	ProfitFactor     float64 `json:"profit_factor"`
	AvgTradeDuration string  `json:"avg_trade_duration"`
}

type BacktestTrade struct {
	ID         int     `json:"id"`
	Type       string  `json:"type"`
	EntryTime  string  `json:"entry_time"`
	EntryPrice float64 `json:"entry_price"`
	ExitTime   string  `json:"exit_time"`
	ExitPrice  float64 `json:"exit_price"`
	PnL        float64 `json:"pnl"`
	PnLPercent float64 `json:"pnl_percent"`
	Duration   string  `json:"duration"`
}

type BacktestFormattedReport struct {
	Summary          string `json:"summary"`
	DetailedAnalysis string `json:"detailed_analysis"`
	Recommendations  string `json:"recommendations"`
}

type BacktestMetadata struct {
	ExecutionTimeMs int    `json:"execution_time_ms"`
	GeneratedAt     string `json:"generated_at"`
	Language        string `json:"language"`
	DataPoints      int    `json:"data_points"`
}

// runBacktestStrategy ejecuta el backtesting principal usando MCP
func (s *MCPServer) runBacktestStrategy(ctx context.Context, req BacktestRequest) (map[string]interface{}, error) {
	params := map[string]interface{}{
		"exchange":   req.Exchange,
		"pair":       req.Symbol,
		"strategy":   req.Strategy,
		"start_date": req.StartDate,
		"end_date":   req.EndDate,
	}

	if req.Parameters != nil {
		params["parameters"] = req.Parameters
	}

	s.log.WithFields(logrus.Fields{
		"params": params,
	}).Info("About to call backtest_strategy MCP tool")

	result, err := s.callBacktestTool(ctx, params)

	s.log.WithFields(logrus.Fields{
		"result": result,
		"error":  err,
	}).Info("MCP tool call completed")

	return result, err
}

// callBacktestTool is a shared method for executing backtest_strategy MCP tool
// This ensures identical parameter passing and error handling between HTTP and JSON-RPC endpoints
func (s *MCPServer) callBacktestTool(ctx context.Context, params map[string]interface{}) (map[string]interface{}, error) {
	toolName := "backtest_strategy"

	s.log.WithFields(logrus.Fields{
		"tool":   toolName,
		"params": params,
	}).Info("🔧 Executing shared backtest tool method")

	// Validate required parameters
	requiredParams := []string{"exchange", "pair", "strategy", "start_date", "end_date"}
	for _, param := range requiredParams {
		if _, exists := params[param]; !exists {
			return nil, fmt.Errorf("missing required parameter: %s", param)
		}
	}

	// Log parameter validation
	s.log.WithFields(logrus.Fields{
		"tool":             toolName,
		"validated_params": params,
	}).Info("✅ Parameter validation passed")

	// Execute MCP tool using existing callMCPTool method
	result, err := s.callMCPTool(ctx, toolName, params)

	if err != nil {
		s.log.WithError(err).WithFields(logrus.Fields{
			"tool":   toolName,
			"params": params,
		}).Error("❌ Shared backtest tool execution failed")
		return nil, fmt.Errorf("backtest tool execution failed: %w", err)
	}

	s.log.WithFields(logrus.Fields{
		"tool":        toolName,
		"result_keys": getMapKeys(result),
	}).Info("✅ Shared backtest tool execution successful")

	return result, nil
}

// callMCPTool llama una herramienta MCP
func (s *MCPServer) callMCPTool(ctx context.Context, toolName string, params map[string]interface{}) (map[string]interface{}, error) {
	s.log.WithFields(logrus.Fields{
		"tool":   toolName,
		"params": params,
	}).Info("Calling MCP tool from HTTP endpoint")

	// Buscar la herramienta en el registry
	tool, exists := s.registry.Get(toolName)
	if !exists {
		s.log.WithField("tool", toolName).Error("Tool not found in registry")
		return nil, fmt.Errorf("tool '%s' not found", toolName)
	}

	s.log.WithField("tool", toolName).Info("Tool found in registry")

	// Ejecutar la herramienta
	result, err := tool.Execute(ctx, params)
	if err != nil {
		s.log.WithError(err).WithField("tool", toolName).Error("Tool execution failed")
		return nil, err
	}

	// DEBUG: Log raw tool result
	s.log.WithFields(logrus.Fields{
		"tool":        toolName,
		"result_type": fmt.Sprintf("%T", result),
		"result":      result,
	}).Info("🔍 DEBUG: Raw tool execution result")

	// Convertir resultado a map[string]interface{}
	if resultMap, ok := result.(map[string]interface{}); ok {
		s.log.WithFields(logrus.Fields{
			"tool":         toolName,
			"result_keys":  getMapKeys(resultMap),
			"result_count": len(resultMap),
		}).Info("🔍 DEBUG: Tool result is map - returning directly")
		return resultMap, nil
	}

	// Si no es un map, envolver en un map
	wrappedResult := map[string]interface{}{
		"result": result,
	}
	s.log.WithFields(logrus.Fields{
		"tool":           toolName,
		"wrapped_result": wrappedResult,
	}).Info("🔍 DEBUG: Tool result wrapped in map")
	return wrappedResult, nil
}

// buildBacktestResponse construye la respuesta consolidada
func (s *MCPServer) buildBacktestResponse(
	req BacktestRequest,
	backtestResult map[string]interface{},
	startTime time.Time,
) BacktestResponse {

	// DEBUG: Log raw backtest result for debugging
	s.log.WithFields(logrus.Fields{
		"raw_backtest_result": backtestResult,
		"result_keys":         getMapKeys(backtestResult),
		"result_length":       len(backtestResult),
	}).Info("🔍 DEBUG: Raw backtest result received")

	// Extract the actual result data (it may be wrapped under "result" key)
	var actualResult map[string]interface{}
	if resultData, exists := backtestResult["result"]; exists {
		s.log.WithFields(logrus.Fields{
			"result_data_type": fmt.Sprintf("%T", resultData),
			"result_data":      resultData,
		}).Info("🔍 DEBUG: Found result key, checking type")

		if resultMap, ok := resultData.(map[string]interface{}); ok {
			// Data is wrapped under "result" key as map
			actualResult = resultMap
			s.log.WithFields(logrus.Fields{
				"actual_result_keys": getMapKeys(actualResult),
			}).Info("🔍 DEBUG: Found wrapped result data as map")
		} else if backtestStruct, ok := resultData.(*models.BacktestResult); ok {
			// Data is wrapped as BacktestResult struct - convert to map
			actualResult = map[string]interface{}{
				"roi":            backtestStruct.ROI,
				"sharpe_ratio":   backtestStruct.SharpeRatio,
				"max_drawdown":   backtestStruct.MaxDrawdown,
				"total_trades":   backtestStruct.TotalTrades,
				"winning_trades": backtestStruct.WinningTrades,
				"losing_trades":  backtestStruct.LosingTrades,
				"trades":         backtestStruct.Trades,
			}
			s.log.WithFields(logrus.Fields{
				"converted_result": actualResult,
			}).Info("🔍 DEBUG: Converted BacktestResult struct to map")
		} else {
			// Result exists but is unknown type, use original
			actualResult = backtestResult
			s.log.Info("🔍 DEBUG: Result key exists but unknown type, using original")
		}
	} else {
		// Data is directly in backtestResult
		actualResult = backtestResult
		s.log.Info("🔍 DEBUG: No result key found, using direct result data")
	}

	// Extraer datos del resultado del backtesting
	roi := getFloatValue(actualResult, "roi", 0.0)
	sharpeRatio := getFloatValue(actualResult, "sharpe_ratio", 0.0)
	maxDrawdown := getFloatValue(actualResult, "max_drawdown", 0.0)
	totalTrades := getIntValue(actualResult, "total_trades", 0)
	winningTrades := getIntValue(actualResult, "winning_trades", 0)
	losingTrades := getIntValue(actualResult, "losing_trades", 0)

	// DEBUG: Log extracted values
	s.log.WithFields(logrus.Fields{
		"extracted_roi":            roi,
		"extracted_sharpe_ratio":   sharpeRatio,
		"extracted_max_drawdown":   maxDrawdown,
		"extracted_total_trades":   totalTrades,
		"extracted_winning_trades": winningTrades,
		"extracted_losing_trades":  losingTrades,
	}).Info("🔍 DEBUG: Extracted values from backtest result")

	// Calcular métricas derivadas
	winRate := 0.0
	if totalTrades > 0 {
		winRate = float64(winningTrades) / float64(totalTrades) * 100
	}

	profitFactor := 1.0
	if losingTrades > 0 && winningTrades > 0 {
		profitFactor = float64(winningTrades) / float64(losingTrades)
	}

	// Calcular duración del período
	durationDays := s.calculateDurationDays(req.StartDate, req.EndDate)

	// Construir respuesta
	response := BacktestResponse{
		BacktestID: fmt.Sprintf("BT_%d", time.Now().Unix()),
		Symbol:     req.Symbol,
		Strategy:   req.Strategy,
		Period: BacktestPeriod{
			StartDate:    req.StartDate,
			EndDate:      req.EndDate,
			DurationDays: durationDays,
		},
		Performance: BacktestPerformance{
			ROI:              roi,
			SharpeRatio:      sharpeRatio,
			MaxDrawdown:      maxDrawdown,
			TotalTrades:      totalTrades,
			WinningTrades:    winningTrades,
			LosingTrades:     losingTrades,
			WinRate:          winRate,
			ProfitFactor:     profitFactor,
			AvgTradeDuration: "4h 23m", // Placeholder
		},
		Trades:          s.buildTradesList(actualResult),
		FormattedReport: s.buildFormattedReport(req, roi, sharpeRatio, maxDrawdown, winRate, totalTrades),
		Metadata: BacktestMetadata{
			ExecutionTimeMs: int(time.Since(startTime).Milliseconds()),
			GeneratedAt:     time.Now().Format(time.RFC3339),
			Language:        req.Language,
			DataPoints:      durationDays * 24, // Aproximación
		},
	}

	return response
}

// buildTradesList construye la lista de trades del resultado
func (s *MCPServer) buildTradesList(backtestResult map[string]interface{}) []BacktestTrade {
	trades := []BacktestTrade{}

	if tradesData, ok := backtestResult["trades"].([]interface{}); ok {
		for i, tradeData := range tradesData {
			if tradeMap, ok := tradeData.(map[string]interface{}); ok {
				trade := BacktestTrade{
					ID:         i + 1,
					Type:       getStringValue(tradeMap, "type", "BUY"),
					EntryTime:  getStringValue(tradeMap, "entry_time", ""),
					EntryPrice: getFloatValue(tradeMap, "entry_price", 0.0),
					ExitTime:   getStringValue(tradeMap, "exit_time", ""),
					ExitPrice:  getFloatValue(tradeMap, "exit_price", 0.0),
					PnL:        getFloatValue(tradeMap, "pnl", 0.0),
					PnLPercent: getFloatValue(tradeMap, "pnl_percent", 0.0),
					Duration:   getStringValue(tradeMap, "duration", ""),
				}
				trades = append(trades, trade)
			}
		}
	}

	return trades
}

// buildFormattedReport construye el reporte formateado según el idioma
func (s *MCPServer) buildFormattedReport(
	req BacktestRequest,
	roi, sharpeRatio, maxDrawdown, winRate float64,
	totalTrades int,
) BacktestFormattedReport {

	if req.Language == "en" {
		return s.buildEnglishReport(req, roi, sharpeRatio, maxDrawdown, winRate, totalTrades)
	}

	// Español por defecto
	return s.buildSpanishReport(req, roi, sharpeRatio, maxDrawdown, winRate, totalTrades)
}

// buildSpanishReport construye el reporte en español
func (s *MCPServer) buildSpanishReport(
	req BacktestRequest,
	roi, sharpeRatio, maxDrawdown, winRate float64,
	totalTrades int,
) BacktestFormattedReport {

	summary := fmt.Sprintf(`📊 **RESULTADOS DEL BACKTESTING**

✅ **Estrategia**: %s
💰 **ROI**: %.1f%%
📈 **Sharpe Ratio**: %.2f
📉 **Max Drawdown**: %.1f%%
🎯 **Win Rate**: %.1f%%
📋 **Total Trades**: %d
📅 **Período**: %s - %s`,
		strings.ToUpper(req.Strategy),
		roi,
		sharpeRatio,
		maxDrawdown,
		winRate,
		totalTrades,
		req.StartDate,
		req.EndDate)

	detailedAnalysis := fmt.Sprintf(`🔍 **ANÁLISIS DETALLADO**

📊 **Rendimiento**:
  • ROI Total: %.1f%%
  • Sharpe Ratio: %.2f (Excelente > 1.0)
  • Máximo Drawdown: %.1f%%
  • Factor de Beneficio: %.2f

📈 **Estadísticas de Trading**:
  • Total de Operaciones: %d
  • Operaciones Ganadoras: %d
  • Operaciones Perdedoras: %d
  • Tasa de Acierto: %.1f%%

⏱️ **Duración Promedio**: 4h 23m por operación

🎯 **Evaluación de la Estrategia**:
%s`,
		roi,
		sharpeRatio,
		maxDrawdown,
		roi/maxDrawdown, // Profit factor aproximado
		totalTrades,
		int(float64(totalTrades)*winRate/100),
		totalTrades-int(float64(totalTrades)*winRate/100),
		winRate,
		s.getStrategyEvaluation(roi, sharpeRatio, winRate))

	recommendations := s.getSpanishRecommendations(roi, sharpeRatio, maxDrawdown, winRate)

	return BacktestFormattedReport{
		Summary:          summary,
		DetailedAnalysis: detailedAnalysis,
		Recommendations:  recommendations,
	}
}

// buildEnglishReport construye el reporte en inglés
func (s *MCPServer) buildEnglishReport(
	req BacktestRequest,
	roi, sharpeRatio, maxDrawdown, winRate float64,
	totalTrades int,
) BacktestFormattedReport {

	summary := fmt.Sprintf(`📊 **BACKTESTING RESULTS**

✅ **Strategy**: %s
💰 **ROI**: %.1f%%
📈 **Sharpe Ratio**: %.2f
📉 **Max Drawdown**: %.1f%%
🎯 **Win Rate**: %.1f%%
📋 **Total Trades**: %d
📅 **Period**: %s - %s`,
		strings.ToUpper(req.Strategy),
		roi,
		sharpeRatio,
		maxDrawdown,
		winRate,
		totalTrades,
		req.StartDate,
		req.EndDate)

	detailedAnalysis := fmt.Sprintf(`🔍 **DETAILED ANALYSIS**

📊 **Performance**:
  • Total ROI: %.1f%%
  • Sharpe Ratio: %.2f (Excellent > 1.0)
  • Maximum Drawdown: %.1f%%
  • Profit Factor: %.2f

📈 **Trading Statistics**:
  • Total Trades: %d
  • Winning Trades: %d
  • Losing Trades: %d
  • Win Rate: %.1f%%

⏱️ **Average Duration**: 4h 23m per trade

🎯 **Strategy Evaluation**:
%s`,
		roi,
		sharpeRatio,
		maxDrawdown,
		roi/maxDrawdown,
		totalTrades,
		int(float64(totalTrades)*winRate/100),
		totalTrades-int(float64(totalTrades)*winRate/100),
		winRate,
		s.getStrategyEvaluationEnglish(roi, sharpeRatio, winRate))

	recommendations := s.getEnglishRecommendations(roi, sharpeRatio, maxDrawdown, winRate)

	return BacktestFormattedReport{
		Summary:          summary,
		DetailedAnalysis: detailedAnalysis,
		Recommendations:  recommendations,
	}
}

// getStrategyEvaluation evalúa la estrategia en español
func (s *MCPServer) getStrategyEvaluation(roi, sharpeRatio, winRate float64) string {
	if roi > 10 && sharpeRatio > 1.5 && winRate > 60 {
		return "🟢 **EXCELENTE**: Estrategia muy prometedora con alto rendimiento y buen control de riesgo."
	} else if roi > 5 && sharpeRatio > 1.0 && winRate > 50 {
		return "🟡 **BUENA**: Estrategia sólida con potencial de mejora en algunos aspectos."
	} else {
		return "🔴 **REQUIERE OPTIMIZACIÓN**: La estrategia necesita ajustes para mejorar su rendimiento."
	}
}

// getStrategyEvaluationEnglish evalúa la estrategia en inglés
func (s *MCPServer) getStrategyEvaluationEnglish(roi, sharpeRatio, winRate float64) string {
	if roi > 10 && sharpeRatio > 1.5 && winRate > 60 {
		return "🟢 **EXCELLENT**: Very promising strategy with high performance and good risk control."
	} else if roi > 5 && sharpeRatio > 1.0 && winRate > 50 {
		return "🟡 **GOOD**: Solid strategy with potential for improvement in some aspects."
	} else {
		return "🔴 **NEEDS OPTIMIZATION**: Strategy requires adjustments to improve performance."
	}
}

// getSpanishRecommendations genera recomendaciones en español
func (s *MCPServer) getSpanishRecommendations(roi, sharpeRatio, maxDrawdown, winRate float64) string {
	recommendations := []string{
		"🚀 **RECOMENDACIONES**",
		"",
	}

	if roi > 15 {
		recommendations = append(recommendations, "✅ **ROI Excelente**: Mantener la estrategia actual")
	} else if roi > 5 {
		recommendations = append(recommendations, "🔧 **ROI Moderado**: Considerar optimización de parámetros")
	} else {
		recommendations = append(recommendations, "⚠️ **ROI Bajo**: Revisar completamente la estrategia")
	}

	if sharpeRatio > 1.5 {
		recommendations = append(recommendations, "✅ **Excelente relación riesgo/retorno**")
	} else if sharpeRatio > 1.0 {
		recommendations = append(recommendations, "🔧 **Mejorar gestión de riesgo**")
	} else {
		recommendations = append(recommendations, "⚠️ **Riesgo muy alto para el retorno obtenido**")
	}

	if maxDrawdown < -15 {
		recommendations = append(recommendations, "⚠️ **Drawdown Alto**: Implementar stop-loss más estrictos")
	}

	if winRate < 50 {
		recommendations = append(recommendations, "🔧 **Mejorar señales de entrada**: Win rate por debajo del 50%")
	}

	recommendations = append(recommendations, "", "💡 **Próximos Pasos**:")
	recommendations = append(recommendations, "1. **Optimizar Parámetros**: Usar algoritmos genéticos")
	recommendations = append(recommendations, "2. **Walk-Forward Analysis**: Validar robustez temporal")
	recommendations = append(recommendations, "3. **Paper Trading**: Probar en tiempo real antes de capital real")
	recommendations = append(recommendations, "4. **Diversificación**: Probar en múltiples pares de trading")

	return strings.Join(recommendations, "\n")
}

// getEnglishRecommendations genera recomendaciones en inglés
func (s *MCPServer) getEnglishRecommendations(roi, sharpeRatio, maxDrawdown, winRate float64) string {
	recommendations := []string{
		"🚀 **RECOMMENDATIONS**",
		"",
	}

	if roi > 15 {
		recommendations = append(recommendations, "✅ **Excellent ROI**: Maintain current strategy")
	} else if roi > 5 {
		recommendations = append(recommendations, "🔧 **Moderate ROI**: Consider parameter optimization")
	} else {
		recommendations = append(recommendations, "⚠️ **Low ROI**: Completely review strategy")
	}

	if sharpeRatio > 1.5 {
		recommendations = append(recommendations, "✅ **Excellent risk/return ratio**")
	} else if sharpeRatio > 1.0 {
		recommendations = append(recommendations, "🔧 **Improve risk management**")
	} else {
		recommendations = append(recommendations, "⚠️ **Risk too high for obtained return**")
	}

	if maxDrawdown < -15 {
		recommendations = append(recommendations, "⚠️ **High Drawdown**: Implement stricter stop-losses")
	}

	if winRate < 50 {
		recommendations = append(recommendations, "🔧 **Improve entry signals**: Win rate below 50%")
	}

	recommendations = append(recommendations, "", "💡 **Next Steps**:")
	recommendations = append(recommendations, "1. **Optimize Parameters**: Use genetic algorithms")
	recommendations = append(recommendations, "2. **Walk-Forward Analysis**: Validate temporal robustness")
	recommendations = append(recommendations, "3. **Paper Trading**: Test in real-time before real capital")
	recommendations = append(recommendations, "4. **Diversification**: Test on multiple trading pairs")

	return strings.Join(recommendations, "\n")
}

// calculateDurationDays calcula la duración en días entre dos fechas
func (s *MCPServer) calculateDurationDays(startDate, endDate string) int {
	start, err1 := time.Parse("2006-01-02", startDate)
	end, err2 := time.Parse("2006-01-02", endDate)

	if err1 != nil || err2 != nil {
		return 30 // Valor por defecto
	}

	return int(end.Sub(start).Hours() / 24)
}

// Funciones auxiliares para extraer valores de mapas
func getFloatValue(m map[string]interface{}, key string, defaultValue float64) float64 {
	if val, ok := m[key]; ok {
		switch v := val.(type) {
		case float64:
			return v
		case int:
			return float64(v)
		case string:
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				return f
			}
		}
	}
	return defaultValue
}

func getIntValue(m map[string]interface{}, key string, defaultValue int) int {
	if val, ok := m[key]; ok {
		switch v := val.(type) {
		case int:
			return v
		case float64:
			return int(v)
		case string:
			if i, err := strconv.Atoi(v); err == nil {
				return i
			}
		}
	}
	return defaultValue
}

func getStringValue(m map[string]interface{}, key string, defaultValue string) string {
	if val, ok := m[key]; ok {
		if s, ok := val.(string); ok {
			return s
		}
	}
	return defaultValue
}

// getMapKeys returns the keys of a map for debugging
func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// BacktestError represents structured error responses for backtesting endpoints
type BacktestError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Details   string `json:"details,omitempty"`
	Timestamp string `json:"timestamp"`
	RequestID string `json:"request_id,omitempty"`
}

// Error codes for different failure scenarios
const (
	ErrorCodeValidation     = "VALIDATION_ERROR"
	ErrorCodeMCPTool        = "MCP_TOOL_ERROR"
	ErrorCodeInternal       = "INTERNAL_ERROR"
	ErrorCodeTimeout        = "TIMEOUT_ERROR"
	ErrorCodeRateLimit      = "RATE_LIMIT_ERROR"
	ErrorCodeInvalidRequest = "INVALID_REQUEST"
)

// writeErrorResponse writes a structured error response with appropriate HTTP status code
func (s *MCPServer) writeErrorResponse(w http.ResponseWriter, code string, message string, details string, statusCode int) {
	errorResp := BacktestError{
		Code:      code,
		Message:   message,
		Details:   details,
		Timestamp: time.Now().Format(time.RFC3339),
		RequestID: fmt.Sprintf("REQ_%d", time.Now().UnixNano()),
	}

	// Log error with context
	s.log.WithFields(logrus.Fields{
		"error_code":    code,
		"error_message": message,
		"error_details": details,
		"status_code":   statusCode,
		"request_id":    errorResp.RequestID,
	}).Error("Backtesting endpoint error")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(errorResp); err != nil {
		s.log.WithError(err).Error("Failed to encode error response")
		// Fallback to plain text error
		http.Error(w, message, statusCode)
	}
}

// writeValidationError writes a validation error response
func (s *MCPServer) writeValidationError(w http.ResponseWriter, err error) {
	s.writeErrorResponse(w, ErrorCodeValidation, "Request validation failed", err.Error(), http.StatusBadRequest)
}

// writeMCPToolError writes an MCP tool execution error response
func (s *MCPServer) writeMCPToolError(w http.ResponseWriter, err error) {
	s.writeErrorResponse(w, ErrorCodeMCPTool, "Backtesting execution failed", err.Error(), http.StatusInternalServerError)
}

// writeInternalError writes an internal server error response
func (s *MCPServer) writeInternalError(w http.ResponseWriter, err error) {
	s.writeErrorResponse(w, ErrorCodeInternal, "Internal server error", err.Error(), http.StatusInternalServerError)
}

// HandlerRegistrar is the interface that all HTTP handlers must implement
type HandlerRegistrar interface {
	RegisterRoutes(router interface{})
}

// RegisterHandlers stores handler references for introspection/logging.
// Route registration happens in main.go via each handler's RegisterRoutesGin method.
func (s *MCPServer) RegisterHandlers(handlers ...interface{}) {
	for _, handler := range handlers {
		if handler == nil {
			continue
		}

		// Log handler registration
		handlerType := reflect.TypeOf(handler)
		if handlerType != nil {
			s.log.WithField("handler_type", handlerType.String()).Info("Registered API handler")
		}
	}

	s.log.WithField("handler_count", len(handlers)).Info("API handlers registered with MCP server")
}

// handleAIOrderFlow dispatches GET /api/v1/ai-bot/orderflow
func (s *MCPServer) handleAIOrderFlow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.aiBotHandler == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "AI Bot handler not initialized"})
		return
	}
	handlerValue := reflect.ValueOf(s.aiBotHandler)
	method := handlerValue.MethodByName("GetOrderFlowHTTP")
	if method.IsValid() {
		method.Call([]reflect.Value{reflect.ValueOf(w), reflect.ValueOf(r)})
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]string{"error": "GetOrderFlowHTTP method not found"})
	}
}

// handleAIFeedback dispatches GET /api/v1/ai-bot/feedback
// handleRegisterFCMToken handles POST /api/v1/user/fcm-token
func (s *MCPServer) handleRegisterFCMToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		Token     string `json:"fcm_token"`
		Platform  string `json:"platform"`
		Timestamp string `json:"timestamp"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Token == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid or missing fcm_token"})
		return
	}

	if body.Platform == "" {
		body.Platform = "flutter"
	}
	s.log.WithFields(logrus.Fields{
		"platform": body.Platform,
		"token":    body.Token[:min(20, len(body.Token))],
	}).Info("📱 FCM token registered via REST")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"message":  "FCM token registered",
		"platform": body.Platform,
	})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (s *MCPServer) handleAIFeedback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.aiBotHandler == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "AI Bot handler not initialized"})
		return
	}
	handlerValue := reflect.ValueOf(s.aiBotHandler)
	method := handlerValue.MethodByName("GetFeedbackHTTP")
	if method.IsValid() {
		method.Call([]reflect.Value{reflect.ValueOf(w), reflect.ValueOf(r)})
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]string{"error": "GetFeedbackHTTP method not found"})
	}
}

// handleBotTuning dispatches GET and PATCH /api/v1/bot/tuning to the tuning handler.
func (s *MCPServer) handleBotTuning(w http.ResponseWriter, r *http.Request) {
	if s.tuningHandler == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "tuning handler not initialized"})
		return
	}

	hv := reflect.ValueOf(s.tuningHandler)
	args := []reflect.Value{reflect.ValueOf(w), reflect.ValueOf(r)}

	switch r.Method {
	case http.MethodGet:
		if m := hv.MethodByName("GetAll"); m.IsValid() {
			m.Call(args)
			return
		}
	case http.MethodPatch:
		if m := hv.MethodByName("Update"); m.IsValid() {
			m.Call(args)
			return
		}
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	json.NewEncoder(w).Encode(map[string]string{"error": "method not implemented in tuning handler"})
}

// handleBotTuningOne dispatches GET /api/v1/bot/tuning/{key} to the tuning handler.
func (s *MCPServer) handleBotTuningOne(w http.ResponseWriter, r *http.Request) {
	if s.tuningHandler == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "tuning handler not initialized"})
		return
	}

	hv := reflect.ValueOf(s.tuningHandler)
	if m := hv.MethodByName("GetOne"); m.IsValid() {
		m.Call([]reflect.Value{reflect.ValueOf(w), reflect.ValueOf(r)})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	json.NewEncoder(w).Encode(map[string]string{"error": "GetOne method not found in tuning handler"})
}

// requireMinLevel rejects callers whose JWT user_level is below min. When JWT
// auth is disabled (development) the level is absent and the request passes.
func (s *MCPServer) requireMinLevel(min int, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.jwtAuthEnabled {
			level, ok := UserLevelFromContext(r.Context())
			if !ok || level < min {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprintf(w, `{"error":"forbidden: user level %d required"}`, min)
				return
			}
		}
		next(w, r)
	}
}

// callAIBotHandler dispatches a POST to the named AIBotHandler method.
func (s *MCPServer) callAIBotHandler(method string, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.aiBotHandler == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "AI Bot handler not initialized"})
		return
	}
	m := reflect.ValueOf(s.aiBotHandler).MethodByName(method)
	if !m.IsValid() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]string{"error": method + " method not found"})
		return
	}
	m.Call([]reflect.Value{reflect.ValueOf(w), reflect.ValueOf(r)})
}

// handleAIManualEntry handles POST /api/v1/ai-bot/manual-entry (Ring 0 locks it).
func (s *MCPServer) handleAIManualEntry(w http.ResponseWriter, r *http.Request) {
	if killswitch.Ring0Active() {
		killswitch.WriteLocked(w)
		return
	}
	s.callAIBotHandler("ManualEntryHTTP", w, r)
}

// handleAIManualClose handles POST /api/v1/ai-bot/manual-close (closing is
// always allowed, kill switch or not).
func (s *MCPServer) handleAIManualClose(w http.ResponseWriter, r *http.Request) {
	s.callAIBotHandler("ManualCloseHTTP", w, r)
}

// riskRejectionFrom finds a risk sentinel rejection in a tool error. Order
// tools wrap it in a generic *models.MCPError (-32603, "Internal error") with
// the original text in Data, so that form is checked too.
func riskRejectionFrom(err error) (*risk.RejectedError, bool) {
	if rej, ok := risk.RejectionFrom(err); ok {
		return rej, true
	}
	var mcpErr *models.MCPError
	if errors.As(err, &mcpErr) {
		if data, ok := mcpErr.Data.(string); ok {
			return risk.RejectionFrom(errors.New(data))
		}
	}
	return nil, false
}
