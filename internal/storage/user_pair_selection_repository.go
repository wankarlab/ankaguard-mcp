package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// UserPairSelection represents a user's pair selection preferences for an exchange.
type UserPairSelection struct {
	ID        int64
	UserID    string
	Exchange  string
	Mode      string
	Pairs     []string
	Overrides map[string]any
	UpdatedAt time.Time
}

// UserPairSelectionRepository persists user pair selection preferences.
type UserPairSelectionRepository struct {
	db *sql.DB
}

// NewUserPairSelectionRepository creates a new repository instance.
func NewUserPairSelectionRepository(db *sql.DB) *UserPairSelectionRepository {
	return &UserPairSelectionRepository{
		db: db,
	}
}

// Get retrieves the pair selection for a user and exchange.
// Returns a default selection (mode "auto", empty pairs) if no row exists.
func (r *UserPairSelectionRepository) Get(ctx context.Context, userID, exchange string) (*UserPairSelection, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, user_id, exchange, mode, pairs, overrides, updated_at
		FROM user_pair_selection
		WHERE user_id = ? AND exchange = ?
	`, userID, exchange)

	sel := &UserPairSelection{
		UserID:    userID,
		Exchange:  exchange,
		Pairs:     []string{},
		Overrides: make(map[string]any),
	}

	var pairsJSON, overridesJSON string

	err := row.Scan(
		&sel.ID, &sel.UserID, &sel.Exchange, &sel.Mode,
		&pairsJSON, &overridesJSON, &sel.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		// Return default selection: mode "auto", empty pairs, empty overrides
		sel.Mode = "auto"
		sel.Pairs = []string{}
		sel.Overrides = make(map[string]any)
		sel.UpdatedAt = time.Now()
		return sel, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query user pair selection: %w", err)
	}

	// Unmarshal Pairs from JSON
	if pairsJSON != "" && pairsJSON != "[]" {
		if err := json.Unmarshal([]byte(pairsJSON), &sel.Pairs); err != nil {
			return nil, fmt.Errorf("failed to unmarshal pairs: %w", err)
		}
	} else {
		sel.Pairs = []string{}
	}

	// Unmarshal Overrides from JSON
	if overridesJSON != "" && overridesJSON != "{}" {
		if err := json.Unmarshal([]byte(overridesJSON), &sel.Overrides); err != nil {
			return nil, fmt.Errorf("failed to unmarshal overrides: %w", err)
		}
	} else {
		sel.Overrides = make(map[string]any)
	}

	return sel, nil
}

// Upsert inserts or updates a user pair selection, marshaling Pairs and Overrides to JSON.
func (r *UserPairSelectionRepository) Upsert(ctx context.Context, sel *UserPairSelection) error {
	if sel == nil {
		return fmt.Errorf("user pair selection cannot be nil")
	}

	// Marshal Pairs to JSON array
	pairsJSON, err := json.Marshal(sel.Pairs)
	if err != nil {
		return fmt.Errorf("failed to marshal pairs: %w", err)
	}

	// Marshal Overrides to JSON object
	overridesJSON, err := json.Marshal(sel.Overrides)
	if err != nil {
		return fmt.Errorf("failed to marshal overrides: %w", err)
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO user_pair_selection (
			user_id, exchange, mode, pairs, overrides, updated_at
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, exchange) DO UPDATE SET
			mode       = excluded.mode,
			pairs      = excluded.pairs,
			overrides  = excluded.overrides,
			updated_at = excluded.updated_at
	`,
		sel.UserID, sel.Exchange, sel.Mode,
		string(pairsJSON), string(overridesJSON), time.Now(),
	)
	if err != nil {
		return fmt.Errorf("failed to upsert user pair selection: %w", err)
	}
	return nil
}
