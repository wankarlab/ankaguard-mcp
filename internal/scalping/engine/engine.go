package engine

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/thrasher-corp/gocryptotrader/exchanges/order"

	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/monitoring"
	"github.com/rantipay/trading-mcp/internal/notifications"
	"github.com/rantipay/trading-mcp/internal/scalping/models"
	"github.com/rantipay/trading-mcp/internal/scalping/risk"
	"github.com/rantipay/trading-mcp/internal/scalping/strategies"
)

// ScalpingEngine es el motor principal del sistema de scalping
type ScalpingEngine struct {
	mu              sync.RWMutex
	ctx             context.Context
	cancel          context.CancelFunc
	logger          *logrus.Logger
	gctClient       gct.GCTClient
	notificationMgr *notifications.NotificationManager
	monitoringMgr   *monitoring.MetricsManager

	// Estado del motor
	running          atomic.Bool
	startedAt        time.Time
	activePairs      map[string]bool
	activeStrategies map[string]bool
	openPositions    map[string]*models.ScalpingTrade
	metrics          *models.EngineMetrics
	status           *models.EngineStatus

	// Configuración
	riskParams      *models.RiskParameters
	strategyConfigs map[string]*models.StrategyConfig

	// riskManager, when set via SetRiskManager, gates every signal processed
	// by processSignal through AdaptiveRiskManager (kill-switch, exposure/daily
	// loss limits, Kelly-sized positions) instead of the fixed local riskParams.
	// Nil is tolerated for callers that never wire it (e.g. tests), but
	// processSignal logs loudly when it falls back to the unmanaged path.
	riskManager *risk.AdaptiveRiskManager

	// Canales para comunicación
	signalChan chan *models.ScalpingSignal
	tradeChan  chan *models.ScalpingTrade
	errorChan  chan error
	stopChan   chan struct{}
}

// ScalpingEngineInterface define la interfaz del motor de scalping
type ScalpingEngineInterface interface {
	Start(ctx context.Context) error
	Stop() error
	AddPair(exchange, pair string) error
	RemovePair(exchange, pair string) error
	GetStatus() *models.EngineStatus
	GetMetrics() *models.EngineMetrics
	IsRunning() bool
	GetActivePairs() []string
	GetActiveStrategies() []string
}

// NewScalpingEngine crea una nueva instancia del motor de scalping
func NewScalpingEngine(
	logger *logrus.Logger,
	gctClient gct.GCTClient,
	notificationMgr *notifications.NotificationManager,
	monitoringMgr *monitoring.MetricsManager,
) *ScalpingEngine {
	ctx, cancel := context.WithCancel(context.Background())

	return &ScalpingEngine{
		ctx:              ctx,
		cancel:           cancel,
		logger:           logger,
		gctClient:        gctClient,
		notificationMgr:  notificationMgr,
		monitoringMgr:    monitoringMgr,
		activePairs:      make(map[string]bool),
		activeStrategies: make(map[string]bool),
		openPositions:    make(map[string]*models.ScalpingTrade),
		metrics:          &models.EngineMetrics{},
		status:           &models.EngineStatus{},
		strategyConfigs:  make(map[string]*models.StrategyConfig),
		signalChan:       make(chan *models.ScalpingSignal, 1000),
		tradeChan:        make(chan *models.ScalpingTrade, 1000),
		errorChan:        make(chan error, 100),
		stopChan:         make(chan struct{}),
		riskParams:       getDefaultRiskParameters(),
	}
}

// Start inicia el motor de scalping
// SetRiskManager wires an AdaptiveRiskManager into the engine so processSignal
// sizes positions and enforces kill-switch/exposure/daily-loss limits through
// it instead of the fixed local riskParams (SCALP-02).
func (e *ScalpingEngine) SetRiskManager(rm *risk.AdaptiveRiskManager) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.riskManager = rm
}

