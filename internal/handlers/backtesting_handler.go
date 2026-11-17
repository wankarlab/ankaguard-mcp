package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// MCPClient define la interfaz para llamar herramientas MCP
type MCPClient interface {
	CallTool(ctx context.Context, toolName string, params map[string]interface{}) (map[string]interface{}, error)
}

// BacktestingHandler maneja las solicitudes de backtesting consolidado
type BacktestingHandler struct {
	mcpClient MCPClient
	logger    *logrus.Logger
}

// NewBacktestingHandler crea una nueva instancia del handler
func NewBacktestingHandler(mcpClient MCPClient, logger *logrus.Logger) *BacktestingHandler {
	return &BacktestingHandler{
		mcpClient: mcpClient,
		logger:    logger,
	}
}

// BacktestRequest representa la solicitud de backtesting
type BacktestRequest struct {
	Symbol     string                 `json:"symbol"`
	Exchange   string                 `json:"exchange"`
	Strategy   string                 `json:"strategy"`
	StartDate  string                 `json:"start_date"`
	EndDate    string                 `json:"end_date"`
	Parameters map[string]interface{} `json:"parameters"`
	Optimize   bool                   `json:"optimize"`
	Language   string                 `json:"language"`
}

// BacktestResponse representa la respuesta consolidada de backtesting
type BacktestResponse struct {
	BacktestID      string                  `json:"backtest_id"`
	Symbol          string                  `json:"symbol"`
	Strategy        string                  `json:"strategy"`
	Period          BacktestPeriod          `json:"period"`
	Performance     BacktestPerformance     `json:"performance"`
	Trades          []BacktestTrade         `json:"trades"`
	FormattedReport BacktestFormattedReport `json:"formatted_report"`
	Metadata        BacktestMetadata        `json:"metadata"`
}

type BacktestPeriod struct {
	StartDate    string `json:"start_date"`
	EndDate      string `json:"end_date"`
	DurationDays int    `json:"duration_days"`
}

type BacktestPerformance struct {
	ROI              float64 `json:"roi"`
	SharpeRatio      float64 `json:"sharpe_ratio"`
	MaxDrawdown      float64 `json:"max_drawdown"`
	TotalTrades      int     `json:"total_trades"`
	WinningTrades    int     `json:"winning_trades"`
	LosingTrades     int     `json:"losing_trades"`
	WinRate          float64 `json:"win_rate"`
	ProfitFactor     float64 `json:"profit_factor"`
	AvgTradeDuration string  `json:"avg_trade_duration"`
}

type BacktestTrade struct {
	ID         int     `json:"id"`
	Type       string  `json:"type"`
	EntryTime  string  `json:"entry_time"`
	EntryPrice float64 `json:"entry_price"`
	ExitTime   string  `json:"exit_time"`
	ExitPrice  float64 `json:"exit_price"`
	PnL        float64 `json:"pnl"`
	PnLPercent float64 `json:"pnl_percent"`
	Duration   string  `json:"duration"`
}

type BacktestFormattedReport struct {
	Summary          string `json:"summary"`
	DetailedAnalysis string `json:"detailed_analysis"`
	Recommendations  string `json:"recommendations"`
}

type BacktestMetadata struct {
	ExecutionTimeMs int    `json:"execution_time_ms"`
	GeneratedAt     string `json:"generated_at"`
	Language        string `json:"language"`
	DataPoints      int    `json:"data_points"`
	// Warnings lists requested features that could not be honoured.
	Warnings []string `json:"warnings,omitempty"`
}

