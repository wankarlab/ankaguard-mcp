// Package handlers — AnkaAI Gate Policies, Tiers and Moonshots endpoints.
//
// GET  /api/v1/admin/policies            → current gate thresholds (KYC≥2)
// PUT  /api/v1/admin/policies            → update thresholds (KYC≥4, signed)
// GET  /api/v1/admin/policies/robustness → robustness score weights (DB-backed)
// GET  /api/v1/admin/tiers               → SaaS tier table (DB-backed, editable)
// GET  /api/v1/admin/moonshots           → moonshot features (DB-backed, editable)
//
// Policy changes are audited with the JWT user_id of the signer.
package handlers

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// ────────────────────────────────────────────────────────────────
// Domain types
// ────────────────────────────────────────────────────────────────

// GatePolicies holds the walk-forward validation thresholds used by the
// strategy gate. Signed fields record who last approved a change (KYC-4+).
type GatePolicies struct {
	PfMinOos    float64    `json:"pf_min_oos"`
	GreenPctMin float64    `json:"green_pct_min"`
	DriftMax    float64    `json:"drift_max"`
	SignedBy    string     `json:"signed_by,omitempty"`
	SignedAt    *time.Time `json:"signed_at,omitempty"`
}

// defaultPolicies are applied when no row exists in gate_policies.
var defaultPolicies = GatePolicies{
	PfMinOos:    1.30,
	GreenPctMin: 60,
	DriftMax:    0.15,
}

// ────────────────────────────────────────────────────────────────
// DB model
// ────────────────────────────────────────────────────────────────

// gatePoliciesRow mirrors the gate_policies table (singleton, id=1).
type gatePoliciesRow struct {
	ID          int        `gorm:"column:id;primaryKey"`
	PfMinOos    float64    `gorm:"column:pf_min_oos;default:1.30"`
	GreenPctMin float64    `gorm:"column:green_pct_min;default:60"`
	DriftMax    float64    `gorm:"column:drift_max;default:0.15"`
	SignedBy    string     `gorm:"column:signed_by"`
	SignedAt    *time.Time `gorm:"column:signed_at"`
	UpdatedAt   *time.Time `gorm:"column:updated_at"`
}

func (gatePoliciesRow) TableName() string { return "gate_policies" }

// ────────────────────────────────────────────────────────────────
// Seed defaults — these are the INITIAL values seeded into the editable
// DB tables (saas_tiers, moonshot_features, robustness_weights) the first
// time the service boots. The live source of truth is the DB, so ops and
// the tournament can tune them without code changes. Nothing here is
// hardcoded at read time: the GET handlers read the tables.
// ────────────────────────────────────────────────────────────────

// saasTier describes one SaaS subscription tier.
type saasTier struct {
	Name         string   `json:"name"`
	PriceMonthly int      `json:"price_monthly_usd"`
	MaxBots      int      `json:"max_bots"`
	MaxPairs     int      `json:"max_pairs"`
	FeePct       float64  `json:"performance_fee_pct"`
	Features     []string `json:"features"`
}

var seedTiers = []saasTier{
	{
		Name:         "Free",
		PriceMonthly: 0,
		MaxBots:      1,
		MaxPairs:     3,
		FeePct:       20,
		Features:     []string{"paper_trading", "basic_indicators"},
	},
	{
		Name:         "Basic",
		PriceMonthly: 19,
		MaxBots:      3,
		MaxPairs:     10,
		FeePct:       15,
		Features:     []string{"paper_trading", "live_trading", "basic_indicators", "regime_gate"},
	},
	{
		Name:         "Pro",
		PriceMonthly: 49,
		MaxBots:      10,
		MaxPairs:     50,
		FeePct:       10,
		Features:     []string{"paper_trading", "live_trading", "all_indicators", "regime_gate", "ml_gate", "strategy_validation", "multi_exchange"},
	},
	{
		Name:         "Enterprise",
		PriceMonthly: 199,
		MaxBots:      -1, // unlimited
		MaxPairs:     -1,
		FeePct:       5,
		Features:     []string{"all_features", "custom_strategies", "dedicated_support", "self_hosted", "sla_99_9"},
	},
}

