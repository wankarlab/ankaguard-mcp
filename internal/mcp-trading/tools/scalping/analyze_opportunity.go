package scalping

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/tools/common"
)

// AnalyzeOpportunityTool implementa la tool para analizar oportunidades de scalping
type AnalyzeOpportunityTool struct {
	adapter *Adapter
}

// NewAnalyzeOpportunityTool crea una nueva instancia de la tool
func NewAnalyzeOpportunityTool(adapter *Adapter) *AnalyzeOpportunityTool {
	return &AnalyzeOpportunityTool{
		adapter: adapter,
	}
}

// Name retorna el nombre de la tool
func (t *AnalyzeOpportunityTool) Name() string {
	return "analyze_scalping_opportunity"
}

// Description retorna la descripción de la tool
func (t *AnalyzeOpportunityTool) Description() string {
	return "Analyzes if a trading pair is suitable for scalping based on volatility, spread, and volume. " +
		"Returns a score (0-100) and recommendation (excellent/good/fair/poor) with detailed metrics."
}

// InputSchema retorna el schema JSON de los parámetros de entrada
func (t *AnalyzeOpportunityTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name (e.g., 'kucoin', 'binance')",
			},
			"pair": map[string]interface{}{
				"type":        "string",
				"description": "Trading pair (e.g., 'BTC-USDT', 'ETH-USDT')",
			},
			"timeframe": map[string]interface{}{
				"type":        "string",
				"description": "Timeframe for analysis (optional, default: '5m'). Options: '1m', '5m', '15m'",
				"enum":        []string{"1m", "5m", "15m"},
			},
		},
		"required": []string{"exchange", "pair"},
	}
}

// Execute ejecuta la tool
func (t *AnalyzeOpportunityTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	// Parsear parámetros
	req := AnalyzeOpportunityRequest{
		Exchange:  getStringParam(params, "exchange"),
		Pair:      getStringParam(params, "pair"),
		Timeframe: getStringParam(params, "timeframe"),
	}

	// Validar parámetros
	if req.Exchange == "" {
		return nil, ErrInvalidExchange
	}
	if req.Pair == "" {
		return nil, ErrInvalidPair
	}
	if req.Timeframe == "" {
		req.Timeframe = "5m" // Default
	}

	t.adapter.GetLogger().Infof("Analyzing scalping opportunity for %s:%s", req.Exchange, req.Pair)

	// Obtener ticker
	ticker, err := t.adapter.GetGCTClient().GetTicker(ctx, req.Exchange, req.Pair)
	if err != nil {
		return nil, fmt.Errorf("failed to get ticker: %w", err)
	}

	// ATR (Wilder, period 14) from real candles of the requested timeframe.
	candles, err := t.adapter.GetGCTClient().GetCandles(ctx, req.Exchange, req.Pair, req.Timeframe, atrCandles)
	if err != nil {
		return nil, common.DataUnavailable("could not fetch candles to measure volatility: "+err.Error(), map[string]interface{}{
			"exchange": req.Exchange, "pair": req.Pair, "timeframe": req.Timeframe,
		})
	}
	atr, err := atrFromCandles(candles, atrPeriod)
	if err != nil {
		return nil, common.DataUnavailable(err.Error(), map[string]interface{}{
			"exchange": req.Exchange, "pair": req.Pair, "timeframe": req.Timeframe,
			"candles_available": len(candles), "candles_required": atrPeriod + 1,
		})
	}

	// Calcular scores
	volatilityScore := t.calculateVolatilityScore(atr, ticker.Last)
	spreadScore := t.calculateSpreadScore(ticker.Bid, ticker.Ask, ticker.Last)
	volumeScore := t.calculateVolumeScore(ticker.Volume)

	// Overall score (weighted average)
	overallScore := int((float64(volatilityScore)*0.4 + float64(spreadScore)*0.3 + float64(volumeScore)*0.3))

	// Generar recomendación y razones
	recommendation, reasons := t.generateRecommendation(overallScore, volatilityScore, spreadScore, volumeScore)

	// Calcular spread en porcentaje
	spreadPercent := 0.0
	if ticker.Last > 0 {
		spreadPercent = ((ticker.Ask - ticker.Bid) / ticker.Last) * 100
	}

	// Calcular campos adicionales para trading
	opportunityScore := float64(overallScore) / 10.0 // Convertir 0-100 a 0-10
	confidence := float64(overallScore) / 100.0      // Convertir 0-100 a 0-1

	// Determinar dirección basada en análisis
	direction := t.determineDirection(volatilityScore, spreadScore, volumeScore)

	// Calcular precios de entrada, objetivo y stop loss
	entryPrice := ticker.Last
	targetPrice, stopLoss, riskRewardRatio := t.calculateTradingLevels(entryPrice, atr, direction)

	// Calcular porcentajes de profit y loss
	expectedProfitPercent := 0.0
	maxLossPercent := 0.0
	if direction == "long" {
		expectedProfitPercent = ((targetPrice - entryPrice) / entryPrice) * 100
		maxLossPercent = ((entryPrice - stopLoss) / entryPrice) * 100
	} else if direction == "short" {
		expectedProfitPercent = ((entryPrice - targetPrice) / entryPrice) * 100
		maxLossPercent = ((stopLoss - entryPrice) / entryPrice) * 100
	}

	response := &AnalyzeOpportunityResponse{
		Pair:                  req.Pair,
		OverallScore:          overallScore,
		OpportunityScore:      opportunityScore,
		VolatilityScore:       volatilityScore,
		SpreadScore:           spreadScore,
		VolumeScore:           volumeScore,
		Recommendation:        recommendation,
		Direction:             direction,
		Confidence:            confidence,
		Reasons:               reasons,
		CurrentPrice:          ticker.Last,
		EntryPrice:            entryPrice,
		TargetPrice:           targetPrice,
		StopLoss:              stopLoss,
		RiskRewardRatio:       riskRewardRatio,
		ExpectedProfitPercent: expectedProfitPercent,
		MaxLossPercent:        maxLossPercent,
		Spread:                spreadPercent,
		Volume24h:             ticker.Volume,
		ATR:                   atr,
		Timestamp:             time.Now().Format(time.RFC3339),
	}

	t.adapter.GetLogger().Infof("Analysis complete for %s: score=%d, direction=%s, recommendation=%s",
		req.Pair, overallScore, direction, recommendation)

	return response, nil
}

