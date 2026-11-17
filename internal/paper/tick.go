package paper

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/sirupsen/logrus"
)

// TickResult summarizes one pass of the periodic process.
type TickResult struct {
	Filled      int
	Rejected    int
	Liquidated  int
	PriceErrors int
}

type watchKey struct {
	exchange, market, lookup, symbol string
}

// Run checks pending orders and leveraged positions every PollInterval until
// ctx is cancelled. All state lives in SQLite, so a restart resumes from it.
func (b *Broker) Run(ctx context.Context) {
	t := time.NewTicker(b.cfg.PollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if res, err := b.Tick(ctx); err != nil {
				logrus.WithError(err).Warn("paper: periodic check failed")
			} else if res.Filled+res.Liquidated+res.Rejected > 0 {
				logrus.WithFields(logrus.Fields{"filled": res.Filled, "liquidated": res.Liquidated, "rejected": res.Rejected}).Info("paper: periodic check")
			}
		}
	}
}

// Tick runs one pass: for every symbol with an open order or a leveraged
// position (of any holder) it fetches the real price, force-closes positions
// whose loss reached the margin and fills the orders the price now allows.
func (b *Broker) Tick(ctx context.Context) (TickResult, error) {
	var res TickResult
	keys, err := b.watched(ctx)
	if err != nil {
		return res, err
	}
	for _, k := range keys {
		px, perr := b.price(ctx, k.exchange, k.lookup, k.market == marketFutures)
		if perr != nil {
			res.PriceErrors++
			continue
		}
		if err := b.process(ctx, k, px, &res); err != nil {
			return res, err
		}
	}
	return res, nil
}

func (b *Broker) watched(ctx context.Context) ([]watchKey, error) {
	storeMu.Lock()
	defer storeMu.Unlock()
	rows, err := b.db.QueryContext(ctx, `
		SELECT exchange, market, pair, symbol FROM paper_orders WHERE status = 'open'
		UNION
		SELECT exchange, market, pair, symbol FROM paper_positions WHERE status = 'open' AND market = 'futures'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []watchKey
	for rows.Next() {
		var ex, market, pair, symbol string
		if err := rows.Scan(&ex, &market, &pair, &symbol); err != nil {
			return nil, err
		}
		out = append(out, watchKey{exchange: ex, market: market, symbol: symbol, lookup: lookupKey(market, pair, symbol)})
	}
	return out, rows.Err()
}

func (b *Broker) process(ctx context.Context, k watchKey, px float64, res *TickResult) error {
	storeMu.Lock()
	defer storeMu.Unlock()
	return b.inTx(ctx, func(tx *sql.Tx) error {
		if k.market == marketFutures {
			rows, err := tx.QueryContext(ctx, `SELECT `+posCols+`, holder FROM paper_positions
				WHERE symbol = ? AND market = 'futures' AND status = 'open'`, k.symbol)
			if err != nil {
				return err
			}
			var hit []*position
			for rows.Next() {
				p := &position{}
				var cbr int
				var holder string
				if err := rows.Scan(&p.id, &p.exchange, &p.pair, &p.symbol, &p.market, &p.side, &p.size, &p.entry, &p.leverage,
					&p.margin, &p.realized, &p.status, &p.closeReason, &p.closePrice, &cbr, &p.openedAt, &holder); err != nil {
					rows.Close()
					return err
				}
				if b.liquidationHit(p, px) {
					hit = append(hit, p)
				}
			}
			rows.Close()
			for _, p := range hit {
				if err := b.liquidate(ctx, tx, p, px); err != nil {
					return err
				}
				res.Liquidated++
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+orderCols+` FROM paper_orders
			WHERE status = 'open' AND symbol = ? AND market = ? ORDER BY created_at, rowid`, k.symbol, k.market)
		if err != nil {
			return err
		}
		var orders []*orderRow
		for rows.Next() {
			o, err := scanOrder(rows)
			if err != nil {
				rows.Close()
				return err
			}
			orders = append(orders, o)
		}
		rows.Close()
		for _, o := range orders {
			filled, ferr := b.tryFill(ctx, tx, o, px)
			if ferr != nil {
				// A pending order that can no longer be honoured is rejected, not retried forever.
				if _, err := tx.ExecContext(ctx, `UPDATE paper_orders SET status='rejected', reason=?, reserved=0,
					updated_at=CURRENT_TIMESTAMP WHERE id=?`, ferr.Error(), o.id); err != nil {
					return err
				}
				res.Rejected++
				continue
			}
			if filled {
				res.Filled++
			}
		}
		return nil
	})
}

// ResetAccount resets the holder's account using the current paper parameters.
func (b *Broker) ResetAccount(ctx context.Context, holder string) error {
	return Reset(ctx, b.db, holder, b.params().InitialBalanceUSD)
}

// Reset returns a holder's paper account to initialBalanceUSD: open positions
// are closed, pending orders cancelled and all previous history (orders,
// positions, fills) is kept and flagged closed_by_reset. The operator endpoint
// calls this; the server must refuse it for agent tokens before calling.
func Reset(ctx context.Context, db *sql.DB, holder string, initialBalanceUSD float64) error {
	if holder == "" {
		return newErr(CodeNoHolder, "holder is required")
	}
	if initialBalanceUSD <= 0 {
		return newErr(CodeRejected, "initial balance must be positive")
	}
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return err
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmts := []string{
		`UPDATE paper_orders SET status='cancelled', reason='closed by reset', reserved=0, updated_at=CURRENT_TIMESTAMP
			WHERE holder=? AND status='open'`,
		`UPDATE paper_orders SET closed_by_reset=1 WHERE holder=?`,
		`UPDATE paper_positions SET size=0, margin=0, status='closed', close_reason='reset', closed_at=CURRENT_TIMESTAMP
			WHERE holder=? AND status='open'`,
		`UPDATE paper_positions SET closed_by_reset=1 WHERE holder=?`,
		`UPDATE paper_fills SET closed_by_reset=1 WHERE holder=?`,
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s, holder); err != nil {
			return err
		}
	}
	empty, _ := json.Marshal(map[string]float64{})
	if _, err := tx.ExecContext(ctx, `INSERT INTO paper_accounts (holder, balance, initial_balance, leverage_json, reset_count)
		VALUES (?, ?, ?, ?, 1)
		ON CONFLICT(holder) DO UPDATE SET balance=excluded.balance, initial_balance=excluded.initial_balance,
			leverage_json=excluded.leverage_json, reset_count=paper_accounts.reset_count+1, updated_at=CURRENT_TIMESTAMP`,
		holder, initialBalanceUSD, initialBalanceUSD, string(empty)); err != nil {
		return err
	}
	return tx.Commit()
}

func (b *Broker) saveLeverage(ctx context.Context, q dbq, a *account) error {
	raw, err := json.Marshal(a.leverage)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `UPDATE paper_accounts SET leverage_json=?, updated_at=CURRENT_TIMESTAMP WHERE holder=?`, string(raw), a.holder)
	return err
}
