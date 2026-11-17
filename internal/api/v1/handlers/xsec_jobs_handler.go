// Package handlers — REST endpoints for the xsec-live async job runner
// (F4.7) and dynamic xsec_params tuning. See xsec_job_runner.go for the
// mode->argv mapping, single-flight, and dry-run-first safety rules.
package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/rantipay/trading-mcp/internal/xsec"
)

// xsecJobsOutputTruncateBytes caps the per-row output size returned by the
// list endpoint (the detail endpoint returns the full row).
const xsecJobsOutputTruncateBytes = 4096

// XSecParamsWriteLevel is the minimum Kong Auth Service level required to
// mutate xsec_params or start a real-money job mode (rebalance-live,
// cancel-order). Matches the level used for other real-money/admin mutations
// in this router (e.g. specs demote). Exported so the router can apply the
// identical level to the PUT /xsec/params route middleware (job-mode gating
// happens inside CreateJob since it depends on the request body, not the path).
const XSecParamsWriteLevel = 2

// xsecParamWhitelist is the exhaustive set of xsec_params keys writable via
// PUT /api/v1/xsec/params. Any other key is rejected — this endpoint must
// never become a generic KV store for arbitrary config.
var xsecParamWhitelist = func() map[string]bool {
	w := map[string]bool{
		"xsec.maker_first":      true,
		"xsec.maker_timeout":    true,
		"xsec.resize_threshold": true,
		"xsec.book_filter":      true,
		"xsec.rank_buffer":      true,
		"xsec.k":                true,
		"xsec.lookback_days":    true,
		"xsec.min_turnover_usd": true,
		// Candidate-config forward paper A/B (ShadowPanel): toggle + config.
		"xsec.cand_shadow":   true,
		"xsec.cand_lookback": true,
		"xsec.cand_k":        true,
	}
	// Basket-health alert thresholds (edited from ParamsEditor, hot-reloaded by
	// the XSecAlertMonitor each tick).
	for _, k := range AlertParamKeys {
		w[k] = true
	}
	return w
}()

// xsecJobModesRequiringLevel2 are the job modes gated to Level 2+ (real-money
// or order-mutating actions). sync-book and rebalance-dry are read-only/dry
// and available to any JWT-authenticated user.
var xsecJobModesRequiringLevel2 = map[string]bool{
	XSecJobModeRebalanceLive: true,
	XSecJobModeCancelOrder:   true,
	XSecJobModeCloseLeg:      true,
	XSecJobModeCloseAll:      true,
	XSecJobModeRelaunch:      true,
	XSecJobModeNeutralize:    true,
}

// XSecJobsHandler serves the async job endpoints and xsec_params tuning.
type XSecJobsHandler struct {
	db     *sql.DB
	runner *XSecJobRunner
	log    *logrus.Logger
}

// NewXSecJobsHandler creates a handler over the shared trading DB and job
// runner. db may be nil in constrained test/CI setups; every handler here
// then responds 503.
func NewXSecJobsHandler(db *sql.DB, runner *XSecJobRunner, log *logrus.Logger) *XSecJobsHandler {
	if log == nil {
		log = logrus.New()
	}
	return &XSecJobsHandler{db: db, runner: runner, log: log}
}

