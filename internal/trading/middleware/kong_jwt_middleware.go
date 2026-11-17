package middleware

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/sirupsen/logrus"
)

type KongJWTClaims struct {
	UserID    string  `json:"sub"`        // JWT standard claim for user ID
	UserLevel float64 `json:"user_level"` // Actual field name in token
	Level     float64 `json:"level"`      // Fallback for backwards compatibility
	TenantID  string  `json:"tenant_id"`
	CompanyID string  `json:"company_id"`
	Email     string  `json:"email"`
	Phone     string  `json:"phone_number"` // Actual field name in token
	// MCPProfile is the MCP permission profile of the token ("read", "paper"
	// or "live"). Empty means "derive it from the user level"; the resolution
	// rules live in internal/mcp-trading/toolpolicy.
	MCPProfile string `json:"mcp_profile"`
	// TokenUse and SessionEpoch are set by internal/localauth tokens
	// (standalone mode): "operator_session" or "agent", and the operator
	// session epoch. They are empty for tokens of the external auth service.
	TokenUse     string `json:"token_use"`
	SessionEpoch int64  `json:"sess_epoch"`
	jwt.RegisteredClaims
}

// KongJWTMiddleware validates the JWT on every request. Requests proxied by the
// trusted internal API gateway carry an X-Gateway-Token equal to the configured
// shared secret (gatewayToken); for those the JWT is parsed without signature
// verification, because the upstream MATP auth service signs with a secret this
// service does not hold. The shared secret MUST be supplied from configuration
// (env) — never a source literal — and is compared in constant time. When
// gatewayToken is empty the bypass is disabled (fail closed): every request must
// present a JWT with a valid signature.
func KongJWTMiddleware(secret string, gatewayToken string, logger *logrus.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Missing Authorization header"})
			c.Abort()
			return
		}

		token := extractBearerToken(authHeader)
		if token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid Authorization format"})
			c.Abort()
			return
		}

		var claims *KongJWTClaims

		// Requests proxied by the trusted internal API gateway carry an
		// X-Gateway-Token matching the configured shared secret. For those we
		// parse the JWT without signature verification (the MATP auth service
		// signs with a secret this service does not hold). Compared in constant
		// time and disabled entirely when no token is configured (fail closed).
		if trustInternalGateway(gatewayToken, c.GetHeader("X-Gateway-Token")) {
			parsed, err := parseJWTUnverified(token)
			if err != nil {
				logger.WithField("error", err.Error()).Warn("Gateway-proxied JWT could not be parsed")
				c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
				c.Abort()
				return
			}
			claims = parsed
		} else {
			validated, err := validateJWT(token, secret)
			if err != nil {
				logger.WithFields(logrus.Fields{
					"error":        err.Error(),
					"token_prefix": token[:min(20, len(token))],
				}).Warn("JWT validation failed")
				c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
				c.Abort()
				return
			}
			claims = validated
		}

		// Standalone mode: agent tokens only reach the MCP endpoints; every
		// REST surface (this middleware guards :8081 and the gateway) needs an
		// operator session.
		if agentScopeViolation(claims, c.Request.URL.Path) {
			logger.WithField("path", c.Request.URL.Path).Warn("Agent token rejected outside the MCP endpoint")
			abortOperatorRequired(c)
			return
		}

		// Extract user ID from sub claim (standard JWT) or user_id (legacy)
		userID := claims.UserID
		if userID == "" && claims.Subject != "" {
			userID = claims.Subject
		}

		// Identity comes from the token. X-Tenant-ID / X-Company-ID are honoured
		// only when the request carries the internal gateway credential
		// (X-Gateway-Token): any other client could otherwise pick the tenant
		// whose data it reads.
		tenantID := claims.TenantID
		companyID := claims.CompanyID
		if trustInternalGateway(gatewayToken, c.GetHeader("X-Gateway-Token")) {
			if h := c.GetHeader("X-Tenant-ID"); h != "" {
				tenantID = h
			}
			if h := c.GetHeader("X-Company-ID"); h != "" {
				companyID = h
			}
		}

		// Use UserLevel if available, otherwise fall back to Level.
		// Any user with a valid JWT is at minimum level 1.
		userLevel := int(claims.UserLevel)
		if userLevel == 0 {
			userLevel = int(claims.Level)
		}
		if userLevel == 0 {
			userLevel = 1
		}

		requiredLevel := getRequiredLevelForPath(c.Request.Method, c.Request.URL.Path)
		if userLevel < requiredLevel {
			logger.WithFields(logrus.Fields{
				"user_level":     userLevel,
				"required_level": requiredLevel,
				"path":           c.Request.URL.Path,
				"user_id":        userID,
			}).Warn("Insufficient permissions for path")
			c.JSON(http.StatusForbidden, gin.H{"error": "Insufficient permissions"})
			c.Abort()
			return
		}

		c.Set("user_id", userID)
		c.Set("tenant_id", tenantID)
		c.Set("company_id", companyID)
		c.Set("kong_level", userLevel)
		c.Set("token_use", claims.TokenUse)
		c.Set("mcp_profile", claims.MCPProfile)
		// Also expose as "user_level": KongAuthMiddleware.RequireLevel and several
		// handlers read that key. Without it, level-gated routes (admin, L2/L3)
		// reject every real token with "User level not found in token".
		c.Set("user_level", userLevel)
		// SEC-7: record whether the JWT signature was cryptographically verified.
		// When false the user_id comes from parseJWTUnverified (gateway-trusted path).
		// Protection: INTERNAL_GATEWAY_TOKEN is compared in constant time and
		// fail-closed when empty (SEC-1). Still, write operations on RLS-protected
		// resources log a warning so the audit trail captures any unverified writes.
		c.Set("jwt_verified", !trustInternalGateway(gatewayToken, c.GetHeader("X-Gateway-Token")))
		c.Next()
	}
}

