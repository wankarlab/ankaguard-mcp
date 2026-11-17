// Package handlers — XSecHandler exposes the market-neutral cross-sectional
// momentum (xsec) live basket state for dashboards (F4.6, F4.8).
//
// GET /api/v1/xsec/status — equity history, current REAL basket (with live
// PnL), params, shadow baskets.
//
// The basket comes from xsec_live_book — the ownership registry cmd/xsec-live
// itself maintains (see internal/xsec/livebook.go) — NOT from xsec_positions,
// which belongs to the retired cmd/xsec-bot paper simulator and is left
// stale/frozen once the real basket takes over. Equity/params/shadow still
// read their original tables. Any table may not exist yet — every section
// degrades to an empty array instead of erroring, so the endpoint is safe to
// expose before the live basket's first run.
package handlers

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	tradingMiddleware "github.com/rantipay/trading-mcp/internal/trading/middleware"
)

// XSecEquityPoint is one row of the xsec_equity curve.
type XSecEquityPoint struct {
	Ts     time.Time `json:"ts"`
	Equity float64   `json:"equity"`
	NLong  int       `json:"n_long"`
	NShort int       `json:"n_short"`
}

// XSecRealEquityPoint is one row of the REAL account's hourly value history,
// from portfolio_snapshots — the actual KuCoin balance (spot+futures) written
// by internal/tracker.PortfolioTracker every hour, NOT the xsec_equity paper
// curve above. This is what a live-money equity chart must be built from.
type XSecRealEquityPoint struct {
	Ts             time.Time `json:"ts"`
	TotalEquityUSD float64   `json:"total_equity_usd"`
}

// XSecPosition is one open REAL basket leg (from xsec_live_book), enriched
// with live price/PnL from the exchange when available. CurrentPrice and
// UnrealizedPnL are pointers, OMITTED from the JSON entirely (not present
// as `null`, and never a bare 0) when the exchange lookup fails or the pair
// isn't found there (e.g. transient API error) — the leg itself still
// renders. This distinction matters: before, both fields were plain
// float64s that always serialized as `0` on enrichment failure, and the
// dashboard rendered that as a real "$0.00 / +0.00%" figure indistinguishable
// from an actually-flat position (found 2026-07-23 — the basket panel
// looked "stuck at 0" because callers had no way to tell the two cases
// apart). Entry/Notional are real, non-optional numbers now too — see the
// entry_price/notional_usd migration in internal/xsec/livebook.go.
type XSecPosition struct {
	Pair          string    `json:"pair"`
	Dir           int       `json:"dir"` // +1 long, -1 short
	Entry         float64   `json:"entry"`
	Notional      float64   `json:"notional"`
	OpenedAt      time.Time `json:"opened_at"`
	CurrentPrice  *float64  `json:"current_price,omitempty"`
	UnrealizedPnL *float64  `json:"unrealized_pnl,omitempty"`
}

// XSecParam is one key/value row from xsec_params.
type XSecParam struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

// XSecShadowBasket is one row of xsec_shadow_baskets (raw JSON payload kept as
// text — the table is owned by the research tooling and its schema may evolve).
type XSecShadowBasket struct {
	Ts      time.Time `json:"ts"`
	Payload string    `json:"payload"`
}

// XSecTotals is the consolidated basket summary: sums across every open leg
// that has a known live PnL. Legs without a live PnL match (KnownLegs <
// len(Positions)) are excluded from TotalUnrealizedPnL/Pct rather than
// silently counted as 0 — see XSecPosition's doc comment.
type XSecTotals struct {
	NLong              int      `json:"n_long"`
	NShort             int      `json:"n_short"`
	TotalNotional      float64  `json:"total_notional"`
	KnownLegs          int      `json:"known_legs"`
	TotalUnrealizedPnL *float64 `json:"total_unrealized_pnl,omitempty"`
	TotalUnrealizedPct *float64 `json:"total_unrealized_pnl_pct,omitempty"`
}

