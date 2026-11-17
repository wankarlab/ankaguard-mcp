// Package handlers — AnkaAI Kill-Switch endpoints.
//
// POST /api/v1/admin/killswitch/ring/1  → freeze a single strategy spec (KYC≥3)
// POST /api/v1/admin/killswitch/ring/2  → freeze a whole tenant (KYC≥3)
// POST /api/v1/admin/killswitch/ring/0  → global stop AND liquidate (KYC≥4; confirm "CERRAR TODO" unless dry_run)
// POST /api/v1/admin/killswitch/ring/0/reset → clear the ring-0 flag (KYC≥4)
// GET  /api/v1/admin/killswitch/status  → current ring state
//
// State is kept in-memory behind a sync.RWMutex and persisted to the
// killswitch_state SQLite/GORM table so it survives restarts.
package handlers

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/rantipay/trading-mcp/internal/killswitch"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// ────────────────────────────────────────────────────────────────
// Request / response types
// ────────────────────────────────────────────────────────────────

// Ring1Request freezes a single strategy spec.
type Ring1Request struct {
	SpecID string `json:"spec_id" binding:"required"`
}

// Ring2Request freezes all bots belonging to a tenant.
type Ring2Request struct {
	TenantID string `json:"tenant_id" binding:"required"`
}

// Ring0Request triggers a global stop-and-liquidate.
// Confirm must equal the literal string "CERRAR TODO" unless DryRun is true.
// DryRun changes nothing and only reports what would be stopped/closed.
type Ring0Request struct {
	Confirm string `json:"confirm"`
	DryRun  bool   `json:"dry_run"`
}

// KillSwitchStatus is returned by GET /status.
type KillSwitchStatus struct {
	Ring0Active  bool       `json:"ring0_active"`
	Ring1Specs   []string   `json:"ring1_specs"`
	Ring2Tenants []string   `json:"ring2_tenants"`
	LastAction   string     `json:"last_action,omitempty"`
	ActedBy      string     `json:"acted_by,omitempty"`
	ActedAt      *time.Time `json:"acted_at,omitempty"`
}

// ────────────────────────────────────────────────────────────────
// DB model
// ────────────────────────────────────────────────────────────────

// killSwitchState mirrors the killswitch_state table.
type killSwitchState struct {
	Ring     int    `gorm:"column:ring;primaryKey"`
	Target   string `gorm:"column:target;primaryKey"`
	Active   bool   `gorm:"column:active;default:1"`
	ActedBy  string `gorm:"column:acted_by"`
	ActedAt  time.Time `gorm:"column:acted_at;autoCreateTime"`
}

func (killSwitchState) TableName() string { return "killswitch_state" }

// ────────────────────────────────────────────────────────────────
// BotManagerAllStopper optional injection
// ────────────────────────────────────────────────────────────────

// AllStopper is satisfied by *services.BotManager (EmergencyStopAll).
// Injected optionally so the handler compiles without the services package.
type AllStopper interface {
	EmergencyStopAll(ctx context.Context, dryRun bool) (stopped []string, failed map[string]string)
}

// ────────────────────────────────────────────────────────────────
// Handler
// ────────────────────────────────────────────────────────────────

// KillSwitchHandler implements the three-ring kill-switch REST interface.
type KillSwitchHandler struct {
	db         *gorm.DB
	botManager AllStopper // optional; may be nil
	log        *logrus.Logger

	exchange Ring0Exchange // optional; nil → orders/positions steps are skipped and reported
	liveBook LiveBookMarker // optional; keeps xsec_live_book consistent after closes
	engines  []killswitch.EngineStopper

	mu           sync.RWMutex
	ring1Specs   map[string]struct{}
	ring2Tenants map[string]struct{}
	lastAction   string
	actedBy      string
	actedAt      *time.Time
}

