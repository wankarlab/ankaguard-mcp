package handlers

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	tradingMiddleware "github.com/rantipay/trading-mcp/internal/trading/middleware"
)

// fundingFetchConcurrency bounds the parallel GetLatestFundingRate gRPC calls
// so a 13-leg basket doesn't open 13 simultaneous streams to GCT.
const fundingFetchConcurrency = 6

// fundingFetchTimeout caps the whole funding read — a slow/flaky exchange must
// not hang the endpoint; legs whose rate did not arrive are returned without a
// rate rather than failing the request.
const fundingFetchTimeout = 8 * time.Second

// fundingSettlementsPerDay is the KuCoin perpetual funding cadence (00:00,
// 08:00, 16:00 UTC) — used only to annualize the per-settlement estimate.
const fundingSettlementsPerDay = 3

// xsecFundingLeg is one basket leg's funding read. FundingRate is the decimal
// per-settlement rate (0.0001 = 0.01%). NextFundingUSD is the leg's cashflow at
// the next settlement: POSITIVE = the basket RECEIVES, negative = it PAYS.
// Both are pointers, omitted when the live rate could not be fetched, so the
// client can tell "unknown" from a genuine zero.
type xsecFundingLeg struct {
	Pair           string   `json:"pair"`
	Dir            int      `json:"dir"`
	Notional       float64  `json:"notional"`
	FundingRate    *float64 `json:"funding_rate,omitempty"`
	NextFundingUSD *float64 `json:"next_funding_usd,omitempty"`
}

// xsecFundingResponse aggregates the per-leg funding into the basket-level cash
// flow that is otherwise invisible on the dashboard (13 perpetuals accrue
// funding every 8h; on a market-neutral book the NET can be a real drag or
// tailwind). NetNextFundingUSD/EstDailyUSD are POSITIVE when the basket earns.
type xsecFundingResponse struct {
	Legs              []xsecFundingLeg `json:"legs"`
	NetNextFundingUSD float64          `json:"net_next_funding_usd"`
	EstDailyUSD       float64          `json:"est_daily_usd"`
	NextFundingTime   *time.Time       `json:"next_funding_time,omitempty"`
	KnownLegs         int              `json:"known_legs"`
	TotalLegs         int              `json:"total_legs"`
	UpdatedAt         time.Time        `json:"updated_at"`
}

// fundingCashflow is a leg's cashflow at the next funding settlement. On
// perpetuals, when the funding rate is POSITIVE longs pay shorts, so a long's
// cashflow is negative and a short's is positive:
//
//	cashflow = -dir * notional * rate   (dir: +1 long, -1 short)
//
// Positive result = the basket RECEIVES funding, negative = it PAYS.
func fundingCashflow(dir int, notional, rate float64) float64 {
	return -float64(dir) * notional * rate
}

// GetFunding handles GET /api/v1/xsec/funding — the live funding cash flow of
// the current basket. Public read-only, same class as GetStatus. Reads the open
// legs from xsec_live_book, then fetches each pair's latest funding rate from
// GoCryptoTrader concurrently (bounded, time-boxed). funding_archive is a stale
// research archive, so the live GCT rate is the only truthful source here.
func (h *XSecHandler) GetFunding(c *gin.Context) {
	resp := xsecFundingResponse{Legs: []xsecFundingLeg{}, UpdatedAt: time.Now().UTC()}
	// Open legs are account-private: anonymous callers get an empty list
	// (same optional-auth rule as GetStatus).
	if h.db == nil || !tradingMiddleware.IsAuthenticated(c) {
		c.JSON(http.StatusOK, models.NewSuccessResponse(resp))
		return
	}

	// Open legs — same registry GetStatus reads.
	type leg struct {
		pair     string
		dir      int
		notional float64
	}
	var legs []leg
	if rows, err := h.db.QueryContext(c.Request.Context(),
		`SELECT pair, dir, notional_usd FROM xsec_live_book WHERE status = 'open' ORDER BY dir DESC, pair`); err != nil {
		h.log.WithError(err).Debug("xsec funding: xsec_live_book not readable")
	} else {
		for rows.Next() {
			var l leg
			if rows.Scan(&l.pair, &l.dir, &l.notional) == nil {
				legs = append(legs, l)
			}
		}
		rows.Close()
	}
	resp.TotalLegs = len(legs)
	resp.Legs = make([]xsecFundingLeg, len(legs))
	for i, l := range legs {
		resp.Legs[i] = xsecFundingLeg{Pair: l.pair, Dir: l.dir, Notional: l.notional}
	}
	if h.gctClient == nil || len(legs) == 0 {
		c.JSON(http.StatusOK, models.NewSuccessResponse(resp))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), fundingFetchTimeout)
	defer cancel()

	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		sem = make(chan struct{}, fundingFetchConcurrency)
	)
	for i := range legs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			data, err := h.gctClient.GetFundingRate(ctx, "kucoin", legs[i].pair)
			if err != nil || data == nil {
				h.log.WithError(err).WithField("pair", legs[i].pair).Debug("xsec funding: rate fetch failed")
				return
			}
			rate := data.FundingRate
			cash := fundingCashflow(legs[i].dir, legs[i].notional, rate)

			mu.Lock()
			resp.Legs[i].FundingRate = &rate
			resp.Legs[i].NextFundingUSD = &cash
			resp.NetNextFundingUSD += cash
			resp.KnownLegs++
			if resp.NextFundingTime == nil && !data.NextFunding.IsZero() {
				nf := data.NextFunding
				resp.NextFundingTime = &nf
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	resp.EstDailyUSD = resp.NetNextFundingUSD * fundingSettlementsPerDay
	// Stable ordering: shorts and longs grouped as stored, but the goroutines
	// wrote into fixed indices so order already matches the query — keep it.
	sort.SliceStable(resp.Legs, func(a, b int) bool {
		if resp.Legs[a].Dir != resp.Legs[b].Dir {
			return resp.Legs[a].Dir > resp.Legs[b].Dir
		}
		return resp.Legs[a].Pair < resp.Legs[b].Pair
	})

	c.JSON(http.StatusOK, models.NewSuccessResponse(resp))
}