func (e *ScalpingEngine) Start(ctx context.Context) error {
	// Atomic transition false -> true; rejects concurrent Start calls.
	if !e.running.CompareAndSwap(false, true) {
		return fmt.Errorf("scalping engine is already running")
	}

	e.logger.Info("Starting scalping engine...")

	e.mu.Lock()
	// Recreate ctx/cancel/stopChan so the engine can be restarted after Stop().
	e.ctx, e.cancel = context.WithCancel(context.Background())
	e.stopChan = make(chan struct{})
	e.startedAt = time.Now()
	e.updateStatusLocked()
	e.mu.Unlock()

	// Iniciar goroutines principales
	go e.signalProcessor()
	go e.tradeProcessor()
	go e.errorProcessor()
	go e.metricsUpdater()
	go e.healthChecker()
	go e.marketAnalyzer() // NUEVO: Analiza el mercado continuamente

	e.logger.Info("Scalping engine started successfully")

	// Notificar inicio
	if e.notificationMgr != nil {
		e.notificationMgr.TriggerCustomNotification(
			"Scalping Engine Started",
			"The scalping engine has been started successfully",
			notifications.SeverityInfo,
			nil,
			"system",
		)
	}

	return nil
}

// Stop detiene el motor de scalping
func (e *ScalpingEngine) Stop() error {
	// Atomic transition true -> false; rejects concurrent Stop calls.
	if !e.running.CompareAndSwap(true, false) {
		return fmt.Errorf("scalping engine is not running")
	}
	e.logger.Info("Stopping scalping engine...")

	// Snapshot positions to close, then unlock quickly to keep the HTTP
	// handler responsive. Heavy work (notifications, real order closes) is
	// pushed to a background goroutine in performShutdownCleanup.
	e.mu.Lock()
	positionsToClose := make([]*models.ScalpingTrade, 0, len(e.openPositions))
	for _, trade := range e.openPositions {
		positionsToClose = append(positionsToClose, trade)
	}
	// Close channels and cancel context (fast, inside the lock to avoid
	// races with a concurrent Start).
	select {
	case <-e.stopChan:
		// Already closed; nothing to do.
	default:
		close(e.stopChan)
	}
	e.cancel()
	e.updateStatusLocked()
	e.mu.Unlock()

	e.logger.Info("Scalping engine stopped (cleanup running in background)")

	// Heavy cleanup runs in background so the caller (HTTP handler) returns
	// immediately and does not trigger "connection closed before full header
	// was received" errors in production.
	go e.performShutdownCleanup(positionsToClose)

	return nil
}

// performShutdownCleanup runs the slow shutdown work (closing positions,
// sending notifications) off the synchronous HTTP path.
func (e *ScalpingEngine) performShutdownCleanup(positions []*models.ScalpingTrade) {
	for _, trade := range positions {
		e.logger.Warnf("Closing open position on shutdown: %s", trade.ID)
		// TODO: Implementar cierre de posición real via gctClient.
	}

	if e.notificationMgr != nil {
		e.notificationMgr.TriggerCustomNotification(
			"Scalping Engine Stopped",
			"The scalping engine has been stopped",
			notifications.SeverityWarning,
			nil,
			"system",
		)
	}
}

// AddPair agrega un par de trading al motor
func (e *ScalpingEngine) AddPair(exchange, pair string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	key := fmt.Sprintf("%s:%s", exchange, pair)
	if e.activePairs[key] {
		return fmt.Errorf("pair %s is already active", key)
	}

	e.activePairs[key] = true
	e.logger.Infof("Added pair: %s", key)

	// TODO: Iniciar monitoreo del par

	e.updateStatusLocked()
	return nil
}

// RemovePair remueve un par de trading del motor
func (e *ScalpingEngine) RemovePair(exchange, pair string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	key := fmt.Sprintf("%s:%s", exchange, pair)
	if !e.activePairs[key] {
		return fmt.Errorf("pair %s is not active", key)
	}

	delete(e.activePairs, key)
	e.logger.Infof("Removed pair: %s", key)

	// TODO: Detener monitoreo del par

	e.updateStatusLocked()
	return nil
}

// GetStatus retorna el estado actual del motor
func (e *ScalpingEngine) GetStatus() *models.EngineStatus {
	e.mu.RLock()
	defer e.mu.RUnlock()

	e.updateStatusLocked()
	// Return a copy so later mutations to the internal status do not affect
	// the caller's snapshot.
	statusCopy := *e.status
	return &statusCopy
}