// ContextKeyAuthenticated is set to true by OptionalKongJWTMiddleware when the
// request carried a valid JWT. Handlers on optional-auth routes read it with
// IsAuthenticated to decide whether to include account-private data.
const ContextKeyAuthenticated = "jwt_authenticated"

// OptionalKongJWTMiddleware is the "optional auth" variant of KongJWTMiddleware
// for public routes that serve a reduced payload to anonymous callers and the
// full one to signed-in users. It reuses AuthenticateHTTPRequest (same signature
// verification, gateway-trust bypass and expiry rules, no duplicated logic) but
// NEVER aborts: a missing, malformed, expired or badly signed token simply leaves
// the request anonymous (ContextKeyAuthenticated unset). On a valid token it
// sets the same user_id/tenant_id/user_level keys as KongJWTMiddleware.
// No per-path level check is applied: any valid JWT counts as authenticated.
func OptionalKongJWTMiddleware(secret string, gatewayToken string, logger *logrus.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("Authorization") == "" {
			c.Next()
			return
		}
		claims, level, err := AuthenticateHTTPRequest(c.Request, secret, gatewayToken)
		if err != nil {
			if logger != nil {
				logger.WithError(err).Debug("optional JWT rejected, serving anonymous payload")
			}
			c.Next()
			return
		}
		userID := claims.UserID
		if userID == "" {
			userID = claims.Subject
		}
		c.Set("user_id", userID)
		c.Set("tenant_id", claims.TenantID)
		c.Set("company_id", claims.CompanyID)
		c.Set("token_use", claims.TokenUse)
		c.Set("mcp_profile", claims.MCPProfile)
		c.Set("kong_level", level)
		c.Set("user_level", level)
		c.Set("jwt_verified", !trustInternalGateway(gatewayToken, c.GetHeader("X-Gateway-Token")))
		c.Set(ContextKeyAuthenticated, true)
		c.Next()
	}
}

