package storage

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/rantipay/trading-mcp/internal/ai/patterns"
	"github.com/sirupsen/logrus"
)

// PatternLibraryRepository persists learned pattern statistics to SQLite.
// It backs the in-memory patterns.Library so insights survive restarts.
type PatternLibraryRepository struct {
	db  *sql.DB
	log *logrus.Logger
}

// NewPatternLibraryRepository creates a new repository instance.
func NewPatternLibraryRepository(db *sql.DB, log *logrus.Logger) *PatternLibraryRepository {
	if log == nil {
		log = logrus.New()
	}
	return &PatternLibraryRepository{
		db:  db,
		log: log,
	}
}

// Upsert inserts or updates pattern stats by hash. Implements patterns.PersistenceBackend.
func (r *PatternLibraryRepository) Upsert(stats *patterns.PatternStats) error {
	if stats == nil || stats.Hash == "" {
		return fmt.Errorf("pattern stats nil or missing hash")
	}

	now := time.Now()
	lastSeen := stats.LastSeen
	if lastSeen.IsZero() {
		lastSeen = now
	}

	_, err := r.db.Exec(`
		INSERT INTO pattern_library (
			hash, pair, timeframe,
			sample_size, bullish_count, bearish_count, neutral_count,
			avg_price_change, success_rate, reliability,
			last_seen, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(hash) DO UPDATE SET
			pair             = excluded.pair,
			timeframe        = excluded.timeframe,
			sample_size      = excluded.sample_size,
			bullish_count    = excluded.bullish_count,
			bearish_count    = excluded.bearish_count,
			neutral_count    = excluded.neutral_count,
			avg_price_change = excluded.avg_price_change,
			success_rate     = excluded.success_rate,
			reliability      = excluded.reliability,
			last_seen        = excluded.last_seen,
			updated_at       = excluded.updated_at
	`,
		stats.Hash, stats.Pair, "",
		stats.SampleSize, stats.BullishCount, stats.BearishCount, stats.NeutralCount,
		stats.AvgPriceChange, stats.SuccessRate, stats.Reliability,
		lastSeen, now, now,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert pattern %s: %w", stats.Hash, err)
	}

	return nil
}

// GetByHash retrieves stats for a single pattern hash. Returns (nil, nil) if not found.
func (r *PatternLibraryRepository) GetByHash(hash string) (*patterns.PatternStats, error) {
	if hash == "" {
		return nil, fmt.Errorf("hash cannot be empty")
	}

	row := r.db.QueryRow(`
		SELECT hash, pair, sample_size, bullish_count, bearish_count, neutral_count,
		       avg_price_change, success_rate, reliability, last_seen
		FROM pattern_library
		WHERE hash = ?
	`, hash)

	stats, err := scanPatternStats(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get pattern %s: %w", hash, err)
	}

	return stats, nil
}

// GetTopByPair returns the top N reliable patterns for a pair, ordered by success_rate desc.
// Patterns with reliability == 'INSUFFICIENT' are excluded.
func (r *PatternLibraryRepository) GetTopByPair(pair string, n int) ([]*patterns.PatternStats, error) {
	if n <= 0 {
		n = 10
	}

	rows, err := r.db.Query(`
		SELECT hash, pair, sample_size, bullish_count, bearish_count, neutral_count,
		       avg_price_change, success_rate, reliability, last_seen
		FROM pattern_library
		WHERE pair = ? AND reliability != 'INSUFFICIENT'
		ORDER BY success_rate DESC, sample_size DESC
		LIMIT ?
	`, pair, n)
	if err != nil {
		return nil, fmt.Errorf("failed to query top patterns for %s: %w", pair, err)
	}
	defer rows.Close()

	return scanPatternStatsRows(rows)
}

// LoadAll loads every persisted pattern. Used on startup to seed the in-memory cache.
// Implements patterns.PersistenceBackend.
func (r *PatternLibraryRepository) LoadAll() ([]*patterns.PatternStats, error) {
	rows, err := r.db.Query(`
		SELECT hash, pair, sample_size, bullish_count, bearish_count, neutral_count,
		       avg_price_change, success_rate, reliability, last_seen
		FROM pattern_library
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to load patterns: %w", err)
	}
	defer rows.Close()

	return scanPatternStatsRows(rows)
}

// PurgeStale deletes patterns whose last_seen is older than the given duration.
// Returns the number of rows removed.
func (r *PatternLibraryRepository) PurgeStale(olderThan time.Duration) (int, error) {
	if olderThan <= 0 {
		return 0, nil
	}

	cutoff := time.Now().Add(-olderThan)

	res, err := r.db.Exec(`DELETE FROM pattern_library WHERE last_seen < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("failed to purge stale patterns: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to read rows affected: %w", err)
	}

	if affected > 0 {
		r.log.WithField("removed", affected).Info("Purged stale pattern library entries")
	}

	return int(affected), nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...interface{}) error
}

// scanPatternStats reads a single row into a PatternStats.
func scanPatternStats(s rowScanner) (*patterns.PatternStats, error) {
	stats := &patterns.PatternStats{}
	err := s.Scan(
		&stats.Hash,
		&stats.Pair,
		&stats.SampleSize,
		&stats.BullishCount,
		&stats.BearishCount,
		&stats.NeutralCount,
		&stats.AvgPriceChange,
		&stats.SuccessRate,
		&stats.Reliability,
		&stats.LastSeen,
	)
	if err != nil {
		return nil, err
	}
	return stats, nil
}

// scanPatternStatsRows iterates a rows result into a slice.
func scanPatternStatsRows(rows *sql.Rows) ([]*patterns.PatternStats, error) {
	var out []*patterns.PatternStats
	for rows.Next() {
		stats, err := scanPatternStats(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan pattern row: %w", err)
		}
		out = append(out, stats)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating pattern rows: %w", err)
	}
	return out, nil
}
