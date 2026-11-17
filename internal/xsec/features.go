package xsec

import (
	"math"
	"time"

	"github.com/rantipay/trading-mcp/internal/ai"
	"github.com/rantipay/trading-mcp/internal/storage"
)

// This file is the SINGLE source of truth for the LTR feature set.
// cmd/build-xsec-dataset (training data) and LTRScorer (live serving) both
// call ComputeFeatures so train and serve can never diverge.

// MinFeatureBars is the minimum number of daily bars (ending at the as-of
// bar) required to compute the full feature set: 84 bars for ret_12w,
// 90 for dist_high_90d, plus the as-of bar itself.
const MinFeatureBars = 91

// amihudScale rescales the Amihud illiquidity ratio (|ret|/USD-turnover,
// naturally ~1e-9) into a readable magnitude. Cosmetic only: features are
// cross-sectionally z-scored, but the same constant must be used on both
// the train and serve path (it is, because both call this code).
const amihudScale = 1e6

// BetaBenchmarkCandidates lists market-beta benchmark pairs in preference
// order. The candle archive currently has no BTC-USDT daily candles, so the
// beta feature falls back to ETH-USDT. Shared by the dataset builder and the
// live LTRScorer so train and serve use the same benchmark.
var BetaBenchmarkCandidates = []string{"BTC-USDT", "ETH-USDT"}

// FeatureNames returns the canonical feature order. The CSV dataset columns,
// the /rank request payload and the trained model's feature_cols all follow
// this exact order.
func FeatureNames() []string {
	return []string{
		"ret_1w", "ret_2w", "ret_4w", "ret_8w", "ret_12w",
		"vol_7d", "vol_30d",
		"beta_btc_60d",
		"amihud_30d",
		"turnover_avg_7d", "turnover_delta",
		"dist_high_90d",
		"funding_last", "funding_avg_7d",
	}
}