// determineDirection determina la dirección del trade basado en los scores
func (t *AnalyzeOpportunityTool) determineDirection(volatility, spread, volume int) string {
	// Si la volatilidad es alta y el spread es bajo, favorece long
	// Si el volumen es bajo, neutral
	if volume < 40 {
		return "neutral"
	}

	// Volatilidad alta + spread bajo = buenas condiciones para long
	if volatility >= 70 && spread >= 70 {
		return "long"
	}

	// Volatilidad moderada
	if volatility >= 50 && spread >= 60 {
		return "long"
	}

	// Condiciones no ideales
	return "neutral"
}

// calculateTradingLevels calcula los niveles de trading basados en ATR
func (t *AnalyzeOpportunityTool) calculateTradingLevels(entryPrice, atr float64, direction string) (targetPrice, stopLoss, riskRewardRatio float64) {
	// Usar ATR para calcular niveles
	// Target: 2x ATR
	// Stop Loss: 1x ATR
	// Risk:Reward = 1:2

	if direction == "long" {
		targetPrice = entryPrice + (atr * 2.0)
		stopLoss = entryPrice - atr
		riskRewardRatio = 2.0
	} else if direction == "short" {
		targetPrice = entryPrice - (atr * 2.0)
		stopLoss = entryPrice + atr
		riskRewardRatio = 2.0
	} else {
		// Neutral - usar valores conservadores
		targetPrice = entryPrice + (atr * 1.5)
		stopLoss = entryPrice - (atr * 0.75)
		riskRewardRatio = 2.0
	}

	return targetPrice, stopLoss, riskRewardRatio
}

const (
	atrPeriod  = 14
	atrCandles = 60
)

