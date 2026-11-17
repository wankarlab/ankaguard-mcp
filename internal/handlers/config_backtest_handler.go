// Package handlers — config_backtest_handler.go adds the backtest endpoints to the
// dynamic config handler group (TC-7.5 / TC-7.6).
//
// Endpoints:
//
//	POST /api/v1/config/backtest        — enqueue a new backtest run (async, returns 202)
//	GET  /api/v1/config/backtest/:id    — poll status, metrics, and first 10 trades
package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/rantipay/trading-mcp/internal/backtest/domain"
	"github.com/rantipay/trading-mcp/internal/backtest/repository"
)

// ConfigBacktestHandler exposes the backtest lifecycle endpoints.
// It is wired into the same /api/v1/config router group as ConfigHandler.
type ConfigBacktestHandler struct {
	repo repository.Repository
}

// NewConfigBacktestHandler creates a handler backed by the given backtest repository.
func NewConfigBacktestHandler(repo repository.Repository) *ConfigBacktestHandler {
	return &ConfigBacktestHandler{repo: repo}
}

// Register mounts the backtest endpoints on the supplied RouterGroup.
//
//	POST   /backtest
//	GET    /backtest/:id
func (h *ConfigBacktestHandler) Register(rg *gin.RouterGroup) {
	rg.POST("/backtest", h.createBacktest)
	rg.GET("/backtest/:id", h.getBacktest)
}

// ---- request / response types ----

// createBacktestRequest is the payload for POST /api/v1/config/backtest.
type createBacktestRequest struct {
	By          string              `json:"by"`       // operator identifier
	Baseline    string              `json:"baseline"` // "current" | "preset:<name>"
	Changes     []configChangeEntry `json:"changes"`
	Pair        string              `json:"pair"`
	Exchange    string              `json:"exchange"`
	HorizonDays int                 `json:"horizon_days"`
}

// configChangeEntry is one key→value override in the backtest request.
type configChangeEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// createBacktestResponse is the 202 response for POST /api/v1/config/backtest.
type createBacktestResponse struct {
	RunID      int64  `json:"run_id"`
	Status     string `json:"status"`
	ETASeconds int    `json:"eta_seconds"`
	ResultURL  string `json:"result_url"`
}

// getBacktestResponse is the response for GET /api/v1/config/backtest/:id.
type getBacktestResponse struct {
	ID          int64                    `json:"id"`
	Status      string                   `json:"status"`
	StartedAt   *time.Time               `json:"started_at,omitempty"`
	FinishedAt  *time.Time               `json:"finished_at,omitempty"`
	Metrics     *domain.Metrics          `json:"metrics,omitempty"`
	VsBaseline  *baselineDiff            `json:"vs_baseline,omitempty"`
	TradePreview []domain.BacktestTrade  `json:"trades_preview,omitempty"`
	Error       *string                  `json:"error,omitempty"`
}

// baselineDiff summarises changes in key metrics vs the current live config.
type baselineDiff struct {
	WinRateDelta  string `json:"win_rate_delta,omitempty"`
	SharpeDelta   string `json:"sharpe_delta,omitempty"`
	NTradesDelta  string `json:"n_trades_delta,omitempty"`
}

// ---- handlers ----

// createBacktest enqueues a new backtest run and returns 202 Accepted immediately.
//
// POST /api/v1/config/backtest
func (h *ConfigBacktestHandler) createBacktest(c *gin.Context) {
	var req createBacktestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "invalid request body: " + err.Error()})
		return
	}

	// Validation
	if req.Pair == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "pair is required"})
		return
	}
	if req.Exchange == "" {
		req.Exchange = "kucoin"
	}
	if req.HorizonDays <= 0 {
		req.HorizonDays = 7
	}
	if req.Baseline == "" {
		req.Baseline = "current"
	}
	if req.By == "" {
		req.By = "unknown"
	}

	// Build the domain request.
	overrides := make(map[string]string, len(req.Changes))
	for _, ch := range req.Changes {
		overrides[ch.Key] = ch.Value
	}

	now := time.Now().UTC()
	domainReq := domain.BacktestRequest{
		Overrides:      overrides,
		BaselinePreset: req.Baseline,
		Pair:           req.Pair,
		Exchange:       req.Exchange,
		From:           now.AddDate(0, 0, -req.HorizonDays),
		To:             now,
		TriggeredBy:    req.By,
	}

	runID, err := h.repo.CreateRun(c.Request.Context(), domainReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to create backtest run: " + err.Error()})
		return
	}

	c.JSON(http.StatusAccepted, createBacktestResponse{
		RunID:      runID,
		Status:     "pending",
		ETASeconds: 45,
		ResultURL:  fmt.Sprintf("/api/v1/config/backtest/%d", runID),
	})
}

// getBacktest retrieves the current state of a backtest run.
//
// GET /api/v1/config/backtest/:id
func (h *ConfigBacktestHandler) getBacktest(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "invalid run id"})
		return
	}

	ctx := c.Request.Context()
	run, err := h.repo.GetRun(ctx, id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": err.Error()})
		return
	}

	resp := getBacktestResponse{
		ID:         run.ID,
		Status:     run.Status,
		StartedAt:  run.StartedAt,
		FinishedAt: run.FinishedAt,
		Metrics:    run.Metrics,
		Error:      run.Error,
	}

	// Attach first 10 trades for preview (only when done).
	if run.Status == "done" {
		trades, err := h.repo.ListTrades(ctx, run.ID, 10)
		if err == nil {
			resp.TradePreview = trades
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": resp})
}
