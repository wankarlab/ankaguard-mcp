// Package handlers — AnkaAI Console engine stats endpoints.
//
// GET /api/v1/engine/stats       — live engine health: feature store, edges, GBM, AUM
// GET /api/v1/flywheel/events    — chronological event log (gate decisions, trade closes, etc.)
// GET /api/v1/capital/ladder     — current step in the paper→shadow→micro→real capital ladder
//
// All three handlers are served from EngineHandler. The constructor ensures
// the flywheel_events table exists (idempotent, safe to call on every boot).
package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/sirupsen/logrus"
)

// statsCacheTTL bounds how often GetStats re-runs its underlying queries — in
// particular COUNT(*) FROM feature_store, a full scan over a table that grows
// past 1.8M rows. Two independent frontend pollers (the global nav ticker and
// the flywheel page) each hit this endpoint every 30s; without a cache,
// concurrent tabs fan that out into parallel multi-second scans that contend
// with the live bot's own writes and can stall long enough to reset the
// client connection. The data is a dashboard vanity metric, not something
// that needs per-request freshness.
const statsCacheTTL = 20 * time.Second

// ── response structs ──────────────────────────────────────────────────────────

// EngineStatsResponse carries live engine health figures for the AnkaAI Console.
type EngineStatsResponse struct {
	FeatureStoreRows    int64   `json:"feature_store_rows"`
	EdgesLive           int     `json:"edges_live"`
	EdgesTotal          int     `json:"edges_total"`
	GatePassRate        float64 `json:"gate_pass_rate"` // 0-100 %
	GbmPfOos            float64 `json:"gbm_pf_oos"`
	AumCert             float64 `json:"aum_cert"`
	SpecsCertifiedToday int     `json:"specs_certified_today"`
	SpecsInQueue        int     `json:"specs_in_queue"`
}

// FlywheelEvent represents a single entry in the AnkaAI flywheel event log.
type FlywheelEvent struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"` // gate_decision | trade_close | spec_promoted | retrain | cohort
	Message   string    `json:"message"`
	CapState  string    `json:"cap_state"` // paper | shadow | micro | real
	Timestamp time.Time `json:"ts"`
}

// FlywheelEventsResponse wraps the event slice.
type FlywheelEventsResponse struct {
	Events []FlywheelEvent `json:"events"`
	Total  int             `json:"total"`
}

// CapitalLadder describes the current position in the capital escalation ladder.
type CapitalLadder struct {
	CurrentStep int          `json:"current_step"`
	Steps       []LadderStep `json:"steps"`
}

// LadderStep is one rung of the ladder.
type LadderStep struct {
	Name       string  `json:"name"`
	State      string  `json:"state"` // active | done | locked
	CapitalUSD float64 `json:"capital_usd"`
}

// ── handler ───────────────────────────────────────────────────────────────────

// EngineHandler serves the AnkaAI Console engine/flywheel/capital endpoints.
type EngineHandler struct {
	db  *sql.DB
	log *logrus.Logger

	statsMu       sync.Mutex
	statsCache    EngineStatsResponse
	statsCachedAt time.Time
}

// NewEngineHandler creates an EngineHandler and ensures the flywheel_events
// table exists in the database. db may be nil (endpoints return defaults).
func NewEngineHandler(db *sql.DB, log *logrus.Logger) *EngineHandler {
	if log == nil {
		log = logrus.New()
	}
	h := &EngineHandler{db: db, log: log}
	if db != nil {
		if err := h.ensureFlywheelTable(); err != nil {
			log.WithError(err).Warn("engine_handler: could not create flywheel_events table")
		}
	}
	return h
}

