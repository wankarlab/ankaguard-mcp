package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/rantipay/trading-mcp/internal/launch/launchapi"
)

// ListPositions handles GET /launch/positions.
func (h *LaunchHandler) ListPositions(c *gin.Context) {
	if !h.available(c) {
		return
	}
	page, err := launchapi.ListPositions(c.Request.Context(), h.pool, launchapi.PositionFilter{
		Channel: c.Query("channel"), State: c.Query("state"),
		Limit: queryInt(c, "limit"), BeforeID: int64(queryInt(c, "before_id")),
	})
	if err != nil {
		h.fail(c, "positions", err)
		return
	}
	c.JSON(http.StatusOK, models.NewSuccessResponse(page))
}

// GetPosition handles GET /launch/positions/:id.
func (h *LaunchHandler) GetPosition(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	pos, err := launchapi.GetPosition(c.Request.Context(), h.pool, id)
	if err != nil {
		h.failOrNotFound(c, "position", err)
		return
	}
	c.JSON(http.StatusOK, models.NewSuccessResponse(pos))
}
