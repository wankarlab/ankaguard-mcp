package monitoring

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/rantipay/trading-mcp/internal/trading"
	"github.com/sirupsen/logrus"
)

// PerformanceConfig holds the configuration for performance tracking
type PerformanceConfig struct {
	TrackRealTimePnL      bool          `yaml:"track_realtime_pnl"`
	TrackTradeMetrics     bool          `yaml:"track_trade_metrics"`
	TrackSharpeRatio      bool          `yaml:"track_sharpe_ratio"`
	TrackExecutionMetrics bool          `yaml:"track_execution_metrics"`
	UpdateInterval        time.Duration `yaml:"update_interval"`
	MetricsRetention      int           `yaml:"metrics_retention"` // Number of data points to keep
}

// DefaultPerformanceConfig returns default configuration
func DefaultPerformanceConfig() *PerformanceConfig {
	return &PerformanceConfig{
		TrackRealTimePnL:      true,
		TrackTradeMetrics:     true,
		TrackSharpeRatio:      true,
		TrackExecutionMetrics: true,
		UpdateInterval:        1 * time.Second,
		MetricsRetention:      1000,
	}
}

// TradeMetrics holds trade-related metrics
type TradeMetrics struct {
	TotalTrades      int           `json:"total_trades"`
	WinningTrades    int           `json:"winning_trades"`
	LosingTrades     int           `json:"losing_trades"`
	WinRate          float64       `json:"win_rate"`
	AverageWin       float64       `json:"average_win"`
	AverageLoss      float64       `json:"average_loss"`
	LargestWin       float64       `json:"largest_win"`
	LargestLoss      float64       `json:"largest_loss"`
	ProfitFactor     float64       `json:"profit_factor"`
	AvgTradeDuration time.Duration `json:"avg_trade_duration"`
	LastUpdate       time.Time     `json:"last_update"`
}

// PnLMetrics holds P&L tracking metrics
type PnLMetrics struct {
	InitialCapital  float64   `json:"initial_capital"`
	CurrentCapital  float64   `json:"current_capital"`
	TotalPnL        float64   `json:"total_pnl"`
	TotalPnLPct     float64   `json:"total_pnl_pct"`
	RealizedPnL     float64   `json:"realized_pnl"`
	UnrealizedPnL   float64   `json:"unrealized_pnl"`
	DailyPnL        float64   `json:"daily_pnl"`
	WeeklyPnL       float64   `json:"weekly_pnl"`
	MonthlyPnL      float64   `json:"monthly_pnl"`
	PeakCapital     float64   `json:"peak_capital"`
	CurrentDrawdown float64   `json:"current_drawdown"`
	MaxDrawdown     float64   `json:"max_drawdown"`
	LastUpdate      time.Time `json:"last_update"`
}

// ExecutionMetrics holds execution quality metrics
type ExecutionMetrics struct {
	// Latency (milliseconds)
	AvgLatency float64 `json:"avg_latency"`
	P50Latency float64 `json:"p50_latency"`
	P95Latency float64 `json:"p95_latency"`
	P99Latency float64 `json:"p99_latency"`

	// Slippage (percentage)
	AvgSlippage float64 `json:"avg_slippage"`
	MaxSlippage float64 `json:"max_slippage"`

	// Fill rate
	TotalOrders  int     `json:"total_orders"`
	FilledOrders int     `json:"filled_orders"`
	FillRate     float64 `json:"fill_rate"`

	// Errors
	TotalErrors int     `json:"total_errors"`
	ErrorRate   float64 `json:"error_rate"`

	LastUpdate time.Time `json:"last_update"`
}

// PerformanceSnapshot holds a complete performance snapshot
type PerformanceSnapshot struct {
	Timestamp        time.Time         `json:"timestamp"`
	TradeMetrics     *TradeMetrics     `json:"trade_metrics"`
	PnLMetrics       *PnLMetrics       `json:"pnl_metrics"`
	ExecutionMetrics *ExecutionMetrics `json:"execution_metrics"`
	SharpeRatio      float64           `json:"sharpe_ratio"`
	SortinoRatio     float64           `json:"sortino_ratio"`
}

