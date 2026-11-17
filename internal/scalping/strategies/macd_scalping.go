package strategies

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/scalping/models"
)

// MACDScalpingStrategy implementa estrategia de scalping basada en MACD
type MACDScalpingStrategy struct {
	logger    *logrus.Logger
	gctClient gct.GCTClient
	config    *models.StrategyConfig
	enabled   bool

	// Parámetros específicos de MACD
	fastPeriod   int
	slowPeriod   int
	signalPeriod int
}

// NewMACDScalpingStrategy crea una nueva estrategia MACD para scalping
func NewMACDScalpingStrategy(logger *logrus.Logger, gctClient gct.GCTClient) *MACDScalpingStrategy {
	return &MACDScalpingStrategy{
		logger:       logger,
		gctClient:    gctClient,
		enabled:      true,
		fastPeriod:   8,  // Más rápido que 12 normal
		slowPeriod:   17, // Más rápido que 26 normal
		signalPeriod: 9,  // Igual que normal
		config: &models.StrategyConfig{
			Name:          "macd_scalping",
			Enabled:       true,
			Weight:        1.0,
			MinConfidence: 0.6,
			Params: map[string]interface{}{
				"fast_period":   8,
				"slow_period":   17,
				"signal_period": 9,
			},
		},
	}
}

// Name retorna el nombre de la estrategia
func (s *MACDScalpingStrategy) Name() string {
	return "macd_scalping"
}

// Analyze analiza el mercado y genera una señal
func (s *MACDScalpingStrategy) Analyze(ctx context.Context, pair string, data *MarketData) (*models.ScalpingSignal, error) {
	if !s.enabled {
		return nil, nil
	}

	// Obtener valores MACD
	macd := data.Features.MACD
	macdSignal := data.Features.MACDSignal
	macdHist := data.Features.MACDHist

	// Si no vienen en features, calcularlos desde los precios
	if macd == 0 && macdSignal == 0 {
		if len(data.Features.Prices) < s.slowPeriod+s.signalPeriod {
			return nil, fmt.Errorf("insufficient price data for MACD calculation (need %d, got %d)",
				s.slowPeriod+s.signalPeriod, len(data.Features.Prices))
		}

		macd, macdSignal, macdHist = calculateMACD(data.Features.Prices, s.fastPeriod, s.slowPeriod, s.signalPeriod)
	}

	// Generar señal basada en MACD
	var direction string
	var confidence float64

	// Cruce alcista: MACD cruza por encima de la señal
	if macdHist > 0 && macd > macdSignal {
		direction = "buy"
		// Confianza basada en la fuerza del histograma
		confidence = s.calculateConfidence(macdHist, macd, macdSignal)
	} else if macdHist < 0 && macd < macdSignal {
		// Cruce bajista: MACD cruza por debajo de la señal
		direction = "sell"
		confidence = s.calculateConfidence(math.Abs(macdHist), math.Abs(macd), math.Abs(macdSignal))
	} else {
		// Sin señal clara
		return nil, nil
	}

	// Verificar confianza mínima
	if confidence < s.config.MinConfidence {
		return nil, nil
	}

	// Ajustar confianza basado en momentum
	if data.Features.Momentum != 0 {
		momentumFactor := 1.0 + (data.Features.Momentum * 0.1)
		if momentumFactor > 1.5 {
			momentumFactor = 1.5
		}
		if momentumFactor < 0.5 {
			momentumFactor = 0.5
		}
		confidence *= momentumFactor
		if confidence > 1.0 {
			confidence = 1.0
		}
	}

	// Crear señal
	signal := &models.ScalpingSignal{
		ID:         fmt.Sprintf("macd_%d", time.Now().UnixNano()),
		Pair:       pair,
		Exchange:   data.Exchange,
		Direction:  direction,
		Confidence: confidence,
		Strategies: []string{s.Name()},
		Price:      data.Price,
		Volume:     data.Volume,
		Timestamp:  time.Now(),
		Metadata: map[string]interface{}{
			"macd":          macd,
			"macd_signal":   macdSignal,
			"macd_hist":     macdHist,
			"fast_period":   s.fastPeriod,
			"slow_period":   s.slowPeriod,
			"signal_period": s.signalPeriod,
		},
	}

	s.logger.Debugf("MACD signal for %s: %s (hist: %.4f, confidence: %.3f)",
		pair, direction, macdHist, confidence)

	return signal, nil
}