// GetMetrics retorna las métricas actuales del motor
func (e *ScalpingEngine) GetMetrics() *models.EngineMetrics {
	e.mu.RLock()
	defer e.mu.RUnlock()

	e.updateMetrics()
	return e.metrics
}

// IsRunning retorna si el motor está ejecutándose
func (e *ScalpingEngine) IsRunning() bool {
	return e.running.Load()
}

// GetActivePairs retorna la lista de pares activos
func (e *ScalpingEngine) GetActivePairs() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	pairs := make([]string, 0, len(e.activePairs))
	for pair := range e.activePairs {
		pairs = append(pairs, pair)
	}
	return pairs
}

// GetActiveStrategies retorna la lista de estrategias activas
func (e *ScalpingEngine) GetActiveStrategies() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	strategies := make([]string, 0, len(e.activeStrategies))
	for strategy := range e.activeStrategies {
		strategies = append(strategies, strategy)
	}
	return strategies
}

// signalProcessor procesa las señales de trading
func (e *ScalpingEngine) signalProcessor() {
	for {
		select {
		case signal := <-e.signalChan:
			e.processSignal(signal)
		case <-e.stopChan:
			return
		case <-e.ctx.Done():
			return
		}
	}
}

// tradeProcessor procesa los trades ejecutados
func (e *ScalpingEngine) tradeProcessor() {
	for {
		select {
		case trade := <-e.tradeChan:
			e.processTrade(trade)
		case <-e.stopChan:
			return
		case <-e.ctx.Done():
			return
		}
	}
}

// errorProcessor procesa los errores
func (e *ScalpingEngine) errorProcessor() {
	for {
		select {
		case err := <-e.errorChan:
			e.processError(err)
		case <-e.stopChan:
			return
		case <-e.ctx.Done():
			return
		}
	}
}

// metricsUpdater actualiza las métricas periódicamente
func (e *ScalpingEngine) metricsUpdater() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			e.updateMetrics()
		case <-e.stopChan:
			return
		case <-e.ctx.Done():
			return
		}
	}
}

// healthChecker verifica la salud del sistema
func (e *ScalpingEngine) healthChecker() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			e.checkHealth()
		case <-e.stopChan:
			return
		case <-e.ctx.Done():
			return
		}
	}
}

