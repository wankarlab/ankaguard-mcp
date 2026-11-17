// Package storage — G4: performance fee ledger repository.
//
// Implements HWM-based performance fee accrual.  Only PnL above the
// high-water mark generates fees, so users are never charged twice on
// recovered losses.
//
// Table: performance_fee_ledger (created by migration 023).
// Actual column names:
//
//	id, user_id, period_start, period_end (NOT NULL),
//	high_water_mark, period_net_pnl_sum, accrued_fee_usd, rate, status,
//	created_at, updated_at
package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
)

// FeeLedgerRow represents one row of the performance_fee_ledger table.
type FeeLedgerRow struct {
	ID            int64
	UserID        string
	PeriodStart   time.Time
	PeriodEnd     time.Time
	NetPnL        float64 // maps to period_net_pnl_sum
	HighWaterMark float64
	AccruedFee    float64 // maps to accrued_fee_usd
	FeeRate       float64 // maps to rate
	Status        string  // "pending", "closed", "collected"
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// FeeLedgerRepository persists and updates performance fee periods.
type FeeLedgerRepository struct {
	db  *sql.DB
	log *logrus.Logger
}

// NewFeeLedgerRepository constructs a FeeLedgerRepository.
func NewFeeLedgerRepository(db *sql.DB, log *logrus.Logger) *FeeLedgerRepository {
	return &FeeLedgerRepository{db: db, log: log}
}

// defaultFeeRate is the fallback rate (20%) used when the caller passes 0.
const defaultFeeRate = 0.20

// feeLedgerColumns lists SELECT columns in the order scanFeeLedgerRow expects.
const feeLedgerColumns = `id, user_id, period_start, period_end,
	high_water_mark, period_net_pnl_sum, accrued_fee_usd, rate,
	status, created_at, updated_at`

// periodStartForNow returns the first day of the current calendar month (UTC).
func periodStartForNow() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// periodEndForStart returns the first day of the next calendar month.
func periodEndForStart(start time.Time) time.Time {
	return start.AddDate(0, 1, 0)
}

// scanFeeLedgerRow scans a single performance_fee_ledger row into a FeeLedgerRow.
// Works with both *sql.Row and *sql.Rows (both implement the Scan method).
func scanFeeLedgerRow(row interface {
	Scan(...interface{}) error
}) (*FeeLedgerRow, error) {
	r := &FeeLedgerRow{}
	err := row.Scan(
		&r.ID,
		&r.UserID,
		&r.PeriodStart,
		&r.PeriodEnd,
		&r.HighWaterMark,
		&r.NetPnL,
		&r.AccruedFee,
		&r.FeeRate,
		&r.Status,
		&r.CreatedAt,
		&r.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// GetOrCreateOpenPeriod returns the current "pending" period for userID,
// creating one for the current calendar month if none exists.
// feeRate is only applied at creation; pass 0 to use defaultFeeRate (20%).
func (r *FeeLedgerRepository) GetOrCreateOpenPeriod(ctx context.Context, userID string, feeRate float64) (*FeeLedgerRow, error) {
	if userID == "" {
		userID = "default"
	}
	if feeRate <= 0 {
		feeRate = defaultFeeRate
	}

	start := periodStartForNow()
	end := periodEndForStart(start)
	now := time.Now().UTC()

	// Insert; if the UNIQUE(user_id, period_start, period_end) fires, do nothing
	// and fall through to GetCurrentPeriod.
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO performance_fee_ledger
			(user_id, period_start, period_end, high_water_mark, period_net_pnl_sum,
			 accrued_fee_usd, rate, status, created_at, updated_at)
		VALUES (?, ?, ?, 0, 0, 0, ?, 'pending', ?, ?)
		ON CONFLICT(user_id, period_start, period_end) DO NOTHING`,
		userID, start, end, feeRate, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("fee_ledger: upsert open period: %w", err)
	}

	return r.GetCurrentPeriod(ctx, userID)
}

// AccrueFee adds tradePnL to the open period's net PnL and recomputes the
// accrued fee using the HWM formula:
//
//	newFee = max(0, (netPnL - HWM) * rate)
//
// If no open period exists, one is created with defaultFeeRate.
// Returns the updated row. Does NOT collect or transfer funds.
func (r *FeeLedgerRepository) AccrueFee(ctx context.Context, userID string, tradePnL float64) (*FeeLedgerRow, error) {
	if userID == "" {
		userID = "default"
	}

	row, err := r.GetOrCreateOpenPeriod(ctx, userID, 0)
	if err != nil {
		return nil, err
	}

	// DATA-02: the previous version read row.NetPnL/HighWaterMark into Go,
	// computed newNetPnL/newFee in memory, then wrote them back with
	// `WHERE id=? AND status='pending'` — a WHERE clause that only checks
	// status, not the value that was read. Two concurrent AccrueFee calls for
	// the same user (e.g. two trade closes racing) would both read the same
	// period_net_pnl_sum, compute independently, and the second UPDATE would
	// silently overwrite the first's result — a lost update that under-charges
	// performance fees with no visible error.
	//
	// Fixed by folding the read-modify-write into a single atomic UPDATE that
	// adds tradePnL to the CURRENT (not previously-read) period_net_pnl_sum and
	// recomputes accrued_fee_usd from that same current value, replicating the
	// exact Go formula in SQL: newFee = max(0, (netPnL_after - HWM) * rate).
	now := time.Now().UTC()
	res, err := r.db.ExecContext(ctx, `
		UPDATE performance_fee_ledger
		SET period_net_pnl_sum = period_net_pnl_sum + ?,
		    accrued_fee_usd    = MAX(0, CASE
		                                  WHEN (period_net_pnl_sum + ?) > high_water_mark
		                                  THEN (period_net_pnl_sum + ? - high_water_mark) * rate
		                                  ELSE 0
		                                END),
		    updated_at         = ?
		WHERE id = ? AND status = 'pending'`,
		tradePnL, tradePnL, tradePnL, now, row.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("fee_ledger: accrue fee: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, fmt.Errorf("fee_ledger: period %d is no longer open (status≠pending)", row.ID)
	}

	updated, err := r.getByID(ctx, row.ID)
	if err != nil {
		return nil, err
	}

	r.log.WithFields(logrus.Fields{
		"user_id":     userID,
		"trade_pnl":   tradePnL,
		"net_pnl":     updated.NetPnL,
		"accrued_fee": updated.AccruedFee,
		"hwm":         updated.HighWaterMark,
	}).Debug("fee_ledger: fee accrued")

	return updated, nil
}

// ClosePeriod finalises the current open period:
//   - sets status = "closed"
//   - sets period_end = now (actual close timestamp)
//   - bumps high_water_mark to max(HWM, netPnL)
//
// Returns the closed row. Returns an error if no pending period exists.
func (r *FeeLedgerRepository) ClosePeriod(ctx context.Context, userID string) (*FeeLedgerRow, error) {
	if userID == "" {
		userID = "default"
	}

	row, err := r.GetCurrentPeriod(ctx, userID)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("fee_ledger: no open period for user %q", userID)
	}
	if err != nil {
		return nil, err
	}

	newHWM := row.HighWaterMark
	if row.NetPnL > newHWM {
		newHWM = row.NetPnL
	}

	now := time.Now().UTC()
	_, err = r.db.ExecContext(ctx, `
		UPDATE performance_fee_ledger
		SET status          = 'closed',
		    period_end      = ?,
		    high_water_mark = ?,
		    updated_at      = ?
		WHERE id = ? AND status = 'pending'`,
		now, newHWM, now, row.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("fee_ledger: close period: %w", err)
	}

	return r.getByID(ctx, row.ID)
}

// GetCurrentPeriod returns the most recent "pending" period for userID.
// Returns sql.ErrNoRows when none exists.
func (r *FeeLedgerRepository) GetCurrentPeriod(ctx context.Context, userID string) (*FeeLedgerRow, error) {
	if userID == "" {
		userID = "default"
	}
	q := `SELECT ` + feeLedgerColumns + `
	      FROM performance_fee_ledger
	      WHERE user_id = ? AND status = 'pending'
	      ORDER BY period_start DESC
	      LIMIT 1`
	return scanFeeLedgerRow(r.db.QueryRowContext(ctx, q, userID))
}

// GetHistory returns up to limit closed/collected periods for userID,
// ordered by period_start descending.
func (r *FeeLedgerRepository) GetHistory(ctx context.Context, userID string, limit int) ([]*FeeLedgerRow, error) {
	if userID == "" {
		userID = "default"
	}
	if limit <= 0 {
		limit = 12
	}

	q := `SELECT ` + feeLedgerColumns + `
	      FROM performance_fee_ledger
	      WHERE user_id = ? AND status != 'pending'
	      ORDER BY period_start DESC
	      LIMIT ?`

	rows, err := r.db.QueryContext(ctx, q, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("fee_ledger: get history: %w", err)
	}
	defer rows.Close()

	var out []*FeeLedgerRow
	for rows.Next() {
		row, scanErr := scanFeeLedgerRow(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// getByID fetches a single row by primary key — used internally after mutations.
func (r *FeeLedgerRepository) getByID(ctx context.Context, id int64) (*FeeLedgerRow, error) {
	q := `SELECT ` + feeLedgerColumns + `
	      FROM performance_fee_ledger WHERE id = ?`
	return scanFeeLedgerRow(r.db.QueryRowContext(ctx, q, id))
}
