// Package billing implements CP-1: the non-custodial performance fee ledger.
//
// AccruePerformanceFee computes the incremental fee on top of the High Water
// Mark (HWM) over reconciled trade_history NetPnL.  It is READ-ONLY accrual:
// it writes to performance_fee_ledger but NEVER triggers a payment, exchange
// withdrawal, or Fineract call.  Collection is a separate, opt-in operator
// step via Odoo ProcessPayment.
//
// Design choices (ADR-0002):
//   - NetPnL from trade_history only (not equity — deposits distort equity).
//   - HWM persists across periods; accrual = rate × max(0, cumPnL - HWM).
//   - Collection OFF by default; ledger table exists as accounting record.
//   - Gated on reconciler_enabled (SEC-10): fees over unreconciled PnL are
//     not meaningful.
package billing

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/rantipay/trading-mcp/internal/api/v1/handlers"
)

const defaultFeeRate = 0.20 // 20 % performance fee (high-water mark)

// FeePeriod represents one period's accrual entry in performance_fee_ledger.
type FeePeriod struct {
	ID              int64
	UserID          string
	TenantID        string
	PeriodStart     time.Time
	PeriodEnd       time.Time
	HighWaterMark   float64
	PeriodNetPnLSum float64
	AccruedFeeUSD   float64
	Rate            float64
	Status          string // "pending" | "collected" | "waived"
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// FeeLedger manages performance fee accrual over reconciled NetPnL.
type FeeLedger struct {
	db *sql.DB
}

// NewFeeLedger creates a FeeLedger backed by the given database (SQLite or Postgres).
func NewFeeLedger(db *sql.DB) *FeeLedger {
	return &FeeLedger{db: db}
}

// AccruePerformanceFee computes the fee for [periodStart, periodEnd] using the
// HWM principle.  It reads closed trade NetPnL from trade_history and inserts
// (or updates) a row in performance_fee_ledger.
//
// Formula: accrued = rate × max(0, Σ NetPnL_period + prior cumulative PnL − HWM)
//
// Returns the FeePeriod written.  Returns nil, nil when there is no accrual
// (PnL did not exceed the HWM).
func (l *FeeLedger) AccruePerformanceFee(
	ctx context.Context,
	userID string,
	periodStart, periodEnd time.Time,
	rate float64,
) (*FeePeriod, error) {
	if rate <= 0 {
		rate = defaultFeeRate
	}
	if userID == "" {
		return nil, fmt.Errorf("userID is required")
	}
	if !periodEnd.After(periodStart) {
		return nil, fmt.Errorf("periodEnd must be after periodStart")
	}

	// 1. Sum reconciled NetPnL for this period from trade_history.
	periodNetPnL, err := l.sumNetPnL(ctx, userID, periodStart, periodEnd)
	if err != nil {
		return nil, fmt.Errorf("sumNetPnL: %w", err)
	}

	// 2. Load the current High Water Mark (max cumulative NetPnL ever reached).
	hwm, err := l.currentHWM(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("currentHWM: %w", err)
	}

	// 3. Compute cumulative PnL = HWM + this period's increment.
	// The HWM already represents the max of all prior periods; we add the
	// current period PnL to see if we are above it.
	cumulativePnL := hwm + periodNetPnL
	above := cumulativePnL - hwm
	if above < 0 {
		above = 0
	}
	accruedFee := rate * above

	// 4. Update HWM if we exceeded it.
	newHWM := hwm
	if cumulativePnL > hwm {
		newHWM = cumulativePnL
	}

	// 5. Upsert the period row.
	period := &FeePeriod{
		UserID:          userID,
		PeriodStart:     periodStart,
		PeriodEnd:       periodEnd,
		HighWaterMark:   newHWM,
		PeriodNetPnLSum: periodNetPnL,
		AccruedFeeUSD:   accruedFee,
		Rate:            rate,
		Status:          "pending",
	}

	if err := l.upsertPeriod(ctx, period); err != nil {
		return nil, fmt.Errorf("upsertPeriod: %w", err)
	}

	if accruedFee == 0 {
		return nil, nil // no fee this period
	}
	return period, nil
}

// GetPendingPeriods returns all pending (uncollected) fee periods for a user.
func (l *FeeLedger) GetPendingPeriods(ctx context.Context, userID string) ([]*FeePeriod, error) {
	rows, err := l.db.QueryContext(ctx, `
		SELECT id, user_id, COALESCE(tenant_id,''), period_start, period_end,
		       high_water_mark, period_net_pnl_sum, accrued_fee_usd, rate,
		       status, created_at, updated_at
		  FROM performance_fee_ledger
		 WHERE user_id = ? AND status = 'pending'
		 ORDER BY period_start DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPeriods(rows)
}

// GetPeriodByID returns a single fee period by its row ID.
func (l *FeeLedger) GetPeriodByID(ctx context.Context, id int64) (*FeePeriod, error) {
	rows, err := l.db.QueryContext(ctx, `
		SELECT id, user_id, COALESCE(tenant_id,''), period_start, period_end,
		       high_water_mark, period_net_pnl_sum, accrued_fee_usd, rate,
		       status, created_at, updated_at
		  FROM performance_fee_ledger
		 WHERE id = ?
		 LIMIT 1
	`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	periods, err := scanPeriods(rows)
	if err != nil || len(periods) == 0 {
		return nil, err
	}
	return periods[0], nil
}

// sumNetPnL sums closed trade net_pnl for a user in the given window.
func (l *FeeLedger) sumNetPnL(ctx context.Context, userID string, from, to time.Time) (float64, error) {
	var total sql.NullFloat64
	err := l.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(net_pnl), 0)
		  FROM trade_history
		 WHERE user_id = ?
		   AND status  = 'closed'
		   AND exit_time >= ?
		   AND exit_time <  ?
		   AND net_pnl IS NOT NULL
	`, userID, from, to).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total.Float64, nil
}

// currentHWM returns the most recent high_water_mark stored for this user.
// Returns 0 if no prior period exists.
func (l *FeeLedger) currentHWM(ctx context.Context, userID string) (float64, error) {
	var hwm sql.NullFloat64
	err := l.db.QueryRowContext(ctx, `
		SELECT high_water_mark
		  FROM performance_fee_ledger
		 WHERE user_id = ?
		 ORDER BY period_end DESC
		 LIMIT 1
	`, userID).Scan(&hwm)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return hwm.Float64, nil
}

// upsertPeriod inserts or replaces a fee period row.
func (l *FeeLedger) upsertPeriod(ctx context.Context, p *FeePeriod) error {
	now := time.Now()
	_, err := l.db.ExecContext(ctx, `
		INSERT INTO performance_fee_ledger
		       (user_id, tenant_id, period_start, period_end,
		        high_water_mark, period_net_pnl_sum, accrued_fee_usd, rate,
		        status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (user_id, period_start, period_end) DO UPDATE SET
		    high_water_mark    = excluded.high_water_mark,
		    period_net_pnl_sum = excluded.period_net_pnl_sum,
		    accrued_fee_usd    = excluded.accrued_fee_usd,
		    rate               = excluded.rate,
		    updated_at         = excluded.updated_at
	`,
		p.UserID,
		nilableString(p.TenantID),
		p.PeriodStart,
		p.PeriodEnd,
		p.HighWaterMark,
		p.PeriodNetPnLSum,
		p.AccruedFeeUSD,
		p.Rate,
		p.Status,
		now,
		now,
	)
	return err
}

func scanPeriods(rows *sql.Rows) ([]*FeePeriod, error) {
	var out []*FeePeriod
	for rows.Next() {
		p := &FeePeriod{}
		if err := rows.Scan(
			&p.ID, &p.UserID, &p.TenantID,
			&p.PeriodStart, &p.PeriodEnd,
			&p.HighWaterMark, &p.PeriodNetPnLSum, &p.AccruedFeeUSD, &p.Rate,
			&p.Status, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// FeePeriodsForUser satisfies the handlers.FeeLedger interface: returns the
// user's pending periods projected to handlers.FeePeriodEconomy rows. The split
// economy (rate + author/treasury) is applied by the handler from config.
func (l *FeeLedger) FeePeriodsForUser(ctx context.Context, userID string) ([]handlers.FeePeriodEconomy, error) {
	periods, err := l.GetPendingPeriods(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]handlers.FeePeriodEconomy, 0, len(periods))
	for _, p := range periods {
		out = append(out, toEconomy(p))
	}
	return out, nil
}

// FeePeriodByID satisfies the handlers.FeeLedger interface.
func (l *FeeLedger) FeePeriodByID(ctx context.Context, id int64) (*handlers.FeePeriodEconomy, error) {
	p, err := l.GetPeriodByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, nil
	}
	e := toEconomy(p)
	return &e, nil
}

// toEconomy projects an internal FeePeriod to the handler-facing economy row.
func toEconomy(p *FeePeriod) handlers.FeePeriodEconomy {
	return handlers.FeePeriodEconomy{
		ID:              p.ID,
		UserID:          p.UserID,
		PeriodStart:     p.PeriodStart,
		PeriodEnd:       p.PeriodEnd,
		HighWaterMark:   p.HighWaterMark,
		PeriodNetPnLSum: p.PeriodNetPnLSum,
		AccruedFeeUSD:   p.AccruedFeeUSD,
		Rate:            p.Rate,
		Status:          p.Status,
	}
}

func nilableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
