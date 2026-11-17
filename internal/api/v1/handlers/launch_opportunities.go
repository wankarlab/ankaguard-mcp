package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/rantipay/trading-mcp/internal/launch/launchapi"
	"github.com/rantipay/trading-mcp/internal/trading/plans"
)

// ListOpportunities handles GET /launch/opportunities.
func (h *LaunchHandler) ListOpportunities(c *gin.Context) {
	if !h.available(c) {
		return
	}
	page, err := launchapi.ListOpportunities(c.Request.Context(), h.pool, launchapi.OpportunityFilter{
		Band: c.Query("band"), Status: c.Query("status"),
		Limit: queryInt(c, "limit"), BeforeID: int64(queryInt(c, "before_id")),
	})
	if err != nil {
		h.fail(c, "opportunities", err)
		return
	}
	c.JSON(http.StatusOK, models.NewSuccessResponse(page))
}

// GetOpportunity handles GET /launch/opportunities/:id.
func (h *LaunchHandler) GetOpportunity(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	opp, err := launchapi.GetOpportunity(c.Request.Context(), h.pool, id)
	if err != nil {
		h.failOrNotFound(c, "opportunity", err)
		return
	}
	c.JSON(http.StatusOK, models.NewSuccessResponse(opp))
}

// ListApprovals handles GET /launch/approvals.
func (h *LaunchHandler) ListApprovals(c *gin.Context) {
	if !h.available(c) {
		return
	}
	items, err := launchapi.ListApprovals(c.Request.Context(), h.pool)
	if err != nil {
		h.fail(c, "approvals", err)
		return
	}
	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"items":      items,
		"can_decide": plans.HasFeature(userLevelFromContext(c), plans.FeatureLaunchOperate),
	}))
}

type launchDecisionRequest struct {
	Action   string `json:"action"`
	Category string `json:"category"`
	Comment  string `json:"comment"`
}

// Decide handles POST /launch/opportunities/:id/decision (launch_operate).
func (h *LaunchHandler) Decide(c *gin.Context) {
	if !h.requireFeature(c, plans.FeatureLaunchOperate) {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req launchDecisionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_REQUEST", err.Error()))
		return
	}
	if req.Action != "approve" && req.Action != "reject" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_ACTION", "action must be approve or reject"))
		return
	}
	if !launchapi.ValidCategory(req.Category) {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_CATEGORY",
			"category must be one of narrative, team, community, chart, other"))
		return
	}
	if !h.available(c) {
		return
	}
	dec, err := launchapi.Decide(c.Request.Context(), h.pool, id, req.Action, req.Category, req.Comment, userIDFromContext(c))
	switch {
	case err == nil:
		c.JSON(http.StatusOK, models.NewSuccessResponse(dec))
	case errors.Is(err, launchapi.ErrNotFound):
		c.JSON(http.StatusNotFound, models.NewErrorResponse("NOT_FOUND", "opportunity not found"))
	case errors.Is(err, launchapi.ErrOpportunityExpired):
		c.JSON(http.StatusConflict, models.NewErrorResponse("OPPORTUNITY_EXPIRED", "the approval window of this opportunity has expired"))
	case errors.Is(err, launchapi.ErrAlreadyDecided):
		c.JSON(http.StatusConflict, models.NewErrorResponse("ALREADY_DECIDED", "this opportunity already has a decision"))
	case errors.Is(err, launchapi.ErrNotPending):
		c.JSON(http.StatusConflict, models.NewErrorResponse("NOT_PENDING", "this opportunity is not in the approval queue"))
	default:
		h.fail(c, "decision", err)
	}
}