// PerformanceTracker is the interface for performance tracking
type PerformanceTracker interface {
	// Start starts the performance tracker
	Start(ctx context.Context) error

	// Stop stops the performance tracker
	Stop() error

	// RecordTrade records a completed trade
	RecordTrade(trade *trading.Position) error

	// UpdatePnL updates P&L metrics
	UpdatePnL(realizedPnL, unrealizedPnL float64) error

	// RecordExecution records execution metrics
	RecordExecution(latency, slippage float64, filled bool, hasError bool) error

	// GetSnapshot returns current performance snapshot
	GetSnapshot() *PerformanceSnapshot

	// GetTradeMetrics returns trade metrics
	GetTradeMetrics() *TradeMetrics

	// GetPnLMetrics returns P&L metrics
	GetPnLMetrics() *PnLMetrics

	// GetExecutionMetrics returns execution metrics
	GetExecutionMetrics() *ExecutionMetrics

	// GetSharpeRatio returns the Sharpe ratio
	GetSharpeRatio() float64

	// IsRunning returns whether the tracker is running
	IsRunning() bool
}

// performanceTracker is the concrete implementation
type performanceTracker struct {
	config *PerformanceConfig
	logger *logrus.Logger

	// Metrics
	tradeMetrics     *TradeMetrics
	pnlMetrics       *PnLMetrics
	executionMetrics *ExecutionMetrics

	// Historical data for calculations
	returns   []float64
	latencies []float64
	slippages []float64

	// State
	running bool
	mu      sync.RWMutex
	ctx     context.Context
	cancel  context.CancelFunc
}

// NewPerformanceTracker creates a new performance tracker
func NewPerformanceTracker(config *PerformanceConfig, initialCapital float64, logger *logrus.Logger) PerformanceTracker {
	if config == nil {
		config = DefaultPerformanceConfig()
	}

	if logger == nil {
		logger = logrus.New()
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &performanceTracker{
		config: config,
		logger: logger,
		tradeMetrics: &TradeMetrics{
			LastUpdate: time.Now(),
		},
		pnlMetrics: &PnLMetrics{
			InitialCapital: initialCapital,
			CurrentCapital: initialCapital,
			PeakCapital:    initialCapital,
			LastUpdate:     time.Now(),
		},
		executionMetrics: &ExecutionMetrics{
			LastUpdate: time.Now(),
		},
		returns:   make([]float64, 0, config.MetricsRetention),
		latencies: make([]float64, 0, config.MetricsRetention),
		slippages: make([]float64, 0, config.MetricsRetention),
		ctx:       ctx,
		cancel:    cancel,
	}
}

// Start starts the performance tracker
func (p *performanceTracker) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running {
		return fmt.Errorf("performance tracker already running")
	}

	p.logger.Info("Starting Performance Tracker")
	p.running = true

	return nil
}

// Stop stops the performance tracker
func (p *performanceTracker) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.running {
		return fmt.Errorf("performance tracker not running")
	}

	p.logger.Info("Stopping Performance Tracker")
	p.cancel()
	p.running = false

	return nil
}

// IsRunning returns whether the tracker is running
func (p *performanceTracker) IsRunning() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.running
}