// NewKillSwitchHandler constructs and migrates the killswitch_state table.
// botManager is optional (pass nil when not available).
func NewKillSwitchHandler(db *gorm.DB, botManager AllStopper, log *logrus.Logger) *KillSwitchHandler {
	if log == nil {
		log = logrus.New()
	}
	h := &KillSwitchHandler{
		db:           db,
		botManager:   botManager,
		log:          log,
		ring1Specs:   make(map[string]struct{}),
		ring2Tenants: make(map[string]struct{}),
	}
	if db != nil {
		if err := db.Exec(`CREATE TABLE IF NOT EXISTS killswitch_state (
			ring     INT  NOT NULL,
			target   TEXT NOT NULL,
			active   BOOLEAN DEFAULT 1,
			acted_by TEXT,
			acted_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (ring, target)
		)`).Error; err != nil {
			log.WithError(err).Warn("killswitch_state table migration failed")
		}
		// audit_log may not exist in the local SQLite DB (it is created by Postgres
		// migrations elsewhere); writeAudit inserts into it, so ensure it exists.
		if err := db.Exec(`CREATE TABLE IF NOT EXISTS audit_log (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			actor      TEXT,
			action     TEXT,
			target     TEXT,
			created_at TEXT
		)`).Error; err != nil {
			log.WithError(err).Warn("audit_log table migration failed")
		}
		// The DB is the source of truth on boot: start from "inactive" and let
		// loadFromDB flip the process-wide flag on if ring 0 was left active.
		killswitch.SetRing0Active(false)
		h.loadFromDB()
	}
	return h
}

// loadFromDB restores ring state from persistent storage on startup.
func (h *KillSwitchHandler) loadFromDB() {
	var rows []killSwitchState
	if err := h.db.Where("active = ?", true).Find(&rows).Error; err != nil {
		h.log.WithError(err).Warn("killswitch: failed to load state from DB")
		return
	}
	for _, r := range rows {
		switch r.Ring {
		case 0:
			killswitch.SetRing0Active(true)
			t := r.ActedAt
			h.actedAt = &t
			h.actedBy = r.ActedBy
			h.lastAction = "ring0_global_stop"
		case 1:
			h.ring1Specs[r.Target] = struct{}{}
		case 2:
			h.ring2Tenants[r.Target] = struct{}{}
		}
	}
}

// writeAudit appends a row to audit_log (best-effort; never fatal).
func (h *KillSwitchHandler) writeAudit(actedBy, action, target string) {
	if h.db == nil {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	msg := fmt.Sprintf("killswitch %s target=%s by=%s at=%s", action, target, actedBy, now)
	_ = h.db.Exec(
		`INSERT OR IGNORE INTO audit_log (actor, action, target, created_at) VALUES (?, ?, ?, ?)`,
		actedBy, action, target, now,
	).Error
	h.log.WithFields(logrus.Fields{
		"actor":  actedBy,
		"action": action,
		"target": target,
	}).Warn(msg)
}

// kycLevel extracts the kong_level set by gateway middleware (0 = unauthenticated).
func kycLevel(c *gin.Context) int {
	if v, ok := c.Get("kong_level"); ok {
		if l, ok := v.(int); ok {
			return l
		}
	}
	return 0
}

// ────────────────────────────────────────────────────────────────
// Ring 1 — freeze a spec
// ────────────────────────────────────────────────────────────────

// Ring1 handles POST /api/v1/admin/killswitch/ring/1.
// Requires KYC level ≥ 3. Marks the spec as frozen in strategy_specs.
func (h *KillSwitchHandler) Ring1(c *gin.Context) {
	if kycLevel(c) < 3 {
		c.JSON(http.StatusForbidden, models.NewErrorResponse("FORBIDDEN", "KYC level ≥ 3 required for ring-1"))
		return
	}

	var req Ring1Request
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponseWithDetails("VALIDATION_FAILED", "spec_id is required", err.Error()))
		return
	}

	actor := userIDFromContext(c)
	now := time.Now().UTC()

	// Freeze in strategy_specs table (best-effort — table may not exist yet).
	if h.db != nil {
		h.db.Exec(
			`UPDATE strategy_specs SET status='frozen', updated_at=? WHERE id=?`,
			now, req.SpecID,
		)
	}

	// Persist to killswitch_state.
	if h.db != nil {
		row := killSwitchState{Ring: 1, Target: req.SpecID, Active: true, ActedBy: actor, ActedAt: now}
		h.db.Save(&row)
	}

	// Update in-memory state.
	h.mu.Lock()
	h.ring1Specs[req.SpecID] = struct{}{}
	h.lastAction = "ring1_spec_frozen"
	h.actedBy = actor
	h.actedAt = &now
	h.mu.Unlock()

	h.writeAudit(actor, "ring1_spec_frozen", req.SpecID)

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"ring":    1,
		"spec_id": req.SpecID,
		"status":  "frozen",
		"acted_by": actor,
		"acted_at": now,
	}))
}

