package monitoring

import (
	"time"
)

// DecisionRecord represents a recorded trading decision
type DecisionRecord struct {
	ID        string
	Timestamp time.Time
	Pair      string
	Direction string

	// AI Analysis
	AIConfidence      string
	AIReasoning       string
	ConfluenceFactors []string

	// Trade Details
	Entry        float64
	StopLoss     float64
	TakeProfit   float64
	PositionSize float64

	// Validation
	ValidationPassed bool
	ValidationErrors []string

	// Outcome (filled after trade closes)
	Executed     bool
	Outcome      string // "win", "loss", "scratch"
	ActualPnL    float64
	ActualPnLPct float64
	Duration     time.Duration
}

// PerformanceMetrics contains aggregated performance statistics
type PerformanceMetrics struct {
	Period    string
	StartTime time.Time
	EndTime   time.Time

	// Decision Quality
	TotalDecisions int
	ExecutedTrades int
	RejectedTrades int
	RejectionRate  float64

	// Trade Outcomes
	WinningTrades int
	LosingTrades  int
	ScratchTrades int
	WinRate       float64

	// Financial
	TotalPnL     float64
	TotalPnLPct  float64
	AverageWin   float64
	AverageLoss  float64
	ProfitFactor float64

	// AI Accuracy
	SetupAccuracy float64
	EntryAccuracy float64
	ExitAccuracy  float64

	// Validation
	TopRejectionReasons       map[string]int
	CircuitBreakerActivations int
}

// Report represents a performance report
type Report struct {
	GeneratedAt     time.Time
	Period          string
	Metrics         *PerformanceMetrics
	Recommendations []string
	Warnings        []string
}
