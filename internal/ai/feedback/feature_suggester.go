package feedback

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// FeatureSuggester generates prompts for an LLM to propose new features to
// explore, based on which existing features have the lowest importance and
// which regimes have the lowest signal calibration hit-rate.
//
// This is P4.5: the LLM is NOT a predictor — it reads performance stats and
// proposes hypotheses ("maybe add order-book imbalance for RANGING regimes").
// The owner validates suggestions and implements promising ones.
//
// Usage: FeatureSuggester.BuildPrompt() → feed the prompt to any LLM provider.
// The output is a free-text hypothesis list to be reviewed, not executed.
type FeatureSuggester struct {
	db  *sql.DB
	log *logrus.Logger
}

// NewFeatureSuggester creates a suggester backed by the given SQLite connection.
func NewFeatureSuggester(db *sql.DB, log *logrus.Logger) *FeatureSuggester {
	return &FeatureSuggester{db: db, log: log}
}

// PerformanceStats is the structured context fed to the LLM.
type PerformanceStats struct {
	// Hit rates per source and regime from signal_calibration.
	HitRates []HitRateStat
	// Regimes with persistent underperformance (hit_rate < 0.5 for ≥20 obs).
	UnderperformingRegimes []string
	// Total observations available.
	TotalObservations int
	// Rolling window used (days).
	WindowDays int
}

// HitRateStat is one (source, regime) hit-rate entry.
type HitRateStat struct {
	Source  string
	Regime  string
	HitRate float64
	N       int
}

