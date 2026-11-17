package gate

import (
	"math"
	"math/rand"
	"sort"
	"time"
)

// Trade is one closed (or dropped) auto/manual position.
type Trade struct {
	NetPnLUSD float64
	ClosedAt  time.Time
	Fraud     bool
}

// Bootstrap returns the percentile confidence interval of the mean of vals at
// the given level using `resamples` resamples with replacement. It is
// deterministic for a given rng state. Fewer than 2 values -> ok=false.
func Bootstrap(vals []float64, level float64, resamples int, rng *rand.Rand) (lo, hi float64, ok bool) {
	n := len(vals)
	if n < 2 || resamples < 1 {
		return 0, 0, false
	}
	means := make([]float64, resamples)
	for b := range means {
		var s float64
		for i := 0; i < n; i++ {
			s += vals[rng.Intn(n)]
		}
		means[b] = s / float64(n)
	}
	sort.Float64s(means)
	alpha := 1 - level
	li := int(math.Floor(alpha / 2 * float64(resamples)))
	ui := int(math.Ceil((1-alpha/2)*float64(resamples))) - 1
	if li < 0 {
		li = 0
	}
	if ui >= resamples {
		ui = resamples - 1
	}
	if ui < li {
		ui = li
	}
	return means[li], means[ui], true
}

// profitFactor is gross profit / gross loss; nil when there are no losses
// (undefined / infinite) or no trades.
func profitFactor(nets []float64) *float64 {
	var win, loss float64
	for _, v := range nets {
		if v > 0 {
			win += v
		} else {
			loss -= v
		}
	}
	if loss == 0 {
		return nil
	}
	pf := win / loss
	return &pf
}

// maxDrawdown is the largest peak-to-trough fall of the cumulative net PnL
// (starting from 0) over trades ordered by closing time.
func maxDrawdown(trades []Trade) float64 {
	var cum, peak, dd float64
	for _, t := range trades {
		cum += t.NetPnLUSD
		if cum > peak {
			peak = cum
		}
		if peak-cum > dd {
			dd = peak - cum
		}
	}
	return dd
}
