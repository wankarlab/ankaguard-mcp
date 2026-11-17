package services

import "fmt"

// Hard server-side caps for per-user bot risk limits.
// These are absolute ceilings that no user config may exceed, regardless of
// what the user submits. They are separate from the trading engine's own
// risk parameters (which are per-strategy and can be lower).
const (
	// MinTradeSizeUSD is the smallest allowed single-trade size.
	// Below this, fees eat the position and slippage dominates.
	MinTradeSizeUSD = 0.5

	// MaxTradeSizeUSD is the hard ceiling for a single trade, server-side.
	// This maps to MAX_ORDER_SIZE_USD in the risk config.
	MaxTradeSizeUSD = 50.0

	// MaxDailyLossCap is the hard ceiling for per-user daily loss limit.
	// Even if a user requests a higher limit, we enforce this ceiling.
	MaxDailyLossCap = 100.0

	// MinOpenPositions is the minimum value the user may configure.
	MinOpenPositions = 1

	// MaxOpenPositionsCap is the hard ceiling for concurrent open positions per user.
	MaxOpenPositionsCap = 10

	// MinLeverage is the lowest meaningful leverage value (1 = no leverage).
	MinLeverage = 1

	// MaxLeverageCap is the hard ceiling for leverage. KuCoin supports up to 100x
	// but we cap server-side at a safer maximum.
	MaxLeverageCap = 20

	// maxConsecutiveFailures is the per-user in-process failure threshold.
	// When a user's bot exceeds this count the circuit opens and StartBot is blocked.
	// NOTE: This is the in-memory counter; the durable counter lives in
	// UserCredential.FailureCount (credentials package). Both are consulted
	// independently; this one trips faster for transient runtime failures.
	maxConsecutiveFailures = 5
)

// validateUserConfig applies hard server-side caps to a UserBotCfg before a bot
// is launched. It returns a descriptive error if any field violates a cap.
// Zero values are not validated here — the caller (StartBot / buildBotConfig)
// applies defaults before this function is called.
func validateUserConfig(cfg *UserBotCfg) error {
	if cfg == nil {
		return fmt.Errorf("validateUserConfig: cfg must not be nil")
	}

	// TradeSizeUSD bounds.
	if cfg.TradeSizeUSD != 0 {
		if cfg.TradeSizeUSD < MinTradeSizeUSD {
			return fmt.Errorf(
				"trade_size_usd %.2f is below minimum allowed (%.2f USD)",
				cfg.TradeSizeUSD, MinTradeSizeUSD,
			)
		}
		if cfg.TradeSizeUSD > MaxTradeSizeUSD {
			return fmt.Errorf(
				"trade_size_usd %.2f exceeds server hard cap (%.2f USD); reduce to comply",
				cfg.TradeSizeUSD, MaxTradeSizeUSD,
			)
		}
	}

	// MaxDailyLossUSD ceiling.
	if cfg.MaxDailyLossUSD > MaxDailyLossCap {
		return fmt.Errorf(
			"max_daily_loss_usd %.2f exceeds server hard cap (%.2f USD)",
			cfg.MaxDailyLossUSD, MaxDailyLossCap,
		)
	}

	// MaxOpenPositions range.
	if cfg.MaxOpenPositions != 0 {
		if cfg.MaxOpenPositions < MinOpenPositions {
			return fmt.Errorf(
				"max_open_positions %d is below minimum (%d)",
				cfg.MaxOpenPositions, MinOpenPositions,
			)
		}
		if cfg.MaxOpenPositions > MaxOpenPositionsCap {
			return fmt.Errorf(
				"max_open_positions %d exceeds server hard cap (%d)",
				cfg.MaxOpenPositions, MaxOpenPositionsCap,
			)
		}
	}

	// Leverage range (only meaningful for futures/margin; ignored for spot).
	if cfg.Leverage != 0 {
		if cfg.Leverage < MinLeverage {
			return fmt.Errorf(
				"leverage %d is below minimum (%d)",
				cfg.Leverage, MinLeverage,
			)
		}
		if cfg.Leverage > MaxLeverageCap {
			return fmt.Errorf(
				"leverage %d exceeds server hard cap (%d×)",
				cfg.Leverage, MaxLeverageCap,
			)
		}
	}

	return nil
}

// ValidateRealTradingAccess checks that the user's subscription tier allows
// real-money trading. PR-7: Free tier is paper-only; Basic+ allows real trading.
// kongLevel is the Kong-verified JWT level (1=Free, 2=Basic, 3=Pro, 4=Enterprise).
// dryRun=true bypasses this check (paper trading is always allowed).
func ValidateRealTradingAccess(kongLevel int, dryRun bool) error {
	if dryRun {
		return nil // paper trading is always allowed on any tier
	}
	const minRealTradingLevel = 2 // Basic and above
	if kongLevel < minRealTradingLevel {
		return fmt.Errorf(
			"real trading requires subscription level >= %d (Basic); current level=%d (Free tier = paper only)",
			minRealTradingLevel, kongLevel,
		)
	}
	return nil
}

// ValidateMaxBotsQuota checks that launching a new bot for userID would not exceed
// the per-tier MaxBots limit. PR-7: server-side enforcement before exchange calls.
func ValidateMaxBotsQuota(currentBotCount, tierMaxBots int) error {
	if tierMaxBots > 0 && currentBotCount >= tierMaxBots {
		return fmt.Errorf(
			"bot quota exhausted: %d/%d bots already active for this subscription tier",
			currentBotCount, tierMaxBots,
		)
	}
	return nil
}
