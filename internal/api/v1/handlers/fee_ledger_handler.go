// Package handlers — G4: performance fee ledger REST endpoints.
//
// GET  /api/v1/fees/current         — current open period for the caller
// GET  /api/v1/fees/history         — closed periods (?limit=12)
// POST /api/v1/fees/accrue          — accrue fee for a single trade (internal, level 3+)
package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/rantipay/trading-mcp/internal/storage"
	"github.com/sirupsen/logrus"
)

// feeLedgerRepo is the minimal interface FeeLedgerHandler needs.
// Satisfied by *storage.FeeLedgerRepository without any wrapper.
type feeLedgerRepo interface {
	GetCurrentPeriod(ctx context.Context, userID string) (*storage.FeeLedgerRow, error)
	AccrueFee(ctx context.Context, userID string, tradePnL float64) (*storage.FeeLedgerRow, error)
	GetHistory(ctx context.Context, userID string, limit int) ([]*storage.FeeLedgerRow, error)
}

// FeeLedgerHandler exposes performance fee ledger data and accrual over REST.
type FeeLedgerHandler struct {
	repo feeLedgerRepo
	log  *logrus.Logger
}

// NewFeeLedgerHandler constructs a FeeLedgerHandler.
// repo may be nil (returns 503 on every request).
func NewFeeLedgerHandler(repo feeLedgerRepo, log *logrus.Logger) *FeeLedgerHandler {
	if log == nil {
		log = logrus.New()
	}
	return &FeeLedgerHandler{repo: repo, log: log}
}

// RegisterRoutes registers the fee ledger endpoints on the given router group.
// Call with the /api/v1 Gin group.
func (h *FeeLedgerHandler) RegisterRoutes(v1 *gin.RouterGroup) {
	fees := v1.Group("/fees")
	{
		fees.GET("/current", h.GetCurrent)
		fees.GET("/history", h.GetHistory)
		fees.POST("/accrue", h.AccrueFee)
	}
}

// RegisterRoutesGin registers the fee ledger endpoints directly on a *gin.Engine,
// creating the /api/v1/fees group internally.  Matches the RegisterRoutesGin
// convention used by other handlers in this package (e.g. StrategyValidationHandler).
func (h *FeeLedgerHandler) RegisterRoutesGin(router *gin.Engine) {
	v1 := router.Group("/api/v1")
	h.RegisterRoutes(v1)
}

// GetCurrent handles GET /api/v1/fees/current.
// Returns the current open fee period for the authenticated user.
func (h *FeeLedgerHandler) GetCurrent(c *gin.Context) {
	if h.repo == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("FEE_LEDGER_UNAVAILABLE", "fee ledger not configured"))
		return
	}

	userID := userIDFromContext(c)

	period, err := h.repo.GetCurrentPeriod(c.Request.Context(), userID)
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusNotFound, models.NewErrorResponse("NOT_FOUND", "no open fee period for this user"))
		return
	}
	if err != nil {
		h.log.WithError(err).WithField("user_id", userID).Error("FeeLedgerHandler.GetCurrent: repo error")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", "failed to fetch fee period"))
		return
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(period))
}

// GetHistory handles GET /api/v1/fees/history?limit=12.
// Returns closed fee periods for the authenticated user.
func (h *FeeLedgerHandler) GetHistory(c *gin.Context) {
	if h.repo == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("FEE_LEDGER_UNAVAILABLE", "fee ledger not configured"))
		return
	}

	userID := userIDFromContext(c)

	limit := 12
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	history, err := h.repo.GetHistory(c.Request.Context(), userID, limit)
	if err != nil {
		h.log.WithError(err).WithField("user_id", userID).Error("FeeLedgerHandler.GetHistory: repo error")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", "failed to fetch fee history"))
		return
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"periods": history,
		"total":   len(history),
	}))
}

// AccrueFee handles POST /api/v1/fees/accrue.
// Body: {"trade_pnl": 1.23}
// Intended for internal service-to-service use (operator/level 3+).
func (h *FeeLedgerHandler) AccrueFee(c *gin.Context) {
	if h.repo == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("FEE_LEDGER_UNAVAILABLE", "fee ledger not configured"))
		return
	}

	userID := userIDFromContext(c)

	var req struct {
		TradePnL float64 `json:"trade_pnl"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponseWithDetails("VALIDATION_FAILED", "invalid request body", err.Error()))
		return
	}

	updated, err := h.repo.AccrueFee(c.Request.Context(), userID, req.TradePnL)
	if err != nil {
		h.log.WithError(err).WithFields(logrus.Fields{
			"user_id":   userID,
			"trade_pnl": req.TradePnL,
		}).Error("FeeLedgerHandler.AccrueFee: repo error")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", "failed to accrue fee"))
		return
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(updated))
}
