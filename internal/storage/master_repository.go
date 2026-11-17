package storage

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/master"
)

// Compile-time assertion: MasterRepository implements master.MasterPersistence.
var _ master.MasterPersistence = (*MasterRepository)(nil)

// MasterRepository persists MasterAgent state, scoring weights and an
// opportunity history so the agent can survive restarts without losing
// learned scoring adjustments.
type MasterRepository struct {
	db  *sql.DB
	log *logrus.Logger
}

// NewMasterRepository creates a new repository instance.
func NewMasterRepository(db *sql.DB, log *logrus.Logger) *MasterRepository {
	if log == nil {
		log = logrus.New()
	}
	return &MasterRepository{
		db:  db,
		log: log,
	}
}

// SaveScoringWeights upserts the single-row scoring weights record.
func (r *MasterRepository) SaveScoringWeights(w *master.ScoringWeights) error {
	if w == nil {
		return fmt.Errorf("scoring weights cannot be nil")
	}

	_, err := r.db.Exec(`
		INSERT INTO master_scoring_weights (
			id, volatility, volume, trend_strength, sentiment,
			regime_match, btc_correlation_penalty, recent_loss_penalty,
			profit_factor, updated_at
		) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			volatility               = excluded.volatility,
			volume                   = excluded.volume,
			trend_strength           = excluded.trend_strength,
			sentiment                = excluded.sentiment,
			regime_match             = excluded.regime_match,
			btc_correlation_penalty  = excluded.btc_correlation_penalty,
			recent_loss_penalty      = excluded.recent_loss_penalty,
			profit_factor            = excluded.profit_factor,
			updated_at               = excluded.updated_at
	`,
		w.Volatility, w.Volume, w.TrendStrength, w.Sentiment,
		w.RegimeMatch, w.BTCCorrelationPenalty, w.RecentLossPenalty,
		w.ProfitFactor, time.Now(),
	)
	if err != nil {
		return fmt.Errorf("failed to save scoring weights: %w", err)
	}
	return nil
}

// LoadScoringWeights returns the persisted weights, or (nil, nil) when none
// have ever been written. Callers should fall back to defaults in that case.
func (r *MasterRepository) LoadScoringWeights() (*master.ScoringWeights, error) {
	row := r.db.QueryRow(`
		SELECT volatility, volume, trend_strength, sentiment,
		       regime_match, btc_correlation_penalty, recent_loss_penalty,
		       profit_factor
		FROM master_scoring_weights
		WHERE id = 1
	`)

	w := &master.ScoringWeights{}
	err := row.Scan(
		&w.Volatility, &w.Volume, &w.TrendStrength, &w.Sentiment,
		&w.RegimeMatch, &w.BTCCorrelationPenalty, &w.RecentLossPenalty,
		&w.ProfitFactor,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load scoring weights: %w", err)
	}
	return w, nil
}

