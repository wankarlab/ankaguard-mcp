package scalping

import (
	"context"
	"fmt"
	"time"
)

// GetBotPerformanceTool implementa la tool para obtener datos históricos de performance
type GetBotPerformanceTool struct {
	adapter *Adapter
}

// NewGetBotPerformanceTool crea una nueva instancia de la tool
func NewGetBotPerformanceTool(adapter *Adapter) *GetBotPerformanceTool {
	return &GetBotPerformanceTool{
		adapter: adapter,
	}
}

// Name retorna el nombre de la tool
func (t *GetBotPerformanceTool) Name() string {
	return "get_scalping_bot_performance"
}

// Description retorna la descripción de la tool
func (t *GetBotPerformanceTool) Description() string {
	return "Gets historical performance data of a scalping bot for chart visualization. " +
		"Returns time-series data of cumulative P&L, win rate, and trade count over time. " +
		"Supports multiple timeframes: 1h, 4h, 24h, 7d, 30d."
}

// InputSchema retorna el schema JSON de los parámetros de entrada
func (t *GetBotPerformanceTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name (e.g., 'kucoin', 'binance')",
			},
			"pair": map[string]interface{}{
				"type":        "string",
				"description": "Trading pair (e.g., 'BTC-USDT', 'DOGE-USDT')",
			},
			"bot_id": map[string]interface{}{
				"type":        "string",
				"description": "Bot ID (optional, will be inferred from exchange+pair if not provided)",
			},
			"timeframe": map[string]interface{}{
				"type":        "string",
				"description": "Time range for performance data",
				"enum":        []string{"1h", "4h", "24h", "7d", "30d"},
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum number of data points to return (default: 100, max: 500)",
				"default":     100,
				"minimum":     10,
				"maximum":     500,
			},
		},
		"required": []string{"exchange", "pair", "timeframe"},
	}
}

// Execute ejecuta la tool
func (t *GetBotPerformanceTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	// Parsear parámetros
	exchange := getStringParam(params, "exchange")
	pair := getStringParam(params, "pair")
	botID := getStringParam(params, "bot_id")
	timeframe := getStringParam(params, "timeframe")

	// Parsear limit
	limit := 100
	if l, ok := params["limit"].(float64); ok {
		limit = int(l)
		if limit > 500 {
			limit = 500
		}
		if limit < 10 {
			limit = 10
		}
	}

	// Validar parámetros requeridos
	if exchange == "" {
		return nil, ErrInvalidExchange
	}
	if pair == "" {
		return nil, ErrInvalidPair
	}
	if timeframe == "" {
		return nil, fmt.Errorf("timeframe is required")
	}

	// Validar timeframe
	validTimeframes := map[string]bool{
		"1h": true, "4h": true, "24h": true, "7d": true, "30d": true,
	}
	if !validTimeframes[timeframe] {
		return nil, fmt.Errorf("invalid timeframe: %s (must be one of: 1h, 4h, 24h, 7d, 30d)", timeframe)
	}

	// Si no se proporciona bot_id, generarlo a partir de exchange+pair
	if botID == "" {
		botID = fmt.Sprintf("bot_%s_%s", exchange, pair)
	}

	t.adapter.GetLogger().Infof("Getting performance data for bot %s (timeframe=%s, limit=%d)",
		botID, timeframe, limit)

	// Calcular rango de tiempo
	endTime := time.Now()
	startTime := t.calculateStartTime(endTime, timeframe)

	// Obtener trades del bot en el rango de tiempo
	trades, _, err := t.adapter.GetBotManager().GetBotTrades(botID, 1000, 0, "completed")
	if err != nil {
		t.adapter.GetLogger().Warnf("Failed to get trades for bot %s: %v", botID, err)
		// Devolver respuesta vacía en lugar de error
		return t.createEmptyResponse(timeframe, startTime, endTime), nil
	}

	// Calcular data points
	dataPoints := t.calculateDataPoints(trades, startTime, endTime, timeframe, limit)

	// Calcular summary
	summary := t.calculateSummary(dataPoints, timeframe, startTime, endTime)

	response := &GetBotPerformanceResponse{
		DataPoints: dataPoints,
		Summary:    summary,
	}

	// Agregar mensaje si no hay datos
	if len(dataPoints) == 0 {
		response.Message = "No performance data available for this timeframe"
	}

	t.adapter.GetLogger().Infof("Retrieved %d performance data points for bot %s",
		len(dataPoints), botID)

	return response, nil
}

