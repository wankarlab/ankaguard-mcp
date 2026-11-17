package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/ai/knowledge"
	"github.com/sirupsen/logrus"
)

// KnowledgeHandler handles knowledge base API endpoints
type KnowledgeHandler struct {
	knowledgeService *knowledge.Service
	log              *logrus.Logger
}

// NewKnowledgeHandler creates a new knowledge handler
func NewKnowledgeHandler(knowledgeService *knowledge.Service, log *logrus.Logger) *KnowledgeHandler {
	return &KnowledgeHandler{
		knowledgeService: knowledgeService,
		log:              log,
	}
}

// RegisterRoutesGin registers all knowledge base routes on the Gin router
func (h *KnowledgeHandler) RegisterRoutesGin(router gin.IRouter) {
	router.GET("/api/v1/knowledge/status", h.GetStatus)
	router.GET("/api/v1/knowledge/context/:type", h.GetContext)
	router.POST("/api/v1/knowledge/reload", h.Reload)
}

// GetStatus returns the current status of the knowledge base
// GET /api/v1/knowledge/status
func (h *KnowledgeHandler) GetStatus(c *gin.Context) {
	status := (*h.knowledgeService).GetStatus()
	c.JSON(http.StatusOK, gin.H{"success": true, "data": status})
}

// GetContext retrieves knowledge context for a specific analysis type
// GET /api/v1/knowledge/context/:type
func (h *KnowledgeHandler) GetContext(c *gin.Context) {
	analysisType := c.Param("type")
	if analysisType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "analysis type is required"})
		return
	}

	req := &knowledge.ContextRequest{
		AnalysisType: knowledge.AnalysisType(analysisType),
		Pair:         c.Query("pair"),
		Timeframe:    c.Query("timeframe"),
		TradingStyle: c.Query("trading_style"),
		Additional: map[string]interface{}{
			"sections": c.QueryArray("section"),
		},
	}

	ctx, err := (*h.knowledgeService).GetContext(c.Request.Context(), req)
	if err != nil {
		h.log.WithError(err).Error("Failed to get knowledge context")
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to get context"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": ctx})
}

// Reload reloads the knowledge base from disk
// POST /api/v1/knowledge/reload
func (h *KnowledgeHandler) Reload(c *gin.Context) {
	if err := (*h.knowledgeService).Reload(c.Request.Context()); err != nil {
		h.log.WithError(err).Error("Failed to reload knowledge base")
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to reload knowledge base"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Knowledge base reloaded successfully"})
}
