// Package handlers — EmergencyStopHandler exposes the admin-level
// POST /api/v1/admin/bots/emergency-stop-all endpoint (H0.3.2).
package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/rantipay/trading-mcp/internal/services"
	"github.com/sirupsen/logrus"
)

// botManagerEmergencyStop is the minimal interface of services.BotManager that
// EmergencyStopHandler requires. Using an interface keeps the handler testable
// without the full BotManager construction.
type botManagerEmergencyStop interface {
	ListActive() []string
	EmergencyStop(ctx context.Context, userID string) error
}

// EmergencyStopHandler handles admin emergency-stop-all operations.
type EmergencyStopHandler struct {
	botManager botManagerEmergencyStop
	logger     *logrus.Logger
}

// NewEmergencyStopHandler constructs an EmergencyStopHandler.
// botManager must be non-nil.
func NewEmergencyStopHandler(botManager *services.BotManager, logger *logrus.Logger) *EmergencyStopHandler {
	if logger == nil {
		logger = logrus.New()
	}
	return &EmergencyStopHandler{
		botManager: botManager,
		logger:     logger,
	}
}

// newEmergencyStopHandlerWithInterface is used in tests to inject a mock.
func newEmergencyStopHandlerWithInterface(bm botManagerEmergencyStop, logger *logrus.Logger) *EmergencyStopHandler {
	if logger == nil {
		logger = logrus.New()
	}
	return &EmergencyStopHandler{botManager: bm, logger: logger}
}

// BotManagerEmergencyStopIface is the exported alias for the internal interface
// so that tests in external packages (e.g. tests/e2e) can implement it without
// importing internal/services (which would drag in heavy dependencies).
type BotManagerEmergencyStopIface interface {
	ListActive() []string
	EmergencyStop(ctx context.Context, userID string) error
}

// NewEmergencyStopHandlerForTest constructs an EmergencyStopHandler from any
// type that satisfies BotManagerEmergencyStopIface. Intended exclusively for
// unit/e2e tests — production code should use NewEmergencyStopHandler instead.
func NewEmergencyStopHandlerForTest(bm BotManagerEmergencyStopIface) *EmergencyStopHandler {
	return &EmergencyStopHandler{
		botManager: bm,
		logger:     logrus.New(),
	}
}

// EmergencyStopAllRequest is the request body for POST /api/v1/admin/bots/emergency-stop-all.
type EmergencyStopAllRequest struct {
	DryRun bool   `json:"dry_run"`
	Reason string `json:"reason"`
}

// EmergencyStopAllResponse contains the result of the operation.
type EmergencyStopAllResponse struct {
	TriggeredAt      string   `json:"triggered_at"`
	DryRun           bool     `json:"dry_run"`
	ActiveBotsBefore int      `json:"active_bots_before"`
	PositionsClosed  int      `json:"positions_closed"`
	Errors           []string `json:"errors,omitempty"`
}

// EmergencyStopAll handles POST /api/v1/admin/bots/emergency-stop-all.
//
// Requires user_level >= 4 (enforced by the admin middleware group in the router).
//
// Dry-run mode: returns counts without performing any stops.
// Live mode: iterates all active bots sequentially (no goroutines — avoids
// metric races) and calls EmergencyStop on each. Errors are collected and
// returned in the response; the operation continues even if individual bots fail.
func (h *EmergencyStopHandler) EmergencyStopAll(c *gin.Context) {
	var req EmergencyStopAllRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// Empty body is valid — use zero values (dry_run=false, reason="").
		req = EmergencyStopAllRequest{}
	}

	activeIDs := h.botManager.ListActive()
	activeBotsBefore := len(activeIDs)
	triggeredAt := time.Now().UTC().Format(time.RFC3339)

	h.logger.WithFields(logrus.Fields{
		"active_bots":  activeBotsBefore,
		"dry_run":      req.DryRun,
		"reason":       req.Reason,
		"triggered_by": userIDFromContext(c),
	}).Warn("EmergencyStopAll requested")

	if req.DryRun {
		c.JSON(http.StatusOK, models.NewSuccessResponse(EmergencyStopAllResponse{
			TriggeredAt:      triggeredAt,
			DryRun:           true,
			ActiveBotsBefore: activeBotsBefore,
			PositionsClosed:  0,
		}))
		return
	}

	// Live stop: iterate sequentially to avoid metric counter races.
	var errs []string
	positionsClosed := 0

	stopCtx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()

	for _, userID := range activeIDs {
		if err := h.botManager.EmergencyStop(stopCtx, userID); err != nil {
			h.logger.WithError(err).WithField("user_id", userID).Error("EmergencyStopAll: stop failed for user")
			errs = append(errs, err.Error())
		} else {
			// Each stopped bot counts as 1 unit closed (individual position counts
			// are tracked via the Prometheus counter inside BotManager.EmergencyStop).
			positionsClosed++
		}
	}

	h.logger.WithFields(logrus.Fields{
		"active_bots_before": activeBotsBefore,
		"positions_closed":   positionsClosed,
		"error_count":        len(errs),
		"reason":             req.Reason,
	}).Warn("EmergencyStopAll completed")

	c.JSON(http.StatusOK, models.NewSuccessResponse(EmergencyStopAllResponse{
		TriggeredAt:      triggeredAt,
		DryRun:           false,
		ActiveBotsBefore: activeBotsBefore,
		PositionsClosed:  positionsClosed,
		Errors:           errs,
	}))
}