// ensureFlywheelTable creates flywheel_events if it does not exist yet.
func (h *EngineHandler) ensureFlywheelTable() error {
	_, err := h.db.Exec(`CREATE TABLE IF NOT EXISTS flywheel_events (
		id         TEXT PRIMARY KEY,
		kind       TEXT,
		message    TEXT,
		cap_state  TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	return err
}

// ── GetStats ──────────────────────────────────────────────────────────────────

// GetStats handles GET /api/v1/engine/stats.
// Reads live figures from SQLite and returns them as EngineStatsResponse.
// Missing tables or empty databases return safe zero-value defaults.
func (h *EngineHandler) GetStats(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusOK, models.NewSuccessResponse(EngineStatsResponse{GbmPfOos: 0, AumCert: 0}))
		return
	}

	h.statsMu.Lock()
	if time.Since(h.statsCachedAt) < statsCacheTTL {
		cached := h.statsCache
		h.statsMu.Unlock()
		c.JSON(http.StatusOK, models.NewSuccessResponse(cached))
		return
	}
	h.statsMu.Unlock()

	resp := EngineStatsResponse{
		GbmPfOos: 0,
		AumCert:  0,
	}

	// feature_store row count
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM feature_store`).Scan(&resp.FeatureStoreRows); err != nil {
		h.log.WithError(err).Debug("engine/stats: feature_store query skipped")
	}

	// strategy_validation: certified (pass=1, not expired) vs total
	today := time.Now().Format("2006-01-02")
	_ = h.db.QueryRow(
		`SELECT COUNT(*) FROM strategy_validation WHERE pass = 1 AND expires_at > datetime('now')`,
	).Scan(&resp.EdgesLive)
	_ = h.db.QueryRow(
		`SELECT COUNT(*) FROM strategy_validation`,
	).Scan(&resp.EdgesTotal)

	// gate pass rate from strategy_validation
	if resp.EdgesTotal > 0 {
		resp.GatePassRate = float64(resp.EdgesLive) / float64(resp.EdgesTotal) * 100
	}

	// specs certified today (status='live', updated today)
	_ = h.db.QueryRow(
		`SELECT COUNT(*) FROM strategy_specs WHERE status = 'live' AND date(updated_at) = ?`, today,
	).Scan(&resp.SpecsCertifiedToday)

	// specs in queue (status='pending' or 'validating')
	_ = h.db.QueryRow(
		`SELECT COUNT(*) FROM strategy_specs WHERE status IN ('pending','validating')`,
	).Scan(&resp.SpecsInQueue)

	// GBM out-of-sample profit factor stored by ml pipeline in ml_metrics
	_ = h.db.QueryRow(
		`SELECT COALESCE(MAX(profit_factor),0) FROM ml_metrics WHERE split='oos' ORDER BY created_at DESC LIMIT 1`,
	).Scan(&resp.GbmPfOos)

	h.statsMu.Lock()
	h.statsCache = resp
	h.statsCachedAt = time.Now()
	h.statsMu.Unlock()

	c.JSON(http.StatusOK, models.NewSuccessResponse(resp))
}

// ── GetFlywheelEvents ─────────────────────────────────────────────────────────

// GetFlywheelEvents handles GET /api/v1/flywheel/events?limit=20.
// Reads from flywheel_events. If that table is empty, synthesises events from
// the last N closed trades in trade_history so the UI always has something to show.
func (h *EngineHandler) GetFlywheelEvents(c *gin.Context) {
	limit := 20
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	if h.db == nil {
		c.JSON(http.StatusOK, models.NewSuccessResponse(FlywheelEventsResponse{Events: []FlywheelEvent{}, Total: 0}))
		return
	}

	events, err := h.loadFlywheelEvents(limit)
	if err != nil {
		h.log.WithError(err).Error("engine/flywheel: load events failed")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", err.Error()))
		return
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(FlywheelEventsResponse{
		Events: events,
		Total:  len(events),
	}))
}

