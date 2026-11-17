package feedback

import (
	"context"
	"database/sql"
	"math"
	"time"

	"github.com/sirupsen/logrus"
)

// Calibrator reads signal_calibration observations and maintains Bayesian
// hit-rate estimates per (source, regime). It writes the updated effective
// weights back to signal_source_weights so the bot can read them at runtime.
//
// Bayesian update rule (Beta-Binomial conjugate):
//
//	Prior: Beta(α₀, β₀) where α₀=β₀=2 (weak symmetric prior, 50% hit-rate)
//	After N wins and M losses: α = α₀+N, β = β₀+M
//	Posterior mean (hit-rate): α / (α + β)
//	Weight normalisation: weights across sources sum to 1.0 per regime.
//
// A source with a rolling hit-rate below 0.5 for ≥30 observations sees its
// weight decay toward 0 — effectively auto-disabling underperforming sources
// without a hard threshold that could cause sudden regime changes.
type Calibrator struct {
	db        *sql.DB
	log       *logrus.Logger
	window    int     // lookback window in days (default 30)
	minObs    int     // min observations per (source, regime) before leaving the prior (default 10)
	bootstrap bool    // seed the per-regime prior from feature_store when signal_calibration is thin
	alpha0    float64 // Beta-Binomial prior alpha (default 2.0; dynamic: ai.feedback.calibrator.alpha0)
	beta0     float64 // Beta-Binomial prior beta  (default 2.0; dynamic: ai.feedback.calibrator.beta0)
}

// NewCalibrator returns a Calibrator with a 30-day rolling window.
func NewCalibrator(db *sql.DB, log *logrus.Logger) *Calibrator {
	return &Calibrator{db: db, log: log, window: 30, minObs: 10, bootstrap: true, alpha0: 2.0, beta0: 2.0}
}

// SetPrior overrides the Beta-Binomial prior (alpha0, beta0). Non-positive values
// are ignored so a missing dynamic config keeps the weak symmetric default (2,2).
// Dynamic config: ai.feedback.calibrator.alpha0 / .beta0 (audit P1).
func (c *Calibrator) SetPrior(alpha0, beta0 float64) {
	if alpha0 > 0 {
		c.alpha0 = alpha0
	}
	if beta0 > 0 {
		c.beta0 = beta0
	}
}

// SetMinObs overrides the minimum observations per (source, regime) required
// before the Bayesian estimate replaces the prior (dynamic config:
// feedback.calibrator.min_obs_per_regime).
func (c *Calibrator) SetMinObs(n int) {
	if n > 0 {
		c.minObs = n
	}
}

// SetBootstrap toggles seeding the per-regime prior from feature_store
// (feedback.calibrator.bootstrap_from_feature_store).
func (c *Calibrator) SetBootstrap(on bool) { c.bootstrap = on }

// bootstrapHitRates returns the per-regime TP base rate derived from the labeled
// feature_store. This breaks the "everything is 0.33 because sample_n=0" inertia
// by giving the technical/ml priors a regime-aware starting point until enough
// live signal_calibration observations accumulate.
func (c *Calibrator) bootstrapHitRates(ctx context.Context) map[string]float64 {
	out := map[string]float64{}
	if !c.bootstrap {
		return out
	}
	rows, err := c.db.QueryContext(ctx, `
		SELECT regime, AVG(label_hit_tp) AS hr, COUNT(*) AS n
		FROM feature_store
		WHERE label_direction != '' AND regime IS NOT NULL AND regime != ''
		GROUP BY regime
	`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var reg string
		var hr float64
		var n int
		if err := rows.Scan(&reg, &hr, &n); err == nil && n >= 100 {
			out[reg] = hr
		}
	}
	return out
}

// WeightFor returns the current effective weight for (source, regime).
// Falls back to the default (0.33 for technical/llm, 0.10 for ml) if no data.
func (c *Calibrator) WeightFor(ctx context.Context, source, regime string) float64 {
	if regime == "" {
		regime = "UNKNOWN"
	}
	var w float64
	err := c.db.QueryRowContext(ctx,
		`SELECT weight FROM signal_source_weights WHERE source=? AND regime=?`,
		source, regime,
	).Scan(&w)
	if err != nil || w <= 0 {
		return defaultWeight(source)
	}
	return w
}

