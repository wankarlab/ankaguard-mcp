package config

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// SeedDefaultProfiles creates default trading profiles in the database
func SeedDefaultProfiles(ctx context.Context, cm *ConfigManager, logger *logrus.Logger) error {
	if logger == nil {
		logger = logrus.New()
	}

	profiles := []*TradingProfile{
		GetScalpingAggressiveProfile(),
		GetScalpingModerateProfile(),
		GetScalpingConservativeProfile(),
	}

	for _, profile := range profiles {
		// Check if profile already exists
		existing, err := cm.LoadConfig(ctx, profile.Name)
		if err == nil && existing != nil {
			logger.WithField("profile", profile.Name).Debug("Profile already exists, skipping")
			continue
		}

		// Save profile
		if err := cm.SaveProfile(ctx, profile); err != nil {
			logger.WithError(err).WithField("profile", profile.Name).Warn("Failed to seed profile")
			continue
		}

		logger.WithField("profile", profile.Name).Info("Default profile seeded")
	}

	return nil
}

// GetScalpingAggressiveProfile returns aggressive 1-2 minute scalping profile
func GetScalpingAggressiveProfile() *TradingProfile {
	return &TradingProfile{
		ID:          uuid.New().String(),
		Name:        "scalping-aggressive",
		Description: "Aggressive 1-2 minute scalping with quick entries and exits",
		Config: DynamicTradingConfig{
			ProfileName:         "scalping-aggressive",
			Description:         "Aggressive 1-2 minute scalping",
			Pair:                "DOGE-USDT",
			Exchange:            "kucoin",
			Timeframes:          []string{"1m", "2m"},
			ConfidenceThreshold: 0.60,
			TradeSize:           2.0,
			Leverage:            5,

			AnalysisIntervalSeconds: 5,
			MonitorIntervalSeconds:  3,

			RiskPerTradePct:      0.5,
			MaxDailyLoss:         15.0,
			MaxDailyTrades:       100,
			ConsecutiveLossLimit: 3,
			LossReductionPct:     50.0,
			PauseAfterLosses:     5,
			PauseDurationMinutes: 15,
			MinWinRatePct:        35.0,
			MaxOpenPositions:     1,

			TakeProfitPct: 0.5,
			StopLossPct:   1.0,
			TPLevels: []TPLevel{
				{PricePct: 0.3, SizePct: 50},
				{PricePct: 0.5, SizePct: 50},
			},
			BreakevenConfig: &BreakevenConfig{
				Enabled:    true,
				TriggerPct: 0.3,
				BufferPct:  0.05,
			},
			TrailingConfig: &TrailingConfig{
				Enabled:     true,
				DistancePct: 0.2,
			},

			PostCloseReanalysis:    true,
			ReanalysisDelaySeconds: 1,
			MinConfluenceFactors:   2,
			QuickReentryEnabled:    true,
			MaxReentrySeconds:      5,
			ReanalysisIntervalSec:  15,

			DirectionSwitchingEnabled: true,
			MinSwitchIntervalMinutes:  10,
			AutoCloseOpposite:         true,
			SwitchCooldownMinutes:     5,

			EnabledTools:       []string{},
			MinToolsRequired:   2,
			ToolTimeoutSeconds: 20,
			ToolRetryEnabled:   true,
			MaxToolRetries:     2,

			OrderType:            "market",
			ExecutionTimeoutSec:  10,
			RetryOnFailure:       true,
			MaxExecutionRetries:  3,
			MaxLatencyMs:         500,
			SlippageTolerancePct: 0.15,

			MaxSpreadPct:            0.15,
			MinVolumeMultiplier:     0.5,
			MaxVolatilityMultiplier: 2.5,
			PauseOnNews:             true,
			NewsPauseMinutes:        15,
			FundingRateLimits: &FundingRateLimits{
				MaxPositive: 0.15,
				MaxNegative: -0.15,
			},

			LogLevel:                 "info",
			DetailedLogging:          true,
			ReportIntervalMinutes:    30,
			SaveTradeHistory:         true,
			MetricsUpdateIntervalSec: 15,
			AlertThresholds: &AlertThresholds{
				MaxDailyLossPct: 5.0,
				MinWinRatePct:   30.0,
				MaxDrawdownPct:  15.0,
			},
			NotificationChannels: []string{"webhook"},
			EmergencyStopConditions: &EmergencyConditions{
				MaxDailyLoss:      20.0,
				MaxDrawdownPct:    20.0,
				ConsecutiveLosses: 8,
			},

			AutoExecute:    false,
			DryRun:         true,
			AutonomousMode: true,
			HotReload:      true,

			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			Version:   1,
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		IsDefault: false,
		Tags:      []string{"scalping", "aggressive", "1min", "2min", "high-frequency"},
	}
}

// GetScalpingModerateProfile returns moderate 2-5 minute scalping profile
func GetScalpingModerateProfile() *TradingProfile {
	return &TradingProfile{
		ID:          uuid.New().String(),
		Name:        "scalping-moderate",
		Description: "Moderate 2-5 minute scalping with balanced risk/reward",
		Config:      *GetDefault(), // Use the default config
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		IsDefault:   true, // This is the default profile
		Tags:        []string{"scalping", "moderate", "2min", "3min", "5min", "balanced"},
	}
}

// GetScalpingConservativeProfile returns conservative 5-15 minute scalping profile
func GetScalpingConservativeProfile() *TradingProfile {
	return &TradingProfile{
		ID:          uuid.New().String(),
		Name:        "scalping-conservative",
		Description: "Conservative 5-15 minute scalping with higher confidence requirements",
		Config: DynamicTradingConfig{
			ProfileName:         "scalping-conservative",
			Description:         "Conservative 5-15 minute scalping",
			Pair:                "DOGE-USDT",
			Exchange:            "kucoin",
			Timeframes:          []string{"5m", "15m"},
			ConfidenceThreshold: 0.70,
			TradeSize:           3.0,
			Leverage:            3,

			AnalysisIntervalSeconds: 30,
			MonitorIntervalSeconds:  10,

			RiskPerTradePct:      0.5,
			MaxDailyLoss:         30.0,
			MaxDailyTrades:       20,
			ConsecutiveLossLimit: 3,
			LossReductionPct:     50.0,
			PauseAfterLosses:     4,
			PauseDurationMinutes: 60,
			MinWinRatePct:        45.0,
			MaxOpenPositions:     2,

			TakeProfitPct: 1.5,
			StopLossPct:   2.0,
			TPLevels: []TPLevel{
				{PricePct: 1.0, SizePct: 50},
				{PricePct: 1.5, SizePct: 50},
			},
			BreakevenConfig: &BreakevenConfig{
				Enabled:    true,
				TriggerPct: 0.7,
				BufferPct:  0.1,
			},
			TrailingConfig: &TrailingConfig{
				Enabled:     true,
				DistancePct: 0.5,
			},

			PostCloseReanalysis:    true,
			ReanalysisDelaySeconds: 5,
			MinConfluenceFactors:   4,
			QuickReentryEnabled:    false,
			MaxReentrySeconds:      30,
			ReanalysisIntervalSec:  60,

			DirectionSwitchingEnabled: true,
			MinSwitchIntervalMinutes:  30,
			AutoCloseOpposite:         true,
			SwitchCooldownMinutes:     15,

			EnabledTools:       []string{},
			MinToolsRequired:   4,
			ToolTimeoutSeconds: 45,
			ToolRetryEnabled:   true,
			MaxToolRetries:     3,

			OrderType:            "market",
			ExecutionTimeoutSec:  15,
			RetryOnFailure:       true,
			MaxExecutionRetries:  3,
			MaxLatencyMs:         1500,
			SlippageTolerancePct: 0.1,

			MaxSpreadPct:            0.1,
			MinVolumeMultiplier:     0.7,
			MaxVolatilityMultiplier: 1.5,
			PauseOnNews:             true,
			NewsPauseMinutes:        60,
			FundingRateLimits: &FundingRateLimits{
				MaxPositive: 0.08,
				MaxNegative: -0.08,
			},

			LogLevel:                 "info",
			DetailedLogging:          false,
			ReportIntervalMinutes:    120,
			SaveTradeHistory:         true,
			MetricsUpdateIntervalSec: 60,
			AlertThresholds: &AlertThresholds{
				MaxDailyLossPct: 5.0,
				MinWinRatePct:   40.0,
				MaxDrawdownPct:  12.0,
			},
			NotificationChannels: []string{"webhook"},
			EmergencyStopConditions: &EmergencyConditions{
				MaxDailyLoss:      50.0,
				MaxDrawdownPct:    20.0,
				ConsecutiveLosses: 6,
			},

			AutoExecute:    false,
			DryRun:         true,
			AutonomousMode: true,
			HotReload:      true,

			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			Version:   1,
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		IsDefault: false,
		Tags:      []string{"scalping", "conservative", "5min", "15min", "safe"},
	}
}
