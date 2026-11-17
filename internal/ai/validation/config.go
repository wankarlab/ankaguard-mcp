package validation

// Config holds configuration for the validation system
type Config struct {
	// RulesDir is the directory containing validation rule files
	RulesDir string

	// StrictMode enables strict validation (fail on warnings)
	StrictMode bool

	// LogAllViolations logs all rule violations including warnings
	LogAllViolations bool

	// CircuitBreakerEnabled enables circuit breaker functionality
	CircuitBreakerEnabled bool

	// Risk limits
	// Risk limits are percentages (1.0 = 1%), the same unit the rule YAML, the
	// circuit breaker and TradingDecision.RiskPercent use.
	MaxRiskPerTrade      float64 // Maximum risk per trade, % of capital (e.g., 1.0 = 1%)
	MaxDailyLoss         float64 // Maximum daily loss, % of capital (e.g., 3.0 = 3%)
	MaxDrawdown          float64 // Maximum drawdown before halt, % (e.g., 10.0 = 10%)
	MaxStopLossPct       float64 // Maximum stop distance from entry, % of price (e.g., 5.0 = 5%)
	ConsecutiveLossLimit int     // Number of consecutive losses before action

	// Position sizing
	MinRiskReward float64 // Minimum risk-reward ratio (e.g., 1.5)
	MinConfluence int     // Minimum confluence factors required
}

// DefaultConfig returns default validation configuration
func DefaultConfig() *Config {
	return &Config{
		RulesDir:              "config/validation",
		StrictMode:            true,
		LogAllViolations:      true,
		CircuitBreakerEnabled: true,
		MaxRiskPerTrade:       1.0,  // 1% of capital
		MaxDailyLoss:          3.0,  // 3% of capital
		MaxDrawdown:           10.0, // 10%
		MaxStopLossPct:        5.0,  // 5% of price
		ConsecutiveLossLimit:  3,
		MinRiskReward:         1.5,
		MinConfluence:         3,
	}
}