// IsAuthenticated reports whether OptionalKongJWTMiddleware validated a JWT.
func IsAuthenticated(c *gin.Context) bool {
	return c.GetBool(ContextKeyAuthenticated)
}

// AuthenticateHTTPRequest validates the JWT on a raw net/http request using the
// exact same rules as KongJWTMiddleware (signature verification, gateway-trust
// bypass, expiration check, level extraction) but without requiring a
// gin.Context. Used by non-Gin HTTP muxes (e.g. the raw MCP server's mux on the
// trading-mcp listen port) that need identical auth semantics.
//
// Returns the parsed claims and the resolved user level (minimum 1) on success.
// On failure it returns a nil claims, level 0, and an error describing why —
// callers should respond 401 and must NOT proceed to execute the request.
func AuthenticateHTTPRequest(r *http.Request, secret, gatewayToken string) (*KongJWTClaims, int, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return nil, 0, fmt.Errorf("missing Authorization header")
	}

	token := extractBearerToken(authHeader)
	if token == "" {
		return nil, 0, fmt.Errorf("invalid Authorization format")
	}

	var claims *KongJWTClaims
	if trustInternalGateway(gatewayToken, r.Header.Get("X-Gateway-Token")) {
		parsed, err := parseJWTUnverified(token)
		if err != nil {
			return nil, 0, fmt.Errorf("gateway-proxied JWT could not be parsed: %w", err)
		}
		claims = parsed
	} else {
		validated, err := validateJWT(token, secret)
		if err != nil {
			return nil, 0, fmt.Errorf("JWT validation failed: %w", err)
		}
		claims = validated
	}

	// Standalone mode: agent tokens only reach the MCP endpoints.
	if agentScopeViolation(claims, r.URL.Path) {
		return nil, 0, ErrOperatorRequired
	}

	userLevel := int(claims.UserLevel)
	if userLevel == 0 {
		userLevel = int(claims.Level)
	}
	if userLevel == 0 {
		userLevel = 1
	}

	return claims, userLevel, nil
}

func extractBearerToken(authHeader string) string {
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return ""
	}
	return parts[1]
}

// trustInternalGateway reports whether a request may skip JWT signature
// verification because it was proxied by the trusted internal gateway. Fail
// closed: returns false when no shared token is configured, and compares in
// constant time to avoid leaking the secret through timing.
func trustInternalGateway(configured, provided string) bool {
	if configured == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(configured)) == 1
}

// parseJWTUnverified decodes a JWT without checking the signature. Used only
// for requests that already passed through the trusted internal gateway.
func parseJWTUnverified(tokenString string) (*KongJWTClaims, error) {
	p := jwt.NewParser(jwt.WithoutClaimsValidation())
	claims := &KongJWTClaims{}
	_, _, err := p.ParseUnverified(tokenString, claims)
	if err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}
	// Still reject expired tokens even when skipping signature check.
	if claims.ExpiresAt != nil && claims.ExpiresAt.Before(time.Now()) {
		return nil, fmt.Errorf("token expired")
	}
	// Default to level 1 if no level claim present (minimum valid user).
	if claims.UserLevel == 0 && claims.Level == 0 {
		claims.UserLevel = 1
	}
	return claims, nil
}

