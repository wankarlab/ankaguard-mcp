package ai

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"

	"github.com/sirupsen/logrus"
)

// LSTMModel implementa un modelo LSTM simplificado para predicción
// Nota: Esta es una implementación básica. En producción se usaría TensorFlow/PyTorch vía CGo o API
type LSTMModel struct {
	logger     *logrus.Logger
	inputSize  int
	hiddenSize int
	outputSize int
	weights    *LSTMWeights
	trained    bool
	version    string
}

// LSTMWeights contiene los pesos del modelo LSTM (simplificado)
type LSTMWeights struct {
	// Pesos de entrada a hidden
	Wih [][]float64 `json:"wih"`
	Bih []float64   `json:"bih"`

	// Pesos de hidden a output
	Who [][]float64 `json:"who"`
	Bho []float64   `json:"bho"`

	// Pesos recurrentes (simplificado)
	Whh [][]float64 `json:"whh"`
}

// NewLSTMModel crea un nuevo modelo LSTM
func NewLSTMModel(logger *logrus.Logger) *LSTMModel {
	model := &LSTMModel{
		logger:     logger,
		inputSize:  50, // Número de características de entrada
		hiddenSize: 64, // Tamaño del estado oculto
		outputSize: 3,  // 3 clases: down, neutral, up
		trained:    false,
		version:    "1.0.0",
	}

	// Inicializar pesos aleatoriamente
	model.initializeWeights()

	return model
}

// initializeWeights inicializa los pesos del modelo
func (m *LSTMModel) initializeWeights() {
	m.weights = &LSTMWeights{
		Wih: m.randomMatrix(m.hiddenSize, m.inputSize),
		Bih: m.randomVector(m.hiddenSize),
		Who: m.randomMatrix(m.outputSize, m.hiddenSize),
		Bho: m.randomVector(m.outputSize),
		Whh: m.randomMatrix(m.hiddenSize, m.hiddenSize),
	}
}

// Predict hace una predicción usando el modelo
func (m *LSTMModel) Predict(features []float64) ([]float64, error) {
	if !m.trained {
		// Permitir predicciones con modelo no entrenado para testing
		m.logger.Warn("Making prediction with untrained model")
	}

	if len(features) != m.inputSize {
		return nil, fmt.Errorf("input size mismatch: expected %d, got %d", m.inputSize, len(features))
	}

	// Forward pass simplificado
	// Input -> Hidden
	hidden := make([]float64, m.hiddenSize)
	for i := 0; i < m.hiddenSize; i++ {
		sum := m.weights.Bih[i]
		for j := 0; j < m.inputSize; j++ {
			sum += m.weights.Wih[i][j] * features[j]
		}
		hidden[i] = m.tanh(sum)
	}

	// Hidden -> Output
	output := make([]float64, m.outputSize)
	for i := 0; i < m.outputSize; i++ {
		sum := m.weights.Bho[i]
		for j := 0; j < m.hiddenSize; j++ {
			sum += m.weights.Who[i][j] * hidden[j]
		}
		output[i] = sum
	}

	// Aplicar softmax
	output = m.softmax(output)

	return output, nil
}

// Train entrena el modelo (implementación simplificada)
func (m *LSTMModel) Train(X [][]float64, y []float64) error {
	if len(X) == 0 || len(y) == 0 {
		return fmt.Errorf("empty training data")
	}

	if len(X) != len(y) {
		return fmt.Errorf("X and y length mismatch")
	}

	m.logger.Infof("Training LSTM model with %d samples...", len(X))

	// Implementación simplificada de entrenamiento
	// En producción, esto sería reemplazado por TensorFlow/PyTorch

	learningRate := 0.001
	epochs := 10

	for epoch := 0; epoch < epochs; epoch++ {
		totalLoss := 0.0

		for i := 0; i < len(X); i++ {
			// Forward pass
			prediction, err := m.Predict(X[i])
			if err != nil {
				continue
			}

			// Calcular loss (cross-entropy)
			targetClass := int(y[i])
			loss := -math.Log(prediction[targetClass] + 1e-10)
			totalLoss += loss

			// Backward pass simplificado (gradient descent)
			// Actualizar pesos basado en el error
			m.updateWeights(X[i], prediction, targetClass, learningRate)
		}

		avgLoss := totalLoss / float64(len(X))
		if epoch%5 == 0 {
			m.logger.Debugf("Epoch %d/%d - Loss: %.4f", epoch+1, epochs, avgLoss)
		}
	}

	m.trained = true
	m.logger.Info("LSTM model training completed")

	return nil
}