// processSignal procesa una señal de trading
func (e *ScalpingEngine) processSignal(signal *models.ScalpingSignal) {
	e.logger.Infof("📊 Processing signal: %s %s at $%.2f (confidence: %.2f)",
		signal.Direction, signal.Pair, signal.Price, signal.Confidence)

	// 1. Validar señal
	if signal.Confidence < e.riskParams.ConfidenceThreshold {
		e.logger.Warnf("Signal confidence %.2f below threshold %.2f, skipping",
			signal.Confidence, e.riskParams.ConfidenceThreshold)
		return
	}

	// 2. Verificar límites de riesgo
	e.mu.RLock()
	openPositions := len(e.openPositions)
	riskManager := e.riskManager
	e.mu.RUnlock()

	if openPositions >= 3 {
		e.logger.Warn("Maximum open positions reached, skipping signal")
		return
	}

	// 3. Calcular tamaño de posición.
	//
	// SCALP-02: previously this always used the fixed
	// e.riskParams.MaxPositionSizeUSD, bypassing AdaptiveRiskManager's
	// kill-switch, exposure limits, daily-loss cap and Kelly sizing entirely.
	// When a risk manager is wired in, defer to it for both the go/no-go
	// decision and the sizing; only fall back to the unmanaged fixed size
	// when no risk manager is configured (documented, logged, and expected to
	// go away once every caller wires one in).
	var positionSize float64
	if riskManager != nil {
		if ok, reason := riskManager.CheckRiskLimits(); !ok {
			e.logger.Warnf("Risk limits breached (%s), skipping signal for %s %s", reason, signal.Pair, signal.Direction)
			return
		}
		if riskManager.IsKillSwitchActive() {
			e.logger.Warn("Kill switch active, skipping signal")
			return
		}
		sizedCtx, sizedCancel := context.WithTimeout(context.Background(), 10*time.Second)
		size, err := riskManager.CalculatePositionSize(sizedCtx, signal)
		sizedCancel()
		if err != nil {
			e.logger.Errorf("Risk manager rejected position sizing for %s: %v", signal.Pair, err)
			return
		}
		positionSize = size
	} else {
		e.logger.Warn("⚠️ processSignal: no AdaptiveRiskManager wired — using unmanaged fixed position size (SCALP-02 fallback path)")
		positionSize = e.riskParams.MaxPositionSizeUSD
	}

	if positionSize <= 0 {
		e.logger.Warnf("Computed position size %.2f is non-positive, skipping signal for %s", positionSize, signal.Pair)
		return
	}

	// 4. Ejecutar orden via GCT
	e.logger.Infof("💰 EXECUTING TRADE: %s %s - $%.2f USD",
		signal.Direction, signal.Pair, positionSize)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Preparar orden
	side := signal.Direction // "buy" o "sell"

	// Ejecutar orden market
	clientOrderID := fmt.Sprintf("scalp_%d", time.Now().UnixNano())
	order, err := e.gctClient.SubmitOrder(ctx, &gct.SubmitOrderRequest{
		Exchange: signal.Exchange,
		Pair:     signal.Pair,
		Side:     side,
		Type:     "market",
		Amount:   positionSize / signal.Price, // Convertir USD a cantidad de crypto
		Price:    0,                           // Market order no necesita precio
		ClientID: clientOrderID,
	})

	if err != nil {
		e.logger.Errorf("❌ Failed to execute order: %v", err)
		e.errorChan <- fmt.Errorf("order execution failed: %w", err)
		return
	}

	e.logger.Infof("✅ ORDER EXECUTED: ID=%s, Price=$%.2f, Amount=%.8f",
		order.ID, order.Price, order.Amount)

	// Crear trade record
	trade := &models.ScalpingTrade{
		ID:         order.ID,
		Pair:       signal.Pair,
		Exchange:   signal.Exchange,
		EntryPrice: order.Price,
		Amount:     order.Amount,
		Status:     "open",
		OpenedAt:   time.Now(),
		SignalID:   signal.ID,
		OrderID:    order.ID,
		Strategies: signal.Strategies,
	}

	// Agregar a posiciones abiertas
	e.mu.Lock()
	e.openPositions[trade.ID] = trade
	e.mu.Unlock()

	// Enviar al trade processor
	select {
	case e.tradeChan <- trade:
		e.logger.Debug("Trade sent to processor")
	default:
		e.logger.Warn("Trade channel full")
	}
}

// processTrade procesa un trade ejecutado
func (e *ScalpingEngine) processTrade(trade *models.ScalpingTrade) {
	e.logger.WithFields(logrus.Fields{
		"trade_id": trade.ID,
		"pair":     trade.Pair,
		"exchange": trade.Exchange,
		"status":   trade.Status,
	}).Info("Processing trade")

	// 1. Update open positions tracking
	e.mu.Lock()
	if existingTrade, exists := e.openPositions[trade.ID]; exists {
		// Update existing trade with new information
		existingTrade.Status = trade.Status
		existingTrade.ExitPrice = trade.ExitPrice
		existingTrade.ClosedAt = trade.ClosedAt
		existingTrade.PnL = trade.PnL

		// If trade is closed, remove from open positions
		if trade.Status == "closed" || trade.Status == "stopped" {
			delete(e.openPositions, trade.ID)
		}
	}
	e.mu.Unlock()

	// 2. Calculate P&L if trade is closed
	if trade.Status == "closed" || trade.Status == "stopped" {
		pnl := (trade.ExitPrice - trade.EntryPrice) * trade.Amount
		if trade.Side == order.Sell {
			pnl = -pnl
		}
		trade.PnL = pnl

		e.logger.WithFields(logrus.Fields{
			"trade_id":    trade.ID,
			"entry_price": trade.EntryPrice,
			"exit_price":  trade.ExitPrice,
			"amount":      trade.Amount,
			"pnl":         pnl,
		}).Info("Trade closed with P&L")
	}

	// 3. Update metrics
	e.mu.Lock()
	e.metrics.TotalTrades++
	if trade.PnL > 0 {
		e.metrics.WinningTrades++
		e.metrics.TotalProfit += trade.PnL
	} else if trade.PnL < 0 {
		e.metrics.LosingTrades++
		e.metrics.TotalLoss += trade.PnL
	}
	e.metrics.LastTradeAt = time.Now()
	e.mu.Unlock()

	// 4. Send notifications for significant events
	if e.notificationMgr != nil {
		if trade.Status == "closed" || trade.Status == "stopped" {
			severity := notifications.SeverityInfo
			if trade.PnL < 0 {
				severity = notifications.SeverityWarning
			}
			e.notificationMgr.TriggerCustomNotification(
				fmt.Sprintf("Trade %s closed", trade.ID),
				fmt.Sprintf("Trade on %s closed with P&L: %.4f", trade.Pair, trade.PnL),
				severity,
				map[string]interface{}{
					"trade_id":    trade.ID,
					"pair":        trade.Pair,
					"pnl":         trade.PnL,
					"entry_price": trade.EntryPrice,
					"exit_price":  trade.ExitPrice,
				},
				"system",
			)
		}
	}
}