// xsecJobCreateRequest is the POST /api/v1/xsec/jobs body. K/MaxPosUSD/
// Lookback/MaxTotalUSD are optional overrides for rebalance-dry/
// rebalance-live; OrderID is required (and validated) for cancel-order; Pair
// is required (and validated) for close-leg; Rebal..Windows are optional
// overrides for backtest (K/Lookback are shared with rebalance — same
// meaning). Every other flag is server-fixed — see XSecJobRunner.BuildArgv.
type xsecJobCreateRequest struct {
	Mode        string   `json:"mode" binding:"required"`
	K           *int     `json:"k"`
	MaxPosUSD   *float64 `json:"max_pos_usd"`
	Lookback    *int     `json:"lookback"`
	MaxTotalUSD *float64 `json:"max_total_usd"`
	OrderID     string   `json:"order_id"`
	Pair        string   `json:"pair"`

	// Autoconfig-only (mode "autoconfig"; Capital below doubles as the equity
	// override and Lookback as the momentum lookback).
	Utilization *float64 `json:"utilization"`

	// Backtest-only overrides (cmd/xsec-sim flags).
	Rebal          *int     `json:"rebal"`
	Band           *int     `json:"band"`
	Cost           *float64 `json:"cost"`
	Funding        *float64 `json:"funding"`
	Capital        *float64 `json:"capital"`
	VolWeights     *bool    `json:"vol_weights"`
	MinTurnoverUSD *float64 `json:"min_turnover_usd"`
	Windows        *int     `json:"windows"`

	// Backtest-sweep-only.
	SweepParam  string    `json:"sweep_param"`
	SweepValues []float64 `json:"sweep_values"`

	// Backtest-only, advanced (Fase 3).
	StopShort      *float64 `json:"stop_short"`
	StopLong       *float64 `json:"stop_long"`
	TPLiquidate    *float64 `json:"tp_liquidate"`
	TPLeg          *float64 `json:"tp_leg"`
	TPRebalance    *float64 `json:"tp_rebalance"`
	TrailTP        *float64 `json:"trail_tp"`
	RegimeFloor    *float64 `json:"regime_floor"`
	RegimeWindow   *int     `json:"regime_window"`
	MakerFillProb  *float64 `json:"maker_fill_prob"`
	TakerCost      *float64 `json:"taker_cost"`
	VolTarget      *float64 `json:"vol_target"`
	MaxShortWeight *float64 `json:"max_short_weight"`
	FundingTilt    *float64 `json:"funding_tilt"`
	Pairs          string   `json:"pairs"`
	StartFrac      *float64 `json:"start_frac"`
	EndFrac        *float64 `json:"end_frac"`
}