// ────────────────────────────────────────────────────────────────
// Ring 2 — freeze a tenant
// ────────────────────────────────────────────────────────────────

// Ring2 handles POST /api/v1/admin/killswitch/ring/2.
// Requires KYC level ≥ 3. Writes to killswitch_state and audit.
func (h *KillSwitchHandler) Ring2(c *gin.Context) {
	if kycLevel(c) < 3 {
		c.JSON(http.StatusForbidden, models.NewErrorResponse("FORBIDDEN", "KYC level ≥ 3 required for ring-2"))
		return
	}

	var req Ring2Request
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponseWithDetails("VALIDATION_FAILED", "tenant_id is required", err.Error()))
		return
	}

	actor := userIDFromContext(c)
	now := time.Now().UTC()

	// Persist to killswitch_state.
	if h.db != nil {
		row := killSwitchState{Ring: 2, Target: req.TenantID, Active: true, ActedBy: actor, ActedAt: now}
		h.db.Save(&row)
	}

	// Update in-memory state.
	h.mu.Lock()
	h.ring2Tenants[req.TenantID] = struct{}{}
	h.lastAction = "ring2_tenant_frozen"
	h.actedBy = actor
	h.actedAt = &now
	h.mu.Unlock()

	h.writeAudit(actor, "ring2_tenant_frozen", req.TenantID)

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"ring":      2,
		"tenant_id": req.TenantID,
		"status":    "frozen",
		"acted_by":  actor,
		"acted_at":  now,
	}))
}

// ────────────────────────────────────────────────────────────────
// Status
// ────────────────────────────────────────────────────────────────

// GetStatus handles GET /api/v1/admin/killswitch/status.
// Returns current in-memory ring state. No KYC gate — readonly diagnostic.
func (h *KillSwitchHandler) GetStatus(c *gin.Context) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	specs := make([]string, 0, len(h.ring1Specs))
	for s := range h.ring1Specs {
		specs = append(specs, s)
	}
	tenants := make([]string, 0, len(h.ring2Tenants))
	for t := range h.ring2Tenants {
		tenants = append(tenants, t)
	}

	status := KillSwitchStatus{
		Ring0Active:  killswitch.Ring0Active(),
		Ring1Specs:   specs,
		Ring2Tenants: tenants,
		LastAction:   h.lastAction,
		ActedBy:      h.actedBy,
		ActedAt:      h.actedAt,
	}
	c.JSON(http.StatusOK, models.NewSuccessResponse(status))
}

// ROUTE_MANIFEST_KILLSWITCH: POST /api/v1/admin/killswitch/ring/1 -> killswitchHandler.Ring1 | POST /api/v1/admin/killswitch/ring/2 -> killswitchHandler.Ring2 | POST /api/v1/admin/killswitch/ring/0 -> killswitchHandler.Ring0 | POST /api/v1/admin/killswitch/ring/0/reset -> killswitchHandler.Ring0Reset | GET /api/v1/admin/killswitch/status -> killswitchHandler.GetStatus
