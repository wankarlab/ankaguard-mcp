package xsec

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"
)

// Position is one open paper position.
type Position struct {
	Pair     string
	Dir      int     // +1 long, -1 short
	Entry    float64 // entry price
	Notional float64 // USD notional at entry
}

// RebalanceSummary describes one rebalance.
type RebalanceSummary struct {
	Equity   float64
	Opened   int
	Closed   int
	Resized  int // kept positions re-sized to match their new target notional
	CostPaid float64
}

// Portfolio is a paper-traded dollar-neutral long/short book persisted in SQLite.
type Portfolio struct {
	db        *sql.DB
	cash      float64
	positions map[string]*Position // keyed by pair
}

// NewPaperPortfolio loads existing paper state from db or initializes it with
// startCapital. Creates tables xsec_positions and xsec_equity if absent
// (CREATE TABLE IF NOT EXISTS, idempotent).
func NewPaperPortfolio(db *sql.DB, startCapital float64) (*Portfolio, error) {
	if db == nil {
		return nil, fmt.Errorf("xsec: nil db")
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS xsec_positions (
		pair      TEXT PRIMARY KEY,
		dir       INTEGER NOT NULL,
		entry     REAL NOT NULL,
		notional  REAL NOT NULL,
		opened_at TIMESTAMP NOT NULL
	)`); err != nil {
		return nil, fmt.Errorf("xsec: create xsec_positions: %w", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS xsec_equity (
		ts      TIMESTAMP NOT NULL,
		equity  REAL NOT NULL,
		n_long  INTEGER NOT NULL,
		n_short INTEGER NOT NULL,
		cash    REAL
	)`); err != nil {
		return nil, fmt.Errorf("xsec: create xsec_equity: %w", err)
	}
	// Idempotent schema upgrades for existing tables.
	db.Exec(`ALTER TABLE xsec_equity ADD COLUMN cash REAL`)                          // no-op if already present
	db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS xsec_equity_ts ON xsec_equity (ts)`) // prevents double-run duplicates

	p := &Portfolio{
		db:        db,
		positions: make(map[string]*Position),
	}

	// Reload open positions.
	rows, err := db.Query(`SELECT pair, dir, entry, notional FROM xsec_positions`)
	if err != nil {
		return nil, fmt.Errorf("xsec: load positions: %w", err)
	}
	var heldNotional float64
	for rows.Next() {
		var pos Position
		if err := rows.Scan(&pos.Pair, &pos.Dir, &pos.Entry, &pos.Notional); err != nil {
			rows.Close()
			return nil, fmt.Errorf("xsec: scan position: %w", err)
		}
		pc := pos
		p.positions[pos.Pair] = &pc
		heldNotional += pos.Notional
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("xsec: rows: %w", err)
	}
	rows.Close()

	// Reload cash from the last equity row. Prefer the explicit `cash` column
	// (written since this fix) over the derived equity-heldNotional calculation
	// (which drifts as prices move between rebalances).
	var lastCash, lastEquity sql.NullFloat64
	if err := db.QueryRow(
		`SELECT equity, cash FROM xsec_equity ORDER BY rowid DESC LIMIT 1`,
	).Scan(&lastEquity, &lastCash); err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("xsec: load equity: %w", err)
	}
	switch {
	case lastCash.Valid:
		p.cash = lastCash.Float64
	case lastEquity.Valid:
		// Legacy rows (no cash column yet): derive best-effort from equity minus
		// held notional (only accurate at rebalance time — an approximation).
		p.cash = lastEquity.Float64 - heldNotional
	default:
		p.cash = startCapital
	}

	return p, nil
}

// markToMarket returns the value of a position at the given price.
// Long MtM  = notional + (price-entry)/entry*notional
// Short MtM = notional + (entry-price)/entry*notional
func markToMarket(pos *Position, price float64) float64 {
	if pos.Entry == 0 {
		return pos.Notional
	}
	move := (price - pos.Entry) / pos.Entry * float64(pos.Dir) * pos.Notional
	return pos.Notional + move
}

// realizedPnL returns the profit/loss of closing a position at price (MtM - notional).
func realizedPnL(pos *Position, price float64) float64 {
	return markToMarket(pos, price) - pos.Notional
}

// Snapshot returns current positions and mark-to-market equity at the given prices.
func (p *Portfolio) Snapshot(prices map[string]float64) ([]Position, float64) {
	out := make([]Position, 0, len(p.positions))
	equity := p.cash
	for _, pos := range p.positions {
		out = append(out, *pos)
		price, ok := prices[pos.Pair]
		if !ok {
			price = pos.Entry // fall back to entry if price missing
		}
		equity += markToMarket(pos, price)
	}
	return out, equity
}

// Rebalance moves the book to the target baskets at the given prices using
// equal weights and full gross exposure. It is a thin wrapper around
// RebalanceTarget and preserves the original baseline behavior exactly
// (in particular, kept positions are never resized).
func (p *Portfolio) Rebalance(ctx context.Context, target Baskets, prices map[string]float64, costFrac float64) (RebalanceSummary, error) {
	return p.RebalanceTarget(ctx, Target{Baskets: target}, prices, costFrac)
}

// RebalanceTarget moves the book to the given Target, honoring optional
// per-pair leg weights and a gross-exposure scale:
//
//	legSize_i = equity * 0.5 * GrossScale * weight_i
//
// Nil weight maps mean equal weight within the leg; GrossScale 0 means 1.0.
//
// Resizing kept positions: when weights or vol targeting are active, a pair
// that stays on the same side may still need a different notional. If the
// target notional differs from the held notional by more than 10% relative,
// the position is closed and reopened at the current price (PnL is realized,
// the entry resets). For a paper book this is equivalent to trading the delta;
// we charge cost only on |delta| notional, which is what a real partial
// resize would pay. In legacy mode (nil weights AND GrossScale in {0, 1}) no
// resizing ever happens, so the equal-weight baseline is reproduced exactly.
func (p *Portfolio) RebalanceTarget(ctx context.Context, target Target, prices map[string]float64, costFrac float64) (RebalanceSummary, error) {
	gross := target.GrossScale
	if gross <= 0 {
		gross = 1.0
	}
	// Legacy mode: equal weights, unit gross — reproduce Rebalance() exactly
	// (no resize of kept positions).
	legacy := target.LongWeights == nil && target.ShortWeights == nil && gross == 1.0

	// Build target side lookup: pair -> direction.
	targetDir := make(map[string]int, len(target.Longs)+len(target.Shorts))
	for _, pair := range target.Longs {
		targetDir[pair] = +1
	}
	for _, pair := range target.Shorts {
		targetDir[pair] = -1
	}

	var summary RebalanceSummary

	// 1) Close positions no longer on their target side (gone, or side flipped).
	for pair, pos := range p.positions {
		want, ok := targetDir[pair]
		if ok && want == pos.Dir {
			continue // keep (possibly resized below)
		}
		price, ok := prices[pair]
		if !ok {
			return summary, fmt.Errorf("xsec: missing price for held pair %s", pair)
		}
		pnl := realizedPnL(pos, price)
		fee := pos.Notional * costFrac
		p.cash += pos.Notional + pnl - fee
		summary.CostPaid += fee
		summary.Closed++
		delete(p.positions, pair)
	}

	// 2) Compute current equity to size the new book (so it compounds).
	_, equity := p.Snapshot(prices)

	// weightFor resolves a pair's fraction of its leg capital.
	weightFor := func(pair string, leg []string, w map[string]float64) float64 {
		if w == nil {
			if len(leg) == 0 {
				return 0
			}
			return 1.0 / float64(len(leg))
		}
		return w[pair] // absent pair => 0 => not opened / closed on resize
	}
	desired := make(map[string]float64, len(targetDir))
	for _, pair := range target.Longs {
		desired[pair] = equity * 0.5 * gross * weightFor(pair, target.Longs, target.LongWeights)
	}
	for _, pair := range target.Shorts {
		desired[pair] = equity * 0.5 * gross * weightFor(pair, target.Shorts, target.ShortWeights)
	}

	// 3) Resize kept positions whose target notional drifted >10% relative.
	// Skipped entirely in legacy mode (see doc comment).
	if !legacy {
		for pair, pos := range p.positions {
			want := desired[pair]
			price, ok := prices[pair]
			if !ok {
				return summary, fmt.Errorf("xsec: missing price for held pair %s", pair)
			}
			if want <= 0 {
				// Zero target weight: close outright.
				pnl := realizedPnL(pos, price)
				fee := pos.Notional * costFrac
				p.cash += pos.Notional + pnl - fee
				summary.CostPaid += fee
				summary.Closed++
				delete(p.positions, pair)
				continue
			}
			if math.Abs(want-pos.Notional)/want <= 0.10 {
				continue // close enough: avoid churning fees on noise
			}
			// Close at current price (realize PnL) and reopen at the target
			// notional; charge cost on the traded delta only.
			pnl := realizedPnL(pos, price)
			fee := math.Abs(want-pos.Notional) * costFrac
			p.cash += pos.Notional + pnl - want - fee
			pos.Entry = price
			pos.Notional = want
			summary.CostPaid += fee
			summary.Resized++
		}
	}

	// 4) Open target positions not yet held (with matching direction).
	open := func(pair string, dir int) error {
		size := desired[pair]
		if size <= 0 {
			return nil
		}
		if _, held := p.positions[pair]; held {
			return nil // already on correct side (kept/resized above)
		}
		price, ok := prices[pair]
		if !ok {
			return fmt.Errorf("xsec: missing price for target pair %s", pair)
		}
		fee := size * costFrac
		p.cash -= size + fee
		p.positions[pair] = &Position{
			Pair:     pair,
			Dir:      dir,
			Entry:    price,
			Notional: size,
		}
		summary.CostPaid += fee
		summary.Opened++
		return nil
	}
	for _, pair := range target.Longs {
		if err := open(pair, +1); err != nil {
			return summary, err
		}
	}
	for _, pair := range target.Shorts {
		if err := open(pair, -1); err != nil {
			return summary, err
		}
	}

	nLong := len(target.Longs)
	nShort := len(target.Shorts)

	// 5) Recompute final MtM equity and persist.
	_, summary.Equity = p.Snapshot(prices)

	ts := target.AsOf
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	if err := p.persist(ctx, ts, summary.Equity, nLong, nShort); err != nil {
		return summary, err
	}

	return summary, nil
}

// persist writes the current positions table and appends an equity row.
func (p *Portfolio) persist(ctx context.Context, ts time.Time, equity float64, nLong, nShort int) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("xsec: begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM xsec_positions`); err != nil {
		return fmt.Errorf("xsec: clear positions: %w", err)
	}
	for _, pos := range p.positions {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO xsec_positions (pair, dir, entry, notional, opened_at) VALUES (?, ?, ?, ?, ?)`,
			pos.Pair, pos.Dir, pos.Entry, pos.Notional, ts,
		); err != nil {
			return fmt.Errorf("xsec: insert position %s: %w", pos.Pair, err)
		}
	}
	// INSERT OR REPLACE ensures a double-run on the same timestamp overwrites
	// the stale first-run row instead of creating a duplicate that confuses
	// the ORDER BY rowid DESC reload.
	if _, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO xsec_equity (ts, equity, n_long, n_short, cash) VALUES (?, ?, ?, ?, ?)`,
		ts, equity, nLong, nShort, p.cash,
	); err != nil {
		return fmt.Errorf("xsec: insert equity: %w", err)
	}
	return tx.Commit()
}