// xsecJobResponse is the JSON shape of one xsec_jobs row.
type xsecJobResponse struct {
	ID         int64      `json:"id"`
	Mode       string     `json:"mode"`
	Args       []string   `json:"args"`
	Status     string     `json:"status"`
	Output     string     `json:"output"`
	ExitCode   *int64     `json:"exit_code,omitempty"`
	CreatedBy  string     `json:"created_by"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

func toXSecJobResponse(j xsec.Job) xsecJobResponse {
	resp := xsecJobResponse{
		ID:        j.ID,
		Mode:      j.Mode,
		Args:      j.Args,
		Status:    j.Status,
		Output:    j.Output,
		CreatedBy: j.CreatedBy,
		StartedAt: j.StartedAt,
	}
	if j.ExitCode.Valid {
		resp.ExitCode = &j.ExitCode.Int64
	}
	if j.FinishedAt.Valid {
		resp.FinishedAt = &j.FinishedAt.Time
	}
	return resp
}

// truncateOutput keeps the last n bytes of s (the most recent progress
// matters most for a running or just-finished job).
func truncateOutput(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "...[truncated]...\n" + s[len(s)-n:]
}

// userLevelFromContext mirrors KongAuthMiddleware.RequireLevel's read of the
// "user_level" context key (set by KongJWTMiddleware) without requiring a
// static per-route level — this handler gates by request body (mode), so the
// check has to happen inside the handler rather than as route middleware.
// Returns 0 (no access) when the key is missing or of an unexpected type.
func userLevelFromContext(c *gin.Context) int {
	v, exists := c.Get("user_level")
	if !exists {
		return 0
	}
	level, ok := v.(int)
	if !ok {
		return 0
	}
	return level
}

// CreateJob handles POST /api/v1/xsec/jobs.
// JWT required (route group). Modes rebalance-live and cancel-order
// additionally require Level 2+ (XSecParamsWriteLevel).
func (h *XSecJobsHandler) CreateJob(c *gin.Context) {
	if h.db == nil || h.runner == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("XSEC_JOBS_UNAVAILABLE", "xsec job runner not configured"))
		return
	}

	var req xsecJobCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_REQUEST", err.Error()))
		return
	}

	if xsecJobModesRequiringLevel2[req.Mode] && userLevelFromContext(c) < XSecParamsWriteLevel {
		c.JSON(http.StatusForbidden, models.NewErrorResponse("INSUFFICIENT_LEVEL",
			"mode "+req.Mode+" requires account level 2 or higher"))
		return
	}

	createdBy := userIDFromContext(c)
	jobID, err := h.runner.Start(XSecJobRequest{
		Mode:        req.Mode,
		K:           req.K,
		MaxPosUSD:   req.MaxPosUSD,
		Lookback:    req.Lookback,
		MaxTotalUSD: req.MaxTotalUSD,
		OrderID:     req.OrderID,
		Pair:        req.Pair,
		Utilization: req.Utilization,

		Rebal:          req.Rebal,
		Band:           req.Band,
		Cost:           req.Cost,
		Funding:        req.Funding,
		Capital:        req.Capital,
		VolWeights:     req.VolWeights,
		MinTurnoverUSD: req.MinTurnoverUSD,
		Windows:        req.Windows,

		SweepParam:  req.SweepParam,
		SweepValues: req.SweepValues,

		StopShort:      req.StopShort,
		StopLong:       req.StopLong,
		TPLiquidate:    req.TPLiquidate,
		TPLeg:          req.TPLeg,
		TPRebalance:    req.TPRebalance,
		TrailTP:        req.TrailTP,
		RegimeFloor:    req.RegimeFloor,
		RegimeWindow:   req.RegimeWindow,
		MakerFillProb:  req.MakerFillProb,
		TakerCost:      req.TakerCost,
		VolTarget:      req.VolTarget,
		MaxShortWeight: req.MaxShortWeight,
		FundingTilt:    req.FundingTilt,
		Pairs:          req.Pairs,
		StartFrac:      req.StartFrac,
		EndFrac:        req.EndFrac,
	}, createdBy)
	if err != nil {
		var startErr *XSecJobStartError
		if errors.As(err, &startErr) {
			code := "XSEC_JOB_REJECTED"
			if startErr.Code != "" {
				code = startErr.Code
			}
			body := models.NewErrorResponse(code, startErr.Message)
			if startErr.Status == http.StatusConflict {
				c.JSON(startErr.Status, gin.H{
					"success":        false,
					"error":          body.Error,
					"running_job_id": startErr.RunningJobID,
				})
				return
			}
			c.JSON(startErr.Status, body)
			return
		}
		h.log.WithError(err).WithField("user_id", createdBy).Error("xsec jobs: failed to start job")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("XSEC_JOB_START_FAILED", "failed to start job"))
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"job_id": jobID})
}

// GetJob handles GET /api/v1/xsec/jobs/:id — full row, JWT.
func (h *XSecJobsHandler) GetJob(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("XSEC_JOBS_UNAVAILABLE", "xsec job store not configured"))
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_ID", "job id must be an integer"))
		return
	}
	job, err := xsec.GetJob(h.db, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, models.NewErrorResponse("JOB_NOT_FOUND", "job not found"))
			return
		}
		h.log.WithError(err).WithField("job_id", id).Error("xsec jobs: get job failed")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("XSEC_JOB_READ_FAILED", "failed to load job"))
		return
	}
	c.JSON(http.StatusOK, models.NewSuccessResponse(toXSecJobResponse(job)))
}

// ListJobs handles GET /api/v1/xsec/jobs?limit=20 — recent rows newest first,
// output truncated to the last 4KB per row, JWT.
func (h *XSecJobsHandler) ListJobs(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("XSEC_JOBS_UNAVAILABLE", "xsec job store not configured"))
		return
	}
	limit := 20
	if raw := c.Query("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	jobs, err := xsec.ListJobs(h.db, limit)
	if err != nil {
		h.log.WithError(err).Error("xsec jobs: list jobs failed")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("XSEC_JOB_LIST_FAILED", "failed to list jobs"))
		return
	}
	out := make([]xsecJobResponse, 0, len(jobs))
	for _, j := range jobs {
		resp := toXSecJobResponse(j)
		resp.Output = truncateOutput(resp.Output, xsecJobsOutputTruncateBytes)
		out = append(out, resp)
	}
	c.JSON(http.StatusOK, models.NewSuccessResponse(out))
}

// xsecParamPutRequest is the PUT /api/v1/xsec/params body.
type xsecParamPutRequest struct {
	Key   string `json:"key" binding:"required"`
	Value string `json:"value" binding:"required"`
}

// PutParam handles PUT /api/v1/xsec/params — JWT + Level 2. Only keys in
// xsecParamWhitelist are writable.
func (h *XSecJobsHandler) PutParam(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("XSEC_PARAMS_UNAVAILABLE", "xsec params store not configured"))
		return
	}
	if userLevelFromContext(c) < XSecParamsWriteLevel {
		c.JSON(http.StatusForbidden, models.NewErrorResponse("INSUFFICIENT_LEVEL", "writing xsec_params requires account level 2 or higher"))
		return
	}

	var req xsecParamPutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_REQUEST", err.Error()))
		return
	}
	if !xsecParamWhitelist[req.Key] {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("KEY_NOT_ALLOWED", "key "+req.Key+" is not writable via this endpoint"))
		return
	}

	if err := xsec.SetParam(h.db, req.Key, req.Value); err != nil {
		h.log.WithError(err).WithField("key", req.Key).Error("xsec jobs: set param failed")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("XSEC_PARAM_WRITE_FAILED", "failed to write param"))
		return
	}
	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{"key": req.Key, "value": req.Value}))
}

// shadowScorerRe restricts which scorer a reset may target: the recorded
// scorer names (momentum, ltr, cand_lb<NN>k<NN>). Prevents arbitrary SQL
// values reaching the DELETE and refuses to wipe anything unexpected.
var shadowScorerRe = regexp.MustCompile(`^(momentum|ltr|cand_lb[0-9]{1,3}k[0-9]{1,3})$`)

// ResetShadow handles DELETE /api/v1/xsec/shadow/:scorer — JWT + Level 2.
// Clears one scorer's recorded shadow baskets so a forward paper A/B can be
// restarted from a fresh t0. Paper-only data (never traded), but gated at the
// same level as param writes since it discards history.
func (h *XSecJobsHandler) ResetShadow(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("XSEC_UNAVAILABLE", "xsec db not configured"))
		return
	}
	if userLevelFromContext(c) < XSecParamsWriteLevel {
		c.JSON(http.StatusForbidden, models.NewErrorResponse("INSUFFICIENT_LEVEL", "resetting a shadow A/B requires account level 2 or higher"))
		return
	}
	scorer := c.Param("scorer")
	if !shadowScorerRe.MatchString(scorer) {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_SCORER", "scorer must be momentum, ltr, or cand_lb<NN>k<NN>"))
		return
	}
	res, err := h.db.ExecContext(c.Request.Context(), `DELETE FROM xsec_shadow_baskets WHERE scorer = ?`, scorer)
	if err != nil {
		h.log.WithError(err).WithField("scorer", scorer).Error("xsec: reset shadow failed")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("XSEC_SHADOW_RESET_FAILED", "failed to reset shadow"))
		return
	}
	n, _ := res.RowsAffected()
	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{"scorer": scorer, "deleted": n}))
}

// GetParams handles GET /api/v1/xsec/params — all rows, JWT.
func (h *XSecJobsHandler) GetParams(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusOK, models.NewSuccessResponse([]XSecParam{}))
		return
	}
	rows, err := h.db.QueryContext(c.Request.Context(), `SELECT key, value, updated_at FROM xsec_params ORDER BY key`)
	if err != nil {
		// Table may not exist yet — same degrade-to-empty pattern as XSecHandler.GetStatus.
		h.log.WithError(err).Debug("xsec jobs: xsec_params not readable (table may not exist yet)")
		c.JSON(http.StatusOK, models.NewSuccessResponse([]XSecParam{}))
		return
	}
	defer rows.Close()

	out := []XSecParam{}
	for rows.Next() {
		var p XSecParam
		if rows.Scan(&p.Key, &p.Value, &p.UpdatedAt) == nil {
			out = append(out, p)
		}
	}
	c.JSON(http.StatusOK, models.NewSuccessResponse(out))
}