// calculateStartTime calcula el tiempo de inicio basado en el timeframe
func (t *GetBotPerformanceTool) calculateStartTime(endTime time.Time, timeframe string) time.Time {
	switch timeframe {
	case "1h":
		return endTime.Add(-1 * time.Hour)
	case "4h":
		return endTime.Add(-4 * time.Hour)
	case "24h":
		return endTime.Add(-24 * time.Hour)
	case "7d":
		return endTime.Add(-7 * 24 * time.Hour)
	case "30d":
		return endTime.Add(-30 * 24 * time.Hour)
	default:
		return endTime.Add(-24 * time.Hour)
	}
}

// calculateDataPoints calcula los puntos de datos de performance
func (t *GetBotPerformanceTool) calculateDataPoints(trades []BotTrade, startTime, endTime time.Time, timeframe string, limit int) []PerformanceDataPoint {
	if len(trades) == 0 {
		return []PerformanceDataPoint{}
	}

	// Filtrar trades en el rango de tiempo
	var filteredTrades []BotTrade
	for _, trade := range trades {
		tradeTime, err := time.Parse(time.RFC3339, trade.Timestamp)
		if err != nil {
			continue
		}
		if tradeTime.After(startTime) && tradeTime.Before(endTime) {
			filteredTrades = append(filteredTrades, trade)
		}
	}

	if len(filteredTrades) == 0 {
		return []PerformanceDataPoint{}
	}

	// Calcular intervalo entre puntos
	interval := t.calculateInterval(timeframe)

	// Generar puntos de datos
	dataPoints := []PerformanceDataPoint{}
	cumulativePnL := 0.0
	winningTrades := 0
	totalTrades := 0
	currentTime := startTime
	tradeIndex := 0

	for currentTime.Before(endTime) && len(dataPoints) < limit {
		// Procesar todos los trades hasta currentTime
		for tradeIndex < len(filteredTrades) {
			tradeTime, _ := time.Parse(time.RFC3339, filteredTrades[tradeIndex].Timestamp)
			if tradeTime.After(currentTime) {
				break
			}

			cumulativePnL += filteredTrades[tradeIndex].PnL
			totalTrades++
			if filteredTrades[tradeIndex].PnL > 0 {
				winningTrades++
			}
			tradeIndex++
		}

		// Calcular win rate
		winRate := 0.0
		if totalTrades > 0 {
			winRate = (float64(winningTrades) / float64(totalTrades)) * 100
		}

		// Agregar punto de datos
		dataPoint := PerformanceDataPoint{
			Timestamp:      currentTime.Format(time.RFC3339),
			CumulativePnL:  cumulativePnL,
			WinRate:        winRate,
			TradesCount:    totalTrades,
		}
		dataPoints = append(dataPoints, dataPoint)

		// Avanzar al siguiente intervalo
		currentTime = currentTime.Add(interval)
	}

	return dataPoints
}

// calculateInterval calcula el intervalo entre puntos basado en el timeframe
func (t *GetBotPerformanceTool) calculateInterval(timeframe string) time.Duration {
	switch timeframe {
	case "1h":
		return 1 * time.Minute
	case "4h":
		return 5 * time.Minute
	case "24h":
		return 15 * time.Minute
	case "7d":
		return 2 * time.Hour
	case "30d":
		return 8 * time.Hour
	default:
		return 15 * time.Minute
	}
}

// calculateSummary calcula el resumen de performance
func (t *GetBotPerformanceTool) calculateSummary(dataPoints []PerformanceDataPoint, timeframe string, startTime, endTime time.Time) PerformanceSummary {
	summary := PerformanceSummary{
		TotalPnL:     0.0,
		AvgWinRate:   0.0,
		TotalTrades:  0,
		Timeframe:    timeframe,
		StartTime:    startTime.Format(time.RFC3339),
		EndTime:      endTime.Format(time.RFC3339),
	}

	if len(dataPoints) == 0 {
		return summary
	}

	// Obtener valores del último punto
	lastPoint := dataPoints[len(dataPoints)-1]
	summary.TotalPnL = lastPoint.CumulativePnL
	summary.TotalTrades = lastPoint.TradesCount

	// Calcular win rate promedio
	totalWinRate := 0.0
	for _, point := range dataPoints {
		totalWinRate += point.WinRate
	}
	summary.AvgWinRate = totalWinRate / float64(len(dataPoints))

	return summary
}

// createEmptyResponse crea una respuesta vacía
func (t *GetBotPerformanceTool) createEmptyResponse(timeframe string, startTime, endTime time.Time) *GetBotPerformanceResponse {
	return &GetBotPerformanceResponse{
		DataPoints: []PerformanceDataPoint{},
		Summary: PerformanceSummary{
			TotalPnL:     0.0,
			AvgWinRate:   0.0,
			TotalTrades:  0,
			Timeframe:    timeframe,
			StartTime:    startTime.Format(time.RFC3339),
			EndTime:      endTime.Format(time.RFC3339),
		},
		Message: "No bot found for this pair",
	}
}
