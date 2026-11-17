package storage

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
)

// FundingRate is a single settled funding-rate observation for a perpetual
// futures pair. Rate is the decimal per-period rate (e.g. 0.0001 = 0.01%).
type FundingRate struct {
	Timestamp time.Time
	Rate      float64
}

// FundingArchiveRepository provides durable, append-only storage of historical
// funding rates for backtesting funding-carry strategies. Mirrors
// CandleArchiveRepository: idempotent upserts, never expires.
type FundingArchiveRepository struct {
	db  *sql.DB
	log *logrus.Logger
}

// NewFundingArchiveRepository constructs a FundingArchiveRepository.
func NewFundingArchiveRepository(db *sql.DB, log *logrus.Logger) *FundingArchiveRepository {
	return &FundingArchiveRepository{db: db, log: log}
}

// EnsureSchema creates the funding_archive table and index if they do not
// exist. Safe to call on every startup (idempotent DDL).
func (r *FundingArchiveRepository) EnsureSchema() error {
	if _, err := r.db.Exec(`
		CREATE TABLE IF NOT EXISTS funding_archive (
			pair TEXT NOT NULL,
			ts   INTEGER NOT NULL, -- unix seconds of the funding settlement
			rate REAL NOT NULL,    -- decimal per-period rate (0.0001 = 0.01%)
			PRIMARY KEY (pair, ts)
		)
	`); err != nil {
		return fmt.Errorf("create funding_archive: %w", err)
	}
	if _, err := r.db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_funding_archive_range
			ON funding_archive (pair, ts)
	`); err != nil {
		return fmt.Errorf("create funding_archive index: %w", err)
	}
	return nil
}

// Save upserts a batch of funding rates. Idempotent: re-saving the same
// (pair, ts) overwrites the rate, so backfills can overlap safely.
func (r *FundingArchiveRepository) Save(pair string, rates []FundingRate) (int, error) {
	if len(rates) == 0 {
		return 0, nil
	}
	tx, err := r.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO funding_archive (pair, ts, rate)
		VALUES (?, ?, ?)
	`)
	if err != nil {
		return 0, fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	n := 0
	for _, fr := range rates {
		if fr.Timestamp.IsZero() {
			continue
		}
		if _, err := stmt.Exec(pair, fr.Timestamp.Unix(), fr.Rate); err != nil {
			return n, fmt.Errorf("insert funding rate: %w", err)
		}
		n++
	}
	if err := tx.Commit(); err != nil {
		return n, fmt.Errorf("commit: %w", err)
	}
	return n, nil
}

// LoadRange returns funding rates for [from, to] (inclusive) ordered by time
// ascending.
func (r *FundingArchiveRepository) LoadRange(pair string, from, to time.Time) ([]FundingRate, error) {
	rows, err := r.db.Query(`
		SELECT ts, rate
		FROM funding_archive
		WHERE pair = ? AND ts >= ? AND ts <= ?
		ORDER BY ts ASC
	`, pair, from.Unix(), to.Unix())
	if err != nil {
		return nil, fmt.Errorf("query range: %w", err)
	}
	defer rows.Close()

	var out []FundingRate
	for rows.Next() {
		var ts int64
		var fr FundingRate
		if err := rows.Scan(&ts, &fr.Rate); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		fr.Timestamp = time.Unix(ts, 0)
		out = append(out, fr)
	}
	return out, rows.Err()
}

// Coverage reports how many funding periods are stored and the oldest/newest
// timestamps for a pair. count is 0 when nothing is stored.
func (r *FundingArchiveRepository) Coverage(pair string) (count int, oldest, newest time.Time, err error) {
	var oldestTS, newestTS sql.NullInt64
	row := r.db.QueryRow(`
		SELECT COUNT(*), MIN(ts), MAX(ts)
		FROM funding_archive WHERE pair = ?
	`, pair)
	if err = row.Scan(&count, &oldestTS, &newestTS); err != nil {
		return 0, time.Time{}, time.Time{}, fmt.Errorf("scan coverage: %w", err)
	}
	if oldestTS.Valid {
		oldest = time.Unix(oldestTS.Int64, 0)
	}
	if newestTS.Valid {
		newest = time.Unix(newestTS.Int64, 0)
	}
	return count, oldest, newest, nil
}

// LatestRate returns the most recent stored funding rate for a pair, or an
// error when none is stored.
func (r *FundingArchiveRepository) LatestRate(pair string) (FundingRate, error) {
	var ts int64
	var fr FundingRate
	row := r.db.QueryRow(`
		SELECT ts, rate FROM funding_archive
		WHERE pair = ? ORDER BY ts DESC LIMIT 1
	`, pair)
	if err := row.Scan(&ts, &fr.Rate); err != nil {
		if err == sql.ErrNoRows {
			return FundingRate{}, fmt.Errorf("no funding rates stored for %s", pair)
		}
		return FundingRate{}, fmt.Errorf("scan latest rate: %w", err)
	}
	fr.Timestamp = time.Unix(ts, 0)
	return fr, nil
}
