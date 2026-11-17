package strategies

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/scalping/models"
)

// BollingerScalpingStrategy implementa estrategia de scalping basada en Bollinger Bands
type BollingerScalpingStrategy struct {
	logger    *logrus.Logger
	gctClient gct.GCTClient
	config    *models.StrategyConfig
	enabled   bool

	// Parámetros específicos de Bollinger
	period int
	stdDev float64
}

// NewBollingerScalpingStrategy crea una nueva estrategia Bollinger para scalping
func NewBollingerScalpingStrategy(logger *logrus.Logger, gctClient gct.GCTClient) *BollingerScalpingStrategy {
	return &BollingerScalpingStrategy{
		logger:    logger,
		gctClient: gctClient,
		enabled:   true,
		period:    15,  // Período más corto para scalping
		stdDev:    2.0, // Desviación estándar normal
		config: &models.StrategyConfig{
			Name:          "bollinger_scalping",
			Enabled:       true,
			Weight:        1.0,
			MinConfidence: 0.6,
			Params: map[string]interface{}{
				"period":  15,
				"std_dev": 2.0,
			},
		},
	}
}

// Name retorna el nombre de la estrategia
func (s *BollingerScalpingStrategy) Name() string {
	return "bollinger_scalping"
}

// Analyze analiza el mercado y genera una señal
func (s *BollingerScalpingStrategy) Analyze(ctx context.Context, pair string, data *MarketData) (*models.ScalpingSignal, error) {
	if !s.enabled {
		return nil, nil
	}

	// Obtener Bollinger Bands
	bbUpper := data.Features.BBUpper
	bbMiddle := data.Features.BBMiddle
	bbLower := data.Features.BBLower
	currentPrice := data.Price

	// Si no vienen en features, calcularlos
	if bbUpper == 0 && bbMiddle == 0 && bbLower == 0 {
		bbData, err := s.gctClient.CalculateBollingerBands(ctx, data.Exchange, pair, s.period, s.stdDev)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate Bollinger Bands: %w", err)
		}

		bbUpper = bbData["upper"]
		bbMiddle = bbData["middle"]
		bbLower = bbData["lower"]
	}

	if bbUpper == 0 || bbLower == 0 {
		return nil, fmt.Errorf("invalid Bollinger Bands values")
	}

	// Calcular posición del precio en las bandas
	bandWidth := bbUpper - bbLower
	if bandWidth == 0 {
		return nil, nil
	}

	pricePosition := (currentPrice - bbLower) / bandWidth

	var direction string
	var confidence float64

	// Bounce en banda inferior - señal de compra
	if pricePosition <= 0.1 {
		direction = "buy"
		// Confianza aumenta cuanto más cerca de la banda inferior
		confidence = 1.0 - (pricePosition / 0.1)
	} else if pricePosition >= 0.9 {
		// Bounce en banda superior - señal de venta
		direction = "sell"
		// Confianza aumenta cuanto más cerca de la banda superior
		confidence = (pricePosition - 0.9) / 0.1
	} else {
		// Precio en zona media - no operar
		return nil, nil
	}

	// Limitar confianza
	if confidence > 1.0 {
		confidence = 1.0
	}

	// Verificar confianza mínima
	if confidence < s.config.MinConfidence {
		return nil, nil
	}

	// Ajustar confianza basado en ancho de banda (volatilidad)
	// Bandas más anchas = mayor volatilidad = menor confianza
	avgPrice := (bbUpper + bbLower) / 2
	if avgPrice > 0 {
		bandWidthPercent := bandWidth / avgPrice
		volatilityFactor := 1.0 - (bandWidthPercent * 5.0)
		if volatilityFactor < 0.5 {
			volatilityFactor = 0.5
		}
		if volatilityFactor > 1.0 {
			volatilityFactor = 1.0
		}
		confidence *= volatilityFactor
	}

	// Crear señal
	signal := &models.ScalpingSignal{
		ID:         fmt.Sprintf("bb_%d", time.Now().UnixNano()),
		Pair:       pair,
		Exchange:   data.Exchange,
		Direction:  direction,
		Confidence: confidence,
		Strategies: []string{s.Name()},
		Price:      data.Price,
		Volume:     data.Volume,
		Timestamp:  time.Now(),
		Metadata: map[string]interface{}{
			"bb_upper":       bbUpper,
			"bb_middle":      bbMiddle,
			"bb_lower":       bbLower,
			"price_position": pricePosition,
			"band_width":     bandWidth,
			"period":         s.period,
			"std_dev":        s.stdDev,
		},
	}

	s.logger.Debugf("Bollinger signal for %s: %s (position: %.3f, confidence: %.3f)",
		pair, direction, pricePosition, confidence)

	return signal, nil
}

// IsEnabled retorna si la estrategia está habilitada
func (s *BollingerScalpingStrategy) IsEnabled() bool {
	return s.enabled
}

// SetEnabled habilita o deshabilita la estrategia
func (s *BollingerScalpingStrategy) SetEnabled(enabled bool) {
	s.enabled = enabled
	s.config.Enabled = enabled
}

// GetConfig retorna la configuración de la estrategia
func (s *BollingerScalpingStrategy) GetConfig() *models.StrategyConfig {
	return s.config
}

// UpdateConfig actualiza la configuración de la estrategia
func (s *BollingerScalpingStrategy) UpdateConfig(config *models.StrategyConfig) error {
	if config == nil {
		return fmt.Errorf("config cannot be nil")
	}

	// Actualizar parámetros desde config
	if period, ok := config.Params["period"].(float64); ok {
		s.period = int(period)
	}
	if stdDev, ok := config.Params["std_dev"].(float64); ok {
		s.stdDev = stdDev
	}

	s.config = config
	s.enabled = config.Enabled

	s.logger.Infof("Updated Bollinger strategy config: period=%d, std_dev=%.1f",
		s.period, s.stdDev)

	return nil
}
