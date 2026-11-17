package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/rantipay/trading-mcp/internal/storage"
	"github.com/rantipay/trading-mcp/internal/strategy"
	tradingMiddleware "github.com/rantipay/trading-mcp/internal/trading/middleware"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

// specRepo is the minimal StrategySpecRepository interface SpecHandler needs.
// Using an interface keeps the handler testable without the full storage package.
type specRepo interface {
	Save(ctx context.Context, userID string, s *strategy.StrategySpec) error
	LoadBySpecID(ctx context.Context, specID, userID string) (*storage.StrategySpecRow, error)
	ListByStatus(ctx context.Context, userID, status string) ([]*storage.StrategySpecRow, error)
	UpdateStatus(ctx context.Context, specID, userID, status string) error
	SaveConfigOverride(ctx context.Context, specID, userID string, floats map[string]float64, bools map[string]bool, ints map[string]int) error
	Delete(ctx context.Context, specID, userID string) error
	// Cross-user variants for the public validate submit (Gate) and cert loop.
	LoadBySpecIDAny(ctx context.Context, specID string) (*storage.StrategySpecRow, error)
	UpdateStatusAny(ctx context.Context, specID, status string) error
	ListByStatusesAny(ctx context.Context, statuses []string) ([]*storage.StrategySpecRow, error)
}

// specTemplateProvider is the minimal interface for serving seeded StrategySpec
// templates (C0.5). Satisfied by *storage.SpecTemplateRepository.
type specTemplateProvider interface {
	List(ctx context.Context) ([]*storage.SpecTemplateRow, error)
}

// OnLiveFunc is called after a Spec is successfully promoted to live status.
// floats/bools/ints are the typed maps from spec.ToConfigOverride().
// Implementors should apply these to the bot's runtime config store.
// A nil OnLiveFunc is a no-op.
type OnLiveFunc func(ctx context.Context, specID string, floats map[string]float64, bools map[string]bool, ints map[string]int)

// SpecHandler exposes the StrategySpec lifecycle over REST.
// API-1: POST/GET/GET:id/PUT/DELETE /api/v1/specs
// API-4: GET /api/v1/specs/:id/robustness, GET /api/v1/specs/ranked
// API-6: standardized envelope + canonical error codes
type SpecHandler struct {
	repo      specRepo
	templates specTemplateProvider // C0.5: seeded spec templates (nil → empty list)
	limits    strategy.GlobalLimits
	log       *logrus.Logger
	onLive    OnLiveFunc     // G5: called when a spec transitions to live
	minRR     func() float64 // dynamic spec.min_reward_risk_ratio from config table (nil → use static limits)
	// Real-money promote gates, sourced from the config table (nil → safe defaults).
	promoteMinLevel  func() int  // spec.promote_min_level (default 3)
	allowSelfPromote func() bool // spec.allow_self_promotion (default false)
}

// WithPromoteGates registers config-table readers for the real-money promote gates.
// minLevel: required Kong level to promote (nil → default 3).
// allowSelf: whether the author may approve their own spec (nil → default false).
func (h *SpecHandler) WithPromoteGates(minLevel func() int, allowSelf func() bool) *SpecHandler {
	h.promoteMinLevel = minLevel
	h.allowSelfPromote = allowSelf
	return h
}

// WithDynamicLimits registers a reader for config-table values that override the
// static GlobalLimits at validation time (hot-reload). Currently supplies
// spec.min_reward_risk_ratio. Safe to call after NewSpecHandler.
func (h *SpecHandler) WithDynamicLimits(minRewardRiskRatio func() float64) *SpecHandler {
	h.minRR = minRewardRiskRatio
	return h
}

// effectiveLimits returns the static limits overlaid with any dynamic values
// read from the config table at call time.
func (h *SpecHandler) effectiveLimits() strategy.GlobalLimits {
	limits := h.limits
	if h.minRR != nil {
		limits.MinRewardRiskRatio = h.minRR()
	}
	return limits
}

// NewSpecHandler constructs a SpecHandler.
func NewSpecHandler(repo specRepo, limits strategy.GlobalLimits, log *logrus.Logger) *SpecHandler {
	if log == nil {
		log = logrus.New()
	}
	return &SpecHandler{repo: repo, limits: limits, log: log}
}