// XSecActiveAlert is one currently-firing basket-health alert (from
// xsec_alert_state, written by the XSecAlertMonitor). Surfaced here so the
// polled dashboard shows the same alerts that are pushed to the phone.
type XSecActiveAlert struct {
	Key     string    `json:"key"`
	Title   string    `json:"title"`
	Body    string    `json:"body"`
	FiredAt time.Time `json:"fired_at"`
}

// xsecStatusResponse is the GET /api/v1/xsec/status payload.
type xsecStatusResponse struct {
	EquityHistory     []XSecEquityPoint     `json:"equity_history"`
	RealEquityHistory []XSecRealEquityPoint `json:"real_equity_history"`
	Positions         []XSecPosition        `json:"positions"`
	Totals            XSecTotals            `json:"totals"`
	Params            []XSecParam           `json:"params"`
	Shadow            []XSecShadowBasket    `json:"shadow"`
	ActiveAlerts      []XSecActiveAlert     `json:"active_alerts"`
	UpdatedAt         time.Time             `json:"updated_at"`
}

// XSecHandler serves read-only xsec strategy status.
type XSecHandler struct {
	db        *sql.DB
	gctClient *gctclient.Client // optional; nil skips live price/PnL enrichment
	log       *logrus.Logger
}

// NewXSecHandler creates an XSecHandler over the shared SQLite handle that the
// router already derives from the GORM connection (same data/trading.db file the
// xsec tables live in — no separate read-only open is needed). db may be nil:
// the endpoint then returns empty sections rather than 500s. gctClient may
// also be nil (e.g. in tests): legs are then returned without live price/PnL.
func NewXSecHandler(db *sql.DB, gctClient *gctclient.Client, log *logrus.Logger) *XSecHandler {
	if log == nil {
		log = logrus.New()
	}
	return &XSecHandler{db: db, gctClient: gctClient, log: log}
}