// CollectStats reads signal_calibration for the last windowDays and returns stats.
func (s *FeatureSuggester) CollectStats(ctx context.Context, windowDays int) (*PerformanceStats, error) {
	since := time.Now().AddDate(0, 0, -windowDays).Unix()
	rows, err := s.db.QueryContext(ctx, `
		SELECT source, regime,
		       SUM(CASE WHEN actual='win' THEN 1 ELSE 0 END) AS wins,
		       COUNT(*) AS n
		FROM signal_calibration
		WHERE ts >= ?
		GROUP BY source, regime
		HAVING n >= 5
		ORDER BY source, regime
	`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stats := &PerformanceStats{WindowDays: windowDays}
	for rows.Next() {
		var src, reg string
		var wins, n int
		if err := rows.Scan(&src, &reg, &wins, &n); err != nil {
			continue
		}
		hr := float64(wins) / float64(n)
		stats.HitRates = append(stats.HitRates, HitRateStat{
			Source: src, Regime: reg, HitRate: hr, N: n,
		})
		stats.TotalObservations += n
		if hr < 0.5 && n >= 20 {
			label := fmt.Sprintf("%s/%s (hr=%.2f, n=%d)", src, reg, hr, n)
			stats.UnderperformingRegimes = append(stats.UnderperformingRegimes, label)
		}
	}
	return stats, rows.Err()
}

// BuildPrompt generates the LLM prompt for feature hypothesis generation.
// Feed the returned string to any LLM provider (Gemini, OpenAI, etc.).
func (s *FeatureSuggester) BuildPrompt(ctx context.Context) (string, error) {
	stats, err := s.CollectStats(ctx, 30)
	if err != nil {
		return "", fmt.Errorf("feature suggester: collect stats: %w", err)
	}
	if stats.TotalObservations < 20 {
		return "", fmt.Errorf("feature suggester: insufficient data (%d obs, need ≥20)",
			stats.TotalObservations)
	}

	var sb strings.Builder
	sb.WriteString(`You are a quantitative trading researcher reviewing the performance of a crypto scalping bot.
The bot uses three signal sources: deterministic technical indicators (RSI, MACD, Bollinger, ATR, EMA20,
volume_ratio, trend_slope, candle microstructure), an ML gradient-boosting predictor (GBM trained on
those same features), and an LLM consensus layer.

Current 30-day hit-rate stats (wins / total trades) per source and market regime:
`)

	for _, h := range stats.HitRates {
		pct := h.HitRate * 100
		quality := "OK"
		if h.HitRate < 0.45 {
			quality = "⚠️ UNDERPERFORMING"
		} else if h.HitRate >= 0.6 {
			quality = "✅ STRONG"
		}
		sb.WriteString(fmt.Sprintf("  %-12s %-16s  hit=%.1f%%  n=%d  %s\n",
			h.Source, h.Regime, pct, h.N, quality))
	}

	if len(stats.UnderperformingRegimes) > 0 {
		sb.WriteString("\nPersistently underperforming (hit < 50%, n≥20):\n")
		for _, r := range stats.UnderperformingRegimes {
			sb.WriteString("  - " + r + "\n")
		}
	}

	sb.WriteString(`
Current feature set:
  rsi, macd_value/signal/histogram, bb_upper/middle/lower/width/position,
  ema20, price_vs_ema, atr, atr_pct, volume, volume_ratio, trend_slope,
  hour_utc, body_pct, upper_wick, lower_wick, bullish (candle direction),
  regime (one-hot: TRENDING_UP/DOWN, RANGING, BREAKOUT, CHAOS, UNKNOWN).

Based on the underperforming source/regime combinations above:
1. Hypothesize 3-5 NEW features that might improve signal quality, especially
   for the weakest regimes. Focus on features extractable from public OHLCV data
   or level-2 order-book data (if available on KuCoin futures).
2. For each feature: explain WHY it might help, how to compute it, and which regime(s)
   it targets. Be specific (e.g. "VWAP deviation over 20 bars for RANGING regimes").
3. Flag any features that are dangerous (look-ahead risk, data leakage) and explain why.
4. Propose one experiment design: which pair, timeframe, and horizon would you test first?

Answer in plain text, concise, actionable. Do not suggest deep learning — the current
infrastructure uses GBM and we want interpretable, low-data features.
`)

	return sb.String(), nil
}

// LogSuggestion logs the LLM's response for review.
// RecordMutationHypothesis writes a hypothesis to the mutation_hypotheses table (BB-5).
// BB-5 two-lane data policy:
//   - source "ranking_verdict": causal feed, eligible to seed a candidate Spec
//   - source "drift_diagnostic": observational only; NEVER a training feature or label
//
// The DB table is the ONLY storage — never use an in-memory map for hypotheses.
func (s *FeatureSuggester) RecordMutationHypothesis(ctx context.Context, source, specID, regime, metricsJSON string) error {
	if source != "ranking_verdict" && source != "drift_diagnostic" {
		return fmt.Errorf("invalid source_signal %q: must be 'ranking_verdict' or 'drift_diagnostic'", source)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO mutation_hypotheses (source_signal, spec_id, regime, metrics_json, status)
		VALUES (?, ?, ?, ?, 'open')
	`, source, specID, regime, metricsJSON)
	if err != nil {
		return fmt.Errorf("record mutation hypothesis: %w", err)
	}
	s.log.WithFields(logrus.Fields{
		"source":  source,
		"spec_id": specID,
		"regime":  regime,
	}).Info("BB-5: mutation hypothesis recorded")
	return nil
}

// IsCausalFeed reports whether source_signal is eligible to feed a candidate Spec.
// Only 'ranking_verdict' is causal; 'drift_diagnostic' is NEVER a training input.
func IsCausalFeed(source string) bool {
	return source == "ranking_verdict"
}

// The suggestion is NOT executed automatically — it must be reviewed by a human.
func (s *FeatureSuggester) LogSuggestion(suggestion string) {
	if s.log != nil {
		s.log.WithField("suggestion_length", len(suggestion)).
			Info("💡 LLM feature hypothesis generated — review in logs")
		// Log at INFO level so it's captured by the audit log but doesn't flood.
		for _, line := range strings.Split(suggestion, "\n") {
			if strings.TrimSpace(line) != "" {
				s.log.Info("[FEATURE_HYPOTHESIS] " + line)
			}
		}
	}
}