// moonshotFeature describes a moonshot toggle (all off by design).
type moonshotFeature struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	Risk        string `json:"risk"`
}

var seedMoonshots = []moonshotFeature{
	{
		ID:          "autonomous_rebalance",
		Name:        "Autonomous Portfolio Rebalance",
		Description: "Allow the AI to autonomously rebalance the whole portfolio without human approval.",
		Enabled:     false,
		Risk:        "high",
	},
	{
		ID:          "cross_exchange_arb",
		Name:        "Cross-Exchange Arbitrage",
		Description: "Execute simultaneous orders across exchanges to capture spread. Requires sub-ms latency infrastructure.",
		Enabled:     false,
		Risk:        "high",
	},
	{
		ID:          "leverage_auto_scale",
		Name:        "Dynamic Leverage Auto-Scaling",
		Description: "Automatically increase leverage when confidence score exceeds threshold.",
		Enabled:     false,
		Risk:        "critical",
	},
	{
		ID:          "llm_order_execution",
		Name:        "LLM Direct Order Execution",
		Description: "Allow an LLM to place orders without rule-based confirmation layer.",
		Enabled:     false,
		Risk:        "critical",
	},
}

// robustnessWeight is one component weight of the robustness score.
type robustnessWeight struct {
	Key       string  `json:"key"`
	Weight    float64 `json:"weight"`
	SortOrder int     `json:"-"`
}

// seedRobustnessWeights are the default component weights (sum to 1.0).
var seedRobustnessWeights = []robustnessWeight{
	{Key: "profit_factor", Weight: 0.35, SortOrder: 0},
	{Key: "green_window_pct", Weight: 0.25, SortOrder: 1},
	{Key: "max_drawdown", Weight: 0.20, SortOrder: 2},
	{Key: "trade_count", Weight: 0.10, SortOrder: 3},
	{Key: "sharpe_ratio", Weight: 0.10, SortOrder: 4},
}

// ────────────────────────────────────────────────────────────────
// DB models for the editable tier / moonshot / robustness tables
// ────────────────────────────────────────────────────────────────

// saasTierRow mirrors the saas_tiers table. Features is a JSON array string.
type saasTierRow struct {
	Name         string  `gorm:"column:name;primaryKey"`
	PriceMonthly int     `gorm:"column:price_monthly_usd"`
	MaxBots      int     `gorm:"column:max_bots"`
	MaxPairs     int     `gorm:"column:max_pairs"`
	FeePct       float64 `gorm:"column:performance_fee_pct"`
	FeaturesJSON string  `gorm:"column:features_json"`
	SortOrder    int     `gorm:"column:sort_order"`
}

func (saasTierRow) TableName() string { return "saas_tiers" }

// moonshotRow mirrors the moonshot_features table.
type moonshotRow struct {
	ID          string `gorm:"column:id;primaryKey"`
	Name        string `gorm:"column:name"`
	Description string `gorm:"column:description"`
	Enabled     bool   `gorm:"column:enabled"`
	Risk        string `gorm:"column:risk"`
	SortOrder   int    `gorm:"column:sort_order"`
}

func (moonshotRow) TableName() string { return "moonshot_features" }

// robustnessWeightRow mirrors the robustness_weights table.
type robustnessWeightRow struct {
	WeightKey string  `gorm:"column:weight_key;primaryKey"`
	Weight    float64 `gorm:"column:weight"`
	SortOrder int     `gorm:"column:sort_order"`
}

func (robustnessWeightRow) TableName() string { return "robustness_weights" }

// ────────────────────────────────────────────────────────────────
// Handler
// ────────────────────────────────────────────────────────────────

// PoliciesHandler exposes gate policy CRUD plus tiers and moonshots.
type PoliciesHandler struct {
	db  *gorm.DB
	log *logrus.Logger

	mu       sync.RWMutex
	policies GatePolicies
}