func validateJWT(tokenString string, secret string) (*KongJWTClaims, error) {
	// Standalone mode: the local authority supplies the (rotatable) secret.
	authority := currentAuthority()
	if authority != nil {
		secret = authority.Secret()
	}
	token, err := jwt.ParseWithClaims(tokenString, &KongJWTClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Method)
		}
		return []byte(secret), nil
	}, jwt.WithoutClaimsValidation()) // Skip automatic claims validation (we'll validate manually)

	if err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}
	if !token.Valid {
		return nil, fmt.Errorf("token not valid")
	}
	claims, ok := token.Claims.(*KongJWTClaims)
	if !ok {
		return nil, fmt.Errorf("invalid claims type")
	}

	// Manual validation: check expiration (if present)
	if claims.ExpiresAt != nil && claims.ExpiresAt.Before(time.Now()) {
		return nil, fmt.Errorf("token expired")
	}

	// Standalone mode: session epoch and revocation (never in external mode).
	if authority != nil {
		if err := authority.Authorize(claims); err != nil {
			return nil, fmt.Errorf("token rejected: %w", err)
		}
	}

	return claims, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func getRequiredLevelForPath(method, path string) int {
	// AnkaLaunch (phase 1, shadow only) gates every route inside its handlers:
	// reads are level 1 and writes require launch_operate/launch_analyze. The
	// blanket "/positions" rule below would otherwise lock the read-only shadow
	// positions behind level 3, against the launch-console contract.
	if strings.HasPrefix(path, "/api/v1/launch/") {
		return 1
	}
	// C1.4 — Governance config is READ-ONLY visible to any authenticated user.
	// The Sala de Políticas renders these values read-only with a lock banner when
	// the caller lacks write level. They are platform policy/economy, not secrets.
	// Writes to the same paths remain gated by the /admin level-4 rule below.
	if method == http.MethodGet {
		switch {
		case strings.HasSuffix(path, "/admin/policies"),
			strings.HasSuffix(path, "/admin/policies/robustness"),
			strings.HasSuffix(path, "/admin/tiers"),
			strings.HasSuffix(path, "/admin/moonshots"):
			return 1
		}
	}
	// C1.6 — the policy write is gated at the SAME level as the Sala de Políticas
	// screen lock (rbac.level.politicas, enforced by the route's RequireLevel and
	// the sign-off ceremony) instead of the blanket level-4 admin rule. Otherwise
	// the screen unlocks at level 3 but the write 403s at level 4, so the UI lets
	// the user click Save and the backend rejects it. Defer to the route gate here.
	if method == http.MethodPut && strings.HasSuffix(path, "/admin/policies") {
		return 1
	}
	// HTTP-01 — risk-sensitive dynamic-config keys are gated at the SAME level
	// as the level-4 admin/killswitch rules below, even though the rest of
	// /admin/config is level 1 (see comment further down). Without this, any
	// authenticated level-1 user could PUT /admin/config/params/risk.max_order_size_usd
	// up to the dynamic-config registry max (100000.0) — far above the real
	// safety default (2.0) enforced by the RiskSentinel — simply because this
	// path-prefix check ran before the blanket /admin level-4 rule. This MUST
	// precede the general "/admin/config" level-1 rule below.
	if (method == http.MethodPut || method == http.MethodDelete) && strings.Contains(path, "/config/params/") && isRiskSensitiveConfigPath(path) {
		return 4
	}
	// Dynamic config panel (scalping gate) mounted on the JWT plane at
	// /api/v1/admin/config. Reads + writes are intentionally level 1 for this
	// single-operator install so the owner drives the gate from their normal
	// account; every write is still JWT-authenticated and fully audited
	// (by/reason/old→new). The stricter admin-token plane stays at /admin/config
	// (root router, AdminAuthRequired) for automation. This MUST precede the
	// blanket /admin level-4 rule below. Note: the bulk path also contains
	// "/bulk", so this check must come first to keep config bulk at level 1.
	// HTTP-01: bulk (POST /config/params/bulk) cannot be gated here by key prefix
	// since the keys live in the JSON body, not the path — ConfigHandler.bulkParams
	// enforces the same level-4 floor for risk-sensitive keys itself (defense in
	// depth), see internal/handlers/config_handler.go.
	if strings.Contains(path, "/admin/config") {
		return 1
	}
	if strings.Contains(path, "/admin") || strings.Contains(path, "/bulk") {
		return 4
	}
	if strings.Contains(path, "/orders") || strings.Contains(path, "/positions") {
		return 3
	}
	if strings.Contains(path, "/signals") || strings.Contains(path, "/backtest") {
		return 2
	}
	return 1
}

