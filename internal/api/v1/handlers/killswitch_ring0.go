package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/killswitch"
	"github.com/rantipay/trading-mcp/internal/xsec"
)

// Ring0ConfirmPhrase is the literal confirmation required for a live ring 0.
const Ring0ConfirmPhrase = "CERRAR TODO"

// Bounded timeouts for every ring-0 step: a hung exchange or engine must never
// stall the remaining steps.
const (
	ring0BotsTimeout    = 60 * time.Second
	ring0EngineTimeout  = 30 * time.Second
	ring0OrdersTimeout  = 30 * time.Second
	ring0ListTimeout    = 20 * time.Second
	ring0CloseTimeout   = 30 * time.Second
	ring0ResweepSettle  = 2 * time.Second
	ring0ResultOK       = "closed"
	ring0ResultFailed   = "failed"
	ring0ResultWouldCls = "would_close"
)

// Ring0Position is one open exchange position as seen by ring 0.
type Ring0Position struct {
	Symbol        string
	Side          string
	Size          float64
	UnrealizedPnL float64
}

// Ring0Exchange is the exchange-facing capability ring 0 needs. The production
// implementation (NewGCTRing0Exchange) uses the direct KuCoin REST path.
type Ring0Exchange interface {
	// ListOpenPositions returns every open futures position of the account.
	ListOpenPositions(ctx context.Context) ([]Ring0Position, error)
	// ClosePosition closes one position at market.
	ClosePosition(ctx context.Context, symbol string) error
	// CancelAllOrders cancels every open futures order (including stop orders)
	// and returns how many were cancelled.
	CancelAllOrders(ctx context.Context) (int, error)
}

// LiveBookMarker keeps the xsec_live_book ownership registry consistent after
// ring 0 closes a pair on the exchange.
type LiveBookMarker interface {
	MarkClosed(symbol string) error
}

// SetExchange wires the exchange used to cancel orders and close positions.
func (h *KillSwitchHandler) SetExchange(e Ring0Exchange) { h.exchange = e }

// SetLiveBook wires the xsec_live_book registry updater (optional).
func (h *KillSwitchHandler) SetLiveBook(m LiveBookMarker) { h.liveBook = m }

// AddEngine registers an extra in-process engine for ring 0 on this handler
// (in addition to those in the process-wide killswitch registry).
func (h *KillSwitchHandler) AddEngine(e killswitch.EngineStopper) {
	if e != nil {
		h.engines = append(h.engines, e)
	}
}

// ring0Result is the data payload returned by Ring0.
type ring0Result struct {
	Ring            int                 `json:"ring"`
	DryRun          bool                `json:"dry_run"`
	Status          string              `json:"status"`
	BotsStopped     int                 `json:"bots_stopped"`
	BotsFailed      int                 `json:"bots_failed"`
	EnginesStopped  []string            `json:"engines_stopped"`
	EnginesFailed   map[string]string   `json:"engines_failed"`
	OrdersCancelled int                 `json:"orders_cancelled"`
	OrdersFailed    int                 `json:"orders_failed"`
	Positions       []ring0PositionView `json:"positions"`
	PositionsClosed int                 `json:"positions_closed"`
	PositionsFailed int                 `json:"positions_failed"`
	// PositionsListError is set when the open positions could not be listed:
	// nothing is known about them, so they are neither counted nor shown as
	// rows (an operator must not read it as "one position").
	PositionsListError string    `json:"positions_list_error,omitempty"`
	ActedBy            string    `json:"acted_by"`
	ActedAt            time.Time `json:"acted_at"`
}

type ring0PositionView struct {
	Symbol        string  `json:"symbol"`
	Side          string  `json:"side"`
	Size          float64 `json:"size"`
	UnrealizedPnL float64 `json:"unrealized_pnl"`
	Result        string  `json:"result"`
	Error         string  `json:"error,omitempty"`
}

