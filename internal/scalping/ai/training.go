package ai

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/sirupsen/logrus"
)

// ModelTrainer maneja el entrenamiento de modelos de IA
type ModelTrainer struct {
	logger *logrus.Logger
}

// TrainingConfig contiene configuración para el entrenamiento
type TrainingConfig struct {
	Epochs          int     `json:"epochs"`
	BatchSize       int     `json:"batch_size"`
	LearningRate    float64 `json:"learning_rate"`
	ValidationSplit float64 `json:"validation_split"`
	EarlyStopping   bool    `json:"early_stopping"`
	Patience        int     `json:"patience"`
}

// TrainingResult contiene resultados del entrenamiento
type TrainingResult struct {
	FinalLoss      float64       `json:"final_loss"`
	ValidationLoss float64       `json:"validation_loss"`
	Accuracy       float64       `json:"accuracy"`
	Epochs         int           `json:"epochs"`
	TrainingTime   time.Duration `json:"training_time"`
	LossHistory    []float64     `json:"loss_history"`
	ValLossHistory []float64     `json:"val_loss_history"`
}

// NewModelTrainer crea una nueva instancia del trainer
func NewModelTrainer(logger *logrus.Logger) *ModelTrainer {
	return &ModelTrainer{
		logger: logger,
	}
}

// Train entrena un modelo con los datos proporcionados
func (t *ModelTrainer) Train(
	ctx context.Context,
	model ModelPredictor,
	X [][]float64,
	y []float64,
	config *TrainingConfig,
) (*TrainingResult, error) {
	t.logger.Info("Starting model training...")
	start := time.Now()

	if config == nil {
		config = t.getDefaultConfig()
	}

	// Validar datos
	if len(X) == 0 || len(y) == 0 {
		return nil, fmt.Errorf("empty training data")
	}

	if len(X) != len(y) {
		return nil, fmt.Errorf("X and y length mismatch: %d vs %d", len(X), len(y))
	}

	// Dividir datos en train/validation
	trainX, trainY, valX, valY := t.splitData(X, y, config.ValidationSplit)

	t.logger.Infof("Training samples: %d, Validation samples: %d", len(trainX), len(valX))

	// Entrenar modelo
	err := model.Train(trainX, trainY)
	if err != nil {
		return nil, fmt.Errorf("training failed: %w", err)
	}

	// Evaluar en conjunto de validación
	valLoss, accuracy := t.evaluate(model, valX, valY)

	result := &TrainingResult{
		FinalLoss:      0.0, // TODO: Obtener del modelo
		ValidationLoss: valLoss,
		Accuracy:       accuracy,
		Epochs:         config.Epochs,
		TrainingTime:   time.Since(start),
		LossHistory:    []float64{},
		ValLossHistory: []float64{valLoss},
	}

	t.logger.Infof("Training completed in %v (accuracy: %.3f, val_loss: %.3f)",
		result.TrainingTime, result.Accuracy, result.ValidationLoss)

	return result, nil
}

// TrainWithEarlyStopping entrena con early stopping
func (t *ModelTrainer) TrainWithEarlyStopping(
	ctx context.Context,
	model ModelPredictor,
	X [][]float64,
	y []float64,
	config *TrainingConfig,
) (*TrainingResult, error) {
	if !config.EarlyStopping {
		return t.Train(ctx, model, X, y, config)
	}

	t.logger.Info("Starting training with early stopping...")
	start := time.Now()

	// Dividir datos
	trainX, trainY, valX, valY := t.splitData(X, y, config.ValidationSplit)

	bestValLoss := float64(1e10)
	patienceCounter := 0
	epochsRun := 0

	lossHistory := []float64{}
	valLossHistory := []float64{}

	for epoch := 0; epoch < config.Epochs; epoch++ {
		// Verificar contexto
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("training cancelled")
		default:
		}

		// Entrenar una época
		err := model.Train(trainX, trainY)
		if err != nil {
			return nil, fmt.Errorf("training failed at epoch %d: %w", epoch, err)
		}

		// Evaluar
		valLoss, accuracy := t.evaluate(model, valX, valY)
		lossHistory = append(lossHistory, 0.0) // TODO: Obtener loss de entrenamiento
		valLossHistory = append(valLossHistory, valLoss)

		t.logger.Debugf("Epoch %d/%d - val_loss: %.4f, accuracy: %.3f",
			epoch+1, config.Epochs, valLoss, accuracy)

		// Early stopping check
		if valLoss < bestValLoss {
			bestValLoss = valLoss
			patienceCounter = 0
		} else {
			patienceCounter++
			if patienceCounter >= config.Patience {
				t.logger.Infof("Early stopping at epoch %d", epoch+1)
				break
			}
		}

		epochsRun = epoch + 1
	}

	// Evaluación final
	_, finalAccuracy := t.evaluate(model, valX, valY)

	result := &TrainingResult{
		FinalLoss:      lossHistory[len(lossHistory)-1],
		ValidationLoss: bestValLoss,
		Accuracy:       finalAccuracy,
		Epochs:         epochsRun,
		TrainingTime:   time.Since(start),
		LossHistory:    lossHistory,
		ValLossHistory: valLossHistory,
	}

	t.logger.Infof("Training completed in %v (epochs: %d, accuracy: %.3f, best_val_loss: %.3f)",
		result.TrainingTime, result.Epochs, result.Accuracy, result.ValidationLoss)

	return result, nil
}