// ErrorSeverity represents the severity level of an error
type ErrorSeverity int

const (
	ErrorSeverityLow ErrorSeverity = iota
	ErrorSeverityMedium
	ErrorSeverityHigh
	ErrorSeverityCritical
)

// processError procesa un error with classification and appropriate action
func (e *ScalpingEngine) processError(err error) {
	if err == nil {
		return
	}

	// 1. Classify the error
	severity := e.classifyError(err)
	errStr := err.Error()

	e.logger.WithFields(logrus.Fields{
		"error":    errStr,
		"severity": severity,
	}).Error("Processing scalping engine error")

	// 2. Update error metrics
	e.mu.Lock()
	e.metrics.ErrorCount++
	e.metrics.LastErrorAt = time.Now()
	e.metrics.LastError = errStr
	e.mu.Unlock()

	// 3. Take action based on severity
	switch severity {
	case ErrorSeverityCritical:
		// Critical errors: stop engine, close positions, alert immediately
		e.logger.Error("CRITICAL ERROR - initiating emergency stop")
		if e.notificationMgr != nil {
			e.notificationMgr.TriggerCustomNotification(
				"Scalping Engine Critical Error",
				fmt.Sprintf("Critical error occurred: %s. Engine stopping.", errStr),
				notifications.SeverityCritical,
				map[string]interface{}{
					"error":       errStr,
					"action":      "engine_stop",
					"open_trades": len(e.openPositions),
				},
				"system",
			)
		}
		// Initiate graceful stop
		go func() {
			if stopErr := e.Stop(); stopErr != nil {
				e.logger.WithError(stopErr).Error("Failed to stop engine after critical error")
			}
		}()

	case ErrorSeverityHigh:
		// High severity: pause trading, alert
		e.logger.Warn("High severity error - pausing new trades")
		if e.notificationMgr != nil {
			e.notificationMgr.TriggerCustomNotification(
				"Scalping Engine High Severity Error",
				fmt.Sprintf("High severity error: %s", errStr),
				notifications.SeverityError,
				map[string]interface{}{"error": errStr},
				"system",
			)
		}

	case ErrorSeverityMedium:
		// Medium severity: log and continue, but track for pattern detection
		e.logger.Warn("Medium severity error - continuing with monitoring")

	case ErrorSeverityLow:
		// Low severity: log and continue
		e.logger.Info("Low severity error - logged and continuing")
	}
}

