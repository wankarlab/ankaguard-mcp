// Package feedback implements the post-trade feedback loop (P3).
//
// After each trade closes, the Labeler:
//  1. Finds the feature_store row closest to the entry timestamp for this pair.
//  2. Updates it with the real outcome (hitTP, hitSL, fwdReturn, direction).
//  3. Records one observation per signal source (technical, llm, ml) in
//     signal_calibration so the Calibrator can update Bayesian weights.
//
// This closes the ML training loop: the feature_store accumulates ground-truth
// labels from live trading, complementing the synthetic forward labels from the
// batch labeler (cmd/build-dataset).
package feedback

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// TradeOutcome carries the closed-trade data the Labeler needs.
type TradeOutcome struct {
	Pair       string
	Exchange   string
	Side       string // BUY | SELL
	EntryPrice float64
	ExitPrice  float64
	EntryTime  time.Time
	ExitTime   time.Time
	PnLPct     float64 // net signed return %
	ExitReason string  // stop_loss | take_profit | manual | timeout
	Confidence float64 // combined confidence at entry [0,1]
	Regime     string  // market regime at entry (from RegimeDetector)
	MLProb     float64 // ML model probability at entry (0 if not available)

	// Per-source vote attribution (Active Profit Engine F0). When set, the
	// labeler records distinct technical vs llm calibration observations instead
	// of crediting both with the combined side (which made them collinear).
	// Empty vote falls back to the combined side; zero confidence falls back to
	// the combined Confidence.
	TechnicalVote string
	LLMVote       string
	TechnicalConf float64
	LLMConf       float64
}

// Labeler writes post-trade ground-truth labels to feature_store and records
// signal_calibration observations for Bayesian weight updates.
type Labeler struct {
	db  *sql.DB
	log *logrus.Logger
}

// NewLabeler creates a Labeler backed by the given SQLite connection.
func NewLabeler(db *sql.DB, log *logrus.Logger) *Labeler {
	return &Labeler{db: db, log: log}
}

// Label is called after each trade closes (in a goroutine — must not block).
// It updates feature_store and records signal_calibration in a single transaction.
func (l *Labeler) Label(ctx context.Context, o *TradeOutcome) {
	if o == nil {
		return
	}
	if err := l.label(ctx, o); err != nil {
		l.log.WithError(err).WithFields(logrus.Fields{
			"pair":      o.Pair,
			"exit_time": o.ExitTime.Format(time.RFC3339),
		}).Warn("feedback: label failed")
	}
}