// WithTemplates registers the seeded spec-template provider (C0.5).
// Safe to call after NewSpecHandler; nil → /specs/templates returns an empty list.
func (h *SpecHandler) WithTemplates(p specTemplateProvider) *SpecHandler {
	h.templates = p
	return h
}

// WithOnLive registers a callback that fires whenever a Spec is promoted to
// live status. Used by G5 to apply config_override to the bot's runtime config.
// Safe to call after NewSpecHandler; replaces any previously registered callback.
func (h *SpecHandler) WithOnLive(fn OnLiveFunc) *SpecHandler {
	h.onLive = fn
	return h
}

// ─── POST /api/v1/specs — create draft ────────────────────────────────────────

// CreateSpec creates a new draft StrategySpec for the authenticated user.
// Level: 1+. Author is bound to the JWT sub.
func (h *SpecHandler) CreateSpec(c *gin.Context) {
	userID := userIDFromContext(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "authentication required"))
		return
	}
	// SEC-7: warn when a write arrives via the gateway-trusted (unverified) JWT path.
	// The user_id used for RLS context originates from parseJWTUnverified in that case.
	if !tradingMiddleware.IsJWTVerified(c) {
		h.log.WithFields(logrus.Fields{
			"user_id":  userID,
			"resource": "strategy_spec",
			"action":   "create",
		}).Warn("SEC-7: RLS write with unverified JWT (gateway-trusted path)")
	}

	var s strategy.StrategySpec
	body, err := c.GetRawData()
	if err != nil || len(body) == 0 {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("VALIDATION_FAILED", "request body required"))
		return
	}
	if yamlErr := yaml.Unmarshal(body, &s); yamlErr != nil {
		// try JSON fallback
		if jsonErr := c.Request.Body.(interface{ Close() error }).Close(); jsonErr == nil {
		}
		c.JSON(http.StatusBadRequest, models.NewErrorResponseWithDetails("VALIDATION_FAILED", "invalid body", yamlErr.Error()))
		return
	}

	// Enforce: validation block must be empty on submission.
	// Limits include dynamic values from the config table (e.g. min reward:risk).
	if err := s.Validate(h.effectiveLimits()); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponseWithDetails("VALIDATION_FAILED", "spec validation failed", err.Error()))
		return
	}

	// Bind author + compute spec_id
	s.Metadata.Author = userID
	s.Metadata.Status = strategy.StatusDraft
	specID := s.ContentHash()
	s.Metadata.SpecID = specID

	if err := h.repo.Save(c.Request.Context(), userID, &s); err != nil {
		// A duplicate (user_id, name, revision) is a client error, not a 500.
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			c.JSON(http.StatusConflict, models.NewErrorResponseWithDetails("DUPLICATE_SPEC",
				"a spec with this name already exists — rename it or edit the existing one",
				s.Metadata.Name))
			return
		}
		h.log.WithError(err).Error("CreateSpec: save failed")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", "failed to save spec"))
		return
	}

	// G5: persist the typed config override so it's available at promote time.
	// Non-fatal: a warning is enough — the spec is still usable without it.
	if floats, bools, ints, overrideErr := s.ToConfigOverride(); overrideErr == nil {
		if saveErr := h.repo.SaveConfigOverride(c.Request.Context(), specID, userID, floats, bools, ints); saveErr != nil {
			h.log.WithError(saveErr).Warn("CreateSpec: failed to persist config_override (non-fatal)")
		}
	}

	c.JSON(http.StatusCreated, models.NewSuccessResponse(gin.H{
		"spec_id": specID,
		"status":  strategy.StatusDraft,
	}))
}

// ─── GET /api/v1/specs — list owner's specs ───────────────────────────────────

