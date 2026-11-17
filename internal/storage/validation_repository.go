package storage

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
)

// ValidationRecord is the persisted summary of a strategy validation verdict.
type ValidationRecord struct {
	Pair           string
	Exchange       string
	StrategyHash   string // EV-5: populated with ContentHash() spec_id, not 'live'
	Pass           bool
	Reason         string
	ProfitFactor   float64
	Consistency    float64
	NTrades        int
	WorstDrawdown  float64
	ValidatedAt    time.Time
	ExpiresAt      time.Time
	// EV-9: extended fields for robustness ranking
	SpecID         string  // logical FK to strategy_specs.spec_id
	CostModelID    string  // which cost model was used
	Sharpe         float64 // already computed in AggregateMetric; now persisted
	RobustnessScore float64 // [0..100] ranking score (shadow by default)
}

// ValidationRepository persists strategy validation verdicts that gate live
// trading. Distinct from backtest runs: this is the certification of record.
type ValidationRepository struct {
	db  *sql.DB
	log *logrus.Logger
}

// NewValidationRepository constructs a ValidationRepository.
func NewValidationRepository(db *sql.DB, log *logrus.Logger) *ValidationRepository {
	return &ValidationRepository{db: db, log: log}
}

// Save upserts a verdict with the given TTL.
func (r *ValidationRepository) Save(rec ValidationRecord, ttl time.Duration) error {
	if rec.Exchange == "" {
		rec.Exchange = "kucoin"
	}
	if rec.StrategyHash == "" {
		// EV-5: default is 'live' for legacy callers; new callers must set
		// StrategyHash = spec.ContentHash() so distinct Specs produce distinct rows.
		rec.StrategyHash = "live"
	}
	pass := 0
	if rec.Pass {
		pass = 1
	}
	now := time.Now()
	_, err := r.db.Exec(`
		INSERT INTO strategy_validation
		(pair, exchange, strategy_hash, pass, reason, profit_factor, consistency, n_trades, worst_drawdown, validated_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(pair, exchange, strategy_hash) DO UPDATE SET
			pass = excluded.pass, reason = excluded.reason, profit_factor = excluded.profit_factor,
			consistency = excluded.consistency, n_trades = excluded.n_trades,
			worst_drawdown = excluded.worst_drawdown, validated_at = excluded.validated_at,
			expires_at = excluded.expires_at
	`, rec.Pair, rec.Exchange, rec.StrategyHash, pass, rec.Reason, rec.ProfitFactor,
		rec.Consistency, rec.NTrades, rec.WorstDrawdown, now, now.Add(ttl))
	if err != nil {
		return fmt.Errorf("save validation: %w", err)
	}
	return nil
}

// IsCertified reports whether (pair, exchange) holds a non-expired PASS for the
// 'live' strategy. This is the gate the live bot consults before opening a trade.
func (r *ValidationRepository) IsCertified(pair, exchange string) bool {
	if exchange == "" {
		exchange = "kucoin"
	}
	var pass int
	var expires time.Time
	err := r.db.QueryRow(`
		SELECT pass, expires_at FROM strategy_validation
		WHERE pair = ? AND exchange = ? AND strategy_hash = 'live'
	`, pair, exchange).Scan(&pass, &expires)
	if err != nil {
		return false // no record = not certified
	}
	return pass == 1 && time.Now().Before(expires)
}

// Get returns the stored verdict for a pair (or false if none).
func (r *ValidationRepository) Get(pair, exchange string) (ValidationRecord, bool) {
	if exchange == "" {
		exchange = "kucoin"
	}
	var rec ValidationRecord
	var pass int
	err := r.db.QueryRow(`
		SELECT pair, exchange, strategy_hash, pass, reason, profit_factor, consistency,
		       n_trades, worst_drawdown, validated_at, expires_at
		FROM strategy_validation WHERE pair = ? AND exchange = ? AND strategy_hash = 'live'
	`, pair, exchange).Scan(&rec.Pair, &rec.Exchange, &rec.StrategyHash, &pass, &rec.Reason,
		&rec.ProfitFactor, &rec.Consistency, &rec.NTrades, &rec.WorstDrawdown, &rec.ValidatedAt, &rec.ExpiresAt)
	if err != nil {
		return ValidationRecord{}, false
	}
	rec.Pass = pass == 1
	return rec, true
}

// SpecKey encodes the EV-8 composite cache key for a Spec verdict:
// (spec_id, pair, exchange, cost_model_id, oos_window_days) → bucketized to day.
// Stored as strategy_hash in the existing table (no schema change needed).
func SpecKey(specID, pair, exchange, costModelID string, oosWindowDays int) string {
	if costModelID == "" {
		costModelID = "kucoin_futures_default"
	}
	return fmt.Sprintf("spec:%s:%s:%s:%s:%dd", specID, pair, exchange, costModelID, oosWindowDays)
}

// IsCertifiedBySpec checks certification for a SPECIFIC Spec (EV-8 re-key).
// Returns true if a non-expired PASS exists for the full SpecKey tuple.
// A second tenant with an identical tuple reuses the cached verdict.
func (r *ValidationRepository) IsCertifiedBySpec(specID, pair, exchange, costModelID string, oosWindowDays int) bool {
	key := SpecKey(specID, pair, exchange, costModelID, oosWindowDays)
	var pass int
	var expires time.Time
	err := r.db.QueryRow(`
		SELECT pass, expires_at FROM strategy_validation
		WHERE pair = ? AND exchange = ? AND strategy_hash = ?
	`, pair, exchange, key).Scan(&pass, &expires)
	if err != nil {
		return false
	}
	return pass == 1 && time.Now().Before(expires)
}

// All returns every stored verdict (for the status endpoint).
func (r *ValidationRepository) All() ([]ValidationRecord, error) {
	rows, err := r.db.Query(`
		SELECT pair, exchange, strategy_hash, pass, reason, profit_factor, consistency,
		       n_trades, worst_drawdown, validated_at, expires_at
		FROM strategy_validation ORDER BY validated_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ValidationRecord
	for rows.Next() {
		var rec ValidationRecord
		var pass int
		if err := rows.Scan(&rec.Pair, &rec.Exchange, &rec.StrategyHash, &pass, &rec.Reason,
			&rec.ProfitFactor, &rec.Consistency, &rec.NTrades, &rec.WorstDrawdown, &rec.ValidatedAt, &rec.ExpiresAt); err != nil {
			return nil, err
		}
		rec.Pass = pass == 1
		out = append(out, rec)
	}
	return out, rows.Err()
}
