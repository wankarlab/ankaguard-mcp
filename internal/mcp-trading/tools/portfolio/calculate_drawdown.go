package portfolio

import (
	"context"
	"math"
	"time"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
)

// CalculateDrawdownTool calcula metricas de drawdown del portfolio
type CalculateDrawdownTool struct {
	client *gctclient.Client
}

// NewCalculateDrawdownTool crea una nueva instancia del tool
func NewCalculateDrawdownTool(client *gctclient.Client) *CalculateDrawdownTool {
	return &CalculateDrawdownTool{client: client}
}

// Name retorna el nombre del tool
func (t *CalculateDrawdownTool) Name() string {
	return "calculate_drawdown"
}

// Description retorna la descripcion del tool
func (t *CalculateDrawdownTool) Description() string {
	return "Calculate portfolio drawdown metrics including current drawdown, maximum drawdown, and recovery factor based on historical P&L data"
}

// InputSchema retorna el esquema de entrada del tool
func (t *CalculateDrawdownTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name (e.g., 'kucoin', 'binance')",
			},
			"period": map[string]interface{}{
				"type":        "string",
				"description": "Analysis period: 'daily', 'weekly', or 'monthly'",
				"enum":        []string{"daily", "weekly", "monthly"},
				"default":     "daily",
			},
		},
		"required": []string{"exchange"},
	}
}

// DrawdownMetrics contiene las metricas de drawdown calculadas
type DrawdownMetrics struct {
	CurrentDrawdown    float64   `json:"current_drawdown"`     // Drawdown actual en porcentaje
	CurrentDrawdownUSD float64   `json:"current_drawdown_usd"` // Drawdown actual en USD
	MaxDrawdown        float64   `json:"max_drawdown"`         // Maximo drawdown en porcentaje
	MaxDrawdownUSD     float64   `json:"max_drawdown_usd"`     // Maximo drawdown en USD
	MaxDrawdownDate    time.Time `json:"max_drawdown_date"`    // Fecha del maximo drawdown
	RecoveryFactor     float64   `json:"recovery_factor"`      // Factor de recuperacion (profit/max_drawdown)
	PeakValue          float64   `json:"peak_value"`           // Valor pico del portfolio
	CurrentValue       float64   `json:"current_value"`        // Valor actual del portfolio
	DrawdownDuration   int       `json:"drawdown_duration"`    // Duracion del drawdown actual en periodos
	AverageDrawdown    float64   `json:"average_drawdown"`     // Drawdown promedio
	DrawdownCount      int       `json:"drawdown_count"`       // Numero de drawdowns en el periodo
	CalmarRatio        float64   `json:"calmar_ratio"`         // Ratio Calmar (rendimiento anualizado / max drawdown)
	UlcerIndex         float64   `json:"ulcer_index"`          // Indice Ulcer (raiz cuadrada del promedio de drawdowns al cuadrado)
}

// Execute ejecuta el calculo de drawdown
func (t *CalculateDrawdownTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	// Validar exchange
	exchange, ok := params["exchange"].(string)
	if !ok || exchange == "" {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "exchange is required",
		}
	}

	// Obtener periodo (default: daily)
	period := "daily"
	if p, ok := params["period"].(string); ok && p != "" {
		period = p
	}

	// Validar periodo
	if period != "daily" && period != "weekly" && period != "monthly" {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "period must be 'daily', 'weekly', or 'monthly'",
		}
	}

	// Obtener portfolio actual
	portfolio, err := t.client.GetPortfolio(ctx)
	if err != nil {
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Failed to get portfolio",
			Data:    err.Error(),
		}
	}

	// Obtener historial de trades para calcular PnL historico
	tradeLimit := t.getTradeLimit(period)
	trades, err := t.client.GetTradeHistory(ctx, exchange, "", tradeLimit)
	if err != nil {
		// Si no hay trades, retornar metricas basadas solo en portfolio actual
		return t.calculateBasicMetrics(exchange, period, portfolio), nil
	}

	// Calcular metricas de drawdown basadas en el historial
	metrics := t.calculateDrawdownMetrics(exchange, period, portfolio, trades)

	return map[string]interface{}{
		"exchange":             exchange,
		"period":               period,
		"metrics":              metrics,
		"portfolio_value":      portfolio.TotalValueUSD,
		"portfolio_pnl":        portfolio.PnL,
		"portfolio_pnl_pct":    portfolio.PnLPercent,
		"analysis_timestamp":   time.Now().UTC(),
		"trade_count_analyzed": len(trades),
	}, nil
}

