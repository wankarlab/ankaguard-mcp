package validation

import (
	"time"
)

// TradingDecision represents an AI-generated trade proposal
type TradingDecision struct {
	ID        string
	Timestamp time.Time
	Pair      string
	Direction string // "LONG" or "SHORT"

	// Levels
	EntryPrice  float64
	StopLoss    float64
	TakeProfit  float64
	TakeProfit1 float64
	TakeProfit2 float64

	// Risk Management
	PositionSize    float64
	RiskAmount      float64
	RiskPercent     float64
	RewardAmount    float64
	RiskRewardRatio float64
	Leverage        float64
	StopLossPercent float64

	// Analysis
	ConfluenceCount   int
	ConfluenceFactors []string
	Confidence        string // "HIGH", "MEDIUM", "LOW"
	Reasoning         string
	Timeframe         string

	// Market Context
	Volatility string
	Spread     float64

	// Validation
	ValidationStatus string
	ValidationErrors []string
}

// ValidationResult contains the outcome of validation
type ValidationResult struct {
	Passed           bool
	CriticalFailures []RuleViolation
	Warnings         []RuleViolation
	RulesEvaluated   int
	Timestamp        time.Time
	ExecutionTime    time.Duration
}

// RuleViolation describes a failed validation rule
type RuleViolation struct {
	RuleID   string
	RuleName string
	Severity string
	Message  string
	Metadata map[string]interface{}
}

// Rule represents a validation rule
type Rule struct {
	ID        string
	Name      string
	Category  string
	Severity  string
	Priority  int
	Enabled   bool
	Condition string
	Message   string
	Metadata  map[string]interface{}
}

// CircuitBreakerStatus represents the state of circuit breakers
type CircuitBreakerStatus struct {
	TradingHalted     bool
	DailyLossLimit    bool
	DrawdownLimit     bool
	ConsecutiveLosses int
	CurrentDrawdown   float64
	DailyLoss         float64
	Reason            string
	HaltedAt          *time.Time
}

// PortfolioState contains current portfolio information for validation
type PortfolioState struct {
	TotalCapital      float64
	AvailableCapital  float64
	PeakCapital       float64
	DailyLoss         float64
	CurrentDrawdown   float64
	ConsecutiveLosses int
	ConsecutiveWins   int
	TotalDrawdown     float64
	OpenPositions     int
	TotalRiskExposed  float64
}