// GetStatus handles GET /api/v1/xsec/status with OPTIONAL auth.
// Anonymous callers (no/invalid JWT) receive only paper/strategy data
// (equity_history, params, shadow, updated_at); the real-account sections
// (real_equity_history, positions, totals, active_alerts) are returned empty
// and are never even queried. A valid JWT (set upstream by
// OptionalKongJWTMiddleware) unlocks the full payload. Missing tables yield
// empty arrays, never errors.
func (h *XSecHandler) GetStatus(c *gin.Context) {
	resp := xsecStatusResponse{
		EquityHistory:     []XSecEquityPoint{},
		RealEquityHistory: []XSecRealEquityPoint{},
		Positions:         []XSecPosition{},
		Totals:            XSecTotals{},
		Params:            []XSecParam{},
		Shadow:            []XSecShadowBasket{},
		ActiveAlerts:      []XSecActiveAlert{},
		UpdatedAt:         time.Now().UTC(),
	}
	if h.db == nil {
		c.JSON(http.StatusOK, models.NewSuccessResponse(resp))
		return
	}

	authed := tradingMiddleware.IsAuthenticated(c)

	// REAL equity: last 7 days of hourly snapshots (≤168 rows) from the real
	// KuCoin account, written by internal/tracker.PortfolioTracker. This is
	// the ONLY equity series a live-money dashboard may chart — see
	// XSecRealEquityPoint's doc comment. Authenticated callers only.
	if !authed {
		// skip: account-private
	} else if rows, err := h.db.QueryContext(c.Request.Context(),
		`SELECT snapshot_time, total_value_usdt FROM
		   (SELECT snapshot_time, total_value_usdt FROM portfolio_snapshots ORDER BY snapshot_time DESC LIMIT 168)
		 ORDER BY snapshot_time ASC`); err != nil {
		h.log.WithError(err).Debug("xsec: portfolio_snapshots not readable (table may not exist yet)")
	} else {
		for rows.Next() {
			var p XSecRealEquityPoint
			if rows.Scan(&p.Ts, &p.TotalEquityUSD) == nil {
				resp.RealEquityHistory = append(resp.RealEquityHistory, p)
			}
		}
		rows.Close()
	}

	// Equity: last 26 rows (≈ half a year of weekly rebalances), oldest first.
	if rows, err := h.db.QueryContext(c.Request.Context(),
		`SELECT ts, equity, n_long, n_short FROM
		   (SELECT ts, equity, n_long, n_short FROM xsec_equity ORDER BY ts DESC LIMIT 26)
		 ORDER BY ts ASC`); err != nil {
		h.log.WithError(err).Debug("xsec: xsec_equity not readable (table may not exist yet)")
	} else {
		for rows.Next() {
			var p XSecEquityPoint
			if rows.Scan(&p.Ts, &p.Equity, &p.NLong, &p.NShort) == nil {
				resp.EquityHistory = append(resp.EquityHistory, p)
			}
		}
		rows.Close()
	}

	// Current REAL basket — the live-book registry cmd/xsec-live maintains,
	// NOT the paper-bot's xsec_positions (see package doc comment above).
	// Entry/Notional now come from the registry's own entry_price/notional_usd
	// columns (see internal/xsec/livebook.go) — CurrentPrice/UnrealizedPnL are
	// still exchange-sourced by enrichWithLivePnL below, since only the
	// exchange knows the live mark price.
	// Authenticated callers only.
	if !authed {
		// skip: account-private
	} else if rows, err := h.db.QueryContext(c.Request.Context(),
		`SELECT pair, dir, opened_at, entry_price, notional_usd FROM xsec_live_book WHERE status = 'open' ORDER BY dir DESC, pair`); err != nil {
		h.log.WithError(err).Debug("xsec: xsec_live_book not readable (table may not exist yet)")
	} else {
		for rows.Next() {
			var p XSecPosition
			if rows.Scan(&p.Pair, &p.Dir, &p.OpenedAt, &p.Entry, &p.Notional) == nil {
				resp.Positions = append(resp.Positions, p)
			}
		}
		rows.Close()
	}
	resp.Positions = h.enrichWithLivePnL(c.Request.Context(), resp.Positions)
	resp.Totals = computeTotals(resp.Positions)

	// Strategy params (key/value).
	if rows, err := h.db.QueryContext(c.Request.Context(),
		`SELECT key, value, updated_at FROM xsec_params ORDER BY key`); err != nil {
		h.log.WithError(err).Debug("xsec: xsec_params not readable (table may not exist yet)")
	} else {
		for rows.Next() {
			var p XSecParam
			if rows.Scan(&p.Key, &p.Value, &p.UpdatedAt) == nil {
				resp.Params = append(resp.Params, p)
			}
		}
		rows.Close()
	}

	// Shadow baskets: last 8 rows, newest first. The table does not exist yet on
	// most installs; the failed query just leaves the empty array in place.
	if rows, err := h.db.QueryContext(c.Request.Context(),
		`SELECT ts, payload FROM xsec_shadow_baskets ORDER BY ts DESC LIMIT 8`); err != nil {
		h.log.WithError(err).Debug("xsec: xsec_shadow_baskets not readable (table may not exist yet)")
	} else {
		for rows.Next() {
			var s XSecShadowBasket
			if rows.Scan(&s.Ts, &s.Payload) == nil {
				resp.Shadow = append(resp.Shadow, s)
			}
		}
		rows.Close()
	}

	// Active basket-health alerts (written by XSecAlertMonitor). Table may not
	// exist yet (monitor never ran) — the failed query just leaves [] in place.
	if !authed {
		// skip: account-private
	} else if rows, err := h.db.QueryContext(c.Request.Context(),
		`SELECT alert_key, title, body, fired_at FROM xsec_alert_state WHERE active = 1 ORDER BY fired_at DESC`); err != nil {
		h.log.WithError(err).Debug("xsec: xsec_alert_state not readable (monitor may not have run yet)")
	} else {
		for rows.Next() {
			var a XSecActiveAlert
			var firedAt sql.NullTime
			if rows.Scan(&a.Key, &a.Title, &a.Body, &firedAt) == nil {
				if firedAt.Valid {
					a.FiredAt = firedAt.Time
				}
				resp.ActiveAlerts = append(resp.ActiveAlerts, a)
			}
		}
		rows.Close()
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(resp))
}

