package xsec

import (
	"database/sql"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// FundingTiltScorer wraps an inner Scorer and tilts each pair's score by its
// latest perpetual funding rate:
//
//	adjusted = score - tiltWeight * latestFundingRate(pair)
//
// Rationale: positive funding means longs PAY and shorts RECEIVE. Subtracting
// tiltWeight*rate pushes positive-funding pairs DOWN the rank (less likely to
// be longed, more likely to be shorted) and negative-funding pairs UP, so the
// baskets drift toward funding-receivers on both legs — a free carry overlay
// on top of the momentum signal.
//
// Funding comes from the funding_archive table (pair, ts unix-seconds, rate)
// via direct SQL: the most recent row per pair within the last 3 days (older
// rows are considered stale and ignored). Pairs with no usable funding data
// get adjustment 0. If the table does not exist at all, the scorer degrades
// gracefully: it logs once and returns the inner scores unchanged.
type FundingTiltScorer struct {
	inner      Scorer
	db         *sql.DB
	tiltWeight float64
	log        *logrus.Logger

	logMissingOnce sync.Once
}

// NewFundingTiltScorer wraps inner with a funding-rate tilt of the given
// weight. A nil log falls back to the logrus standard logger.
func NewFundingTiltScorer(inner Scorer, db *sql.DB, tiltWeight float64, log *logrus.Logger) *FundingTiltScorer {
	if log == nil {
		log = logrus.StandardLogger()
	}
	return &FundingTiltScorer{inner: inner, db: db, tiltWeight: tiltWeight, log: log}
}

// Scores implements Scorer.
func (f *FundingTiltScorer) Scores(pairs []string) (map[string]float64, time.Time, error) {
	scores, asOf, err := f.inner.Scores(pairs)
	if err != nil {
		return nil, asOf, err
	}
	if f.tiltWeight == 0 || len(scores) == 0 {
		return scores, asOf, nil
	}
	if !f.fundingTableExists() {
		f.logMissingOnce.Do(func() {
			f.log.Warn("xsec: funding_archive table not found; funding tilt disabled, using raw scores")
		})
		return scores, asOf, nil
	}

	cutoff := time.Now().Add(-3 * 24 * time.Hour).Unix()
	out := make(map[string]float64, len(scores))
	for pair, s := range scores {
		rate, ok := f.latestRate(pair, cutoff)
		if !ok {
			out[pair] = s // no fresh funding data: adjustment 0
			continue
		}
		out[pair] = s - f.tiltWeight*rate
	}
	return out, asOf, nil
}

// fundingTableExists probes sqlite_master for the funding_archive table.
func (f *FundingTiltScorer) fundingTableExists() bool {
	var name string
	err := f.db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='funding_archive'`,
	).Scan(&name)
	return err == nil
}

// latestRate returns the most recent funding rate for pair at or after the
// cutoff unix timestamp, or ok=false when none is fresh enough.
func (f *FundingTiltScorer) latestRate(pair string, cutoff int64) (float64, bool) {
	var rate float64
	err := f.db.QueryRow(
		`SELECT rate FROM funding_archive WHERE pair = ? AND ts >= ? ORDER BY ts DESC LIMIT 1`,
		pair, cutoff,
	).Scan(&rate)
	if err != nil {
		if err != sql.ErrNoRows {
			f.log.WithError(err).WithField("pair", pair).Warn("xsec: funding rate query failed; adjustment 0")
		}
		return 0, false
	}
	return rate, true
}
