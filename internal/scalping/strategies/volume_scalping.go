package strategies

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/scalping/models"
)

// VolumeScalpingStrategy implementa estrategia de scalping basada en picos de volumen
type VolumeScalpingStrategy struct {
	logger    *logrus.Logger
	gctClient gct.GCTClient
	config    *models.StrategyConfig
	enabled   bool

	// Parámetros específicos de volumen
	threshold float64 // Multiplicador de volumen promedio
}

// NewVolumeScalpingStrategy crea una nueva estrategia de volumen para scalping
func NewVolumeScalpingStrategy(logger *logrus.Logger, gctClient gct.GCTClient) *VolumeScalpingStrategy {
	return &VolumeScalpingStrategy{
		logger:    logger,
		gctClient: gctClient,
		enabled:   true,
		threshold: 2.5, // Volumen debe ser 2.5x el promedio
		config: &models.StrategyConfig{
			Name:          "volume_scalping",
			Enabled:       true,
			Weight:        1.0,
			MinConfidence: 0.6,
			Params: map[string]interface{}{
				"threshold": 2.5,
			},
		},
	}
}

// Name retorna el nombre de la estrategia
func (s *VolumeScalpingStrategy) Name() string {
	return "volume_scalping"
}

// Analyze analiza el mercado y genera una señal
func (s *VolumeScalpingStrategy) Analyze(ctx context.Context, pair string, data *MarketData) (*models.ScalpingSignal, error) {
	if !s.enabled {
		return nil, nil
	}

	// Obtener volumen actual y ratio
	currentVolume := data.Volume
	volumeRatio := data.Features.VolumeRatio

	if currentVolume == 0 || volumeRatio == 0 {
		return nil, nil
	}

	// Verificar si hay pico de volumen
	if volumeRatio < s.threshold {
		return nil, nil // No hay pico significativo
	}

	// Determinar dirección basada en cambio de precio
	priceChange := data.Features.PriceChange
	var direction string
	var confidence float64

	if priceChange > 0 {
		// Volumen alto con precio subiendo - señal alcista
		direction = "buy"
	} else if priceChange < 0 {
		// Volumen alto con precio bajando - señal bajista
		direction = "sell"
	} else {
		// Sin cambio de precio claro
		return nil, nil
	}

	// Calcular confianza basada en:
	// 1. Magnitud del pico de volumen
	// 2. Magnitud del cambio de precio
	volumeStrength := (volumeRatio - s.threshold) / s.threshold
	if volumeStrength > 1.0 {
		volumeStrength = 1.0
	}

	priceStrength := 0.0
	if priceChange != 0 {
		priceStrength = priceChange / 2.0 // Normalizar
		if priceStrength < 0 {
			priceStrength = -priceStrength
		}
		if priceStrength > 1.0 {
			priceStrength = 1.0
		}
	}

	// Combinar ambos factores
	confidence = (volumeStrength*0.6 + priceStrength*0.4)

	// Verificar confianza mínima
	if confidence < s.config.MinConfidence {
		return nil, nil
	}

	// Ajustar confianza basado en momentum
	if data.Features.Momentum != 0 {
		// Momentum en la misma dirección aumenta confianza
		if (direction == "buy" && data.Features.Momentum > 0) ||
			(direction == "sell" && data.Features.Momentum < 0) {
			confidence *= 1.2
			if confidence > 1.0 {
				confidence = 1.0
			}
		}
	}

	// Crear señal
	signal := &models.ScalpingSignal{
		ID:         fmt.Sprintf("vol_%d", time.Now().UnixNano()),
		Pair:       pair,
		Exchange:   data.Exchange,
		Direction:  direction,
		Confidence: confidence,
		Strategies: []string{s.Name()},
		Price:      data.Price,
		Volume:     data.Volume,
		Timestamp:  time.Now(),
		Metadata: map[string]interface{}{
			"volume":          currentVolume,
			"volume_ratio":    volumeRatio,
			"price_change":    priceChange,
			"threshold":       s.threshold,
			"volume_strength": volumeStrength,
			"price_strength":  priceStrength,
		},
	}

	s.logger.Debugf("Volume signal for %s: %s (ratio: %.2fx, confidence: %.3f)",
		pair, direction, volumeRatio, confidence)

	return signal, nil
}

// IsEnabled retorna si la estrategia está habilitada
func (s *VolumeScalpingStrategy) IsEnabled() bool {
	return s.enabled
}

// SetEnabled habilita o deshabilita la estrategia
func (s *VolumeScalpingStrategy) SetEnabled(enabled bool) {
	s.enabled = enabled
	s.config.Enabled = enabled
}

// GetConfig retorna la configuración de la estrategia
func (s *VolumeScalpingStrategy) GetConfig() *models.StrategyConfig {
	return s.config
}

// UpdateConfig actualiza la configuración de la estrategia
func (s *VolumeScalpingStrategy) UpdateConfig(config *models.StrategyConfig) error {
	if config == nil {
		return fmt.Errorf("config cannot be nil")
	}

	// Actualizar parámetros desde config
	if threshold, ok := config.Params["threshold"].(float64); ok {
		s.threshold = threshold
	}

	s.config = config
	s.enabled = config.Enabled

	s.logger.Infof("Updated Volume strategy config: threshold=%.1f", s.threshold)

	return nil
}
