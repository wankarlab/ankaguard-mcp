package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rantipay/trading-mcp/internal/strategy"
	"github.com/sirupsen/logrus"
)

// StrategySpecRepository persists StrategySpec documents in SQLite (embedded,
// physical isolation per user) or Postgres (RLS by user_id).
// BA-9: CRUD scoped by user_id; idempotent Save via ON CONFLICT DO NOTHING.
type StrategySpecRepository struct {
	db  *sql.DB
	log *logrus.Logger
}

// NewStrategySpecRepository creates a StrategySpecRepository.
func NewStrategySpecRepository(db *sql.DB, log *logrus.Logger) *StrategySpecRepository {
	return &StrategySpecRepository{db: db, log: log}
}

// Save persists s. Idempotent: if a row with the same (spec_id, user_id) already
// exists it is not overwritten (ON CONFLICT DO NOTHING). Returns nil on either
// insert or skip.
func (r *StrategySpecRepository) Save(ctx context.Context, userID string, s *strategy.StrategySpec) error {
	scopeJSON, err := json.Marshal(s.Scope)
	if err != nil {
		return fmt.Errorf("marshal scope: %w", err)
	}
	now := time.Now()
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO strategy_specs
		  (spec_id, user_id, name, author, revision, parent_revision, status,
		   description, scope_json, config_override, validation_verdict, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '{}', '{}', ?, ?)
		ON CONFLICT(spec_id, user_id) DO NOTHING
	`,
		s.Metadata.SpecID, userID, s.Metadata.Name, s.Metadata.Author,
		s.Metadata.Revision, s.Metadata.ParentRevision, s.Metadata.Status,
		s.Metadata.Description, string(scopeJSON), now, now,
	)
	if err != nil {
		return fmt.Errorf("save strategy_spec: %w", err)
	}
	return nil
}

// UpdateStatus transitions the lifecycle status of a Spec.
func (r *StrategySpecRepository) UpdateStatus(ctx context.Context, specID, userID, status string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE strategy_specs SET status = ?, updated_at = ? WHERE spec_id = ? AND user_id = ?
	`, status, time.Now(), specID, userID)
	return err
}

// UpdateStatusAny updates a Spec's status without the owner filter. Used by the
// background certification loop, which processes specs across all authors/bots.
func (r *StrategySpecRepository) UpdateStatusAny(ctx context.Context, specID, status string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE strategy_specs SET status = ?, updated_at = ? WHERE spec_id = ?
	`, status, time.Now(), specID)
	return err
}

// SaveVerdictAny writes the validation blob without the owner filter (cert loop).
func (r *StrategySpecRepository) SaveVerdictAny(ctx context.Context, specID string, verdict strategy.ValidationBlock) error {
	b, err := json.Marshal(verdict)
	if err != nil {
		return fmt.Errorf("marshal verdict: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `
		UPDATE strategy_specs SET validation_verdict = ?, updated_at = ? WHERE spec_id = ?
	`, string(b), time.Now(), specID)
	return err
}

// LoadBySpecIDAny returns a Spec row by id without the owner filter (cert loop / public validate).
func (r *StrategySpecRepository) LoadBySpecIDAny(ctx context.Context, specID string) (*StrategySpecRow, error) {
	var row StrategySpecRow
	err := r.db.QueryRowContext(ctx, `
		SELECT spec_id, user_id, name, author, revision, parent_revision, status,
		       description, scope_json, config_override, validation_verdict, created_at, updated_at
		FROM strategy_specs WHERE spec_id = ?
	`, specID).Scan(
		&row.SpecID, &row.UserID, &row.Name, &row.Author, &row.Revision,
		&row.ParentRevision, &row.Status, &row.Description, &row.ScopeJSON,
		&row.ConfigOverride, &row.ValidationVerdict, &row.CreatedAt, &row.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// ListByStatusesAny returns all Specs whose status is in the given set, across
// all users (the public/community Gate). Empty set → all specs.
func (r *StrategySpecRepository) ListByStatusesAny(ctx context.Context, statuses []string) ([]*StrategySpecRow, error) {
	query := `SELECT spec_id, user_id, name, author, revision, parent_revision, status,
	                 description, scope_json, config_override, validation_verdict, created_at, updated_at
	          FROM strategy_specs`
	var args []any
	if len(statuses) > 0 {
		ph := ""
		for i, s := range statuses {
			if i > 0 {
				ph += ","
			}
			ph += "?"
			args = append(args, s)
		}
		query += " WHERE status IN (" + ph + ")"
	}
	query += " ORDER BY updated_at DESC"

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list strategy_specs by statuses: %w", err)
	}
	defer rows.Close()

	var out []*StrategySpecRow
	for rows.Next() {
		var row StrategySpecRow
		if err := rows.Scan(
			&row.SpecID, &row.UserID, &row.Name, &row.Author, &row.Revision,
			&row.ParentRevision, &row.Status, &row.Description, &row.ScopeJSON,
			&row.ConfigOverride, &row.ValidationVerdict, &row.CreatedAt, &row.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, &row)
	}
	return out, rows.Err()
}

// ListByStatusAny returns all Specs with the given status across all users
// (cert loop). status must be non-empty.
func (r *StrategySpecRepository) ListByStatusAny(ctx context.Context, status string) ([]*StrategySpecRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT spec_id, user_id, name, author, revision, parent_revision, status,
		       description, scope_json, config_override, validation_verdict, created_at, updated_at
		FROM strategy_specs WHERE status = ? ORDER BY updated_at ASC
	`, status)
	if err != nil {
		return nil, fmt.Errorf("list strategy_specs by status: %w", err)
	}
	defer rows.Close()

	var out []*StrategySpecRow
	for rows.Next() {
		var row StrategySpecRow
		if err := rows.Scan(
			&row.SpecID, &row.UserID, &row.Name, &row.Author, &row.Revision,
			&row.ParentRevision, &row.Status, &row.Description, &row.ScopeJSON,
			&row.ConfigOverride, &row.ValidationVerdict, &row.CreatedAt, &row.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, &row)
	}
	return out, rows.Err()
}

