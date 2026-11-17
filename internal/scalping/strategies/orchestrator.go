package strategies

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/scalping/models"
)

// Strategy define la interfaz para todas las estrategias de scalping
type Strategy interface {
	Name() string
	Analyze(ctx context.Context, pair string, data *MarketData) (*models.ScalpingSignal, error)
	IsEnabled() bool
	SetEnabled(enabled bool)
	GetConfig() *models.StrategyConfig
	UpdateConfig(config *models.StrategyConfig) error
}

// MarketData contiene datos de mercado para análisis
type MarketData struct {
	Pair      string
	Exchange  string
	Price     float64
	Volume    float64
	Timestamp time.Time
	Features  *models.Features
	OHLCV     []OHLCV
}

// OHLCV representa datos de velas
type OHLCV struct {
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64
	Timestamp time.Time
}

// StrategyOrchestrator coordina múltiples estrategias de scalping
type StrategyOrchestrator struct {
	mu          sync.RWMutex
	logger      *logrus.Logger
	strategies  map[string]Strategy
	weights     map[string]float64
	performance map[string]*StrategyPerformance
	signalChan  chan *models.ScalpingSignal
	ctx         context.Context
	cancel      context.CancelFunc
}

// StrategyPerformance rastrea el rendimiento de una estrategia
type StrategyPerformance struct {
	TotalSignals     int
	CorrectSignals   int
	IncorrectSignals int
	Accuracy         float64
	AvgConfidence    float64
	LastUpdated      time.Time
}

// NewStrategyOrchestrator crea un nuevo orquestador de estrategias
func NewStrategyOrchestrator(logger *logrus.Logger) *StrategyOrchestrator {
	ctx, cancel := context.WithCancel(context.Background())

	return &StrategyOrchestrator{
		logger:      logger,
		strategies:  make(map[string]Strategy),
		weights:     make(map[string]float64),
		performance: make(map[string]*StrategyPerformance),
		signalChan:  make(chan *models.ScalpingSignal, 1000),
		ctx:         ctx,
		cancel:      cancel,
	}
}

// RegisterStrategy registra una nueva estrategia
func (o *StrategyOrchestrator) RegisterStrategy(strategy Strategy) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	name := strategy.Name()
	if _, exists := o.strategies[name]; exists {
		return fmt.Errorf("strategy %s already registered", name)
	}

	o.strategies[name] = strategy
	o.weights[name] = 1.0 // Peso por defecto
	o.performance[name] = &StrategyPerformance{
		LastUpdated: time.Now(),
	}

	o.logger.Infof("Registered strategy: %s", name)
	return nil
}

// UnregisterStrategy elimina una estrategia
func (o *StrategyOrchestrator) UnregisterStrategy(name string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if _, exists := o.strategies[name]; !exists {
		return fmt.Errorf("strategy %s not found", name)
	}

	delete(o.strategies, name)
	delete(o.weights, name)
	delete(o.performance, name)

	o.logger.Infof("Unregistered strategy: %s", name)
	return nil
}

// AnalyzeMarket analiza el mercado con todas las estrategias activas
func (o *StrategyOrchestrator) AnalyzeMarket(ctx context.Context, pair string, data *MarketData) (*models.ScalpingSignal, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	if len(o.strategies) == 0 {
		return nil, fmt.Errorf("no strategies registered")
	}

	// Ejecutar todas las estrategias en paralelo
	signals := make(chan *models.ScalpingSignal, len(o.strategies))
	errors := make(chan error, len(o.strategies))
	var wg sync.WaitGroup

	for name, strategy := range o.strategies {
		if !strategy.IsEnabled() {
			continue
		}

		wg.Add(1)
		go func(name string, strat Strategy) {
			defer wg.Done()

			signal, err := strat.Analyze(ctx, pair, data)
			if err != nil {
				o.logger.Warnf("Strategy %s failed: %v", name, err)
				errors <- err
				return
			}

			if signal != nil {
				signals <- signal
			}
		}(name, strategy)
	}

	// Esperar a que todas las estrategias terminen
	wg.Wait()
	close(signals)
	close(errors)

	// Recolectar señales
	var collectedSignals []*models.ScalpingSignal
	for signal := range signals {
		collectedSignals = append(collectedSignals, signal)
	}

	if len(collectedSignals) == 0 {
		return nil, nil // No hay señales
	}

	// Combinar señales
	combinedSignal := o.combineSignals(collectedSignals, pair, data)

	return combinedSignal, nil
}

