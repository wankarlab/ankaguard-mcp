package validation

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Service provides trade validation capabilities
type Service interface {
	// ValidateTrade checks if a trade proposal passes all rules
	ValidateTrade(ctx context.Context, trade *TradingDecision, portfolio *PortfolioState) (*ValidationResult, error)

	// CheckCircuitBreakers verifies if trading should continue
	CheckCircuitBreakers(ctx context.Context, portfolio *PortfolioState) (*CircuitBreakerStatus, error)

	// GetRules returns all loaded validation rules
	GetRules() []*Rule

	// ReloadRules reloads validation rules from configuration
	ReloadRules(ctx context.Context) error
}

// ValidationService implements the Service interface
type ValidationService struct {
	ruleEngine        *RuleEngine
	preTradeValidator *PreTradeValidator
	circuitBreaker    *CircuitBreakerManager
	postVerifier      *PostDecisionVerifier

	mu          sync.RWMutex
	initialized bool
}

// NewValidationService creates a new validation service
func NewValidationService(config *Config) *ValidationService {
	return &ValidationService{
		ruleEngine:        NewRuleEngine(config.RulesDir),
		preTradeValidator: NewPreTradeValidator(config),
		circuitBreaker:    NewCircuitBreakerManager(config),
		postVerifier:      NewPostDecisionVerifier(),
	}
}

// Initialize loads validation rules
func (v *ValidationService) Initialize(ctx context.Context) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if err := v.ruleEngine.LoadRules(ctx); err != nil {
		return fmt.Errorf("failed to load validation rules: %w", err)
	}

	v.initialized = true
	return nil
}

// ValidateTrade checks if a trade proposal passes all rules
func (v *ValidationService) ValidateTrade(
	ctx context.Context,
	trade *TradingDecision,
	portfolio *PortfolioState,
) (*ValidationResult, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if !v.initialized {
		return nil, fmt.Errorf("validation service not initialized")
	}

	startTime := time.Now()

	// Check circuit breakers first
	cbStatus, err := v.circuitBreaker.Check(portfolio)
	if err != nil {
		return nil, fmt.Errorf("circuit breaker check failed: %w", err)
	}

	if cbStatus.TradingHalted {
		return &ValidationResult{
			Passed: false,
			CriticalFailures: []RuleViolation{{
				RuleID:   "circuit_breaker",
				RuleName: "Circuit Breaker",
				Severity: "CRITICAL",
				Message:  cbStatus.Reason,
				Metadata: map[string]interface{}{
					"action": "halt_trading",
				},
			}},
			Timestamp:     time.Now(),
			ExecutionTime: time.Since(startTime),
		}, nil
	}

	// Run pre-trade validation
	preResult := v.preTradeValidator.Validate(trade, portfolio)

	// Execute all validation rules
	ruleResult := v.ruleEngine.ExecuteRules(trade, portfolio)

	// Post-decision verification
	postResult := v.postVerifier.Verify(trade)

	// Aggregate results
	result := v.aggregateResults(preResult, ruleResult, postResult)
	result.ExecutionTime = time.Since(startTime)

	return result, nil
}

// CheckCircuitBreakers verifies if trading should continue
func (v *ValidationService) CheckCircuitBreakers(
	ctx context.Context,
	portfolio *PortfolioState,
) (*CircuitBreakerStatus, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	return v.circuitBreaker.Check(portfolio)
}

// GetRules returns all loaded validation rules
func (v *ValidationService) GetRules() []*Rule {
	v.mu.RLock()
	defer v.mu.RUnlock()

	return v.ruleEngine.GetRules()
}

// ReloadRules reloads validation rules from configuration
func (v *ValidationService) ReloadRules(ctx context.Context) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	return v.ruleEngine.LoadRules(ctx)
}

