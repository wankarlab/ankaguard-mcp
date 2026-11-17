// Package productapi serves the product routes shared by the server, the
// licence service and the console (docs/product/api-contract.md): the paper
// account reset and the product status.
//
// All routes expect to be mounted behind the JWT middleware
// (tradingmw.KongJWTMiddleware), which leaves user_level, token_use and
// mcp_profile in the gin context.
package productapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// OperatorLevel is the minimum user level of an operator session.
const OperatorLevel = 4

// tokenUseOperatorSession is the token_use claim of an operator session issued
// by internal/localauth.
const tokenUseOperatorSession = "operator_session"

// LicenseStatus is the licence part of the product status (api-contract.md).
type LicenseStatus struct {
	Edition            string  `json:"edition"` // community | pro
	State              string  `json:"state"`   // valid | grace | expired | invalid
	Holder             *string `json:"holder"`
	LicenseID          *string `json:"license_id"`
	ExpiresAt          *string `json:"expires_at"`
	DaysRemaining      *int    `json:"days_remaining"`
	GraceDaysRemaining int     `json:"grace_days_remaining"`
	Warning            *string `json:"warning"`
}

// LicenseSource is the HOOK for the licence service (task group 7).
//
// Until internal/license provides the real status, main.go wires
// ProvisionalLicense, which reports the community edition with state
// "invalid". To plug the real service in, implement this interface on the
// licence service and pass it in Config.License; nothing else in this package
// has to change. Status must be cheap (it runs on every GET of the product
// status) and must never include the licence document.
type LicenseSource interface {
	Status(ctx context.Context) LicenseStatus
}

// ProvisionalLicense is the placeholder LicenseSource used until the licence
// service exists.
type ProvisionalLicense struct{}

// Status reports the community edition with an invalid licence.
func (ProvisionalLicense) Status(context.Context) LicenseStatus {
	return LicenseStatus{Edition: "community", State: "invalid"}
}

// PaperResetter resets a paper account to its initial balance.
type PaperResetter interface {
	ResetAccount(ctx context.Context, holder string) error
}

// Config configures the API.
type Config struct {
	// Standalone is true when auth.mode is standalone: an operator session must
	// then also carry token_use=operator_session.
	Standalone bool
	AuthMode   string
	// Build is "standard", "product" or "product-pro".
	Build string
	// Paper resets paper accounts; nil answers 503 PAPER_UNAVAILABLE.
	Paper PaperResetter
	// InitialBalance returns paper.initial_balance_usd.
	InitialBalance func() float64
	// LiveEnabled returns trading.live_enabled.
	LiveEnabled func() bool
	// License supplies the licence status; nil = ProvisionalLicense.
	License LicenseSource
	Log     *logrus.Logger
}

// API holds the handlers.
type API struct{ cfg Config }

// New builds the API.
func New(cfg Config) *API {
	if cfg.License == nil {
		cfg.License = ProvisionalLicense{}
	}
	if cfg.Log == nil {
		cfg.Log = logrus.New()
	}
	return &API{cfg: cfg}
}

// RegisterRoutes mounts the routes on rg, expected to be the /api/v1 group
// behind the JWT middleware.
func (a *API) RegisterRoutes(rg *gin.RouterGroup) {
	rg.POST("/paper/accounts/:holder/reset", a.handlePaperReset)
	rg.GET("/product/status", a.handleProductStatus)
}

// IsOperator reports whether the request carries an operator session: user
// level 4 or higher and, in standalone mode, token_use=operator_session.
func (a *API) IsOperator(c *gin.Context) bool {
	return IsOperator(c, a.cfg.Standalone)
}

// IsOperator is the stateless variant of API.IsOperator.
func IsOperator(c *gin.Context, standalone bool) bool {
	level, ok := c.Get("user_level")
	if !ok {
		return false
	}
	if l, isInt := level.(int); !isInt || l < OperatorLevel {
		return false
	}
	if standalone {
		return strings.EqualFold(c.GetString("token_use"), tokenUseOperatorSession)
	}
	return true
}

func abort(c *gin.Context, status int, code, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"error": msg, "code": code})
}

func (a *API) handlePaperReset(c *gin.Context) {
	if _, ok := c.Get("user_id"); !ok {
		abort(c, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}
	if !a.IsOperator(c) {
		abort(c, http.StatusForbidden, "OPERATOR_REQUIRED", "only an operator session can reset a paper account")
		return
	}
	if a.cfg.Paper == nil {
		abort(c, http.StatusServiceUnavailable, "PAPER_UNAVAILABLE", "the paper broker is not available")
		return
	}
	holder := c.Param("holder")
	if holder == "" {
		abort(c, http.StatusBadRequest, "INVALID_HOLDER", "holder is required")
		return
	}
	if err := a.cfg.Paper.ResetAccount(c.Request.Context(), holder); err != nil {
		a.cfg.Log.WithError(err).WithField("holder", holder).Warn("paper account reset failed")
		abort(c, http.StatusInternalServerError, "PAPER_RESET_FAILED", "the paper account could not be reset")
		return
	}
	balance := 0.0
	if a.cfg.InitialBalance != nil {
		balance = a.cfg.InitialBalance()
	}
	a.cfg.Log.WithField("holder", holder).Info("paper account reset by operator")
	c.JSON(http.StatusOK, gin.H{"holder": holder, "balance_usd": balance})
}

func (a *API) handleProductStatus(c *gin.Context) {
	if _, ok := c.Get("user_id"); !ok {
		abort(c, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}
	lic := a.cfg.License.Status(c.Request.Context())
	live := true
	if a.cfg.LiveEnabled != nil {
		live = a.cfg.LiveEnabled()
	}
	build := a.cfg.Build
	if build == "" {
		build = "standard"
	}
	c.JSON(http.StatusOK, gin.H{
		"edition":      lic.Edition,
		"build":        build,
		"auth_mode":    a.cfg.AuthMode,
		"live_enabled": live,
		"license":      lic,
	})
}
