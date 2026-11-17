package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// UserBotConfig represents one row in user_bot_config.
// ConfigJSON holds the full TradingBotConfig serialized as JSON string
// to avoid import cycle with services package.
type UserBotConfig struct {
	ID                  int64
	UserID              string
	Exchange            string
	Enabled             bool
	TradeSizeUSD        float64
	Leverage            int
	MarketType          string
	ModeSelection       string
	ConfidenceThreshold float64
	MaxDailyLossUSD     float64
	MaxOpenPositions    int
	DryRun              bool
	ConfigJSON          string // Full TradingBotConfig as JSON
	UpdatedAt           time.Time
}

// UserBotConfigRepository persists per-user trading bot configuration.
type UserBotConfigRepository struct {
	db *sql.DB
}

// NewUserBotConfigRepository creates a new UserBotConfigRepository.
func NewUserBotConfigRepository(db *sql.DB) *UserBotConfigRepository {
	return &UserBotConfigRepository{db: db}
}

// Get retrieves the bot config for a user and exchange.
// Returns a sensible default-populated struct if no row exists, NOT an error.
// Defaults match the SQL table defaults.
func (r *UserBotConfigRepository) Get(ctx context.Context, userID, exchange string) (*UserBotConfig, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, user_id, exchange, enabled, trade_size_usd, leverage,
		       market_type, mode_selection, confidence_threshold, max_daily_loss_usd,
		       max_open_positions, dry_run, config_json, updated_at
		FROM user_bot_config
		WHERE user_id = ? AND exchange = ?
	`, userID, exchange)

	cfg := &UserBotConfig{
		UserID:              userID,
		Exchange:            exchange,
		Enabled:             false,
		TradeSizeUSD:        2.0,
		Leverage:            5,
		MarketType:          "futures",
		ModeSelection:       "auto",
		ConfidenceThreshold: 0.65,
		MaxDailyLossUSD:     20.0,
		MaxOpenPositions:    1,
		DryRun:              true,
		ConfigJSON:          "{}",
		UpdatedAt:           time.Now(),
	}

	var enabledInt int
	var dryRunInt int

	err := row.Scan(
		&cfg.ID, &cfg.UserID, &cfg.Exchange, &enabledInt,
		&cfg.TradeSizeUSD, &cfg.Leverage, &cfg.MarketType, &cfg.ModeSelection,
		&cfg.ConfidenceThreshold, &cfg.MaxDailyLossUSD, &cfg.MaxOpenPositions,
		&dryRunInt, &cfg.ConfigJSON, &cfg.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		// No row found; return default config
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query user bot config for user %q on %q: %w", userID, exchange, err)
	}

	// Convert INTEGER (0/1) back to bool
	cfg.Enabled = enabledInt != 0
	cfg.DryRun = dryRunInt != 0

	return cfg, nil
}

// Upsert inserts or updates a bot config, setting updated_at = CURRENT_TIMESTAMP.
// Uses SQLite upsert syntax: INSERT ... ON CONFLICT(user_id, exchange) DO UPDATE.
func (r *UserBotConfigRepository) Upsert(ctx context.Context, cfg *UserBotConfig) error {
	if cfg == nil {
		return fmt.Errorf("user bot config cannot be nil")
	}

	// Convert bool to INTEGER for storage
	enabledInt := 0
	if cfg.Enabled {
		enabledInt = 1
	}
	dryRunInt := 0
	if cfg.DryRun {
		dryRunInt = 1
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO user_bot_config (
			user_id, exchange, enabled, trade_size_usd, leverage,
			market_type, mode_selection, confidence_threshold, max_daily_loss_usd,
			max_open_positions, dry_run, config_json, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, exchange) DO UPDATE SET
			enabled              = excluded.enabled,
			trade_size_usd       = excluded.trade_size_usd,
			leverage             = excluded.leverage,
			market_type          = excluded.market_type,
			mode_selection       = excluded.mode_selection,
			confidence_threshold = excluded.confidence_threshold,
			max_daily_loss_usd   = excluded.max_daily_loss_usd,
			max_open_positions   = excluded.max_open_positions,
			dry_run              = excluded.dry_run,
			config_json          = excluded.config_json,
			updated_at           = excluded.updated_at
	`,
		cfg.UserID, cfg.Exchange, enabledInt, cfg.TradeSizeUSD, cfg.Leverage,
		cfg.MarketType, cfg.ModeSelection, cfg.ConfidenceThreshold, cfg.MaxDailyLossUSD,
		cfg.MaxOpenPositions, dryRunInt, cfg.ConfigJSON, time.Now(),
	)
	if err != nil {
		return fmt.Errorf("failed to upsert user bot config for user %q on %q: %w", cfg.UserID, cfg.Exchange, err)
	}
	return nil
}

// Delete removes the bot config for a user and exchange.
func (r *UserBotConfigRepository) Delete(ctx context.Context, userID, exchange string) error {
	if userID == "" || exchange == "" {
		return fmt.Errorf("userID and exchange cannot be empty")
	}

	_, err := r.db.ExecContext(ctx, `
		DELETE FROM user_bot_config
		WHERE user_id = ? AND exchange = ?
	`, userID, exchange)
	if err != nil {
		return fmt.Errorf("failed to delete user bot config for user %q on %q: %w", userID, exchange, err)
	}
	return nil
}

// ListEnabled returns all enabled bot configs (enabled=1) for bot resume-after-restart.
func (r *UserBotConfigRepository) ListEnabled(ctx context.Context) ([]*UserBotConfig, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, user_id, exchange, enabled, trade_size_usd, leverage,
		       market_type, mode_selection, confidence_threshold, max_daily_loss_usd,
		       max_open_positions, dry_run, config_json, updated_at
		FROM user_bot_config
		WHERE enabled = 1
		ORDER BY user_id ASC, exchange ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query enabled bot configs: %w", err)
	}
	defer rows.Close()

	var results []*UserBotConfig
	for rows.Next() {
		cfg := &UserBotConfig{}
		var enabledInt int
		var dryRunInt int

		err := rows.Scan(
			&cfg.ID, &cfg.UserID, &cfg.Exchange, &enabledInt,
			&cfg.TradeSizeUSD, &cfg.Leverage, &cfg.MarketType, &cfg.ModeSelection,
			&cfg.ConfidenceThreshold, &cfg.MaxDailyLossUSD, &cfg.MaxOpenPositions,
			&dryRunInt, &cfg.ConfigJSON, &cfg.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan enabled bot config row: %w", err)
		}

		cfg.Enabled = enabledInt != 0
		cfg.DryRun = dryRunInt != 0
		results = append(results, cfg)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error listing enabled bot configs: %w", err)
	}

	return results, nil
}
