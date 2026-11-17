// Package handlers — C1.4: effective per-user permission gates.
//
// GET /api/v1/me/permissions (jwt) returns the caller's user_level, the
// required level per gated screen (from the dynamic config table, not the
// client rbac matrix), the KYC status (derived from JWT claims — "unknown" when
// absent; never invented), and a derived locks list the FE renders as visible
// LockedScreen banners.
package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	tradingMiddleware "github.com/rantipay/trading-mcp/internal/trading/middleware"
	"github.com/sirupsen/logrus"
)

// RBACLevels supplies the required Kong level per gated screen, read from the
// dynamic config table (rbac.level.*). All readers optional: nil → manifest
// defaults (politicas/aprobaciones 3, tribunal/pool 4).
type RBACLevels struct {
	Politicas    func() int
	Aprobaciones func() int
	Tribunal     func() int
	Pool         func() int
}

// PermissionsHandler exposes the caller's effective gates.
type PermissionsHandler struct {
	levels RBACLevels
	log    *logrus.Logger
}

// NewPermissionsHandler constructs a PermissionsHandler.
func NewPermissionsHandler(levels RBACLevels, log *logrus.Logger) *PermissionsHandler {
	if log == nil {
		log = logrus.New()
	}
	return &PermissionsHandler{levels: levels, log: log}
}

// resolveLevel reads a configured required level, falling back to def when the
// reader is nil or returns a non-positive value.
func resolveLevel(fn func() int, def int) int {
	if fn != nil {
		if v := fn(); v > 0 {
			return v
		}
	}
	return def
}

// lockEntry describes one gated screen for the FE LockedScreen pattern.
type lockEntry struct {
	Screen        string `json:"screen"`
	Locked        bool   `json:"locked"`
	RequiredLevel int    `json:"required_level"`
	HowToUnlock   string `json:"how_to_unlock"`
}

// GetPermissions handles GET /api/v1/me/permissions.
func (h *PermissionsHandler) GetPermissions(c *gin.Context) {
	userID := userIDFromContext(c)
	if userID == "" || userID == "default" {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "authentication required"))
		return
	}

	userLevel, _ := tradingMiddleware.GetKongLevelFromContext(c)
	if userLevel < 1 {
		userLevel = 1
	}

	required := gin.H{
		"politicas":    resolveLevel(h.levels.Politicas, 3),
		"aprobaciones": resolveLevel(h.levels.Aprobaciones, 3),
		"tribunal":     resolveLevel(h.levels.Tribunal, 4),
		"pool":         resolveLevel(h.levels.Pool, 4),
	}

	// KYC status: derive from JWT claims. There is no kyc claim today → "unknown".
	// kyc_level may be set by the Kong/JWT middleware in some deployments.
	kycStatus := "unknown"
	if v, ok := c.Get("kyc_status"); ok {
		if s, ok := v.(string); ok && s != "" {
			kycStatus = s
		}
	} else if lvl := kycLevelFromContext(c); lvl > 0 {
		kycStatus = "verified"
	}

	// Locks: one entry per gated screen, with the visible reason/how-to-unlock.
	locks := []lockEntry{
		buildLock("politicas", required["politicas"].(int), userLevel),
		buildLock("aprobaciones", required["aprobaciones"].(int), userLevel),
		buildLock("tribunal", required["tribunal"].(int), userLevel),
		buildLock("pool", required["pool"].(int), userLevel),
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"user_level":      userLevel,
		"required_levels": required,
		"kyc_status":      kycStatus,
		"locks":           locks,
	}))
}

func buildLock(screen string, requiredLevel, userLevel int) lockEntry {
	locked := userLevel < requiredLevel
	how := ""
	if locked {
		how = "Requires access level " + itoa(requiredLevel) + " — upgrade your account level to unlock."
	}
	return lockEntry{
		Screen:        screen,
		Locked:        locked,
		RequiredLevel: requiredLevel,
		HowToUnlock:   how,
	}
}

// itoa is a tiny dependency-free int→string for the unlock message.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