// combineSignals combina múltiples señales en una sola
func (o *StrategyOrchestrator) combineSignals(signals []*models.ScalpingSignal, pair string, data *MarketData) *models.ScalpingSignal {
	if len(signals) == 0 {
		return nil
	}

	if len(signals) == 1 {
		return signals[0]
	}

	// Contar votos por dirección
	votes := make(map[string]float64)
	totalWeight := 0.0
	strategies := []string{}
	totalConfidence := 0.0

	for _, signal := range signals {
		for _, stratName := range signal.Strategies {
			weight := o.weights[stratName]
			votes[signal.Direction] += weight * signal.Confidence
			totalWeight += weight
			strategies = append(strategies, stratName)
			totalConfidence += signal.Confidence
		}
	}

	// Encontrar dirección con más votos
	var bestDirection string
	var bestScore float64

	for direction, score := range votes {
		if score > bestScore {
			bestScore = score
			bestDirection = direction
		}
	}

	// Normalizar score
	if totalWeight > 0 {
		bestScore /= totalWeight
	}

	// Calcular confianza promedio
	avgConfidence := totalConfidence / float64(len(signals))

	// Crear señal combinada
	combinedSignal := &models.ScalpingSignal{
		ID:            fmt.Sprintf("combined_%d", time.Now().UnixNano()),
		Pair:          pair,
		Exchange:      data.Exchange,
		Direction:     bestDirection,
		Confidence:    avgConfidence,
		Strategies:    strategies,
		CombinedScore: bestScore,
		Price:         data.Price,
		Volume:        data.Volume,
		Timestamp:     time.Now(),
		Metadata:      make(map[string]interface{}),
	}

	// Agregar metadata
	combinedSignal.Metadata["num_strategies"] = len(signals)
	combinedSignal.Metadata["votes"] = votes
	combinedSignal.Metadata["total_weight"] = totalWeight

	o.logger.Debugf("Combined signal for %s: %s (score: %.3f, confidence: %.3f, strategies: %d)",
		pair, bestDirection, bestScore, avgConfidence, len(signals))

	return combinedSignal
}

// GetActiveStrategies retorna las estrategias activas
func (o *StrategyOrchestrator) GetActiveStrategies() []string {
	o.mu.RLock()
	defer o.mu.RUnlock()

	var active []string
	for name, strategy := range o.strategies {
		if strategy.IsEnabled() {
			active = append(active, name)
		}
	}

	return active
}

// GetAllStrategies retorna todas las estrategias registradas
func (o *StrategyOrchestrator) GetAllStrategies() []string {
	o.mu.RLock()
	defer o.mu.RUnlock()

	var all []string
	for name := range o.strategies {
		all = append(all, name)
	}

	return all
}

// EnableStrategy activa una estrategia
func (o *StrategyOrchestrator) EnableStrategy(name string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	strategy, exists := o.strategies[name]
	if !exists {
		return fmt.Errorf("strategy %s not found", name)
	}

	strategy.SetEnabled(true)
	o.logger.Infof("Enabled strategy: %s", name)

	return nil
}

// DisableStrategy desactiva una estrategia
func (o *StrategyOrchestrator) DisableStrategy(name string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	strategy, exists := o.strategies[name]
	if !exists {
		return fmt.Errorf("strategy %s not found", name)
	}

	strategy.SetEnabled(false)
	o.logger.Infof("Disabled strategy: %s", name)

	return nil
}

// SetStrategyWeight establece el peso de una estrategia
func (o *StrategyOrchestrator) SetStrategyWeight(name string, weight float64) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if _, exists := o.strategies[name]; !exists {
		return fmt.Errorf("strategy %s not found", name)
	}

	if weight < 0 || weight > 10 {
		return fmt.Errorf("weight must be between 0 and 10")
	}

	o.weights[name] = weight
	o.logger.Infof("Set weight for strategy %s: %.2f", name, weight)

	return nil
}