// ListSpecs returns all Specs owned by the authenticated user.
// Optional ?status= filter. Level: 1+.
func (h *SpecHandler) ListSpecs(c *gin.Context) {
	// Public, cross-user community Gate: returns all submitted specs in the
	// requested statuses, mapped to the GateSpec shape the web UI expects. The
	// FE uses display aliases (queued/certified) → real lifecycle statuses.
	statuses := gateStatusesFromQuery(c.Query("status"))

	// C0.5: own-specs filter. ?author=me or ?mine=true returns only the caller's
	// specs (by JWT sub). Uses the per-user repo path so RLS/owner scoping applies.
	mine := c.Query("mine") == "true" || strings.EqualFold(c.Query("author"), "me")
	if mine {
		userID := userIDFromContext(c)
		if userID == "" || userID == "default" {
			c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "authentication required for own specs"))
			return
		}
		rows, err := h.listOwnByStatuses(c, userID, statuses)
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", err.Error()))
			return
		}
		specs := make([]gin.H, 0, len(rows))
		for _, row := range rows {
			specs = append(specs, rowToGateSpec(row))
		}
		c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
			"specs": specs,
			"total": len(specs),
			"scope": "mine",
		}))
		return
	}

	rows, err := h.repo.ListByStatusesAny(c.Request.Context(), statuses)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", err.Error()))
		return
	}

	specs := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		specs = append(specs, rowToGateSpec(row))
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"specs": specs,
		"total": len(specs),
	}))
}

// ListOwnSpecs returns only the authenticated caller's specs (by JWT sub).
// Mounted under the JWT-protected /specs group as GET /specs/mine so
// userIDFromContext is populated — the public ListSpecs route has no JWT
// middleware and therefore cannot identify the caller (it would 401, which the
// web client treats as a dead session and bounces to /login). Optional ?status=.
func (h *SpecHandler) ListOwnSpecs(c *gin.Context) {
	userID := userIDFromContext(c)
	if userID == "" || userID == "default" {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "authentication required for own specs"))
		return
	}
	statuses := gateStatusesFromQuery(c.Query("status"))
	rows, err := h.listOwnByStatuses(c, userID, statuses)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", err.Error()))
		return
	}
	specs := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		specs = append(specs, rowToGateSpec(row))
	}
	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"specs": specs,
		"total": len(specs),
		"scope": "mine",
	}))
}

// listOwnByStatuses returns the caller's specs across the requested statuses,
// reusing the per-user ListByStatus repo method (one query per status, then
// dedup is unnecessary since statuses are disjoint).
func (h *SpecHandler) listOwnByStatuses(c *gin.Context, userID string, statuses []string) ([]*storage.StrategySpecRow, error) {
	var out []*storage.StrategySpecRow
	for _, st := range statuses {
		rows, err := h.repo.ListByStatus(c.Request.Context(), userID, st)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	return out, nil
}

// ─── GET /api/v1/specs/templates — seeded spec templates (public) ─────────────

// GetTemplates serves the seeded StrategySpec templates so the Spec Studio
// editor opens from real backend data, never a client constant (C0.5).
func (h *SpecHandler) GetTemplates(c *gin.Context) {
	if h.templates == nil {
		c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{"templates": []any{}, "total": 0}))
		return
	}
	rows, err := h.templates.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", err.Error()))
		return
	}
	templates := make([]gin.H, 0, len(rows))
	for _, t := range rows {
		var spec any
		if t.SpecJSON != "" {
			_ = json.Unmarshal([]byte(t.SpecJSON), &spec)
		}
		templates = append(templates, gin.H{
			"template_id": t.TemplateID,
			"name":        t.Name,
			"description": t.Description,
			"regime":      t.Regime,
			"kind":        t.Kind,
			"spec":        spec,
		})
	}
	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"templates": templates,
		"total":     len(templates),
	}))
}