// RunBacktest maneja la solicitud de backtesting consolidado
func (h *BacktestingHandler) RunBacktest(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	h.logger.Info("Iniciando backtesting consolidado")

	// Parsear solicitud
	var req BacktestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.WithError(err).Error("Error al parsear solicitud de backtesting")
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Valores por defecto
	if req.Exchange == "" {
		req.Exchange = "kucoin"
	}
	if req.Strategy == "" {
		req.Strategy = "rsi_macd"
	}
	if req.Language == "" {
		req.Language = "es"
	}

	h.logger.WithFields(logrus.Fields{
		"symbol":   req.Symbol,
		"strategy": req.Strategy,
		"period":   fmt.Sprintf("%s to %s", req.StartDate, req.EndDate),
	}).Info("Ejecutando backtesting")

	ctx := r.Context()

	// 1. Ejecutar backtesting principal
	backtestResult, err := h.runBacktestStrategy(ctx, req)
	if err != nil {
		h.logger.WithError(err).Error("Error en backtesting principal")
		http.Error(w, fmt.Sprintf("Backtest failed: %v", err), http.StatusInternalServerError)
		return
	}

	// The tools get_backtest_metrics and optimize_parameters were retired
	// (they returned invented data), so they are not called any more. A
	// request for optimisation is answered honestly with a warning.
	var warnings []string
	if req.Optimize {
		warnings = append(warnings, "parameter optimization is not available: the optimize_parameters tool was retired")
	}

	// 4. Construir respuesta consolidada
	response := h.buildBacktestResponse(req, backtestResult, warnings, startTime)

	// 5. Enviar respuesta
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.WithError(err).Error("Error al enviar respuesta")
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}

	h.logger.WithFields(logrus.Fields{
		"execution_time_ms": time.Since(startTime).Milliseconds(),
		"roi":               response.Performance.ROI,
		"total_trades":      response.Performance.TotalTrades,
	}).Info("Backtesting completado exitosamente")
}

// runBacktestStrategy ejecuta el backtesting principal usando MCP
func (h *BacktestingHandler) runBacktestStrategy(ctx context.Context, req BacktestRequest) (map[string]interface{}, error) {
	params := map[string]interface{}{
		"exchange":   req.Exchange,
		"pair":       req.Symbol,
		"strategy":   req.Strategy,
		"start_date": req.StartDate,
		"end_date":   req.EndDate,
	}

	if req.Parameters != nil {
		params["parameters"] = req.Parameters
	}

	return h.mcpClient.CallTool(ctx, "backtest_strategy", params)
}

// buildBacktestResponse construye la respuesta consolidada
func (h *BacktestingHandler) buildBacktestResponse(
	req BacktestRequest,
	backtestResult map[string]interface{},
	warnings []string,
	startTime time.Time,
) BacktestResponse {

	// Extraer datos del resultado del backtesting
	roi := getFloatValue(backtestResult, "roi", 0.0)
	sharpeRatio := getFloatValue(backtestResult, "sharpe_ratio", 0.0)
	maxDrawdown := getFloatValue(backtestResult, "max_drawdown", 0.0)
	totalTrades := getIntValue(backtestResult, "total_trades", 0)
	winningTrades := getIntValue(backtestResult, "winning_trades", 0)
	losingTrades := getIntValue(backtestResult, "losing_trades", 0)

	// Calcular métricas derivadas
	winRate := 0.0
	if totalTrades > 0 {
		winRate = float64(winningTrades) / float64(totalTrades) * 100
	}

	trades := h.buildTradesList(backtestResult)
	// Derived only from real per-trade data; 0 / "" mean "not available".
	profitFactor := profitFactorFromTrades(trades)
	avgDuration := averageDuration(trades)

	// Calcular duración del período
	durationDays := h.calculateDurationDays(req.StartDate, req.EndDate)

	// Construir respuesta
	response := BacktestResponse{
		BacktestID: fmt.Sprintf("BT_%d", time.Now().Unix()),
		Symbol:     req.Symbol,
		Strategy:   req.Strategy,
		Period: BacktestPeriod{
			StartDate:    req.StartDate,
			EndDate:      req.EndDate,
			DurationDays: durationDays,
		},
		Performance: BacktestPerformance{
			ROI:              roi,
			SharpeRatio:      sharpeRatio,
			MaxDrawdown:      maxDrawdown,
			TotalTrades:      totalTrades,
			WinningTrades:    winningTrades,
			LosingTrades:     losingTrades,
			WinRate:          winRate,
			ProfitFactor:     profitFactor,
			AvgTradeDuration: avgDuration,
		},
		Trades:          trades,
		FormattedReport: h.buildFormattedReport(req, roi, sharpeRatio, maxDrawdown, winRate, profitFactor, avgDuration, totalTrades),
		Metadata: BacktestMetadata{
			ExecutionTimeMs: int(time.Since(startTime).Milliseconds()),
			GeneratedAt:     time.Now().Format(time.RFC3339),
			Language:        req.Language,
			DataPoints:      getIntValue(backtestResult, "data_points", 0),
			Warnings:        warnings,
		},
	}

	return response
}