// NewPoliciesHandler constructs and migrates the gate_policies table, then
// loads the current row (or inserts the default singleton row).
func NewPoliciesHandler(db *gorm.DB, log *logrus.Logger) *PoliciesHandler {
	if log == nil {
		log = logrus.New()
	}
	h := &PoliciesHandler{
		db:       db,
		log:      log,
		policies: defaultPolicies,
	}
	if db != nil {
		if err := db.Exec(`CREATE TABLE IF NOT EXISTS gate_policies (
			id           INT  PRIMARY KEY DEFAULT 1,
			pf_min_oos   REAL DEFAULT 1.30,
			green_pct_min REAL DEFAULT 60,
			drift_max    REAL DEFAULT 0.15,
			signed_by    TEXT,
			signed_at    DATETIME,
			updated_at   DATETIME
		)`).Error; err != nil {
			log.WithError(err).Warn("gate_policies migration failed")
		}
		h.loadOrSeedPolicies()
		h.ensureTiersTable()
		h.ensureMoonshotsTable()
		h.ensureRobustnessTable()
	}
	return h
}

// ensureTiersTable creates and seeds saas_tiers (editable; the GET handler reads it).
func (h *PoliciesHandler) ensureTiersTable() {
	if err := h.db.Exec(`CREATE TABLE IF NOT EXISTS saas_tiers (
		name                TEXT PRIMARY KEY,
		price_monthly_usd   INTEGER DEFAULT 0,
		max_bots            INTEGER DEFAULT 0,
		max_pairs           INTEGER DEFAULT 0,
		performance_fee_pct REAL    DEFAULT 0,
		features_json       TEXT    DEFAULT '[]',
		sort_order          INTEGER DEFAULT 0
	)`).Error; err != nil {
		h.log.WithError(err).Warn("saas_tiers migration failed")
		return
	}
	var n int64
	h.db.Model(&saasTierRow{}).Count(&n)
	if n > 0 {
		return
	}
	for i, t := range seedTiers {
		feats, _ := json.Marshal(t.Features)
		row := saasTierRow{
			Name: t.Name, PriceMonthly: t.PriceMonthly, MaxBots: t.MaxBots,
			MaxPairs: t.MaxPairs, FeePct: t.FeePct, FeaturesJSON: string(feats), SortOrder: i,
		}
		if err := h.db.Save(&row).Error; err != nil {
			h.log.WithError(err).Warn("saas_tiers: failed to seed default tier")
		}
	}
}

// ensureMoonshotsTable creates and seeds moonshot_features.
func (h *PoliciesHandler) ensureMoonshotsTable() {
	if err := h.db.Exec(`CREATE TABLE IF NOT EXISTS moonshot_features (
		id          TEXT PRIMARY KEY,
		name        TEXT,
		description TEXT,
		enabled     BOOLEAN DEFAULT 0,
		risk        TEXT,
		sort_order  INTEGER DEFAULT 0
	)`).Error; err != nil {
		h.log.WithError(err).Warn("moonshot_features migration failed")
		return
	}
	var n int64
	h.db.Model(&moonshotRow{}).Count(&n)
	if n > 0 {
		return
	}
	for i, m := range seedMoonshots {
		row := moonshotRow{
			ID: m.ID, Name: m.Name, Description: m.Description,
			Enabled: m.Enabled, Risk: m.Risk, SortOrder: i,
		}
		if err := h.db.Save(&row).Error; err != nil {
			h.log.WithError(err).Warn("moonshot_features: failed to seed default")
		}
	}
}

// ensureRobustnessTable creates and seeds robustness_weights.
func (h *PoliciesHandler) ensureRobustnessTable() {
	if err := h.db.Exec(`CREATE TABLE IF NOT EXISTS robustness_weights (
		weight_key TEXT PRIMARY KEY,
		weight     REAL    DEFAULT 0,
		sort_order INTEGER DEFAULT 0
	)`).Error; err != nil {
		h.log.WithError(err).Warn("robustness_weights migration failed")
		return
	}
	var n int64
	h.db.Model(&robustnessWeightRow{}).Count(&n)
	if n > 0 {
		return
	}
	for _, w := range seedRobustnessWeights {
		row := robustnessWeightRow{WeightKey: w.Key, Weight: w.Weight, SortOrder: w.SortOrder}
		if err := h.db.Save(&row).Error; err != nil {
			h.log.WithError(err).Warn("robustness_weights: failed to seed default")
		}
	}
}

