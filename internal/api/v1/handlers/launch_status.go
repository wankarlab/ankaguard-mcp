// Package handlers — AnkaLaunch phase-1 console endpoints (/api/v1/launch).
// Contract: docs/bot/launch-api-phase1.md. Level/feature checks happen inside
// the handlers (like the xsec jobs) because RequireLevel can read user_level=0
// on some auth paths. Nothing here trades: the only writes are human
// decisions, strategy versions and operator commands for launch-bot.
package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/rantipay/trading-mcp/internal/launch/launchapi"
	"github.com/rantipay/trading-mcp/internal/trading/plans"
)

// LaunchHandler serves the /api/v1/launch endpoints over the config Postgres.
type LaunchHandler struct {
	pool         *pgxpool.Pool
	requirements launchapi.RequirementsFunc
	log          *logrus.Logger
}

// NewLaunchHandler creates the handler. pool may be nil (every endpoint then
// answers 503 LAUNCH_UNAVAILABLE); requirements may be nil (manifest defaults).
func NewLaunchHandler(pool *pgxpool.Pool, requirements launchapi.RequirementsFunc, log *logrus.Logger) *LaunchHandler {
	if log == nil {
		log = logrus.New()
	}
	if requirements == nil {
		requirements = launchapi.DefaultRequirements
	}
	return &LaunchHandler{pool: pool, requirements: requirements, log: log}
}

// requireFeature answers 403 FEATURE_ACCESS_DENIED and returns false when the
// caller's level lacks the feature.
func (h *LaunchHandler) requireFeature(c *gin.Context, feature string) bool {
	if plans.HasFeature(userLevelFromContext(c), feature) {
		return true
	}
	c.JSON(http.StatusForbidden, models.NewErrorResponse("FEATURE_ACCESS_DENIED",
		"feature "+feature+" is not available for your account level"))
	return false
}

// available answers 503 LAUNCH_UNAVAILABLE and returns false without a pool.
func (h *LaunchHandler) available(c *gin.Context) bool {
	if h.pool != nil {
		return true
	}
	c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("LAUNCH_UNAVAILABLE", "launch database is not configured"))
	return false
}

func (h *LaunchHandler) fail(c *gin.Context, op string, err error) {
	h.log.WithError(err).WithField("op", op).Error("launch api: query failed")
	c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", "launch query failed"))
}

// failOrNotFound maps launchapi.ErrNotFound to 404.
func (h *LaunchHandler) failOrNotFound(c *gin.Context, op string, err error) {
	if errors.Is(err, launchapi.ErrNotFound) {
		c.JSON(http.StatusNotFound, models.NewErrorResponse("NOT_FOUND", "not found"))
		return
	}
	h.fail(c, op, err)
}

func queryInt(c *gin.Context, key string) int {
	n, _ := strconv.Atoi(c.Query(key))
	return n
}

func pathID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_REQUEST", "invalid id"))
		return 0, false
	}
	return id, true
}

// GetStatus handles GET /launch/status.
func (h *LaunchHandler) GetStatus(c *gin.Context) {
	if !h.available(c) {
		return
	}
	st, err := launchapi.GetStatus(c.Request.Context(), h.pool, h.requirements(), time.Time{})
	if err != nil {
		h.fail(c, "status", err)
		return
	}
	c.JSON(http.StatusOK, models.NewSuccessResponse(st))
}
