package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/rantipay/trading-mcp/internal/launch/core"
	"github.com/rantipay/trading-mcp/internal/launch/launchapi"
	"github.com/rantipay/trading-mcp/internal/trading/plans"
)

// ListStrategies handles GET /launch/strategies.
func (h *LaunchHandler) ListStrategies(c *gin.Context) {
	if !h.available(c) {
		return
	}
	items, err := launchapi.ListStrategies(c.Request.Context(), h.pool)
	if err != nil {
		h.fail(c, "strategies", err)
		return
	}
	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{"items": items}))
}

type launchVersionRequest struct {
	Params core.StrategyParams `json:"params"`
	Reason string              `json:"reason"`
}

// CreateStrategyVersion handles POST /launch/strategies/:id/versions
// (launch_analyze). Versions are immutable; each call appends version N+1.
func (h *LaunchHandler) CreateStrategyVersion(c *gin.Context) {
	if !h.requireFeature(c, plans.FeatureLaunchAnalyze) {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req launchVersionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_REQUEST", err.Error()))
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("REASON_REQUIRED", "reason is required"))
		return
	}
	if err := req.Params.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponseWithDetails("INVALID_PARAMS", "invalid strategy params", err.Error()))
		return
	}
	if !h.available(c) {
		return
	}
	v, err := launchapi.CreateStrategyVersion(c.Request.Context(), h.pool, id, req.Params, userIDFromContext(c), strings.TrimSpace(req.Reason))
	var invalid launchapi.ErrInvalidParams
	switch {
	case err == nil:
		c.JSON(http.StatusCreated, models.NewSuccessResponse(v))
	case errors.As(err, &invalid):
		c.JSON(http.StatusBadRequest, models.NewErrorResponseWithDetails("INVALID_PARAMS", "invalid strategy params", err.Error()))
	default:
		h.failOrNotFound(c, "strategy_version", err)
	}
}