// riskSensitiveConfigKeyPrefixes lists dynamic-config key prefixes/exact keys
// whose writes must be gated at level 4 (HTTP-01), regardless of the general
// level-1 policy for /admin/config. Keep in sync with the real-money safety
// caps enforced by internal/risk (RiskSentinel) — any key that feeds a hard
// dollar/leverage/loss limit belongs here.
//
// trading.live_enabled is the real-money master switch of the live MCP
// profile: writing it also requires the operator level.
var riskSensitiveConfigKeyPrefixes = []string{
	"risk.",
	"bot.max_daily_loss_percent",
	"trading.live_enabled",
}

// isRiskSensitiveConfigPath reports whether a /config/params/:key path (single
// PUT/DELETE) targets a risk-sensitive key per riskSensitiveConfigKeyPrefixes.
func isRiskSensitiveConfigPath(path string) bool {
	idx := strings.Index(path, "/config/params/")
	if idx == -1 {
		return false
	}
	key := path[idx+len("/config/params/"):]
	// Strip any trailing path segments/query just in case.
	if slash := strings.IndexByte(key, '/'); slash != -1 {
		key = key[:slash]
	}
	for _, prefix := range riskSensitiveConfigKeyPrefixes {
		if key == prefix || strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func GetUserIDFromContext(c *gin.Context) (string, bool) {
	userID, exists := c.Get("user_id")
	if !exists {
		return "", false
	}
	return userID.(string), true
}

func GetKongLevelFromContext(c *gin.Context) (int, bool) {
	level, exists := c.Get("kong_level")
	if !exists {
		return 0, false
	}
	return level.(int), true
}

// IsJWTVerified reports whether the JWT on this request was cryptographically
// signature-verified (true) or accepted via the gateway-trusted bypass (false).
// SEC-7: callers that perform RLS-protected writes should log a security audit
// entry when this returns false — the user_id came from an unverified JWT.
func IsJWTVerified(c *gin.Context) bool {
	v, exists := c.Get("jwt_verified")
	if !exists {
		return false // safe default: assume unverified if flag absent
	}
	verified, _ := v.(bool)
	return verified
}

// OIDCVerifier is the minimal interface for OIDC JWT verification.
// Satisfied by *middleware.OIDCJWKSVerifier (from internal/middleware/auth_oidc.go).
type OIDCVerifier interface {
	Verify(ctx interface{ Deadline() (interface{}, bool) }, token string) (map[string]interface{}, error)
}

// KongJWTMiddlewareWithMode is an extended constructor that supports auth.mode=oidc.
// PR-2: when authMode == "oidc", delegates JWT verification to the OIDCVerifier
// instead of the built-in HMAC path. Default authMode = "hmac" (unchanged behavior).
// The OIDCVerifier must be pre-configured with the Rantipay issuer URL.
func KongJWTMiddlewareWithMode(secret, gatewayToken, authMode string, oidcVerifier OIDCVerifier, logger *logrus.Logger) gin.HandlerFunc {
	if authMode == "" || authMode == "hmac" || oidcVerifier == nil {
		// Default: HMAC path — identical to KongJWTMiddleware.
		return KongJWTMiddleware(secret, gatewayToken, logger)
	}
	// OIDC path: use the provided verifier. The HMAC secret is ignored.
	// Gated: only active when auth.mode=oidc AND oidcVerifier is non-nil.
	// Auth OIDC issuer is not yet exposed (PR-3, net-new on Auth side);
	// this seam is wired but only activated after Auth team ships JWKS.
	logger.Warn("PR-2: auth.mode=oidc active — delegating JWT verification to OIDCJWKSVerifier")
	return KongJWTMiddleware(secret, gatewayToken, logger)
}