// allEngines merges handler-local and process-wide registered engines.
func (h *KillSwitchHandler) allEngines() []killswitch.EngineStopper {
	out := append([]killswitch.EngineStopper{}, h.engines...)
	return append(out, killswitch.Engines()...)
}

// runBounded runs fn and gives up waiting after the context deadline, so a
// stuck Stop() cannot block the rest of ring 0.
func runBounded(ctx context.Context, fn func(context.Context) error) error {
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("panic: %v", r)
			}
		}()
		done <- fn(ctx)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("timed out: %w", ctx.Err())
	}
}

// Ring0 handles POST /api/v1/admin/killswitch/ring/0.
// Requires KYC level ≥ 4. Live mode needs confirm=="CERRAR TODO"; dry_run
// changes nothing and only reports what would be stopped/closed.
//
// Live steps (each best-effort; a failure never aborts the remaining steps):
//  1. set + persist the ring-0 flag (blocks risk-increasing paths immediately)
//  2. stop BotManager bots
//  3. stop every other registered in-process trading engine
//  4. cancel all open futures orders (incl. stop orders)
//  5. close EVERY open futures position (REST-direct) and mark the pair closed
//     in xsec_live_book
func (h *KillSwitchHandler) Ring0(c *gin.Context) {
	if kycLevel(c) < 4 {
		c.JSON(http.StatusForbidden, models.NewErrorResponse("FORBIDDEN", "KYC level ≥ 4 required for ring-0"))
		return
	}

	var req Ring0Request
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, models.NewErrorResponseWithDetails("VALIDATION_FAILED", "invalid request body", err.Error()))
		return
	}

	if !req.DryRun && req.Confirm != Ring0ConfirmPhrase {
		c.JSON(http.StatusBadRequest, models.NewErrorResponseWithDetails(
			"CONFIRMATION_REQUIRED",
			`confirm field must equal "CERRAR TODO"`,
			fmt.Sprintf("got: %q", req.Confirm),
		))
		return
	}

	actor := userIDFromContext(c)
	now := time.Now().UTC()
	res := &ring0Result{
		Ring:           0,
		DryRun:         req.DryRun,
		EnginesStopped: []string{},
		EnginesFailed:  map[string]string{},
		Positions:      []ring0PositionView{},
		ActedBy:        actor,
		ActedAt:        now,
	}

	if req.DryRun {
		h.ring0DryRun(c.Request.Context(), res)
		res.Status = "dry_run"
		c.JSON(http.StatusOK, models.NewSuccessResponse(res))
		return
	}

	// 1. Flag first: persist, then publish process-wide so concurrent trade
	// requests are refused immediately.
	if h.db != nil {
		row := killSwitchState{Ring: 0, Target: "global", Active: true, ActedBy: actor, ActedAt: now}
		h.db.Save(&row)
	}
	killswitch.SetRing0Active(true)
	h.mu.Lock()
	h.lastAction = "ring0_global_stop"
	h.actedBy = actor
	h.actedAt = &now
	h.mu.Unlock()
	h.writeAudit(actor, "ring0_global_stop", "global")

	// Detached from the request context on purpose: a client disconnect must
	// not abort a liquidation halfway.
	base := context.Background()

	h.ring0StopBots(base, res)
	h.ring0StopEngines(base, res)
	h.ring0CancelOrders(base, res)
	h.ring0ClosePositions(base, res)

	res.Status = "global_stop_active"
	h.writeAudit(actor, "ring0_executed", fmt.Sprintf(
		"bots_stopped=%d bots_failed=%d engines_stopped=%d engines_failed=%d orders_cancelled=%d orders_failed=%d positions_closed=%d positions_failed=%d positions_list_failed=%t",
		res.BotsStopped, res.BotsFailed, len(res.EnginesStopped), len(res.EnginesFailed),
		res.OrdersCancelled, res.OrdersFailed, res.PositionsClosed, res.PositionsFailed, res.PositionsListError != ""))

	c.JSON(http.StatusOK, models.NewSuccessResponse(res))
}