func (h *EngineHandler) loadFlywheelEvents(limit int) ([]FlywheelEvent, error) {
	rows, err := h.db.Query(
		`SELECT id, kind, message, cap_state, created_at FROM flywheel_events ORDER BY created_at DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		// table might not exist in an older DB — fall through to synthetic
		h.log.WithError(err).Debug("engine/flywheel: flywheel_events query failed, falling back to synthetic")
		return h.syntheticFlywheelEvents(limit)
	}
	defer rows.Close()

	var events []FlywheelEvent
	for rows.Next() {
		var ev FlywheelEvent
		var createdAt string
		if err := rows.Scan(&ev.ID, &ev.Kind, &ev.Message, &ev.CapState, &createdAt); err != nil {
			continue
		}
		if t, err := time.Parse("2006-01-02 15:04:05", createdAt); err == nil {
			ev.Timestamp = t
		} else {
			ev.Timestamp = time.Now()
		}
		events = append(events, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan flywheel rows: %w", err)
	}

	// If the table exists but is empty, supplement with synthetic events from trade_history.
	if len(events) == 0 {
		return h.syntheticFlywheelEvents(limit)
	}
	return events, nil
}

// syntheticFlywheelEvents generates flywheel events from trade_history when the
// dedicated table has no entries yet. This gives the UI data immediately.
func (h *EngineHandler) syntheticFlywheelEvents(limit int) ([]FlywheelEvent, error) {
	rows, err := h.db.Query(`
		SELECT trade_id, pair, side, status,
		       COALESCE(exit_reason, 'unknown') AS reason,
		       COALESCE(net_pnl, 0)             AS net_pnl,
		       COALESCE(exit_time, created_at)  AS ts
		FROM trade_history
		WHERE status IN ('closed','cancelled')
		ORDER BY ts DESC
		LIMIT ?`, limit)
	if err != nil {
		// trade_history might also be absent on a fresh install
		return []FlywheelEvent{}, nil
	}
	defer rows.Close()

	var events []FlywheelEvent
	for rows.Next() {
		var tradeID, pair, side, status, reason, tsStr string
		var netPnL float64
		if err := rows.Scan(&tradeID, &pair, &side, &status, &reason, &netPnL, &tsStr); err != nil {
			continue
		}
		ts, ok := parseTradeHistoryTime(tsStr)
		if !ok {
			ts = time.Now()
		}
		sign := "+"
		if netPnL < 0 {
			sign = ""
		}
		events = append(events, FlywheelEvent{
			ID:        "th-" + tradeID,
			Kind:      "trade_close",
			Message:   fmt.Sprintf("%s %s %s — reason: %s  net: %s%.4f USD", pair, side, status, reason, sign, netPnL),
			CapState:  "real",
			Timestamp: ts,
		})
	}
	return events, rows.Err()
}

// tradeHistoryTimeLayouts lists the timestamp formats found in trade_history.
// go-sqlite3 stores time.Time values as "2006-01-02 15:04:05.999999999-07:00",
// which none of the plain layouts match.
var tradeHistoryTimeLayouts = []string{
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05Z",
	time.RFC3339Nano,
}

// parseTradeHistoryTime parses a trade_history timestamp string.
func parseTradeHistoryTime(s string) (time.Time, bool) {
	for _, layout := range tradeHistoryTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// ── GetCapitalLadder ──────────────────────────────────────────────────────────

// GetCapitalLadder handles GET /api/v1/capital/ladder.
// Returns the current step in the capital escalation ladder. The active step is
// read from the capital_ladder_state table (key='current_step'). If that table
// does not exist the default ladder is returned with step 0 (paper) active.
func (h *EngineHandler) GetCapitalLadder(c *gin.Context) {
	defaultLadder := CapitalLadder{
		CurrentStep: 0,
		Steps: []LadderStep{
			{Name: "paper", State: "active", CapitalUSD: 0},
			{Name: "shadow", State: "locked", CapitalUSD: 0},
			{Name: "micro", State: "locked", CapitalUSD: 50},
			{Name: "real", State: "locked", CapitalUSD: 500},
		},
	}

	if h.db == nil {
		c.JSON(http.StatusOK, models.NewSuccessResponse(defaultLadder))
		return
	}

	ladder, err := h.loadCapitalLadder(defaultLadder)
	if err != nil {
		h.log.WithError(err).Error("engine/capital: load ladder failed")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", err.Error()))
		return
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(ladder))
}

func (h *EngineHandler) loadCapitalLadder(fallback CapitalLadder) (CapitalLadder, error) {
	// Ensure the state table exists.
	_, _ = h.db.Exec(`CREATE TABLE IF NOT EXISTS capital_ladder_state (
		key   TEXT PRIMARY KEY,
		value TEXT
	)`)

	var currentStepStr string
	err := h.db.QueryRow(`SELECT value FROM capital_ladder_state WHERE key = 'current_step'`).Scan(&currentStepStr)
	if err != nil {
		// No row yet — return default.
		return fallback, nil
	}

	currentStep, err := strconv.Atoi(currentStepStr)
	if err != nil || currentStep < 0 || currentStep >= len(fallback.Steps) {
		return fallback, nil
	}

	// Annotate states: done < currentStep, active == currentStep, locked > currentStep.
	ladder := CapitalLadder{CurrentStep: currentStep, Steps: make([]LadderStep, len(fallback.Steps))}
	for i, s := range fallback.Steps {
		step := LadderStep{Name: s.Name, CapitalUSD: s.CapitalUSD}
		switch {
		case i < currentStep:
			step.State = "done"
		case i == currentStep:
			step.State = "active"
		default:
			step.State = "locked"
		}
		ladder.Steps[i] = step
	}

	// Try to load per-step capital amounts from the DB if stored.
	rows, err := h.db.Query(`SELECT key, value FROM capital_ladder_state WHERE key LIKE 'step_%_capital'`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var k, v string
			if err := rows.Scan(&k, &v); err != nil {
				continue
			}
			// key format: step_0_capital, step_1_capital, …
			var idx int
			if n, err := fmt.Sscanf(k, "step_%d_capital", &idx); n == 1 && err == nil && idx < len(ladder.Steps) {
				if cap, err := strconv.ParseFloat(v, 64); err == nil {
					ladder.Steps[idx].CapitalUSD = cap
				}
			}
		}
	}

	return ladder, nil
}

// ROUTE_MANIFEST: GET /api/v1/engine/stats -> engineHandler.GetStats | GET /api/v1/flywheel/events -> engineHandler.GetFlywheelEvents | GET /api/v1/capital/ladder -> engineHandler.GetCapitalLadder
