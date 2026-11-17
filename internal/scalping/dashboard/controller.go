package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/killswitch"
	"github.com/rantipay/trading-mcp/internal/scalping/engine"
	"github.com/rantipay/trading-mcp/internal/scalping/executor"
	"github.com/rantipay/trading-mcp/internal/scalping/models"
	"github.com/rantipay/trading-mcp/internal/scalping/risk"
	"github.com/rantipay/trading-mcp/internal/scalping/strategies"
)

// DashboardController maneja las peticiones del dashboard
type DashboardController struct {
	logger       *logrus.Logger
	engine       *engine.ScalpingEngine
	orchestrator *strategies.StrategyOrchestrator
	executor     *executor.HighFrequencyExecutor
	riskManager  *risk.AdaptiveRiskManager
}

// NewDashboardController crea un nuevo controlador de dashboard
func NewDashboardController(
	logger *logrus.Logger,
	engine *engine.ScalpingEngine,
	orchestrator *strategies.StrategyOrchestrator,
	executor *executor.HighFrequencyExecutor,
	riskManager *risk.AdaptiveRiskManager,
) *DashboardController {
	return &DashboardController{
		logger:       logger,
		engine:       engine,
		orchestrator: orchestrator,
		executor:     executor,
		riskManager:  riskManager,
	}
}

// RegisterRoutes registra las rutas del dashboard.
//
// SCALP-01: these routes were previously registered directly on the base
// *gin.Engine with no auth middleware at all — /api/v1/scalping/pairs/add,
// /start, PUT /risk/limits and /risk/sentinel/kill-switch were reachable by
// anyone. authMiddleware (typically the same KongJWTMiddleware instance used
// by the REST v1 router) is applied to this controller's own route group so
// protection does not depend on registration order relative to other routers.
func (dc *DashboardController) RegisterRoutes(router *gin.Engine, authMiddleware ...gin.HandlerFunc) {
	api := router.Group("/api/v1/scalping")
	if len(authMiddleware) > 0 {
		api.Use(authMiddleware...)
	}
	{
		// Status endpoints
		api.GET("/status", dc.GetStatus)
		api.GET("/metrics", dc.GetMetrics)
		api.GET("/health", dc.GetHealth)

		// Position endpoints
		api.GET("/positions", dc.GetPositions)
		api.GET("/positions/history", dc.GetPositionHistory)

		// Strategy endpoints
		api.GET("/strategies", dc.GetStrategies)
		api.GET("/strategies/:name", dc.GetStrategy)
		api.POST("/strategies/:name/start", dc.StartStrategy)
		api.POST("/strategies/:name/stop", dc.StopStrategy)
		api.PUT("/strategies/:name/config", dc.UpdateStrategyConfig)
		api.GET("/strategies/:name/performance", dc.GetStrategyPerformance)

		// Risk endpoints
		api.GET("/risk/limits", dc.GetRiskLimits)
		api.PUT("/risk/limits", dc.UpdateRiskLimits)
		api.GET("/risk/exposure", dc.GetExposure)

		// Risk Sentinel endpoints
		api.GET("/risk/sentinel/state", dc.GetRiskSentinelState)
		api.POST("/risk/sentinel/kill-switch", dc.ActivateKillSwitch)
		api.DELETE("/risk/sentinel/kill-switch", dc.DeactivateKillSwitch)

		// Execution endpoints
		api.GET("/execution/latency", dc.GetLatencyStats)
		api.GET("/execution/history", dc.GetExecutionHistory)

		// Control endpoints
		api.POST("/start", dc.StartEngine)
		api.POST("/stop", dc.StopEngine)
		api.POST("/pairs/add", dc.AddPair)
		api.POST("/pairs/remove", dc.RemovePair)
	}
}

// GetStatus retorna el estado del sistema.
// El engine.GetStatus() devuelve una copia segura y lee running desde atomic.Bool,
// por lo que es seguro llamarlo durante un Stop en curso (cleanup en background).
func (dc *DashboardController) GetStatus(c *gin.Context) {
	status := dc.engine.GetStatus()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    status,
	})
}