// atrFromCandles computes Wilder's ATR over candles ordered oldest first
// (GCTClient.GetCandles contract).
func atrFromCandles(candles []*gct.Candle, period int) (float64, error) {
	if len(candles) < period+1 {
		return 0, fmt.Errorf("not enough candles to compute ATR: have %d, need %d", len(candles), period+1)
	}
	tr := func(i int) float64 {
		c, p := candles[i], candles[i-1]
		return math.Max(c.High-c.Low, math.Max(math.Abs(c.High-p.Close), math.Abs(c.Low-p.Close)))
	}
	sum := 0.0
	for i := 1; i <= period; i++ {
		sum += tr(i)
	}
	atr := sum / float64(period)
	for i := period + 1; i < len(candles); i++ {
		atr = (atr*float64(period-1) + tr(i)) / float64(period)
	}
	return atr, nil
}

// calculateVolatilityScore calcula el score de volatilidad basado en ATR
func (t *AnalyzeOpportunityTool) calculateVolatilityScore(atr, price float64) int {
	if price == 0 {
		return 0
	}

	atrPercent := (atr / price) * 100

	// Score basado en ATR percentage
	// Para scalping, queremos volatilidad moderada-alta
	switch {
	case atrPercent > 3.0:
		return 100 // Excelente volatilidad para scalping
	case atrPercent > 2.0:
		return 85
	case atrPercent > 1.5:
		return 70
	case atrPercent > 1.0:
		return 55
	case atrPercent > 0.5:
		return 40
	default:
		return 20 // Baja volatilidad
	}
}

// calculateSpreadScore calcula el score del spread
func (t *AnalyzeOpportunityTool) calculateSpreadScore(bid, ask, last float64) int {
	if last == 0 {
		return 0
	}

	spreadPercent := ((ask - bid) / last) * 100

	// Lower spread is better for scalping
	switch {
	case spreadPercent < 0.05:
		return 100 // Excelente spread
	case spreadPercent < 0.1:
		return 85
	case spreadPercent < 0.15:
		return 70
	case spreadPercent < 0.2:
		return 55
	case spreadPercent < 0.3:
		return 40
	default:
		return 20 // Spread alto
	}
}

// calculateVolumeScore calcula el score del volumen
func (t *AnalyzeOpportunityTool) calculateVolumeScore(volume float64) int {
	// Score basado en volumen 24h
	// Estos thresholds son genéricos y deberían ajustarse por mercado
	switch {
	case volume > 10000000: // >10M
		return 100 // Alto volumen
	case volume > 5000000: // >5M
		return 85
	case volume > 1000000: // >1M
		return 70
	case volume > 500000: // >500K
		return 55
	case volume > 100000: // >100K
		return 40
	default:
		return 20 // Bajo volumen
	}
}

// generateRecommendation genera la recomendación y razones
func (t *AnalyzeOpportunityTool) generateRecommendation(overall, volatility, spread, volume int) (string, []string) {
	var reasons []string

	// Analizar volatilidad
	if volatility >= 70 {
		reasons = append(reasons, "High volatility provides good scalping opportunities")
	} else if volatility < 40 {
		reasons = append(reasons, "Low volatility may limit scalping profits")
	}

	// Analizar spread
	if spread >= 70 {
		reasons = append(reasons, "Tight spread reduces trading costs")
	} else if spread < 40 {
		reasons = append(reasons, "Wide spread increases trading costs significantly")
	}

	// Analizar volumen
	if volume >= 70 {
		reasons = append(reasons, "High volume ensures good liquidity and fast execution")
	} else if volume < 40 {
		reasons = append(reasons, "Low volume may cause slippage and execution delays")
	}

	// Determinar recomendación general
	var recommendation string
	switch {
	case overall >= 80:
		recommendation = "excellent"
		reasons = append([]string{"This pair shows excellent conditions for scalping"}, reasons...)
	case overall >= 65:
		recommendation = "good"
		reasons = append([]string{"This pair shows good conditions for scalping"}, reasons...)
	case overall >= 45:
		recommendation = "fair"
		reasons = append([]string{"This pair shows fair conditions for scalping, proceed with caution"}, reasons...)
	default:
		recommendation = "poor"
		reasons = append([]string{"This pair shows poor conditions for scalping, not recommended"}, reasons...)
	}

	return recommendation, reasons
}
