package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/storage"
	"github.com/sirupsen/logrus"
)

// MetricsHandler handles metrics and trade history API endpoints
type MetricsHandler struct {
	storageManager *storage.Manager
	log            *logrus.Logger
}

// NewMetricsHandler creates a new metrics handler
func NewMetricsHandler(storageManager *storage.Manager, log *logrus.Logger) *MetricsHandler {
	return &MetricsHandler{
		storageManager: storageManager,
		log:            log,
	}
}

// RegisterRoutesGin registers all metrics routes on the Gin router
func (h *MetricsHandler) RegisterRoutesGin(router gin.IRouter) {
	router.GET("/api/v1/metrics/type/:type", h.GetMetricsByType)
	router.POST("/api/v1/metrics/calculate/:style", h.CalculateMetrics)
	router.GET("/api/v1/metrics/:style/:timeframe", h.GetMetricsByStyleAndTimeframe)
	router.GET("/api/v1/metrics/:style", h.GetMetricsByStyle)

	router.GET("/api/v1/trades", h.ListTrades)
	router.GET("/api/v1/trades/open", h.GetOpenTrades)
	router.GET("/api/v1/trades/closed", h.GetClosedTrades)
	router.GET("/api/v1/trades/style/:style", h.GetTradesByStyle)
	router.GET("/api/v1/trades/timeframe/:timeframe", h.GetTradesByTimeframe)
	router.GET("/api/v1/trades/:id", h.GetTrade)

	router.GET("/api/v1/storage/stats", h.GetStorageStats)
	router.GET("/api/v1/storage/cache/stats", h.GetCacheStats)
	router.POST("/api/v1/storage/cache/clean", h.CleanCache)
}

// GetMetricsByStyle returns metrics for a specific trading style
// GET /api/v1/metrics/:style
func (h *MetricsHandler) GetMetricsByStyle(c *gin.Context) {
	style := c.Param("style")
	if style == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "style is required"})
		return
	}

	metrics, err := h.storageManager.PerformanceMetrics.GetAggregated(style)
	if err != nil {
		h.log.WithError(err).WithField("style", style).Error("Failed to get metrics")
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "metrics not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": metrics})
}

// GetMetricsByStyleAndTimeframe returns metrics for a specific style and timeframe
// GET /api/v1/metrics/:style/:timeframe
func (h *MetricsHandler) GetMetricsByStyleAndTimeframe(c *gin.Context) {
	style := c.Param("style")
	timeframe := c.Param("timeframe")

	if style == "" || timeframe == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "style and timeframe are required"})
		return
	}

	metrics, err := h.storageManager.PerformanceMetrics.LoadMetrics(style, timeframe)
	if err != nil {
		h.log.WithError(err).WithFields(logrus.Fields{"style": style, "timeframe": timeframe}).Error("Failed to get metrics")
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "metrics not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": metrics})
}

// GetMetricsByType returns metrics for all styles of a specific type
// GET /api/v1/metrics/type/:type
func (h *MetricsHandler) GetMetricsByType(c *gin.Context) {
	styleType := c.Param("type")
	if styleType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "type is required"})
		return
	}

	metrics, err := h.storageManager.PerformanceMetrics.GetByStyleType(styleType)
	if err != nil {
		h.log.WithError(err).WithField("type", styleType).Error("Failed to get metrics by type")
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to get metrics"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"type": styleType, "metrics": metrics, "count": len(metrics)},
	})
}

// CalculateMetrics calculates metrics from trade history
// POST /api/v1/metrics/calculate/:style
func (h *MetricsHandler) CalculateMetrics(c *gin.Context) {
	style := c.Param("style")
	if style == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "style is required"})
		return
	}

	timeframe := c.Query("timeframe")
	end := time.Now()
	start := end.AddDate(0, 0, -30)

	if s := c.Query("start"); s != "" {
		if parsed, err := time.Parse(time.RFC3339, s); err == nil {
			start = parsed
		}
	}
	if e := c.Query("end"); e != "" {
		if parsed, err := time.Parse(time.RFC3339, e); err == nil {
			end = parsed
		}
	}

	metrics, err := h.storageManager.PerformanceMetrics.CalculateMetricsFromTrades(style, timeframe, start, end)
	if err != nil {
		h.log.WithError(err).WithField("style", style).Error("Failed to calculate metrics")
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to calculate metrics"})
		return
	}

	if err := h.storageManager.PerformanceMetrics.SaveMetrics(style, timeframe, metrics); err != nil {
		h.log.WithError(err).Warn("Failed to save calculated metrics")
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "metrics calculated successfully", "data": metrics})
}

