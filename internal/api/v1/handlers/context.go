// Package handlers — shared context helpers for REST API v1 handlers.
package handlers

import (
	"github.com/gin-gonic/gin"
)

// userIDFromContext extracts the user ID from the Gin context.
// The value is set by KongJWTMiddleware under the key "user_id" (sourced from the
// JWT "sub" claim). Returns "default" when no JWT-scoped user is present so that
// legacy / non-JWT request paths are unaffected.
func userIDFromContext(c *gin.Context) string {
	if v, exists := c.Get("user_id"); exists {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return "default"
}