func (l *Labeler) label(ctx context.Context, o *TradeOutcome) error {
	hitTP := o.ExitReason == "take_profit"
	hitSL := o.ExitReason == "stop_loss"
	direction := "flat"
	switch {
	case o.PnLPct > 0.1:
		direction = "up"
	case o.PnLPct < -0.1:
		direction = "down"
	}
	hitTPInt, hitSLInt := 0, 0
	if hitTP {
		hitTPInt = 1
	}
	if hitSL {
		hitSLInt = 1
	}

	entryTS := o.EntryTime.Unix()

	// Find the feature_store row nearest to (pair, entryTS) within ±10 minutes.
	// We update the row closest in time to the entry rather than requiring exact ts match,
	// because the archive may have slightly different cadences than live tick timestamps.
	var rowTS int64
	err := l.db.QueryRowContext(ctx, `
		SELECT ts FROM feature_store
		WHERE pair = ? AND abs(ts - ?) <= 600
		ORDER BY abs(ts - ?) ASC
		LIMIT 1
	`, o.Pair, entryTS, entryTS).Scan(&rowTS)

	if err == sql.ErrNoRows {
		// No feature row near entry — happens when the pair hasn't been backfilled.
		// Still record the calibration observation (no feature_store update needed).
		l.log.WithFields(logrus.Fields{
			"pair":     o.Pair,
			"entry_ts": entryTS,
		}).Debug("feedback: no feature_store row near entry, skipping label update")
	} else if err != nil {
		return fmt.Errorf("feature_store lookup: %w", err)
	} else {
		// Update real outcome labels on the nearest feature_store row.
		_, err = l.db.ExecContext(ctx, `
			UPDATE feature_store SET
				label_hit_tp      = ?,
				label_hit_sl      = ?,
				label_fwd_return  = ?,
				label_direction   = ?
			WHERE pair = ? AND ts = ?
		`, hitTPInt, hitSLInt, o.PnLPct, direction, o.Pair, rowTS)
		if err != nil {
			return fmt.Errorf("feature_store update: %w", err)
		}
	}

	// Record calibration observations for each source.
	// "predicted" is the direction the source voted for; "actual" is the outcome.
	// We only have the combined confidence at entry, so all sources share it.
	// A more granular version would track per-source confidence (P3 enhancement).
	actual := outcome(o.PnLPct, hitTP, hitSL)
	regime := o.Regime
	if regime == "" {
		regime = "UNKNOWN"
	}

	// Per-source votes captured at entry (Active Profit Engine F0). Technical and
	// llm now record their OWN vote/confidence so the calibrator can distinguish
	// them; before this they shared the combined side and were collinear. Each
	// falls back to the combined side / confidence when not captured.
	techVote := o.TechnicalVote
	if techVote == "" {
		techVote = predictedAction(o.Side)
	}
	llmVote := o.LLMVote
	if llmVote == "" {
		llmVote = predictedAction(o.Side)
	}
	techConf := o.TechnicalConf
	if techConf == 0 {
		techConf = o.Confidence
	}
	llmConf := o.LLMConf
	if llmConf == 0 {
		llmConf = o.Confidence
	}

	sources := []struct {
		name    string
		predict string
		conf    float64
	}{
		{"technical", techVote, techConf},
		{"llm", llmVote, llmConf},
		{"ml", mlAction(o.MLProb), o.Confidence},
	}
	for _, s := range sources {
		if _, err := l.db.ExecContext(ctx, `
			INSERT INTO signal_calibration
			  (source, regime, pair, predicted, actual, confidence, pnl_pct, ts)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, s.name, regime, o.Pair, s.predict, actual, s.conf, o.PnLPct, entryTS); err != nil {
			l.log.WithError(err).WithField("source", s.name).Warn("feedback: calibration insert failed")
		}
	}

	l.log.WithFields(logrus.Fields{
		"pair":      o.Pair,
		"exit":      o.ExitReason,
		"pnl_pct":   o.PnLPct,
		"direction": direction,
		"regime":    regime,
	}).Debug("📊 feedback: trade labeled")
	return nil
}

func predictedAction(side string) string {
	switch side {
	case "SELL", "SHORT":
		return "SELL"
	default:
		return "BUY"
	}
}

// mlActionThreshold is the probability threshold mapping the ML output to
// BUY/SELL/WAIT. Default 0.55 mirrors cmd/ml-server/server.py — it MUST stay in
// sync with the server. Tunable via SetMLActionThreshold (dynamic config:
// ai.feedback.ml_action_threshold). Audit P1: previously a hardcoded const.
var (
	mlActionMu        sync.RWMutex
	mlActionThreshold = 0.55
)

// SetMLActionThreshold overrides the ML action threshold. Values outside (0.5,1.0)
// are ignored so a missing/invalid dynamic config keeps the 0.55 default.
// WARNING: this must be kept consistent with cmd/ml-server/server.py.
func SetMLActionThreshold(v float64) {
	if v <= 0.5 || v >= 1.0 {
		return
	}
	mlActionMu.Lock()
	mlActionThreshold = v
	mlActionMu.Unlock()
}

// mlAction converts the raw ML probability to the predicted action.
// Matches the logic in cmd/ml-server/server.py.
func mlAction(prob float64) string {
	mlActionMu.RLock()
	threshold := mlActionThreshold
	mlActionMu.RUnlock()
	switch {
	case prob >= threshold:
		return "BUY"
	case prob > 0 && prob <= (1-threshold):
		return "SELL"
	default:
		return "WAIT"
	}
}

// outcome maps trade result to the label stored in signal_calibration.
func outcome(pnlPct float64, hitTP, hitSL bool) string {
	if hitTP || pnlPct > 0.05 {
		return "win"
	}
	if hitSL || pnlPct < -0.05 {
		return "loss"
	}
	return "flat"
}