// calculateConfidence calcula la confianza basada en los valores MACD
func (s *MACDScalpingStrategy) calculateConfidence(hist, macd, signal float64) float64 {
	// Confianza basada en:
	// 1. Fuerza del histograma (diferencia entre MACD y señal)
	// 2. Magnitud del MACD

	histStrength := math.Min(math.Abs(hist)*100, 1.0)
	macdStrength := math.Min(math.Abs(macd)*10, 1.0)

	// Combinar ambos factores
	confidence := (histStrength*0.6 + macdStrength*0.4)

	// Limitar entre 0 y 1
	if confidence > 1.0 {
		confidence = 1.0
	}
	if confidence < 0.0 {
		confidence = 0.0
	}

	return confidence
}

// IsEnabled retorna si la estrategia está habilitada
func (s *MACDScalpingStrategy) IsEnabled() bool {
	return s.enabled
}

// SetEnabled habilita o deshabilita la estrategia
func (s *MACDScalpingStrategy) SetEnabled(enabled bool) {
	s.enabled = enabled
	s.config.Enabled = enabled
}

// GetConfig retorna la configuración de la estrategia
func (s *MACDScalpingStrategy) GetConfig() *models.StrategyConfig {
	return s.config
}

// UpdateConfig actualiza la configuración de la estrategia
func (s *MACDScalpingStrategy) UpdateConfig(config *models.StrategyConfig) error {
	if config == nil {
		return fmt.Errorf("config cannot be nil")
	}

	// Actualizar parámetros desde config
	if fastPeriod, ok := config.Params["fast_period"].(float64); ok {
		s.fastPeriod = int(fastPeriod)
	}
	if slowPeriod, ok := config.Params["slow_period"].(float64); ok {
		s.slowPeriod = int(slowPeriod)
	}
	if signalPeriod, ok := config.Params["signal_period"].(float64); ok {
		s.signalPeriod = int(signalPeriod)
	}

	s.config = config
	s.enabled = config.Enabled

	s.logger.Infof("Updated MACD strategy config: fast=%d, slow=%d, signal=%d",
		s.fastPeriod, s.slowPeriod, s.signalPeriod)

	return nil
}

// calculateEMA calcula la media móvil exponencial
func calculateEMA(prices []float64, period int) []float64 {
	if len(prices) < period {
		return []float64{}
	}

	ema := make([]float64, len(prices))
	multiplier := 2.0 / float64(period+1)

	// Primera EMA es una media simple
	sum := 0.0
	for i := 0; i < period; i++ {
		sum += prices[i]
	}
	ema[period-1] = sum / float64(period)

	// Calcular EMAs subsecuentes
	for i := period; i < len(prices); i++ {
		ema[i] = (prices[i]-ema[i-1])*multiplier + ema[i-1]
	}

	return ema
}

// calculateMACD calcula MACD, Signal y Histogram
func calculateMACD(prices []float64, fastPeriod, slowPeriod, signalPeriod int) (float64, float64, float64) {
	if len(prices) < slowPeriod+signalPeriod {
		return 0, 0, 0
	}

	// Calcular EMAs rápida y lenta
	fastEMA := calculateEMA(prices, fastPeriod)
	slowEMA := calculateEMA(prices, slowPeriod)

	// Calcular línea MACD
	macdLine := make([]float64, len(prices))
	for i := slowPeriod - 1; i < len(prices); i++ {
		macdLine[i] = fastEMA[i] - slowEMA[i]
	}

	// Calcular línea de señal (EMA del MACD)
	signalLine := calculateEMA(macdLine[slowPeriod-1:], signalPeriod)

	// Obtener valores más recientes
	macd := macdLine[len(macdLine)-1]
	signal := signalLine[len(signalLine)-1]
	histogram := macd - signal

	return macd, signal, histogram
}
