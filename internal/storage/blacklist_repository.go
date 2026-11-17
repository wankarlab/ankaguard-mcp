package storage

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
)

// BlacklistRepository persists the toxic-pair blacklist so auto-pause survives
// restarts. The in-memory map in the bot is the fast path; this is the durable
// backing store loaded on startup.
type BlacklistRepository struct {
	db  *sql.DB
	log *logrus.Logger
}

// NewBlacklistRepository constructs a BlacklistRepository.
func NewBlacklistRepository(db *sql.DB, log *logrus.Logger) *BlacklistRepository {
	return &BlacklistRepository{db: db, log: log}
}

// Save upserts a blacklist entry for (userID, pair) with its expiry.
func (r *BlacklistRepository) Save(userID, pair, reason string, until time.Time) error {
	if userID == "" {
		userID = "default"
	}
	_, err := r.db.Exec(`
		INSERT INTO pair_blacklist (user_id, pair, reason, until_ts, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(user_id, pair) DO UPDATE SET reason = excluded.reason, until_ts = excluded.until_ts
	`, userID, pair, reason, until, time.Now())
	if err != nil {
		return fmt.Errorf("failed to save blacklist entry: %w", err)
	}
	return nil
}

// Delete removes a blacklist entry (e.g. on manual clear).
func (r *BlacklistRepository) Delete(userID, pair string) error {
	if userID == "" {
		userID = "default"
	}
	_, err := r.db.Exec(`DELETE FROM pair_blacklist WHERE user_id = ? AND pair = ?`, userID, pair)
	if err != nil {
		return fmt.Errorf("failed to delete blacklist entry: %w", err)
	}
	return nil
}

// LoadActive returns all non-expired blacklist entries for userID as a
// pair→until map. Expired rows are purged as a side effect.
func (r *BlacklistRepository) LoadActive(userID string) (map[string]time.Time, error) {
	if userID == "" {
		userID = "default"
	}
	// Purge expired rows first to keep the table small.
	_, _ = r.db.Exec(`DELETE FROM pair_blacklist WHERE until_ts <= ?`, time.Now())

	rows, err := r.db.Query(`SELECT pair, until_ts FROM pair_blacklist WHERE user_id = ? AND until_ts > ?`, userID, time.Now())
	if err != nil {
		return nil, fmt.Errorf("failed to load blacklist: %w", err)
	}
	defer rows.Close()

	out := make(map[string]time.Time)
	for rows.Next() {
		var pair string
		var until time.Time
		if err := rows.Scan(&pair, &until); err != nil {
			return nil, fmt.Errorf("failed to scan blacklist row: %w", err)
		}
		out[pair] = until
	}
	return out, rows.Err()
}