// classifyError determines the severity of an error
func (e *ScalpingEngine) classifyError(err error) ErrorSeverity {
	errStr := err.Error()

	// Critical errors - immediate action required
	criticalPatterns := []string{
		"authentication failed", "api key invalid", "insufficient funds",
		"account suspended", "connection lost", "panic",
	}
	for _, pattern := range criticalPatterns {
		if containsIgnoreCase(errStr, pattern) {
			return ErrorSeverityCritical
		}
	}

	// High severity - trading should pause
	highPatterns := []string{
		"order rejected", "rate limit exceeded", "exchange error",
		"timeout", "market closed", "invalid order",
	}
	for _, pattern := range highPatterns {
		if containsIgnoreCase(errStr, pattern) {
			return ErrorSeverityHigh
		}
	}

	// Medium severity - worth monitoring
	mediumPatterns := []string{
		"retry", "temporary", "partial", "delayed",
	}
	for _, pattern := range mediumPatterns {
		if containsIgnoreCase(errStr, pattern) {
			return ErrorSeverityMedium
		}
	}

	// Default to low severity
	return ErrorSeverityLow
}

// containsIgnoreCase checks if s contains substr (case-insensitive)
func containsIgnoreCase(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr ||
			len(substr) == 0 ||
			(len(s) > 0 && containsLower(toLower(s), toLower(substr))))
}

func toLower(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			result[i] = c + 32
		} else {
			result[i] = c
		}
	}
	return string(result)
}