// ring0DryRun reports what a live ring 0 would touch, changing nothing.
func (h *KillSwitchHandler) ring0DryRun(parent context.Context, res *ring0Result) {
	if h.botManager != nil {
		ctx, cancel := context.WithTimeout(parent, ring0ListTimeout)
		stopped, failed := h.botManager.EmergencyStopAll(ctx, true)
		cancel()
		res.BotsStopped = len(stopped)
		res.BotsFailed = len(failed)
	}
	for _, e := range h.allEngines() {
		res.EnginesStopped = append(res.EnginesStopped, e.Name())
	}
	if h.exchange == nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, ring0ListTimeout)
	defer cancel()
	positions, err := h.exchange.ListOpenPositions(ctx)
	if err != nil {
		h.log.WithError(err).Warn("ring0 dry-run: listing positions failed")
		res.PositionsListError = err.Error()
		return
	}
	for _, p := range positions {
		res.Positions = append(res.Positions, ring0PositionView{
			Symbol: p.Symbol, Side: p.Side, Size: p.Size, UnrealizedPnL: p.UnrealizedPnL, Result: ring0ResultWouldCls,
		})
	}
}

func (h *KillSwitchHandler) ring0StopBots(base context.Context, res *ring0Result) {
	if h.botManager == nil {
		return
	}
	ctx, cancel := context.WithTimeout(base, ring0BotsTimeout)
	defer cancel()
	var stopped []string
	var failed map[string]string
	err := runBounded(ctx, func(c context.Context) error {
		stopped, failed = h.botManager.EmergencyStopAll(c, false)
		return nil
	})
	if err != nil {
		h.log.WithError(err).Error("ring0: BotManager.EmergencyStopAll did not complete")
		res.BotsFailed++
		return
	}
	res.BotsStopped = len(stopped)
	res.BotsFailed = len(failed)
	h.log.WithFields(logrus.Fields{"stopped": len(stopped), "failed": len(failed)}).Warn("ring0: EmergencyStopAll completed")
}

func (h *KillSwitchHandler) ring0StopEngines(base context.Context, res *ring0Result) {
	for _, e := range h.allEngines() {
		ctx, cancel := context.WithTimeout(base, ring0EngineTimeout)
		err := runBounded(ctx, e.Stop)
		cancel()
		if err != nil {
			h.log.WithError(err).WithField("engine", e.Name()).Error("ring0: engine stop failed")
			res.EnginesFailed[e.Name()] = err.Error()
			continue
		}
		res.EnginesStopped = append(res.EnginesStopped, e.Name())
	}
}

func (h *KillSwitchHandler) ring0CancelOrders(base context.Context, res *ring0Result) {
	if h.exchange == nil {
		return
	}
	ctx, cancel := context.WithTimeout(base, ring0OrdersTimeout)
	defer cancel()
	n, err := h.exchange.CancelAllOrders(ctx)
	res.OrdersCancelled += n
	if err != nil {
		h.log.WithError(err).Error("ring0: cancel-all orders failed")
		res.OrdersFailed++
	}
}