// ComputeFeatures computes the raw (pre-z-score) feature vector for one pair.
//
//   - bars: the pair's daily candles, ascending, with the LAST bar being the
//     as-of bar. The caller must ensure every bar's timestamp is <= the as-of
//     time (no lookahead). ComputeFeatures re-asserts ascending order and
//     returns ok=false on any violation (this codebase once shipped a
//     newest-first candle ordering bug; we never trust ordering again).
//   - btcBars: BTC-USDT daily candles, ascending, all <= as-of (used for beta).
//     May be nil/short: beta degrades to 0.
//   - funding: settled funding rates for the pair, ascending, all <= as-of.
//     May be nil (table absent): funding features degrade to 0.
//
// Returns the vector in FeatureNames() order and ok=false when the pair lacks
// sufficient history.
func ComputeFeatures(bars, btcBars []ai.Candle, funding []storage.FundingRate) ([]float64, bool) {
	n := len(bars)
	if n < MinFeatureBars {
		return nil, false
	}
	// Anti-leakage assertion: strictly ascending timestamps.
	for i := 1; i < n; i++ {
		if !bars[i].Timestamp.After(bars[i-1].Timestamp) {
			return nil, false
		}
	}
	last := bars[n-1]
	c0 := last.Close
	if c0 <= 0 {
		return nil, false
	}

	retOver := func(k int) float64 {
		p := bars[n-1-k].Close
		if p <= 0 {
			return 0
		}
		return c0/p - 1
	}

	// Daily log returns, lr[i] is the return INTO bars[i+1].
	logRets := make([]float64, 0, n-1)
	retTimes := make([]time.Time, 0, n-1)
	for i := 1; i < n; i++ {
		if bars[i].Close <= 0 || bars[i-1].Close <= 0 {
			logRets = append(logRets, 0)
		} else {
			logRets = append(logRets, math.Log(bars[i].Close/bars[i-1].Close))
		}
		retTimes = append(retTimes, bars[i].Timestamp)
	}

	volOver := func(k int) float64 {
		if len(logRets) < k || k < 2 {
			return 0
		}
		return xsStddev(logRets[len(logRets)-k:])
	}

	// Beta vs BTC over the last 60 matched daily log returns.
	beta := 0.0
	if len(btcBars) >= 2 {
		btcRet := make(map[int64]float64, len(btcBars)-1)
		for i := 1; i < len(btcBars); i++ {
			if btcBars[i].Close > 0 && btcBars[i-1].Close > 0 {
				btcRet[btcBars[i].Timestamp.Unix()] = math.Log(btcBars[i].Close / btcBars[i-1].Close)
			}
		}
		var xs, ys []float64
		start := len(logRets) - 60
		if start < 0 {
			start = 0
		}
		for i := start; i < len(logRets); i++ {
			if b, ok := btcRet[retTimes[i].Unix()]; ok {
				xs = append(xs, b) // btc
				ys = append(ys, logRets[i])
			}
		}
		if len(xs) >= 30 {
			vb := xsVariance(xs)
			if vb > 0 {
				beta = xsCovariance(xs, ys) / vb
			}
		}
	}

	// Amihud illiquidity over the last 30 bars: avg(|daily ret| / USD turnover).
	amihud := 0.0
	{
		cnt := 0
		sum := 0.0
		for i := n - 30; i < n; i++ {
			prev := bars[i-1].Close
			turn := bars[i].Close * bars[i].Volume
			if prev <= 0 || turn <= 0 {
				continue
			}
			sum += math.Abs(bars[i].Close/prev-1) / turn
			cnt++
		}
		if cnt > 0 {
			amihud = sum / float64(cnt) * amihudScale
		}
	}

	turnoverAvg := func(k int) float64 {
		sum := 0.0
		for i := n - k; i < n; i++ {
			sum += bars[i].Close * bars[i].Volume
		}
		return sum / float64(k)
	}
	t7 := turnoverAvg(7)
	t30 := turnoverAvg(30)
	turnDelta := 0.0
	if t30 > 0 {
		turnDelta = t7 / t30
	}

	maxHigh := 0.0
	for i := n - 90; i < n; i++ {
		if bars[i].High > maxHigh {
			maxHigh = bars[i].High
		}
	}
	distHigh := 0.0
	if maxHigh > 0 {
		distHigh = c0/maxHigh - 1
	}

	fundingLast := 0.0
	fundingAvg7 := 0.0
	if len(funding) > 0 {
		fundingLast = funding[len(funding)-1].Rate
		cutoff := last.Timestamp.Add(-7 * 24 * time.Hour)
		sum, cnt := 0.0, 0
		for _, fr := range funding {
			if fr.Timestamp.After(cutoff) {
				sum += fr.Rate
				cnt++
			}
		}
		if cnt > 0 {
			fundingAvg7 = sum / float64(cnt)
		}
	}

	return []float64{
		retOver(7), retOver(14), retOver(28), retOver(56), retOver(84),
		volOver(7), volOver(30),
		beta,
		amihud,
		t7, turnDelta,
		distHigh,
		fundingLast, fundingAvg7,
	}, true
}

// ZScoreColumns z-scores each feature column in place across the given rows
// (one row per pair = one cross-section). Columns with zero variance become
// all zeros. This is the per-week cross-sectional normalization used both in
// dataset building and live scoring (non-stationarity rule).
func ZScoreColumns(rows [][]float64) {
	if len(rows) == 0 {
		return
	}
	cols := len(rows[0])
	for c := 0; c < cols; c++ {
		col := make([]float64, len(rows))
		for r := range rows {
			col[r] = rows[r][c]
		}
		m := xsMean(col)
		sd := xsStddev(col)
		for r := range rows {
			if sd > 0 {
				rows[r][c] = (rows[r][c] - m) / sd
			} else {
				rows[r][c] = 0
			}
		}
	}
}

func xsMean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func xsVariance(xs []float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	m := xsMean(xs)
	s := 0.0
	for _, x := range xs {
		d := x - m
		s += d * d
	}
	return s / float64(len(xs)-1)
}

func xsStddev(xs []float64) float64 { return math.Sqrt(xsVariance(xs)) }

func xsCovariance(xs, ys []float64) float64 {
	if len(xs) != len(ys) || len(xs) < 2 {
		return 0
	}
	mx, my := xsMean(xs), xsMean(ys)
	s := 0.0
	for i := range xs {
		s += (xs[i] - mx) * (ys[i] - my)
	}
	return s / float64(len(xs)-1)
}