// SaveVerdict writes the STATUS-ONLY validation blob to strategy_specs.validation_verdict.
// This is the ONLY write path for the validation block — called by ValidationService.
func (r *StrategySpecRepository) SaveVerdict(ctx context.Context, specID, userID string, verdict strategy.ValidationBlock) error {
	b, err := json.Marshal(verdict)
	if err != nil {
		return fmt.Errorf("marshal verdict: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `
		UPDATE strategy_specs SET validation_verdict = ?, updated_at = ? WHERE spec_id = ? AND user_id = ?
	`, string(b), time.Now(), specID, userID)
	return err
}

// SaveConfigOverride stores the typed ToConfigOverride output for a Spec.
func (r *StrategySpecRepository) SaveConfigOverride(ctx context.Context, specID, userID string,
	floats map[string]float64, bools map[string]bool, ints map[string]int) error {

	override := map[string]any{
		"floats": floats,
		"bools":  bools,
		"ints":   ints,
	}
	b, err := json.Marshal(override)
	if err != nil {
		return fmt.Errorf("marshal config_override: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `
		UPDATE strategy_specs SET config_override = ?, updated_at = ? WHERE spec_id = ? AND user_id = ?
	`, string(b), time.Now(), specID, userID)
	return err
}

// LoadBySpecID returns the raw Spec row for (specID, userID).
// Non-owner access returns sql.ErrNoRows so callers can map to 404.
func (r *StrategySpecRepository) LoadBySpecID(ctx context.Context, specID, userID string) (*StrategySpecRow, error) {
	var row StrategySpecRow
	err := r.db.QueryRowContext(ctx, `
		SELECT spec_id, user_id, name, author, revision, parent_revision, status,
		       description, scope_json, config_override, validation_verdict, created_at, updated_at
		FROM strategy_specs WHERE spec_id = ? AND user_id = ?
	`, specID, userID).Scan(
		&row.SpecID, &row.UserID, &row.Name, &row.Author, &row.Revision,
		&row.ParentRevision, &row.Status, &row.Description, &row.ScopeJSON,
		&row.ConfigOverride, &row.ValidationVerdict, &row.CreatedAt, &row.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// ListByStatus returns all Specs for a user filtered by status, ordered by updated_at DESC.
func (r *StrategySpecRepository) ListByStatus(ctx context.Context, userID, status string) ([]*StrategySpecRow, error) {
	query := `SELECT spec_id, user_id, name, author, revision, parent_revision, status,
	                  description, scope_json, config_override, validation_verdict, created_at, updated_at
	           FROM strategy_specs WHERE user_id = ?`
	args := []any{userID}
	if status != "" {
		query += " AND status = ?"
		args = append(args, status)
	}
	query += " ORDER BY updated_at DESC"

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list strategy_specs: %w", err)
	}
	defer rows.Close()

	var out []*StrategySpecRow
	for rows.Next() {
		var row StrategySpecRow
		if err := rows.Scan(
			&row.SpecID, &row.UserID, &row.Name, &row.Author, &row.Revision,
			&row.ParentRevision, &row.Status, &row.Description, &row.ScopeJSON,
			&row.ConfigOverride, &row.ValidationVerdict, &row.CreatedAt, &row.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, &row)
	}
	return out, rows.Err()
}

// Delete removes a draft or rejected Spec. Returns an error if the Spec is live or validated.
func (r *StrategySpecRepository) Delete(ctx context.Context, specID, userID string) error {
	res, err := r.db.ExecContext(ctx, `
		DELETE FROM strategy_specs WHERE spec_id = ? AND user_id = ? AND status IN ('draft', 'rejected')
	`, specID, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("spec %q not found or not in draft/rejected status", specID)
	}
	return nil
}

// StrategySpecRow is the raw storage representation of a persisted Spec.
// Callers unmarshal ScopeJSON, ConfigOverride, ValidationVerdict as needed.
type StrategySpecRow struct {
	SpecID            string
	UserID            string
	Name              string
	Author            string
	Revision          int
	ParentRevision    string
	Status            string
	Description       string
	ScopeJSON         string
	ConfigOverride    string
	ValidationVerdict string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}
