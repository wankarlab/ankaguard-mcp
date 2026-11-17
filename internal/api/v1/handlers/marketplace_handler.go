// Package handlers — Gremio/Marketplace endpoints.
//
// GET    /api/v1/marketplace/strategies  — certified strategy specs (includes retired, anti-survivorship)
// POST   /api/v1/marketplace/copy        — link BYO-keys + consent (KYC≥2 required)
// GET    /api/v1/marketplace/copy/status — active copy status for the caller
// DELETE /api/v1/marketplace/copy/:id   — disconnect (soft-delete)
//
// marketplace_copies table is created inline on first use (SQLite-compatible DDL).
// Audit entries are written to audit_log when that table exists; silently skipped otherwise.
package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// ─── domain structs ────────────────────────────────────────────────────────────

// MarketplaceStrategy is the public view of a certified/retired/shadow strategy spec.
type MarketplaceStrategy struct {
	SpecID      string  `json:"spec_id"`
	Author      string  `json:"author"`
	Regime      string  `json:"regime"`
	PfOos       float64 `json:"pf_oos"`
	Consistency float64 `json:"consistency"`
	// MaxDrawdown is the worst out-of-sample drawdown (C1.1 anti-survivorship);
	// always shown so copiers see downside, not just winners.
	MaxDrawdown float64 `json:"max_drawdown"`
	DaysActive  int     `json:"days_active"`
	Copiers     int     `json:"copiers"`
	Status      string  `json:"status"` // certified / retired / shadow
	// Retired is true for demoted/rejected specs (withdrawn from the marketplace);
	// kept visible for anti-survivorship transparency.
	Retired   bool       `json:"retired"`
	RetiredAt *time.Time `json:"retired_at,omitempty"`
}

// CopyRequest is the body for POST /api/v1/marketplace/copy.
type CopyRequest struct {
	SpecID      string `json:"spec_id"      binding:"required"`
	ExchangeKey string `json:"exchange_key" binding:"required"` // ref to user's stored credentials
	ConsentOK   bool   `json:"consent_ok"   binding:"required"` // legal disclaimer acceptance
}

// CopyStatus is the response for GET /api/v1/marketplace/copy/status.
type CopyStatus struct {
	Active    bool      `json:"active"`
	SpecID    string    `json:"spec_id,omitempty"`
	StartedAt time.Time `json:"started_at,omitempty"`
	PnlUSD    float64   `json:"pnl_usd"`
}

// ─── DB model ─────────────────────────────────────────────────────────────────

// marketplaceCopy is the GORM model that maps to marketplace_copies.
type marketplaceCopy struct {
	ID          string     `gorm:"column:id;primaryKey"`
	UserID      string     `gorm:"column:user_id;not null"`
	SpecID      string     `gorm:"column:spec_id;not null"`
	ExchangeKey string     `gorm:"column:exchange_key;not null"`
	ConsentOK   bool       `gorm:"column:consent_ok;default:false"`
	PnlUSD      float64    `gorm:"column:pnl_usd;default:0"`
	Status      string     `gorm:"column:status;default:active"`
	StartedAt   time.Time  `gorm:"column:started_at;autoCreateTime"`
	EndedAt     *time.Time `gorm:"column:ended_at"`
}

func (marketplaceCopy) TableName() string { return "marketplace_copies" }

// ─── handler ──────────────────────────────────────────────────────────────────

// MarketplaceHandler exposes the Gremio copy-trading marketplace over REST.
type MarketplaceHandler struct {
	db  *gorm.DB
	log *logrus.Logger
}

// NewMarketplaceHandler constructs a MarketplaceHandler and ensures the
// marketplace_copies table exists (idempotent DDL).
// db may be nil; all endpoints return 503 in that case.
func NewMarketplaceHandler(db *gorm.DB, log *logrus.Logger) *MarketplaceHandler {
	if log == nil {
		log = logrus.New()
	}
	h := &MarketplaceHandler{db: db, log: log}
	if db != nil {
		h.ensureSchema()
	}
	return h
}

// ensureSchema creates the marketplace_copies table if it does not exist.
// Uses raw SQL to stay compatible with both SQLite and PostgreSQL.
func (h *MarketplaceHandler) ensureSchema() {
	ddl := `CREATE TABLE IF NOT EXISTS marketplace_copies (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		spec_id TEXT NOT NULL,
		exchange_key TEXT NOT NULL,
		consent_ok BOOLEAN DEFAULT 0,
		pnl_usd REAL DEFAULT 0,
		status TEXT DEFAULT 'active',
		started_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		ended_at DATETIME
	)`
	if err := h.db.Exec(ddl).Error; err != nil {
		h.log.WithError(err).Warn("marketplace: could not ensure marketplace_copies table")
	}
}

