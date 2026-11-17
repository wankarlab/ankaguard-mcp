package strategies

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/scalping/ai"
	"github.com/rantipay/trading-mcp/internal/scalping/models"
)

// AIScalpingStrategy implementa estrategia de scalping basada en IA
type AIScalpingStrategy struct {
	logger      *logrus.Logger
	aiPredictor *ai.AIPredictor
	config      *models.StrategyConfig
	enabled     bool

	// Parámetros específicos de IA
	minConfidence float64
}

// NewAIScalpingStrategy crea una nueva estrategia de IA para scalping
func NewAIScalpingStrategy(logger *logrus.Logger, aiPredictor *ai.AIPredictor) *AIScalpingStrategy {
	return &AIScalpingStrategy{
		logger:        logger,
		aiPredictor:   aiPredictor,
		enabled:       true,
		minConfidence: 0.65,
		config: &models.StrategyConfig{
			Name:          "ai_scalping",
			Enabled:       true,
			Weight:        1.5, // Mayor peso por ser IA
			MinConfidence: 0.65,
			Params: map[string]interface{}{
				"min_confidence": 0.65,
			},
		},
	}
}

// Name retorna el nombre de la estrategia
func (s *AIScalpingStrategy) Name() string {
	return "ai_scalping"
}

// Analyze analiza el mercado y genera una señal usando IA
func (s *AIScalpingStrategy) Analyze(ctx context.Context, pair string, data *MarketData) (*models.ScalpingSignal, error) {
	if !s.enabled {
		return nil, nil
	}

	// Verificar que el predictor esté listo
	if !s.aiPredictor.IsReady() {
		s.logger.Debug("AI predictor not ready, skipping AI strategy")
		return nil, nil
	}

	// Hacer predicción con IA
	prediction, err := s.aiPredictor.Predict(ctx, pair, data.Features)
	if err != nil {
		return nil, fmt.Errorf("AI prediction failed: %w", err)
	}

	// Verificar confianza mínima
	if prediction.Confidence < s.minConfidence {
		return nil, nil
	}

	// Convertir dirección de IA a dirección de trading
	var direction string
	switch prediction.Direction {
	case "up":
		direction = "buy"
	case "down":
		direction = "sell"
	case "neutral":
		return nil, nil // No operar en neutral
	default:
		return nil, nil
	}

	// Ajustar confianza combinando con indicadores técnicos
	adjustedConfidence := s.adjustConfidenceWithIndicators(prediction.Confidence, data)

	// Verificar confianza ajustada
	if adjustedConfidence < s.config.MinConfidence {
		return nil, nil
	}

	// Crear señal
	signal := &models.ScalpingSignal{
		ID:            fmt.Sprintf("ai_%d", time.Now().UnixNano()),
		Pair:          pair,
		Exchange:      data.Exchange,
		Direction:     direction,
		Confidence:    adjustedConfidence,
		AIConfidence:  prediction.Confidence,
		AIDirection:   prediction.Direction,
		Strategies:    []string{s.Name()},
		CombinedScore: adjustedConfidence,
		Price:         data.Price,
		Volume:        data.Volume,
		Timestamp:     time.Now(),
		Metadata: map[string]interface{}{
			"ai_direction":  prediction.Direction,
			"ai_confidence": prediction.Confidence,
			"expected_move": prediction.ExpectedMove,
			"time_horizon":  prediction.TimeHorizon,
			"model_version": prediction.ModelVersion,
			"features_used": len(prediction.Features),
		},
	}

	s.logger.Debugf("AI signal for %s: %s (AI conf: %.3f, adjusted: %.3f, expected: %.2f%%)",
		pair, direction, prediction.Confidence, adjustedConfidence, prediction.ExpectedMove)

	return signal, nil
}

// adjustConfidenceWithIndicators ajusta la confianza de IA con indicadores técnicos
func (s *AIScalpingStrategy) adjustConfidenceWithIndicators(aiConfidence float64, data *MarketData) float64 {
	confidence := aiConfidence

	// Factor 1: RSI
	if data.Features.RSI > 0 {
		// RSI en zona extrema aumenta confianza
		if data.Features.RSI < 30 || data.Features.RSI > 70 {
			confidence *= 1.1
		}
	}

	// Factor 2: MACD
	if data.Features.MACDHist != 0 {
		// MACD histogram fuerte aumenta confianza
		histStrength := data.Features.MACDHist
		if histStrength < 0 {
			histStrength = -histStrength
		}
		if histStrength > 0.01 {
			confidence *= 1.05
		}
	}

	// Factor 3: Volumen
	if data.Features.VolumeRatio > 1.5 {
		// Alto volumen aumenta confianza
		confidence *= 1.1
	} else if data.Features.VolumeRatio < 0.5 {
		// Bajo volumen reduce confianza
		confidence *= 0.9
	}

	// Factor 4: Volatilidad
	if data.Features.Volatility > 0 {
		// Alta volatilidad reduce confianza
		if data.Features.Volatility > 0.05 {
			confidence *= 0.9
		}
	}

	// Limitar confianza entre 0 y 1
	if confidence > 1.0 {
		confidence = 1.0
	}
	if confidence < 0.0 {
		confidence = 0.0
	}

	return confidence
}

// IsEnabled retorna si la estrategia está habilitada
func (s *AIScalpingStrategy) IsEnabled() bool {
	return s.enabled && s.aiPredictor.IsReady()
}

// SetEnabled habilita o deshabilita la estrategia
func (s *AIScalpingStrategy) SetEnabled(enabled bool) {
	s.enabled = enabled
	s.config.Enabled = enabled
}

// GetConfig retorna la configuración de la estrategia
func (s *AIScalpingStrategy) GetConfig() *models.StrategyConfig {
	return s.config
}

// UpdateConfig actualiza la configuración de la estrategia
func (s *AIScalpingStrategy) UpdateConfig(config *models.StrategyConfig) error {
	if config == nil {
		return fmt.Errorf("config cannot be nil")
	}

	// Actualizar parámetros desde config
	if minConf, ok := config.Params["min_confidence"].(float64); ok {
		s.minConfidence = minConf
		s.aiPredictor.SetMinConfidence(minConf)
	}

	s.config = config
	s.enabled = config.Enabled

	s.logger.Infof("Updated AI strategy config: min_confidence=%.2f", s.minConfidence)

	return nil
}
