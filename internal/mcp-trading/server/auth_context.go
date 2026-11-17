package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/toolpolicy"
	tradingmw "github.com/rantipay/trading-mcp/internal/trading/middleware"
)

// contextKey is a private type for context keys defined in this package, per
// Go convention, to avoid collisions with keys defined in other packages.
type contextKey string

const (
	// UserIDContextKey holds the authenticated caller's user id (JWT "sub"/
	// "user_id" claim), as a string, once the request has passed JWT auth.
	UserIDContextKey contextKey = "mcp_auth_user_id"
	// UserLevelContextKey holds the authenticated caller's numeric level
	// (int), resolved from the JWT "user_level"/"level" claim (default 1).
	UserLevelContextKey contextKey = "mcp_auth_user_level"
	// TenantIDContextKey holds the caller's tenant id, when present on the JWT.
	TenantIDContextKey contextKey = "mcp_auth_tenant_id"
	// CompanyIDContextKey holds the caller's company id, when present on the JWT.
	CompanyIDContextKey contextKey = "mcp_auth_company_id"
	// ProfileContextKey holds the caller's resolved MCP permission profile
	// (toolpolicy.Profile), derived from the JWT "mcp_profile" claim and level.
	ProfileContextKey contextKey = "mcp_auth_profile"
	// TokenIDContextKey holds an identifier of the presented token for audit
	// trails ("jti:<id>" or "sub:<subject>"). It never contains the token.
	TokenIDContextKey contextKey = "mcp_auth_token_id"
)

// ProfileFromContext reads the MCP profile set by requireJWTAuth.
func ProfileFromContext(ctx context.Context) (toolpolicy.Profile, bool) {
	v, ok := ctx.Value(ProfileContextKey).(toolpolicy.Profile)
	return v, ok
}

// TokenIDFromContext reads the audit identifier of the presented token.
func TokenIDFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(TokenIDContextKey).(string)
	return v, ok && v != ""
}

// tokenAuditID builds the audit identifier of a token from its jti, falling
// back to the subject. The raw token is never used.
func tokenAuditID(jti, subject string) string {
	if jti != "" {
		return "jti:" + jti
	}
	if subject != "" {
		return "sub:" + subject
	}
	return "unknown"
}

// UserIDFromContext reads the authenticated user id set by requireJWTAuth.
// Returns ("", false) when the request context carries no authenticated
// identity (e.g. stdio mode, or auth disabled).
func UserIDFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(UserIDContextKey).(string)
	return v, ok && v != ""
}

// UserLevelFromContext reads the authenticated user level set by
// requireJWTAuth. Returns (0, false) when absent.
func UserLevelFromContext(ctx context.Context) (int, bool) {
	v, ok := ctx.Value(UserLevelContextKey).(int)
	return v, ok
}

// SetJWTAuth wires the Kong Gateway JWT secret and internal-gateway shared
// token into the MCP server so its own raw HTTP mux (port 10600) can validate
// requests itself, instead of relying entirely on the separate API Gateway
// (port 9090) to have been the only reachable entrypoint. Without this, the
// MCP server accepted unauthenticated tools/call and ai-bot control requests
// on its own port (CORE-01).
//
// enabled mirrors the same ENABLE_JWT_AUTH flag used for the API Gateway/REST
// v1 router, so operators keep one on/off switch across the whole service.
func (s *MCPServer) SetJWTAuth(secret, gatewayToken string, enabled bool) {
	s.jwtSecret = secret
	s.jwtGatewayToken = gatewayToken
	s.jwtAuthEnabled = enabled
}

// requireJWTAuth wraps an http.HandlerFunc so it only runs after a valid Kong
// Gateway JWT (or trusted internal-gateway bypass) has been presented. On
// success it injects the authenticated user id/level/tenant/company into the
// request context so downstream tool handlers can read the caller's real
// identity (see UserIDFromContext) instead of trusting client-supplied
// user_id fields in the request body (TOOL-04).
//
// When s.jwtAuthEnabled is false (ENABLE_JWT_AUTH=false, development only),
// requests pass through unauthenticated — matching the same escape hatch the
// REST v1 router and API Gateway already use, so this stays a single switch.
func (s *MCPServer) requireJWTAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.jwtAuthEnabled {
			next(w, r)
			return
		}

		claims, level, err := tradingmw.AuthenticateHTTPRequest(r, s.jwtSecret, s.jwtGatewayToken)
		if errors.Is(err, tradingmw.ErrOperatorRequired) {
			s.log.WithField("path", r.URL.Path).Warn("MCP HTTP request rejected: agent token outside the MCP endpoint")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprintf(w, `{"error":%q,"code":%q}`, tradingmw.ErrOperatorRequired.Error(), tradingmw.OperatorRequiredCode)
			return
		}
		if err != nil {
			s.log.WithError(err).WithField("path", r.URL.Path).Warn("MCP HTTP request rejected: JWT auth failed")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, `{"error":"unauthorized: %s"}`, err.Error())
			return
		}

		userID := claims.UserID
		if userID == "" {
			userID = claims.Subject
		}

		ctx := r.Context()
		ctx = context.WithValue(ctx, UserIDContextKey, userID)
		ctx = context.WithValue(ctx, UserLevelContextKey, level)
		ctx = context.WithValue(ctx, TenantIDContextKey, claims.TenantID)
		ctx = context.WithValue(ctx, CompanyIDContextKey, claims.CompanyID)
		ctx = context.WithValue(ctx, ProfileContextKey, toolpolicy.ProfileFromClaim(claims.MCPProfile, level))
		ctx = context.WithValue(ctx, TokenIDContextKey, tokenAuditID(claims.ID, userID))

		next(w, r.WithContext(ctx))
	}
}

// SetPublicHealth installs the source of the three build fields served by the
// public GET /health: version, commit and the effective edition.
func (s *MCPServer) SetPublicHealth(fn func() (version, commit, edition string)) {
	s.publicHealth = fn
}

// HealthDetail returns the detailed health payload (the one GET /health used
// to serve publicly) for in-process consumers such as the console health
// aggregator, which must not depend on a public endpoint for it.
func (s *MCPServer) HealthDetail() map[string]interface{} {
	return map[string]interface{}{
		"status":        "ok",
		"gct_connected": s.gctClient.IsConnected(),
		"tools_count":   s.registry.Count(),
		"request_count": s.requestCount,
		"error_count":   s.errorCount,
		"uptime":        time.Since(s.startTime).String(),
		"sandbox":       s.tradingModeInfo(),
	}
}

// requireJWTHandler is requireJWTAuth for an http.Handler.
func (s *MCPServer) requireJWTHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(s.requireJWTAuth(h.ServeHTTP))
}

// HealthSummary returns (GCT connected, engine real money) for in-process
// consumers; it reads the same data the detailed health route serves.
func (s *MCPServer) HealthSummary() (gctConnected, engineRealMoney bool) {
	gctConnected = s.gctClient.IsConnected()
	if m, ok := s.tradingModeInfo()["engine_real_money"].(bool); ok {
		engineRealMoney = m
	}
	return gctConnected, engineRealMoney
}

// OmitDeviceRoutes leaves the device-registration route
// (/api/v1/user/fcm-token) unmounted. The product edition calls it: push
// notifications go through RantiPay services that the product does not ship.
// Must be called before the HTTP listener starts.
func (s *MCPServer) OmitDeviceRoutes() { s.omitDeviceRoutes = true }
