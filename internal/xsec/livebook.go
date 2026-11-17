package xsec

import (
	"database/sql"
	"fmt"
)

// xsec_live_book is the local ownership registry that lets two independent
// trading systems (the AI scalper bot and the xsec-live basket trader) share
// one KuCoin futures account without netting against each other. KuCoin
// futures run in one-way mode and gct.Position carries no clientOid, so there
// is no exchange-side way to tell "whose" a position is — this table is the
// only source of truth.
//
// Ownership contract:
//   - cmd/xsec-live is the ONLY writer: it upserts a row when it opens or
//     resizes a position, and marks it closed when it closes one.
//   - internal/services (the AI scalper bot) is a READ-ONLY consumer: it must
//     never adopt, trade, or otherwise touch a pair while it has an open row
//     here — see ActiveLiveBookPairs.
//
// Table lives in the same SQLite DB as everything else in this package
// (trading.db), created lazily and idempotently on first use, same pattern
// as xsec_params in params.go.

// ensureLiveBookSchema creates the xsec_live_book table if absent (idempotent)
// and backfills entry_price/notional_usd onto pre-existing installs.
//
// Those two columns did not exist in the original schema — GetStatus
// (internal/api/v1/handlers/xsec_handler.go) always rendered Entry/Notional
// as the Go zero value (0), which the dashboard displayed as a literal
// "$0.00" for every leg regardless of the real position size (found
// 2026-07-23: the basket status panel always showed 0, not because of a
// stale/dead endpoint but because these fields were structurally never
// populated). SQLite has no `ADD COLUMN IF NOT EXISTS`, so the migration
// checks pragma table_info first — safe to run on every startup.
func ensureLiveBookSchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS xsec_live_book (
		pair         TEXT PRIMARY KEY,
		dir          INTEGER NOT NULL,
		status       TEXT NOT NULL DEFAULT 'open',
		opened_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		entry_price  REAL NOT NULL DEFAULT 0,
		notional_usd REAL NOT NULL DEFAULT 0
	)`)
	if err != nil {
		return fmt.Errorf("xsec: create xsec_live_book: %w", err)
	}
	if err := addColumnIfMissing(db, "xsec_live_book", "entry_price", "REAL NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	return addColumnIfMissing(db, "xsec_live_book", "notional_usd", "REAL NOT NULL DEFAULT 0")
}

// addColumnIfMissing runs `ALTER TABLE ... ADD COLUMN` only when the column
// is absent from the table's current pragma table_info — the idempotent
// substitute SQLite lacks natively.
func addColumnIfMissing(db *sql.DB, table, column, ddl string) error {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return fmt.Errorf("xsec: inspect %s schema: %w", table, err)
	}
	defer rows.Close()

	found := false
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			return fmt.Errorf("xsec: scan %s pragma row: %w", table, err)
		}
		if name == column {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("xsec: read %s pragma: %w", table, err)
	}
	if found {
		return nil
	}
	if _, err := db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column, ddl)); err != nil {
		return fmt.Errorf("xsec: add column %s.%s: %w", table, column, err)
	}
	return nil
}

// EnsureLiveBookSchema creates the xsec_live_book table if absent (idempotent).
// Exported so callers outside this package (e.g. the scalper bot, which only
// reads the table) can guarantee it exists before querying.
func EnsureLiveBookSchema(db *sql.DB) error {
	return ensureLiveBookSchema(db)
}

// UpsertLiveBookEntry records pair as owned by xsec-live, holding direction
// dir (+1 long, -1 short) at entryPrice with notionalUSD dollar size. Called
// after every successfully confirmed open, resize, or flip. Overwriting an
// existing row (e.g. on a flip's re-entry, or a resize that keeps the same
// direction) is intentional — entryPrice/notionalUSD are overwritten too, so
// a resize always reflects the position's current size, not its original one.
//
// entryPrice/notionalUSD may legitimately be 0 at call sites that genuinely
// don't have a confirmed number yet (see recordLiveBookAfterOrder's
// close-flip re-entry path) — callers should pass the best real number they
// have (order.price/order.notionalUSD, or the post-fill exchange readback)
// rather than leaving this 0 by default, since 0 is indistinguishable here
// from "confirmed zero" and renders as a literal $0.00 on the dashboard.
func UpsertLiveBookEntry(db *sql.DB, pair string, dir int, entryPrice, notionalUSD float64) error {
	if err := ensureLiveBookSchema(db); err != nil {
		return err
	}
	_, err := db.Exec(`INSERT INTO xsec_live_book (pair, dir, status, opened_at, updated_at, entry_price, notional_usd)
		VALUES (?, ?, 'open', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, ?, ?)
		ON CONFLICT(pair) DO UPDATE SET
			dir = excluded.dir,
			status = 'open',
			updated_at = CURRENT_TIMESTAMP,
			entry_price = excluded.entry_price,
			notional_usd = excluded.notional_usd`,
		pair, dir, entryPrice, notionalUSD)
	if err != nil {
		return fmt.Errorf("xsec: upsert live-book entry %s: %w", pair, err)
	}
	return nil
}

// CloseLiveBookEntry marks pair as closed (no longer owned by xsec-live). A
// no-op — not an error — when no row exists for pair, since a close call on
// a pair that was never recorded (e.g. registry seeded after the fact) is
// expected during rollout.
func CloseLiveBookEntry(db *sql.DB, pair string) error {
	if err := ensureLiveBookSchema(db); err != nil {
		return err
	}
	_, err := db.Exec(`UPDATE xsec_live_book SET status = 'closed', updated_at = CURRENT_TIMESTAMP WHERE pair = ?`, pair)
	if err != nil {
		return fmt.Errorf("xsec: close live-book entry %s: %w", pair, err)
	}
	return nil
}

// ActiveLiveBookPairs returns pair -> dir for every row currently marked
// 'open' — the set of positions xsec-live owns right now. Consumers (the
// scalper bot's adoption/entry guards, xsec-live's own foreign-position
// filter) treat this as the authoritative "hands off" list.
func ActiveLiveBookPairs(db *sql.DB) (map[string]int, error) {
	if err := ensureLiveBookSchema(db); err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT pair, dir FROM xsec_live_book WHERE status = 'open'`)
	if err != nil {
		return nil, fmt.Errorf("xsec: load live-book pairs: %w", err)
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var pair string
		var dir int
		if err := rows.Scan(&pair, &dir); err != nil {
			return nil, fmt.Errorf("xsec: scan live-book row: %w", err)
		}
		out[pair] = dir
	}
	return out, rows.Err()
}