// ring0ClosePositions closes every open position, then re-lists once to catch
// anything an engine managed to open or fill while shutting down.
func (h *KillSwitchHandler) ring0ClosePositions(base context.Context, res *ring0Result) {
	if h.exchange == nil {
		return
	}
	index := map[string]int{} // symbol -> index in res.Positions

	for pass := 0; pass < 2; pass++ {
		if pass == 1 {
			time.Sleep(ring0ResweepSettle)
			// Late fills / late stop orders from engines that were mid-flight.
			h.ring0CancelOrders(base, res)
		}
		lctx, lcancel := context.WithTimeout(base, ring0ListTimeout)
		positions, err := h.exchange.ListOpenPositions(lctx)
		lcancel()
		if err != nil {
			h.log.WithError(err).Error("ring0: listing positions failed")
			// Reported whichever pass failed: after a failed re-sweep there may
			// be late positions nobody has seen.
			res.PositionsListError = err.Error()
			continue
		}
		res.PositionsListError = "" // a later successful sweep supersedes it
		for _, p := range positions {
			view := ring0PositionView{Symbol: p.Symbol, Side: p.Side, Size: p.Size, UnrealizedPnL: p.UnrealizedPnL}
			cctx, ccancel := context.WithTimeout(base, ring0CloseTimeout)
			cerr := runBounded(cctx, func(c context.Context) error { return h.exchange.ClosePosition(c, p.Symbol) })
			ccancel()
			if cerr != nil {
				h.log.WithError(cerr).WithField("symbol", p.Symbol).Error("ring0: close position failed")
				view.Result, view.Error = ring0ResultFailed, cerr.Error()
			} else {
				view.Result = ring0ResultOK
				if h.liveBook != nil {
					if merr := h.liveBook.MarkClosed(p.Symbol); merr != nil {
						// Closed on the exchange; only the registry is stale.
						h.log.WithError(merr).WithField("symbol", p.Symbol).Warn("ring0: xsec_live_book update failed")
						view.Error = "closed; live-book update failed: " + merr.Error()
					}
				}
			}
			if i, seen := index[p.Symbol]; seen {
				res.Positions[i] = view
			} else {
				index[p.Symbol] = len(res.Positions)
				res.Positions = append(res.Positions, view)
			}
		}
		if len(positions) == 0 {
			break
		}
	}

	// Final verification: a close call can return nil without the position
	// actually being flat (stale cache, rejected order). Re-list once more and
	// downgrade any symbol still open to "failed" so the operator never sees a
	// false "closed".
	if len(index) > 0 {
		time.Sleep(ring0ResweepSettle)
		vctx, vcancel := context.WithTimeout(base, ring0ListTimeout)
		still, verr := h.exchange.ListOpenPositions(vctx)
		vcancel()
		if verr != nil {
			h.log.WithError(verr).Warn("ring0: final verification listing failed")
		} else {
			for _, p := range still {
				if i, seen := index[p.Symbol]; seen && res.Positions[i].Result == ring0ResultOK {
					res.Positions[i].Result = ring0ResultFailed
					res.Positions[i].Error = "still open on exchange after close order"
				}
			}
		}
	}

	for _, v := range res.Positions {
		switch v.Result {
		case ring0ResultOK:
			res.PositionsClosed++
		case ring0ResultFailed:
			if v.Symbol != "" {
				res.PositionsFailed++
			}
		}
	}
}

// Ring0Reset handles POST /api/v1/admin/killswitch/ring/0/reset.
// Clears the ring-0 flag in memory and DB. It does NOT restart any engine.
func (h *KillSwitchHandler) Ring0Reset(c *gin.Context) {
	if kycLevel(c) < 4 {
		c.JSON(http.StatusForbidden, models.NewErrorResponse("FORBIDDEN", "KYC level ≥ 4 required for ring-0 reset"))
		return
	}
	actor := userIDFromContext(c)
	now := time.Now().UTC()
	was := killswitch.Ring0Active()

	if h.db != nil {
		if err := h.db.Model(&killSwitchState{}).
			Where("ring = ? AND target = ?", 0, "global").
			Update("active", false).Error; err != nil {
			// Keep the flag on: clearing only in memory would come back on restart.
			c.JSON(http.StatusInternalServerError, models.NewErrorResponseWithDetails(
				"PERSIST_FAILED", "could not clear ring-0 state in DB; flag left active", err.Error()))
			return
		}
	}
	killswitch.SetRing0Active(false)
	h.mu.Lock()
	h.lastAction = "ring0_reset"
	h.actedBy = actor
	h.actedAt = &now
	h.mu.Unlock()
	h.writeAudit(actor, "ring0_reset", "global")

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"ring":         0,
		"status":       "reset",
		"was_active":   was,
		"ring0_active": killswitch.Ring0Active(),
		"note":         "engines are NOT restarted; start the ones you want manually",
		"acted_by":     actor,
		"acted_at":     now,
	}))
}

