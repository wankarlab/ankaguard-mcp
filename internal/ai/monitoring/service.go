package monitoring

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Service provides performance monitoring capabilities
type Service interface {
	// RecordDecision records a trading decision
	RecordDecision(ctx context.Context, record *DecisionRecord) error

	// UpdateOutcome updates the outcome of a trade
	UpdateOutcome(ctx context.Context, decisionID string, outcome string, pnl float64) error

	// GetMetrics returns performance metrics for a period
	GetMetrics(ctx context.Context, period string) (*PerformanceMetrics, error)

	// GenerateReport generates a performance report
	GenerateReport(ctx context.Context, period string) (*Report, error)

	// GetRecentDecisions returns recent trading decisions
	GetRecentDecisions(ctx context.Context, limit int) ([]*DecisionRecord, error)
}

// PerformanceMonitor implements the Service interface
type PerformanceMonitor struct {
	storage    *DecisionStorage
	calculator *MetricsCalculator
	reporter   *ReportGenerator

	mu          sync.RWMutex
	initialized bool
}

// NewPerformanceMonitor creates a new performance monitor
func NewPerformanceMonitor(config *Config) *PerformanceMonitor {
	return &PerformanceMonitor{
		storage:    NewDecisionStorage(config.StoragePath),
		calculator: NewMetricsCalculator(),
		reporter:   NewReportGenerator(),
	}
}

// Initialize sets up the performance monitor
func (p *PerformanceMonitor) Initialize(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.storage.Initialize(ctx); err != nil {
		return fmt.Errorf("failed to initialize storage: %w", err)
	}

	p.initialized = true
	return nil
}

// RecordDecision records a trading decision
func (p *PerformanceMonitor) RecordDecision(ctx context.Context, record *DecisionRecord) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.initialized {
		return fmt.Errorf("performance monitor not initialized")
	}

	return p.storage.Save(ctx, record)
}

// UpdateOutcome updates the outcome of a trade
func (p *PerformanceMonitor) UpdateOutcome(
	ctx context.Context,
	decisionID string,
	outcome string,
	pnl float64,
) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.storage.UpdateOutcome(ctx, decisionID, outcome, pnl)
}

// GetMetrics returns performance metrics for a period
func (p *PerformanceMonitor) GetMetrics(ctx context.Context, period string) (*PerformanceMetrics, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Get decisions for period
	decisions, err := p.storage.GetByPeriod(ctx, period)
	if err != nil {
		return nil, fmt.Errorf("failed to get decisions: %w", err)
	}

	// Calculate metrics
	metrics := p.calculator.Calculate(decisions, period)

	return metrics, nil
}

// GenerateReport generates a performance report
func (p *PerformanceMonitor) GenerateReport(ctx context.Context, period string) (*Report, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Get metrics
	metrics, err := p.GetMetrics(ctx, period)
	if err != nil {
		return nil, fmt.Errorf("failed to get metrics: %w", err)
	}

	// Generate report
	report := p.reporter.Generate(metrics)
	report.GeneratedAt = time.Now()
	report.Period = period

	return report, nil
}

// GetRecentDecisions returns recent trading decisions
func (p *PerformanceMonitor) GetRecentDecisions(ctx context.Context, limit int) ([]*DecisionRecord, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.storage.GetRecent(ctx, limit)
}
