package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// valid enum sets for validation.
var validMarketTypes = map[string]bool{
	"spot":    true,
	"futures": true,
	"margin":  true,
}

var validModeSelections = map[string]bool{
	"auto":   true,
	"manual": true,
	"hybrid": true,
}

// UserTradingMode represents one row in user_trading_mode.
type UserTradingMode struct {
	ID            int64
	UserID        string
	Scope         string // "global" or "pair"
	Pair          string // empty when Scope == "global"
	MarketType    string // spot | futures | margin
	ModeSelection string // auto | manual | hybrid
	Leverage      float64
	UpdatedAt     time.Time
}

// TradingModeRepository persists user trading mode preferences.
type TradingModeRepository struct {
	db *sql.DB
}

// NewTradingModeRepository creates a new TradingModeRepository.
func NewTradingModeRepository(db *sql.DB) *TradingModeRepository {
	return &TradingModeRepository{db: db}
}

// validateMarketType returns an error if marketType is not a recognised value.
func validateMarketType(marketType string) error {
	if !validMarketTypes[marketType] {
		return fmt.Errorf("market_type must be one of spot/futures/margin; got %q", marketType)
	}
	return nil
}

// validateModeSelection returns an error if modeSelection is not a recognised value.
func validateModeSelection(modeSelection string) error {
	if !validModeSelections[modeSelection] {
		return fmt.Errorf("mode_selection must be one of auto/manual/hybrid; got %q", modeSelection)
	}
	return nil
}

// validateSpotLeverage enforces that spot market type cannot use leverage > 1.
func validateSpotLeverage(marketType string, leverage float64) error {
	if marketType == "spot" && leverage > 1 {
		return fmt.Errorf("spot market_type requires leverage <= 1; got %g", leverage)
	}
	return nil
}

// scan reads a UserTradingMode from a sql.Row or sql.Rows.
func scanTradingMode(scan func(...interface{}) error) (*UserTradingMode, error) {
	m := &UserTradingMode{}
	err := scan(&m.ID, &m.UserID, &m.Scope, &m.Pair, &m.MarketType, &m.ModeSelection, &m.Leverage, &m.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return m, nil
}

// GetGlobal returns the global trading mode for user "default".
// Returns a sensible default (futures/auto/0) when no row exists.
func (r *TradingModeRepository) GetGlobal(ctx context.Context) (*UserTradingMode, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, user_id, scope, pair, market_type, mode_selection, leverage, updated_at
		FROM user_trading_mode
		WHERE user_id = 'default' AND scope = 'global'
	`)
	m, err := scanTradingMode(row.Scan)
	if err == sql.ErrNoRows {
		return &UserTradingMode{
			UserID:        "default",
			Scope:         "global",
			Pair:          "",
			MarketType:    "futures",
			ModeSelection: "auto",
			Leverage:      0,
			UpdatedAt:     time.Now(),
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query global trading mode: %w", err)
	}
	return m, nil
}

// GetForPair returns the per-pair trading mode override for user "default" and the given pair.
// Returns nil (no error) when no per-pair row exists.
func (r *TradingModeRepository) GetForPair(ctx context.Context, pair string) (*UserTradingMode, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, user_id, scope, pair, market_type, mode_selection, leverage, updated_at
		FROM user_trading_mode
		WHERE user_id = 'default' AND scope = 'pair' AND pair = ?
	`, pair)
	m, err := scanTradingMode(row.Scan)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query pair trading mode for %q: %w", pair, err)
	}
	return m, nil
}

// SetGlobal persists the global trading mode for user "default".
// Validates enums and the spot ⇒ leverage ≤ 1 constraint.
func (r *TradingModeRepository) SetGlobal(ctx context.Context, marketType, modeSelection string, leverage float64) error {
	if err := validateMarketType(marketType); err != nil {
		return err
	}
	if err := validateModeSelection(modeSelection); err != nil {
		return err
	}
	if err := validateSpotLeverage(marketType, leverage); err != nil {
		return err
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO user_trading_mode (user_id, scope, pair, market_type, mode_selection, leverage, updated_at)
		VALUES ('default', 'global', '', ?, ?, ?, ?)
		ON CONFLICT(user_id, scope, pair) DO UPDATE SET
			market_type    = excluded.market_type,
			mode_selection = excluded.mode_selection,
			leverage       = excluded.leverage,
			updated_at     = excluded.updated_at
	`, marketType, modeSelection, leverage, time.Now())
	if err != nil {
		return fmt.Errorf("failed to upsert global trading mode: %w", err)
	}
	return nil
}

// SetForPair persists a per-pair trading mode override for user "default".
// Validates market_type and the spot ⇒ leverage ≤ 1 constraint.
// mode_selection is always stored as "manual" for per-pair overrides.
func (r *TradingModeRepository) SetForPair(ctx context.Context, pair, marketType string, leverage float64) error {
	if pair == "" {
		return fmt.Errorf("pair cannot be empty for per-pair override")
	}
	if err := validateMarketType(marketType); err != nil {
		return err
	}
	if err := validateSpotLeverage(marketType, leverage); err != nil {
		return err
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO user_trading_mode (user_id, scope, pair, market_type, mode_selection, leverage, updated_at)
		VALUES ('default', 'pair', ?, ?, 'manual', ?, ?)
		ON CONFLICT(user_id, scope, pair) DO UPDATE SET
			market_type    = excluded.market_type,
			mode_selection = excluded.mode_selection,
			leverage       = excluded.leverage,
			updated_at     = excluded.updated_at
	`, pair, marketType, leverage, time.Now())
	if err != nil {
		return fmt.Errorf("failed to upsert per-pair trading mode for %q: %w", pair, err)
	}
	return nil
}

// DeleteForPair removes the per-pair trading mode override, reverting to global settings.
func (r *TradingModeRepository) DeleteForPair(ctx context.Context, pair string) error {
	if pair == "" {
		return fmt.Errorf("pair cannot be empty")
	}
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM user_trading_mode
		WHERE user_id = 'default' AND scope = 'pair' AND pair = ?
	`, pair)
	if err != nil {
		return fmt.Errorf("failed to delete per-pair trading mode for %q: %w", pair, err)
	}
	return nil
}

// ListAll returns all trading mode rows for user "default" (global first, then per-pair).
func (r *TradingModeRepository) ListAll(ctx context.Context) ([]*UserTradingMode, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, user_id, scope, pair, market_type, mode_selection, leverage, updated_at
		FROM user_trading_mode
		WHERE user_id = 'default'
		ORDER BY scope ASC, pair ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list trading modes: %w", err)
	}
	defer rows.Close()

	var results []*UserTradingMode
	for rows.Next() {
		m, err := scanTradingMode(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("failed to scan trading mode row: %w", err)
		}
		results = append(results, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error listing trading modes: %w", err)
	}
	return results, nil
}
