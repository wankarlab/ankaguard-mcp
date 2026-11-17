package handlers

import (
	"database/sql"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/api/v1/models"
)

// maxEquityPoints is the hard cap on points returned by the equity-history
// endpoint. Metrics are always computed on the full set BEFORE reduction.
const maxEquityPoints = 500

// equityRanges maps the accepted `range` query values to their window.
// A zero duration means "no lower bound" (all).
var equityRanges = map[string]time.Duration{
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour,
	"all": 0,
}

// AccountEquityHandler serves the real account equity curve (whole exchange
// account, not just XSEC) from the hourly portfolio_snapshots table.
type AccountEquityHandler struct {
	db  *sql.DB
	log *logrus.Logger
}

// NewAccountEquityHandler builds the handler over the shared SQLite handle.
func NewAccountEquityHandler(db *sql.DB, log *logrus.Logger) *AccountEquityHandler {
	if log == nil {
		log = logrus.New()
	}
	return &AccountEquityHandler{db: db, log: log}
}

// EquityPoint is one equity sample.
type EquityPoint struct {
	Ts             time.Time `json:"ts"`
	TotalEquityUSD float64   `json:"total_equity_usd"`
}

// EquityMetrics are computed over the full (unreduced) range. Percentages are
// positive magnitudes for drawdowns (e.g. 4.2 means a 4.2% drop from peak).
type EquityMetrics struct {
	Current            float64   `json:"current"`
	Peak               float64   `json:"peak"`
	PeakAt             time.Time `json:"peak_at"`
	MaxDrawdownPct     float64   `json:"max_drawdown_pct"`
	CurrentDrawdownPct float64   `json:"current_drawdown_pct"`
	ChangeAbs          float64   `json:"change_abs"`
	// ChangePct is nil when the first value is not positive (undefined).
	ChangePct *float64 `json:"change_pct"`
}

// EquityHistoryResponse is the endpoint payload. Metrics is null when the
// range has no snapshots (never invented zeros).
type EquityHistoryResponse struct {
	Range       string         `json:"range"`
	Points      []EquityPoint  `json:"points"`
	TotalPoints int            `json:"total_points"`
	Reduced     bool           `json:"reduced"`
	Metrics     *EquityMetrics `json:"metrics"`
}

// GetEquityHistory handles GET /api/v1/account/equity-history?range=24h|7d|30d|all.
// Registered on the strict JWT group: anonymous callers never reach it.
func (h *AccountEquityHandler) GetEquityHistory(c *gin.Context) {
	rng := c.DefaultQuery("range", "7d")
	window, ok := equityRanges[rng]
	if !ok {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_RANGE",
			"invalid range; valid values: 24h, 7d, 30d, all"))
		return
	}

	resp := EquityHistoryResponse{Range: rng, Points: []EquityPoint{}}
	if h.db == nil {
		c.JSON(http.StatusOK, models.NewSuccessResponse(resp))
		return
	}

	// Read ascending and filter in Go: robust to the timestamp text format the
	// SQLite driver stored, and the table is small (one row per hour).
	rows, err := h.db.QueryContext(c.Request.Context(),
		`SELECT snapshot_time, total_value_usdt FROM portfolio_snapshots ORDER BY snapshot_time ASC`)
	if err != nil {
		// Table may not exist yet: same convention as /xsec/status (empty, no 500).
		h.log.WithError(err).Debug("equity-history: portfolio_snapshots not readable")
		c.JSON(http.StatusOK, models.NewSuccessResponse(resp))
		return
	}
	defer rows.Close()

	var cutoff time.Time
	if window > 0 {
		cutoff = time.Now().Add(-window)
	}
	var all []EquityPoint
	for rows.Next() {
		var p EquityPoint
		if rows.Scan(&p.Ts, &p.TotalEquityUSD) != nil {
			continue
		}
		if window > 0 && p.Ts.Before(cutoff) {
			continue
		}
		all = append(all, p)
	}

	if len(all) == 0 {
		c.JSON(http.StatusOK, models.NewSuccessResponse(resp))
		return
	}

	metrics, keep := computeEquityMetrics(all)
	resp.Metrics = metrics
	resp.TotalPoints = len(all)
	resp.Points = reduceEquityPoints(all, maxEquityPoints, keep...)
	resp.Reduced = len(resp.Points) < len(all)
	c.JSON(http.StatusOK, models.NewSuccessResponse(resp))
}

