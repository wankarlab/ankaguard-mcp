// Package handlers — KYC endpoints (API-9).
// GET /api/v1/kyc/level/:user_id  — returns KYC level from Auth service.
// POST /api/v1/kyc/verify/:flow   — checks if caller's KYC meets a flow's requirement.
// Gated by capital_flow != 'self' (default 'self' = no KYC required, RBAC-3).
package handlers

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/sirupsen/logrus"
)

// kycClient is the minimal interface the KYCHandler needs.
// Satisfied by *services.KYCVerifier.
type kycClient interface {
	KYCLevel(ctx context.Context, userID string) (int, error)
	CheckLevel(ctx context.Context, userID string, required int, capitalFlow string) error
}

// KYCHandler exposes KYC-level inspection and gating over REST.
type KYCHandler struct {
	verifier kycClient
	log      *logrus.Logger
}

// NewKYCHandler constructs a KYCHandler. verifier may be nil (returns 503).
func NewKYCHandler(verifier kycClient, log *logrus.Logger) *KYCHandler {
	if log == nil {
		log = logrus.New()
	}
	return &KYCHandler{verifier: verifier, log: log}
}

// kycFlowRequirements maps capital_flow name → minimum required KYC level.
// capital_flow='self' (default) is handled upstream (no KYC required).
var kycFlowRequirements = map[string]int{
	"copy":      1, // F2: basic KYC to follow a strategy
	"prop":      2, // F3: enhanced KYC for prop-firm capital
	"pool":      3, // F4: full KYC for pooled funds
	"whitelist": 2, // enterprise white-label
}

// GetLevel handles GET /api/v1/kyc/level/:user_id.
// Returns the verified KYC level (0=unverified, 1=basic, 2=enhanced, 3=full)
// for the given user_id by querying Auth :10100.
// Level 1+ (any authenticated user can query their own or an arbiter can query any).
func (h *KYCHandler) GetLevel(c *gin.Context) {
	if h.verifier == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("KYC_UNAVAILABLE", "KYC verifier not configured"))
		return
	}

	targetUserID := c.Param("user_id")
	if targetUserID == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("VALIDATION_FAILED", "user_id is required"))
		return
	}

	// Only allow querying own level unless caller is level 3+ (reviewer/arbiter).
	callerID := userIDFromContext(c)
	if callerID != targetUserID {
		level, exists := c.Get("kong_level")
		callerLevel := 0
		if exists {
			callerLevel, _ = level.(int)
		}
		if callerLevel < 3 {
			c.JSON(http.StatusForbidden, models.NewErrorResponse("FORBIDDEN", "can only query your own KYC level"))
			return
		}
	}

	kycLevel, err := h.verifier.KYCLevel(c.Request.Context(), targetUserID)
	if err != nil {
		h.log.WithError(err).WithField("user_id", targetUserID).Warn("KYC level lookup failed")
		c.JSON(http.StatusBadGateway, models.NewErrorResponse("KYC_LOOKUP_FAILED", err.Error()))
		return
	}

	levelNames := []string{"unverified", "basic", "enhanced", "full"}
	levelName := "unknown"
	if kycLevel >= 0 && kycLevel < len(levelNames) {
		levelName = levelNames[kycLevel]
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"user_id":    targetUserID,
		"level":      kycLevel,
		"level_name": levelName,
	}))
}

// VerifyFlow handles POST /api/v1/kyc/verify/:flow.
// Checks whether the authenticated caller's KYC level meets the requirement
// for the requested capital_flow. Returns 200 OK if sufficient, 403 if not.
// capital_flow='self' always returns 200 (no KYC required, RBAC-3 rule zero).
func (h *KYCHandler) VerifyFlow(c *gin.Context) {
	if h.verifier == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("KYC_UNAVAILABLE", "KYC verifier not configured"))
		return
	}

	flow := c.Param("flow")
	if flow == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("VALIDATION_FAILED", "flow is required"))
		return
	}

	// capital_flow='self' → no KYC requirement (RBAC-3 default).
	if flow == "self" {
		c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
			"flow":       flow,
			"sufficient": true,
			"note":       "capital_flow=self requires no KYC",
		}))
		return
	}

	required, ok := kycFlowRequirements[flow]
	if !ok {
		c.JSON(http.StatusBadRequest, models.NewErrorResponseWithDetails(
			"VALIDATION_FAILED",
			"unknown capital_flow",
			"supported: self, copy, prop, pool, whitelist",
		))
		return
	}

	callerID := userIDFromContext(c)
	if callerID == "" {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "authentication required"))
		return
	}

	if err := h.verifier.CheckLevel(c.Request.Context(), callerID, required, flow); err != nil {
		h.log.WithError(err).WithFields(logrus.Fields{
			"user_id":  callerID,
			"flow":     flow,
			"required": required,
		}).Info("KYC gate rejected")
		c.JSON(http.StatusForbidden, models.NewErrorResponseWithDetails(
			"KYC_INSUFFICIENT",
			"KYC level insufficient for this capital flow",
			err.Error(),
		))
		return
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"flow":          flow,
		"sufficient":    true,
		"required_level": required,
	}))
}
