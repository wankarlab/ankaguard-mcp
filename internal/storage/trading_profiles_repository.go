package storage

import (
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
)

// TradingProfile is a row in the trading_profiles table.
//
// The schema (migration 002 + 005) defines:
//
//	id          TEXT PRIMARY KEY
//	name        TEXT NOT NULL UNIQUE
//	description TEXT
//	config      TEXT NOT NULL  -- JSON serialised config blob
//	created_at  DATETIME
//	updated_at  DATETIME
//	is_default  BOOLEAN
//	tags        TEXT           -- JSON array
//	created_by  TEXT NOT NULL DEFAULT 'user'
type TradingProfile struct {
	ID          string
	Name        string
	Description string
	Config      string // JSON serialised
	IsDefault   bool
	Tags        string // JSON array (or empty string)
	CreatedBy   string // e.g. "master_agent" or "user"
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// TradingProfilesRepository persists trading profiles to SQLite.
type TradingProfilesRepository struct {
	db  *sql.DB
	log *logrus.Logger
}

// NewTradingProfilesRepository creates a new repository instance.
func NewTradingProfilesRepository(db *sql.DB, log *logrus.Logger) *TradingProfilesRepository {
	if log == nil {
		log = logrus.New()
	}
	return &TradingProfilesRepository{
		db:  db,
		log: log,
	}
}

// Upsert inserts or updates a profile by name. Idempotent — calling it twice
// with the same name updates the prior record. Auto-fills ID, CreatedAt,
// UpdatedAt and Tags when omitted.
func (r *TradingProfilesRepository) Upsert(profile *TradingProfile) error {
	if profile == nil {
		return fmt.Errorf("profile is nil")
	}
	if profile.Name == "" {
		return fmt.Errorf("profile name is required")
	}
	if profile.Config == "" {
		return fmt.Errorf("profile config is required")
	}

	now := time.Now()
	if profile.CreatedAt.IsZero() {
		profile.CreatedAt = now
	}
	if profile.UpdatedAt.IsZero() {
		profile.UpdatedAt = now
	}
	if profile.ID == "" {
		profile.ID = deterministicID(profile.Name)
	}
	if profile.CreatedBy == "" {
		profile.CreatedBy = "user"
	}
	if profile.Tags == "" {
		profile.Tags = "[]"
	}

	_, err := r.db.Exec(`
		INSERT INTO trading_profiles (
			id, name, description, config,
			created_at, updated_at, is_default, tags, created_by
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			description = excluded.description,
			config      = excluded.config,
			updated_at  = excluded.updated_at,
			is_default  = excluded.is_default,
			tags        = excluded.tags,
			created_by  = excluded.created_by
	`,
		profile.ID,
		profile.Name,
		profile.Description,
		profile.Config,
		profile.CreatedAt,
		profile.UpdatedAt,
		profile.IsDefault,
		profile.Tags,
		profile.CreatedBy,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert profile %s: %w", profile.Name, err)
	}

	return nil
}

// GetByName returns the profile by name or (nil, nil) if not found.
func (r *TradingProfilesRepository) GetByName(name string) (*TradingProfile, error) {
	if name == "" {
		return nil, fmt.Errorf("name cannot be empty")
	}

	row := r.db.QueryRow(`
		SELECT id, name, description, config,
		       created_at, updated_at, is_default, tags, created_by
		FROM trading_profiles
		WHERE name = ?
	`, name)

	profile, err := scanTradingProfile(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get profile %s: %w", name, err)
	}

	return profile, nil
}

// ListByCreatedBy returns profiles created by the given author, newest first.
// A non-positive limit defaults to 50.
func (r *TradingProfilesRepository) ListByCreatedBy(createdBy string, limit int) ([]*TradingProfile, error) {
	if createdBy == "" {
		return nil, fmt.Errorf("createdBy cannot be empty")
	}
	if limit <= 0 {
		limit = 50
	}

	rows, err := r.db.Query(`
		SELECT id, name, description, config,
		       created_at, updated_at, is_default, tags, created_by
		FROM trading_profiles
		WHERE created_by = ?
		ORDER BY updated_at DESC
		LIMIT ?
	`, createdBy, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list profiles by created_by=%s: %w", createdBy, err)
	}
	defer rows.Close()

	var out []*TradingProfile
	for rows.Next() {
		profile, err := scanTradingProfile(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan profile row: %w", err)
		}
		out = append(out, profile)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating profile rows: %w", err)
	}

	return out, nil
}

// DeleteByName removes a profile by name. Returns nil if no row matched.
func (r *TradingProfilesRepository) DeleteByName(name string) error {
	if name == "" {
		return fmt.Errorf("name cannot be empty")
	}

	res, err := r.db.Exec(`DELETE FROM trading_profiles WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("failed to delete profile %s: %w", name, err)
	}

	if affected, _ := res.RowsAffected(); affected > 0 {
		r.log.WithField("name", name).Debug("Deleted trading profile")
	}

	return nil
}

// scanTradingProfile reads a single row into a TradingProfile.
func scanTradingProfile(s rowScanner) (*TradingProfile, error) {
	profile := &TradingProfile{}
	var description sql.NullString
	var tags sql.NullString
	err := s.Scan(
		&profile.ID,
		&profile.Name,
		&description,
		&profile.Config,
		&profile.CreatedAt,
		&profile.UpdatedAt,
		&profile.IsDefault,
		&tags,
		&profile.CreatedBy,
	)
	if err != nil {
		return nil, err
	}
	if description.Valid {
		profile.Description = description.String
	}
	if tags.Valid {
		profile.Tags = tags.String
	}
	return profile, nil
}

// deterministicID derives a stable ID from a profile name so repeated upserts
// without a pre-set ID converge on the same row.
func deterministicID(name string) string {
	sum := sha1.Sum([]byte(name))
	return hex.EncodeToString(sum[:])
}