// ─── GET /api/v1/marketplace/strategies ───────────────────────────────────────

// GetStrategies returns marketplace-relevant strategy specs from the REAL
// strategy_specs table — certified (validated/live) AND retired (demoted/rejected),
// so the Gremio shows withdrawals and drawdowns, never just survivors (C1.1).
// No mock fallback: an empty table yields an empty list.
func (h *MarketplaceHandler) GetStrategies(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("DB_UNAVAILABLE", "database not configured"))
		return
	}

	strategies, err := h.loadStrategiesFromDB(c)
	if err != nil {
		h.log.WithError(err).Error("marketplace: failed to load strategies from strategy_specs")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", err.Error()))
		return
	}

	c.JSON(http.StatusOK, models.NewSuccessResponseWithMeta(
		gin.H{"strategies": strategies},
		&models.APIMeta{Total: len(strategies), Count: len(strategies)},
	))
}

// loadStrategiesFromDB reads marketplace specs from the real strategy_specs
// schema (migration 021). pf_oos / consistency / worst_dd live inside the
// validation_verdict JSON and regime inside scope_json — they are derived here,
// not read from non-existent columns.
func (h *MarketplaceHandler) loadStrategiesFromDB(c *gin.Context) ([]MarketplaceStrategy, error) {
	type specRow struct {
		SpecID            string     `gorm:"column:spec_id"`
		Author            string     `gorm:"column:author"`
		UserID            string     `gorm:"column:user_id"`
		Status            string     `gorm:"column:status"`
		ScopeJSON         string     `gorm:"column:scope_json"`
		ValidationVerdict string     `gorm:"column:validation_verdict"`
		CreatedAt         *time.Time `gorm:"column:created_at"`
		UpdatedAt         *time.Time `gorm:"column:updated_at"`
	}

	var rows []specRow
	// validated/live = certified; demoted/rejected = retired (anti-survivorship).
	err := h.db.WithContext(c.Request.Context()).
		Raw(`SELECT spec_id, author, user_id, status, scope_json, validation_verdict, created_at, updated_at
			 FROM strategy_specs
			 WHERE status IN ('validated','live','demoted','rejected')
			 ORDER BY updated_at DESC`).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	// Count active copiers per spec.
	type countRow struct {
		SpecID  string `gorm:"column:spec_id"`
		Copiers int    `gorm:"column:copiers"`
	}
	var counts []countRow
	_ = h.db.WithContext(c.Request.Context()).
		Raw(`SELECT spec_id, COUNT(*) AS copiers FROM marketplace_copies WHERE status='active' GROUP BY spec_id`).
		Scan(&counts).Error

	copierMap := make(map[string]int, len(counts))
	for _, cr := range counts {
		copierMap[cr.SpecID] = cr.Copiers
	}

	out := make([]MarketplaceStrategy, 0, len(rows))
	for _, r := range rows {
		var vb struct {
			AggPF       float64 `json:"agg_pf"`
			Consistency float64 `json:"consistency"`
			WorstDD     float64 `json:"worst_dd"`
		}
		if r.ValidationVerdict != "" {
			_ = json.Unmarshal([]byte(r.ValidationVerdict), &vb)
		}
		regime := "UNKNOWN"
		var scope struct {
			Regime string `json:"regime"`
		}
		if r.ScopeJSON != "" {
			_ = json.Unmarshal([]byte(r.ScopeJSON), &scope)
			if scope.Regime != "" {
				regime = scope.Regime
			}
		}

		author := r.Author
		if author == "" {
			author = r.UserID
		}
		retired := r.Status == "demoted" || r.Status == "rejected"
		displayStatus := "certified"
		if retired {
			displayStatus = "retired"
		} else if r.Status == "shadow" {
			displayStatus = "shadow"
		}

		daysActive := 0
		if r.CreatedAt != nil {
			daysActive = int(time.Since(*r.CreatedAt).Hours() / 24)
		}

		ms := MarketplaceStrategy{
			SpecID:      r.SpecID,
			Author:      author,
			Regime:      regime,
			PfOos:       vb.AggPF,
			Consistency: vb.Consistency,
			MaxDrawdown: vb.WorstDD,
			DaysActive:  daysActive,
			Copiers:     copierMap[r.SpecID],
			Status:      displayStatus,
			Retired:     retired,
		}
		if retired {
			ms.RetiredAt = r.UpdatedAt
		}
		out = append(out, ms)
	}
	return out, nil
}