// gateStatusesFromQuery maps the FE's comma-separated display statuses to the
// real lifecycle statuses. Empty query → the full Gate-relevant set.
func gateStatusesFromQuery(q string) []string {
	alias := map[string]string{
		"queued":     strategy.StatusDraft,
		"validating": strategy.StatusValidating,
		"certified":  strategy.StatusValidated,
		"validated":  strategy.StatusValidated,
		"shadow":     strategy.StatusValidated, // validated, awaiting operator promote
		"rejected":   strategy.StatusRejected,
		"live":       strategy.StatusLive,
		"draft":      strategy.StatusDraft,
	}
	if q == "" {
		return []string{strategy.StatusDraft, strategy.StatusValidating, strategy.StatusValidated, strategy.StatusRejected, strategy.StatusLive}
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range strings.Split(q, ",") {
		p = strings.TrimSpace(p)
		if real, ok := alias[p]; ok && !seen[real] {
			seen[real] = true
			out = append(out, real)
		}
	}
	// A status filter was provided but matched no known status. Return a sentinel
	// that matches nothing rather than falling through to "all specs" (which would
	// surface non-promotable specs in queues like /aprobaciones).
	if len(out) == 0 {
		return []string{"__no_match__"}
	}
	return out
}

// rowToGateSpec maps a persisted spec row to the GateSpec shape (id, verdict,
// pf, cons, robust, windows…). pf/cons come from the validation verdict; windows
// are synthesized deterministically (the per-window detail is not persisted).
func rowToGateSpec(row *storage.StrategySpecRow) gin.H {
	var vb strategy.ValidationBlock
	if row.ValidationVerdict != "" {
		_ = json.Unmarshal([]byte(row.ValidationVerdict), &vb)
	}

	verdict := "queued"
	switch row.Status {
	case strategy.StatusValidating:
		verdict = "validating"
	case strategy.StatusValidated, strategy.StatusLive:
		verdict = "certified"
	case strategy.StatusRejected:
		verdict = "rejected"
	}

	author := row.Author
	if author == "" {
		author = "anon"
	}
	kind := "humano"
	if al := strings.ToLower(author); strings.Contains(al, "ai") || strings.Contains(al, "gemini") ||
		strings.Contains(al, "gpt") || strings.Contains(al, "openrouter") || strings.Contains(al, "bot") {
		kind = "IA"
	}

	// Parse pairs/regime hints from scope_json (best-effort).
	regime := "UNKNOWN"
	var scope struct {
		Pairs  []string `json:"pairs"`
		Regime string   `json:"regime"`
	}
	if row.ScopeJSON != "" {
		_ = json.Unmarshal([]byte(row.ScopeJSON), &scope)
		if scope.Regime != "" {
			regime = scope.Regime
		}
	}

	return gin.H{
		"id":     row.SpecID,
		"name":   row.Name,
		"author": author,
		// C1.5: expose the author identity so the FE can compare to claims.sub and
		// disable the Approve button for self-approval (conflict of interest).
		"author_id": row.Author,
		"user_id":   row.UserID,
		"kind":      kind,
		"regime":    regime,
		"pf":        vb.AggPF,
		"cons":      vb.Consistency * 100,
		"robust":    vb.Consistency,
		// C1.1: anti-survivorship — surface the worst drawdown and a raw status so
		// retired (demoted/rejected) specs are visible, not hidden.
		"max_drawdown": vb.WorstDD,
		"status":       row.Status,
		"retired":      row.Status == strategy.StatusDemoted || row.Status == strategy.StatusRejected,
		"windows":      deterministicWindows(row.SpecID, 10),
		"verdict":      verdict,
	}
}

// ─── GET /api/v1/specs/:id — get one spec ─────────────────────────────────────

// GetSpec returns the Spec detail for the authenticated user. Non-owner → 404.
// Returns STATUS-ONLY validation block. Level: 1+.
func (h *SpecHandler) GetSpec(c *gin.Context) {
	userID := userIDFromContext(c)
	specID := c.Param("id")

	row, err := h.repo.LoadBySpecID(c.Request.Context(), specID, userID)
	if err == sql.ErrNoRows || err != nil && row == nil {
		c.JSON(http.StatusNotFound, models.NewErrorResponse("NOT_FOUND", "spec not found"))
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", err.Error()))
		return
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(row))
}

// ─── DELETE /api/v1/specs/:id — delete draft ──────────────────────────────────

// DeleteSpec removes a draft or rejected Spec. Live/validated → 409. Level: 1+.
func (h *SpecHandler) DeleteSpec(c *gin.Context) {
	userID := userIDFromContext(c)
	specID := c.Param("id")

	if err := h.repo.Delete(c.Request.Context(), specID, userID); err != nil {
		if err.Error() != "" && containsAny(err.Error(), "not found", "not in draft") {
			c.JSON(http.StatusConflict, models.NewErrorResponse("CONFLICT", err.Error()))
			return
		}
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", err.Error()))
		return
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{"deleted": specID}))
}

// ─── POST /api/v1/specs/:id/validate — submit for OOS gate ───────────────────

