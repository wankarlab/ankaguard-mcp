package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/ai"
	"github.com/rantipay/trading-mcp/internal/storage"
	"github.com/sirupsen/logrus"
)

// StrategyHandler handles strategy management API endpoints
type StrategyHandler struct {
	storageManager *storage.Manager
	log            *logrus.Logger
}

// NewStrategyHandler creates a new strategy handler
func NewStrategyHandler(storageManager *storage.Manager, log *logrus.Logger) *StrategyHandler {
	return &StrategyHandler{
		storageManager: storageManager,
		log:            log,
	}
}

// RegisterRoutesGin registers all strategy routes on the Gin router
func (h *StrategyHandler) RegisterRoutesGin(router gin.IRouter) {
	router.GET("/api/v1/strategies", h.ListStrategies)
	router.POST("/api/v1/strategies", h.CreateStrategy)
	router.GET("/api/v1/strategies/type/:type", h.GetByType)
	router.GET("/api/v1/strategies/:name", h.GetStrategy)
	router.PUT("/api/v1/strategies/:name", h.UpdateStrategy)
	router.DELETE("/api/v1/strategies/:name", h.DeleteStrategy)
}

// ListStrategies returns all active trading strategies
// GET /api/v1/strategies
func (h *StrategyHandler) ListStrategies(c *gin.Context) {
	strategies, err := h.storageManager.TradingStyles.List()
	if err != nil {
		h.log.WithError(err).Error("Failed to list strategies")
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to list strategies"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"strategies": strategies, "count": len(strategies)},
	})
}

// GetStrategy retrieves a specific strategy by name
// GET /api/v1/strategies/:name
func (h *StrategyHandler) GetStrategy(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "strategy name is required"})
		return
	}

	strategy, err := h.storageManager.TradingStyles.Load(name)
	if err != nil {
		h.log.WithError(err).WithField("name", name).Error("Failed to load strategy")
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "strategy not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": strategy})
}

// CreateStrategy creates a new trading strategy
// POST /api/v1/strategies
func (h *StrategyHandler) CreateStrategy(c *gin.Context) {
	var strategy ai.Strategy
	if err := c.ShouldBindJSON(&strategy); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "invalid request body"})
		return
	}

	if strategy.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "strategy name is required"})
		return
	}

	if len(strategy.Timeframes) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "at least one timeframe is required"})
		return
	}

	if err := h.storageManager.TradingStyles.Save(&strategy); err != nil {
		h.log.WithError(err).WithField("name", strategy.Name).Error("Failed to save strategy")
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to save strategy"})
		return
	}

	h.log.WithField("name", strategy.Name).Info("Strategy created")
	c.JSON(http.StatusCreated, gin.H{"success": true, "message": "strategy created successfully", "data": strategy})
}

// UpdateStrategy updates an existing trading strategy
// PUT /api/v1/strategies/:name
func (h *StrategyHandler) UpdateStrategy(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "strategy name is required"})
		return
	}

	if _, err := h.storageManager.TradingStyles.Load(name); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "strategy not found"})
		return
	}

	var strategy ai.Strategy
	if err := c.ShouldBindJSON(&strategy); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "invalid request body"})
		return
	}
	strategy.Name = name

	if err := h.storageManager.TradingStyles.Save(&strategy); err != nil {
		h.log.WithError(err).WithField("name", name).Error("Failed to update strategy")
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to update strategy"})
		return
	}

	h.log.WithField("name", name).Info("Strategy updated")
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "strategy updated successfully", "data": strategy})
}

// DeleteStrategy deletes a trading strategy
// DELETE /api/v1/strategies/:name
func (h *StrategyHandler) DeleteStrategy(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "strategy name is required"})
		return
	}

	if err := h.storageManager.TradingStyles.Delete(name); err != nil {
		h.log.WithError(err).WithField("name", name).Error("Failed to delete strategy")
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to delete strategy"})
		return
	}

	h.log.WithField("name", name).Info("Strategy deleted")
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "strategy deleted successfully"})
}

// GetByType returns strategies filtered by type
// GET /api/v1/strategies/type/:type
func (h *StrategyHandler) GetByType(c *gin.Context) {
	styleType := c.Param("type")
	if styleType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "strategy type is required"})
		return
	}

	strategies, err := h.storageManager.TradingStyles.GetByType(styleType)
	if err != nil {
		h.log.WithError(err).WithField("type", styleType).Error("Failed to get strategies by type")
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to get strategies"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"type": styleType, "strategies": strategies, "count": len(strategies)},
	})
}