// SaveOpportunity persists a single opportunity associated with a scan id.
func (r *MasterRepository) SaveOpportunity(opp *master.MarketOpportunity, scanID string) error {
	if opp == nil {
		return fmt.Errorf("opportunity cannot be nil")
	}

	detectedAt := opp.DetectedAt
	if detectedAt.IsZero() {
		detectedAt = time.Now()
	}

	_, err := r.db.Exec(`
		INSERT INTO master_opportunity_history (
			pair, exchange, opportunity_score, volatility, volume_24h_usd,
			trend_strength, sentiment_score, btc_correlation, regime_match,
			reasoning, detected_at, scan_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		opp.Pair, opp.Exchange, opp.OpportunityScore, opp.Volatility, opp.Volume24hUSD,
		opp.TrendStrength, opp.SentimentScore, opp.BTCCorrelation, opp.RegimeMatch,
		opp.Reasoning, detectedAt, scanID,
	)
	if err != nil {
		return fmt.Errorf("failed to save opportunity %s: %w", opp.Pair, err)
	}
	return nil
}

// SaveOpportunities bulk-inserts opportunities inside a single transaction.
func (r *MasterRepository) SaveOpportunities(opps []*master.MarketOpportunity, scanID string) error {
	if len(opps) == 0 {
		return nil
	}

	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO master_opportunity_history (
			pair, exchange, opportunity_score, volatility, volume_24h_usd,
			trend_strength, sentiment_score, btc_correlation, regime_match,
			reasoning, detected_at, scan_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare insert: %w", err)
	}
	defer stmt.Close()

	for _, opp := range opps {
		if opp == nil {
			continue
		}
		detectedAt := opp.DetectedAt
		if detectedAt.IsZero() {
			detectedAt = time.Now()
		}
		if _, err := stmt.Exec(
			opp.Pair, opp.Exchange, opp.OpportunityScore, opp.Volatility, opp.Volume24hUSD,
			opp.TrendStrength, opp.SentimentScore, opp.BTCCorrelation, opp.RegimeMatch,
			opp.Reasoning, detectedAt, scanID,
		); err != nil {
			return fmt.Errorf("failed to insert opportunity %s: %w", opp.Pair, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit opportunities: %w", err)
	}
	return nil
}

// GetRecentOpportunities returns the latest opportunities sorted by detected_at
// descending. pair == "" returns rows for all pairs.
func (r *MasterRepository) GetRecentOpportunities(pair string, limit int) ([]*master.MarketOpportunity, error) {
	if limit <= 0 {
		limit = 50
	}

	var rows *sql.Rows
	var err error

	if pair == "" {
		rows, err = r.db.Query(`
			SELECT pair, exchange, opportunity_score, volatility, volume_24h_usd,
			       trend_strength, sentiment_score, btc_correlation, regime_match,
			       reasoning, detected_at
			FROM master_opportunity_history
			ORDER BY detected_at DESC
			LIMIT ?
		`, limit)
	} else {
		rows, err = r.db.Query(`
			SELECT pair, exchange, opportunity_score, volatility, volume_24h_usd,
			       trend_strength, sentiment_score, btc_correlation, regime_match,
			       reasoning, detected_at
			FROM master_opportunity_history
			WHERE pair = ?
			ORDER BY detected_at DESC
			LIMIT ?
		`, pair, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query opportunities: %w", err)
	}
	defer rows.Close()

	var out []*master.MarketOpportunity
	for rows.Next() {
		opp := &master.MarketOpportunity{}
		var reasoning sql.NullString
		err := rows.Scan(
			&opp.Pair, &opp.Exchange, &opp.OpportunityScore, &opp.Volatility, &opp.Volume24hUSD,
			&opp.TrendStrength, &opp.SentimentScore, &opp.BTCCorrelation, &opp.RegimeMatch,
			&reasoning, &opp.DetectedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan opportunity row: %w", err)
		}
		if reasoning.Valid {
			opp.Reasoning = reasoning.String
		}
		out = append(out, opp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating opportunity rows: %w", err)
	}
	return out, nil
}

// PurgeOldOpportunities deletes opportunity history older than the given duration.
// Returns the number of rows removed.
func (r *MasterRepository) PurgeOldOpportunities(olderThan time.Duration) (int, error) {
	if olderThan <= 0 {
		return 0, nil
	}

	cutoff := time.Now().Add(-olderThan)
	res, err := r.db.Exec(
		`DELETE FROM master_opportunity_history WHERE detected_at < ?`,
		cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to purge opportunities: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to read rows affected: %w", err)
	}

	if affected > 0 {
		r.log.WithField("removed", affected).Info("Purged old master opportunity history")
	}
	return int(affected), nil
}

// SaveStateSnapshot upserts the single-row state snapshot.
func (r *MasterRepository) SaveStateSnapshot(state *master.MasterState) error {
	if state == nil {
		return fmt.Errorf("master state cannot be nil")
	}

	var startedAt sql.NullTime
	if !state.StartedAt.IsZero() {
		startedAt = sql.NullTime{Time: state.StartedAt, Valid: true}
	}
	var lastScanAt sql.NullTime
	if !state.LastScanAt.IsZero() {
		lastScanAt = sql.NullTime{Time: state.LastScanAt, Valid: true}
	}

	running := 0
	if state.Running {
		running = 1
	}

	_, err := r.db.Exec(`
		INSERT INTO master_state_snapshot (
			id, running, started_at, last_scan_at, last_scan_duration_ms,
			scan_count, active_bots, total_capital_usd, allocated_usd,
			last_error, updated_at
		) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			running               = excluded.running,
			started_at            = excluded.started_at,
			last_scan_at          = excluded.last_scan_at,
			last_scan_duration_ms = excluded.last_scan_duration_ms,
			scan_count            = excluded.scan_count,
			active_bots           = excluded.active_bots,
			total_capital_usd     = excluded.total_capital_usd,
			allocated_usd         = excluded.allocated_usd,
			last_error            = excluded.last_error,
			updated_at            = excluded.updated_at
	`,
		running, startedAt, lastScanAt, state.LastScanDuration.Milliseconds(),
		state.ScanCount, state.ActiveBots, state.TotalCapitalUSD, state.AllocatedUSD,
		state.LastError, time.Now(),
	)
	if err != nil {
		return fmt.Errorf("failed to save state snapshot: %w", err)
	}
	return nil
}

// LoadStateSnapshot returns the persisted snapshot or (nil, nil) when absent.
func (r *MasterRepository) LoadStateSnapshot() (*master.MasterState, error) {
	row := r.db.QueryRow(`
		SELECT running, started_at, last_scan_at, last_scan_duration_ms,
		       scan_count, active_bots, total_capital_usd, allocated_usd, last_error
		FROM master_state_snapshot
		WHERE id = 1
	`)

	var (
		running          int
		startedAt        sql.NullTime
		lastScanAt       sql.NullTime
		lastScanDuration int64
		scanCount        int64
		activeBots       int
		totalCapital     float64
		allocatedUSD     float64
		lastError        sql.NullString
	)
	err := row.Scan(
		&running, &startedAt, &lastScanAt, &lastScanDuration,
		&scanCount, &activeBots, &totalCapital, &allocatedUSD, &lastError,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load state snapshot: %w", err)
	}

	state := &master.MasterState{
		Running:          running == 1,
		LastScanDuration: time.Duration(lastScanDuration) * time.Millisecond,
		ScanCount:        scanCount,
		ActiveBots:       activeBots,
		TotalCapitalUSD:  totalCapital,
		AllocatedUSD:     allocatedUSD,
	}
	if startedAt.Valid {
		state.StartedAt = startedAt.Time
	}
	if lastScanAt.Valid {
		state.LastScanAt = lastScanAt.Time
	}
	if lastError.Valid {
		state.LastError = lastError.String
	}
	return state, nil
}
