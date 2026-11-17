package ai

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/scalping/models"
)

// AIPredictor maneja la predicción de movimientos de precio usando IA
type AIPredictor struct {
	logger        *logrus.Logger
	modelInfo     *models.AIModelInfo
	featureEng    *FeatureEngineer
	trainer       *ModelTrainer
	predictor     ModelPredictor
	isLoaded      bool
	minConfidence float64
}

// AIPredictorInterface define la interfaz del predictor de IA
type AIPredictorInterface interface {
	Predict(ctx context.Context, pair string, features *models.Features) (*models.Prediction, error)
	Train(ctx context.Context, data *TrainingData) error
	LoadModel(modelPath string) error
	SaveModel(modelPath string) error
	GetModelInfo() *models.AIModelInfo
	IsReady() bool
	SetMinConfidence(confidence float64)
}

// ModelPredictor interface para diferentes tipos de modelos
type ModelPredictor interface {
	Predict(features []float64) ([]float64, error)
	Train(X [][]float64, y []float64) error
	Save(path string) error
	Load(path string) error
	GetInfo() map[string]interface{}
}

// TrainingData contiene datos para entrenar el modelo
type TrainingData struct {
	Features   [][]float64 `json:"features"`
	Labels     []float64   `json:"labels"`
	Pairs      []string    `json:"pairs"`
	Timestamps []time.Time `json:"timestamps"`
}

// NewAIPredictor crea una nueva instancia del predictor de IA
func NewAIPredictor(logger *logrus.Logger) *AIPredictor {
	return &AIPredictor{
		logger:        logger,
		featureEng:    NewFeatureEngineer(logger),
		trainer:       NewModelTrainer(logger),
		isLoaded:      false,
		minConfidence: 0.65,
		modelInfo: &models.AIModelInfo{
			ModelType: "LSTM",
			Version:   "1.0.0",
			Active:    false,
		},
	}
}

// Predict genera una predicción para un par específico
func (p *AIPredictor) Predict(ctx context.Context, pair string, features *models.Features) (*models.Prediction, error) {
	if !p.isLoaded {
		return nil, fmt.Errorf("model not loaded")
	}

	start := time.Now()

	// Convertir features a array numérico
	featureArray, err := p.featureEng.ConvertToArray(features)
	if err != nil {
		return nil, fmt.Errorf("failed to convert features: %w", err)
	}

	// Normalizar features
	normalizedFeatures, err := p.featureEng.Normalize(featureArray)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize features: %w", err)
	}

	// Hacer predicción
	output, err := p.predictor.Predict(normalizedFeatures)
	if err != nil {
		return nil, fmt.Errorf("prediction failed: %w", err)
	}

	// Interpretar output del modelo
	direction, confidence, expectedMove := p.interpretOutput(output)

	// Verificar confianza mínima
	if confidence < p.minConfidence {
		direction = "neutral"
	}

	prediction := &models.Prediction{
		Direction:    direction,
		Confidence:   confidence,
		ExpectedMove: expectedMove,
		TimeHorizon:  5, // 5 minutos
		Timestamp:    time.Now(),
		ModelVersion: p.modelInfo.Version,
		Features:     p.featureEng.GetFeatureMap(features),
	}

	latency := time.Since(start)
	p.logger.Debugf("AI prediction for %s: %s (confidence: %.3f, latency: %v)",
		pair, direction, confidence, latency)

	// Verificar que la latencia sea aceptable (<50ms)
	if latency > 50*time.Millisecond {
		p.logger.Warnf("AI prediction latency too high: %v", latency)
	}

	return prediction, nil
}

// Train entrena el modelo con nuevos datos
func (p *AIPredictor) Train(ctx context.Context, data *TrainingData) error {
	p.logger.Info("Starting AI model training...")
	start := time.Now()

	if len(data.Features) == 0 || len(data.Labels) == 0 {
		return fmt.Errorf("training data is empty")
	}

	if len(data.Features) != len(data.Labels) {
		return fmt.Errorf("features and labels length mismatch")
	}

	// Preparar datos de entrenamiento
	X, y, err := p.prepareTrainingData(data)
	if err != nil {
		return fmt.Errorf("failed to prepare training data: %w", err)
	}

	// Crear nuevo modelo si no existe
	if p.predictor == nil {
		p.predictor = NewLSTMModel(p.logger)
	}

	// Entrenar modelo
	err = p.predictor.Train(X, y)
	if err != nil {
		return fmt.Errorf("training failed: %w", err)
	}

	// Actualizar información del modelo
	p.modelInfo.TrainedAt = time.Now()
	p.modelInfo.Samples = len(data.Features)
	p.modelInfo.Features = len(data.Features[0])
	p.modelInfo.Active = true
	p.isLoaded = true

	// Evaluar modelo
	accuracy, err := p.evaluateModel(X, y)
	if err != nil {
		p.logger.Warnf("Failed to evaluate model: %v", err)
	} else {
		p.modelInfo.Accuracy = accuracy
	}

	trainingTime := time.Since(start)
	p.logger.Infof("AI model training completed in %v (accuracy: %.3f, samples: %d)",
		trainingTime, p.modelInfo.Accuracy, p.modelInfo.Samples)

	return nil
}

