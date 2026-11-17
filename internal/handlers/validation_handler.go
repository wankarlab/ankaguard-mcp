package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/ai/validation"
	"github.com/sirupsen/logrus"
)

// ValidationHandler handles validation API endpoints
type ValidationHandler struct {
	validationService *validation.Service
	log               *logrus.Logger
}

// NewValidationHandler creates a new validation handler
func NewValidationHandler(validationService *validation.Service, log *logrus.Logger) *ValidationHandler {
	return &ValidationHandler{
		validationService: validationService,
		log:               log,
	}
}

// RegisterRoutesGin registers all validation routes on the Gin router
func (h *ValidationHandler) RegisterRoutesGin(router gin.IRouter) {
	router.GET("/api/v1/validation/rules", h.GetRules)
	router.GET("/api/v1/validation/circuit-breakers", h.GetCircuitBreakers)
	router.POST("/api/v1/validation/test", h.TestTrade)
}

// GetRules returns all loaded validation rules
// GET /api/v1/validation/rules
func (h *ValidationHandler) GetRules(c *gin.Context) {
	rules := (*h.validationService).GetRules()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"total": len(rules), "rules": rules},
	})
}

// GetCircuitBreakers returns current circuit breaker status
// GET /api/v1/validation/circuit-breakers
func (h *ValidationHandler) GetCircuitBreakers(c *gin.Context) {
	portfolio := &validation.PortfolioState{
		TotalCapital: 10000.0, AvailableCapital: 9000.0, PeakCapital: 10000.0,
	}

	status, err := (*h.validationService).CheckCircuitBreakers(c.Request.Context(), portfolio)
	if err != nil {
		h.log.WithError(err).Error("Failed to check circuit breakers")
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to check circuit breakers"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": status})
}

// TestTrade validates a proposed trade
// POST /api/v1/validation/test
func (h *ValidationHandler) TestTrade(c *gin.Context) {
	var req struct {
		Trade     *validation.TradingDecision `json:"trade"`
		Portfolio *validation.PortfolioState  `json:"portfolio"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "invalid request body"})
		return
	}

	if req.Trade == nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "trade is required"})
		return
	}

	if req.Portfolio == nil {
		req.Portfolio = &validation.PortfolioState{
			TotalCapital: 10000.0, AvailableCapital: 9000.0, PeakCapital: 10000.0,
		}
	}

	result, err := (*h.validationService).ValidateTrade(c.Request.Context(), req.Trade, req.Portfolio)
	if err != nil {
		h.log.WithError(err).Error("Failed to validate trade")
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to validate trade"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}