// GetMetrics retorna las métricas del sistema
func (dc *DashboardController) GetMetrics(c *gin.Context) {
	metrics := dc.engine.GetMetrics()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    metrics,
	})
}

// GetHealth retorna el estado de salud del sistema
func (dc *DashboardController) GetHealth(c *gin.Context) {
	status := dc.engine.GetStatus()

	health := gin.H{
		"status":    status.HealthStatus,
		"running":   status.Running,
		"uptime":    status.Uptime.String(),
		"errors":    status.Errors,
		"timestamp": time.Now(),
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    health,
	})
}

// GetPositions retorna las posiciones abiertas
func (dc *DashboardController) GetPositions(c *gin.Context) {
	// TODO: Implementar obtención de posiciones abiertas
	positions := []interface{}{}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    positions,
		"count":   len(positions),
	})
}

// GetPositionHistory retorna el historial de posiciones
func (dc *DashboardController) GetPositionHistory(c *gin.Context) {
	limit := 100
	if limitParam := c.Query("limit"); limitParam != "" {
		var err error
		if _, err = json.Marshal(limitParam); err == nil {
			// Parse limit
		}
	}

	history := dc.executor.GetOrderHistory(limit)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    history,
		"count":   len(history),
	})
}

// GetStrategies retorna todas las estrategias
func (dc *DashboardController) GetStrategies(c *gin.Context) {
	allStrategies := dc.orchestrator.GetAllStrategies()
	activeStrategies := dc.orchestrator.GetActiveStrategies()

	strategies := make([]gin.H, 0, len(allStrategies))
	for _, name := range allStrategies {
		isActive := false
		for _, active := range activeStrategies {
			if active == name {
				isActive = true
				break
			}
		}

		perf, _ := dc.orchestrator.GetStrategyPerformance(name)
		weight, _ := dc.orchestrator.GetStrategyWeight(name)

		strategies = append(strategies, gin.H{
			"name":        name,
			"active":      isActive,
			"weight":      weight,
			"performance": perf,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    strategies,
		"count":   len(strategies),
	})
}

// GetStrategy retorna información de una estrategia específica
func (dc *DashboardController) GetStrategy(c *gin.Context) {
	name := c.Param("name")

	perf, err := dc.orchestrator.GetStrategyPerformance(name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	weight, _ := dc.orchestrator.GetStrategyWeight(name)
	activeStrategies := dc.orchestrator.GetActiveStrategies()

	isActive := false
	for _, active := range activeStrategies {
		if active == name {
			isActive = true
			break
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"name":        name,
			"active":      isActive,
			"weight":      weight,
			"performance": perf,
		},
	})
}

// StartStrategy inicia una estrategia
func (dc *DashboardController) StartStrategy(c *gin.Context) {
	if killswitch.Ring0Active() {
		c.JSON(http.StatusLocked, gin.H{"success": false, "error": killswitch.ErrorCode})
		return
	}
	name := c.Param("name")

	err := dc.orchestrator.EnableStrategy(name)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	dc.logger.Infof("Strategy started via API: %s", name)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Strategy started successfully",
	})
}

// StopStrategy detiene una estrategia
func (dc *DashboardController) StopStrategy(c *gin.Context) {
	name := c.Param("name")

	err := dc.orchestrator.DisableStrategy(name)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	dc.logger.Infof("Strategy stopped via API: %s", name)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Strategy stopped successfully",
	})
}

// UpdateStrategyConfig actualiza la configuración de una estrategia
func (dc *DashboardController) UpdateStrategyConfig(c *gin.Context) {
	name := c.Param("name")

	var config models.StrategyConfig
	if err := c.ShouldBindJSON(&config); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid configuration",
		})
		return
	}

	// TODO: Implementar actualización de configuración
	dc.logger.Infof("Strategy config updated via API: %s", name)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Strategy configuration updated successfully",
	})
}

// GetStrategyPerformance retorna el rendimiento de una estrategia
func (dc *DashboardController) GetStrategyPerformance(c *gin.Context) {
	name := c.Param("name")

	perf, err := dc.orchestrator.GetStrategyPerformance(name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    perf,
	})
}