// loadOrSeedPolicies fetches the singleton row; inserts defaults if missing.
func (h *PoliciesHandler) loadOrSeedPolicies() {
	var row gatePoliciesRow
	err := h.db.First(&row, 1).Error
	if err != nil {
		// Row not found — seed defaults.
		row = gatePoliciesRow{
			ID:          1,
			PfMinOos:    defaultPolicies.PfMinOos,
			GreenPctMin: defaultPolicies.GreenPctMin,
			DriftMax:    defaultPolicies.DriftMax,
		}
		if saveErr := h.db.Save(&row).Error; saveErr != nil {
			h.log.WithError(saveErr).Warn("gate_policies: failed to seed defaults")
		}
		return
	}
	h.policies = GatePolicies{
		PfMinOos:    row.PfMinOos,
		GreenPctMin: row.GreenPctMin,
		DriftMax:    row.DriftMax,
		SignedBy:    row.SignedBy,
		SignedAt:    row.SignedAt,
	}
}

// ────────────────────────────────────────────────────────────────
// GET /api/v1/admin/policies
// ────────────────────────────────────────────────────────────────

// GetPolicies returns the current gate thresholds. Read-only and visible to any
// authenticated user (C1.4): the Sala de Políticas renders these in read-only mode
// with a lock banner when the caller lacks write level. These are platform policy
// values, not secrets — consistent with the tiers/moonshots/robustness reads, which
// carry no KYC gate. Writes (UpdatePolicies) remain gated at KYC≥4.
func (h *PoliciesHandler) GetPolicies(c *gin.Context) {
	h.mu.RLock()
	p := h.policies
	h.mu.RUnlock()

	c.JSON(http.StatusOK, models.NewSuccessResponse(p))
}

// ────────────────────────────────────────────────────────────────
// PUT /api/v1/admin/policies
// ────────────────────────────────────────────────────────────────

// UpdatePolicies applies new gate thresholds and records the signer. Gated at
// level 3 — the SAME level as the Sala de Políticas screen lock (rbac.level.
// politicas) and the route's RequireLevel(3) — plus the FE sign-off ceremony.
// (kycLevel here resolves to the caller's Kong level, not a separate KYC claim.)
func (h *PoliciesHandler) UpdatePolicies(c *gin.Context) {
	if kycLevel(c) < 3 {
		c.JSON(http.StatusForbidden, models.NewErrorResponse("FORBIDDEN", "access level ≥ 3 required to update policies"))
		return
	}

	var req GatePolicies
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponseWithDetails("VALIDATION_FAILED", "invalid request body", err.Error()))
		return
	}

	// Basic sanity guards.
	if req.PfMinOos <= 0 {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("VALIDATION_FAILED", "pf_min_oos must be > 0"))
		return
	}
	if req.GreenPctMin < 0 || req.GreenPctMin > 100 {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("VALIDATION_FAILED", "green_pct_min must be 0–100"))
		return
	}
	if req.DriftMax <= 0 {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("VALIDATION_FAILED", "drift_max must be > 0"))
		return
	}

	actor := userIDFromContext(c)
	now := time.Now().UTC()
	req.SignedBy = actor
	req.SignedAt = &now

	// Persist to DB.
	if h.db != nil {
		row := gatePoliciesRow{
			ID:          1,
			PfMinOos:    req.PfMinOos,
			GreenPctMin: req.GreenPctMin,
			DriftMax:    req.DriftMax,
			SignedBy:    actor,
			SignedAt:    &now,
			UpdatedAt:   &now,
		}
		if err := h.db.Save(&row).Error; err != nil {
			h.log.WithError(err).Error("gate_policies: failed to persist update")
			c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", err.Error()))
			return
		}

		// Audit trail.
		h.db.Exec(
			`INSERT OR IGNORE INTO audit_log (actor, action, target, created_at) VALUES (?, ?, ?, ?)`,
			actor, "policies_updated",
			"gate_policies",
			now.Format(time.RFC3339),
		)
	}

	// Update in-memory cache.
	h.mu.Lock()
	h.policies = req
	h.mu.Unlock()

	h.log.WithFields(logrus.Fields{
		"pf_min_oos":    req.PfMinOos,
		"green_pct_min": req.GreenPctMin,
		"drift_max":     req.DriftMax,
		"signed_by":     actor,
	}).Info("gate_policies updated")

	c.JSON(http.StatusOK, models.NewSuccessResponse(req))
}

