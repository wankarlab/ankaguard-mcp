package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/ai/knowledge"
	"github.com/rantipay/trading-mcp/internal/ai/prompts"
	"github.com/rantipay/trading-mcp/internal/ai/validation"
	"github.com/sirupsen/logrus"
)

// AnalysisHandler handles analysis API endpoints
type AnalysisHandler struct {
	knowledgeService  *knowledge.Service
	promptService     *prompts.Service
	validationService *validation.Service
	log               *logrus.Logger
}

// NewAnalysisHandler creates a new analysis handler
func NewAnalysisHandler(
	knowledgeService *knowledge.Service,
	promptService *prompts.Service,
	validationService *validation.Service,
	log *logrus.Logger,
) *AnalysisHandler {
	return &AnalysisHandler{
		knowledgeService:  knowledgeService,
		promptService:     promptService,
		validationService: validationService,
		log:               log,
	}
}

// RegisterRoutesGin registers all analysis routes on the Gin router
func (h *AnalysisHandler) RegisterRoutesGin(router gin.IRouter) {
	router.POST("/api/v1/analysis/market", h.AnalyzeMarket)
	router.POST("/api/v1/analysis/setup", h.IdentifySetup)
	router.POST("/api/v1/analysis/validate", h.ValidateTrade)
}

// AnalyzeMarket performs market analysis with knowledge context
// POST /api/v1/analysis/market
func (h *AnalysisHandler) AnalyzeMarket(c *gin.Context) {
	var req struct {
		MarketData *prompts.MarketData `json:"market_data"`
		Sections   []string            `json:"sections"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "invalid request body"})
		return
	}

	if req.MarketData == nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "market_data is required"})
		return
	}

	contextReq := &knowledge.ContextRequest{
		AnalysisType: knowledge.AnalysisTypeMarket,
		Pair:         req.MarketData.Pair,
		Additional: map[string]interface{}{
			"sections": req.Sections,
		},
	}

	ctx, err := (*h.knowledgeService).GetContext(c.Request.Context(), contextReq)
	if err != nil {
		h.log.WithError(err).Error("Failed to get knowledge context")
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to get context"})
		return
	}

	prompt, err := (*h.promptService).GeneratePrompt(c.Request.Context(), "market_analysis", req.MarketData, ctx.Context)
	if err != nil {
		h.log.WithError(err).Error("Failed to generate prompt")
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to generate prompt"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"prompt": prompt, "context": ctx},
	})
}

// IdentifySetup identifies trading setups
// POST /api/v1/analysis/setup
func (h *AnalysisHandler) IdentifySetup(c *gin.Context) {
	var req struct {
		MarketData *prompts.MarketData `json:"market_data"`
		Sections   []string            `json:"sections"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "invalid request body"})
		return
	}

	if req.MarketData == nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "market_data is required"})
		return
	}

	contextReq := &knowledge.ContextRequest{
		AnalysisType: knowledge.AnalysisTypeSetup,
		Pair:         req.MarketData.Pair,
		Additional: map[string]interface{}{
			"sections": req.Sections,
		},
	}

	ctx, err := (*h.knowledgeService).GetContext(c.Request.Context(), contextReq)
	if err != nil {
		h.log.WithError(err).Error("Failed to get knowledge context")
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to get context"})
		return
	}

	prompt, err := (*h.promptService).GeneratePrompt(c.Request.Context(), "setup_identification", req.MarketData, ctx.Context)
	if err != nil {
		h.log.WithError(err).Error("Failed to generate prompt")
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to generate prompt"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"prompt": prompt, "context": ctx},
	})
}

// ValidateTrade validates a proposed trade with full workflow
// POST /api/v1/analysis/validate
func (h *AnalysisHandler) ValidateTrade(c *gin.Context) {
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

	cbStatus, err := (*h.validationService).CheckCircuitBreakers(c.Request.Context(), req.Portfolio)
	if err != nil {
		h.log.WithError(err).Error("Failed to check circuit breakers")
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to check circuit breakers"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"validation":      result,
			"circuit_breaker": cbStatus,
			"approved":        result.Passed && !cbStatus.TradingHalted,
		},
	})
}
