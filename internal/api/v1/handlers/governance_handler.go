// Package handlers — C1.3 / C3.2 / C3.3 governance endpoints.
//
// Consent (informed-consent gate pre-copy):
//
//	GET  /api/v1/consent/template/:kind   (public) — versioned legal text
//	GET  /api/v1/consent/status?kind=...  (jwt)    — caller's acceptance state
//	POST /api/v1/consent                  (jwt)    — record acceptance (idempotent)
//
// Adversarial review queue (C3.2):
//
//	GET  /api/v1/adversarial/queue        (jwt) — items pending review
//	POST /api/v1/adversarial/:id/flag     (jwt) — record a reviewer flag
//
// Appeals / tribunal (C3.3):
//
//	GET  /api/v1/appeals                  (jwt) — list appeals
//	POST /api/v1/appeals                  (jwt) — create an appeal
//
// All responses use the standard {success, data, timestamp} envelope (snake_case).
package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/rantipay/trading-mcp/internal/storage"
	"github.com/sirupsen/logrus"
)

// GovernanceHandler serves the consent, adversarial-review, and appeals domains.
// Each backing repository is optional: a nil repo makes its endpoints respond
// 503 so the router can wire the routes unconditionally.
type GovernanceHandler struct {
	consent    *storage.ConsentRepository
	governance *storage.GovernanceRepository
	log        *logrus.Logger
}

// NewGovernanceHandler constructs a GovernanceHandler. Either repo may be nil.
func NewGovernanceHandler(consent *storage.ConsentRepository, governance *storage.GovernanceRepository, log *logrus.Logger) *GovernanceHandler {
	if log == nil {
		log = logrus.New()
	}
	return &GovernanceHandler{consent: consent, governance: governance, log: log}
}

// tenantFromContext returns the tenant id set by the tenant middleware, or "".
func tenantFromContext(c *gin.Context) string {
	if v, ok := c.Get("tenant_id"); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// ── C1.3: consent ──────────────────────────────────────────────────────────

// GetConsentTemplate handles GET /api/v1/consent/template/:kind (public).
func (h *GovernanceHandler) GetConsentTemplate(c *gin.Context) {
	if h.consent == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("CONSENT_UNAVAILABLE", "consent store not configured"))
		return
	}
	kind := c.Param("kind")
	if kind == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("VALIDATION_FAILED", "kind is required"))
		return
	}

	tpl, err := h.consent.LatestTemplate(c.Request.Context(), kind)
	if err != nil {
		h.log.WithError(err).WithField("kind", kind).Error("LatestTemplate failed")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", err.Error()))
		return
	}
	if tpl == nil {
		c.JSON(http.StatusNotFound, models.NewErrorResponse("NOT_FOUND", "no consent template for kind"))
		return
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"kind":    tpl.Kind,
		"version": tpl.Version,
		"body_md": tpl.BodyMD,
	}))
}

// GetConsentStatus handles GET /api/v1/consent/status?kind=copy_trading (jwt).
func (h *GovernanceHandler) GetConsentStatus(c *gin.Context) {
	if h.consent == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("CONSENT_UNAVAILABLE", "consent store not configured"))
		return
	}
	kind := c.Query("kind")
	if kind == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("VALIDATION_FAILED", "kind query param is required"))
		return
	}

	userID := userIDFromContext(c)
	st, err := h.consent.Status(c.Request.Context(), userID, kind)
	if err != nil {
		h.log.WithError(err).Error("consent Status failed")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", err.Error()))
		return
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"kind":             st.Kind,
		"accepted":         st.Accepted,
		"accepted_version": st.AcceptedVersion,
	}))
}

// postConsentRequest is the body for POST /api/v1/consent.
type postConsentRequest struct {
	Kind            string `json:"kind" binding:"required"`
	TemplateVersion int    `json:"template_version" binding:"required"`
}