// ─── POST /api/v1/marketplace/copy ────────────────────────────────────────────

// PostCopy links a user's BYO exchange keys to a certified strategy.
// Returns 451 when consent has not been given.
// Requires KYC level ≥ 2 (enhanced KYC for copy-trading).
func (h *MarketplaceHandler) PostCopy(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("DB_UNAVAILABLE", "database not configured"))
		return
	}

	var req CopyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("VALIDATION_FAILED", err.Error()))
		return
	}

	// 451 Unavailable For Legal Reasons — consent not recorded.
	if !req.ConsentOK {
		c.JSON(http.StatusUnavailableForLegalReasons, models.NewErrorResponseWithDetails(
			"CONSENT_REQUIRED",
			"You must accept the copy-trading legal disclaimer before proceeding.",
			"Set consent_ok=true to confirm you have read and accepted the risk disclosure and terms of service for copy-trading on AnkaAI Gremio.",
		))
		return
	}

	// KYC gate: copy-trading requires enhanced KYC (level ≥ 2).
	callerID := userIDFromContext(c)
	kycLevel := kycLevelFromContext(c)
	if kycLevel < 2 {
		c.JSON(http.StatusForbidden, models.NewErrorResponseWithDetails(
			"KYC_INSUFFICIENT",
			"Copy-trading requires enhanced KYC (level 2 or higher).",
			fmt.Sprintf("Your current KYC level is %d. Complete identity verification to unlock copy-trading.", kycLevel),
		))
		return
	}

	// Check for an already-active copy subscription for this spec.
	var existing marketplaceCopy
	err := h.db.WithContext(c.Request.Context()).
		Where("user_id = ? AND spec_id = ? AND status = 'active'", callerID, req.SpecID).
		First(&existing).Error
	if err == nil {
		c.JSON(http.StatusConflict, models.NewErrorResponse(
			"ALREADY_COPYING",
			fmt.Sprintf("You are already copying spec %s (copy_id=%s).", req.SpecID, existing.ID),
		))
		return
	}

	// Insert new copy record.
	copyID := uuid.New().String()
	now := time.Now().UTC()
	record := marketplaceCopy{
		ID:          copyID,
		UserID:      callerID,
		SpecID:      req.SpecID,
		ExchangeKey: req.ExchangeKey,
		ConsentOK:   true,
		Status:      "active",
		StartedAt:   now,
	}
	if err := h.db.WithContext(c.Request.Context()).Create(&record).Error; err != nil {
		h.log.WithError(err).WithFields(logrus.Fields{
			"user_id": callerID,
			"spec_id": req.SpecID,
		}).Error("marketplace: failed to insert copy record")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", err.Error()))
		return
	}

	// Audit entry — best-effort, silently ignored if audit_log is absent.
	h.writeAudit(c, callerID, "copy_started", fmt.Sprintf("spec_id=%s copy_id=%s", req.SpecID, copyID))

	h.log.WithFields(logrus.Fields{
		"user_id": callerID,
		"spec_id": req.SpecID,
		"copy_id": copyID,
	}).Info("marketplace: copy subscription started")

	c.JSON(http.StatusCreated, models.NewSuccessResponse(gin.H{
		"copy_id":      copyID,
		"spec_id":      req.SpecID,
		"exchange_key": req.ExchangeKey,
		"started_at":   now,
		"status":       "active",
	}))
}

// ─── GET /api/v1/marketplace/copy/status ──────────────────────────────────────

// GetCopyStatus returns the active copy subscription for the caller, if any.
func (h *MarketplaceHandler) GetCopyStatus(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("DB_UNAVAILABLE", "database not configured"))
		return
	}

	callerID := userIDFromContext(c)

	var record marketplaceCopy
	err := h.db.WithContext(c.Request.Context()).
		Where("user_id = ? AND status = 'active'", callerID).
		Order("started_at DESC").
		First(&record).Error

	if err == gorm.ErrRecordNotFound {
		c.JSON(http.StatusOK, models.NewSuccessResponse(CopyStatus{Active: false}))
		return
	}
	if err != nil {
		h.log.WithError(err).WithField("user_id", callerID).Error("marketplace: GetCopyStatus query failed")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", err.Error()))
		return
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(CopyStatus{
		Active:    true,
		SpecID:    record.SpecID,
		StartedAt: record.StartedAt,
		PnlUSD:    record.PnlUSD,
	}))
}