// updateWeights actualiza los pesos del modelo (simplificado)
func (m *LSTMModel) updateWeights(input []float64, prediction []float64, targetClass int, lr float64) {
	// Crear target one-hot
	target := make([]float64, m.outputSize)
	target[targetClass] = 1.0

	// Calcular gradiente de salida
	outputGrad := make([]float64, m.outputSize)
	for i := 0; i < m.outputSize; i++ {
		outputGrad[i] = prediction[i] - target[i]
	}

	// Actualizar pesos de salida (simplificado)
	for i := 0; i < m.outputSize; i++ {
		m.weights.Bho[i] -= lr * outputGrad[i]
		for j := 0; j < m.hiddenSize; j++ {
			// Gradient descent simplificado
			m.weights.Who[i][j] -= lr * outputGrad[i] * 0.1
		}
	}

	// Actualizar pesos de entrada (muy simplificado)
	for i := 0; i < m.hiddenSize; i++ {
		m.weights.Bih[i] -= lr * 0.01
		for j := 0; j < m.inputSize; j++ {
			m.weights.Wih[i][j] -= lr * 0.001 * input[j]
		}
	}
}

// Save guarda el modelo a disco
func (m *LSTMModel) Save(path string) error {
	m.logger.Infof("Saving LSTM model to: %s", path)

	data, err := json.MarshalIndent(m.weights, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal weights: %w", err)
	}

	err = os.WriteFile(path, data, 0644)
	if err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	m.logger.Info("LSTM model saved successfully")
	return nil
}

// Load carga el modelo desde disco
func (m *LSTMModel) Load(path string) error {
	m.logger.Infof("Loading LSTM model from: %s", path)

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	weights := &LSTMWeights{}
	err = json.Unmarshal(data, weights)
	if err != nil {
		return fmt.Errorf("failed to unmarshal weights: %w", err)
	}

	m.weights = weights
	m.trained = true

	m.logger.Info("LSTM model loaded successfully")
	return nil
}

// GetInfo retorna información del modelo
func (m *LSTMModel) GetInfo() map[string]interface{} {
	return map[string]interface{}{
		"type":        "LSTM",
		"version":     m.version,
		"input_size":  m.inputSize,
		"hidden_size": m.hiddenSize,
		"output_size": m.outputSize,
		"trained":     m.trained,
	}
}

// Helper functions

func (m *LSTMModel) randomMatrix(rows, cols int) [][]float64 {
	matrix := make([][]float64, rows)
	for i := 0; i < rows; i++ {
		matrix[i] = make([]float64, cols)
		for j := 0; j < cols; j++ {
			// Xavier initialization
			matrix[i][j] = (rand.Float64()*2 - 1) * math.Sqrt(6.0/float64(rows+cols))
		}
	}
	return matrix
}

func (m *LSTMModel) randomVector(size int) []float64 {
	vector := make([]float64, size)
	for i := 0; i < size; i++ {
		vector[i] = (rand.Float64()*2 - 1) * 0.1
	}
	return vector
}

func (m *LSTMModel) tanh(x float64) float64 {
	return math.Tanh(x)
}

func (m *LSTMModel) softmax(x []float64) []float64 {
	// Encontrar el máximo para estabilidad numérica
	max := x[0]
	for _, val := range x {
		if val > max {
			max = val
		}
	}

	// Calcular exp(x - max)
	expSum := 0.0
	result := make([]float64, len(x))
	for i, val := range x {
		result[i] = math.Exp(val - max)
		expSum += result[i]
	}

	// Normalizar
	for i := range result {
		result[i] /= expSum
	}

	return result
}

// sigmoid función de activación
func (m *LSTMModel) sigmoid(x float64) float64 {
	return 1.0 / (1.0 + math.Exp(-x))
}