// GetRiskLimits retorna los límites de riesgo actuales
func (dc *DashboardController) GetRiskLimits(c *gin.Context) {
	params := dc.riskManager.GetRiskParameters()

	canTrade, reason := dc.riskManager.CheckRiskLimits()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"parameters":         params,
			"can_trade":          canTrade,
			"limit_reason":       reason,
			"current_exposure":   dc.riskManager.GetCurrentExposure(),
			"daily_pnl":          dc.riskManager.GetDailyPnL(),
			"consecutive_losses": dc.riskManager.GetConsecutiveLosses(),
		},
	})
}

// UpdateRiskLimits actualiza los límites de riesgo
func (dc *DashboardController) UpdateRiskLimits(c *gin.Context) {
	var params models.RiskParameters
	if err := c.ShouldBindJSON(&params); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid risk parameters",
		})
		return
	}

	dc.riskManager.UpdateRiskParameters(&params)
	dc.logger.Info("Risk parameters updated via API")

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Risk parameters updated successfully",
	})
}

// GetExposure retorna la exposición actual
func (dc *DashboardController) GetExposure(c *gin.Context) {
	exposure := dc.riskManager.GetCurrentExposure()
	params := dc.riskManager.GetRiskParameters()

	exposurePercent := 0.0
	if params.MaxTotalExposureUSD > 0 {
		exposurePercent = (exposure / params.MaxTotalExposureUSD) * 100
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"current_exposure":   exposure,
			"max_exposure":       params.MaxTotalExposureUSD,
			"exposure_percent":   exposurePercent,
			"available_exposure": params.MaxTotalExposureUSD - exposure,
		},
	})
}

// GetLatencyStats retorna estadísticas de latencia
func (dc *DashboardController) GetLatencyStats(c *gin.Context) {
	stats := dc.executor.GetLatencyStats()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    stats,
	})
}

// GetExecutionHistory retorna el historial de ejecuciones
func (dc *DashboardController) GetExecutionHistory(c *gin.Context) {
	limit := 50
	if limitParam := c.Query("limit"); limitParam != "" {
		// Parse limit
	}

	history := dc.executor.GetOrderHistory(limit)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    history,
		"count":   len(history),
	})
}

// StartEngine inicia el motor de scalping.
// Incluye running en la respuesta para que el cliente Flutter pueda reconciliar
// el estado local sin tener que hacer un GET /status adicional.
func (dc *DashboardController) StartEngine(c *gin.Context) {
	if killswitch.Ring0Active() {
		c.JSON(http.StatusLocked, gin.H{"success": false, "error": killswitch.ErrorCode})
		return
	}
	ctx := context.Background()

	err := dc.engine.Start(ctx)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   err.Error(),
			"running": dc.engine.IsRunning(),
		})
		return
	}

	dc.logger.Info("Scalping engine started via API")

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Scalping engine started successfully",
		"running": dc.engine.IsRunning(),
	})
}

// StopEngine detiene el motor de scalping.
// El motor marca running=false inmediatamente; el cleanup (cierre de posiciones,
// notificaciones) corre en goroutine de fondo. Por eso devolvemos 202 Accepted.
func (dc *DashboardController) StopEngine(c *gin.Context) {
	err := dc.engine.Stop()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   err.Error(),
			"running": dc.engine.IsRunning(),
		})
		return
	}

	dc.logger.Info("Scalping engine stop initiated via API")

	c.JSON(http.StatusAccepted, gin.H{
		"success": true,
		"message": "Scalping engine stop initiated (cleanup running in background)",
		"running": dc.engine.IsRunning(),
	})
}

