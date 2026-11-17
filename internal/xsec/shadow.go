package xsec

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Shadow basket recording: every rebalance, each candidate scorer (e.g.
// "momentum_lb14" and "ltr") records the baskets it WOULD have traded.
// Comparing realized next-week spreads of shadow baskets is how the LTR
// model earns (or fails to earn) promotion to live.

// ShadowRecord is one recorded shadow basket selection.
type ShadowRecord struct {
	Ts     time.Time
	Scorer string
	Longs  []string
	Shorts []string
	Scores map[string]float64
}

// EnsureShadowSchema creates the xsec_shadow_baskets table if it does not
// exist. Idempotent — safe to call on every startup.
func EnsureShadowSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS xsec_shadow_baskets (
			ts          TIMESTAMP NOT NULL,
			scorer      TEXT      NOT NULL,
			longs       TEXT      NOT NULL, -- csv of pairs
			shorts      TEXT      NOT NULL, -- csv of pairs
			scores_json TEXT      NOT NULL,
			PRIMARY KEY (ts, scorer)
		)`)
	if err != nil {
		return fmt.Errorf("create xsec_shadow_baskets: %w", err)
	}
	return nil
}

// RecordShadow upserts the baskets a scorer selected at ts. Idempotent on
// (ts, scorer) so a retried rebalance does not duplicate rows.
func RecordShadow(db *sql.DB, ts time.Time, scorerName string, baskets Baskets, scores map[string]float64) error {
	if err := EnsureShadowSchema(db); err != nil {
		return err
	}
	sj, err := json.Marshal(scores)
	if err != nil {
		return fmt.Errorf("marshal scores: %w", err)
	}
	_, err = db.Exec(`
		INSERT INTO xsec_shadow_baskets (ts, scorer, longs, shorts, scores_json)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(ts, scorer) DO UPDATE SET
			longs = excluded.longs,
			shorts = excluded.shorts,
			scores_json = excluded.scores_json`,
		ts.UTC(), scorerName,
		strings.Join(baskets.Longs, ","),
		strings.Join(baskets.Shorts, ","),
		string(sj))
	if err != nil {
		return fmt.Errorf("insert shadow basket: %w", err)
	}
	return nil
}

// ShadowHistory returns the most recent shadow records (newest first),
// up to limit (limit <= 0 means 100).
func ShadowHistory(db *sql.DB, limit int) ([]ShadowRecord, error) {
	if err := EnsureShadowSchema(db); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.Query(`
		SELECT ts, scorer, longs, shorts, scores_json
		FROM xsec_shadow_baskets
		ORDER BY ts DESC, scorer ASC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("query shadow baskets: %w", err)
	}
	defer rows.Close()

	var out []ShadowRecord
	for rows.Next() {
		var (
			rec           ShadowRecord
			longs, shorts string
			scoresJSON    string
		)
		if err := rows.Scan(&rec.Ts, &rec.Scorer, &longs, &shorts, &scoresJSON); err != nil {
			return nil, fmt.Errorf("scan shadow basket: %w", err)
		}
		rec.Longs = splitCSV(longs)
		rec.Shorts = splitCSV(shorts)
		if err := json.Unmarshal([]byte(scoresJSON), &rec.Scores); err != nil {
			return nil, fmt.Errorf("unmarshal scores for %s@%s: %w", rec.Scorer, rec.Ts, err)
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// ShadowScorers returns the distinct scorer names present in the shadow table.
func ShadowScorers(db *sql.DB) ([]string, error) {
	if err := EnsureShadowSchema(db); err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT DISTINCT scorer FROM xsec_shadow_baskets ORDER BY scorer`)
	if err != nil {
		return nil, fmt.Errorf("query scorers: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ShadowEquityPoint is one settled period of a scorer's paper equity curve.
type ShadowEquityPoint struct {
	Ts        time.Time `json:"ts"`
	Equity    float64   `json:"equity"`      // starts at 1.0, compounds
	PeriodRet float64   `json:"period_ret"`  // net return realized this period
	NLong     int       `json:"n_long"`
	NShort    int       `json:"n_short"`
}

// ShadowEquity is the forward paper evaluation of one scorer.
type ShadowEquity struct {
	Scorer    string              `json:"scorer"`
	Curve     []ShadowEquityPoint `json:"curve"`
	TotalRet  float64             `json:"total_return"`  // net, after cost
	GrossRet  float64             `json:"gross_return"`  // before turnover cost
	NPeriods  int                 `json:"n_periods"`
	AvgRet    float64             `json:"avg_period_ret"`
	LastTs    time.Time           `json:"last_ts"`
}

// EvalShadowEquity turns a scorer's recorded selections into a realized,
// forward, market-neutral paper equity curve — the evaluation the shadow
// table was always meant to feed but never had a reader for. For each
// consecutive pair of recorded baskets it charges the realized long-short
// spread (each leg half the book, equal-weight) between the two dates using
// candle_archive closes, minus a round-trip turnover cost on the fraction of
// names that rotated. Missing closes drop that name from its leg for that
// period (no look-ahead, no fabrication). roundTripCost is the per-rotated-name
// cost fraction (e.g. 0.0012); pass 0 for the gross curve.
func EvalShadowEquity(db *sql.DB, scorer, tf string, roundTripCost float64) (ShadowEquity, error) {
	if err := EnsureShadowSchema(db); err != nil {
		return ShadowEquity{}, err
	}
	rows, err := db.Query(`
		SELECT ts, longs, shorts FROM xsec_shadow_baskets
		WHERE scorer = ? ORDER BY ts ASC`, scorer)
	if err != nil {
		return ShadowEquity{}, fmt.Errorf("query scorer %s: %w", scorer, err)
	}
	type rec struct {
		ts             time.Time
		longs, shorts  []string
	}
	var recs []rec
	for rows.Next() {
		var ts time.Time
		var l, s string
		if err := rows.Scan(&ts, &l, &s); err != nil {
			rows.Close()
			return ShadowEquity{}, err
		}
		recs = append(recs, rec{ts: ts, longs: splitCSV(l), shorts: splitCSV(s)})
	}
	rows.Close()

	out := ShadowEquity{Scorer: scorer, Curve: []ShadowEquityPoint{}}
	if len(recs) == 0 {
		return out, nil
	}
	out.LastTs = recs[len(recs)-1].ts

	// avgLegReturn averages c(next)/c(now)-1 over the pairs whose closes both
	// exist; skips pairs missing a bar (returns 0 members → 0 contribution).
	avgLegReturn := func(pairs []string, now, next time.Time) float64 {
		sum, n := 0.0, 0
		for _, p := range pairs {
			c0, ok0 := closeAt(db, p, tf, now)
			c1, ok1 := closeAt(db, p, tf, next)
			if !ok0 || !ok1 || c0 <= 0 {
				continue
			}
			sum += c1/c0 - 1
			n++
		}
		if n == 0 {
			return 0
		}
		return sum / float64(n)
	}
	jaccardTurnover := func(prevL, prevS, curL, curS []string) float64 {
		if prevL == nil && prevS == nil {
			return 1.0 // initial entry: whole book is a fresh rotation
		}
		inSet := func(xs []string) map[string]bool {
			m := map[string]bool{}
			for _, x := range xs {
				m[x] = true
			}
			return m
		}
		pl, ps := inSet(prevL), inSet(prevS)
		changed, total := 0, len(curL)+len(curS)
		for _, x := range curL {
			if !pl[x] {
				changed++
			}
		}
		for _, x := range curS {
			if !ps[x] {
				changed++
			}
		}
		if total == 0 {
			return 0
		}
		return float64(changed) / float64(total)
	}

	equity, grossEq := 1.0, 1.0
	var retSum float64
	for i := 0; i+1 < len(recs); i++ {
		now, next := recs[i], recs[i+1]
		longRet := avgLegReturn(now.longs, now.ts, next.ts)
		shortRet := avgLegReturn(now.shorts, now.ts, next.ts)
		gross := 0.5*longRet - 0.5*shortRet // each leg = half the book
		var prevL, prevS []string
		if i > 0 {
			prevL, prevS = recs[i-1].longs, recs[i-1].shorts
		}
		cost := jaccardTurnover(prevL, prevS, now.longs, now.shorts) * roundTripCost
		net := gross - cost
		equity *= 1 + net
		grossEq *= 1 + gross
		retSum += net
		out.Curve = append(out.Curve, ShadowEquityPoint{
			Ts: next.ts, Equity: equity, PeriodRet: net,
			NLong: len(now.longs), NShort: len(now.shorts),
		})
	}
	out.NPeriods = len(out.Curve)
	out.TotalRet = equity - 1
	out.GrossRet = grossEq - 1
	if out.NPeriods > 0 {
		out.AvgRet = retSum / float64(out.NPeriods)
	}
	return out, nil
}

// closeAt returns the close of pair at or before ts (nearest prior daily bar),
// and whether one was found. ts is matched against candle_archive.timestamp
// (unix seconds).
func closeAt(db *sql.DB, pair, tf string, ts time.Time) (float64, bool) {
	var c float64
	err := db.QueryRow(`
		SELECT close FROM candle_archive
		WHERE pair = ? AND timeframe = ? AND timestamp <= ?
		ORDER BY timestamp DESC LIMIT 1`,
		pair, tf, ts.UTC().Unix()).Scan(&c)
	if err != nil {
		return 0, false
	}
	return c, true
}
