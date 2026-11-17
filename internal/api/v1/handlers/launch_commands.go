package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/rantipay/trading-mcp/internal/launch"
	"github.com/rantipay/trading-mcp/internal/trading/plans"
)

type launchReasonRequest struct {
	Reason string `json:"reason"`
}

// KillSwitch handles POST /launch/kill-switch (launch_operate).
func (h *LaunchHandler) KillSwitch(c *gin.Context) { h.publish(c, launch.CommandKillSwitch) }

// Resume handles POST /launch/resume (launch_operate).
func (h *LaunchHandler) Resume(c *gin.Context) { h.publish(c, launch.CommandResume) }

// publish enqueues a kill_switch/resume command. It is queued even when
// launch-bot is down; the bot claims it when it returns.
func (h *LaunchHandler) publish(c *gin.Context, kind launch.CommandKind) {
	if !h.requireFeature(c, plans.FeatureLaunchOperate) {
		return
	}
	var req launchReasonRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_REQUEST", err.Error()))
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("REASON_REQUIRED", "reason is required"))
		return
	}
	if !h.available(c) {
		return
	}
	actor := userIDFromContext(c)
	id, err := launch.NewPostgresCommandBus(h.pool, 0, 0).Publish(c.Request.Context(), launch.Command{
		Kind: kind, Actor: actor, Reason: reason, UserID: actor,
	})
	if err != nil {
		h.fail(c, string(kind), err)
		return
	}
	h.log.WithFields(map[string]any{"command_id": id, "kind": string(kind), "actor": actor}).Warn("launch api: operator command queued")
	c.JSON(http.StatusAccepted, models.NewSuccessResponse(gin.H{"command_id": id}))
}

// EnableRealMode handles POST /launch/real-mode/enable. Real trading does not
// exist in phase 1, so this always refuses and changes nothing.
func (h *LaunchHandler) EnableRealMode(c *gin.Context) {
	c.JSON(http.StatusConflict, models.NewErrorResponse("NOT_AVAILABLE_IN_PHASE",
		"real mode is not available in this phase; AnkaLaunch runs in shadow only"))
}
