package risk

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/scalping/models"
)

// AdaptiveRiskManager gestiona el riesgo de manera adaptativa
type AdaptiveRiskManager struct {
	mu        sync.RWMutex
	logger    *logrus.Logger
	gctClient gct.GCTClient
	params    *models.RiskParameters

	// Estado del riesgo
	currentExposure   float64
	dailyPnL          float64
	dailyLoss         float64
	consecutiveLosses int
	lastResetDate     time.Time

	// Historial de trades
	tradeHistory []*models.ScalpingTrade
	maxHistory   int

	// Volatilidad
	volatilityCache      map[string]float64
	lastVolatilityUpdate time.Time

	// Kill Switch
	killSwitchActive bool
	killSwitchReason string
	killSwitchTime   time.Time

	// Drawdown tracking
	weeklyPnL        float64
	monthlyPnL       float64
	lastWeeklyReset  time.Time
	lastMonthlyReset time.Time
	exposureBySymbol map[string]float64
}

// NewAdaptiveRiskManager crea un nuevo risk manager adaptativo
func NewAdaptiveRiskManager(logger *logrus.Logger, gctClient gct.GCTClient, params *models.RiskParameters) *AdaptiveRiskManager {
	if params == nil {
		params = getDefaultRiskParameters()
	}

	now := time.Now()
	return &AdaptiveRiskManager{
		logger:               logger,
		gctClient:            gctClient,
		params:               params,
		currentExposure:      0,
		dailyPnL:             0,
		dailyLoss:            0,
		consecutiveLosses:    0,
		lastResetDate:        now,
		tradeHistory:         make([]*models.ScalpingTrade, 0, 1000),
		maxHistory:           1000,
		volatilityCache:      make(map[string]float64),
		lastVolatilityUpdate: now,
		killSwitchActive:     false,
		killSwitchReason:     "",
		weeklyPnL:            0,
		monthlyPnL:           0,
		lastWeeklyReset:      now,
		lastMonthlyReset:     now,
		exposureBySymbol:     make(map[string]float64),
	}
}

// CalculatePositionSize calcula el tamaño de posición usando Kelly Criterion
func (rm *AdaptiveRiskManager) CalculatePositionSize(ctx context.Context, signal *models.ScalpingSignal) (float64, error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	// Verificar límites de riesgo primero
	if !rm.checkRiskLimits() {
		return 0, fmt.Errorf("risk limits exceeded")
	}

	// Obtener balance disponible; si falla (e.g. gRPC sin credenciales) usamos
	// MaxPositionSizeUSD como proxy para que el bot siga operando.
	balance, err := rm.getAvailableBalance(ctx, signal.Exchange)
	if err != nil {
		rm.logger.Warnf("balance fetch failed, using configured max as fallback: %v", err)
		balance = rm.params.MaxPositionSizeUSD
		if balance <= 0 {
			return 0, fmt.Errorf("failed to get balance and no fallback configured: %w", err)
		}
	}

	// Calcular tamaño base usando Kelly Criterion
	kellySize := rm.calculateKellySize(signal, balance)

	// Ajustar por volatilidad
	volatility := rm.getVolatility(signal.Pair)
	volatilityAdjusted := rm.adjustForVolatility(kellySize, volatility)

	// Ajustar por pérdidas consecutivas
	lossAdjusted := rm.adjustForConsecutiveLosses(volatilityAdjusted)

	// Aplicar límites máximos
	finalSize := rm.applyLimits(lossAdjusted, signal.Price, balance)

	rm.logger.Debugf("Position size for %s: kelly=%.2f, vol_adj=%.2f, loss_adj=%.2f, final=%.2f",
		signal.Pair, kellySize, volatilityAdjusted, lossAdjusted, finalSize)

	// Last-resort guard: never let a NaN/Inf position size escape this function,
	// regardless of which upstream calculation produced it (defense in depth —
	// calculateKellySize already rejects the StopLossPercent<=0 root cause above).
	if math.IsNaN(finalSize) || math.IsInf(finalSize, 0) {
		rm.logger.Errorf("Position size for %s computed as invalid value (%.4f) — rejecting order", signal.Pair, finalSize)
		return 0, fmt.Errorf("computed position size is invalid (NaN/Inf) for %s", signal.Pair)
	}

	return finalSize, nil
}