// aggregateResults combines validation results from all validators
func (v *ValidationService) aggregateResults(
	preResult, ruleResult, postResult *ValidationResult,
) *ValidationResult {
	result := &ValidationResult{
		Passed:           true,
		CriticalFailures: []RuleViolation{},
		Warnings:         []RuleViolation{},
		Timestamp:        time.Now(),
	}

	// Aggregate critical failures
	result.CriticalFailures = append(result.CriticalFailures, preResult.CriticalFailures...)
	result.CriticalFailures = append(result.CriticalFailures, ruleResult.CriticalFailures...)
	result.CriticalFailures = append(result.CriticalFailures, postResult.CriticalFailures...)

	// Aggregate warnings
	result.Warnings = append(result.Warnings, preResult.Warnings...)
	result.Warnings = append(result.Warnings, ruleResult.Warnings...)
	result.Warnings = append(result.Warnings, postResult.Warnings...)

	// If any critical failures, mark as failed
	if len(result.CriticalFailures) > 0 {
		result.Passed = false
	}

	return result
}

// Thresholds are the hot-reloadable validation limits (percentages use the
// same unit as Config: 1.0 = 1 %). A zero field keeps the current value.
type Thresholds struct {
	MaxRiskPerTrade float64
	MaxDailyLoss    float64
	MaxDrawdown     float64
	MaxStopLossPct  float64
	MinRiskReward   float64
	MinConfluence   int
}

// UpdateThresholds applies new limits to the pre-trade validator, the circuit
// breaker and the YAML rules that repeat them, without reloading rule files and
// without resetting the circuit breaker's halt state.
func (v *ValidationService) UpdateThresholds(t Thresholds) {
	v.mu.Lock()
	defer v.mu.Unlock()

	// Copy-on-write: both components share the same *Config.
	next := *v.preTradeValidator.config
	if t.MaxRiskPerTrade > 0 {
		next.MaxRiskPerTrade = t.MaxRiskPerTrade
	}
	if t.MaxDailyLoss > 0 {
		next.MaxDailyLoss = t.MaxDailyLoss
	}
	if t.MaxDrawdown > 0 {
		next.MaxDrawdown = t.MaxDrawdown
	}
	if t.MaxStopLossPct > 0 {
		next.MaxStopLossPct = t.MaxStopLossPct
	}
	if t.MinRiskReward > 0 {
		next.MinRiskReward = t.MinRiskReward
	}
	if t.MinConfluence > 0 {
		next.MinConfluence = t.MinConfluence
	}
	v.preTradeValidator.config = &next
	v.circuitBreaker.setConfig(&next)
	// A zero limit means "not configured": the rule keeps its YAML literal.
	overrides := map[string]float64{}
	for id, val := range map[string]float64{
		"MAX_RISK_PER_TRADE":     next.MaxRiskPerTrade,
		"MIN_RISK_REWARD":        next.MinRiskReward,
		"DAILY_LOSS_LIMIT":       next.MaxDailyLoss,
		"DAILY_LOSS_HALT":        next.MaxDailyLoss,
		"DRAWDOWN_HALT":          next.MaxDrawdown,
		"STOP_LOSS_MAX_DISTANCE": next.MaxStopLossPct,
		"MIN_CONFLUENCE":         float64(next.MinConfluence),
		"CONFIDENCE_ALIGNMENT":   float64(next.MinConfluence),
	} {
		if val > 0 {
			overrides[id] = val
		}
	}
	v.ruleEngine.setThresholds(overrides)
}

// CurrentThresholds returns the limits in force.
func (v *ValidationService) CurrentThresholds() Thresholds {
	v.mu.RLock()
	defer v.mu.RUnlock()
	c := v.preTradeValidator.config
	return Thresholds{
		MaxRiskPerTrade: c.MaxRiskPerTrade,
		MaxDailyLoss:    c.MaxDailyLoss,
		MaxDrawdown:     c.MaxDrawdown,
		MaxStopLossPct:  c.MaxStopLossPct,
		MinRiskReward:   c.MinRiskReward,
		MinConfluence:   c.MinConfluence,
	}
}