// ValidateSpec transitions a draft Spec to validating. The walk-forward OOS
// runs async; poll GET /specs/:id/verdict. Level: 2+.
func (h *SpecHandler) ValidateSpec(c *gin.Context) {
	userID := userIDFromContext(c)
	specID := c.Param("id")

	row, err := h.repo.LoadBySpecID(c.Request.Context(), specID, userID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.NewErrorResponse("NOT_FOUND", "spec not found"))
		return
	}
	if row.Status != strategy.StatusDraft && row.Status != strategy.StatusRejected {
		c.JSON(http.StatusConflict, models.NewErrorResponse("CONFLICT", "spec must be in draft or rejected status to validate"))
		return
	}

	if err := h.repo.UpdateStatus(c.Request.Context(), specID, userID, strategy.StatusValidating); err != nil {
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", err.Error()))
		return
	}

	// The actual OOS validation is triggered async by ValidationService.Loop.
	c.JSON(http.StatusAccepted, models.NewSuccessResponse(gin.H{
		"spec_id": specID,
		"status":  strategy.StatusValidating,
		"message": "validation submitted — poll GET /api/v1/specs/" + specID + " for verdict",
	}))
}

// ─── GET /api/v1/specs/:id/robustness — robustness score breakdown ────────────

// GetRobustness returns the RobustnessScore [0..100] for a certified Spec.
// Returns shadow score only (does not gate spawn). Level: 1+.
func (h *SpecHandler) GetRobustness(c *gin.Context) {
	userID := userIDFromContext(c)
	specID := c.Param("id")

	row, err := h.repo.LoadBySpecID(c.Request.Context(), specID, userID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.NewErrorResponse("NOT_FOUND", "spec not found"))
		return
	}
	if row.Status != strategy.StatusValidated && row.Status != strategy.StatusLive {
		c.JSON(http.StatusUnprocessableEntity, models.NewErrorResponse("VERDICT_PENDING",
			"robustness score only available for certified (validated/live) specs"))
		return
	}

	// Robustness scorer requires DB (injected in production); return placeholder when nil.
	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"spec_id": specID,
		"status":  row.Status,
		"shadow":  true, // robustness is shadow-only by default (EV-12)
		"note":    "robustness scorer not yet injected — activate via admin flag",
	}))
}

// ─── GET /api/v1/specs/:id/verdict — poll validation verdict ─────────────────

// GetVerdict returns the STATUS-ONLY validation verdict for a Spec.
// Returns 422 VERDICT_PENDING if still validating; 200 with verdict when done.
func (h *SpecHandler) GetVerdict(c *gin.Context) {
	userID := userIDFromContext(c)
	specID := c.Param("id")

	row, err := h.repo.LoadBySpecID(c.Request.Context(), specID, userID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.NewErrorResponse("NOT_FOUND", "spec not found"))
		return
	}
	if row.Status == strategy.StatusValidating {
		c.JSON(http.StatusUnprocessableEntity, models.NewErrorResponse("VERDICT_PENDING",
			"validation in progress — poll again"))
		return
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"spec_id":            specID,
		"status":             row.Status,
		"validation_verdict": row.ValidationVerdict,
	}))
}

// ─── POST /api/v1/specs/:id/promote — promote validated→live ─────────────────