// getTradeLimit retorna el limite de trades basado en el periodo
func (t *CalculateDrawdownTool) getTradeLimit(period string) int {
	switch period {
	case "daily":
		return 100 // Aproximadamente 100 trades para analisis diario
	case "weekly":
		return 500 // Mas trades para analisis semanal
	case "monthly":
		return 1000 // Aun mas para analisis mensual
	default:
		return 100
	}
}

// calculateBasicMetrics calcula metricas basicas cuando no hay historial de trades
func (t *CalculateDrawdownTool) calculateBasicMetrics(exchange, period string, portfolio *models.Portfolio) map[string]interface{} {
	metrics := &DrawdownMetrics{
		CurrentValue:    portfolio.TotalValueUSD,
		PeakValue:       portfolio.TotalValueUSD,
		RecoveryFactor:  0,
		DrawdownCount:   0,
		AverageDrawdown: 0,
		CalmarRatio:     0,
		UlcerIndex:      0,
	}

	// Si hay PnL negativo, calculamos drawdown basico
	if portfolio.PnL < 0 {
		// Estimamos el peak value basado en el PnL
		metrics.PeakValue = portfolio.TotalValueUSD - portfolio.PnL
		metrics.CurrentDrawdown = (portfolio.PnL / metrics.PeakValue) * 100
		metrics.CurrentDrawdownUSD = math.Abs(portfolio.PnL)
		metrics.MaxDrawdown = metrics.CurrentDrawdown
		metrics.MaxDrawdownUSD = metrics.CurrentDrawdownUSD
		metrics.MaxDrawdownDate = time.Now().UTC()
		metrics.DrawdownCount = 1
	}

	return map[string]interface{}{
		"exchange":             exchange,
		"period":               period,
		"metrics":              metrics,
		"portfolio_value":      portfolio.TotalValueUSD,
		"portfolio_pnl":        portfolio.PnL,
		"portfolio_pnl_pct":    portfolio.PnLPercent,
		"analysis_timestamp":   time.Now().UTC(),
		"trade_count_analyzed": 0,
		"note":                 "Basic metrics calculated from current portfolio state only (no trade history available)",
	}
}