// CalculateStopLoss calcula el stop loss dinámico basado en ATR
func (rm *AdaptiveRiskManager) CalculateStopLoss(ctx context.Context, pair string, entryPrice float64, side string) (float64, error) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	// Obtener ATR (Average True Range)
	atr := rm.getATR(pair)
	if atr == 0 {
		// Si no hay ATR, usar porcentaje fijo
		atr = entryPrice * (rm.params.StopLossPercent / 100)
	}

	// Calcular stop loss basado en ATR
	var stopLoss float64
	multiplier := rm.params.VolatilityMultiplier

	if side == "buy" {
		stopLoss = entryPrice - (atr * multiplier)
	} else {
		stopLoss = entryPrice + (atr * multiplier)
	}

	// Asegurar que el stop loss no sea demasiado ajustado
	minStopLoss := entryPrice * (rm.params.StopLossPercent / 100)
	if side == "buy" {
		if entryPrice-stopLoss < minStopLoss {
			stopLoss = entryPrice - minStopLoss
		}
	} else {
		if stopLoss-entryPrice < minStopLoss {
			stopLoss = entryPrice + minStopLoss
		}
	}

	rm.logger.Debugf("Stop loss for %s at %.8f: %.8f (ATR: %.8f, multiplier: %.2f)",
		pair, entryPrice, stopLoss, atr, multiplier)

	return stopLoss, nil
}

// CalculateTakeProfit calcula el take profit
func (rm *AdaptiveRiskManager) CalculateTakeProfit(entryPrice float64, side string) float64 {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	var takeProfit float64
	profitPercent := rm.params.TakeProfitPercent / 100

	if side == "buy" {
		takeProfit = entryPrice * (1 + profitPercent)
	} else {
		takeProfit = entryPrice * (1 - profitPercent)
	}

	return takeProfit
}

// CheckRiskLimits verifica si se pueden abrir nuevas posiciones
func (rm *AdaptiveRiskManager) CheckRiskLimits() (bool, string) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	return rm.checkRiskLimits(), rm.getRiskLimitReason()
}

// checkRiskLimits verifica límites de riesgo (sin lock)
func (rm *AdaptiveRiskManager) checkRiskLimits() bool {
	// Reset diario
	rm.checkDailyReset()

	// Verificar kill switch
	if rm.killSwitchActive {
		return false
	}

	// Verificar pérdida diaria máxima
	if rm.dailyLoss >= rm.params.MaxDailyLossUSD {
		return false
	}

	// Verificar exposición total
	if rm.currentExposure >= rm.params.MaxTotalExposureUSD {
		return false
	}

	// Verificar pérdidas consecutivas
	if rm.consecutiveLosses >= rm.params.MaxConsecutiveLosses {
		return false
	}

	return true
}

// getRiskLimitReason obtiene la razón del límite de riesgo
func (rm *AdaptiveRiskManager) getRiskLimitReason() string {
	rm.checkDailyReset()

	if rm.killSwitchActive {
		return fmt.Sprintf("kill switch active: %s", rm.killSwitchReason)
	}

	if rm.dailyLoss >= rm.params.MaxDailyLossUSD {
		return fmt.Sprintf("daily loss limit reached: $%.2f / $%.2f", rm.dailyLoss, rm.params.MaxDailyLossUSD)
	}

	if rm.currentExposure >= rm.params.MaxTotalExposureUSD {
		return fmt.Sprintf("total exposure limit reached: $%.2f / $%.2f", rm.currentExposure, rm.params.MaxTotalExposureUSD)
	}

	if rm.consecutiveLosses >= rm.params.MaxConsecutiveLosses {
		return fmt.Sprintf("consecutive losses limit reached: %d / %d", rm.consecutiveLosses, rm.params.MaxConsecutiveLosses)
	}

	return "all limits ok"
}

// UpdatePerformance actualiza el rendimiento después de un trade
func (rm *AdaptiveRiskManager) UpdatePerformance(trade *models.ScalpingTrade) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	// Agregar al historial
	rm.tradeHistory = append(rm.tradeHistory, trade)
	if len(rm.tradeHistory) > rm.maxHistory {
		rm.tradeHistory = rm.tradeHistory[1:]
	}

	// Actualizar PnL diario
	rm.dailyPnL += trade.PnL
	rm.weeklyPnL += trade.PnL
	rm.monthlyPnL += trade.PnL

	// Actualizar pérdidas consecutivas
	if trade.PnL < 0 {
		rm.consecutiveLosses++
		rm.dailyLoss += math.Abs(trade.PnL)
	} else {
		rm.consecutiveLosses = 0
	}

	// Actualizar exposición
	rm.updateExposure()

	// Actualizar exposición por símbolo
	if trade.Status == "open" {
		rm.exposureBySymbol[trade.Pair] = trade.EntryPrice * trade.Amount
	} else {
		delete(rm.exposureBySymbol, trade.Pair)
	}

	rm.logger.Infof("Performance updated: PnL=%.2f, daily_loss=%.2f, consecutive_losses=%d, exposure=%.2f",
		trade.PnL, rm.dailyLoss, rm.consecutiveLosses, rm.currentExposure)
}

