package portfolio

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/tools/common"
	"github.com/rantipay/trading-mcp/internal/storage"
)

// minDailyReturns is the minimum number of daily equity returns needed before
// risk-adjusted metrics (Sharpe, VaR, ...) mean anything. With less history the
// tools answer DATA_UNAVAILABLE instead of computing on invented data.
const minDailyReturns = 20

// snapshotSource is the real capital history: hourly portfolio snapshots
// written by the PortfolioTracker into the portfolio_snapshots table.
type snapshotSource interface {
	GetByTimeRange(from, to time.Time) ([]*storage.PortfolioSnapshot, error)
}

// asSnapshotSource avoids wrapping a nil repository pointer in a non-nil interface.
func asSnapshotSource(repo *storage.PortfolioSnapshotRepository) snapshotSource {
	if repo == nil {
		return nil
	}
	return repo
}

type dailyEquity struct {
	Day   time.Time
	Value float64
}

// equityDaily loads the snapshots of the last periodDays and reduces them to
// one equity value per UTC day (the last snapshot of the day). Snapshot values
// include deposits and withdrawals; they are account equity, not trading PnL.
func equityDaily(src snapshotSource, periodDays int) ([]dailyEquity, error) {
	if src == nil {
		return nil, common.DataUnavailable("portfolio history tracking is not initialized (no snapshot storage)", map[string]interface{}{
			"daily_returns_required": minDailyReturns, "daily_returns_available": 0, "daily_returns_missing": minDailyReturns,
		})
	}
	to := time.Now().UTC()
	from := to.AddDate(0, 0, -periodDays)
	snaps, err := src.GetByTimeRange(from, to)
	if err != nil {
		return nil, common.DataUnavailable("could not read the portfolio snapshot history: "+err.Error(), nil)
	}
	sort.SliceStable(snaps, func(i, j int) bool { return snaps[i].SnapshotTime.Before(snaps[j].SnapshotTime) })
	var pts []dailyEquity
	for _, s := range snaps {
		if s.TotalValueUSDT <= 0 {
			continue
		}
		day := s.SnapshotTime.UTC().Truncate(24 * time.Hour)
		if n := len(pts); n > 0 && pts[n-1].Day.Equal(day) {
			pts[n-1].Value = s.TotalValueUSDT
			continue
		}
		pts = append(pts, dailyEquity{Day: day, Value: s.TotalValueUSDT})
	}
	returns := len(pts) - 1
	if returns < minDailyReturns {
		avail := returns
		if avail < 0 {
			avail = 0
		}
		return nil, common.DataUnavailable(fmt.Sprintf("not enough portfolio history: %d daily returns available, %d required", avail, minDailyReturns), map[string]interface{}{
			"daily_returns_required":  minDailyReturns,
			"daily_returns_available": avail,
			"daily_returns_missing":   minDailyReturns - avail,
			"period_days":             periodDays,
			"source":                  "portfolio_snapshots (hourly, written by the portfolio tracker)",
		})
	}
	return pts, nil
}

func dailyReturns(pts []dailyEquity) []float64 {
	r := make([]float64, 0, len(pts)-1)
	for i := 1; i < len(pts); i++ {
		r = append(r, pts[i].Value/pts[i-1].Value-1)
	}
	return r
}

// maxDrawdown returns the largest peak-to-trough fall as an absolute amount and a percentage.
func maxDrawdown(pts []dailyEquity) (abs, pct float64) {
	peak := pts[0].Value
	for _, p := range pts {
		if p.Value > peak {
			peak = p.Value
		}
		if d := peak - p.Value; d > abs {
			abs = d
		}
		if d := (peak - p.Value) / peak * 100; d > pct {
			pct = d
		}
	}
	return abs, pct
}

func mean(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

func stdDev(v []float64) float64 {
	if len(v) < 2 {
		return 0
	}
	m := mean(v)
	s := 0.0
	for _, x := range v {
		s += (x - m) * (x - m)
	}
	return math.Sqrt(s / float64(len(v)-1))
}

// historicalVaR returns the loss (as a positive fraction) at the given
// confidence using the empirical quantile of the returns; 0 when the
// quantile is a gain.
func historicalVaR(returns []float64, confidence float64) float64 {
	s := append([]float64(nil), returns...)
	sort.Float64s(s)
	idx := int(math.Floor((1 - confidence) * float64(len(s))))
	if idx >= len(s) {
		idx = len(s) - 1
	}
	return math.Max(0, -s[idx])
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}
