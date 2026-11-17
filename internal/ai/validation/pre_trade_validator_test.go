package validation

import (
	"testing"
)

func TestPreTradeValidator_Validate(t *testing.T) {
	config := &Config{
		MaxRiskPerTrade: 1.0,
		MinRiskReward:   1.5,
		MinConfluence:   3,
	}

	validator := NewPreTradeValidator(config)

	tests := []struct {
		name       string
		trade      *TradingDecision
		portfolio  *PortfolioState
		wantPassed bool
	}{
		{
			name: "valid trade",
			trade: &TradingDecision{
				Pair:            "BTCUSDT",
				Direction:       "LONG",
				EntryPrice:      45000.0,
				StopLoss:        44500.0,
				TakeProfit:      46500.0,
				PositionSize:    100.0,
				RiskPercent:     0.8,
				RiskRewardRatio: 3.0,
				ConfluenceCount: 5,
				Leverage:        5.0,
			},
			portfolio: &PortfolioState{
				TotalCapital:     10000.0,
				AvailableCapital: 9000.0,
			},
			wantPassed: true,
		},
		{
			name: "risk too high",
			trade: &TradingDecision{
				Pair:            "BTCUSDT",
				Direction:       "LONG",
				EntryPrice:      45000.0,
				StopLoss:        44500.0,
				TakeProfit:      46500.0,
				PositionSize:    100.0,
				RiskPercent:     1.5,
				RiskRewardRatio: 3.0,
				ConfluenceCount: 5,
				Leverage:        5.0,
			},
			portfolio: &PortfolioState{
				TotalCapital:     10000.0,
				AvailableCapital: 9000.0,
			},
			wantPassed: false,
		},
		{
			name: "poor risk-reward",
			trade: &TradingDecision{
				Pair:            "BTCUSDT",
				Direction:       "LONG",
				EntryPrice:      45000.0,
				StopLoss:        44500.0,
				TakeProfit:      45500.0,
				PositionSize:    100.0,
				RiskPercent:     0.8,
				RiskRewardRatio: 1.0,
				ConfluenceCount: 5,
				Leverage:        5.0,
			},
			portfolio: &PortfolioState{
				TotalCapital:     10000.0,
				AvailableCapital: 9000.0,
			},
			wantPassed: false,
		},
		{
			name: "insufficient confluence",
			trade: &TradingDecision{
				Pair:            "BTCUSDT",
				Direction:       "LONG",
				EntryPrice:      45000.0,
				StopLoss:        44500.0,
				TakeProfit:      46500.0,
				PositionSize:    100.0,
				RiskPercent:     0.8,
				RiskRewardRatio: 3.0,
				ConfluenceCount: 2,
				Leverage:        5.0,
			},
			portfolio: &PortfolioState{
				TotalCapital:     10000.0,
				AvailableCapital: 9000.0,
			},
			wantPassed: false,
		},
		{
			name: "excessive leverage",
			trade: &TradingDecision{
				Pair:            "BTCUSDT",
				Direction:       "LONG",
				EntryPrice:      45000.0,
				StopLoss:        44500.0,
				TakeProfit:      46500.0,
				PositionSize:    100.0,
				RiskPercent:     0.8,
				RiskRewardRatio: 3.0,
				ConfluenceCount: 5,
				Leverage:        15.0,
			},
			portfolio: &PortfolioState{
				TotalCapital:     10000.0,
				AvailableCapital: 9000.0,
			},
			wantPassed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := validator.Validate(tt.trade, tt.portfolio)

			if result.Passed != tt.wantPassed {
				t.Errorf("Validate() passed = %v, want %v", result.Passed, tt.wantPassed)
				if !result.Passed {
					t.Logf("Failures: %+v", result.CriticalFailures)
				}
			}
		})
	}
}

func TestPreTradeValidator_ValidateStopLoss(t *testing.T) {
	config := &Config{
		MaxRiskPerTrade: 1.0,
		MinRiskReward:   1.5,
		MinConfluence:   3,
	}

	validator := NewPreTradeValidator(config)

	tests := []struct {
		name       string
		trade      *TradingDecision
		wantPassed bool
	}{
		{
			name: "valid LONG stop loss",
			trade: &TradingDecision{
				Direction:  "LONG",
				EntryPrice: 45000.0,
				StopLoss:   44500.0,
			},
			wantPassed: true,
		},
		{
			name: "invalid LONG stop loss (above entry)",
			trade: &TradingDecision{
				Direction:  "LONG",
				EntryPrice: 45000.0,
				StopLoss:   45500.0,
			},
			wantPassed: false,
		},
		{
			name: "valid SHORT stop loss",
			trade: &TradingDecision{
				Direction:  "SHORT",
				EntryPrice: 45000.0,
				StopLoss:   45500.0,
			},
			wantPassed: true,
		},
		{
			name: "invalid SHORT stop loss (below entry)",
			trade: &TradingDecision{
				Direction:  "SHORT",
				EntryPrice: 45000.0,
				StopLoss:   44500.0,
			},
			wantPassed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &ValidationResult{
				Passed:           true,
				CriticalFailures: []RuleViolation{},
			}

			validator.validateStopLoss(tt.trade, result)

			if result.Passed != tt.wantPassed {
				t.Errorf("validateStopLoss() passed = %v, want %v", result.Passed, tt.wantPassed)
			}
		})
	}
}