// buildTradesList construye la lista de trades del resultado
func (h *BacktestingHandler) buildTradesList(backtestResult map[string]interface{}) []BacktestTrade {
	trades := []BacktestTrade{}

	if tradesData, ok := backtestResult["trades"].([]interface{}); ok {
		for i, tradeData := range tradesData {
			if tradeMap, ok := tradeData.(map[string]interface{}); ok {
				trade := BacktestTrade{
					ID:         i + 1,
					Type:       getStringValue(tradeMap, "type", "BUY"),
					EntryTime:  getStringValue(tradeMap, "entry_time", ""),
					EntryPrice: getFloatValue(tradeMap, "entry_price", 0.0),
					ExitTime:   getStringValue(tradeMap, "exit_time", ""),
					ExitPrice:  getFloatValue(tradeMap, "exit_price", 0.0),
					PnL:        getFloatValue(tradeMap, "pnl", 0.0),
					PnLPercent: getFloatValue(tradeMap, "pnl_percent", 0.0),
					Duration:   getStringValue(tradeMap, "duration", ""),
				}
				trades = append(trades, trade)
			}
		}
	}

	return trades
}

// buildFormattedReport construye el reporte formateado según el idioma
func (h *BacktestingHandler) buildFormattedReport(
	req BacktestRequest,
	roi, sharpeRatio, maxDrawdown, winRate, profitFactor float64,
	avgDuration string,
	totalTrades int,
) BacktestFormattedReport {

	if req.Language == "en" {
		return h.buildEnglishReport(req, roi, sharpeRatio, maxDrawdown, winRate, profitFactor, avgDuration, totalTrades)
	}

	// Español por defecto
	return h.buildSpanishReport(req, roi, sharpeRatio, maxDrawdown, winRate, profitFactor, avgDuration, totalTrades)
}

// buildSpanishReport construye el reporte en español
func (h *BacktestingHandler) buildSpanishReport(
	req BacktestRequest,
	roi, sharpeRatio, maxDrawdown, winRate, profitFactor float64,
	avgDuration string,
	totalTrades int,
) BacktestFormattedReport {

	summary := fmt.Sprintf(`📊 **RESULTADOS DEL BACKTESTING**

✅ **Estrategia**: %s
💰 **ROI**: %.1f%%
📈 **Sharpe Ratio**: %.2f
📉 **Max Drawdown**: %.1f%%
🎯 **Win Rate**: %.1f%%
📋 **Total Trades**: %d
📅 **Período**: %s - %s`,
		strings.ToUpper(req.Strategy),
		roi,
		sharpeRatio,
		maxDrawdown,
		winRate,
		totalTrades,
		req.StartDate,
		req.EndDate)

	detailedAnalysis := fmt.Sprintf(`🔍 **ANÁLISIS DETALLADO**

📊 **Rendimiento**:
  • ROI Total: %.1f%%
  • Sharpe Ratio: %.2f (Excelente > 1.0)
  • Máximo Drawdown: %.1f%%
  • Factor de Beneficio: %.2f

📈 **Estadísticas de Trading**:
  • Total de Operaciones: %d
  • Operaciones Ganadoras: %d
  • Operaciones Perdedoras: %d
  • Tasa de Acierto: %.1f%%

⏱️ **Duración Promedio**: %s por operación

🎯 **Evaluación de la Estrategia**:
%s`,
		roi,
		sharpeRatio,
		maxDrawdown,
		profitFactor,
		totalTrades,
		int(float64(totalTrades)*winRate/100),
		totalTrades-int(float64(totalTrades)*winRate/100),
		winRate,
		durationOrUnknown(avgDuration, "es"),
		h.getStrategyEvaluation(roi, sharpeRatio, winRate))

	recommendations := h.getSpanishRecommendations(roi, sharpeRatio, maxDrawdown, winRate)

	return BacktestFormattedReport{
		Summary:          summary,
		DetailedAnalysis: detailedAnalysis,
		Recommendations:  recommendations,
	}
}