// evaluate evalúa el modelo en un conjunto de datos
func (t *ModelTrainer) evaluate(model ModelPredictor, X [][]float64, y []float64) (float64, float64) {
	if len(X) == 0 {
		return 0.0, 0.0
	}

	totalLoss := 0.0
	correct := 0

	for i, features := range X {
		prediction, err := model.Predict(features)
		if err != nil {
			continue
		}

		// Calcular loss (cross-entropy simplificado)
		predictedClass := t.getPredictedClass(prediction)
		actualClass := int(y[i])

		if predictedClass == actualClass {
			correct++
		}

		// Loss simplificado
		loss := 1.0 - prediction[actualClass]
		totalLoss += loss
	}

	avgLoss := totalLoss / float64(len(X))
	accuracy := float64(correct) / float64(len(X))

	return avgLoss, accuracy
}

// splitData divide los datos en train y validation
func (t *ModelTrainer) splitData(X [][]float64, y []float64, validationSplit float64) ([][]float64, []float64, [][]float64, []float64) {
	if validationSplit <= 0 || validationSplit >= 1 {
		// Sin validación, retornar todo como training
		return X, y, [][]float64{}, []float64{}
	}

	// Calcular índice de split
	splitIdx := int(float64(len(X)) * (1.0 - validationSplit))

	// Shuffle data antes de split (opcional)
	// Por ahora, split simple sin shuffle

	trainX := X[:splitIdx]
	trainY := y[:splitIdx]
	valX := X[splitIdx:]
	valY := y[splitIdx:]

	return trainX, trainY, valX, valY
}

// getPredictedClass obtiene la clase predicha del output
func (t *ModelTrainer) getPredictedClass(output []float64) int {
	if len(output) == 0 {
		return 0
	}

	maxIdx := 0
	maxVal := output[0]

	for i, val := range output {
		if val > maxVal {
			maxVal = val
			maxIdx = i
		}
	}

	return maxIdx
}

// getDefaultConfig retorna configuración por defecto
func (t *ModelTrainer) getDefaultConfig() *TrainingConfig {
	return &TrainingConfig{
		Epochs:          100,
		BatchSize:       32,
		LearningRate:    0.001,
		ValidationSplit: 0.2,
		EarlyStopping:   true,
		Patience:        10,
	}
}

// ShuffleData mezcla los datos de entrenamiento
func (t *ModelTrainer) ShuffleData(X [][]float64, y []float64) ([][]float64, []float64) {
	n := len(X)
	indices := rand.Perm(n)

	shuffledX := make([][]float64, n)
	shuffledY := make([]float64, n)

	for i, idx := range indices {
		shuffledX[i] = X[idx]
		shuffledY[i] = y[idx]
	}

	return shuffledX, shuffledY
}

// CrossValidate realiza validación cruzada
func (t *ModelTrainer) CrossValidate(
	ctx context.Context,
	model ModelPredictor,
	X [][]float64,
	y []float64,
	folds int,
) ([]float64, error) {
	if folds < 2 {
		return nil, fmt.Errorf("folds must be >= 2")
	}

	t.logger.Infof("Starting %d-fold cross-validation...", folds)

	foldSize := len(X) / folds
	accuracies := make([]float64, folds)

	for fold := 0; fold < folds; fold++ {
		// Crear train/test split para este fold
		testStart := fold * foldSize
		testEnd := testStart + foldSize

		trainX := append([][]float64{}, X[:testStart]...)
		trainX = append(trainX, X[testEnd:]...)

		trainY := append([]float64{}, y[:testStart]...)
		trainY = append(trainY, y[testEnd:]...)

		testX := X[testStart:testEnd]
		testY := y[testStart:testEnd]

		// Entrenar
		err := model.Train(trainX, trainY)
		if err != nil {
			return nil, fmt.Errorf("training failed in fold %d: %w", fold, err)
		}

		// Evaluar
		_, accuracy := t.evaluate(model, testX, testY)
		accuracies[fold] = accuracy

		t.logger.Infof("Fold %d/%d - accuracy: %.3f", fold+1, folds, accuracy)
	}

	return accuracies, nil
}