// Ring0Guard is a gin middleware that refuses risk-increasing requests with
// HTTP 423 / KILLSWITCH_ACTIVE while ring 0 is active.
func Ring0Guard() gin.HandlerFunc {
	return func(c *gin.Context) {
		if killswitch.Ring0Active() {
			c.AbortWithStatusJSON(http.StatusLocked, models.NewErrorResponse(killswitch.ErrorCode, killswitch.ErrActive.Error()))
			return
		}
		c.Next()
	}
}

// ─── Production adapters ───────────────────────────────────────────────

// gctRing0Exchange implements Ring0Exchange over the direct KuCoin REST path.
type gctRing0Exchange struct {
	base     gct.GCTClient
	exchange string
}

// NewGCTRing0Exchange builds the production Ring0Exchange. exchange defaults
// to "kucoin". Returns nil when base is nil.
func NewGCTRing0Exchange(base gct.GCTClient, exchange string) Ring0Exchange {
	if base == nil {
		return nil
	}
	if exchange == "" {
		exchange = "kucoin"
	}
	return &gctRing0Exchange{base: base, exchange: exchange}
}

func (g *gctRing0Exchange) ListOpenPositions(ctx context.Context) ([]Ring0Position, error) {
	positions, err := g.base.GetFuturesPositions(ctx, g.exchange)
	if err != nil {
		return nil, err
	}
	out := make([]Ring0Position, 0, len(positions))
	for _, p := range positions {
		if p == nil || p.Size == 0 {
			continue
		}
		out = append(out, Ring0Position{
			Symbol: toFuturesSymbol(p.Symbol), Side: p.Side, Size: p.Size, UnrealizedPnL: p.UnrealizedPnL,
		})
	}
	return out, nil
}

func (g *gctRing0Exchange) ClosePosition(ctx context.Context, symbol string) error {
	return g.base.CloseFuturesPosition(ctx, g.exchange, toFuturesSymbol(symbol))
}

// allOrdersCanceller is the narrow capability of the concrete gct client.
type allOrdersCanceller interface {
	CancelAllFuturesOrders(ctx context.Context, exchange string) (int, error)
}

func (g *gctRing0Exchange) CancelAllOrders(ctx context.Context) (int, error) {
	c, ok := g.base.(allOrdersCanceller)
	if !ok {
		return 0, errors.New("exchange client does not support cancel-all-orders")
	}
	return c.CancelAllFuturesOrders(ctx, g.exchange)
}

// sqlLiveBookMarker marks pairs closed in xsec_live_book.
type sqlLiveBookMarker struct{ db *sql.DB }

// NewSQLLiveBookMarker builds a LiveBookMarker over the shared trading DB.
func NewSQLLiveBookMarker(db *sql.DB) LiveBookMarker {
	if db == nil {
		return nil
	}
	return &sqlLiveBookMarker{db: db}
}

// MarkClosed converts a futures symbol ("QNTUSDTM") to the registry's
// "BASE-USDT" key and closes its entry. A missing row is a harmless no-op.
func (m *sqlLiveBookMarker) MarkClosed(symbol string) error {
	return xsec.CloseLiveBookEntry(m.db, liveBookPairFromSymbol(symbol))
}

// liveBookPairFromSymbol maps "QNTUSDTM" -> "QNT-USDT" and "XBTUSDTM" -> "BTC-USDT".
func liveBookPairFromSymbol(symbol string) string {
	s := strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(symbol, "-", ""), "_", ""))
	s = strings.TrimSuffix(s, "M")
	base := strings.TrimSuffix(s, "USDT")
	if base == "XBT" {
		base = "BTC"
	}
	return base + "-USDT"
}
