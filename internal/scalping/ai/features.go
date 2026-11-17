package ai

import (
	"fmt"
	"math"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/scalping/models"
)

// FeatureEngineer maneja la ingeniería de características para el modelo de IA
type FeatureEngineer struct {
	logger      *logrus.Logger
	normalizers map[string]*Normalizer
}

// Normalizer para normalizar características
type Normalizer struct {
	Mean   float64 `json:"mean"`
	StdDev float64 `json:"std_dev"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
	Method string  `json:"method"` // "zscore", "minmax"
}

// NewFeatureEngineer crea una nueva instancia del feature engineer
func NewFeatureEngineer(logger *logrus.Logger) *FeatureEngineer {
	return &FeatureEngineer{
		logger:      logger,
		normalizers: make(map[string]*Normalizer),
	}
}

// ConvertToArray convierte Features a array numérico
func (fe *FeatureEngineer) ConvertToArray(features *models.Features) ([]float64, error) {
	if features == nil {
		return nil, fmt.Errorf("features is nil")
	}

	// Crear array con todas las características
	var result []float64

	// Características de precio (últimos 10 valores)
	priceFeatures := fe.extractPriceFeatures(features.Prices)
	result = append(result, priceFeatures...)

	// Características de volumen (últimos 10 valores)
	volumeFeatures := fe.extractVolumeFeatures(features.Volumes)
	result = append(result, volumeFeatures...)

	// Indicadores técnicos
	result = append(result, features.RSI)
	result = append(result, features.MACD)
	result = append(result, features.MACDSignal)
	result = append(result, features.MACDHist)
	result = append(result, features.BBUpper)
	result = append(result, features.BBMiddle)
	result = append(result, features.BBLower)
	result = append(result, features.ATR)
	result = append(result, features.Spread)
	result = append(result, features.Volatility)

	// Características temporales
	result = append(result, float64(features.TimeOfDay))
	result = append(result, float64(features.DayOfWeek))

	// Características derivadas
	result = append(result, features.VolumeRatio)
	result = append(result, features.PriceChange)
	result = append(result, features.Momentum)

	// Características adicionales calculadas
	additionalFeatures := fe.calculateAdditionalFeatures(features)
	result = append(result, additionalFeatures...)

	return result, nil
}

// Normalize normaliza un array de características
func (fe *FeatureEngineer) Normalize(features []float64) ([]float64, error) {
	if len(features) == 0 {
		return nil, fmt.Errorf("empty features array")
	}

	normalized := make([]float64, len(features))

	for i, value := range features {
		// Usar normalización Z-score por defecto
		normalized[i] = fe.zScoreNormalize(value, i)
	}

	return normalized, nil
}

// GetFeatureMap convierte Features a mapa para logging/debugging
func (fe *FeatureEngineer) GetFeatureMap(features *models.Features) map[string]float64 {
	return map[string]float64{
		"rsi":          features.RSI,
		"macd":         features.MACD,
		"macd_signal":  features.MACDSignal,
		"macd_hist":    features.MACDHist,
		"bb_upper":     features.BBUpper,
		"bb_middle":    features.BBMiddle,
		"bb_lower":     features.BBLower,
		"atr":          features.ATR,
		"spread":       features.Spread,
		"volatility":   features.Volatility,
		"volume_ratio": features.VolumeRatio,
		"price_change": features.PriceChange,
		"momentum":     features.Momentum,
	}
}

// extractPriceFeatures extrae características de los precios
func (fe *FeatureEngineer) extractPriceFeatures(prices []float64) []float64 {
	if len(prices) == 0 {
		return make([]float64, 10) // Retornar zeros si no hay datos
	}

	// Tomar últimos 10 valores o rellenar con el último valor
	features := make([]float64, 10)
	for i := 0; i < 10; i++ {
		if i < len(prices) {
			features[i] = prices[len(prices)-1-i] // Orden inverso (más reciente primero)
		} else {
			features[i] = prices[0] // Rellenar con el primer valor
		}
	}

	// Normalizar precios (diferencias porcentuales)
	for i := 1; i < len(features); i++ {
		if features[i] != 0 {
			features[i] = (features[i-1] - features[i]) / features[i] * 100
		}
	}

	return features
}

// extractVolumeFeatures extrae características de los volúmenes
func (fe *FeatureEngineer) extractVolumeFeatures(volumes []float64) []float64 {
	if len(volumes) == 0 {
		return make([]float64, 10)
	}

	// Tomar últimos 10 valores
	features := make([]float64, 10)
	for i := 0; i < 10; i++ {
		if i < len(volumes) {
			features[i] = volumes[len(volumes)-1-i]
		} else {
			features[i] = volumes[0]
		}
	}

	// Normalizar volúmenes (log transform)
	for i := 0; i < len(features); i++ {
		if features[i] > 0 {
			features[i] = math.Log(features[i] + 1)
		}
	}

	return features
}

// calculateAdditionalFeatures calcula características adicionales
func (fe *FeatureEngineer) calculateAdditionalFeatures(features *models.Features) []float64 {
	var additional []float64

	// Bollinger Band position (0-1)
	if features.BBUpper != features.BBLower {
		bbPosition := (features.BBMiddle - features.BBLower) / (features.BBUpper - features.BBLower)
		additional = append(additional, bbPosition)
	} else {
		additional = append(additional, 0.5)
	}

	// RSI momentum (diferencia con 50)
	rsiMomentum := (features.RSI - 50) / 50
	additional = append(additional, rsiMomentum)

	// MACD strength
	if features.MACDSignal != 0 {
		macdStrength := features.MACD / features.MACDSignal
		additional = append(additional, macdStrength)
	} else {
		additional = append(additional, 0.0)
	}

	// Volatility ratio (ATR vs precio)
	if len(features.Prices) > 0 && features.Prices[len(features.Prices)-1] != 0 {
		volatilityRatio := features.ATR / features.Prices[len(features.Prices)-1]
		additional = append(additional, volatilityRatio)
	} else {
		additional = append(additional, 0.0)
	}

	// Spread ratio
	if len(features.Prices) > 0 && features.Prices[len(features.Prices)-1] != 0 {
		spreadRatio := features.Spread / features.Prices[len(features.Prices)-1]
		additional = append(additional, spreadRatio)
	} else {
		additional = append(additional, 0.0)
	}

	// Time features (sin/cos encoding)
	timeOfDayRad := float64(features.TimeOfDay) * 2 * math.Pi / 24
	additional = append(additional, math.Sin(timeOfDayRad))
	additional = append(additional, math.Cos(timeOfDayRad))

	dayOfWeekRad := float64(features.DayOfWeek) * 2 * math.Pi / 7
	additional = append(additional, math.Sin(dayOfWeekRad))
	additional = append(additional, math.Cos(dayOfWeekRad))

	return additional
}

// zScoreNormalize normaliza usando Z-score
func (fe *FeatureEngineer) zScoreNormalize(value float64, featureIndex int) float64 {
	// Por simplicidad, usar normalización básica
	// En producción, se deberían calcular mean/std de datos de entrenamiento

	// Normalización simple para diferentes tipos de características
	switch {
	case featureIndex < 10: // Price features
		return math.Tanh(value / 5.0) // Limitar cambios de precio a ±5%
	case featureIndex < 20: // Volume features
		return math.Tanh(value / 10.0)
	case featureIndex == 20: // RSI
		return (value - 50) / 50 // Normalizar RSI a [-1, 1]
	case featureIndex >= 21 && featureIndex <= 23: // MACD
		return math.Tanh(value / 0.1)
	case featureIndex >= 24 && featureIndex <= 26: // Bollinger Bands
		return math.Tanh(value / 1000) // Ajustar según el rango de precios
	case featureIndex == 27: // ATR
		return math.Tanh(value / 100)
	case featureIndex == 28: // Spread
		return math.Tanh(value / 0.01) // Spread típico 0.1%
	case featureIndex == 29: // Volatility
		return math.Tanh(value / 0.05) // Volatilidad típica 5%
	default:
		return math.Tanh(value) // Normalización general
	}
}

// FitNormalizers ajusta los normalizadores con datos de entrenamiento
func (fe *FeatureEngineer) FitNormalizers(trainingData [][]float64) error {
	if len(trainingData) == 0 {
		return fmt.Errorf("no training data provided")
	}

	numFeatures := len(trainingData[0])
	fe.normalizers = make(map[string]*Normalizer)

	// Calcular estadísticas para cada característica
	for featureIdx := 0; featureIdx < numFeatures; featureIdx++ {
		values := make([]float64, len(trainingData))
		for i, sample := range trainingData {
			if featureIdx < len(sample) {
				values[i] = sample[featureIdx]
			}
		}

		// Calcular estadísticas
		mean, stdDev := fe.calculateStats(values)
		min, max := fe.calculateMinMax(values)

		fe.normalizers[fmt.Sprintf("feature_%d", featureIdx)] = &Normalizer{
			Mean:   mean,
			StdDev: stdDev,
			Min:    min,
			Max:    max,
			Method: "zscore",
		}
	}

	fe.logger.Infof("Fitted normalizers for %d features", numFeatures)
	return nil
}

// calculateStats calcula media y desviación estándar
func (fe *FeatureEngineer) calculateStats(values []float64) (float64, float64) {
	if len(values) == 0 {
		return 0.0, 1.0
	}

	// Calcular media
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	mean := sum / float64(len(values))

	// Calcular desviación estándar
	sumSquares := 0.0
	for _, v := range values {
		diff := v - mean
		sumSquares += diff * diff
	}
	stdDev := math.Sqrt(sumSquares / float64(len(values)))

	if stdDev == 0 {
		stdDev = 1.0 // Evitar división por cero
	}

	return mean, stdDev
}

// calculateMinMax calcula valores mínimo y máximo
func (fe *FeatureEngineer) calculateMinMax(values []float64) (float64, float64) {
	if len(values) == 0 {
		return 0.0, 1.0
	}

	min := values[0]
	max := values[0]

	for _, v := range values {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}

	return min, max
}