// calculateKellySize calcula el tamaño usando Kelly Criterion
func (rm *AdaptiveRiskManager) calculateKellySize(signal *models.ScalpingSignal, balance float64) float64 {
	// Kelly Criterion: f = (bp - q) / b
	// donde:
	// f = fracción del capital a arriesgar
	// b = odds (ratio ganancia/pérdida)
	// p = probabilidad de ganar (confidence)
	// q = probabilidad de perder (1 - p)

	// StopLossPercent must be strictly positive — a zero or negative value makes
	// b = +Inf/NaN below, which then survives every subsequent <0/>1 comparison
	// (IEEE-754 comparisons against NaN are always false) all the way to the
	// final order Amount. Reject the position instead of propagating garbage.
	if rm.params.StopLossPercent <= 0 {
		rm.logger.Warnf("calculateKellySize: invalid StopLossPercent=%.4f for %s, refusing to size position",
			rm.params.StopLossPercent, signal.Pair)
		return 0
	}

	p := signal.Confidence
	q := 1 - p
	b := rm.params.TakeProfitPercent / rm.params.StopLossPercent // Ratio ganancia/pérdida

	kellyFraction := (b*p - q) / b

	// Aplicar Kelly fraccional (más conservador)
	kellyFraction *= rm.params.KellyFraction

	// Limitar entre 0 y 1
	if kellyFraction < 0 {
		kellyFraction = 0
	}
	if kellyFraction > 1 {
		kellyFraction = 1
	}

	// Calcular tamaño en USD
	sizeUSD := balance * kellyFraction

	return sizeUSD
}

// adjustForVolatility ajusta el tamaño por volatilidad
func (rm *AdaptiveRiskManager) adjustForVolatility(size float64, volatility float64) float64 {
	if volatility == 0 {
		return size
	}

	// Reducir tamaño si volatilidad es alta
	// Volatilidad normal: ~2-5%
	// Volatilidad alta: >5%
	normalVolatility := 0.03 // 3%

	if volatility > normalVolatility*1.5 {
		// Volatilidad alta: reducir 50%
		return size * 0.5
	} else if volatility > normalVolatility {
		// Volatilidad moderada: reducir proporcionalmente
		factor := normalVolatility / volatility
		return size * factor
	}

	return size
}

// adjustForConsecutiveLosses ajusta el tamaño por pérdidas consecutivas
func (rm *AdaptiveRiskManager) adjustForConsecutiveLosses(size float64) float64 {
	if rm.consecutiveLosses == 0 {
		return size
	}

	// Reducir 20% por cada pérdida consecutiva
	reductionFactor := math.Pow(0.8, float64(rm.consecutiveLosses))

	return size * reductionFactor
}

// applyLimits aplica límites máximos al tamaño
func (rm *AdaptiveRiskManager) applyLimits(size float64, price float64, balance float64) float64 {
	// Límite por posición
	if size > rm.params.MaxPositionSizeUSD {
		size = rm.params.MaxPositionSizeUSD
	}

	// Límite por exposición total disponible
	availableExposure := rm.params.MaxTotalExposureUSD - rm.currentExposure
	if size > availableExposure {
		size = availableExposure
	}

	// Límite por balance disponible (no usar más del 20% del balance en una posición)
	maxFromBalance := balance * 0.2
	if size > maxFromBalance {
		size = maxFromBalance
	}

	// Asegurar que sea positivo
	if size < 0 {
		size = 0
	}

	return size
}

// getAvailableBalance obtiene el balance disponible
func (rm *AdaptiveRiskManager) getAvailableBalance(ctx context.Context, exchange string) (float64, error) {
	accountInfo, err := rm.gctClient.GetAccountInfo(ctx, exchange)
	if err != nil {
		return 0, fmt.Errorf("failed to get account info: %w", err)
	}

	// Sumar balance disponible de USDT/USD
	var totalBalance float64
	for _, account := range accountInfo.Accounts {
		for currency, balance := range account.Balances {
			if currency == "USDT" || currency == "USD" || currency == "USDC" {
				totalBalance += balance.Available
			}
		}
	}

	return totalBalance, nil
}