// Recalibrate recomputes hit-rates from recent observations and updates
// signal_source_weights. Safe to call periodically (e.g. every 30 min).
func (c *Calibrator) Recalibrate(ctx context.Context) error {
	since := time.Now().AddDate(0, 0, -c.window).Unix()

	// Fetch win/loss counts per (source, regime) from the rolling window.
	rows, err := c.db.QueryContext(ctx, `
		SELECT source, regime,
		       SUM(CASE WHEN actual='win'  THEN 1 ELSE 0 END) AS wins,
		       SUM(CASE WHEN actual='loss' THEN 1 ELSE 0 END) AS losses,
		       COUNT(*) AS n
		FROM signal_calibration
		WHERE ts >= ?
		GROUP BY source, regime
	`, since)
	if err != nil {
		return err
	}
	defer rows.Close()

	// Beta-binomial posterior mean per (source, regime).
	type key struct{ source, regime string }
	hitRates := map[key]struct {
		hr float64
		n  int
	}{}
	for rows.Next() {
		var src, reg string
		var wins, losses, n int
		if err := rows.Scan(&src, &reg, &wins, &losses, &n); err != nil {
			continue
		}
		// Weak symmetric prior (default 2,2); tunable via SetPrior / dynamic config.
		alpha0, beta0 := c.alpha0, c.beta0
		if alpha0 <= 0 {
			alpha0 = 2.0
		}
		if beta0 <= 0 {
			beta0 = 2.0
		}
		alpha := alpha0 + float64(wins)
		beta := beta0 + float64(losses)
		hr := alpha / (alpha + beta)
		hitRates[key{src, reg}] = struct {
			hr float64
			n  int
		}{hr, n}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// Normalise weights per regime so they sum to 1.0.
	// If a source has insufficient data (< minObs), fall back to its prior — but
	// seed technical/ml from the feature_store regime base-rate when available so
	// the weights are regime-aware instead of a flat 0.33 (breaks the inertia).
	sources := []string{"technical", "llm", "ml"}
	regimes := []string{"TRENDING_UP", "TRENDING_DOWN", "RANGING", "BREAKOUT", "CHAOS", "UNKNOWN"}
	bootHR := c.bootstrapHitRates(ctx)

	for _, reg := range regimes {
		rawWeights := make(map[string]float64)
		for _, src := range sources {
			entry, ok := hitRates[key{src, reg}]
			if !ok || entry.n < c.minObs {
				// Prior. Use the feature_store regime base-rate for technical/ml
				// (the GBM/technical target), LLM keeps its flat default.
				if hr, has := bootHR[reg]; has && src != "llm" {
					rawWeights[src] = math.Max(hr-0.5, 0.01)
				} else {
					rawWeights[src] = defaultWeight(src)
				}
				continue
			}
			// Weight proportional to hit-rate (above 0.5 baseline).
			// Sources below 0.5 get a small positive weight (never 0) to keep
			// the posterior updating.
			excess := math.Max(entry.hr-0.5, 0.01)
			rawWeights[src] = excess
		}

		// Normalise.
		total := 0.0
		for _, w := range rawWeights {
			total += w
		}
		for _, src := range sources {
			w := rawWeights[src]
			if total > 0 {
				w = w / total
			}
			entry := hitRates[key{src, reg}]
			_, err := c.db.ExecContext(ctx, `
				INSERT INTO signal_source_weights (source, regime, weight, hit_rate, sample_n, updated_at)
				VALUES (?, ?, ?, ?, ?, strftime('%s','now'))
				ON CONFLICT(source, regime) DO UPDATE SET
				  weight=excluded.weight, hit_rate=excluded.hit_rate,
				  sample_n=excluded.sample_n, updated_at=excluded.updated_at
			`, src, reg, w, entry.hr, entry.n)
			if err != nil && c.log != nil {
				c.log.WithError(err).WithField("src", src).Warn("calibrator: weight update failed")
			}
		}

		if c.log != nil {
			c.log.WithFields(logrus.Fields{
				"regime":    reg,
				"technical": rawWeights["technical"],
				"llm":       rawWeights["llm"],
				"ml":        rawWeights["ml"],
			}).Debug("📊 calibrator: weights updated")
		}
	}
	return nil
}

// StartLoop runs Recalibrate every interval in the background.
// Cancel ctx to stop.
func (c *Calibrator) StartLoop(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := c.Recalibrate(ctx); err != nil {
					c.log.WithError(err).Warn("calibrator: recalibration error")
				}
			}
		}
	}()
}

func defaultWeight(source string) float64 {
	if source == "ml" {
		return 0.10
	}
	return 0.33
}

// MLWeightFor returns the Bayesian-calibrated weight for the ML voter in the
// given regime. Satisfies the mlWeightStore interface in internal/services.
// Uses a background context with 50ms timeout (non-blocking).
func (c *Calibrator) MLWeightFor(regime string) float64 {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	return c.WeightFor(ctx, "ml", regime)
}

// SourceWeightFor returns the Bayesian-calibrated weight for an arbitrary signal
// source (technical/llm/ml/regime → mapped as registered) in the given regime.
// Non-blocking (50ms timeout). Used by the exit-decision fusion when
// bot.exit.fusion.use_calibrator_weights is enabled. Satisfies the
// sourceWeightStore interface in internal/services.
func (c *Calibrator) SourceWeightFor(source, regime string) float64 {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	return c.WeightFor(ctx, source, regime)
}
