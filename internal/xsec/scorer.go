package xsec

import (
	"sort"
	"time"
)

// Scorer assigns a cross-sectional score to each pair as of the latest data.
// Higher score = stronger long candidate; lower = stronger short candidate.
// Implementations: MomentumScorer (baseline), LTRScorer (ML ranking), and
// wrappers such as FundingTiltScorer that adjust an inner scorer's output.
type Scorer interface {
	// Scores returns pair->score for the pairs it can score (pairs lacking
	// data are omitted) and the as-of timestamp of the data used.
	Scores(pairs []string) (map[string]float64, time.Time, error)
}

// Target is a fully specified rebalance target: which pairs on each side,
// optional per-pair weights, and a gross-exposure scale (vol targeting).
type Target struct {
	Baskets
	// LongWeights/ShortWeights are fractions of one leg's capital per pair
	// (each side should sum to ~1.0). Nil means equal weight.
	LongWeights  map[string]float64
	ShortWeights map[string]float64
	// GrossScale scales total gross exposure (1.0 = fully invested at the
	// portfolio's standard gross). Used by volatility targeting. 0 means 1.0.
	GrossScale float64
}

// BuildBaskets sorts scores descending and returns the top-k as Longs and
// bottom-k as Shorts. Returns an empty Baskets if fewer than 2k pairs scored.
//
// band implements the no-trade band (rank hysteresis) validated in
// cmd/xsec-sim/main.go (F1 of PLAN_IMPLEMENTACION_XSEC_EJECUCION_2026-06-30.md:
// band=2 improved Sharpe 2.49->2.63, cut turnover 57%->51%, held up OOS). When
// band > 0, a pair already held on a side (per prevLong/prevShort) is kept as
// long as its rank stays within the top/bottom k+band; a fresh candidate only
// displaces it once the incumbent falls outside that buffer. band <= 0 (or
// nil prevLong/prevShort) reproduces the original strict top-k/bottom-k
// selection exactly — this is the regression-safety contract every caller
// relies on when the band feature is off.
func BuildBaskets(scores map[string]float64, k int, asOf time.Time, band int, prevLong, prevShort map[string]bool) Baskets {
	if k < 1 || len(scores) < 2*k {
		return Baskets{AsOf: asOf}
	}
	type ps struct {
		pair  string
		score float64
	}
	ranked := make([]ps, 0, len(scores))
	for p, s := range scores {
		ranked = append(ranked, ps{p, s})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].pair < ranked[j].pair // deterministic tie-break
	})

	if band <= 0 {
		longs := make([]string, 0, k)
		for _, r := range ranked[:k] {
			longs = append(longs, r.pair)
		}
		shorts := make([]string, 0, k)
		for _, r := range ranked[len(ranked)-k:] {
			shorts = append(shorts, r.pair)
		}
		return Baskets{Longs: longs, Shorts: shorts, AsOf: asOf}
	}

	// Best-first rankings for each leg (long: highest score first; short:
	// lowest score first), same shape selectWithBand expects in xsec-sim.
	longRanked := make([]string, len(ranked))
	shortRanked := make([]string, len(ranked))
	for i, r := range ranked {
		longRanked[i] = r.pair
		shortRanked[len(ranked)-1-i] = r.pair
	}
	longs := selectWithBand(longRanked, k, band, prevLong, nil)
	longSet := make(map[string]bool, len(longs))
	for _, p := range longs {
		longSet[p] = true
	}
	shorts := selectWithBand(shortRanked, k, band, prevShort, longSet)
	return Baskets{Longs: longs, Shorts: shorts, AsOf: asOf}
}

// selectWithBand picks k names from `ranked` (best-first for this leg),
// applying a no-trade band (rank hysteresis): a pair already held (per prev)
// is kept while still inside the top k+band buffer; the remaining slots are
// filled with the best-ranked available names. exclude removes pairs already
// claimed by the other leg (guards against long/short overlap). Ported
// verbatim from cmd/xsec-sim/main.go's validated selectWithBand.
func selectWithBand(ranked []string, k, band int, prev, exclude map[string]bool) []string {
	bandK := k + band
	if bandK > len(ranked) {
		bandK = len(ranked)
	}
	selected := make([]string, 0, k)
	used := make(map[string]bool, k)
	// 1. Keep incumbents still inside the buffer (top k+band), in rank order, up to k.
	for _, p := range ranked[:bandK] {
		if len(selected) >= k {
			break
		}
		if exclude[p] || used[p] {
			continue
		}
		if prev[p] {
			selected = append(selected, p)
			used[p] = true
		}
	}
	// 2. Fill remaining slots with the best-ranked available names.
	for _, p := range ranked {
		if len(selected) >= k {
			break
		}
		if exclude[p] || used[p] {
			continue
		}
		selected = append(selected, p)
		used[p] = true
	}
	return selected
}

// MomentumScorer scores pairs by trailing lookback-bar return using the
// candle archive via the existing Ranker. It is the validated baseline
// (lb14/k7/weekly on the liquid universe).
type MomentumScorer struct {
	ranker   *Ranker
	lookback int
}

// NewMomentumScorer wraps a Ranker as a Scorer with a fixed lookback.
func NewMomentumScorer(r *Ranker, lookbackDays int) *MomentumScorer {
	return &MomentumScorer{ranker: r, lookback: lookbackDays}
}

// Scores implements Scorer: trailing momentum per pair.
func (m *MomentumScorer) Scores(pairs []string) (map[string]float64, time.Time, error) {
	return m.ranker.Momentum(pairs, m.lookback)
}
