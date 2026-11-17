// Package handlers — AnkaAI Explicabilidad endpoints.
//
// GET  /api/v1/trades/:id/explain  — explicabilidad de un trade (gates, chain, replay)
// POST /api/v1/trades/:id/feedback — flag-as-wrong para retrain
//
// ROUTE_MANIFEST: GET /api/v1/trades/:id/explain -> explainHandler.GetExplain | POST /api/v1/trades/:id/feedback -> explainHandler.PostFeedback
package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// ──────────────────────────────────────────────────────────────────────────────
// Domain types
// ──────────────────────────────────────────────────────────────────────────────

// GateStep represents one gate in the decision chain (regime, ml_gbm, etc.).
type GateStep struct {
	Name   string  `json:"name"`
	Passed bool    `json:"passed"`
	Reason string  `json:"reason"`
	Score  float64 `json:"score,omitempty"`
}

// ChainStep is a single reasoning step (input → output) in the explanation chain.
type ChainStep struct {
	Step   string `json:"step"`
	Input  string `json:"input"`
	Output string `json:"output"`
}

// Candle is a single OHLC candle for the replay view.
type Candle struct {
	T int64   `json:"t"`
	O float64 `json:"o"`
	H float64 `json:"h"`
	L float64 `json:"l"`
	C float64 `json:"c"`
}

// ReplayData bundles the narrative text and surrounding candles for the trade.
type ReplayData struct {
	Narrative string   `json:"narrative"`
	Candles   []Candle `json:"candles"`
}

// ExplainResponse is the full explainability payload returned to the client.
type ExplainResponse struct {
	TradeID string      `json:"trade_id"`
	Pair    string      `json:"pair"`
	Gates   []GateStep  `json:"gates"`
	Chain   []ChainStep `json:"chain"`
	Replay  ReplayData  `json:"replay"`
}

// FeedbackRequest is the body for the POST /feedback endpoint.
type FeedbackRequest struct {
	Label string `json:"label" binding:"required,oneof=wrong_direction wrong_regime wrong_timing ok"`
	Note  string `json:"note,omitempty"`
}

// ──────────────────────────────────────────────────────────────────────────────
// Internal scan target for trade_history
// ──────────────────────────────────────────────────────────────────────────────

type tradeRow struct {
	Pair          string
	Side          string
	Regime        string
	MLProbability float64
	PnlPct        float64
	EntryPrice    float64
	ClosePrice    float64
	OpenedAt      string
}

// ──────────────────────────────────────────────────────────────────────────────
// Handler
// ──────────────────────────────────────────────────────────────────────────────

// ExplainHandler handles trade explicability and feedback endpoints.
type ExplainHandler struct {
	db  *gorm.DB
	log *logrus.Logger
}

// NewExplainHandler constructs an ExplainHandler and ensures the trade_feedback
// table exists in SQLite.
func NewExplainHandler(db *gorm.DB, log *logrus.Logger) *ExplainHandler {
	h := &ExplainHandler{db: db, log: log}
	h.ensureSchema()
	return h
}

// ensureSchema creates the trade_feedback table if it does not exist.
func (h *ExplainHandler) ensureSchema() {
	sql := `CREATE TABLE IF NOT EXISTS trade_feedback (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		trade_id   TEXT    NOT NULL,
		user_id    TEXT    NOT NULL,
		label      TEXT    NOT NULL,
		note       TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`
	if err := h.db.Exec(sql).Error; err != nil {
		h.log.WithError(err).Error("explain_handler: failed to create trade_feedback table")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// GET /api/v1/trades/:id/explain
// ──────────────────────────────────────────────────────────────────────────────

// GetExplain returns the full explainability payload for a single trade.
func (h *ExplainHandler) GetExplain(c *gin.Context) {
	tradeID := c.Param("id")
	userID := userIDFromContext(c)

	var row tradeRow
	query := `SELECT pair, side, regime, ml_probability, pnl_pct, entry_price, close_price, opened_at
	          FROM trade_history WHERE id = ? AND user_id = ?`

	result := h.db.Raw(query, tradeID, userID).Scan(&row)
	if result.Error != nil {
		h.log.WithError(result.Error).WithField("trade_id", tradeID).Error("explain_handler: db error fetching trade")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database error"})
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "trade not found"})
		return
	}

	gates := buildGates(row)
	chain := buildChain(row)
	replay := buildReplay(row)

	c.JSON(http.StatusOK, ExplainResponse{
		TradeID: tradeID,
		Pair:    row.Pair,
		Gates:   gates,
		Chain:   chain,
		Replay:  replay,
	})
}