// AddPair agrega un par de trading
func (dc *DashboardController) AddPair(c *gin.Context) {
	var req struct {
		Exchange string `json:"exchange" binding:"required"`
		Pair     string `json:"pair" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid request",
		})
		return
	}

	err := dc.engine.AddPair(req.Exchange, req.Pair)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	dc.logger.Infof("Pair added via API: %s:%s", req.Exchange, req.Pair)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Pair added successfully",
	})
}

// RemovePair remueve un par de trading
func (dc *DashboardController) RemovePair(c *gin.Context) {
	var req struct {
		Exchange string `json:"exchange" binding:"required"`
		Pair     string `json:"pair" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid request",
		})
		return
	}

	err := dc.engine.RemovePair(req.Exchange, req.Pair)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	dc.logger.Infof("Pair removed via API: %s:%s", req.Exchange, req.Pair)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Pair removed successfully",
	})
}

// GetRiskSentinelState retorna el estado del Risk Sentinel
func (dc *DashboardController) GetRiskSentinelState(c *gin.Context) {
	params := dc.riskManager.GetRiskParameters()
	drawdownMetrics := dc.riskManager.GetDrawdownMetrics()
	exposureBySymbol := dc.riskManager.GetExposureBySymbol()
	killSwitchActive, killSwitchReason, killSwitchTime := dc.riskManager.GetKillSwitchInfo()

	// Determinar modo de riesgo basado en drawdown
	riskMode := "Normal"
	dailyDrawdown := drawdownMetrics["current_drawdown_daily"].(float64)
	if dailyDrawdown > 80 {
		riskMode = "Critical"
	} else if dailyDrawdown > 60 {
		riskMode = "High"
	} else if dailyDrawdown > 40 {
		riskMode = "Elevated"
	}

	state := gin.H{
		"current_drawdown_daily":   drawdownMetrics["current_drawdown_daily"],
		"current_drawdown_weekly":  drawdownMetrics["current_drawdown_weekly"],
		"current_drawdown_monthly": drawdownMetrics["current_drawdown_monthly"],
		"total_exposure":           dc.riskManager.GetCurrentExposure(),
		"exposure_by_symbol":       exposureBySymbol,
		"consecutive_losses":       dc.riskManager.GetConsecutiveLosses(),
		"risk_mode":                riskMode,
		"kill_switch_active":       killSwitchActive,
		"last_update":              time.Now().Format(time.RFC3339),
		"max_daily_drawdown":       params.MaxDailyLossUSD,
		"max_weekly_drawdown":      params.MaxDailyLossUSD * 5,  // Aproximación
		"max_monthly_drawdown":     params.MaxDailyLossUSD * 20, // Aproximación
		"max_consecutive_losses":   params.MaxConsecutiveLosses,
		"max_total_exposure":       params.MaxTotalExposureUSD,
		"daily_pnl":                drawdownMetrics["daily_pnl"],
		"weekly_pnl":               drawdownMetrics["weekly_pnl"],
		"monthly_pnl":              drawdownMetrics["monthly_pnl"],
	}

	if killSwitchActive {
		state["kill_switch_reason"] = killSwitchReason
		state["kill_switch_time"] = killSwitchTime.Format(time.RFC3339)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    state,
	})
}

// ActivateKillSwitch activa el kill switch de emergencia
func (dc *DashboardController) ActivateKillSwitch(c *gin.Context) {
	var req struct {
		Reason string `json:"reason"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		req.Reason = "Manual activation by user"
	}

	if req.Reason == "" {
		req.Reason = "Manual activation by user"
	}

	dc.riskManager.ActivateKillSwitch(req.Reason)
	dc.logger.WithField("reason", req.Reason).Warn("🚨 Kill switch activated via API")

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Kill switch activated",
		"data": gin.H{
			"active":       true,
			"reason":       req.Reason,
			"activated_at": time.Now().Format(time.RFC3339),
		},
	})
}

// DeactivateKillSwitch desactiva el kill switch
func (dc *DashboardController) DeactivateKillSwitch(c *gin.Context) {
	if !dc.riskManager.IsKillSwitchActive() {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Kill switch is not active",
		})
		return
	}

	dc.riskManager.DeactivateKillSwitch()
	dc.logger.Info("✅ Kill switch deactivated via API")

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Kill switch deactivated",
		"data": gin.H{
			"active":         false,
			"deactivated_at": time.Now().Format(time.RFC3339),
		},
	})
}
