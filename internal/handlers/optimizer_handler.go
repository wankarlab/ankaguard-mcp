package handlers

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	optpkg "github.com/rantipay/trading-mcp/internal/optimization"
	optdomain "github.com/rantipay/trading-mcp/internal/optimization/domain"
	optrepo "github.com/rantipay/trading-mcp/internal/optimization/repository"
)

// OptimizerHandler exposes REST endpoints for the optimizer subsystem.
//
// Endpoints (mounted under /api/v1/config):
//
//	POST   /optimizer/start      — start a new optimization run (202 Accepted)
//	GET    /optimizer/runs       — list runs with optional ?status=&pair= filters
//	GET    /optimizer/runs/:id   — detail: run + trials + presets
//	DELETE /optimizer/runs/:id   — cancel a pending/running run
type OptimizerHandler struct {
	svc     *optpkg.OptimizerService
	optRepo optrepo.OptimizerRepository
	log     *logrus.Logger
}

// NewOptimizerHandler creates an OptimizerHandler.
func NewOptimizerHandler(svc *optpkg.OptimizerService, optRepo optrepo.OptimizerRepository, log *logrus.Logger) *OptimizerHandler {
	return &OptimizerHandler{svc: svc, optRepo: optRepo, log: log}
}

// Register mounts all optimizer endpoints on the supplied RouterGroup.
func (h *OptimizerHandler) Register(rg *gin.RouterGroup) {
	og := rg.Group("/optimizer")
	og.POST("/start", h.startRun)
	og.GET("/runs", h.listRuns)
	og.GET("/runs/:id", h.getRun)
	og.DELETE("/runs/:id", h.cancelRun)
}

// ──────────────────────────────────────────────────────────────────────────────
// startRun
// ──────────────────────────────────────────────────────────────────────────────

// startRunRequest is the body for POST /optimizer/start.
type startRunRequest struct {
	Space        string `json:"space"`         // "scoring"|"trap"|"indicator_period"|"risk"|"custom"
	CustomKeys   []string `json:"custom_keys"` // used only when space=="custom"
	Objective    string `json:"objective"`     // e.g. "maximize_sharpe"
	Pair         string `json:"pair"`          // e.g. "NEAR-USDT"
	Exchange     string `json:"exchange"`      // e.g. "KuCoin"
	HorizonDays  int    `json:"horizon_days"`  // default 14
	Iterations   int    `json:"iterations"`    // default 50, max 200
	By           string `json:"by"`            // actor / user ID
}

// startRun handles POST /optimizer/start.
// Returns 202 Accepted with the run_id; the optimization executes in a goroutine.
func (h *OptimizerHandler) startRun(c *gin.Context) {
	var body startRunRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "invalid body: " + err.Error()})
		return
	}
	if body.Pair == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "pair is required"})
		return
	}
	if body.Exchange == "" {
		body.Exchange = "KuCoin"
	}
	if body.Objective == "" {
		body.Objective = "maximize_sharpe"
	}
	if body.HorizonDays <= 0 {
		body.HorizonDays = 14
	}
	if body.Iterations <= 0 {
		body.Iterations = 50
	}
	if body.Iterations > 200 {
		body.Iterations = 200
	}
	if body.By == "" {
		body.By = c.GetHeader("X-By-User")
	}
	if body.By == "" {
		body.By = "api"
	}
	if body.Space == "" {
		body.Space = "scoring"
	}

	// Resolve param space.
	space, ok := optpkg.SpacesByName[body.Space]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "unknown space: " + body.Space + "; available: scoring, trap, indicator_period, risk",
		})
		return
	}

	// Parse objective.
	objFn, err := optpkg.ParseObjective(body.Objective)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	now := time.Now().UTC()
	horizonFrom := now.Add(-time.Duration(body.HorizonDays) * 24 * time.Hour)

	// Persist run record synchronously.
	run := &optdomain.OptimizerRun{
		Pair:        body.Pair,
		Objective:   body.Objective,
		SpaceName:   body.Space,
		Iterations:  body.Iterations,
		TriggeredBy: body.By,
	}
	runID, err := h.optRepo.CreateRun(ctx, run)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to create run: " + err.Error()})
		return
	}

	req := optpkg.OptimizeRequest{
		Space:         space,
		SpaceName:     body.Space,
		ObjectiveName: body.Objective,
		Objective:     objFn,
		Pair:          body.Pair,
		Exchange:      body.Exchange,
		HorizonFrom:   horizonFrom,
		HorizonTo:     now,
		Iterations:    body.Iterations,
		By:            body.By,
	}

	// Execute in background — the endpoint returns immediately.
	go func() {
		bgCtx := context.Background()
		if _, err := h.svc.Run(bgCtx, runID, req); err != nil {
			h.log.WithError(err).WithField("run_id", runID).Error("optimizer: background run failed")
		}
	}()

	c.JSON(http.StatusAccepted, gin.H{
		"success": true,
		"data": gin.H{
			"run_id":  runID,
			"pair":    body.Pair,
			"status":  "pending",
			"message": "Optimization run started. Poll GET /optimizer/runs/" + strconv.FormatInt(runID, 10) + " for progress.",
		},
	})
}

// ──────────────────────────────────────────────────────────────────────────────
// listRuns
// ──────────────────────────────────────────────────────────────────────────────

// listRuns handles GET /optimizer/runs?status=&pair=&limit=.
func (h *OptimizerHandler) listRuns(c *gin.Context) {
	status := c.Query("status")
	pair := c.Query("pair")
	limit := 50
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	runs, err := h.optRepo.ListRuns(c.Request.Context(), status, pair, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}
	if runs == nil {
		runs = []*optdomain.OptimizerRun{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"runs": runs, "total": len(runs)}})
}

// ──────────────────────────────────────────────────────────────────────────────
// getRun
// ──────────────────────────────────────────────────────────────────────────────

// getRun handles GET /optimizer/runs/:id.
func (h *OptimizerHandler) getRun(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "invalid run id"})
		return
	}
	ctx := c.Request.Context()

	run, err := h.optRepo.GetRun(ctx, id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": err.Error()})
		return
	}

	// Attach trials.
	trials, err := h.optRepo.ListTrials(ctx, id)
	if err != nil {
		h.log.WithError(err).WithField("run_id", id).Warn("failed to load trials for run detail")
	} else {
		run.Trials = trials
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": run})
}

// ──────────────────────────────────────────────────────────────────────────────
// cancelRun
// ──────────────────────────────────────────────────────────────────────────────

// cancelRun handles DELETE /optimizer/runs/:id.
func (h *OptimizerHandler) cancelRun(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "invalid run id"})
		return
	}
	if err := h.optRepo.CancelRun(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusConflict, gin.H{"success": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"run_id": id, "status": "cancelled"}})
}