// computeEquityMetrics returns the metrics over pts (ascending, non-empty) and
// the indexes that must survive reduction (global peak, max-drawdown peak and
// trough), so the drawdown extremes are never hidden.
func computeEquityMetrics(pts []EquityPoint) (*EquityMetrics, []int) {
	m := &EquityMetrics{Current: pts[len(pts)-1].TotalEquityUSD}
	runPeak, runPeakIdx := pts[0].TotalEquityUSD, 0
	peakIdx := 0
	ddPeakIdx, ddTroughIdx := 0, 0
	for i, p := range pts {
		if p.TotalEquityUSD > runPeak {
			runPeak, runPeakIdx = p.TotalEquityUSD, i
		}
		if runPeak > 0 {
			if dd := (runPeak - p.TotalEquityUSD) / runPeak * 100; dd > m.MaxDrawdownPct {
				m.MaxDrawdownPct, ddPeakIdx, ddTroughIdx = dd, runPeakIdx, i
			}
		}
		if p.TotalEquityUSD > pts[peakIdx].TotalEquityUSD {
			peakIdx = i
		}
	}
	m.Peak, m.PeakAt = pts[peakIdx].TotalEquityUSD, pts[peakIdx].Ts
	if m.Peak > 0 {
		m.CurrentDrawdownPct = (m.Peak - m.Current) / m.Peak * 100
	}
	m.ChangeAbs = m.Current - pts[0].TotalEquityUSD
	if first := pts[0].TotalEquityUSD; first > 0 {
		v := m.ChangeAbs / first * 100
		m.ChangePct = &v
	}
	return m, []int{peakIdx, ddPeakIdx, ddTroughIdx}
}

// reduceEquityPoints shrinks pts (ascending) to at most limit points using
// time buckets that keep each bucket's min and max, plus the first point, the
// last point and any forced indexes. Output stays in chronological order.
func reduceEquityPoints(pts []EquityPoint, limit int, forced ...int) []EquityPoint {
	if len(pts) <= limit {
		return pts
	}
	keep := map[int]struct{}{0: {}, len(pts) - 1: {}}
	for _, i := range forced {
		keep[i] = struct{}{}
	}
	// Reserve room for first/last/forced; each bucket keeps up to 2 points.
	buckets := (limit - len(keep) - 2) / 2
	if buckets < 1 {
		buckets = 1
	}
	start, end := pts[0].Ts, pts[len(pts)-1].Ts
	span := end.Sub(start)
	minIdx := make([]int, buckets)
	maxIdx := make([]int, buckets)
	for b := range minIdx {
		minIdx[b], maxIdx[b] = -1, -1
	}
	for i, p := range pts {
		b := 0
		if span > 0 {
			b = int(float64(p.Ts.Sub(start)) / float64(span) * float64(buckets))
			if b >= buckets {
				b = buckets - 1
			}
		} else {
			b = i * buckets / len(pts)
		}
		if minIdx[b] < 0 || p.TotalEquityUSD < pts[minIdx[b]].TotalEquityUSD {
			minIdx[b] = i
		}
		if maxIdx[b] < 0 || p.TotalEquityUSD > pts[maxIdx[b]].TotalEquityUSD {
			maxIdx[b] = i
		}
	}
	for b := 0; b < buckets; b++ {
		if minIdx[b] >= 0 {
			keep[minIdx[b]] = struct{}{}
			keep[maxIdx[b]] = struct{}{}
		}
	}
	idx := make([]int, 0, len(keep))
	for i := range keep {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	out := make([]EquityPoint, 0, len(idx))
	for _, i := range idx {
		out = append(out, pts[i])
	}
	return out
}
