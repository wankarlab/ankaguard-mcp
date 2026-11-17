package storage

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/rantipay/trading-mcp/internal/ai"
	"github.com/sirupsen/logrus"
)

// CandleArchiveRepository provides durable, append-only storage of historical
// candles for backtesting and strategy validation. Distinct from
// TimeframeDataCache (TTL-transient, live analysis), this archive never expires.
type CandleArchiveRepository struct {
	db  *sql.DB
	log *logrus.Logger
}

// NewCandleArchiveRepository constructs a CandleArchiveRepository.
func NewCandleArchiveRepository(db *sql.DB, log *logrus.Logger) *CandleArchiveRepository {
	return &CandleArchiveRepository{db: db, log: log}
}

// Save upserts a batch of candles. Idempotent: re-saving the same (pair,
// timeframe, timestamp) overwrites the OHLCV, so backfills can overlap safely.
func (r *CandleArchiveRepository) Save(pair, timeframe string, candles []ai.Candle) (int, error) {
	if len(candles) == 0 {
		return 0, nil
	}
	tx, err := r.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO candle_archive
		(pair, timeframe, timestamp, open, high, low, close, volume)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return 0, fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	n := 0
	for _, c := range candles {
		if c.Timestamp.IsZero() {
			continue
		}
		if _, err := stmt.Exec(
			pair, timeframe, c.Timestamp.Unix(),
			c.Open, c.High, c.Low, c.Close, c.Volume,
		); err != nil {
			return n, fmt.Errorf("insert candle: %w", err)
		}
		n++
	}
	if err := tx.Commit(); err != nil {
		return n, fmt.Errorf("commit: %w", err)
	}
	return n, nil
}

// LoadRange returns candles for [from, to] (inclusive) ordered by time ascending.
func (r *CandleArchiveRepository) LoadRange(pair, timeframe string, from, to time.Time) ([]ai.Candle, error) {
	rows, err := r.db.Query(`
		SELECT timestamp, open, high, low, close, volume
		FROM candle_archive
		WHERE pair = ? AND timeframe = ? AND timestamp >= ? AND timestamp <= ?
		ORDER BY timestamp ASC
	`, pair, timeframe, from.Unix(), to.Unix())
	if err != nil {
		return nil, fmt.Errorf("query range: %w", err)
	}
	defer rows.Close()

	var out []ai.Candle
	for rows.Next() {
		var ts int64
		var c ai.Candle
		if err := rows.Scan(&ts, &c.Open, &c.High, &c.Low, &c.Close, &c.Volume); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		c.Timestamp = time.Unix(ts, 0)
		out = append(out, c)
	}
	return out, rows.Err()
}

// Coverage reports how many candles are stored and the oldest/newest timestamps
// for a pair+timeframe. count is 0 when nothing is stored.
func (r *CandleArchiveRepository) Coverage(pair, timeframe string) (count int, oldest, newest time.Time, err error) {
	var oldestTS, newestTS sql.NullInt64
	row := r.db.QueryRow(`
		SELECT COUNT(*), MIN(timestamp), MAX(timestamp)
		FROM candle_archive WHERE pair = ? AND timeframe = ?
	`, pair, timeframe)
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