func containsLower(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// updateStatusLocked actualiza el estado del motor.
// Naming convention: "Locked" means the caller must already hold e.mu
// (RLock is sufficient because this method only iterates maps and reads
// scalar fields without mutating shared state besides e.status).
func (e *ScalpingEngine) updateStatusLocked() {
	running := e.running.Load()
	e.status.Running = running
	e.status.StartedAt = e.startedAt
	if running {
		e.status.Uptime = time.Since(e.startedAt)
	}

	// Iterate maps directly (caller holds the lock).
	pairs := make([]string, 0, len(e.activePairs))
	for pair := range e.activePairs {
		pairs = append(pairs, pair)
	}
	e.status.ActivePairs = pairs

	strategies := make([]string, 0, len(e.activeStrategies))
	for strategy := range e.activeStrategies {
		strategies = append(strategies, strategy)
	}
	e.status.ActiveStrategies = strategies

	e.status.OpenPositions = len(e.openPositions)
	e.status.HealthStatus = e.determineHealthStatus()
}

// determineHealthStatus determines the health status based on various factors
func (e *ScalpingEngine) determineHealthStatus() string {
	// If not running, status is "stopped"
	if !e.running.Load() {
		return "stopped"
	}

	// Check for recent critical errors
	if e.metrics.LastErrorAt.After(time.Now().Add(-5 * time.Minute)) {
		if e.metrics.ErrorCount > 10 {
			return "unhealthy"
		}
		return "degraded"
	}

	// Check if GCT client is connected
	if e.gctClient != nil && !e.gctClient.IsConnected() {
		return "degraded"
	}

	// Check if we have active pairs but no strategies
	if len(e.activePairs) > 0 && len(e.activeStrategies) == 0 {
		return "degraded"
	}

	return "healthy"
}

// updateMetrics actualiza las métricas del motor
func (e *ScalpingEngine) updateMetrics() {
	e.metrics.LastUpdated = time.Now()
	e.metrics.ActivePairs = len(e.activePairs)
	e.metrics.ActiveStrategies = len(e.activeStrategies)

	// Calculate win rate
	if e.metrics.TotalTrades > 0 {
		e.metrics.WinRate = float64(e.metrics.WinningTrades) / float64(e.metrics.TotalTrades) * 100
	}

	// Calculate profit factor
	if e.metrics.TotalLoss != 0 {
		e.metrics.ProfitFactor = e.metrics.TotalProfit / -e.metrics.TotalLoss
	}

	// Calculate average trade
	if e.metrics.TotalTrades > 0 {
		e.metrics.AvgTrade = (e.metrics.TotalProfit + e.metrics.TotalLoss) / float64(e.metrics.TotalTrades)
	}
}

// checkHealth verifica la salud del sistema
func (e *ScalpingEngine) checkHealth() {
	healthStatus := "healthy"
	healthIssues := []string{}

	// 1. Check GCT connection
	if e.gctClient != nil {
		ctx, cancel := context.WithTimeout(e.ctx, 5*time.Second)
		if err := e.gctClient.HealthCheck(ctx); err != nil {
			healthIssues = append(healthIssues, "GCT connection failed")
			healthStatus = "degraded"
		}
		cancel()

		if !e.gctClient.IsConnected() {
			healthIssues = append(healthIssues, "GCT not connected")
			healthStatus = "degraded"
		}
	}

	// 2. Check recent error rate
	recentErrorThreshold := 10
	if e.metrics.ErrorCount > recentErrorThreshold {
		// Check if errors are recent (last 5 minutes)
		if e.metrics.LastErrorAt.After(time.Now().Add(-5 * time.Minute)) {
			healthIssues = append(healthIssues, fmt.Sprintf("high error rate: %d errors", e.metrics.ErrorCount))
			healthStatus = "unhealthy"
		}
	}

	// 3. Check trading performance
	if e.metrics.TotalTrades > 10 && e.metrics.WinRate < 30 {
		healthIssues = append(healthIssues, fmt.Sprintf("low win rate: %.1f%%", e.metrics.WinRate))
	}

	// 4. Check open positions risk
	if len(e.openPositions) > 10 {
		healthIssues = append(healthIssues, fmt.Sprintf("many open positions: %d", len(e.openPositions)))
	}

	// Update status with health check results
	e.mu.Lock()
	e.status.HealthStatus = healthStatus
	e.status.HealthIssues = healthIssues
	e.mu.Unlock()

	if len(healthIssues) > 0 {
		e.logger.WithFields(logrus.Fields{
			"status": healthStatus,
			"issues": healthIssues,
		}).Warn("Health check completed with issues")
	}
}

// getDefaultRiskParameters retorna parámetros de riesgo por defecto
func getDefaultRiskParameters() *models.RiskParameters {
	return &models.RiskParameters{
		MaxPositionSizeUSD:   100.0,
		MaxTotalExposureUSD:  500.0,
		StopLossPercent:      0.5,
		TakeProfitPercent:    1.0,
		MaxDailyLossUSD:      5.0,
		MaxConsecutiveLosses: 3,
		VolatilityMultiplier: 1.5,
		KellyFraction:        0.25,
		RiskFreeRate:         0.02,
		ConfidenceThreshold:  0.65,
	}
}

// marketAnalyzer analiza el mercado continuamente y genera señales
func (e *ScalpingEngine) marketAnalyzer() {
	// Intervalo de análisis (cada 5 segundos para scalping)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	e.logger.Info("Market analyzer started")

	for {
		select {
		case <-ticker.C:
			e.analyzeMarket()
		case <-e.stopChan:
			e.logger.Info("Market analyzer stopped")
			return
		case <-e.ctx.Done():
			e.logger.Info("Market analyzer stopped (context done)")
			return
		}
	}
}

// analyzeMarket analiza todos los pares activos
func (e *ScalpingEngine) analyzeMarket() {
	e.mu.RLock()
	pairs := make([]string, 0, len(e.activePairs))
	for pair := range e.activePairs {
		pairs = append(pairs, pair)
	}
	e.mu.RUnlock()

	if len(pairs) == 0 {
		return
	}

	// Analizar cada par
	for _, pairKey := range pairs {
		// Parse exchange:pair
		parts := make([]string, 2)
		for i, part := range []rune(pairKey) {
			if part == ':' {
				parts[0] = pairKey[:i]
				parts[1] = pairKey[i+1:]
				break
			}
		}

		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			continue
		}

		exchange := parts[0]
		pair := parts[1]

		// Analizar este par
		go e.analyzePair(exchange, pair)
	}
}

// analyzePair analiza un par específico y genera señales
func (e *ScalpingEngine) analyzePair(exchange, pair string) {
	ctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
	defer cancel()

	// Obtener datos de mercado
	marketData, err := e.getMarketData(ctx, exchange, pair)
	if err != nil {
		e.logger.Warnf("Failed to get market data for %s:%s: %v", exchange, pair, err)
		return
	}

	// Ejecutar análisis con estrategias reales
	// Por ahora, usar análisis simple basado en RSI y precio
	e.analyzeWithStrategies(ctx, exchange, pair, marketData)
}