// GetStrategyWeight obtiene el peso de una estrategia
func (o *StrategyOrchestrator) GetStrategyWeight(name string) (float64, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	weight, exists := o.weights[name]
	if !exists {
		return 0, fmt.Errorf("strategy %s not found", name)
	}

	return weight, nil
}

// UpdateStrategyPerformance actualiza el rendimiento de una estrategia
func (o *StrategyOrchestrator) UpdateStrategyPerformance(name string, correct bool, confidence float64) {
	o.mu.Lock()
	defer o.mu.Unlock()

	perf, exists := o.performance[name]
	if !exists {
		return
	}

	perf.TotalSignals++
	if correct {
		perf.CorrectSignals++
	} else {
		perf.IncorrectSignals++
	}

	// Actualizar accuracy
	if perf.TotalSignals > 0 {
		perf.Accuracy = float64(perf.CorrectSignals) / float64(perf.TotalSignals)
	}

	// Actualizar confianza promedio (moving average)
	alpha := 0.1 // Factor de suavizado
	perf.AvgConfidence = alpha*confidence + (1-alpha)*perf.AvgConfidence

	perf.LastUpdated = time.Now()

	// Ajustar peso automáticamente basado en rendimiento
	o.adjustWeightBasedOnPerformance(name, perf)
}

// adjustWeightBasedOnPerformance ajusta el peso de una estrategia basado en su rendimiento
func (o *StrategyOrchestrator) adjustWeightBasedOnPerformance(name string, perf *StrategyPerformance) {
	// Solo ajustar después de tener suficientes señales
	if perf.TotalSignals < 20 {
		return
	}

	// Calcular nuevo peso basado en accuracy
	// Accuracy > 60%: aumentar peso
	// Accuracy < 50%: disminuir peso
	currentWeight := o.weights[name]
	newWeight := currentWeight

	if perf.Accuracy > 0.60 {
		newWeight = currentWeight * 1.1 // Aumentar 10%
	} else if perf.Accuracy < 0.50 {
		newWeight = currentWeight * 0.9 // Disminuir 10%
	}

	// Limitar peso entre 0.1 y 5.0
	if newWeight < 0.1 {
		newWeight = 0.1
	}
	if newWeight > 5.0 {
		newWeight = 5.0
	}

	if newWeight != currentWeight {
		o.weights[name] = newWeight
		o.logger.Infof("Adjusted weight for strategy %s: %.2f -> %.2f (accuracy: %.2f%%)",
			name, currentWeight, newWeight, perf.Accuracy*100)
	}
}

// GetStrategyPerformance obtiene el rendimiento de una estrategia
func (o *StrategyOrchestrator) GetStrategyPerformance(name string) (*StrategyPerformance, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	perf, exists := o.performance[name]
	if !exists {
		return nil, fmt.Errorf("strategy %s not found", name)
	}

	// Retornar copia para evitar modificaciones
	return &StrategyPerformance{
		TotalSignals:     perf.TotalSignals,
		CorrectSignals:   perf.CorrectSignals,
		IncorrectSignals: perf.IncorrectSignals,
		Accuracy:         perf.Accuracy,
		AvgConfidence:    perf.AvgConfidence,
		LastUpdated:      perf.LastUpdated,
	}, nil
}

// GetAllPerformance obtiene el rendimiento de todas las estrategias
func (o *StrategyOrchestrator) GetAllPerformance() map[string]*StrategyPerformance {
	o.mu.RLock()
	defer o.mu.RUnlock()

	result := make(map[string]*StrategyPerformance)
	for name, perf := range o.performance {
		result[name] = &StrategyPerformance{
			TotalSignals:     perf.TotalSignals,
			CorrectSignals:   perf.CorrectSignals,
			IncorrectSignals: perf.IncorrectSignals,
			Accuracy:         perf.Accuracy,
			AvgConfidence:    perf.AvgConfidence,
			LastUpdated:      perf.LastUpdated,
		}
	}

	return result
}

// Stop detiene el orquestador
func (o *StrategyOrchestrator) Stop() {
	o.cancel()
	close(o.signalChan)
	o.logger.Info("Strategy orchestrator stopped")
}
