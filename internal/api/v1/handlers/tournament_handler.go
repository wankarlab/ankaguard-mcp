// Package handlers — AnkaAI Arena tournament REST endpoints.
//
// GET  /api/v1/tournament                      — list active tournaments
// GET  /api/v1/tournament/:id                  — tournament detail (competitors + current round)
// GET  /api/v1/tournament/:id/leaderboard      — leaderboard snapshot
// POST /api/v1/tournament/:id/vote             — cast audience vote (1 vote/round/user)
// POST /api/v1/admin/tournament                — create tournament (level 4+)
// POST /api/v1/admin/tournament/:id/control    — start / pause / end (level 3+)
//
// capital_state="real" is blocked until SEC-5/6/10 are implemented (returns 400).
//
// ROUTE_MANIFEST: GET /api/v1/tournament -> tournamentHandler.List | GET /api/v1/tournament/:id -> tournamentHandler.Get | GET /api/v1/tournament/:id/leaderboard -> tournamentHandler.GetLeaderboard | POST /api/v1/tournament/:id/vote -> tournamentHandler.Vote | POST /api/v1/admin/tournament -> tournamentHandler.Create | POST /api/v1/admin/tournament/:id/control -> tournamentHandler.Control
package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/rantipay/trading-mcp/internal/tournament/domain"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// ──────────────────────────────────────────────
// DB row structs (flat, no GORM model embedding)
// ──────────────────────────────────────────────

type tournamentRow struct {
	ID           string         `gorm:"column:id"`
	Name         string         `gorm:"column:name"`
	Format       string         `gorm:"column:format"`
	Status       string         `gorm:"column:status"`
	CapitalState string         `gorm:"column:capital_state"`
	ConfigJSON   string         `gorm:"column:config_json"`
	CreatedAt    time.Time      `gorm:"column:created_at"`
	StartedAt    sql.NullTime   `gorm:"column:started_at"`
	EndedAt      sql.NullTime   `gorm:"column:ended_at"`
}

func (tournamentRow) TableName() string { return "tournament" }

type competitorRow struct {
	ID           string    `gorm:"column:id"`
	TournamentID string    `gorm:"column:tournament_id"`
	Kind         string    `gorm:"column:kind"`
	Provider     string    `gorm:"column:provider"`
	SpecID       string    `gorm:"column:spec_id"`
	Subaccount   string    `gorm:"column:subaccount"`
	Rank         int       `gorm:"column:rank"`
	PnlUSD       float64   `gorm:"column:pnl_usd"`
	PnlPct       float64   `gorm:"column:pnl_pct"`
	TradesTotal  int       `gorm:"column:trades_total"`
	Wins         int       `gorm:"column:wins"`
	Status       string    `gorm:"column:status"`
	CreatedAt    time.Time `gorm:"column:created_at"`
}

func (competitorRow) TableName() string { return "tournament_competitor" }

type roundRow struct {
	ID                 string       `gorm:"column:id"`
	TournamentID       string       `gorm:"column:tournament_id"`
	RoundNumber        int          `gorm:"column:round_number"`
	Phase              string       `gorm:"column:phase"`
	Symbol             string       `gorm:"column:symbol"`
	StartedAt          sql.NullTime `gorm:"column:started_at"`
	EndedAt            sql.NullTime `gorm:"column:ended_at"`
	WinnerCompetitorID string       `gorm:"column:winner_competitor_id"`
	CreatedAt          time.Time    `gorm:"column:created_at"`
}

func (roundRow) TableName() string { return "tournament_round" }

type leaderboardSnapshotRow struct {
	ID           int64     `gorm:"column:id;primaryKey;autoIncrement"`
	TournamentID string    `gorm:"column:tournament_id"`
	TakenAt      time.Time `gorm:"column:taken_at"`
	EntriesJSON  string    `gorm:"column:entries_json"`
}

func (leaderboardSnapshotRow) TableName() string { return "tournament_leaderboard_snapshot" }