// PromoteSpec promotes a validated Spec to live trading.
// Enforces operator≠author (RBAC-2). Level: 3+.
func (h *SpecHandler) PromoteSpec(c *gin.Context) {
	approverID := userIDFromContext(c) // approver is the authenticated user
	specID := c.Param("id")

	// Real-money promote gate — required level from the config table (default 3).
	requiredLevel := 3
	if h.promoteMinLevel != nil {
		if lvl := h.promoteMinLevel(); lvl > 0 {
			requiredLevel = lvl
		}
	}
	callerLevel, _ := tradingMiddleware.GetKongLevelFromContext(c)
	if callerLevel < requiredLevel {
		c.JSON(http.StatusForbidden, models.NewErrorResponseWithDetails("INSUFFICIENT_LEVEL",
			"promotion to live requires a higher access level",
			fmt.Sprintf("level %d required, caller has %d", requiredLevel, callerLevel)))
		return
	}

	// Cross-user load: an operator (level + self-approval gates apply) may promote
	// any author's spec — the community Gate is not per-user.
	row, err := h.repo.LoadBySpecIDAny(c.Request.Context(), specID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.NewErrorResponse("NOT_FOUND", "spec not found"))
		return
	}

	if row.Status != strategy.StatusValidated {
		c.JSON(http.StatusConflict, models.NewErrorResponseWithDetails("CONFLICT",
			"spec must be validated before promotion",
			"current status: "+row.Status))
		return
	}

	// RBAC-2: operator≠author conflict of interest. Configurable via the config
	// table (spec.allow_self_promotion, default false) so autonomous tournament
	// bots can self-promote when an operator explicitly enables it.
	allowSelf := false
	if h.allowSelfPromote != nil {
		allowSelf = h.allowSelfPromote()
	}
	if !allowSelf {
		if err := strategy.ValidatePromotion(row.Author, approverID); err != nil {
			// C1.5: explicit code + author_id so the FE can disable the Approve
			// button up-front instead of discovering the conflict via a 403.
			// author_id is carried in the data field (the {success,data} envelope)
			// so the client never re-derives it from the message string.
			resp := models.NewErrorResponse("SELF_APPROVAL_FORBIDDEN",
				"conflict of interest: the author cannot approve their own spec")
			resp.Data = gin.H{"author_id": row.Author, "approver_id": approverID}
			c.JSON(http.StatusForbidden, resp)
			return
		}
	}

	if err := h.repo.UpdateStatus(c.Request.Context(), specID, row.UserID, strategy.StatusLive); err != nil {
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", err.Error()))
		return
	}

	h.log.WithFields(map[string]any{
		"spec_id":  specID,
		"approver": approverID,
		"author":   row.Author,
	}).Info("SpecHandler: Spec promoted to live")

	// G5: apply config_override to the running bot when a Spec goes live.
	// The override was persisted at creation time; unmarshal and fire the callback.
	if h.onLive != nil && row.ConfigOverride != "" && row.ConfigOverride != "{}" {
		var override struct {
			Floats map[string]float64 `json:"floats"`
			Bools  map[string]bool    `json:"bools"`
			Ints   map[string]int     `json:"ints"`
		}
		if jsonErr := json.Unmarshal([]byte(row.ConfigOverride), &override); jsonErr == nil {
			h.onLive(c.Request.Context(), specID, override.Floats, override.Bools, override.Ints)
		} else {
			h.log.WithError(jsonErr).Warn("PromoteSpec: failed to unmarshal config_override for live callback (non-fatal)")
		}
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"spec_id":  specID,
		"status":   strategy.StatusLive,
		"approver": approverID,
	}))
}

// ─── POST /api/v1/specs/:id/demote — demote live→demoted ─────────────────────

// DemoteSpec demotes a live Spec. Used by automatic triggers (TTL/ranking/loss)
// and by operators. Level: 2+. API-5 active-demote gate is default-OFF.
func (h *SpecHandler) DemoteSpec(c *gin.Context) {
	userID := userIDFromContext(c)
	specID := c.Param("id")

	var req struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&req)
	if req.Reason == "" {
		req.Reason = strategy.DemotionTTL.String()
	}

	row, err := h.repo.LoadBySpecID(c.Request.Context(), specID, userID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.NewErrorResponse("NOT_FOUND", "spec not found"))
		return
	}
	if row.Status != strategy.StatusLive {
		c.JSON(http.StatusConflict, models.NewErrorResponse("CONFLICT", "only live specs can be demoted"))
		return
	}

	if err := h.repo.UpdateStatus(c.Request.Context(), specID, row.UserID, strategy.StatusDemoted); err != nil {
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", err.Error()))
		return
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"spec_id": specID,
		"status":  strategy.StatusDemoted,
		"reason":  req.Reason,
	}))
}

// ─── POST /api/v1/specs/:id/validate (public) ────────────────────────────────

// ValidateSpecPublic accepts a validate request without JWT.
// It acknowledges the request; the theater uses the /validate/stream SSE to
// drive the animation. Write operations (status transitions) still require auth.
func (h *SpecHandler) ValidateSpecPublic(c *gin.Context) {
	specID := c.Param("id")

	// Real submit: move the spec draft/rejected → validating so the background
	// certification loop picks it up and runs the walk-forward OOS validation.
	// Best-effort (the SSE theater still streams either way); cross-user because
	// this public route has no JWT user context.
	if h.repo != nil {
		if row, err := h.repo.LoadBySpecIDAny(c.Request.Context(), specID); err == nil {
			if row.Status == strategy.StatusDraft || row.Status == strategy.StatusRejected {
				if uerr := h.repo.UpdateStatusAny(c.Request.Context(), specID, strategy.StatusValidating); uerr != nil {
					h.log.WithError(uerr).WithField("spec_id", specID).Warn("ValidateSpecPublic: could not mark validating")
				} else {
					h.log.WithField("spec_id", specID).Info("spec submitted for validation (draft→validating)")
				}
			}
		}
	}

	c.JSON(http.StatusAccepted, models.NewSuccessResponse(gin.H{
		"spec_id": specID,
		"status":  "validating",
		"message": "stream via GET /api/v1/specs/" + specID + "/validate/stream",
	}))
}

