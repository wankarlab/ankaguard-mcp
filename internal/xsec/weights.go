package xsec

import (
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/storage"
)

// This file implements evidence-based risk overlays for the cross-sectional
// momentum book (Barroso & Santa-Clara, "Momentum has its moments", JFE 2015):
//   - inverse-volatility weighting inside each leg,
//   - per-pair weight caps with proportional redistribution,
//   - portfolio-level volatility targeting via EWMA realized vol.
// All overlays are optional; equal weights and unit gross scale reproduce the
// validated baseline exactly.

// InverseVolWeights computes per-pair weights proportional to the inverse of
// realized volatility of daily log returns over the trailing `window` bars,
// read from the candle_archive (same source the momentum Ranker uses).
//
// Weights are normalized to sum to 1.0 across the provided pair list. Pairs
// with insufficient history (or degenerate sigma ~ 0, e.g. a flat synthetic
// series) receive the mean inverse-vol of the measurable pairs, i.e. an
// "equal residual" weight close to the average, so they are neither excluded
// nor allowed to dominate. If no pair is measurable, equal weights are
// returned.
func InverseVolWeights(db *sql.DB, timeframe string, pairs []string, window int) (map[string]float64, error) {
	out := make(map[string]float64, len(pairs))
	if len(pairs) == 0 {
		return out, nil
	}
	if window < 2 {
		return nil, fmt.Errorf("xsec: vol window must be >= 2, got %d", window)
	}

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	repo := storage.NewCandleArchiveRepository(db, log)

	invVol := make(map[string]float64, len(pairs))
	var sumInv float64
	for _, p := range pairs {
		candles, err := repo.LoadRange(p, timeframe, time.Time{}, time.Now())
		if err != nil {
			return nil, fmt.Errorf("xsec: load candles for %s: %w", p, err)
		}
		// window log returns require window+1 closes.
		if len(candles) < window+1 {
			continue
		}
		tail := candles[len(candles)-window-1:]
		rets := make([]float64, 0, window)
		valid := true
		for i := 1; i < len(tail); i++ {
			if tail[i-1].Close <= 0 || tail[i].Close <= 0 {
				valid = false
				break
			}
			rets = append(rets, math.Log(tail[i].Close/tail[i-1].Close))
		}
		if !valid {
			continue
		}
		sigma := stddev(rets)
		// Guard sigma -> 0: a (near-)flat series would get near-infinite weight
		// and crowd out everything else; treat it as unmeasurable instead.
		if sigma <= 1e-9 {
			continue
		}
		iv := 1.0 / sigma
		invVol[p] = iv
		sumInv += iv
	}

	if len(invVol) == 0 {
		// Nothing measurable: degrade to equal weights.
		eq := 1.0 / float64(len(pairs))
		for _, p := range pairs {
			out[p] = eq
		}
		return out, nil
	}

	// Pairs without a measurable sigma get the mean inverse-vol of the
	// measurable pairs (equal residual treatment), then everything is
	// normalized to sum 1.0.
	meanInv := sumInv / float64(len(invVol))
	total := 0.0
	for _, p := range pairs {
		iv, ok := invVol[p]
		if !ok {
			iv = meanInv
		}
		out[p] = iv
		total += iv
	}
	for p := range out {
		out[p] /= total
	}
	return out, nil
}

// stddev returns the sample standard deviation of xs (n-1 denominator).
func stddev(xs []float64) float64 {
	n := len(xs)
	if n < 2 {
		return 0
	}
	var mean float64
	for _, x := range xs {
		mean += x
	}
	mean /= float64(n)
	var ss float64
	for _, x := range xs {
		d := x - mean
		ss += d * d
	}
	return math.Sqrt(ss / float64(n-1))
}

