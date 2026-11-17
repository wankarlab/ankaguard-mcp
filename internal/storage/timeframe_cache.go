package storage

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/rantipay/trading-mcp/internal/ai"
	"github.com/sirupsen/logrus"
)

// TimeframeDataCache handles caching of market data by timeframe
type TimeframeDataCache struct {
	db  *sql.DB
	log *logrus.Logger
}

// NewTimeframeDataCache creates a new timeframe data cache
func NewTimeframeDataCache(db *sql.DB, log *logrus.Logger) *TimeframeDataCache {
	return &TimeframeDataCache{
		db:  db,
		log: log,
	}
}

// SaveCandles saves candles to the cache
func (c *TimeframeDataCache) SaveCandles(pair, timeframe string, candles []ai.Candle) error {
	if len(candles) == 0 {
		return nil
	}

	// Calculate TTL based on timeframe
	ttl := c.getTTL(timeframe)
	now := time.Now()
	expiresAt := now.Add(ttl)

	// Begin transaction
	tx, err := c.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO timeframe_data_cache 
		(pair, timeframe, timestamp, open, high, low, close, volume, cached_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, candle := range candles {
		_, err = stmt.Exec(
			pair, timeframe, candle.Timestamp,
			candle.Open, candle.High, candle.Low, candle.Close, candle.Volume,
			now, expiresAt,
		)
		if err != nil {
			return fmt.Errorf("failed to insert candle: %w", err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	c.log.WithFields(logrus.Fields{
		"pair":      pair,
		"timeframe": timeframe,
		"count":     len(candles),
		"ttl":       ttl,
	}).Debug("Candles cached")

	return nil
}

// LoadCandles loads candles from the cache
func (c *TimeframeDataCache) LoadCandles(pair, timeframe string, limit int) ([]ai.Candle, error) {
	now := time.Now()

	rows, err := c.db.Query(`
		SELECT timestamp, open, high, low, close, volume
		FROM timeframe_data_cache
		WHERE pair = ? AND timeframe = ? AND expires_at > ?
		ORDER BY timestamp DESC
		LIMIT ?
	`, pair, timeframe, now, limit)

	if err != nil {
		return nil, fmt.Errorf("failed to query candles: %w", err)
	}
	defer rows.Close()

	var candles []ai.Candle

	for rows.Next() {
		var candle ai.Candle
		err := rows.Scan(
			&candle.Timestamp,
			&candle.Open,
			&candle.High,
			&candle.Low,
			&candle.Close,
			&candle.Volume,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan candle: %w", err)
		}

		candles = append(candles, candle)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	// Reverse to get chronological order
	for i, j := 0, len(candles)-1; i < j; i, j = i+1, j-1 {
		candles[i], candles[j] = candles[j], candles[i]
	}

	c.log.WithFields(logrus.Fields{
		"pair":      pair,
		"timeframe": timeframe,
		"requested": limit,
		"found":     len(candles),
	}).Debug("Candles loaded from cache")

	return candles, nil
}

// CleanExpired removes expired cache entries
func (c *TimeframeDataCache) CleanExpired() error {
	result, err := c.db.Exec(`
		DELETE FROM timeframe_data_cache
		WHERE expires_at < ?
	`, time.Now())

	if err != nil {
		return fmt.Errorf("failed to clean expired cache: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows > 0 {
		c.log.WithField("rows", rows).Info("Expired cache entries cleaned")
	}

	return nil
}

// getTTL returns the cache TTL for a given timeframe
func (c *TimeframeDataCache) getTTL(timeframe string) time.Duration {
	switch timeframe {
	case "1m":
		return 30 * time.Second
	case "5m":
		return 2 * time.Minute
	case "15m":
		return 5 * time.Minute
	case "1h":
		return 15 * time.Minute
	case "4h":
		return 1 * time.Hour
	case "1d":
		return 4 * time.Hour
	case "1w":
		return 24 * time.Hour
	default:
		return 5 * time.Minute
	}
}

// GetCacheStats returns cache statistics
func (c *TimeframeDataCache) GetCacheStats() (map[string]interface{}, error) {
	var totalEntries, expiredEntries int64

	err := c.db.QueryRow(`
		SELECT COUNT(*) FROM timeframe_data_cache
	`).Scan(&totalEntries)
	if err != nil {
		return nil, fmt.Errorf("failed to count total entries: %w", err)
	}

	err = c.db.QueryRow(`
		SELECT COUNT(*) FROM timeframe_data_cache WHERE expires_at < ?
	`, time.Now()).Scan(&expiredEntries)
	if err != nil {
		return nil, fmt.Errorf("failed to count expired entries: %w", err)
	}

	// Get entries by timeframe
	rows, err := c.db.Query(`
		SELECT timeframe, COUNT(*) as count
		FROM timeframe_data_cache
		WHERE expires_at > ?
		GROUP BY timeframe
	`, time.Now())
	if err != nil {
		return nil, fmt.Errorf("failed to query by timeframe: %w", err)
	}
	defer rows.Close()

	byTimeframe := make(map[string]int64)
	for rows.Next() {
		var tf string
		var count int64
		if err := rows.Scan(&tf, &count); err != nil {
			return nil, fmt.Errorf("failed to scan timeframe stats: %w", err)
		}
		byTimeframe[tf] = count
	}

	return map[string]interface{}{
		"total_entries":   totalEntries,
		"expired_entries": expiredEntries,
		"active_entries":  totalEntries - expiredEntries,
		"by_timeframe":    byTimeframe,
	}, nil
}