// calculateDrawdownMetrics calcula las metricas de drawdown completas
func (t *CalculateDrawdownTool) calculateDrawdownMetrics(exchange, period string, portfolio *models.Portfolio, trades []map[string]interface{}) *DrawdownMetrics {
	metrics := &DrawdownMetrics{
		CurrentValue:     portfolio.TotalValueUSD,
		MaxDrawdownDate:  time.Now().UTC(),
		DrawdownDuration: 0,
	}

	if len(trades) == 0 {
		metrics.PeakValue = portfolio.TotalValueUSD
		return metrics
	}

	// Reconstruir serie de valores del portfolio basada en trades
	equityCurve := t.buildEquityCurve(portfolio.TotalValueUSD, trades)

	if len(equityCurve) == 0 {
		metrics.PeakValue = portfolio.TotalValueUSD
		return metrics
	}

	// Calcular drawdowns
	var (
		peak             float64
		maxDrawdown      float64
		maxDrawdownUSD   float64
		maxDrawdownDate  time.Time
		drawdowns        []float64
		squaredDrawdowns float64
		inDrawdown       bool
		drawdownStart    int
		drawdownCount    int
		totalProfit      float64
	)

	peak = equityCurve[0].value

	for i, point := range equityCurve {
		if point.value > peak {
			peak = point.value
			inDrawdown = false
		}

		currentDrawdown := 0.0
		currentDrawdownUSD := 0.0
		if peak > 0 {
			currentDrawdownUSD = peak - point.value
			currentDrawdown = (currentDrawdownUSD / peak) * 100
		}

		if currentDrawdown > 0 {
			if !inDrawdown {
				inDrawdown = true
				drawdownStart = i
				drawdownCount++
			}
			drawdowns = append(drawdowns, currentDrawdown)
			squaredDrawdowns += currentDrawdown * currentDrawdown

			if currentDrawdown > maxDrawdown {
				maxDrawdown = currentDrawdown
				maxDrawdownUSD = currentDrawdownUSD
				maxDrawdownDate = point.timestamp
			}
		}

		// Acumular profit de trades
		if point.pnl > 0 {
			totalProfit += point.pnl
		}
	}

	// Calcular metricas finales
	metrics.MaxDrawdown = maxDrawdown
	metrics.MaxDrawdownUSD = maxDrawdownUSD
	metrics.MaxDrawdownDate = maxDrawdownDate
	metrics.PeakValue = peak
	metrics.DrawdownCount = drawdownCount

	// Current drawdown
	if peak > 0 && portfolio.TotalValueUSD < peak {
		metrics.CurrentDrawdown = ((peak - portfolio.TotalValueUSD) / peak) * 100
		metrics.CurrentDrawdownUSD = peak - portfolio.TotalValueUSD
		if inDrawdown {
			metrics.DrawdownDuration = len(equityCurve) - drawdownStart
		}
	}

	// Average drawdown
	if len(drawdowns) > 0 {
		sum := 0.0
		for _, d := range drawdowns {
			sum += d
		}
		metrics.AverageDrawdown = sum / float64(len(drawdowns))
	}

	// Recovery Factor: total profit / max drawdown
	if maxDrawdownUSD > 0 {
		metrics.RecoveryFactor = totalProfit / maxDrawdownUSD
	}

	// Ulcer Index: sqrt(mean of squared drawdowns)
	if len(drawdowns) > 0 {
		metrics.UlcerIndex = math.Sqrt(squaredDrawdowns / float64(len(drawdowns)))
	}

	// Calmar Ratio: annualized return / max drawdown
	// Asumiendo que el periodo de analisis es lo que determina la anualizacion
	if maxDrawdown > 0 {
		annualizedReturn := t.annualizeReturn(portfolio.PnLPercent, period, len(equityCurve))
		metrics.CalmarRatio = annualizedReturn / maxDrawdown
	}

	return metrics
}

// equityPoint representa un punto en la curva de equity
type equityPoint struct {
	timestamp time.Time
	value     float64
	pnl       float64
}

// buildEquityCurve construye la curva de equity basada en trades
func (t *CalculateDrawdownTool) buildEquityCurve(currentValue float64, trades []map[string]interface{}) []equityPoint {
	if len(trades) == 0 {
		return nil
	}

	curve := make([]equityPoint, 0, len(trades))

	// Reconstruir hacia atras desde el valor actual
	runningValue := currentValue

	// Los trades vienen del mas reciente al mas antiguo, los procesamos en orden inverso
	for i := len(trades) - 1; i >= 0; i-- {
		trade := trades[i]

		var pnl float64
		if p, ok := trade["pnl"].(float64); ok {
			pnl = p
		} else if p, ok := trade["realized_pnl"].(float64); ok {
			pnl = p
		}

		var timestamp time.Time
		if ts, ok := trade["timestamp"].(time.Time); ok {
			timestamp = ts
		} else if ts, ok := trade["created_at"].(time.Time); ok {
			timestamp = ts
		} else if ts, ok := trade["time"].(string); ok {
			if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
				timestamp = parsed
			}
		}

		// Agregar punto a la curva
		curve = append(curve, equityPoint{
			timestamp: timestamp,
			value:     runningValue,
			pnl:       pnl,
		})

		// Restar el PnL para obtener el valor anterior
		runningValue -= pnl
	}

	return curve
}

// annualizeReturn anualiza el retorno basado en el periodo y numero de observaciones
func (t *CalculateDrawdownTool) annualizeReturn(returnPct float64, period string, observations int) float64 {
	var periodsPerYear float64

	switch period {
	case "daily":
		periodsPerYear = 365.0
	case "weekly":
		periodsPerYear = 52.0
	case "monthly":
		periodsPerYear = 12.0
	default:
		periodsPerYear = 365.0
	}

	if observations <= 0 {
		return 0
	}

	// Anualizacion simple: (1 + return)^(periods_per_year/observations) - 1
	if returnPct == 0 {
		return 0
	}

	factor := periodsPerYear / float64(observations)
	annualized := (math.Pow(1+(returnPct/100), factor) - 1) * 100

	return annualized
}
