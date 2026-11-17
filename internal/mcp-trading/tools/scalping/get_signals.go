package scalping

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/tools/common"
	"github.com/rantipay/trading-mcp/internal/scalping/models"
	"github.com/rantipay/trading-mcp/internal/scalping/strategies"
)

// GetSignalsTool implementa la tool para obtener señales de scalping
type GetSignalsTool struct {
	adapter *Adapter
}

// NewGetSignalsTool crea una nueva instancia de la tool
func NewGetSignalsTool(adapter *Adapter) *GetSignalsTool {
	return &GetSignalsTool{
		adapter: adapter,
	}
}

// Name retorna el nombre de la tool
func (t *GetSignalsTool) Name() string {
	return "get_scalping_signals"
}

// Description retorna la descripción de la tool
func (t *GetSignalsTool) Description() string {
	return "Gets real-time scalping signals for multiple trading pairs using various strategies. " +
		"Analyzes market conditions and returns buy/sell signals with confidence scores. " +
		"Supports RSI, MACD, Bollinger Bands, and Volume strategies."
}

// InputSchema retorna el schema JSON de los parámetros de entrada
func (t *GetSignalsTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name (e.g., 'kucoin', 'binance')",
			},
			"pairs": map[string]interface{}{
				"type":        "array",
				"description": "List of trading pairs to analyze (max 10). If not provided, uses default pairs: BTC-USDT, ETH-USDT, DOGE-USDT",
				"items": map[string]interface{}{
					"type": "string",
				},
				"maxItems": 10,
			},
			"strategies": map[string]interface{}{
				"type":        "array",
				"description": "Strategies to use (optional). Options: 'rsi', 'macd', 'bollinger', 'volume'. If not provided, uses all available strategies.",
				"items": map[string]interface{}{
					"type": "string",
					"enum": []string{"rsi", "macd", "bollinger", "volume"},
				},
			},
		},
		"required": []string{"exchange"},
	}
}

// Execute ejecuta la tool
func (t *GetSignalsTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	// Parsear parámetros
	req := GetSignalsRequest{
		Exchange:   getStringParam(params, "exchange"),
		Pairs:      getStringSliceParam(params, "pairs"),
		Strategies: getStringSliceParam(params, "strategies"),
	}

	// Validar parámetros
	if req.Exchange == "" {
		return nil, ErrInvalidExchange
	}

	// Si no se especifican pares, usar pares por defecto
	if len(req.Pairs) == 0 {
		req.Pairs = []string{"BTC-USDT", "ETH-USDT", "DOGE-USDT"}
	}

	if len(req.Pairs) > 10 {
		return nil, ErrTooManyPairs
	}

	// Si no se especifican estrategias, usar todas
	if len(req.Strategies) == 0 {
		req.Strategies = []string{"rsi", "macd"}
	}

	t.adapter.GetLogger().Infof("Getting signals for %d pairs on %s using strategies: %v",
		len(req.Pairs), req.Exchange, req.Strategies)

	// Analizar pares en paralelo
	var signals []Signal
	var mu sync.Mutex
	var wg sync.WaitGroup
	dataFailures := 0

	for _, pair := range req.Pairs {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()

			pairSignals, noData := t.analyzePair(ctx, req.Exchange, p, req.Strategies)

			mu.Lock()
			if noData {
				dataFailures++
			}
			signals = append(signals, pairSignals...)
			mu.Unlock()
		}(pair)
	}

	wg.Wait()

	if dataFailures == len(req.Pairs) {
		return nil, common.DataUnavailable("market data could not be fetched for any requested pair", map[string]interface{}{"exchange": req.Exchange, "pairs": req.Pairs})
	}

	// Filtrar señales por confianza mínima
	var filteredSignals []Signal
	for _, signal := range signals {
		if signal.Confidence >= 0.6 {
			filteredSignals = append(filteredSignals, signal)
		}
	}

	t.adapter.GetLogger().Infof("Found %d signals (filtered from %d total)",
		len(filteredSignals), len(signals))

	response := &GetSignalsResponse{
		Signals: filteredSignals,
		Count:   len(filteredSignals),
	}

	return response, nil
}

// analyzePair analiza un par específico
func (t *GetSignalsTool) analyzePair(ctx context.Context, exchange, pair string, strategyNames []string) ([]Signal, bool) {
	var signals []Signal

	// Obtener datos de mercado
	ticker, err := t.adapter.GetGCTClient().GetTicker(ctx, exchange, pair)
	if err != nil {
		t.adapter.GetLogger().Warnf("Failed to get ticker for %s: %v", pair, err)
		return signals, true
	}

	// Closes of recent candles: the RSI/MACD strategies need a price history,
	// a single ticker price cannot produce a real signal.
	candles, err := t.adapter.GetGCTClient().GetCandles(ctx, exchange, pair, signalInterval, signalCandles)
	if err != nil || len(candles) == 0 {
		t.adapter.GetLogger().Warnf("Failed to get candles for %s: %v", pair, err)
		return signals, true
	}
	prices := make([]float64, 0, len(candles))
	volumes := make([]float64, 0, len(candles))
	for _, c := range candles {
		prices = append(prices, c.Close)
		volumes = append(volumes, c.Volume)
	}

	// Crear market data
	marketData := &strategies.MarketData{
		Pair:      pair,
		Exchange:  exchange,
		Price:     ticker.Last,
		Volume:    ticker.Volume,
		Timestamp: time.Now(),
		Features: &models.Features{
			Prices:  prices,
			Volumes: volumes,
		},
	}

	// Analizar con orchestrator
	signal, err := t.adapter.GetOrchestrator().AnalyzeMarket(ctx, pair, marketData)
	if err != nil {
		t.adapter.GetLogger().Warnf("Failed to analyze %s: %v", pair, err)
		return signals, false
	}

	// Convertir señal a formato MCP
	if signal != nil && signal.Confidence >= 0.6 {
		mcpSignal := Signal{
			Pair:       signal.Pair,
			Direction:  signal.Direction,
			Confidence: signal.Confidence,
			Strategy:   signal.Strategies[0], // Primera estrategia
			Price:      signal.Price,
			Reason:     t.generateReason(signal),
			Timestamp:  signal.Timestamp.Format(time.RFC3339),
		}
		signals = append(signals, mcpSignal)
	}

	return signals, false
}

// generateReason genera una razón legible para la señal
func (t *GetSignalsTool) generateReason(signal *models.ScalpingSignal) string {
	strategy := signal.Strategies[0]
	direction := signal.Direction

	var reason string
	switch strategy {
	case "rsi_scalping":
		if direction == "buy" {
			reason = fmt.Sprintf("RSI oversold condition detected (confidence: %.1f%%)", signal.Confidence*100)
		} else {
			reason = fmt.Sprintf("RSI overbought condition detected (confidence: %.1f%%)", signal.Confidence*100)
		}
	case "macd_scalping":
		if direction == "buy" {
			reason = fmt.Sprintf("MACD bullish crossover detected (confidence: %.1f%%)", signal.Confidence*100)
		} else {
			reason = fmt.Sprintf("MACD bearish crossover detected (confidence: %.1f%%)", signal.Confidence*100)
		}
	default:
		reason = fmt.Sprintf("%s signal with %.1f%% confidence", direction, signal.Confidence*100)
	}

	return reason
}

const (
	signalInterval = "5m"
	signalCandles  = 100
)