// buildEnglishReport construye el reporte en inglés
func (h *BacktestingHandler) buildEnglishReport(
	req BacktestRequest,
	roi, sharpeRatio, maxDrawdown, winRate, profitFactor float64,
	avgDuration string,
	totalTrades int,
) BacktestFormattedReport {

	summary := fmt.Sprintf(`📊 **BACKTESTING RESULTS**

✅ **Strategy**: %s
💰 **ROI**: %.1f%%
📈 **Sharpe Ratio**: %.2f
📉 **Max Drawdown**: %.1f%%
🎯 **Win Rate**: %.1f%%
📋 **Total Trades**: %d
📅 **Period**: %s - %s`,
		strings.ToUpper(req.Strategy),
		roi,
		sharpeRatio,
		maxDrawdown,
		winRate,
		totalTrades,
		req.StartDate,
		req.EndDate)

	detailedAnalysis := fmt.Sprintf(`🔍 **DETAILED ANALYSIS**

📊 **Performance**:
  • Total ROI: %.1f%%
  • Sharpe Ratio: %.2f (Excellent > 1.0)
  • Maximum Drawdown: %.1f%%
  • Profit Factor: %.2f

📈 **Trading Statistics**:
  • Total Trades: %d
  • Winning Trades: %d
  • Losing Trades: %d
  • Win Rate: %.1f%%

⏱️ **Average Duration**: %s per trade

🎯 **Strategy Evaluation**:
%s`,
		roi,
		sharpeRatio,
		maxDrawdown,
		profitFactor,
		totalTrades,
		int(float64(totalTrades)*winRate/100),
		totalTrades-int(float64(totalTrades)*winRate/100),
		winRate,
		durationOrUnknown(avgDuration, "en"),
		h.getStrategyEvaluationEnglish(roi, sharpeRatio, winRate))

	recommendations := h.getEnglishRecommendations(roi, sharpeRatio, maxDrawdown, winRate)

	return BacktestFormattedReport{
		Summary:          summary,
		DetailedAnalysis: detailedAnalysis,
		Recommendations:  recommendations,
	}
}

// getStrategyEvaluation evalúa la estrategia en español
func (h *BacktestingHandler) getStrategyEvaluation(roi, sharpeRatio, winRate float64) string {
	if roi > 10 && sharpeRatio > 1.5 && winRate > 60 {
		return "🟢 **EXCELENTE**: Estrategia muy prometedora con alto rendimiento y buen control de riesgo."
	} else if roi > 5 && sharpeRatio > 1.0 && winRate > 50 {
		return "🟡 **BUENA**: Estrategia sólida con potencial de mejora en algunos aspectos."
	} else {
		return "🔴 **REQUIERE OPTIMIZACIÓN**: La estrategia necesita ajustes para mejorar su rendimiento."
	}
}

// getStrategyEvaluationEnglish evalúa la estrategia en inglés
func (h *BacktestingHandler) getStrategyEvaluationEnglish(roi, sharpeRatio, winRate float64) string {
	if roi > 10 && sharpeRatio > 1.5 && winRate > 60 {
		return "🟢 **EXCELLENT**: Very promising strategy with high performance and good risk control."
	} else if roi > 5 && sharpeRatio > 1.0 && winRate > 50 {
		return "🟡 **GOOD**: Solid strategy with potential for improvement in some aspects."
	} else {
		return "🔴 **NEEDS OPTIMIZATION**: Strategy requires adjustments to improve performance."
	}
}

// getSpanishRecommendations genera recomendaciones en español
func (h *BacktestingHandler) getSpanishRecommendations(roi, sharpeRatio, maxDrawdown, winRate float64) string {
	recommendations := []string{
		"🚀 **RECOMENDACIONES**",
		"",
	}

	if roi > 15 {
		recommendations = append(recommendations, "✅ **ROI Excelente**: Mantener la estrategia actual")
	} else if roi > 5 {
		recommendations = append(recommendations, "🔧 **ROI Moderado**: Considerar optimización de parámetros")
	} else {
		recommendations = append(recommendations, "⚠️ **ROI Bajo**: Revisar completamente la estrategia")
	}

	if sharpeRatio > 1.5 {
		recommendations = append(recommendations, "✅ **Excelente relación riesgo/retorno**")
	} else if sharpeRatio > 1.0 {
		recommendations = append(recommendations, "🔧 **Mejorar gestión de riesgo**")
	} else {
		recommendations = append(recommendations, "⚠️ **Riesgo muy alto para el retorno obtenido**")
	}

	if maxDrawdown < -15 {
		recommendations = append(recommendations, "⚠️ **Drawdown Alto**: Implementar stop-loss más estrictos")
	}

	if winRate < 50 {
		recommendations = append(recommendations, "🔧 **Mejorar señales de entrada**: Win rate por debajo del 50%")
	}

	recommendations = append(recommendations, "", "💡 **Próximos Pasos**:")
	recommendations = append(recommendations, "1. **Optimizar Parámetros**: Usar algoritmos genéticos")
	recommendations = append(recommendations, "2. **Walk-Forward Analysis**: Validar robustez temporal")
	recommendations = append(recommendations, "3. **Paper Trading**: Probar en tiempo real antes de capital real")
	recommendations = append(recommendations, "4. **Diversificación**: Probar en múltiples pares de trading")

	return strings.Join(recommendations, "\n")
}

