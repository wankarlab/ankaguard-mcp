package plans

import (
	"github.com/rantipay/trading-mcp/pkg/auth"
)

// KongLevelMapping maps Kong Auth Service levels to MATP subscription plans
type KongLevelMapping struct {
	Level       int            `json:"level"`
	Name        string         `json:"name"`
	AuthLevel   auth.AuthLevel `json:"auth_level"`
	Features    []string       `json:"features"`
	Limits      *PlanLimits    `json:"limits"`
	Description string         `json:"description"`
}

// PlanLimits defines usage limits for each MATP plan
type PlanLimits struct {
	APICallsPerMinute    int  `json:"api_calls_per_minute"`
	MaxIndicators        int  `json:"max_indicators"`
	MaxBacktests         int  `json:"max_backtests"`
	MaxPositions         int  `json:"max_positions"`
	MaxDailyTrades       int  `json:"max_daily_trades"`
	RealTradingEnabled   bool `json:"real_trading_enabled"`
	AdminAccess          bool `json:"admin_access"`
	BulkOperations       bool `json:"bulk_operations"`
	AdvancedAnalytics    bool `json:"advanced_analytics"`
	PrioritySupport      bool `json:"priority_support"`
	CustomIndicators     bool `json:"custom_indicators"`
	WebhookNotifications bool `json:"webhook_notifications"`
}

// GetKongLevelMappings returns the complete mapping of Kong Auth Service levels to MATP plans
func GetKongLevelMappings() map[int]*KongLevelMapping {
	return map[int]*KongLevelMapping{
		1: {
			Level:     1,
			Name:      "Basic Plan",
			AuthLevel: auth.BASIC,
			Features: []string{
				"view_data",
				"basic_indicators",
				"market_data_access",
				"basic_charts",
				"educational_content",
			},
			Limits: &PlanLimits{
				APICallsPerMinute:    100,
				MaxIndicators:        5,
				MaxBacktests:         0,
				MaxPositions:         0,
				MaxDailyTrades:       0,
				RealTradingEnabled:   false,
				AdminAccess:          false,
				BulkOperations:       false,
				AdvancedAnalytics:    false,
				PrioritySupport:      false,
				CustomIndicators:     false,
				WebhookNotifications: false,
			},
			Description: "Basic access to market data and simple indicators. Perfect for learning and exploration.",
		},
		2: {
			Level:     2,
			Name:      "Standard Plan",
			AuthLevel: auth.VERIFIED,
			Features: []string{
				"all_basic_features",
				"signals_generation",
				"backtesting",
				"alerts_notifications",
				"advanced_indicators",
				"strategy_templates",
				"paper_trading",
			},
			Limits: &PlanLimits{
				APICallsPerMinute:    500,
				MaxIndicators:        13, // All available indicators
				MaxBacktests:         10,
				MaxPositions:         5,  // Paper trading only
				MaxDailyTrades:       20, // Paper trading only
				RealTradingEnabled:   false,
				AdminAccess:          false,
				BulkOperations:       false,
				AdvancedAnalytics:    true,
				PrioritySupport:      false,
				CustomIndicators:     false,
				WebhookNotifications: true,
			},
			Description: "Enhanced features including signal generation, backtesting, and paper trading. Ideal for strategy development.",
		},
		3: {
			Level:     3,
			Name:      "Premium Plan",
			AuthLevel: auth.PREMIUM,
			Features: []string{
				"all_standard_features",
				"real_trading",
				"risk_management",
				"position_management",
				"automated_execution",
				"advanced_backtesting",
				"portfolio_analytics",
				"custom_strategies",
			},
			Limits: &PlanLimits{
				APICallsPerMinute:    2000,
				MaxIndicators:        13, // All available indicators
				MaxBacktests:         50,
				MaxPositions:         20,  // Real trading
				MaxDailyTrades:       100, // Real trading
				RealTradingEnabled:   true,
				AdminAccess:          false,
				BulkOperations:       false,
				AdvancedAnalytics:    true,
				PrioritySupport:      true,
				CustomIndicators:     true,
				WebhookNotifications: true,
			},
			Description: "Full trading capabilities with real money, advanced risk management, and automated execution.",
		},
		4: {
			Level:     4,
			Name:      "Enterprise Plan",
			AuthLevel: auth.ENTERPRISE,
			Features: []string{
				"all_premium_features",
				"admin_apis",
				"bulk_operations",
				"user_management",
				"system_analytics",
				"white_label_options",
				"dedicated_support",
				"custom_integrations",
				"unlimited_everything",
			},
			Limits: &PlanLimits{
				APICallsPerMinute:    0,  // Unlimited
				MaxIndicators:        13, // All available indicators
				MaxBacktests:         0,  // Unlimited
				MaxPositions:         0,  // Unlimited
				MaxDailyTrades:       0,  // Unlimited
				RealTradingEnabled:   true,
				AdminAccess:          true,
				BulkOperations:       true,
				AdvancedAnalytics:    true,
				PrioritySupport:      true,
				CustomIndicators:     true,
				WebhookNotifications: true,
			},
			Description: "Enterprise-grade access with admin capabilities, unlimited usage, and dedicated support.",
		},
	}
}

// GetPlanByKongLevel returns the MATP plan configuration for a given Kong Auth Service level
func GetPlanByKongLevel(level int) *KongLevelMapping {
	mappings := GetKongLevelMappings()
	if plan, exists := mappings[level]; exists {
		return plan
	}
	// Default to Basic plan if level not found
	return mappings[1]
}

// GetAuthLevelByKongLevel returns the auth.AuthLevel for a given Kong level
func GetAuthLevelByKongLevel(level int) auth.AuthLevel {
	plan := GetPlanByKongLevel(level)
	return plan.AuthLevel
}

