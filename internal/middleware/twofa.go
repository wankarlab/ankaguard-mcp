// Package middleware provides HTTP middleware for authentication and authorization.
package middleware

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/twofa"
)

// TwoFARequireMiddleware checks the X-TwoFA-Code header and validates it against
// the user's stored TOTP secret. Rejects with 403 if the header is missing or
// the code is invalid.
type TwoFARequireMiddleware struct {
	totp *twofa.TOTPService
	// userSecretFetcher retrieves the TOTP secret and enabled flag for a user_id.
	// It must return ("", false, nil) when the user has no 2FA configured.
	userSecretFetcher func(ctx context.Context, userID string) (secret string, enabled bool, err error)
}

// NewTwoFARequireMiddleware creates a TwoFARequireMiddleware.
//
// Parameters:
//   - totpSvc    — shared TOTPService instance.
//   - fetcher    — function that retrieves (secret, enabled) for a given user_id.
func NewTwoFARequireMiddleware(
	totpSvc *twofa.TOTPService,
	fetcher func(ctx context.Context, userID string) (secret string, enabled bool, err error),
) *TwoFARequireMiddleware {
	return &TwoFARequireMiddleware{
		totp:              totpSvc,
		userSecretFetcher: fetcher,
	}
}

// RequireTwoFA returns a gin middleware that enforces 2FA for sensitive operations.
//
// Flow:
//  1. Extract user_id from gin context (set by JWT middleware).
//  2. Look up whether 2FA is enabled for the user.
//  3. If enabled, validate the X-TwoFA-Code header value.
//  4. Reject with 403 if the header is absent or the code is wrong.
//  5. If 2FA is not enabled for the user, the request passes through unchanged
//     (opt-in enforcement — callers may wrap routes that require mandatory 2FA
//     with an additional check or configure the fetcher to always return enabled=true
//     for those users).
func (m *TwoFARequireMiddleware) RequireTwoFA() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Retrieve user_id from gin context (populated by JWT middleware).
		userID, exists := c.Get("user_id")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "authentication required",
			})
			c.Abort()
			return
		}

		uid, ok := userID.(string)
		if !ok || uid == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "invalid user identity in context",
			})
			c.Abort()
			return
		}

		secret, enabled, err := m.userSecretFetcher(c.Request.Context(), uid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "failed to retrieve 2FA configuration",
			})
			c.Abort()
			return
		}

		// If 2FA is not enabled for this user, let the request through.
		if !enabled {
			c.Next()
			return
		}

		code := c.GetHeader("X-TwoFA-Code")
		if code == "" {
			c.JSON(http.StatusForbidden, gin.H{
				"error": "2FA code required (X-TwoFA-Code header)",
			})
			c.Abort()
			return
		}

		if !m.totp.Verify(secret, code) {
			c.JSON(http.StatusForbidden, gin.H{
				"error": "invalid or expired 2FA code",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}