// CapWeights caps any single weight at maxW and redistributes the excess
// proportionally among the uncapped pairs, iterating until stable (each pass
// can push a previously-uncapped pair over the cap). If maxW <= 0 the input is
// returned unchanged (cap disabled). If the cap is infeasible
// (len(weights)*maxW < sum), every pair ends at maxW and the total sum drops
// below the original — a deliberate gross-exposure reduction on that leg.
func CapWeights(weights map[string]float64, maxW float64) map[string]float64 {
	out := make(map[string]float64, len(weights))
	for p, w := range weights {
		out[p] = w
	}
	if maxW <= 0 || len(out) == 0 {
		return out
	}

	capped := make(map[string]bool, len(out))
	for iter := 0; iter < len(out)+1; iter++ {
		var excess, uncappedSum float64
		for p, w := range out {
			if capped[p] {
				continue
			}
			if w > maxW {
				excess += w - maxW
				out[p] = maxW
				capped[p] = true
			} else {
				uncappedSum += w
			}
		}
		if excess <= 1e-15 {
			break // stable: nothing above the cap
		}
		if uncappedSum <= 1e-15 {
			break // infeasible: everyone capped, drop the excess
		}
		// Redistribute excess proportionally to the still-uncapped pairs.
		for p, w := range out {
			if capped[p] {
				continue
			}
			out[p] = w + excess*(w/uncappedSum)
		}
	}
	return out
}

// VolTargetScale returns the gross-exposure multiplier that brings the
// portfolio's EWMA realized volatility to targetAnnualVol (e.g. 0.20 = 20%/yr).
// dailyReturns must be ordered oldest-first. The EWMA uses the given half-life
// in days; realized vol is annualized with sqrt(365) (crypto trades daily).
// The scale is clamped to [0.25, 1.5] so a quiet stretch cannot over-lever and
// a panic cannot fully de-risk the book. Returns 1.0 (neutral) with fewer than
// 10 observations or a non-positive target.
func VolTargetScale(dailyReturns []float64, targetAnnualVol float64, halflifeDays float64) float64 {
	if targetAnnualVol <= 0 || len(dailyReturns) < 10 {
		return 1.0
	}
	// EWMA of squared returns (zero-mean assumption, standard for daily vol).
	alpha := 1.0
	if halflifeDays > 0 {
		alpha = 1.0 - math.Pow(0.5, 1.0/halflifeDays)
	}
	ewmaVar := dailyReturns[0] * dailyReturns[0]
	for _, r := range dailyReturns[1:] {
		ewmaVar = (1-alpha)*ewmaVar + alpha*r*r
	}
	realized := math.Sqrt(ewmaVar) * math.Sqrt(365)
	if realized <= 1e-12 {
		return 1.5 // dead-flat history: allow max scale, the clamp bounds it
	}
	scale := targetAnnualVol / realized
	if scale < 0.25 {
		return 0.25
	}
	if scale > 1.5 {
		return 1.5
	}
	return scale
}

// PortfolioDailyReturns derives daily portfolio returns from the xsec_equity
// table (one return per calendar day, using the last equity snapshot of each
// day), returning at most the trailing maxDays observations, oldest first.
func PortfolioDailyReturns(db *sql.DB, maxDays int) ([]float64, error) {
	rows, err := db.Query(`SELECT ts, equity FROM xsec_equity ORDER BY ts ASC`)
	if err != nil {
		return nil, fmt.Errorf("xsec: query equity history: %w", err)
	}
	defer rows.Close()

	// Collapse intraday rows to the last equity per UTC day.
	var days []string
	lastByDay := map[string]float64{}
	for rows.Next() {
		var ts time.Time
		var eq float64
		if err := rows.Scan(&ts, &eq); err != nil {
			return nil, fmt.Errorf("xsec: scan equity row: %w", err)
		}
		day := ts.UTC().Format("2006-01-02")
		if _, seen := lastByDay[day]; !seen {
			days = append(days, day)
		}
		lastByDay[day] = eq
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("xsec: equity rows: %w", err)
	}

	rets := make([]float64, 0, len(days))
	for i := 1; i < len(days); i++ {
		prev := lastByDay[days[i-1]]
		cur := lastByDay[days[i]]
		if prev <= 0 {
			continue
		}
		rets = append(rets, cur/prev-1)
	}
	if maxDays > 0 && len(rets) > maxDays {
		rets = rets[len(rets)-maxDays:]
	}
	return rets, nil
}