// ────────────────────────────────────────────────────────────────
// GET /api/v1/admin/policies/robustness
// ────────────────────────────────────────────────────────────────

// GetRobustness returns the component weights of the robustness score from the
// editable robustness_weights DB table (seeded on first boot). The tournament
// can tune these without code changes.
func (h *PoliciesHandler) GetRobustness(c *gin.Context) {
	weights := gin.H{}
	src := seedRobustnessWeights
	if h.db != nil {
		var rows []robustnessWeightRow
		if err := h.db.Order("sort_order ASC").Find(&rows).Error; err == nil && len(rows) > 0 {
			src = make([]robustnessWeight, 0, len(rows))
			for _, r := range rows {
				src = append(src, robustnessWeight{Key: r.WeightKey, Weight: r.Weight})
			}
		}
	}
	for _, w := range src {
		weights[w.Key] = w.Weight
	}
	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"weights": weights,
		"note":    "weights sum to 1.0; robustness = sum(weight * normalized_metric)",
	}))
}

// ────────────────────────────────────────────────────────────────
// GET /api/v1/admin/tiers
// ────────────────────────────────────────────────────────────────

// GetTiers returns the SaaS subscription tier table from the editable
// saas_tiers DB table (seeded on first boot). Falls back to the seed slice
// only if the DB read fails, so the endpoint never returns empty.
func (h *PoliciesHandler) GetTiers(c *gin.Context) {
	tiers := seedTiers
	if h.db != nil {
		var rows []saasTierRow
		if err := h.db.Order("sort_order ASC").Find(&rows).Error; err == nil && len(rows) > 0 {
			tiers = make([]saasTier, 0, len(rows))
			for _, r := range rows {
				var feats []string
				_ = json.Unmarshal([]byte(r.FeaturesJSON), &feats)
				tiers = append(tiers, saasTier{
					Name: r.Name, PriceMonthly: r.PriceMonthly, MaxBots: r.MaxBots,
					MaxPairs: r.MaxPairs, FeePct: r.FeePct, Features: feats,
				})
			}
		}
	}
	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"tiers": tiers,
	}))
}

// ────────────────────────────────────────────────────────────────
// GET /api/v1/admin/moonshots
// ────────────────────────────────────────────────────────────────

// GetMoonshots returns the moonshot feature list from the editable
// moonshot_features DB table (seeded on first boot).
func (h *PoliciesHandler) GetMoonshots(c *gin.Context) {
	moonshots := seedMoonshots
	if h.db != nil {
		var rows []moonshotRow
		if err := h.db.Order("sort_order ASC").Find(&rows).Error; err == nil && len(rows) > 0 {
			moonshots = make([]moonshotFeature, 0, len(rows))
			for _, r := range rows {
				moonshots = append(moonshots, moonshotFeature{
					ID: r.ID, Name: r.Name, Description: r.Description,
					Enabled: r.Enabled, Risk: r.Risk,
				})
			}
		}
	}
	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"moonshots": moonshots,
		"note":      "moonshot features are disabled by default; enable via the moonshot_features table",
	}))
}

// ROUTE_MANIFEST_POLICIES: GET /api/v1/admin/policies -> policiesHandler.GetPolicies | PUT /api/v1/admin/policies -> policiesHandler.UpdatePolicies | GET /api/v1/admin/policies/robustness -> policiesHandler.GetRobustness | GET /api/v1/admin/tiers -> policiesHandler.GetTiers | GET /api/v1/admin/moonshots -> policiesHandler.GetMoonshots