// getVolatility obtiene la volatilidad de un par
func (rm *AdaptiveRiskManager) getVolatility(pair string) float64 {
	// Usar cache si está disponible y es reciente
	if vol, exists := rm.volatilityCache[pair]; exists {
		if time.Since(rm.lastVolatilityUpdate) < 5*time.Minute {
			return vol
		}
	}

	// Calcular volatilidad desde historial de trades
	if len(rm.tradeHistory) < 10 {
		return 0.03 // Volatilidad por defecto 3%
	}

	// Calcular desviación estándar de returns
	var returns []float64
	for i := 1; i < len(rm.tradeHistory); i++ {
		if rm.tradeHistory[i].Pair == pair && rm.tradeHistory[i-1].Pair == pair {
			ret := (rm.tradeHistory[i].ExitPrice - rm.tradeHistory[i-1].ExitPrice) / rm.tradeHistory[i-1].ExitPrice
			returns = append(returns, ret)
		}
	}

	if len(returns) < 5 {
		return 0.03
	}

	volatility := calculateStdDev(returns)
	rm.volatilityCache[pair] = volatility
	rm.lastVolatilityUpdate = time.Now()

	return volatility
}

// getATR obtiene el ATR de un par
func (rm *AdaptiveRiskManager) getATR(pair string) float64 {
	// Calcular ATR desde historial de trades
	if len(rm.tradeHistory) < 10 {
		return 0
	}

	var ranges []float64
	for _, trade := range rm.tradeHistory {
		if trade.Pair == pair {
			trueRange := math.Abs(trade.ExitPrice - trade.EntryPrice)
			ranges = append(ranges, trueRange)
		}
	}

	if len(ranges) < 5 {
		return 0
	}

	// Promedio de los últimos 14 ranges
	n := 14
	if len(ranges) < n {
		n = len(ranges)
	}

	var sum float64
	for i := len(ranges) - n; i < len(ranges); i++ {
		sum += ranges[i]
	}

	return sum / float64(n)
}

// updateExposure actualiza la exposición actual
func (rm *AdaptiveRiskManager) updateExposure() {
	// Calcular exposición desde trades abiertos
	var exposure float64
	for _, trade := range rm.tradeHistory {
		if trade.Status == "open" {
			exposure += trade.EntryPrice * trade.Amount
		}
	}
	rm.currentExposure = exposure
}

// checkDailyReset verifica si es necesario resetear contadores diarios
func (rm *AdaptiveRiskManager) checkDailyReset() {
	now := time.Now()
	if now.Day() != rm.lastResetDate.Day() || now.Month() != rm.lastResetDate.Month() {
		rm.dailyPnL = 0
		rm.dailyLoss = 0
		rm.lastResetDate = now
		rm.logger.Info("Daily risk counters reset")
	}
}

// GetRiskParameters retorna los parámetros de riesgo actuales
func (rm *AdaptiveRiskManager) GetRiskParameters() *models.RiskParameters {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	// Retornar copia
	return &models.RiskParameters{
		MaxPositionSizeUSD:   rm.params.MaxPositionSizeUSD,
		MaxTotalExposureUSD:  rm.params.MaxTotalExposureUSD,
		StopLossPercent:      rm.params.StopLossPercent,
		TakeProfitPercent:    rm.params.TakeProfitPercent,
		MaxDailyLossUSD:      rm.params.MaxDailyLossUSD,
		MaxConsecutiveLosses: rm.params.MaxConsecutiveLosses,
		VolatilityMultiplier: rm.params.VolatilityMultiplier,
		KellyFraction:        rm.params.KellyFraction,
		RiskFreeRate:         rm.params.RiskFreeRate,
		ConfidenceThreshold:  rm.params.ConfidenceThreshold,
	}
}

// UpdateRiskParameters actualiza los parámetros de riesgo
func (rm *AdaptiveRiskManager) UpdateRiskParameters(params *models.RiskParameters) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.params = params
	rm.logger.Info("Risk parameters updated")
}

// GetCurrentExposure retorna la exposición actual
func (rm *AdaptiveRiskManager) GetCurrentExposure() float64 {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.currentExposure
}

// GetDailyPnL retorna el PnL diario
func (rm *AdaptiveRiskManager) GetDailyPnL() float64 {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	rm.checkDailyReset()
	return rm.dailyPnL
}

// GetConsecutiveLosses retorna el número de pérdidas consecutivas
func (rm *AdaptiveRiskManager) GetConsecutiveLosses() int {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.consecutiveLosses
}

