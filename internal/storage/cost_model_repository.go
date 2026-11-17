package storage

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/rantipay/trading-mcp/internal/backtest/domain"
	"github.com/sirupsen/logrus"
)

// CostModelRepository reads from the GLOBAL (no-RLS) cost_models catalog.
// It is read-only for the application; rows are seeded by migration 026.
type CostModelRepository struct {
	db  *sql.DB
	log *logrus.Logger
}

// NewCostModelRepository creates a CostModelRepository.
func NewCostModelRepository(db *sql.DB, log *logrus.Logger) *CostModelRepository {
	return &CostModelRepository{db: db, log: log}
}

// Get returns the CostModel for costModelID, or an error if not found.
func (r *CostModelRepository) Get(ctx context.Context, costModelID string) (domain.CostModel, error) {
	var m domain.CostModel
	err := r.db.QueryRowContext(ctx, `
		SELECT cost_model_id, taker_fee_pct, maker_fee_pct, funding_fee_pct, slippage_pct
		FROM cost_models WHERE cost_model_id = ?
	`, costModelID).Scan(&m.ID, &m.EntryTakerFeePct, &m.ExitMakerFeePct, &m.ExitTakerFeePct, &m.SlippagePct)
	if err == sql.ErrNoRows {
		return domain.CostModel{}, fmt.Errorf("cost model %q not found", costModelID)
	}
	if err != nil {
		return domain.CostModel{}, fmt.Errorf("get cost model: %w", err)
	}
	return m, nil
}

// GetOrDefault returns the CostModel for costModelID, or domain.DefaultCostModel() if not found.
func (r *CostModelRepository) GetOrDefault(ctx context.Context, costModelID string) domain.CostModel {
	if costModelID == "" {
		return domain.DefaultCostModel()
	}
	m, err := r.Get(ctx, costModelID)
	if err != nil {
		r.log.WithField("cost_model_id", costModelID).Warn("cost model not found — using default")
		return domain.DefaultCostModel()
	}
	return m
}

// List returns all cost models in the catalog.
func (r *CostModelRepository) List(ctx context.Context) ([]domain.CostModel, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT cost_model_id, taker_fee_pct, maker_fee_pct, funding_fee_pct, slippage_pct
		FROM cost_models ORDER BY cost_model_id
	`)
	if err != nil {
		return nil, fmt.Errorf("list cost models: %w", err)
	}
	defer rows.Close()

	var out []domain.CostModel
	for rows.Next() {
		var m domain.CostModel
		if err := rows.Scan(&m.ID, &m.EntryTakerFeePct, &m.ExitMakerFeePct, &m.ExitTakerFeePct, &m.SlippagePct); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