// AnkaLaunch features (docs/bot/frd.md §3.12).
const (
	FeatureLaunchAnalyze = "launch_analyze"
	FeatureLaunchOperate = "launch_operate"
)

// HasFeature checks if a Kong level has access to a specific feature
func HasFeature(level int, feature string) bool {
	plan := GetPlanByKongLevel(level)

	// Check direct feature access
	for _, f := range plan.Features {
		if f == feature {
			return true
		}
	}

	// Check inherited features based on level
	switch feature {
	case "view_data", "basic_indicators", "market_data_access", "basic_charts", "educational_content":
		return level >= 1 // All levels have basic features
	case "signals_generation", "backtesting", "alerts_notifications", "advanced_indicators", "strategy_templates", "paper_trading":
		return level >= 2 // Standard and above
	case "real_trading", "risk_management", "position_management", "automated_execution", "advanced_backtesting", "portfolio_analytics", "custom_strategies":
		return level >= 3 // Premium and above
	case "admin_apis", "bulk_operations", "user_management", "system_analytics", "white_label_options", "dedicated_support", "custom_integrations", "unlimited_everything":
		return level >= 4 // Enterprise only
	// RBAC-1: governance roles added to level-based feature check
	case "review": // reviewer: can read and comment on Spec gate results
		return level >= 3 // Pro and above
	case "arbitrate": // arbiter: can resolve Spec gate appeals
		return level >= 4 // Enterprise only
	// AnkaLaunch phase 1 (openspec ankalaunch-phase1-shadow).
	case FeatureLaunchAnalyze: // edit strategy versions, replay, export
		return level >= 2
	case FeatureLaunchOperate: // manual approval queue, kill switch / resume
		return level >= 3
	}

	return false
}

// GetAPIRateLimit returns the API rate limit for a Kong level
func GetAPIRateLimit(level int) int {
	plan := GetPlanByKongLevel(level)
	return plan.Limits.APICallsPerMinute
}

// CanAccessRealTrading checks if a Kong level can access real trading features
func CanAccessRealTrading(level int) bool {
	plan := GetPlanByKongLevel(level)
	return plan.Limits.RealTradingEnabled
}

// CanAccessAdminFeatures checks if a Kong level can access admin features
func CanAccessAdminFeatures(level int) bool {
	plan := GetPlanByKongLevel(level)
	return plan.Limits.AdminAccess
}

// GetMaxPositions returns the maximum number of positions for a Kong level
func GetMaxPositions(level int) int {
	plan := GetPlanByKongLevel(level)
	return plan.Limits.MaxPositions
}

// GetMaxDailyTrades returns the maximum number of daily trades for a Kong level
func GetMaxDailyTrades(level int) int {
	plan := GetPlanByKongLevel(level)
	return plan.Limits.MaxDailyTrades
}

// ValidateKongLevel checks if a Kong level is valid
func ValidateKongLevel(level int) bool {
	mappings := GetKongLevelMappings()
	_, exists := mappings[level]
	return exists
}

// GetAllPlans returns all available MATP plans
func GetAllPlans() []*KongLevelMapping {
	mappings := GetKongLevelMappings()
	plans := make([]*KongLevelMapping, 0, len(mappings))

	// Return in order: Basic (1), Standard (2), Premium (3), Enterprise (4)
	for i := 1; i <= 4; i++ {
		if plan, exists := mappings[i]; exists {
			plans = append(plans, plan)
		}
	}

	return plans
}

// PlanComparison provides a comparison structure for frontend display
type PlanComparison struct {
	Feature    string      `json:"feature"`
	Basic      interface{} `json:"basic"`      // Level 1
	Standard   interface{} `json:"standard"`   // Level 2
	Premium    interface{} `json:"premium"`    // Level 3
	Enterprise interface{} `json:"enterprise"` // Level 4
}

// GetPlanComparison returns a feature comparison matrix for all plans
func GetPlanComparison() []*PlanComparison {
	mappings := GetKongLevelMappings()

	return []*PlanComparison{
		{
			Feature:    "API Calls per Minute",
			Basic:      mappings[1].Limits.APICallsPerMinute,
			Standard:   mappings[2].Limits.APICallsPerMinute,
			Premium:    mappings[3].Limits.APICallsPerMinute,
			Enterprise: "Unlimited",
		},
		{
			Feature:    "Technical Indicators",
			Basic:      mappings[1].Limits.MaxIndicators,
			Standard:   "All (13)",
			Premium:    "All (13)",
			Enterprise: "All (13)",
		},
		{
			Feature:    "Backtesting",
			Basic:      "❌",
			Standard:   "✅ (10/month)",
			Premium:    "✅ (50/month)",
			Enterprise: "✅ Unlimited",
		},
		{
			Feature:    "Real Trading",
			Basic:      "❌",
			Standard:   "❌ (Paper only)",
			Premium:    "✅",
			Enterprise: "✅",
		},
		{
			Feature:    "Max Positions",
			Basic:      "❌",
			Standard:   "5 (Paper)",
			Premium:    "20 (Real)",
			Enterprise: "Unlimited",
		},
		{
			Feature:    "Admin Access",
			Basic:      "❌",
			Standard:   "❌",
			Premium:    "❌",
			Enterprise: "✅",
		},
		{
			Feature:    "Priority Support",
			Basic:      "❌",
			Standard:   "❌",
			Premium:    "✅",
			Enterprise: "✅ Dedicated",
		},
		{
			Feature:    "Custom Indicators",
			Basic:      "❌",
			Standard:   "❌",
			Premium:    "✅",
			Enterprise: "✅",
		},
		{
			Feature:    "Webhook Notifications",
			Basic:      "❌",
			Standard:   "✅",
			Premium:    "✅",
			Enterprise: "✅",
		},
	}
}
