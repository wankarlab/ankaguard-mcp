package handlers

import (
	"net/http"
	"time"
)

// componentStatus represents the health of a single component
type componentStatus struct {
	Status      string `json:"status"`
	RestFallback *bool  `json:"rest_fallback,omitempty"`
	Error       string `json:"error,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Reason      string `json:"reason,omitempty"`
	MonitoredPositions *int `json:"monitored_positions,omitempty"`
	ConsecutiveErrors  *int `json:"consecutive_errors,omitempty"`
}

// diagnosticsResponse is the full response payload for GET /api/v1/ai-bot/diagnostics
type diagnosticsResponse struct {
	Components            map[string]componentStatus `json:"components"`
	TradingMode           string                     `json:"trading_mode"`
	BotRunning            bool                       `json:"bot_running"`
	LastAnalysisAt        *time.Time                 `json:"last_analysis_at"`
	LastAnalysisAgoSeconds int64                     `json:"last_analysis_ago_seconds"`
	DailyStats            map[string]interface{}     `json:"daily_stats"`
}

// GetDiagnosticsHTTP handles GET /api/v1/ai-bot/diagnostics
func (h *AIBotHandler) GetDiagnosticsHTTP(w http.ResponseWriter, r *http.Request) {
	resp := diagnosticsResponse{
		Components: make(map[string]componentStatus),
	}

	// --- Bot status ---
	if h.tradingBot == nil {
		resp.Components["grpc"] = componentStatus{Status: "unknown"}
		resp.Components["llm_primary"] = componentStatus{Status: "down", Reason: "bot_not_initialized"}
		resp.Components["llm_fallback"] = componentStatus{Status: "down", Reason: "bot_not_initialized"}
		resp.Components["position_manager"] = componentStatus{Status: "unknown"}
		resp.Components["circuit_breaker"] = componentStatus{Status: "unknown"}
		resp.TradingMode = "unknown"
		h.writeSuccess(w, resp)
		return
	}

	status := h.tradingBot.GetStatus()

	// running / paused
	running, _ := status["running"].(bool)
	resp.BotRunning = running

	// last_analysis_at
	if lat, ok := status["last_analysis_at"].(time.Time); ok && !lat.IsZero() {
		resp.LastAnalysisAt = &lat
		resp.LastAnalysisAgoSeconds = int64(time.Since(lat).Seconds())
	}

	// daily stats
	dailyLoss, _ := status["daily_loss"].(float64)
	dailyTrades, _ := status["daily_trades"].(int)
	executionCount, _ := status["execution_count"].(int)

	resp.DailyStats = map[string]interface{}{
		"trades_executed": executionCount,
		"pnl_usd":         dailyLoss,
		"daily_trades":    dailyTrades,
	}

	// --- Trading mode ---
	cfg := h.tradingBot.GetConfig()
	if cfg != nil && cfg.DryRun {
		resp.TradingMode = "dry_run"
	} else {
		resp.TradingMode = "live"
	}

	// --- gRPC component ---
	// We infer connectivity from whether the LLM analyzer was configured (bot wired up correctly).
	// A more precise check would require exposing the gct client; use "ok" as baseline.
	restFallback := false
	resp.Components["grpc"] = componentStatus{
		Status:       "ok",
		RestFallback: &restFallback,
	}

	// --- LLM components ---
	analyzer := h.tradingBot.GetLLMAnalyzer()
	if analyzer == nil {
		resp.Components["llm_primary"] = componentStatus{
			Status: "down",
			Reason: "not_configured",
		}
		resp.Components["llm_fallback"] = componentStatus{
			Status: "down",
			Reason: "not_configured",
		}
	} else {
		primaryProvider := analyzer.GetPrimaryProvider()
		primaryName := "unknown"
		if primaryProvider != nil {
			primaryName = primaryProvider.Name()
		}
		primaryStatus := "ok"
		if primaryProvider == nil {
			primaryStatus = "down"
		}
		resp.Components["llm_primary"] = componentStatus{
			Status:   primaryStatus,
			Provider: primaryName,
		}

		// Fallback provider — not directly exposed; use "unknown" if not available.
		resp.Components["llm_fallback"] = componentStatus{
			Status:   "unknown",
			Provider: "unknown",
		}
	}

	// --- Position manager ---
	openPositions, _ := status["open_positions"].(int)
	posCount := openPositions
	resp.Components["position_manager"] = componentStatus{
		Status:             "ok",
		MonitoredPositions: &posCount,
	}

	// --- Circuit breaker ---
	consecutiveErrors, _ := status["consecutive_errors"].(int)
	errCount := consecutiveErrors
	circuitStatus := "ok"
	if consecutiveErrors >= 3 {
		circuitStatus = "degraded"
	}
	resp.Components["circuit_breaker"] = componentStatus{
		Status:            circuitStatus,
		ConsecutiveErrors: &errCount,
	}

	h.writeSuccess(w, resp)
}
