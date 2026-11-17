// Package handlers — strategy_validation_handler.go exposes the STRATEGY
// validation gate: certification that a (pair, strategy) has a demonstrable
// out-of-sample edge before it may trade live. Distinct from ValidationHandler,
// which validates individual trade decisions against risk rules.
//
// Endpoints (mounted under /api/v1):
//
//	GET  /strategy-validation        — list all stored verdicts
//	POST /strategy-validation/run    — re-validate a pair on demand {"pair":"WLD-USDT"}
package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/rantipay/trading-mcp/internal/backtest"
	"github.com/rantipay/trading-mcp/internal/storage"
)

// StrategyValidationHandler exposes strategy-edge validation endpoints.
type StrategyValidationHandler struct {
	svc  *backtest.ValidationService
	repo *storage.ValidationRepository
}

// NewStrategyValidationHandler constructs the handler.
func NewStrategyValidationHandler(svc *backtest.ValidationService, repo *storage.ValidationRepository) *StrategyValidationHandler {
	return &StrategyValidationHandler{svc: svc, repo: repo}
}

// RegisterRoutesGin mounts the endpoints.
func (h *StrategyValidationHandler) RegisterRoutesGin(router gin.IRouter) {
	router.GET("/api/v1/strategy-validation", h.list)
	router.POST("/api/v1/strategy-validation/run", h.run)
}

func (h *StrategyValidationHandler) list(c *gin.Context) {
	recs, err := h.repo.All()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"verdicts": recs, "count": len(recs)})
}

func (h *StrategyValidationHandler) run(c *gin.Context) {
	var req struct {
		Pair string `json:"pair"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Pair == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "pair is required"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 120*time.Second)
	defer cancel()
	verdict, err := h.svc.ValidatePair(ctx, req.Pair)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, verdict)
}
