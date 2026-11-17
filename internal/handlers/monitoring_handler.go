package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/ai/monitoring"
	"github.com/sirupsen/logrus"
)

// MonitoringHandler handles monitoring API endpoints
type MonitoringHandler struct {
	monitoringService *monitoring.Service
	log               *logrus.Logger
}

// NewMonitoringHandler creates a new monitoring handler
func NewMonitoringHandler(monitoringService *monitoring.Service, log *logrus.Logger) *MonitoringHandler {
	return &MonitoringHandler{
		monitoringService: monitoringService,
		log:               log,
	}
}

// RegisterRoutesGin registers all monitoring routes on the Gin router
func (h *MonitoringHandler) RegisterRoutesGin(router gin.IRouter) {
	router.GET("/api/v1/monitoring/metrics", h.GetMetrics)
	router.GET("/api/v1/monitoring/decisions", h.GetDecisions)
	router.GET("/api/v1/monitoring/report/:period", h.GetReport)
}

// GetMetrics returns performance metrics for a period
// GET /api/v1/monitoring/metrics?period=24h
func (h *MonitoringHandler) GetMetrics(c *gin.Context) {
	period := c.Query("period")
	if period == "" {
		period = "24h"
	}

	metrics, err := (*h.monitoringService).GetMetrics(c.Request.Context(), period)
	if err != nil {
		h.log.WithError(err).Error("Failed to get metrics")
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to get metrics"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": metrics})
}

// GetDecisions returns recent trading decisions
// GET /api/v1/monitoring/decisions?limit=50
func (h *MonitoringHandler) GetDecisions(c *gin.Context) {
	limit := 50
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 {
		limit = l
	}

	decisions, err := (*h.monitoringService).GetRecentDecisions(c.Request.Context(), limit)
	if err != nil {
		h.log.WithError(err).Error("Failed to get decisions")
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to get decisions"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"total": len(decisions), "decisions": decisions},
	})
}

// GetReport generates a performance report for a period
// GET /api/v1/monitoring/report/:period
func (h *MonitoringHandler) GetReport(c *gin.Context) {
	period := c.Param("period")
	if period == "" {
		period = "24h"
	}

	report, err := (*h.monitoringService).GenerateReport(c.Request.Context(), period)
	if err != nil {
		h.log.WithError(err).Error("Failed to generate report")
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to generate report"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": report})
}