// enrichWithLivePnL fetches real KuCoin futures positions once and attaches
// CurrentPrice/UnrealizedPnL to each leg by symbol match. On any failure
// (nil client, exchange error, timeout) it logs and returns legs unchanged —
// this endpoint is public/read-only and must never 500 on an exchange hiccup.
func (h *XSecHandler) enrichWithLivePnL(ctx context.Context, positions []XSecPosition) []XSecPosition {
	if h.gctClient == nil || len(positions) == 0 {
		return positions
	}
	base := h.gctClient.GetBaseClient()
	if base == nil {
		return positions
	}
	livePositions, err := base.GetFuturesPositions(ctx, "kucoin")
	if err != nil {
		h.log.WithError(err).Debug("xsec: live PnL enrichment: GetFuturesPositions failed, serving legs without PnL")
		return positions
	}
	// GetFuturesPositions runs pos.Symbol through the client's symbolConverter
	// when one is configured, normalizing it to "BASE-USDT" — the SAME shape
	// XSecPosition.Pair already uses; when no converter is configured, Symbol
	// stays in KuCoin's raw futures form ("BASEUSDTM"). Whether the converter
	// is active is a runtime config detail this handler has no business
	// assuming either way, so try both lookup keys per leg. (Found 2026-07-23:
	// looking up only the raw-converted key matched zero legs on a deployment
	// where the converter WAS active — PnL silently 100% unknown, not a
	// partial miss.)
	bySymbol := make(map[string]*gct.Position, len(livePositions))
	for _, p := range livePositions {
		if p != nil {
			bySymbol[p.Symbol] = p
		}
	}
	for i := range positions {
		if live, ok := matchLivePosition(positions[i].Pair, bySymbol); ok {
			price, pnl := live.CurrentPrice, live.UnrealizedPnL
			positions[i].CurrentPrice = &price
			positions[i].UnrealizedPnL = &pnl
		}
	}
	return positions
}

// matchLivePosition looks up pair ("BASE-USDT") in bySymbol trying the
// standard form first, then the raw KuCoin futures form — see the call site's
// doc comment for why both are tried. Split out as a pure function so the
// symbol-matching logic is unit-testable without a real gctClient.
func matchLivePosition(pair string, bySymbol map[string]*gct.Position) (*gct.Position, bool) {
	if live, ok := bySymbol[pair]; ok {
		return live, true
	}
	live, ok := bySymbol[xsecFuturesSymbol(pair)]
	return live, ok
}

// computeTotals sums the consolidated basket figures: notional across every
// leg (always known), PnL $ and % only across legs with a known live PnL
// (KnownLegs < len(positions) when the exchange enrichment missed one or
// more legs — TotalUnrealizedPnL/Pct are then a PARTIAL sum, still better
// than silently treating the unknown legs as flat).
func computeTotals(positions []XSecPosition) XSecTotals {
	t := XSecTotals{}
	var pnlSum float64
	for _, p := range positions {
		if p.Dir > 0 {
			t.NLong++
		} else {
			t.NShort++
		}
		t.TotalNotional += p.Notional
		if p.UnrealizedPnL != nil {
			t.KnownLegs++
			pnlSum += *p.UnrealizedPnL
		}
	}
	if t.KnownLegs > 0 {
		t.TotalUnrealizedPnL = &pnlSum
		if t.TotalNotional != 0 {
			pct := (pnlSum / t.TotalNotional) * 100
			t.TotalUnrealizedPct = &pct
		}
	}
	return t
}

// xsecFuturesSymbol maps "BASE-USDT" to KuCoin's futures symbol format
// ("BASEUSDTM", "BTC" -> "XBT"). Mirrors futuresSym in cmd/xsec-live/main.go
// and toFuturesSymbol in internal/mcp-trading/tools/orders/direct_orders.go —
// each package keeps its own copy of this tiny pure mapping rather than
// exporting a shared one across an internal package boundary.
func xsecFuturesSymbol(pair string) string {
	base := pair
	if i := len(pair) - 5; i > 0 && pair[i:] == "-USDT" {
		base = pair[:i]
	}
	if base == "BTC" {
		base = "XBT"
	}
	return strings.ToUpper(base) + "USDTM"
}
