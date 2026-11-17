package storage

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// AdversarialItemRow is one spec queued for adversarial review (C3.2).
type AdversarialItemRow struct {
	ID        string `json:"id"`
	SpecID    string `json:"spec_id"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

// AppealRow is one tribunal appeal (C3.3).
type AppealRow struct {
	ID          string `json:"id"`
	SpecID      string `json:"spec_id"`
	AppellantID string `json:"appellant_id"`
	Status      string `json:"status"`
	Reason      string `json:"reason"`
	CreatedAt   string `json:"created_at"`
}

// GovernanceRepository backs the adversarial review queue (C3.2) and the
// tribunal appeals (C3.3) tables (migration 029).
type GovernanceRepository struct {
	db  *sql.DB
	log *logrus.Logger
}

// NewGovernanceRepository constructs a GovernanceRepository.
func NewGovernanceRepository(db *sql.DB, log *logrus.Logger) *GovernanceRepository {
	if log == nil {
		log = logrus.New()
	}
	return &GovernanceRepository{db: db, log: log}
}

// PendingItems returns adversarial items awaiting review. May be empty.
func (r *GovernanceRepository) PendingItems(ctx context.Context) ([]*AdversarialItemRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, spec_id, status, created_at
		  FROM adversarial_item
		 WHERE status = 'pending' AND deleted_at IS NULL
		 ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list adversarial_item: %w", err)
	}
	defer rows.Close()

	out := []*AdversarialItemRow{}
	for rows.Next() {
		it := &AdversarialItemRow{}
		if err := rows.Scan(&it.ID, &it.SpecID, &it.Status, &it.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan adversarial_item: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ItemExists reports whether an adversarial item with the given id exists and is
// not soft-deleted.
func (r *GovernanceRepository) ItemExists(ctx context.Context, itemID string) (bool, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM adversarial_item WHERE id = ? AND deleted_at IS NULL
	`, itemID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check adversarial_item %q: %w", itemID, err)
	}
	return count > 0, nil
}

// AddFlag records a reviewer flag against an adversarial item.
func (r *GovernanceRepository) AddFlag(ctx context.Context, itemID, reviewerID, tenantID, reason string) (string, error) {
	id := uuid.NewString()
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO adversarial_flag (id, item_id, reviewer_id, tenant_id, reason)
		VALUES (?, ?, ?, ?, ?)
	`, id, itemID, reviewerID, nullString(tenantID), reason)
	if err != nil {
		return "", fmt.Errorf("add adversarial_flag for %q: %w", itemID, err)
	}
	return id, nil
}

// ListAppeals returns appeals, newest first. May be empty.
func (r *GovernanceRepository) ListAppeals(ctx context.Context) ([]*AppealRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, spec_id, appellant_id, status, reason, created_at
		  FROM appeal
		 WHERE deleted_at IS NULL
		 ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list appeal: %w", err)
	}
	defer rows.Close()

	out := []*AppealRow{}
	for rows.Next() {
		a := &AppealRow{}
		if err := rows.Scan(&a.ID, &a.SpecID, &a.AppellantID, &a.Status, &a.Reason, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan appeal: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CreateAppeal records a new tribunal appeal and returns its id.
func (r *GovernanceRepository) CreateAppeal(ctx context.Context, specID, appellantID, tenantID, reason string) (string, error) {
	id := uuid.NewString()
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO appeal (id, spec_id, appellant_id, tenant_id, reason, status)
		VALUES (?, ?, ?, ?, ?, 'open')
	`, id, specID, appellantID, nullString(tenantID), reason)
	if err != nil {
		return "", fmt.Errorf("create appeal for spec %q: %w", specID, err)
	}
	return id, nil
}