// LoadModel carga un modelo desde disco
func (p *AIPredictor) LoadModel(modelPath string) error {
	p.logger.Infof("Loading AI model from: %s", modelPath)

	// Crear predictor si no existe
	if p.predictor == nil {
		p.predictor = NewLSTMModel(p.logger)
	}

	// Cargar modelo
	err := p.predictor.Load(modelPath)
	if err != nil {
		return fmt.Errorf("failed to load model: %w", err)
	}

	// Actualizar información
	p.modelInfo.ModelPath = modelPath
	p.modelInfo.Active = true
	p.isLoaded = true

	p.logger.Info("AI model loaded successfully")
	return nil
}

// SaveModel guarda el modelo a disco
func (p *AIPredictor) SaveModel(modelPath string) error {
	if !p.isLoaded {
		return fmt.Errorf("no model to save")
	}

	p.logger.Infof("Saving AI model to: %s", modelPath)

	err := p.predictor.Save(modelPath)
	if err != nil {
		return fmt.Errorf("failed to save model: %w", err)
	}

	p.modelInfo.ModelPath = modelPath
	p.logger.Info("AI model saved successfully")
	return nil
}

// GetModelInfo retorna información del modelo
func (p *AIPredictor) GetModelInfo() *models.AIModelInfo {
	return p.modelInfo
}

// IsReady verifica si el predictor está listo para usar
func (p *AIPredictor) IsReady() bool {
	return p.isLoaded && p.predictor != nil
}

// SetMinConfidence establece la confianza mínima
func (p *AIPredictor) SetMinConfidence(confidence float64) {
	p.minConfidence = confidence
	p.logger.Infof("AI predictor min confidence set to: %.3f", confidence)
}

// interpretOutput interpreta la salida del modelo
func (p *AIPredictor) interpretOutput(output []float64) (string, float64, float64) {
	if len(output) < 3 {
		return "neutral", 0.0, 0.0
	}

	// Asumiendo output: [prob_down, prob_neutral, prob_up]
	probDown := output[0]
	probNeutral := output[1]
	probUp := output[2]

	// Encontrar la probabilidad máxima
	maxProb := math.Max(math.Max(probDown, probNeutral), probUp)

	var direction string
	var expectedMove float64

	if maxProb == probUp {
		direction = "up"
		expectedMove = probUp * 2.0 // Movimiento esperado en %
	} else if maxProb == probDown {
		direction = "down"
		expectedMove = -probDown * 2.0
	} else {
		direction = "neutral"
		expectedMove = 0.0
	}

	confidence := maxProb

	return direction, confidence, expectedMove
}

// prepareTrainingData prepara los datos para entrenamiento
func (p *AIPredictor) prepareTrainingData(data *TrainingData) ([][]float64, []float64, error) {
	// Normalizar features
	normalizedFeatures := make([][]float64, len(data.Features))
	for i, features := range data.Features {
		normalized, err := p.featureEng.Normalize(features)
		if err != nil {
			return nil, nil, err
		}
		normalizedFeatures[i] = normalized
	}

	// Convertir labels a clasificación (0: down, 1: neutral, 2: up)
	classificationLabels := make([]float64, len(data.Labels))
	for i, label := range data.Labels {
		if label < -0.5 {
			classificationLabels[i] = 0 // down
		} else if label > 0.5 {
			classificationLabels[i] = 2 // up
		} else {
			classificationLabels[i] = 1 // neutral
		}
	}

	return normalizedFeatures, classificationLabels, nil
}

// evaluateModel evalúa la precisión del modelo
func (p *AIPredictor) evaluateModel(X [][]float64, y []float64) (float64, error) {
	if len(X) == 0 {
		return 0.0, fmt.Errorf("no data to evaluate")
	}

	correct := 0
	total := len(X)

	for i, features := range X {
		prediction, err := p.predictor.Predict(features)
		if err != nil {
			continue
		}

		// Interpretar predicción
		direction, _, _ := p.interpretOutput(prediction)

		// Convertir dirección a clase
		var predictedClass float64
		switch direction {
		case "down":
			predictedClass = 0
		case "neutral":
			predictedClass = 1
		case "up":
			predictedClass = 2
		}

		// Comparar con label real
		if predictedClass == y[i] {
			correct++
		}
	}

	accuracy := float64(correct) / float64(total)
	return accuracy, nil
}