// ──────────────────────────────────────────────────────────────────────────────
// GET /api/v1/trades/history?limit=N
// ──────────────────────────────────────────────────────────────────────────────

// historyRow is the scan target for the trade_history list query.
// Only columns that exist in the live schema are selected (regime/vol_tier are
// not stored in trade_history yet — see ExplainHandler schema-drift note).
type historyRow struct {
	TradeID   string  `json:"id"`
	Pair      string  `json:"pair"`
	Side      string  `json:"side"`
	Pnl       float64 `json:"pnl"`
	PnlPct    float64 `json:"pnl_pct"`
	Regime    string  `json:"regime"`
	VolTier   string  `json:"vol_tier"`
	Note      string  `json:"note,omitempty"`
	OpenedAt  string  `json:"opened_at"`
	ClosedAt  string  `json:"closed_at"`
}

// GetHistory returns the most recent closed trades for the authenticated user,
// shaped for the Anka Arena ReplayFeed ({ trades: TradeRecord[] }).
func (h *ExplainHandler) GetHistory(c *gin.Context) {
	userID := userIDFromContext(c)

	limit := 20
	if v := c.Query("limit"); v != "" {
		if n, err := fmt.Sscanf(v, "%d", &limit); err != nil || n != 1 {
			limit = 20
		}
	}
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	type rawRow struct {
		TradeID    string
		Pair       string
		Side       string
		Pnl        float64
		PnlPct     float64
		ExitReason string
		EntryTime  string
		ExitTime   string
	}

	var rows []rawRow
	query := `SELECT trade_id, pair, side, pnl, pnl_pct, exit_reason, entry_time, exit_time
	          FROM trade_history
	          WHERE user_id = ? AND status = 'closed'
	          ORDER BY exit_time DESC
	          LIMIT ?`
	if err := h.db.Raw(query, userID, limit).Scan(&rows).Error; err != nil {
		h.log.WithError(err).Error("explain_handler: db error fetching trade history")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database error"})
		return
	}

	trades := make([]historyRow, 0, len(rows))
	for _, r := range rows {
		side := "LONG"
		switch r.Side {
		case "sell", "SELL", "short", "SHORT":
			side = "SHORT"
		}
		trades = append(trades, historyRow{
			TradeID:  r.TradeID,
			Pair:     r.Pair,
			Side:     side,
			Pnl:      r.Pnl,
			PnlPct:   r.PnlPct,
			Regime:   "", // not stored in trade_history yet
			VolTier:  "",
			Note:     r.ExitReason,
			OpenedAt: r.EntryTime,
			ClosedAt: r.ExitTime,
		})
	}

	c.JSON(http.StatusOK, gin.H{"trades": trades})
}

// buildGates constructs the gate steps from trade_history fields.
func buildGates(row tradeRow) []GateStep {
	gates := make([]GateStep, 0, 4)

	// Gate 1 — regime
	regimePassed := row.Regime == "TRENDING" || row.Regime == "BREAKOUT"
	regimeReason := fmt.Sprintf("régimen detectado: %s", row.Regime)
	if row.Regime == "" {
		regimeReason = "régimen no registrado"
	}
	gates = append(gates, GateStep{
		Name:   "regime",
		Passed: regimePassed,
		Reason: regimeReason,
	})

	// Gate 2 — ml_gbm
	mlPassed := row.MLProbability >= 0.5
	mlReason := fmt.Sprintf("GBM probabilidad: %.0f%%", row.MLProbability*100)
	if row.MLProbability == 0 {
		mlReason = "GBM sin voto registrado"
		mlPassed = false
	}
	gates = append(gates, GateStep{
		Name:   "ml_gbm",
		Passed: mlPassed,
		Reason: mlReason,
		Score:  row.MLProbability,
	})

	// Gate 3 — trend_filter (inferred from side consistency; no dedicated column)
	trendPassed := row.Side != ""
	trendReason := "filtro de tendencia aplicado"
	if !trendPassed {
		trendReason = "dirección no registrada"
	}
	gates = append(gates, GateStep{
		Name:   "trend_filter",
		Passed: trendPassed,
		Reason: trendReason,
	})

	// Gate 4 — llm_vote (not yet stored in trade_history; surfaced as informational)
	gates = append(gates, GateStep{
		Name:   "llm_vote",
		Passed: false,
		Reason: "voto LLM no registrado en este trade",
	})

	return gates
}

