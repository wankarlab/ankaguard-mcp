package storage

import (
	"database/sql"
	"fmt"
	"sync"
	"time"
)

// TuningParam holds a tuning parameter with its metadata.
type TuningParam struct {
	Key         string
	Value       float64
	Description string
	UpdatedAt   time.Time
	UpdatedBy   string
}

// tuningDefault defines a parameter's default value and description.
type tuningDefault struct {
	value       float64
	description string
}

// defaultTuningParams lists all parameters seeded on first run.
var defaultTuningParams = []struct {
	key  string
	tuningDefault
}{
	{"regime.ranging.size_multiplier", tuningDefault{0.85, "Size multiplier in RANGING regime"}},
	{"regime.breakout.size_multiplier", tuningDefault{0.70, "Size multiplier in BREAKOUT regime"}},
	{"regime.chaos.size_multiplier", tuningDefault{0.00, "Size multiplier in CHAOS regime (pause)"}},
	{"regime.trending.size_multiplier", tuningDefault{1.00, "Size multiplier in TRENDING regime"}},
	{"regime.unknown.size_multiplier", tuningDefault{1.00, "Size multiplier when regime is unknown"}},
	{"btc.counter_trend.penalty", tuningDefault{0.85, "Confidence penalty when trading against BTC direction"}},
	{"btc.aligned.boost", tuningDefault{1.10, "Confidence boost when aligned with BTC direction"}},
	{"btc.strong_momentum_threshold", tuningDefault{0.30, "Momentum % to consider BTC \"moving strongly\""}},
	{"llm.consensus_boost", tuningDefault{1.15, "Boost when LLM and technical agree"}},
	{"llm.wait_penalty", tuningDefault{0.90, "Penalty when LLM says WAIT but technical says trade"}},
	{"llm.disagreement_penalty", tuningDefault{0.85, "Penalty when LLM and technical disagree"}},
	{"llm.circuit_breaker.max_failures", tuningDefault{10, "Max failures before circuit breaker opens"}},
	{"llm.circuit_breaker.reset_seconds", tuningDefault{30, "Seconds before circuit breaker attempts recovery"}},
	{"confidence.threshold", tuningDefault{0.65, "Minimum confidence to execute a trade"}},
}

// TuningStore reads and writes bot tuning parameters backed by SQLite,
// with an in-memory cache for low-latency hot reads.
type TuningStore struct {
	db    *sql.DB
	mu    sync.RWMutex
	cache map[string]float64
}

// NewTuningStore initialises the store: creates the table, seeds defaults,
// and loads the cache from the database.
func NewTuningStore(db *sql.DB) (*TuningStore, error) {
	if db == nil {
		return nil, fmt.Errorf("tuning store: db must not be nil")
	}

	s := &TuningStore{
		db:    db,
		cache: make(map[string]float64),
	}

	if err := s.createTable(); err != nil {
		return nil, err
	}

	if err := s.seedDefaults(); err != nil {
		return nil, err
	}

	if err := s.loadCache(); err != nil {
		return nil, err
	}

	return s, nil
}

// Get returns the cached value for key, or fallback when the key is absent.
func (s *TuningStore) Get(key string, fallback float64) float64 {
	s.mu.RLock()
	v, ok := s.cache[key]
	s.mu.RUnlock()
	if !ok {
		return fallback
	}
	return v
}

// Set persists a parameter to the database and refreshes the in-memory cache.
func (s *TuningStore) Set(key, description, updatedBy string, value float64) error {
	if key == "" {
		return fmt.Errorf("tuning store: key must not be empty")
	}
	if updatedBy == "" {
		updatedBy = "system"
	}

	_, err := s.db.Exec(`
		INSERT INTO bot_tuning_params (key, value, description, updated_at, updated_by)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP, ?)
		ON CONFLICT(key) DO UPDATE SET
			value       = excluded.value,
			description = excluded.description,
			updated_at  = excluded.updated_at,
			updated_by  = excluded.updated_by`,
		key, value, description, updatedBy,
	)
	if err != nil {
		return fmt.Errorf("tuning store: set %q: %w", key, err)
	}

	s.mu.Lock()
	s.cache[key] = value
	s.mu.Unlock()

	return nil
}

// GetAll returns every parameter with its full metadata queried directly from
// the database to guarantee up-to-date descriptions and timestamps.
func (s *TuningStore) GetAll() map[string]TuningParam {
	rows, err := s.db.Query(`
		SELECT key, value, description, updated_at, updated_by
		FROM bot_tuning_params
		ORDER BY key`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	result := make(map[string]TuningParam)
	for rows.Next() {
		var p TuningParam
		var updatedAt string
		if err := rows.Scan(&p.Key, &p.Value, &p.Description, &updatedAt, &p.UpdatedBy); err != nil {
			continue
		}
		// SQLite returns DATETIME as a string; parse the two common formats.
		if t, err := time.Parse("2006-01-02 15:04:05", updatedAt); err == nil {
			p.UpdatedAt = t
		} else if t, err := time.Parse(time.RFC3339, updatedAt); err == nil {
			p.UpdatedAt = t
		}
		result[p.Key] = p
	}
	return result
}

// --- private helpers ---------------------------------------------------------

func (s *TuningStore) createTable() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS bot_tuning_params (
			key         TEXT PRIMARY KEY,
			value       REAL NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_by  TEXT NOT NULL DEFAULT 'system'
		)`)
	if err != nil {
		return fmt.Errorf("tuning store: create table: %w", err)
	}
	return nil
}

func (s *TuningStore) seedDefaults() error {
	stmt, err := s.db.Prepare(`
		INSERT OR IGNORE INTO bot_tuning_params (key, value, description)
		VALUES (?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("tuning store: prepare seed statement: %w", err)
	}
	defer stmt.Close()

	for _, d := range defaultTuningParams {
		if _, err := stmt.Exec(d.key, d.value, d.description); err != nil {
			return fmt.Errorf("tuning store: seed %q: %w", d.key, err)
		}
	}
	return nil
}

func (s *TuningStore) loadCache() error {
	rows, err := s.db.Query(`SELECT key, value FROM bot_tuning_params`)
	if err != nil {
		return fmt.Errorf("tuning store: load cache: %w", err)
	}
	defer rows.Close()

	s.mu.Lock()
	defer s.mu.Unlock()

	for rows.Next() {
		var key string
		var value float64
		if err := rows.Scan(&key, &value); err != nil {
			return fmt.Errorf("tuning store: scan row: %w", err)
		}
		s.cache[key] = value
	}
	return rows.Err()
}
