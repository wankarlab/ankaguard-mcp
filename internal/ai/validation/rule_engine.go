package validation

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// RuleEngine executes validation rules
type RuleEngine struct {
	rulesDir string
	rules    []*Rule
	// thresholds overrides the literal right-hand side of a rule condition by
	// rule ID, so hot-reloaded limits (UpdateThresholds) also reach the YAML
	// rules that repeat them. Guarded by the owning service's lock.
	thresholds map[string]float64
}

// setThresholds replaces the per-rule threshold overrides.
func (re *RuleEngine) setThresholds(m map[string]float64) {
	re.thresholds = m
}

// NewRuleEngine creates a new rule engine
func NewRuleEngine(rulesDir string) *RuleEngine {
	return &RuleEngine{
		rulesDir: rulesDir,
		rules:    []*Rule{},
	}
}

// LoadRules loads validation rules from configuration
func (re *RuleEngine) LoadRules(ctx context.Context) error {
	if _, err := os.Stat(re.rulesDir); os.IsNotExist(err) {
		return fmt.Errorf("rules directory does not exist: %s", re.rulesDir)
	}

	re.rules = []*Rule{}

	err := filepath.Walk(re.rulesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return nil
		}

		rules, err := re.loadRuleFile(path)
		if err != nil {
			return fmt.Errorf("failed to load rules from %s: %w", path, err)
		}

		re.rules = append(re.rules, rules...)
		return nil
	})

	if err != nil {
		return err
	}

	// Sort rules by priority (higher priority first)
	sort.Slice(re.rules, func(i, j int) bool {
		return re.rules[i].Priority > re.rules[j].Priority
	})

	return nil
}

// loadRuleFile loads rules from a YAML file
func (re *RuleEngine) loadRuleFile(path string) ([]*Rule, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read rule file: %w", err)
	}

	var rulesData struct {
		Rules []struct {
			ID        string                 `yaml:"id"`
			Name      string                 `yaml:"name"`
			Category  string                 `yaml:"category"`
			Severity  string                 `yaml:"severity"`
			Priority  int                    `yaml:"priority"`
			Enabled   bool                   `yaml:"enabled"`
			Condition string                 `yaml:"condition"`
			Message   string                 `yaml:"message"`
			Metadata  map[string]interface{} `yaml:"metadata"`
		} `yaml:"rules"`
	}

	if err := yaml.Unmarshal(data, &rulesData); err != nil {
		return nil, fmt.Errorf("failed to parse rules YAML: %w", err)
	}

	rules := make([]*Rule, 0, len(rulesData.Rules))
	for _, ruleData := range rulesData.Rules {
		if !ruleData.Enabled {
			continue
		}

		rule := &Rule{
			ID:        ruleData.ID,
			Name:      ruleData.Name,
			Category:  ruleData.Category,
			Severity:  ruleData.Severity,
			Priority:  ruleData.Priority,
			Enabled:   ruleData.Enabled,
			Condition: ruleData.Condition,
			Message:   ruleData.Message,
			Metadata:  ruleData.Metadata,
		}

		rules = append(rules, rule)
	}

	return rules, nil
}

// ExecuteRules runs all rules against a trade
func (re *RuleEngine) ExecuteRules(
	trade *TradingDecision,
	portfolio *PortfolioState,
) *ValidationResult {
	result := &ValidationResult{
		Passed:           true,
		CriticalFailures: []RuleViolation{},
		Warnings:         []RuleViolation{},
		RulesEvaluated:   len(re.rules),
	}

	for _, rule := range re.rules {
		if !rule.Enabled {
			continue
		}

		if !ruleApplies(rule, trade) {
			continue
		}
		passed, err := re.evaluateRule(rule, trade, portfolio)
		if err != nil {
			// Log error but continue with other rules
			result.Warnings = append(result.Warnings, RuleViolation{
				RuleID:   rule.ID,
				RuleName: rule.Name,
				Severity: "ERROR",
				Message:  fmt.Sprintf("Rule evaluation error: %v", err),
			})
			continue
		}

		if !passed {
			violation := RuleViolation{
				RuleID:   rule.ID,
				RuleName: rule.Name,
				Severity: rule.Severity,
				Message:  rule.Message,
				Metadata: rule.Metadata,
			}

			if rule.Severity == "CRITICAL" || rule.Severity == "HIGH" {
				result.CriticalFailures = append(result.CriticalFailures, violation)
				result.Passed = false
			} else {
				result.Warnings = append(result.Warnings, violation)
			}
		}
	}

	return result
}

// evaluateRule evaluates a single rule
func (re *RuleEngine) evaluateRule(rule *Rule, trade *TradingDecision, portfolio *PortfolioState) (bool, error) {
	// Simple expression evaluator
	// Format: "field operator value"
	// Examples:
	//   "RiskPercent <= 1.0"
	//   "RiskRewardRatio >= 1.5"
	//   "ConfluenceCount >= 3"

	condition := strings.TrimSpace(rule.Condition)

	// Parse condition
	parts := strings.Fields(condition)
	if len(parts) < 3 {
		return false, fmt.Errorf("invalid condition format: %s", condition)
	}

	field := parts[0]
	operator := parts[1]
	valueStr := parts[2]
	if v, ok := re.thresholds[rule.ID]; ok {
		valueStr = fmt.Sprintf("%v", v)
	}

	// Get field value from trade or portfolio
	fieldValue, err := re.getFieldValue(field, trade, portfolio)
	if err != nil {
		return false, err
	}

	// The right-hand side may name another field ("StopLoss < EntryPrice").
	if other, ferr := re.getFieldValue(valueStr, trade, portfolio); ferr == nil {
		switch v := other.(type) {
		case float64:
			valueStr = fmt.Sprintf("%v", v)
		case int:
			valueStr = fmt.Sprintf("%d", v)
		case string:
			valueStr = v
		}
	}

	// Compare based on operator
	holds, err := re.compareValues(fieldValue, operator, valueStr)
	if err != nil {
		return false, err
	}
	// A rule with metadata trigger=true states the condition that FIRES it
	// ("DailyLoss >= 3.0" halts); every other rule states what must hold.
	if isTrigger(rule) {
		return !holds, nil
	}
	return holds, nil
}

