package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
)

// PortfolioSnapshot represents a point-in-time portfolio state
type PortfolioSnapshot struct {
	ID               int64
	SnapshotTime     time.Time
	TotalValueUSDT   float64
	AvailableBalance float64
	UnrealizedPnL    float64
	PositionsCount   int
	Assets           map[string]float64
}

// PortfolioSnapshotRepository handles portfolio snapshot persistence
type PortfolioSnapshotRepository struct {
	db  *sql.DB
	log *logrus.Logger
}

// NewPortfolioSnapshotRepository creates a new portfolio snapshot repository
func NewPortfolioSnapshotRepository(db *sql.DB, log *logrus.Logger) *PortfolioSnapshotRepository {
	return &PortfolioSnapshotRepository{db: db, log: log}
}

// Save inserts a new portfolio snapshot for the "default" user,
// ignoring duplicates for the same time.
// Use SaveForUser in multi-user contexts.
func (r *PortfolioSnapshotRepository) Save(snap *PortfolioSnapshot) error {
	return r.SaveForUser("default", snap)
}

// SaveForUser inserts a new portfolio snapshot attributed to the given userID,
// ignoring duplicates (same snapshot_time + user_id).
func (r *PortfolioSnapshotRepository) SaveForUser(userID string, snap *PortfolioSnapshot) error {
	if userID == "" {
		userID = "default"
	}

	assetsJSON, err := json.Marshal(snap.Assets)
	if err != nil {
		return fmt.Errorf("failed to marshal assets: %w", err)
	}

	_, err = r.db.Exec(`
		INSERT OR IGNORE INTO portfolio_snapshots
		(snapshot_time, total_value_usdt, available_balance, unrealized_pnl, positions_count, assets_json, user_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		snap.SnapshotTime.UTC().Format("2006-01-02T15:04:05"),
		snap.TotalValueUSDT,
		snap.AvailableBalance,
		snap.UnrealizedPnL,
		snap.PositionsCount,
		string(assetsJSON),
		userID,
	)
	return err
}

// GetByTimeRange returns snapshots within the given time range for the "default" user,
// ordered by time asc. Use GetByTimeRangeForUser in multi-user contexts.
func (r *PortfolioSnapshotRepository) GetByTimeRange(from, to time.Time) ([]*PortfolioSnapshot, error) {
	return r.GetByTimeRangeForUser("default", from, to)
}

// GetByTimeRangeForUser returns snapshots for a specific user within the given time range,
// ordered by snapshot_time ascending.
func (r *PortfolioSnapshotRepository) GetByTimeRangeForUser(userID string, from, to time.Time) ([]*PortfolioSnapshot, error) {
	if userID == "" {
		userID = "default"
	}

	rows, err := r.db.Query(`
		SELECT id, snapshot_time, total_value_usdt, available_balance, unrealized_pnl, positions_count, assets_json
		FROM portfolio_snapshots
		WHERE snapshot_time BETWEEN ? AND ? AND user_id = ?
		ORDER BY snapshot_time ASC`,
		from.UTC().Format("2006-01-02T15:04:05"),
		to.UTC().Format("2006-01-02T15:04:05"),
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query snapshots: %w", err)
	}
	defer rows.Close()

	var snapshots []*PortfolioSnapshot
	for rows.Next() {
		var s PortfolioSnapshot
		var snapshotTimeStr string
		var assetsJSON string

		if err := rows.Scan(&s.ID, &snapshotTimeStr, &s.TotalValueUSDT, &s.AvailableBalance, &s.UnrealizedPnL, &s.PositionsCount, &assetsJSON); err != nil {
			return nil, fmt.Errorf("failed to scan snapshot: %w", err)
		}

		s.SnapshotTime, err = time.Parse("2006-01-02T15:04:05", snapshotTimeStr)
		if err != nil {
			// Try RFC3339
			s.SnapshotTime, _ = time.Parse(time.RFC3339, snapshotTimeStr)
		}

		s.Assets = make(map[string]float64)
		if err := json.Unmarshal([]byte(assetsJSON), &s.Assets); err != nil {
			r.log.WithError(err).Warn("Failed to unmarshal assets JSON")
		}

		snapshots = append(snapshots, &s)
	}

	return snapshots, rows.Err()
}

// GetLatest returns the most recent snapshot for the "default" user.
// Use GetLatestForUser in multi-user contexts.
func (r *PortfolioSnapshotRepository) GetLatest() (*PortfolioSnapshot, error) {
	return r.GetLatestForUser("default")
}

// GetLatestForUser returns the most recent snapshot for a specific user.
func (r *PortfolioSnapshotRepository) GetLatestForUser(userID string) (*PortfolioSnapshot, error) {
	if userID == "" {
		userID = "default"
	}

	var s PortfolioSnapshot
	var snapshotTimeStr string
	var assetsJSON string

	err := r.db.QueryRow(`
		SELECT id, snapshot_time, total_value_usdt, available_balance, unrealized_pnl, positions_count, assets_json
		FROM portfolio_snapshots
		WHERE user_id = ?
		ORDER BY snapshot_time DESC
		LIMIT 1`,
		userID,
	).Scan(&s.ID, &snapshotTimeStr, &s.TotalValueUSDT, &s.AvailableBalance, &s.UnrealizedPnL, &s.PositionsCount, &assetsJSON)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get latest snapshot: %w", err)
	}

	s.SnapshotTime, err = time.Parse("2006-01-02T15:04:05", snapshotTimeStr)
	if err != nil {
		s.SnapshotTime, _ = time.Parse(time.RFC3339, snapshotTimeStr)
	}

	s.Assets = make(map[string]float64)
	if err := json.Unmarshal([]byte(assetsJSON), &s.Assets); err != nil {
		r.log.WithError(err).Warn("Failed to unmarshal assets JSON")
	}

	return &s, nil
}

// Count returns the total number of snapshots
func (r *PortfolioSnapshotRepository) Count() (int64, error) {
	var count int64
	err := r.db.QueryRow("SELECT COUNT(*) FROM portfolio_snapshots").Scan(&count)
	return count, err
}
