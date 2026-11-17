// Package httpapi exposes the license over the REST API (GET/PUT /license).
// It is a separate package so the core license package stays free of
// networking imports.
package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/license"
)

const maxBody = 16 * 1024

// Handler serves the operator license endpoints.
type Handler struct {
	Manager *license.Manager
}

// Register mounts GET and PUT /license on rg. The caller passes the
// /api/v1 group and the operator-session middleware, which this package does
// not know about.
func (h *Handler) Register(rg *gin.RouterGroup, operatorOnly gin.HandlerFunc) {
	g := rg.Group("/license", operatorOnly)
	g.GET("", h.Get)
	g.PUT("", h.Put)
}

// Get handles GET /license and returns the LicenseStatus.
func (h *Handler) Get(c *gin.Context) {
	c.JSON(http.StatusOK, h.Manager.Status())
}

type putRequest struct {
	License string `json:"license"`
}

// Put handles PUT /license: installs a new license document in caliente. An
// invalid document yields 400 LICENSE_INVALID and keeps the previous license.
func (h *Handler) Put(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBody)
	var req putRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.License == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "license is invalid", "code": "LICENSE_INVALID", "reason": license.ReasonMalformed,
		})
		return
	}
	st, err := h.Manager.Install(req.License)
	if err != nil {
		if reason := license.Reason(err); reason != "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "license is invalid", "code": "LICENSE_INVALID", "reason": reason,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not store the license", "code": "LICENSE_STORE_FAILED"})
		return
	}
	c.JSON(http.StatusOK, st)
}