// ActivateKillSwitch activa el kill switch de emergencia
func (rm *AdaptiveRiskManager) ActivateKillSwitch(reason string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.killSwitchActive = true
	rm.killSwitchReason = reason
	rm.killSwitchTime = time.Now()

	rm.logger.WithFields(logrus.Fields{
		"reason": reason,
		"time":   rm.killSwitchTime,
	}).Warn("🚨 KILL SWITCH ACTIVATED - All trading stopped")
}

// DeactivateKillSwitch desactiva el kill switch
func (rm *AdaptiveRiskManager) DeactivateKillSwitch() {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.killSwitchActive = false
	rm.killSwitchReason = ""

	rm.logger.Info("✅ Kill switch deactivated - Trading resumed")
}

// IsKillSwitchActive retorna si el kill switch está activo
func (rm *AdaptiveRiskManager) IsKillSwitchActive() bool {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.killSwitchActive
}

// GetKillSwitchInfo retorna información del kill switch
func (rm *AdaptiveRiskManager) GetKillSwitchInfo() (bool, string, time.Time) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.killSwitchActive, rm.killSwitchReason, rm.killSwitchTime
}

// GetDrawdownMetrics retorna métricas de drawdown
func (rm *AdaptiveRiskManager) GetDrawdownMetrics() map[string]interface{} {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	rm.checkPeriodicResets()

	// Calcular drawdown percentages
	dailyDrawdownPct := 0.0
	if rm.params.MaxDailyLossUSD > 0 {
		dailyDrawdownPct = (rm.dailyLoss / rm.params.MaxDailyLossUSD) * 100
	}

	weeklyDrawdownPct := 0.0
	weeklyLoss := math.Abs(math.Min(rm.weeklyPnL, 0))
	maxWeeklyLoss := rm.params.MaxDailyLossUSD * 5 // Aproximación
	if maxWeeklyLoss > 0 {
		weeklyDrawdownPct = (weeklyLoss / maxWeeklyLoss) * 100
	}

	monthlyDrawdownPct := 0.0
	monthlyLoss := math.Abs(math.Min(rm.monthlyPnL, 0))
	maxMonthlyLoss := rm.params.MaxDailyLossUSD * 20 // Aproximación
	if maxMonthlyLoss > 0 {
		monthlyDrawdownPct = (monthlyLoss / maxMonthlyLoss) * 100
	}

	return map[string]interface{}{
		"current_drawdown_daily":   dailyDrawdownPct,
		"current_drawdown_weekly":  weeklyDrawdownPct,
		"current_drawdown_monthly": monthlyDrawdownPct,
		"daily_pnl":                rm.dailyPnL,
		"weekly_pnl":               rm.weeklyPnL,
		"monthly_pnl":              rm.monthlyPnL,
		"daily_loss":               rm.dailyLoss,
		"max_daily_loss":           rm.params.MaxDailyLossUSD,
	}
}

// GetExposureBySymbol retorna la exposición por símbolo
func (rm *AdaptiveRiskManager) GetExposureBySymbol() map[string]float64 {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	// Crear copia del mapa
	exposure := make(map[string]float64)
	for symbol, value := range rm.exposureBySymbol {
		exposure[symbol] = value
	}

	return exposure
}

// checkPeriodicResets verifica resets semanales y mensuales
func (rm *AdaptiveRiskManager) checkPeriodicResets() {
	now := time.Now()

	// Reset semanal (cada lunes)
	if now.Weekday() == time.Monday && now.Day() != rm.lastWeeklyReset.Day() {
		rm.weeklyPnL = 0
		rm.lastWeeklyReset = now
		rm.logger.Info("Weekly risk counters reset")
	}

	// Reset mensual
	if now.Month() != rm.lastMonthlyReset.Month() {
		rm.monthlyPnL = 0
		rm.lastMonthlyReset = now
		rm.logger.Info("Monthly risk counters reset")
	}
}

// Helper functions

func calculateStdDev(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	// Calcular media
	var sum float64
	for _, v := range values {
		sum += v
	}
	mean := sum / float64(len(values))

	// Calcular varianza
	var variance float64
	for _, v := range values {
		diff := v - mean
		variance += diff * diff
	}
	variance /= float64(len(values))

	return math.Sqrt(variance)
}

func getDefaultRiskParameters() *models.RiskParameters {
	return &models.RiskParameters{
		MaxPositionSizeUSD:   100.0,
		MaxTotalExposureUSD:  500.0,
		StopLossPercent:      0.5,
		TakeProfitPercent:    1.0,
		MaxDailyLossUSD:      50.0,
		MaxConsecutiveLosses: 3,
		VolatilityMultiplier: 1.5,
		KellyFraction:        0.25,
		RiskFreeRate:         0.02,
		ConfidenceThreshold:  0.65,
	}
}