type voteRow struct {
	ID           string    `gorm:"column:id"`
	RoundID      string    `gorm:"column:round_id"`
	VoterID      string    `gorm:"column:voter_id"`
	CompetitorID string    `gorm:"column:competitor_id"`
	Side         string    `gorm:"column:side"`
	Weight       float64   `gorm:"column:weight"`
	Platform     string    `gorm:"column:platform"`
	CreatedAt    time.Time `gorm:"column:created_at"`
}

func (voteRow) TableName() string { return "tournament_vote" }

// ──────────────────────────────────────────────
// Handler
// ──────────────────────────────────────────────

// TournamentHandler exposes tournament management and audience interaction endpoints.
type TournamentHandler struct {
	db  *gorm.DB
	log *logrus.Logger
}

// NewTournamentHandler constructs a TournamentHandler.
// db may be nil (returns 503 on all requests).
func NewTournamentHandler(db *gorm.DB, log *logrus.Logger) *TournamentHandler {
	if log == nil {
		log = logrus.New()
	}
	h := &TournamentHandler{db: db, log: log}
	h.ensureSchema()
	return h
}

// ensureSchema creates the tournament tables if they do not exist (idempotent).
// Mirrors the SQLite-local pattern used by ExplainHandler so the endpoints work
// on a fresh DB without a separate migration step. No-op when db is nil.
func (h *TournamentHandler) ensureSchema() {
	if h.db == nil {
		return
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS tournament (
			id            TEXT PRIMARY KEY,
			name          TEXT NOT NULL,
			format        TEXT,
			status        TEXT,
			capital_state TEXT,
			config_json   TEXT,
			created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
			started_at    DATETIME,
			ended_at      DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS tournament_competitor (
			id            TEXT PRIMARY KEY,
			tournament_id TEXT NOT NULL,
			kind          TEXT,
			provider      TEXT,
			spec_id       TEXT,
			subaccount    TEXT,
			rank          INTEGER DEFAULT 0,
			pnl_usd       REAL    DEFAULT 0,
			pnl_pct       REAL    DEFAULT 0,
			trades_total  INTEGER DEFAULT 0,
			wins          INTEGER DEFAULT 0,
			status        TEXT,
			created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tcomp_tournament ON tournament_competitor(tournament_id)`,
		`CREATE TABLE IF NOT EXISTS tournament_round (
			id                   TEXT PRIMARY KEY,
			tournament_id        TEXT NOT NULL,
			round_number         INTEGER DEFAULT 0,
			phase                TEXT,
			symbol               TEXT,
			started_at           DATETIME,
			ended_at             DATETIME,
			winner_competitor_id TEXT,
			created_at           DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tround_tournament ON tournament_round(tournament_id)`,
		`CREATE TABLE IF NOT EXISTS tournament_leaderboard_snapshot (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			tournament_id TEXT NOT NULL,
			taken_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
			entries_json  TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tsnap_tournament ON tournament_leaderboard_snapshot(tournament_id)`,
		`CREATE TABLE IF NOT EXISTS tournament_vote (
			id            TEXT PRIMARY KEY,
			round_id      TEXT NOT NULL,
			voter_id      TEXT,
			competitor_id TEXT,
			side          TEXT,
			weight        REAL DEFAULT 1,
			platform      TEXT,
			created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tvote_round ON tournament_vote(round_id)`,
	}
	for _, s := range stmts {
		if err := h.db.Exec(s).Error; err != nil {
			h.log.WithError(err).Error("tournament_handler: ensureSchema failed")
		}
	}
}

// RegisterRoutes wires all tournament endpoints onto the provided Gin engine.
// Call after middleware is set up.
func (h *TournamentHandler) RegisterRoutes(router *gin.Engine) {
	v1 := router.Group("/api/v1")
	{
		t := v1.Group("/tournament")
		t.GET("", h.List)
		t.GET("/:id", h.Get)
		t.GET("/:id/leaderboard", h.GetLeaderboard)
		t.POST("/:id/vote", h.Vote)
	}

	admin := v1.Group("/admin")
	{
		admin.POST("/tournament", h.Create)
		admin.POST("/tournament/:id/control", h.Control)
	}
}

// ──────────────────────────────────────────────
// List — GET /api/v1/tournament
// ──────────────────────────────────────────────

// List returns all tournaments that are not ended.
// Query param ?status=all returns every status.
func (h *TournamentHandler) List(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("DB_UNAVAILABLE", "tournament store not configured"))
		return
	}

	statusFilter := c.Query("status")

	var rows []tournamentRow
	q := h.db.WithContext(c.Request.Context())
	if statusFilter == "" || statusFilter == "active" {
		q = q.Where("status != ?", string(domain.TournamentEnded))
	}
	if err := q.Order("created_at DESC").Find(&rows).Error; err != nil {
		h.log.WithError(err).Error("TournamentHandler.List: db error")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", "failed to list tournaments"))
		return
	}

	result := make([]domain.Tournament, 0, len(rows))
	for _, r := range rows {
		result = append(result, rowToTournament(r))
	}

	c.JSON(http.StatusOK, models.NewSuccessResponseWithMeta(result, &models.APIMeta{Total: len(result)}))
}

// ──────────────────────────────────────────────
// Get — GET /api/v1/tournament/:id
// ──────────────────────────────────────────────

// Get returns full tournament detail including competitors and the current (latest) round.
func (h *TournamentHandler) Get(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("DB_UNAVAILABLE", "tournament store not configured"))
		return
	}

	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("VALIDATION_FAILED", "tournament id is required"))
		return
	}

	ctx := c.Request.Context()

	var tr tournamentRow
	if err := h.db.WithContext(ctx).Where("id = ?", id).First(&tr).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, models.NewErrorResponse("NOT_FOUND", "tournament not found"))
			return
		}
		h.log.WithError(err).WithField("tournament_id", id).Error("TournamentHandler.Get: db error")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", "failed to fetch tournament"))
		return
	}

	t := rowToTournament(tr)

	// Load competitors.
	var compRows []competitorRow
	if err := h.db.WithContext(ctx).Where("tournament_id = ?", id).Order("rank ASC").Find(&compRows).Error; err != nil {
		h.log.WithError(err).WithField("tournament_id", id).Warn("TournamentHandler.Get: failed to load competitors")
	}
	for _, cr := range compRows {
		t.Competitors = append(t.Competitors, rowToCompetitor(cr))
	}

	// Load latest round.
	var latestRound roundRow
	if err := h.db.WithContext(ctx).
		Where("tournament_id = ?", id).
		Order("round_number DESC").
		First(&latestRound).Error; err == nil {
		t.Rounds = []domain.Round{rowToRound(latestRound)}
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(t))
}

// ──────────────────────────────────────────────
// GetLeaderboard — GET /api/v1/tournament/:id/leaderboard
// ──────────────────────────────────────────────

// GetLeaderboard returns the latest leaderboard snapshot, or falls back to live
// competitor rankings if no snapshot exists yet.
func (h *TournamentHandler) GetLeaderboard(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("DB_UNAVAILABLE", "tournament store not configured"))
		return
	}

	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("VALIDATION_FAILED", "tournament id is required"))
		return
	}

	ctx := c.Request.Context()

	// Try latest snapshot first.
	var snap leaderboardSnapshotRow
	err := h.db.WithContext(ctx).
		Where("tournament_id = ?", id).
		Order("taken_at DESC").
		First(&snap).Error

	if err == nil {
		// Parse snapshot entries.
		var entries interface{}
		if jsonErr := json.Unmarshal([]byte(snap.EntriesJSON), &entries); jsonErr != nil {
			entries = snap.EntriesJSON // return raw string on parse failure
		}
		c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
			"tournament_id": id,
			"taken_at":      snap.TakenAt,
			"entries":       entries,
			"source":        "snapshot",
		}))
		return
	}

	if !errors.Is(err, gorm.ErrRecordNotFound) {
		h.log.WithError(err).WithField("tournament_id", id).Error("TournamentHandler.GetLeaderboard: snapshot query error")
	}

	// Fallback: live competitor list ordered by pnl_usd.
	var compRows []competitorRow
	if dbErr := h.db.WithContext(ctx).
		Where("tournament_id = ?", id).
		Order("pnl_usd DESC").
		Find(&compRows).Error; dbErr != nil {
		h.log.WithError(dbErr).WithField("tournament_id", id).Error("TournamentHandler.GetLeaderboard: competitors query error")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", "failed to fetch leaderboard"))
		return
	}

	entries := make([]domain.Competitor, 0, len(compRows))
	for i, cr := range compRows {
		comp := rowToCompetitor(cr)
		comp.Rank = i + 1
		entries = append(entries, comp)
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"tournament_id": id,
		"taken_at":      time.Now(),
		"entries":       entries,
		"source":        "live",
	}))
}

// ──────────────────────────────────────────────
// Vote — POST /api/v1/tournament/:id/vote
// ──────────────────────────────────────────────

// voteRequest is the expected JSON body for the Vote endpoint.
type voteRequest struct {
	CompetitorID string `json:"competitor_id" binding:"required"`
	Side         string `json:"side"`     // support (default), against
	Platform     string `json:"platform"` // web (default)
}

// Vote casts an audience vote for a competitor in the active round.
// Enforces one vote per round per authenticated user via UNIQUE constraint.
func (h *TournamentHandler) Vote(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("DB_UNAVAILABLE", "tournament store not configured"))
		return
	}

	tournamentID := c.Param("id")
	if tournamentID == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("VALIDATION_FAILED", "tournament id is required"))
		return
	}

	var req voteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponseWithDetails("VALIDATION_FAILED", "invalid request body", err.Error()))
		return
	}

	voterID := userIDFromContext(c)

	// Default field values.
	if req.Side == "" {
		req.Side = "support"
	}
	if req.Platform == "" {
		req.Platform = "web"
	}

	ctx := c.Request.Context()

	// Resolve active round for this tournament.
	var activeRound roundRow
	if err := h.db.WithContext(ctx).
		Where("tournament_id = ? AND phase IN (?, ?)", tournamentID, string(domain.PhaseActive), string(domain.PhaseVoting)).
		Order("round_number DESC").
		First(&activeRound).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusConflict, models.NewErrorResponse("NO_ACTIVE_ROUND", "no round is currently accepting votes"))
			return
		}
		h.log.WithError(err).WithField("tournament_id", tournamentID).Error("TournamentHandler.Vote: round query error")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", "failed to resolve active round"))
		return
	}

	// Verify competitor belongs to this tournament.
	var count int64
	if err := h.db.WithContext(ctx).Model(&competitorRow{}).
		Where("id = ? AND tournament_id = ?", req.CompetitorID, tournamentID).
		Count(&count).Error; err != nil || count == 0 {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_COMPETITOR", "competitor does not belong to this tournament"))
		return
	}

	vote := voteRow{
		ID:           uuid.New().String(),
		RoundID:      activeRound.ID,
		VoterID:      voterID,
		CompetitorID: req.CompetitorID,
		Side:         req.Side,
		Weight:       1.0,
		Platform:     req.Platform,
		CreatedAt:    time.Now(),
	}

	if err := h.db.WithContext(ctx).Create(&vote).Error; err != nil {
		// SQLite/Postgres UNIQUE violation — already voted this round.
		if isUniqueViolation(err) {
			c.JSON(http.StatusConflict, models.NewErrorResponse("ALREADY_VOTED", "you have already voted in this round"))
			return
		}
		h.log.WithError(err).WithFields(logrus.Fields{
			"round_id":      activeRound.ID,
			"voter_id":      voterID,
			"competitor_id": req.CompetitorID,
		}).Error("TournamentHandler.Vote: insert error")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", "failed to record vote"))
		return
	}

	c.JSON(http.StatusCreated, models.NewSuccessResponse(gin.H{
		"vote_id":       vote.ID,
		"round_id":      vote.RoundID,
		"competitor_id": vote.CompetitorID,
		"side":          vote.Side,
		"platform":      vote.Platform,
	}))
}

// ──────────────────────────────────────────────
// Create — POST /api/v1/admin/tournament
// ──────────────────────────────────────────────

// createTournamentRequest is the expected JSON body for the Create endpoint.
type createTournamentRequest struct {
	Name         string                   `json:"name"          binding:"required"`
	Format       string                   `json:"format"`
	CapitalState string                   `json:"capital_state"`
	Config       domain.TournamentConfig  `json:"config"`
}

// Create creates a new tournament. Requires level 4+.
// capital_state="real" is rejected until SEC-5/6/10 are implemented.
func (h *TournamentHandler) Create(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("DB_UNAVAILABLE", "tournament store not configured"))
		return
	}

	// Authorization: level 4+ required.
	if !hasMinLevel(c, 4) {
		c.JSON(http.StatusForbidden, models.NewErrorResponse("FORBIDDEN", "creating tournaments requires level 4"))
		return
	}

	var req createTournamentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponseWithDetails("VALIDATION_FAILED", "invalid request body", err.Error()))
		return
	}

	// Guard: real capital blocked until compliance gates are closed.
	if req.CapitalState == string(domain.CapitalReal) {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse(
			"REAL_CAPITAL_BLOCKED",
			"capital_state=real is not yet available — SEC-5/6/10 compliance gates must be closed first. Use paper, shadow, or micro.",
		))
		return
	}

	// Defaults.
	if req.Format == "" {
		req.Format = "round_robin"
	}
	if req.CapitalState == "" {
		req.CapitalState = string(domain.CapitalPaper)
	}

	configBytes, err := json.Marshal(req.Config)
	if err != nil {
		configBytes = []byte("{}")
	}

	row := tournamentRow{
		ID:           uuid.New().String(),
		Name:         req.Name,
		Format:       req.Format,
		Status:       string(domain.TournamentPending),
		CapitalState: req.CapitalState,
		ConfigJSON:   string(configBytes),
		CreatedAt:    time.Now(),
	}

	if err := h.db.WithContext(c.Request.Context()).Create(&row).Error; err != nil {
		h.log.WithError(err).Error("TournamentHandler.Create: db insert error")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", "failed to create tournament"))
		return
	}

	c.JSON(http.StatusCreated, models.NewSuccessResponse(rowToTournament(row)))
}

// ──────────────────────────────────────────────
// Control — POST /api/v1/admin/tournament/:id/control
// ──────────────────────────────────────────────

// controlRequest is the expected JSON body for the Control endpoint.
type controlRequest struct {
	Action string `json:"action" binding:"required"` // start, pause, end
}

// Control transitions a tournament status. Requires level 3+.
func (h *TournamentHandler) Control(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("DB_UNAVAILABLE", "tournament store not configured"))
		return
	}

	// Authorization: level 3+ required.
	if !hasMinLevel(c, 3) {
		c.JSON(http.StatusForbidden, models.NewErrorResponse("FORBIDDEN", "tournament control requires level 3"))
		return
	}

	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("VALIDATION_FAILED", "tournament id is required"))
		return
	}

	var req controlRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponseWithDetails("VALIDATION_FAILED", "invalid request body", err.Error()))
		return
	}

	ctx := c.Request.Context()

	// Fetch current tournament.
	var tr tournamentRow
	if err := h.db.WithContext(ctx).Where("id = ?", id).First(&tr).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, models.NewErrorResponse("NOT_FOUND", "tournament not found"))
			return
		}
		h.log.WithError(err).WithField("tournament_id", id).Error("TournamentHandler.Control: fetch error")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", "failed to fetch tournament"))
		return
	}

	// Validate and apply transition.
	now := time.Now()
	updates := map[string]interface{}{}

	switch req.Action {
	case "start":
		if tr.Status != string(domain.TournamentPending) && tr.Status != string(domain.TournamentPaused) {
			c.JSON(http.StatusConflict, models.NewErrorResponse("INVALID_TRANSITION", fmt.Sprintf("cannot start a tournament in status %q", tr.Status)))
			return
		}
		updates["status"] = string(domain.TournamentLive)
		updates["started_at"] = now

	case "pause":
		if tr.Status != string(domain.TournamentLive) {
			c.JSON(http.StatusConflict, models.NewErrorResponse("INVALID_TRANSITION", fmt.Sprintf("cannot pause a tournament in status %q", tr.Status)))
			return
		}
		updates["status"] = string(domain.TournamentPaused)

	case "end":
		if tr.Status == string(domain.TournamentEnded) {
			c.JSON(http.StatusConflict, models.NewErrorResponse("INVALID_TRANSITION", "tournament is already ended"))
			return
		}
		updates["status"] = string(domain.TournamentEnded)
		updates["ended_at"] = now

	default:
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("UNKNOWN_ACTION", fmt.Sprintf("unknown action %q — expected start, pause, or end", req.Action)))
		return
	}

	if err := h.db.WithContext(ctx).Model(&tournamentRow{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		h.log.WithError(err).WithFields(logrus.Fields{
			"tournament_id": id,
			"action":        req.Action,
		}).Error("TournamentHandler.Control: update error")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", "failed to apply control action"))
		return
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"tournament_id": id,
		"action":        req.Action,
		"new_status":    updates["status"],
	}))
}

// ──────────────────────────────────────────────
// Helper: row → domain conversion
// ──────────────────────────────────────────────

func rowToTournament(r tournamentRow) domain.Tournament {
	t := domain.Tournament{
		ID:           r.ID,
		Name:         r.Name,
		Format:       r.Format,
		Status:       domain.TournamentStatus(r.Status),
		CapitalState: domain.CapitalState(r.CapitalState),
		CreatedAt:    r.CreatedAt,
	}
	if r.StartedAt.Valid {
		v := r.StartedAt.Time
		t.StartedAt = &v
	}
	if r.EndedAt.Valid {
		v := r.EndedAt.Time
		t.EndedAt = &v
	}
	if r.ConfigJSON != "" {
		_ = json.Unmarshal([]byte(r.ConfigJSON), &t.Config)
	}
	return t
}

func rowToCompetitor(r competitorRow) domain.Competitor {
	return domain.Competitor{
		ID:           r.ID,
		TournamentID: r.TournamentID,
		Kind:         domain.CompetitorKind(r.Kind),
		Provider:     r.Provider,
		SpecID:       r.SpecID,
		Subaccount:   r.Subaccount,
		Rank:         r.Rank,
		PnlUSD:       r.PnlUSD,
		PnlPct:       r.PnlPct,
		TradesTotal:  r.TradesTotal,
		Wins:         r.Wins,
		Status:       r.Status,
		CreatedAt:    r.CreatedAt,
	}
}

func rowToRound(r roundRow) domain.Round {
	round := domain.Round{
		ID:                 r.ID,
		TournamentID:       r.TournamentID,
		RoundNumber:        r.RoundNumber,
		Phase:              domain.RoundPhase(r.Phase),
		Symbol:             r.Symbol,
		WinnerCompetitorID: r.WinnerCompetitorID,
	}
	if r.StartedAt.Valid {
		v := r.StartedAt.Time
		round.StartedAt = &v
	}
	if r.EndedAt.Valid {
		v := r.EndedAt.Time
		round.EndedAt = &v
	}
	return round
}

// ──────────────────────────────────────────────
// Helper: auth level check
// ──────────────────────────────────────────────

// hasMinLevel returns true when the caller's Kong-injected level is >= min.
// Falls through to false when the key is absent (unauthenticated).
func hasMinLevel(c *gin.Context, min int) bool {
	level, _ := c.Get("kong_level")
	callerLevel, _ := level.(int)
	return callerLevel >= min
}

// ──────────────────────────────────────────────
// Helper: unique constraint detection
// ──────────────────────────────────────────────

// isUniqueViolation detects UNIQUE constraint errors from SQLite and PostgreSQL.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// SQLite: "UNIQUE constraint failed"
	// PostgreSQL: "duplicate key value violates unique constraint"
	return contains(msg, "UNIQUE constraint failed") || contains(msg, "duplicate key value")
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && indexStr(s, sub) >= 0)
}

func indexStr(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