// isTrigger reports whether the rule's condition describes the violation
// rather than the requirement.
func isTrigger(rule *Rule) bool {
	v, ok := rule.Metadata["trigger"].(bool)
	return ok && v
}

// ruleApplies honours the metadata scoping used by the rule files:
// direction (LONG/SHORT) and applies_to (pairs, or "alts" for everything
// except BTC/ETH). A rule without scoping applies to every trade.
func ruleApplies(rule *Rule, trade *TradingDecision) bool {
	if trade == nil {
		return true
	}
	if dir, ok := rule.Metadata["direction"].(string); ok && dir != "" {
		if !strings.EqualFold(dir, trade.Direction) {
			return false
		}
	}
	if raw, ok := rule.Metadata["applies_to"].([]interface{}); ok && len(raw) > 0 {
		pair := normalizePair(trade.Pair)
		for _, item := range raw {
			s, _ := item.(string)
			switch {
			case strings.EqualFold(s, "alts"):
				if pair != "BTCUSDT" && pair != "ETHUSDT" {
					return true
				}
			case normalizePair(s) == pair:
				return true
			}
		}
		return false
	}
	return true
}

// normalizePair folds "btc-usdt", "BTC/USDT" and "XBTUSDTM"-style spellings
// into the "BTCUSDT" form the rule files use.
func normalizePair(p string) string {
	p = strings.ToUpper(strings.NewReplacer("-", "", "/", "", "_", "").Replace(strings.TrimSpace(p)))
	if p == "XBTUSDTM" || p == "XBTUSDT" {
		return "BTCUSDT"
	}
	return strings.TrimSuffix(p, "M")
}

// getFieldValue extracts a field value from trade or portfolio
func (re *RuleEngine) getFieldValue(field string, trade *TradingDecision, portfolio *PortfolioState) (interface{}, error) {
	switch field {
	case "RiskPercent":
		return trade.RiskPercent, nil
	case "EntryPrice":
		return trade.EntryPrice, nil
	case "StopLoss":
		return trade.StopLoss, nil
	case "TakeProfit":
		return trade.TakeProfit, nil
	case "Volatility":
		return trade.Volatility, nil
	case "Spread":
		return trade.Spread, nil
	case "RiskRewardRatio":
		return trade.RiskRewardRatio, nil
	case "ConfluenceCount":
		return trade.ConfluenceCount, nil
	case "PositionSize":
		return trade.PositionSize, nil
	case "Leverage":
		return trade.Leverage, nil
	case "StopLossPercent":
		return trade.StopLossPercent, nil
	case "DailyLoss":
		// Rules express the daily loss in % of capital; the portfolio carries USD.
		if portfolio.TotalCapital > 0 {
			return portfolio.DailyLoss / portfolio.TotalCapital * 100, nil
		}
		return portfolio.DailyLoss, nil
	case "ConsecutiveLosses":
		return portfolio.ConsecutiveLosses, nil
	case "TotalDrawdown":
		return portfolio.TotalDrawdown, nil
	case "OpenPositions":
		return portfolio.OpenPositions, nil
	default:
		return nil, fmt.Errorf("unknown field: %s", field)
	}
}

// compareValues compares two values based on operator
func (re *RuleEngine) compareValues(fieldValue interface{}, operator, valueStr string) (bool, error) {
	// Strings only support equality.
	if sv, ok := fieldValue.(string); ok {
		switch operator {
		case "==":
			return strings.EqualFold(sv, valueStr), nil
		case "!=":
			return !strings.EqualFold(sv, valueStr), nil
		default:
			return false, fmt.Errorf("operator %s not supported for string fields", operator)
		}
	}

	// Convert values to float64 for comparison
	var fv float64
	var cv float64

	switch v := fieldValue.(type) {
	case float64:
		fv = v
	case int:
		fv = float64(v)
	case int64:
		fv = float64(v)
	default:
		return false, fmt.Errorf("unsupported field value type: %T", fieldValue)
	}

	// Parse comparison value
	_, err := fmt.Sscanf(valueStr, "%f", &cv)
	if err != nil {
		return false, fmt.Errorf("failed to parse comparison value: %w", err)
	}

	// Perform comparison
	switch operator {
	case "<=":
		return fv <= cv, nil
	case "<":
		return fv < cv, nil
	case ">=":
		return fv >= cv, nil
	case ">":
		return fv > cv, nil
	case "==":
		return fv == cv, nil
	case "!=":
		return fv != cv, nil
	default:
		return false, fmt.Errorf("unknown operator: %s", operator)
	}
}

// GetRules returns all loaded rules
func (re *RuleEngine) GetRules() []*Rule {
	return re.rules
}

// GetRuleByID retrieves a specific rule by ID
func (re *RuleEngine) GetRuleByID(id string) (*Rule, error) {
	for _, rule := range re.rules {
		if rule.ID == id {
			return rule, nil
		}
	}
	return nil, fmt.Errorf("rule not found: %s", id)
}

// Reload reloads all rules from disk
func (re *RuleEngine) Reload(ctx context.Context) error {
	return re.LoadRules(ctx)
}