// ListTrades returns paginated list of trades
// GET /api/v1/trades?limit=50
func (h *MetricsHandler) ListTrades(c *gin.Context) {
	limit := 50
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 {
		limit = l
	}

	trades, err := h.storageManager.TradeHistory.GetClosedTrades(limit)
	if err != nil {
		h.log.WithError(err).Error("Failed to list trades")
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to list trades"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"trades": trades, "count": len(trades), "limit": limit},
	})
}

// GetTrade returns a specific trade by ID
// GET /api/v1/trades/:id
func (h *MetricsHandler) GetTrade(c *gin.Context) {
	tradeID := c.Param("id")
	if tradeID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "trade ID is required"})
		return
	}

	trade, err := h.storageManager.TradeHistory.GetByID(tradeID)
	if err != nil {
		h.log.WithError(err).WithField("trade_id", tradeID).Error("Failed to get trade")
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "trade not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": trade})
}

// GetTradesByStyle returns trades filtered by style
// GET /api/v1/trades/style/:style
func (h *MetricsHandler) GetTradesByStyle(c *gin.Context) {
	style := c.Param("style")
	if style == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "style is required"})
		return
	}

	trades, err := h.storageManager.TradeHistory.GetByStyle(style)
	if err != nil {
		h.log.WithError(err).WithField("style", style).Error("Failed to get trades by style")
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to get trades"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"style": style, "trades": trades, "count": len(trades)},
	})
}

// GetTradesByTimeframe returns trades filtered by timeframe
// GET /api/v1/trades/timeframe/:timeframe
func (h *MetricsHandler) GetTradesByTimeframe(c *gin.Context) {
	timeframe := c.Param("timeframe")
	if timeframe == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "timeframe is required"})
		return
	}

	trades, err := h.storageManager.TradeHistory.GetByTimeframe(timeframe)
	if err != nil {
		h.log.WithError(err).WithField("timeframe", timeframe).Error("Failed to get trades by timeframe")
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to get trades"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"timeframe": timeframe, "trades": trades, "count": len(trades)},
	})
}

// GetOpenTrades returns all open trades
// GET /api/v1/trades/open
func (h *MetricsHandler) GetOpenTrades(c *gin.Context) {
	trades, err := h.storageManager.TradeHistory.GetOpenTrades()
	if err != nil {
		h.log.WithError(err).Error("Failed to get open trades")
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to get open trades"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"trades": trades, "count": len(trades)},
	})
}

// GetClosedTrades returns closed trades
// GET /api/v1/trades/closed?limit=100
func (h *MetricsHandler) GetClosedTrades(c *gin.Context) {
	limit := 100
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 {
		limit = l
	}

	trades, err := h.storageManager.TradeHistory.GetClosedTrades(limit)
	if err != nil {
		h.log.WithError(err).Error("Failed to get closed trades")
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to get closed trades"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"trades": trades, "count": len(trades), "limit": limit},
	})
}

// GetStorageStats returns database statistics
// GET /api/v1/storage/stats
func (h *MetricsHandler) GetStorageStats(c *gin.Context) {
	stats := h.storageManager.GetStats()
	c.JSON(http.StatusOK, gin.H{"success": true, "data": stats})
}

// GetCacheStats returns cache statistics
// GET /api/v1/storage/cache/stats
func (h *MetricsHandler) GetCacheStats(c *gin.Context) {
	stats, err := h.storageManager.GetCacheStats()
	if err != nil {
		h.log.WithError(err).Error("Failed to get cache stats")
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to get cache stats"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": stats})
}

// CleanCache removes expired cache entries
// POST /api/v1/storage/cache/clean
func (h *MetricsHandler) CleanCache(c *gin.Context) {
	if err := h.storageManager.CleanExpiredCache(); err != nil {
		h.log.WithError(err).Error("Failed to clean cache")
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to clean cache"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "cache cleaned successfully"})
}