// buildChain constructs the reasoning chain from trade data.
func buildChain(row tradeRow) []ChainStep {
	chain := []ChainStep{
		{
			Step:   "signal_generation",
			Input:  fmt.Sprintf("par=%s side=%s", row.Pair, row.Side),
			Output: fmt.Sprintf("señal generada: %s %s", row.Side, row.Pair),
		},
		{
			Step:   "regime_check",
			Input:  fmt.Sprintf("regime=%s", row.Regime),
			Output: fmt.Sprintf("régimen evaluado: %s", row.Regime),
		},
		{
			Step:   "ml_gate",
			Input:  fmt.Sprintf("ml_probability=%.4f", row.MLProbability),
			Output: fmt.Sprintf("probabilidad GBM: %.0f%%", row.MLProbability*100),
		},
		{
			Step:   "execution",
			Input:  fmt.Sprintf("entry_price=%.6f", row.EntryPrice),
			Output: fmt.Sprintf("orden ejecutada en %.6f, cerrada en %.6f (PnL %.2f%%)", row.EntryPrice, row.ClosePrice, row.PnlPct),
		},
	}
	return chain
}

// buildReplay builds the narrative and returns an empty candle slice when no
// candle_archive reference is available in trade_history.
func buildReplay(row tradeRow) ReplayData {
	side := row.Side
	if side == "" {
		side = "UNKNOWN"
	}
	regime := row.Regime
	if regime == "" {
		regime = "UNKNOWN"
	}
	prob := row.MLProbability

	narrative := fmt.Sprintf(
		"El sistema entró %s %s porque el régimen era %s y el GBM dio probabilidad %.0f%%.",
		row.Pair, side, regime, prob*100,
	)

	// candle_archive is not stored in the current trade_history schema;
	// return an empty slice so the client can render the placeholder state.
	return ReplayData{
		Narrative: narrative,
		Candles:   []Candle{},
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// POST /api/v1/trades/:id/feedback
// ──────────────────────────────────────────────────────────────────────────────

// PostFeedback records a user flag (wrong_direction / wrong_regime / wrong_timing / ok)
// for a trade and stores it in trade_feedback for retrain pipelines.
func (h *ExplainHandler) PostFeedback(c *gin.Context) {
	tradeID := c.Param("id")
	userID := userIDFromContext(c)

	var req FeedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Verify trade exists and belongs to this user.
	var count int64
	if err := h.db.Raw("SELECT COUNT(*) FROM trade_history WHERE id = ? AND user_id = ?", tradeID, userID).Scan(&count).Error; err != nil {
		h.log.WithError(err).WithField("trade_id", tradeID).Error("explain_handler: db error verifying trade ownership")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database error"})
		return
	}
	if count == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "trade not found"})
		return
	}

	insert := `INSERT INTO trade_feedback (trade_id, user_id, label, note) VALUES (?, ?, ?, ?)`
	if err := h.db.Exec(insert, tradeID, userID, req.Label, req.Note).Error; err != nil {
		h.log.WithError(err).WithField("trade_id", tradeID).Error("explain_handler: failed to insert feedback")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save feedback"})
		return
	}

	h.log.WithFields(logrus.Fields{
		"trade_id": tradeID,
		"user_id":  userID,
		"label":    req.Label,
	}).Info("explain_handler: feedback recorded")

	c.JSON(http.StatusOK, gin.H{"status": "ok", "trade_id": tradeID, "label": req.Label})
}
