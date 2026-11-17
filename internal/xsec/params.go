package xsec

import (
	"database/sql"
	"fmt"
)

// Dynamic parameters for the xsec bot, persisted in SQLite so behavior can be
// tuned without redeploying (project rule: no hardcoded tunables). Precedence
// in cmd/xsec-bot: explicit CLI flag > DB param > flag default.
//
// Known keys:
//
//	xsec.lookback_days     momentum lookback in days
//	xsec.k                 basket size per side
//	xsec.min_turnover_usd  universe liquidity floor
//	xsec.cost_frac         round-trip cost fraction
//	xsec.vol_weights       "true"/"false": inverse-vol leg weighting
//	xsec.vol_window        realized-vol window in daily bars
//	xsec.vol_target        annual vol target (0 = off)
//	xsec.vol_halflife      EWMA half-life in days
//	xsec.max_short_weight  per-pair cap on the short leg (0 = off)
//	xsec.funding_tilt      funding tilt weight (0 = off)
//	xsec.band              no-trade band: rank hysteresis in slots (0 = off)
//	xsec.maker_first       "true"/"false": xsec-live post-only maker execution
//	xsec.maker_timeout     xsec-live maker fill wait (Go duration, e.g. "60s")
//	xsec.resize_threshold  xsec-live rebalance size-drift ignore fraction
//	xsec.book_filter       "true"/"false": xsec-live drops foreign (non-xsec-owned)
//	                       positions from the exchange book before rebalance diffing,
//	                       using the xsec_live_book registry (see livebook.go)
//	xsec.rank_buffer       int: extra ranks per side beyond k so untradeable pairs
//	                       (no live futures price, e.g. delisted) backfill with the
//	                       next-ranked name instead of shrinking the basket

// ensureParamsSchema creates the xsec_params table if absent (idempotent).
func ensureParamsSchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS xsec_params (
		key        TEXT PRIMARY KEY,
		value      TEXT NOT NULL,
		updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		return fmt.Errorf("xsec: create xsec_params: %w", err)
	}
	return nil
}

// LoadParams returns all dynamic parameters as a key->value map. Creates the
// table on first use, so a fresh database simply yields an empty map.
func LoadParams(db *sql.DB) (map[string]string, error) {
	if err := ensureParamsSchema(db); err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT key, value FROM xsec_params`)
	if err != nil {
		return nil, fmt.Errorf("xsec: load params: %w", err)
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("xsec: scan param: %w", err)
		}
		out[k] = v
	}
	return out, rows.Err()
}

// SetParam upserts one dynamic parameter.
func SetParam(db *sql.DB, key, value string) error {
	if err := ensureParamsSchema(db); err != nil {
		return err
	}
	_, err := db.Exec(`INSERT INTO xsec_params (key, value, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`,
		key, value)
	if err != nil {
		return fmt.Errorf("xsec: set param %s: %w", key, err)
	}
	return nil
}