// PostConsent handles POST /api/v1/consent (jwt). Idempotent.
func (h *GovernanceHandler) PostConsent(c *gin.Context) {
	if h.consent == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("CONSENT_UNAVAILABLE", "consent store not configured"))
		return
	}

	var req postConsentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("VALIDATION_FAILED", err.Error()))
		return
	}

	userID := userIDFromContext(c)
	tenantID := tenantFromContext(c)
	ip := c.ClientIP()

	if err := h.consent.Accept(c.Request.Context(), userID, tenantID, req.Kind, req.TemplateVersion, ip); err != nil {
		h.log.WithError(err).Error("consent Accept failed")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", err.Error()))
		return
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"kind":             req.Kind,
		"accepted":         true,
		"accepted_version": req.TemplateVersion,
	}))
}

// ── C3.2: adversarial review queue ──────────────────────────────────────────

// GetAdversarialQueue handles GET /api/v1/adversarial/queue (jwt).
func (h *GovernanceHandler) GetAdversarialQueue(c *gin.Context) {
	if h.governance == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("ADVERSARIAL_UNAVAILABLE", "governance store not configured"))
		return
	}

	items, err := h.governance.PendingItems(c.Request.Context())
	if err != nil {
		h.log.WithError(err).Error("PendingItems failed")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", err.Error()))
		return
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"items": items,
		"total": len(items),
	}))
}

// postFlagRequest is the body for POST /api/v1/adversarial/:id/flag.
type postFlagRequest struct {
	Reason string `json:"reason" binding:"required"`
}

// PostAdversarialFlag handles POST /api/v1/adversarial/:id/flag (jwt).
func (h *GovernanceHandler) PostAdversarialFlag(c *gin.Context) {
	if h.governance == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("ADVERSARIAL_UNAVAILABLE", "governance store not configured"))
		return
	}

	itemID := c.Param("id")
	if itemID == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("VALIDATION_FAILED", "item id is required"))
		return
	}

	var req postFlagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("VALIDATION_FAILED", err.Error()))
		return
	}

	exists, err := h.governance.ItemExists(c.Request.Context(), itemID)
	if err != nil {
		h.log.WithError(err).Error("ItemExists failed")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", err.Error()))
		return
	}
	if !exists {
		c.JSON(http.StatusNotFound, models.NewErrorResponse("NOT_FOUND", "adversarial item not found"))
		return
	}

	reviewerID := userIDFromContext(c)
	tenantID := tenantFromContext(c)
	flagID, err := h.governance.AddFlag(c.Request.Context(), itemID, reviewerID, tenantID, req.Reason)
	if err != nil {
		h.log.WithError(err).Error("AddFlag failed")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", err.Error()))
		return
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"flag_id":     flagID,
		"item_id":     itemID,
		"reviewer_id": reviewerID,
	}))
}

// ── C3.3: appeals (tribunal) ────────────────────────────────────────────────

// GetAppeals handles GET /api/v1/appeals (jwt).
func (h *GovernanceHandler) GetAppeals(c *gin.Context) {
	if h.governance == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("APPEALS_UNAVAILABLE", "governance store not configured"))
		return
	}

	appeals, err := h.governance.ListAppeals(c.Request.Context())
	if err != nil {
		h.log.WithError(err).Error("ListAppeals failed")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", err.Error()))
		return
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"appeals": appeals,
		"total":   len(appeals),
	}))
}

// postAppealRequest is the body for POST /api/v1/appeals.
type postAppealRequest struct {
	SpecID string `json:"spec_id" binding:"required"`
	Reason string `json:"reason" binding:"required"`
}

// PostAppeal handles POST /api/v1/appeals (jwt).
func (h *GovernanceHandler) PostAppeal(c *gin.Context) {
	if h.governance == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("APPEALS_UNAVAILABLE", "governance store not configured"))
		return
	}

	var req postAppealRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("VALIDATION_FAILED", err.Error()))
		return
	}

	appellantID := userIDFromContext(c)
	tenantID := tenantFromContext(c)
	id, err := h.governance.CreateAppeal(c.Request.Context(), req.SpecID, appellantID, tenantID, req.Reason)
	if err != nil {
		h.log.WithError(err).Error("CreateAppeal failed")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", err.Error()))
		return
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"id":           id,
		"spec_id":      req.SpecID,
		"appellant_id": appellantID,
		"status":       "open",
	}))
}
