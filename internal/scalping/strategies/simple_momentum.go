package strategies

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/scalping/models"
)

// SimpleMomentumStrategy estrategia simple basada en momentum del precio
// NO requiere datos históricos, solo el precio actual y volumen
type SimpleMomentumStrategy struct {
	logger      *logrus.Logger
	gctClient   gct.GCTClient
	config      *models.StrategyConfig
	enabled     bool
	lastPrice   float64
	lastVolume  float64
	priceChange float64
}

// NewSimpleMomentumStrategy crea una nueva estrategia de momentum simple
func NewSimpleMomentumStrategy(logger *logrus.Logger, gctClient gct.GCTClient) *SimpleMomentumStrategy {
	return &SimpleMomentumStrategy{
		logger:    logger,
		gctClient: gctClient,
		enabled:   true,
		config: &models.StrategyConfig{
			Name:          "simple_momentum",
			Enabled:       true,
			Weight:        1.0,
			MinConfidence: 0.5,
			Params: map[string]interface{}{
				"min_price_change": 0.001, // 0.1% mínimo
			},
		},
	}
}

// Name retorna el nombre de la estrategia
func (s *SimpleMomentumStrategy) Name() string {
	return "simple_momentum"
}

// Analyze analiza el mercado y genera una señal
func (s *SimpleMomentumStrategy) Analyze(ctx context.Context, pair string, data *MarketData) (*models.ScalpingSignal, error) {
	if !s.enabled {
		return nil, nil
	}

	currentPrice := data.Price
	currentVolume := data.Volume

	// Si es la primera vez, solo guardar el precio
	if s.lastPrice == 0 {
		s.lastPrice = currentPrice
		s.lastVolume = currentVolume
		return nil, nil
	}

	// Calcular cambio de precio
	priceChange := (currentPrice - s.lastPrice) / s.lastPrice
	volumeChange := (currentVolume - s.lastVolume) / s.lastVolume

	// Actualizar para próxima iteración
	s.lastPrice = currentPrice
	s.lastVolume = currentVolume

	// Generar señal basada en momentum
	var direction string
	var confidence float64

	minChange := 0.001 // 0.1%
	if val, ok := s.config.Params["min_price_change"].(float64); ok {
		minChange = val
	}

	// Momentum alcista con volumen
	if priceChange > minChange && volumeChange > 0 {
		direction = "buy"
		confidence = 0.6 + (priceChange * 100) // Más confianza con mayor cambio
		if confidence > 0.9 {
			confidence = 0.9
		}
	} else if priceChange < -minChange && volumeChange > 0 {
		// Momentum bajista con volumen
		direction = "sell"
		confidence = 0.6 + (-priceChange * 100)
		if confidence > 0.9 {
			confidence = 0.9
		}
	} else {
		// Sin señal clara
		return nil, nil
	}

	// Verificar confianza mínima
	if confidence < s.config.MinConfidence {
		return nil, nil
	}

	// Crear señal
	signal := &models.ScalpingSignal{
		ID:         fmt.Sprintf("momentum_%d", time.Now().UnixNano()),
		Pair:       pair,
		Exchange:   data.Exchange,
		Direction:  direction,
		Confidence: confidence,
		Strategies: []string{s.Name()},
		Price:      currentPrice,
		Volume:     currentVolume,
		Timestamp:  time.Now(),
		Metadata: map[string]interface{}{
			"price_change":  priceChange,
			"volume_change": volumeChange,
		},
	}

	s.logger.Infof("Momentum signal for %s: %s (price change: %.4f%%, confidence: %.3f)",
		pair, direction, priceChange*100, confidence)

	return signal, nil
}

// IsEnabled retorna si la estrategia está habilitada
func (s *SimpleMomentumStrategy) IsEnabled() bool {
	return s.enabled
}

// SetEnabled habilita o deshabilita la estrategia
func (s *SimpleMomentumStrategy) SetEnabled(enabled bool) {
	s.enabled = enabled
	s.config.Enabled = enabled
}

// GetConfig retorna la configuración de la estrategia
func (s *SimpleMomentumStrategy) GetConfig() *models.StrategyConfig {
	return s.config
}

// UpdateConfig actualiza la configuración de la estrategia
func (s *SimpleMomentumStrategy) UpdateConfig(config *models.StrategyConfig) error {
	if config == nil {
		return fmt.Errorf("config cannot be nil")
	}

	s.config = config
	s.enabled = config.Enabled

	return nil
}
