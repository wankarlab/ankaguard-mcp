// Package handlers — ModeHandler exposes trading mode REST endpoints (Wave 2-A).
package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/rantipay/trading-mcp/internal/storage"
	"github.com/sirupsen/logrus"
)

// ModeHandler exposes user trading mode endpoints.
type ModeHandler struct {
	repo   *storage.TradingModeRepository
	logger *logrus.Logger
}

// NewModeHandler creates a new ModeHandler.
func NewModeHandler(repo *storage.TradingModeRepository, logger *logrus.Logger) *ModeHandler {
	return &ModeHandler{repo: repo, logger: logger}
}

// GetMode handles GET /api/v1/mode
// Returns the global trading mode plus all per-pair overrides.
func (h *ModeHandler) GetMode(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	global, err := h.repo.GetGlobal(ctx)
	if err != nil {
		h.logger.WithError(err).Error("Failed to get global trading mode")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponseWithDetails(
			"MODE_ERROR", "Failed to retrieve global trading mode", err.Error(),
		))
		return
	}

	all, err := h.repo.ListAll(ctx)
	if err != nil {
		h.logger.WithError(err).Error("Failed to list trading modes")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponseWithDetails(
			"MODE_ERROR", "Failed to list trading modes", err.Error(),
		))
		return
	}

	// Separate global from per-pair overrides for a clearer response.
	var pairOverrides []gin.H
	for _, m := range all {
		if m.Scope == "pair" {
			pairOverrides = append(pairOverrides, gin.H{
				"pair":           m.Pair,
				"market_type":    m.MarketType,
				"mode_selection": m.ModeSelection,
				"leverage":       m.Leverage,
				"updated_at":     m.UpdatedAt,
			})
		}
	}
	if pairOverrides == nil {
		pairOverrides = []gin.H{}
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"global": gin.H{
			"market_type":    global.MarketType,
			"mode_selection": global.ModeSelection,
			"leverage":       global.Leverage,
			"updated_at":     global.UpdatedAt,
		},
		"pair_overrides": pairOverrides,
	}))
}

// putGlobalModeRequest is the body for PUT /api/v1/mode.
type putGlobalModeRequest struct {
	MarketType    string  `json:"market_type"`
	ModeSelection string  `json:"mode_selection"`
	Leverage      float64 `json:"leverage"`
}

// PutGlobalMode handles PUT /api/v1/mode
// Sets the global market_type, mode_selection, and leverage.
func (h *ModeHandler) PutGlobalMode(c *gin.Context) {
	var req putGlobalModeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_BODY", err.Error()))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	if err := h.repo.SetGlobal(ctx, req.MarketType, req.ModeSelection, req.Leverage); err != nil {
		h.logger.WithError(err).Error("Failed to set global trading mode")
		c.JSON(http.StatusBadRequest, models.NewErrorResponseWithDetails(
			"INVALID_MODE", "Failed to set global trading mode", err.Error(),
		))
		return
	}

	h.logger.WithFields(logrus.Fields{
		"market_type":    req.MarketType,
		"mode_selection": req.ModeSelection,
		"leverage":       req.Leverage,
	}).Info("Global trading mode updated via REST")

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"scope":          "global",
		"market_type":    req.MarketType,
		"mode_selection": req.ModeSelection,
		"leverage":       req.Leverage,
	}))
}

// putPairModeRequest is the body for PUT /api/v1/mode/:pair.
type putPairModeRequest struct {
	MarketType string  `json:"market_type"`
	Leverage   float64 `json:"leverage"`
}

// PutPairMode handles PUT /api/v1/mode/:pair
// Sets a per-pair market_type and leverage override.
func (h *ModeHandler) PutPairMode(c *gin.Context) {
	pair := c.Param("pair")
	if pair == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("MISSING_PAIR", "pair path parameter is required"))
		return
	}

	var req putPairModeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_BODY", err.Error()))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	if err := h.repo.SetForPair(ctx, pair, req.MarketType, req.Leverage); err != nil {
		h.logger.WithError(err).Error("Failed to set per-pair trading mode")
		c.JSON(http.StatusBadRequest, models.NewErrorResponseWithDetails(
			"INVALID_MODE", "Failed to set per-pair trading mode", err.Error(),
		))
		return
	}

	h.logger.WithFields(logrus.Fields{
		"pair":        pair,
		"market_type": req.MarketType,
		"leverage":    req.Leverage,
	}).Info("Per-pair trading mode updated via REST")

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"scope":          "pair",
		"pair":           pair,
		"market_type":    req.MarketType,
		"mode_selection": "manual",
		"leverage":       req.Leverage,
	}))
}

// DeletePairMode handles DELETE /api/v1/mode/:pair
// Removes a per-pair override, reverting to the global setting.
func (h *ModeHandler) DeletePairMode(c *gin.Context) {
	pair := c.Param("pair")
	if pair == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("MISSING_PAIR", "pair path parameter is required"))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	if err := h.repo.DeleteForPair(ctx, pair); err != nil {
		h.logger.WithError(err).Error("Failed to delete per-pair trading mode")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponseWithDetails(
			"DELETE_ERROR", "Failed to delete per-pair trading mode override", err.Error(),
		))
		return
	}

	h.logger.WithField("pair", pair).Info("Per-pair trading mode override deleted via REST")

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"pair": pair,
		"note": "per-pair override removed; global settings will apply",
	}))
}
