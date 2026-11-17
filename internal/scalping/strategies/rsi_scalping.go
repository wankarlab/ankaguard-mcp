package strategies

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/indicators"
	"github.com/rantipay/trading-mcp/internal/scalping/models"
)

// RSIScalpingStrategy implementa estrategia de scalping basada en RSI
type RSIScalpingStrategy struct {
	logger    *logrus.Logger
	gctClient gct.GCTClient
	config    *models.StrategyConfig
	enabled   bool

	// Parámetros específicos de RSI
	period     int
	oversold   float64
	overbought float64
}

// NewRSIScalpingStrategy crea una nueva estrategia RSI para scalping
func NewRSIScalpingStrategy(logger *logrus.Logger, gctClient gct.GCTClient) *RSIScalpingStrategy {
	return &RSIScalpingStrategy{
		logger:     logger,
		gctClient:  gctClient,
		enabled:    true,
		period:     7,    // Período más corto para scalping (vs 14 normal)
		oversold:   35.0, // Más agresivo que 30
		overbought: 65.0, // Más agresivo que 70
		config: &models.StrategyConfig{
			Name:          "rsi_scalping",
			Enabled:       true,
			Weight:        1.0,
			MinConfidence: 0.6,
			Params: map[string]interface{}{
				"period":     7,
				"oversold":   35.0,
				"overbought": 65.0,
			},
		},
	}
}

// Name retorna el nombre de la estrategia
func (s *RSIScalpingStrategy) Name() string {
	return "rsi_scalping"
}

// Analyze analiza el mercado y genera una señal
func (s *RSIScalpingStrategy) Analyze(ctx context.Context, pair string, data *MarketData) (*models.ScalpingSignal, error) {
	if !s.enabled {
		return nil, nil
	}

	// Calcular RSI usando los precios históricos
	rsi := data.Features.RSI
	if rsi == 0 {
		// Calcular RSI manualmente desde los precios
		if len(data.Features.Prices) < s.period+1 {
			return nil, fmt.Errorf("insufficient price data for RSI calculation (need %d, got %d)", s.period+1, len(data.Features.Prices))
		}
		rsi = indicators.CalculateRSI(data.Features.Prices, s.period)
	}

	// Generar señal basada en RSI
	var direction string
	var confidence float64

	if rsi <= s.oversold {
		// Sobreventa - señal de compra
		direction = "buy"
		// Confianza aumenta cuanto más bajo el RSI
		confidence = 1.0 - (rsi / s.oversold)
		if confidence > 1.0 {
			confidence = 1.0
		}
	} else if rsi >= s.overbought {
		// Sobrecompra - señal de venta
		direction = "sell"
		// Confianza aumenta cuanto más alto el RSI
		confidence = (rsi - s.overbought) / (100 - s.overbought)
		if confidence > 1.0 {
			confidence = 1.0
		}
	} else {
		// Zona neutral - no operar
		return nil, nil
	}

	// Verificar confianza mínima
	if confidence < s.config.MinConfidence {
		return nil, nil
	}

	// Ajustar confianza basado en volatilidad
	if data.Features.Volatility > 0 {
		// Reducir confianza en alta volatilidad
		volatilityFactor := 1.0 - (data.Features.Volatility * 0.5)
		if volatilityFactor < 0.5 {
			volatilityFactor = 0.5
		}
		confidence *= volatilityFactor
	}

	// Crear señal
	signal := &models.ScalpingSignal{
		ID:         fmt.Sprintf("rsi_%d", time.Now().UnixNano()),
		Pair:       pair,
		Exchange:   data.Exchange,
		Direction:  direction,
		Confidence: confidence,
		Strategies: []string{s.Name()},
		Price:      data.Price,
		Volume:     data.Volume,
		Timestamp:  time.Now(),
		Metadata: map[string]interface{}{
			"rsi":        rsi,
			"period":     s.period,
			"oversold":   s.oversold,
			"overbought": s.overbought,
		},
	}

	s.logger.Debugf("RSI signal for %s: %s (RSI: %.2f, confidence: %.3f)",
		pair, direction, rsi, confidence)

	return signal, nil
}

// IsEnabled retorna si la estrategia está habilitada
func (s *RSIScalpingStrategy) IsEnabled() bool {
	return s.enabled
}

// SetEnabled habilita o deshabilita la estrategia
func (s *RSIScalpingStrategy) SetEnabled(enabled bool) {
	s.enabled = enabled
	s.config.Enabled = enabled
}

// GetConfig retorna la configuración de la estrategia
func (s *RSIScalpingStrategy) GetConfig() *models.StrategyConfig {
	return s.config
}

// UpdateConfig actualiza la configuración de la estrategia
func (s *RSIScalpingStrategy) UpdateConfig(config *models.StrategyConfig) error {
	if config == nil {
		return fmt.Errorf("config cannot be nil")
	}

	// Actualizar parámetros desde config
	if period, ok := config.Params["period"].(float64); ok {
		s.period = int(period)
	}
	if oversold, ok := config.Params["oversold"].(float64); ok {
		s.oversold = oversold
	}
	if overbought, ok := config.Params["overbought"].(float64); ok {
		s.overbought = overbought
	}

	s.config = config
	s.enabled = config.Enabled

	s.logger.Infof("Updated RSI strategy config: period=%d, oversold=%.1f, overbought=%.1f",
		s.period, s.oversold, s.overbought)

	return nil
}