// getEnglishRecommendations genera recomendaciones en inglés
func (h *BacktestingHandler) getEnglishRecommendations(roi, sharpeRatio, maxDrawdown, winRate float64) string {
	recommendations := []string{
		"🚀 **RECOMMENDATIONS**",
		"",
	}

	if roi > 15 {
		recommendations = append(recommendations, "✅ **Excellent ROI**: Maintain current strategy")
	} else if roi > 5 {
		recommendations = append(recommendations, "🔧 **Moderate ROI**: Consider parameter optimization")
	} else {
		recommendations = append(recommendations, "⚠️ **Low ROI**: Completely review strategy")
	}

	if sharpeRatio > 1.5 {
		recommendations = append(recommendations, "✅ **Excellent risk/return ratio**")
	} else if sharpeRatio > 1.0 {
		recommendations = append(recommendations, "🔧 **Improve risk management**")
	} else {
		recommendations = append(recommendations, "⚠️ **Risk too high for obtained return**")
	}

	if maxDrawdown < -15 {
		recommendations = append(recommendations, "⚠️ **High Drawdown**: Implement stricter stop-losses")
	}

	if winRate < 50 {
		recommendations = append(recommendations, "🔧 **Improve entry signals**: Win rate below 50%")
	}

	recommendations = append(recommendations, "", "💡 **Next Steps**:")
	recommendations = append(recommendations, "1. **Optimize Parameters**: Use genetic algorithms")
	recommendations = append(recommendations, "2. **Walk-Forward Analysis**: Validate temporal robustness")
	recommendations = append(recommendations, "3. **Paper Trading**: Test in real-time before real capital")
	recommendations = append(recommendations, "4. **Diversification**: Test on multiple trading pairs")

	return strings.Join(recommendations, "\n")
}

// calculateDurationDays calcula la duración en días entre dos fechas
func (h *BacktestingHandler) calculateDurationDays(startDate, endDate string) int {
	start, err1 := time.Parse("2006-01-02", startDate)
	end, err2 := time.Parse("2006-01-02", endDate)

	if err1 != nil || err2 != nil {
		return 30 // Valor por defecto
	}

	return int(end.Sub(start).Hours() / 24)
}

// Funciones auxiliares para extraer valores de mapas
func getFloatValue(m map[string]interface{}, key string, defaultValue float64) float64 {
	if val, ok := m[key]; ok {
		switch v := val.(type) {
		case float64:
			return v
		case int:
			return float64(v)
		case string:
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				return f
			}
		}
	}
	return defaultValue
}

func getIntValue(m map[string]interface{}, key string, defaultValue int) int {
	if val, ok := m[key]; ok {
		switch v := val.(type) {
		case int:
			return v
		case float64:
			return int(v)
		case string:
			if i, err := strconv.Atoi(v); err == nil {
				return i
			}
		}
	}
	return defaultValue
}

func getStringValue(m map[string]interface{}, key string, defaultValue string) string {
	if val, ok := m[key]; ok {
		if s, ok := val.(string); ok {
			return s
		}
	}
	return defaultValue
}

// profitFactorFromTrades returns gross profit / gross loss from the real
// per-trade PnL, or 0 when it cannot be computed (no trades, no losing trades).
func profitFactorFromTrades(trades []BacktestTrade) float64 {
	var gain, loss float64
	for _, t := range trades {
		if t.PnL > 0 {
			gain += t.PnL
		} else {
			loss -= t.PnL
		}
	}
	if loss <= 0 {
		return 0
	}
	return gain / loss
}

// averageDuration returns the mean holding time from the real entry/exit
// timestamps of the trades, or "" when none are parseable.
func averageDuration(trades []BacktestTrade) string {
	var total time.Duration
	var n int
	for _, t := range trades {
		in, err1 := time.Parse(time.RFC3339, t.EntryTime)
		out, err2 := time.Parse(time.RFC3339, t.ExitTime)
		if err1 != nil || err2 != nil || out.Before(in) {
			continue
		}
		total += out.Sub(in)
		n++
	}
	if n == 0 {
		return ""
	}
	return (total / time.Duration(n)).Round(time.Minute).String()
}

func durationOrUnknown(d, lang string) string {
	if d != "" {
		return d
	}
	if lang == "en" {
		return "not available"
	}
	return "no disponible"
}