// ─── DELETE /api/v1/marketplace/copy/:id ──────────────────────────────────────

// DeleteCopy soft-deletes a copy subscription (sets status='ended', ended_at=now).
// The caller can only delete their own subscriptions unless they are level ≥ 3.
func (h *MarketplaceHandler) DeleteCopy(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("DB_UNAVAILABLE", "database not configured"))
		return
	}

	copyID := c.Param("id")
	if copyID == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("VALIDATION_FAILED", "copy id is required"))
		return
	}

	callerID := userIDFromContext(c)

	// Load the record first to enforce ownership.
	// The web UI only knows the spec_id (from /copy/status), so accept either the
	// copy row id OR the caller's active copy for a given spec_id.
	var record marketplaceCopy
	err := h.db.WithContext(c.Request.Context()).
		Where("id = ?", copyID).
		First(&record).Error
	if err == gorm.ErrRecordNotFound {
		err = h.db.WithContext(c.Request.Context()).
			Where("spec_id = ? AND user_id = ? AND status = 'active'", copyID, callerID).
			First(&record).Error
	}
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, models.NewErrorResponse("NOT_FOUND", "copy subscription not found"))
			return
		}
		h.log.WithError(err).WithField("copy_id", copyID).Error("marketplace: DeleteCopy lookup failed")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", err.Error()))
		return
	}

	// Ownership check — level ≥ 3 may manage any user's subscriptions.
	if record.UserID != callerID {
		level, _ := c.Get("kong_level")
		callerLevel, _ := level.(int)
		if callerLevel < 3 {
			c.JSON(http.StatusForbidden, models.NewErrorResponse("FORBIDDEN", "can only disconnect your own copy subscriptions"))
			return
		}
	}

	if record.Status == "ended" {
		c.JSON(http.StatusConflict, models.NewErrorResponse("ALREADY_ENDED", "copy subscription is already disconnected"))
		return
	}

	now := time.Now().UTC()
	updates := map[string]interface{}{
		"status":   "ended",
		"ended_at": now,
	}
	if err := h.db.WithContext(c.Request.Context()).
		Model(&marketplaceCopy{}).
		Where("id = ?", copyID).
		Updates(updates).Error; err != nil {
		h.log.WithError(err).WithField("copy_id", copyID).Error("marketplace: DeleteCopy update failed")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", err.Error()))
		return
	}

	// Audit entry — best-effort.
	h.writeAudit(c, callerID, "copy_ended", fmt.Sprintf("copy_id=%s spec_id=%s", copyID, record.SpecID))

	h.log.WithFields(logrus.Fields{
		"caller_id": callerID,
		"copy_id":   copyID,
		"spec_id":   record.SpecID,
	}).Info("marketplace: copy subscription ended")

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"copy_id":  copyID,
		"spec_id":  record.SpecID,
		"status":   "ended",
		"ended_at": now,
	}))
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// kycLevelFromContext extracts the KYC level set by the JWT/Kong middleware.
// Falls back to 0 (unverified) when the key is absent.
func kycLevelFromContext(c *gin.Context) int {
	if v, exists := c.Get("kyc_level"); exists {
		if level, ok := v.(int); ok {
			return level
		}
	}
	return 0
}

// writeAudit inserts a row into audit_log when that table is present.
// Silently no-ops if the table does not exist or the insert fails.
func (h *MarketplaceHandler) writeAudit(c *gin.Context, userID, action, detail string) {
	sql := `INSERT INTO audit_log (id, user_id, action, detail, created_at)
	        VALUES (?, ?, ?, ?, ?)`
	err := h.db.WithContext(c.Request.Context()).
		Exec(sql, uuid.New().String(), userID, action, detail, time.Now().UTC()).Error
	if err != nil {
		h.log.WithError(err).Debug("marketplace: audit_log insert skipped (table may not exist)")
	}
}

// ROUTE_MANIFEST: GET /api/v1/marketplace/strategies -> marketplaceHandler.GetStrategies | POST /api/v1/marketplace/copy -> marketplaceHandler.PostCopy | GET /api/v1/marketplace/copy/status -> marketplaceHandler.GetCopyStatus | DELETE /api/v1/marketplace/copy/:id -> marketplaceHandler.DeleteCopy