// getMarketData obtiene datos de mercado para un par
func (e *ScalpingEngine) getMarketData(ctx context.Context, exchange, pair string) (*strategies.MarketData, error) {
	// Obtener ticker
	ticker, err := e.gctClient.GetTicker(ctx, exchange, pair)
	if err != nil {
		return nil, fmt.Errorf("failed to get ticker: %w", err)
	}

	// Crear market data básico
	marketData := &strategies.MarketData{
		Pair:      pair,
		Exchange:  exchange,
		Price:     ticker.Last,
		Volume:    ticker.Volume,
		Timestamp: time.Now(),
		Features: &models.Features{
			Prices:  []float64{ticker.Last},
			Volumes: []float64{ticker.Volume},
		},
	}

	return marketData, nil
}

// analyzeWithStrategies previously generated a deterministic random test
// signal every ~20s ("prueba en vivo") regardless of any real market
// analysis — with no real strategy logic at all, it fired real orders via
// processSignal purely on a timer (SCALP-02). There is no genuine strategy
// implementation to fall back to in this engine (real strategy analysis
// lives in the StrategyOrchestrator/BotManager path used by AITradingBot),
// so this is disabled by default and requires an explicit opt-in env var —
// it must never run unattended in a production deployment.
func (e *ScalpingEngine) analyzeWithStrategies(ctx context.Context, exchange, pair string, marketData *strategies.MarketData) {
	if os.Getenv("SCALPING_ALLOW_TEST_SIGNALS") != "true" {
		return
	}
	e.logger.Warn("⚠️ SCALPING_ALLOW_TEST_SIGNALS=true — legacy random test-signal generator is ACTIVE and can submit real orders")

	// Para prueba en vivo: generar señales cada 20 segundos
	// Esto asegura que veamos actividad en la prueba de 2-3 minutos

	if time.Now().Unix()%20 < 5 {
		// Alternar entre buy y sell basado en el tiempo
		direction := "buy"
		if time.Now().Unix()%40 < 20 {
			direction = "sell"
		}

		// Crear señal
		signal := &models.ScalpingSignal{
			ID:         fmt.Sprintf("live_%d", time.Now().UnixNano()),
			Pair:       pair,
			Exchange:   exchange,
			Direction:  direction,
			Confidence: 0.70, // Confidence alto para asegurar ejecución
			Strategies: []string{"live_test"},
			Price:      marketData.Price,
			Volume:     marketData.Volume,
			Timestamp:  time.Now(),
			Metadata: map[string]interface{}{
				"test_mode": "live",
				"price":     marketData.Price,
			},
		}

		e.logger.Infof("🎯 LIVE SIGNAL: %s %s at $%.2f (confidence: %.2f)",
			direction, pair, signal.Price, signal.Confidence)

		// Enviar señal al canal
		select {
		case e.signalChan <- signal:
			e.logger.Debug("Signal sent to execution channel")
		default:
			e.logger.Warn("Signal channel full, dropping signal")
		}
	}
}

// generateTestSignal genera una señal de prueba
func (e *ScalpingEngine) generateTestSignal(exchange, pair string, marketData *strategies.MarketData) {
	// Generar señal aleatoria para testing
	directions := []string{"buy", "sell"}
	direction := directions[time.Now().Unix()%2]

	signal := &models.ScalpingSignal{
		ID:         fmt.Sprintf("test_%d", time.Now().UnixNano()),
		Pair:       pair,
		Exchange:   exchange,
		Direction:  direction,
		Confidence: 0.7,
		Strategies: []string{"test_strategy"},
		Price:      marketData.Price,
		Volume:     marketData.Volume,
		Timestamp:  time.Now(),
		Metadata:   make(map[string]interface{}),
	}

	e.logger.Infof("Generated test signal: %s %s at %.8f (confidence: %.2f)",
		direction, pair, signal.Price, signal.Confidence)

	// Enviar señal al canal
	select {
	case e.signalChan <- signal:
		e.logger.Debug("Signal sent to channel")
	default:
		e.logger.Warn("Signal channel full, dropping signal")
	}
}