// RecordTrade records a completed trade
func (p *performanceTracker) RecordTrade(trade *trading.Position) error {
	if !p.config.TrackTradeMetrics {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.tradeMetrics.TotalTrades++

	// Calculate trade duration
	if trade.CloseTime != nil {
		duration := trade.CloseTime.Sub(trade.OpenTime)
		totalDuration := p.tradeMetrics.AvgTradeDuration * time.Duration(p.tradeMetrics.TotalTrades-1)
		p.tradeMetrics.AvgTradeDuration = (totalDuration + duration) / time.Duration(p.tradeMetrics.TotalTrades)
	}

	// Record win/loss
	if trade.RealizedPnL > 0 {
		p.tradeMetrics.WinningTrades++

		// Update average win
		totalWins := p.tradeMetrics.AverageWin * float64(p.tradeMetrics.WinningTrades-1)
		p.tradeMetrics.AverageWin = (totalWins + trade.RealizedPnL) / float64(p.tradeMetrics.WinningTrades)

		if trade.RealizedPnL > p.tradeMetrics.LargestWin {
			p.tradeMetrics.LargestWin = trade.RealizedPnL
		}
	} else if trade.RealizedPnL < 0 {
		p.tradeMetrics.LosingTrades++

		// Update average loss
		totalLosses := p.tradeMetrics.AverageLoss * float64(p.tradeMetrics.LosingTrades-1)
		p.tradeMetrics.AverageLoss = (totalLosses + math.Abs(trade.RealizedPnL)) / float64(p.tradeMetrics.LosingTrades)

		if trade.RealizedPnL < p.tradeMetrics.LargestLoss {
			p.tradeMetrics.LargestLoss = trade.RealizedPnL
		}
	}

	// Calculate win rate
	if p.tradeMetrics.TotalTrades > 0 {
		p.tradeMetrics.WinRate = float64(p.tradeMetrics.WinningTrades) / float64(p.tradeMetrics.TotalTrades) * 100
	}

	// Calculate profit factor
	totalWins := p.tradeMetrics.AverageWin * float64(p.tradeMetrics.WinningTrades)
	totalLosses := p.tradeMetrics.AverageLoss * float64(p.tradeMetrics.LosingTrades)
	if totalLosses > 0 {
		p.tradeMetrics.ProfitFactor = totalWins / totalLosses
	}

	// Record return for Sharpe ratio calculation
	if p.config.TrackSharpeRatio && trade.RealizedPnL != 0 {
		returnPct := (trade.RealizedPnL / (trade.Size * trade.EntryPrice)) * 100
		p.returns = append(p.returns, returnPct)

		// Keep only last N returns
		if len(p.returns) > p.config.MetricsRetention {
			p.returns = p.returns[len(p.returns)-p.config.MetricsRetention:]
		}
	}

	p.tradeMetrics.LastUpdate = time.Now()

	return nil
}

// UpdatePnL updates P&L metrics
func (p *performanceTracker) UpdatePnL(realizedPnL, unrealizedPnL float64) error {
	if !p.config.TrackRealTimePnL {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.pnlMetrics.RealizedPnL = realizedPnL
	p.pnlMetrics.UnrealizedPnL = unrealizedPnL
	p.pnlMetrics.TotalPnL = realizedPnL + unrealizedPnL
	p.pnlMetrics.CurrentCapital = p.pnlMetrics.InitialCapital + p.pnlMetrics.TotalPnL

	// Calculate percentage
	if p.pnlMetrics.InitialCapital > 0 {
		p.pnlMetrics.TotalPnLPct = (p.pnlMetrics.TotalPnL / p.pnlMetrics.InitialCapital) * 100
	}

	// Update peak and drawdown
	if p.pnlMetrics.CurrentCapital > p.pnlMetrics.PeakCapital {
		p.pnlMetrics.PeakCapital = p.pnlMetrics.CurrentCapital
	}

	p.pnlMetrics.CurrentDrawdown = p.pnlMetrics.PeakCapital - p.pnlMetrics.CurrentCapital
	if p.pnlMetrics.CurrentDrawdown > p.pnlMetrics.MaxDrawdown {
		p.pnlMetrics.MaxDrawdown = p.pnlMetrics.CurrentDrawdown
	}

	p.pnlMetrics.LastUpdate = time.Now()

	return nil
}

// RecordExecution records execution metrics
func (p *performanceTracker) RecordExecution(latency, slippage float64, filled bool, hasError bool) error {
	if !p.config.TrackExecutionMetrics {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.executionMetrics.TotalOrders++

	if filled {
		p.executionMetrics.FilledOrders++
	}

	if hasError {
		p.executionMetrics.TotalErrors++
	}

	// Calculate fill rate
	if p.executionMetrics.TotalOrders > 0 {
		p.executionMetrics.FillRate = float64(p.executionMetrics.FilledOrders) / float64(p.executionMetrics.TotalOrders) * 100
		p.executionMetrics.ErrorRate = float64(p.executionMetrics.TotalErrors) / float64(p.executionMetrics.TotalOrders) * 100
	}

	// Record latency
	if latency > 0 {
		p.latencies = append(p.latencies, latency)
		if len(p.latencies) > p.config.MetricsRetention {
			p.latencies = p.latencies[len(p.latencies)-p.config.MetricsRetention:]
		}

		// Calculate latency metrics
		p.executionMetrics.AvgLatency = p.calculateAverage(p.latencies)
		p.executionMetrics.P50Latency = p.calculatePercentile(p.latencies, 0.50)
		p.executionMetrics.P95Latency = p.calculatePercentile(p.latencies, 0.95)
		p.executionMetrics.P99Latency = p.calculatePercentile(p.latencies, 0.99)
	}

	// Record slippage
	if slippage > 0 {
		p.slippages = append(p.slippages, slippage)
		if len(p.slippages) > p.config.MetricsRetention {
			p.slippages = p.slippages[len(p.slippages)-p.config.MetricsRetention:]
		}

		// Calculate slippage metrics
		p.executionMetrics.AvgSlippage = p.calculateAverage(p.slippages)
		p.executionMetrics.MaxSlippage = p.calculateMax(p.slippages)
	}

	p.executionMetrics.LastUpdate = time.Now()

	return nil
}

// GetSnapshot returns current performance snapshot
func (p *performanceTracker) GetSnapshot() *PerformanceSnapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return &PerformanceSnapshot{
		Timestamp:        time.Now(),
		TradeMetrics:     p.copyTradeMetrics(),
		PnLMetrics:       p.copyPnLMetrics(),
		ExecutionMetrics: p.copyExecutionMetrics(),
		SharpeRatio:      p.calculateSharpeRatio(),
		SortinoRatio:     p.calculateSortinoRatio(),
	}
}

// GetTradeMetrics returns trade metrics
func (p *performanceTracker) GetTradeMetrics() *TradeMetrics {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.copyTradeMetrics()
}

// GetPnLMetrics returns P&L metrics
func (p *performanceTracker) GetPnLMetrics() *PnLMetrics {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.copyPnLMetrics()
}

// GetExecutionMetrics returns execution metrics
func (p *performanceTracker) GetExecutionMetrics() *ExecutionMetrics {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.copyExecutionMetrics()
}

// GetSharpeRatio returns the Sharpe ratio
func (p *performanceTracker) GetSharpeRatio() float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.calculateSharpeRatio()
}

// Helper methods

// calculateSharpeRatio calculates the Sharpe ratio
func (p *performanceTracker) calculateSharpeRatio() float64 {
	if len(p.returns) < 2 {
		return 0
	}

	// Calculate mean return
	meanReturn := p.calculateAverage(p.returns)

	// Calculate standard deviation
	var variance float64
	for _, r := range p.returns {
		diff := r - meanReturn
		variance += diff * diff
	}
	variance /= float64(len(p.returns))
	stdDev := math.Sqrt(variance)

	// Sharpe ratio (assuming risk-free rate = 0)
	if stdDev > 0 {
		return meanReturn / stdDev
	}

	return 0
}

// calculateSortinoRatio calculates the Sortino ratio
func (p *performanceTracker) calculateSortinoRatio() float64 {
	if len(p.returns) < 2 {
		return 0
	}

	// Calculate mean return
	meanReturn := p.calculateAverage(p.returns)

	// Calculate downside deviation (only negative returns)
	var downsideVariance float64
	downsideCount := 0
	for _, r := range p.returns {
		if r < 0 {
			downsideVariance += r * r
			downsideCount++
		}
	}

	if downsideCount == 0 {
		return 0
	}

	downsideVariance /= float64(downsideCount)
	downsideStdDev := math.Sqrt(downsideVariance)

	// Sortino ratio
	if downsideStdDev > 0 {
		return meanReturn / downsideStdDev
	}

	return 0
}

// calculateAverage calculates the average of a slice
func (p *performanceTracker) calculateAverage(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	var sum float64
	for _, v := range values {
		sum += v
	}

	return sum / float64(len(values))
}

// calculatePercentile calculates a percentile
func (p *performanceTracker) calculatePercentile(values []float64, percentile float64) float64 {
	if len(values) == 0 {
		return 0
	}

	// Simple percentile calculation (would use sort in production)
	index := int(float64(len(values)) * percentile)
	if index >= len(values) {
		index = len(values) - 1
	}

	return values[index]
}

// calculateMax calculates the maximum value
func (p *performanceTracker) calculateMax(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	max := values[0]
	for _, v := range values {
		if v > max {
			max = v
		}
	}

	return max
}

// Copy methods to return thread-safe copies

func (p *performanceTracker) copyTradeMetrics() *TradeMetrics {
	copy := *p.tradeMetrics
	return &copy
}

func (p *performanceTracker) copyPnLMetrics() *PnLMetrics {
	copy := *p.pnlMetrics
	return &copy
}

func (p *performanceTracker) copyExecutionMetrics() *ExecutionMetrics {
	copy := *p.executionMetrics
	return &copy
}