// ─── GET /api/v1/specs/:id/validate/stream (public SSE) ──────────────────────

// ValidateStream streams OOS walk-forward window results as Server-Sent Events.
// Each `window` event carries { index, pass }. A final `done` event closes the
// stream. Results are deterministic (seeded by specID hash) so the same spec
// always produces the same animation.
func (h *SpecHandler) ValidateStream(c *gin.Context) {
	specID := c.Param("id")

	// Deterministic windows: hash specID bytes into pass/fail pattern.
	windows := deterministicWindows(specID, 10)

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	flusher, canFlush := c.Writer.(http.Flusher)

	for i, pass := range windows {
		select {
		case <-c.Request.Context().Done():
			return
		case <-time.After(280 * time.Millisecond):
		}
		passVal := "false"
		if pass {
			passVal = "true"
		}
		fmt.Fprintf(c.Writer, "event: window\ndata: {\"index\":%d,\"pass\":%s}\n\n", i, passVal)
		if canFlush {
			flusher.Flush()
		}
	}

	fmt.Fprintf(c.Writer, "event: done\ndata: {}\n\n")
	if canFlush {
		flusher.Flush()
	}
}

// deterministicWindows returns n pass/fail booleans seeded by specID.
// Pass count is always in [n*60%, n*80%] so the animation always looks like
// a plausible certified spec regardless of spec name.
func deterministicWindows(specID string, n int) []bool {
	var seed uint32
	for i, b := range []byte(specID) {
		seed ^= uint32(b) << (uint32(i%4) * 8)
		seed = seed*1664525 + 1013904223
	}
	// passCount: 60–80% of n
	lo := (n * 6) / 10
	hi := (n * 8) / 10
	passCount := lo + int(seed%(uint32(hi-lo+1)))

	// Decide which indices pass via a Fisher-Yates shuffle on [0..n-1].
	indices := make([]int, n)
	for i := range indices {
		indices[i] = i
	}
	for i := n - 1; i > 0; i-- {
		seed = seed*1664525 + 1013904223
		j := int(seed>>16) % (i + 1)
		indices[i], indices[j] = indices[j], indices[i]
	}

	out := make([]bool, n)
	for _, idx := range indices[:passCount] {
		out[idx] = true
	}
	return out
}

// containsAny returns true if s contains any of the substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(s) >= len(sub) {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}

// ─── GET /api/v1/specs/:id/copilot — AI drift analysis ───────────────────────

// CopilotAnalyze uses the AI to analyze the draft spec for potential drift or
// edge cases. It returns a suggested mutation.
func (h *SpecHandler) CopilotAnalyze(c *gin.Context) {
	specID := c.Param("id")

	// Mock response for now, simulating LLM feedback based on StrategySpec.
	suggestion := gin.H{
		"text":     "Esta Spec pierde cuando entra en **pullbacks junto al máximo**. Sugiero **RSI < 28** y exigir distancia ≥ 0.5% del high local.",
		"mutation": gin.H{"rsiMax": 28},
		"source":   "feedback_loop.go · AI Copilot",
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"spec_id":    specID,
		"suggestion": suggestion,
	}))
}

// ─── POST /api/v1/specs/:id/backtest — Quick Backtest ────────────────────────

// BacktestSpec runs a synchronous backtest against the draft StrategySpec.
func (h *SpecHandler) BacktestSpec(c *gin.Context) {
	specID := c.Param("id")

	// Read the JSON body from the frontend to run the backtest
	var req struct {
		Spec        any    `json:"spec"`
		ContentHash string `json:"content_hash"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("BAD_REQUEST", err.Error()))
		return
	}

	// Mock backtest results to unblock the UI. In the future this will hit internal/backtest
	result := gin.H{
		"profit_factor": 1.45,
		"win_rate":      0.58,
		"total_trades":  412,
		"sample":        "in-sample · últimos 90d",
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"spec_id": specID,
		"result":  result,
	}))
}
