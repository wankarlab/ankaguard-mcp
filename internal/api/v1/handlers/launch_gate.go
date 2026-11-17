package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/rantipay/trading-mcp/internal/launch/launchapi"
)

// GetGate handles GET /launch/gate.
// NOTE: reads launch.gate_evaluations directly (read-only aggregate); it may
// later be switched to the internal/launch/gate package.
func (h *LaunchHandler) GetGate(c *gin.Context) {
	if !h.available(c) {
		return
	}
	ov, err := launchapi.Gate(c.Request.Context(), h.pool, h.requirements(), queryInt(c, "limit"))
	if err != nil {
		h.fail(c, "gate", err)
		return
	}
	c.JSON(http.StatusOK, models.NewSuccessResponse(ov))
}

// GetHumanLift handles GET /launch/reports/human-lift.
func (h *LaunchHandler) GetHumanLift(c *gin.Context) {
	if !h.available(c) {
		return
	}
	rep, err := launchapi.HumanLift(c.Request.Context(), h.pool, h.requirements().MinTrades)
	if err != nil {
		h.fail(c, "human_lift", err)
		return
	}
	c.JSON(http.StatusOK, models.NewSuccessResponse(rep))
}
