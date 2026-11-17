// Package routepolicy is the single table of the HTTP routes that are NOT
// protected by the JWT middleware (OpenSpec guarded-mcp-product, task 6.16).
//
// Every route mounted on the MCP mux (:10600), the REST engine (:8081) and the
// gateway (:9090) requires a valid token UNLESS it is listed here. The table is
// checked by tests that send an unauthenticated request to every mounted
// route: a new route that answers without a token and is not in the table makes
// the test fail, so an unprotected route can never appear unnoticed.
//
// The human-readable version lives in docs/product/routes.md.
package routepolicy

// Class says why a route does not require a token.
type Class string

const (
	// Public routes expose nothing sensitive or must be reachable to log in.
	Public Class = "public"
	// Self routes authenticate inside the handler (WebSocket token in the query,
	// operator session checks, webhook signatures).
	Self Class = "self"
	// Pending routes are unauthenticated today and are known gaps, kept so this
	// change does not alter behaviour. They are listed so they cannot grow
	// silently; closing them is a product decision (see docs/product/routes.md).
	Pending Class = "pending"
)

// Rule is one route that does not require a token.
type Rule struct {
	// Listener is "mcp" (:10600), "rest" (:8081) or "gateway" (:9090).
	Listener string
	// Method is the HTTP method, or "*" for a mux pattern that accepts any.
	Method string
	// Path is the pattern exactly as mounted (gin :params and *wildcards included).
	Path   string
	Class  Class
	Reason string
}

// Open is the allow-list. Anything not listed requires authentication.
var Open = []Rule{
	// ── MCP mux :10600 ───────────────────────────────────────────────────────
	{"mcp", "*", "/health", Public, "liveness: only status, version, commit and edition"},
	{"mcp", "*", "/ws", Self, "validates the token (query parameter or Authorization header) before the upgrade"},

	// ── Gateway :9090 ────────────────────────────────────────────────────────
	{"gateway", "GET", "/health", Public, "liveness"},
	{"gateway", "GET", "/api/health", Public, "liveness"},
	{"gateway", "GET", "/ready", Public, "readiness for the load balancer (check status only)"},
	{"gateway", "GET", "/api/ready", Public, "readiness for the load balancer (check status only)"},
	{"gateway", "GET", "/api/v1/health", Public, "proxy of the REST liveness"},
	{"gateway", "GET", "/ws", Self, "validates the token before the upgrade and forwards it to the backend"},

	// ── REST engine :8081, mounted by apiv1.Router ───────────────────────────
	{"rest", "GET", "/api/v1/health", Public, "liveness"},
	{"rest", "GET", "/api/v1/stream", Self, "validates the token inside the WebSocket handler"},
	{"rest", "GET", "/api/v1/engine/stats", Public, "read-only console statistics, no credentials or PII"},
	{"rest", "GET", "/api/v1/flywheel/events", Public, "read-only console statistics"},
	{"rest", "GET", "/api/v1/capital/ladder", Public, "read-only console statistics"},
	{"rest", "GET", "/api/v1/xsec/status", Self, "optional token: anonymous callers get paper-only data"},
	{"rest", "GET", "/api/v1/xsec/funding", Self, "optional token: anonymous callers get paper-only data"},
	{"rest", "GET", "/api/v1/xsec/shadow", Public, "read-only statistics"},
	{"rest", "GET", "/api/v1/xsec/suggest-params", Public, "read-only statistics"},
	{"rest", "GET", "/api/v1/tournament", Public, "read-only console data"},
	{"rest", "GET", "/api/v1/tournament/:id", Public, "read-only console data"},
	{"rest", "GET", "/api/v1/tournament/:id/leaderboard", Public, "read-only console data"},
	{"rest", "GET", "/api/v1/marketplace/strategies", Public, "read-only catalogue"},
	{"rest", "GET", "/api/v1/academy/cohorts/current", Public, "read-only catalogue"},
	{"rest", "GET", "/api/v1/exchanges", Public, "read-only exchange list"},
	{"rest", "GET", "/api/v1/specs", Public, "read-only spec catalogue"},
	{"rest", "GET", "/api/v1/specs/templates", Public, "read-only spec templates"},
	{"rest", "POST", "/api/v1/specs/:id/validate", Public, "public validation of a spec (no account access)"},
	{"rest", "GET", "/api/v1/specs/:id/validate/stream", Public, "public validation stream"},
	{"rest", "GET", "/api/v1/consent/template/:kind", Public, "legal text"},
	{"rest", "POST", "/api/v1/auth/login/email", Public, "login proxy to the auth service"},
	{"rest", "POST", "/api/v1/auth/login/phone", Public, "login proxy to the auth service"},
	{"rest", "POST", "/api/v1/auth/login/phone/verify", Public, "login proxy to the auth service"},
	{"rest", "POST", "/api/v1/auth/register/phone", Public, "login proxy to the auth service"},
	{"rest", "POST", "/api/v1/auth/register/phone/verify", Public, "login proxy to the auth service"},
	{"rest", "POST", "/api/v1/auth/login/social", Public, "login proxy to the auth service"},
	{"rest", "POST", "/api/v1/auth/session", Public, "login proxy to the auth service"},
	{"rest", "POST", "/api/v1/auth/session/:id/answer", Public, "login proxy to the auth service"},
	{"rest", "POST", "/api/v1/auth/session/:id/resend", Public, "login proxy to the auth service"},
	{"rest", "POST", "/api/v1/auth/refresh", Public, "login proxy to the auth service"},
	{"rest", "GET", "/api/v1/auth/oauth/:provider/authorize", Public, "login proxy to the auth service"},
	{"rest", "POST", "/api/v1/auth/oauth/:provider/native", Public, "login proxy to the auth service"},
	{"rest", "GET", "/api/v1/auth/providers", Public, "login proxy to the auth service"},
	{"rest", "POST", "/api/v1/webhooks/odoo", Self, "verifies the webhook signature"},
	{"rest", "POST", "/api/v1/webhooks/odoo/subscription", Self, "verifies the webhook signature"},
	{"rest", "GET", "/api/v1/webhooks/status", Public, "webhook processor status"},
	{"rest", "GET", "/api/v1/webhooks/health", Public, "webhook processor liveness"},

	// Local authentication (standalone mode; in external mode they answer 501).
	{"rest", "GET", "/api/v1/auth/local/mode", Public, "tells the console which auth mode is active"},
	{"rest", "POST", "/api/v1/auth/local/login", Public, "operator login (rate limited)"},
	{"rest", "GET", "/api/v1/auth/local/tokens", Self, "checks the operator session inside the handler"},
	{"rest", "POST", "/api/v1/auth/local/tokens", Self, "checks the operator session inside the handler"},
	{"rest", "DELETE", "/api/v1/auth/local/tokens/:id", Self, "checks the operator session inside the handler"},

	// ── REST engine :8081, registered directly by main.go WITHOUT middleware ─
	// Known gap of the STANDARD build only, kept as is so this deployment does
	// not change. They include mutating routes. The product edition does not
	// mount the legacy handlers at all and mounts the fee ledger and the
	// strategy validation behind the JWT middleware (cmd/trading-mcp-server
	// legacy_routes.go). See docs/product/routes.md, section "Pendientes".
	{"rest", "GET", "/api/v1/fees/current", Pending, "standard build only; product: behind the JWT"},
	{"rest", "GET", "/api/v1/fees/history", Pending, "standard build only; product: behind the JWT"},
	{"rest", "POST", "/api/v1/fees/accrue", Pending, "standard build only; product: behind the JWT; records a fee"},
	{"rest", "GET", "/api/v1/strategy-validation", Pending, "standard build only; product: behind the JWT"},
	{"rest", "POST", "/api/v1/strategy-validation/run", Pending, "standard build only; product: behind the JWT; starts a validation run"},
	{"rest", "POST", "/api/v1/analysis/market", Pending, "legacy handler without auth (standard build only)"},
	{"rest", "POST", "/api/v1/analysis/setup", Pending, "legacy handler without auth (standard build only)"},
	{"rest", "POST", "/api/v1/analysis/validate", Pending, "legacy handler without auth (standard build only)"},
	{"rest", "GET", "/api/v1/monitoring/metrics", Pending, "legacy handler without auth (standard build only)"},
	{"rest", "GET", "/api/v1/monitoring/decisions", Pending, "legacy handler without auth (standard build only)"},
	{"rest", "GET", "/api/v1/monitoring/report/:period", Pending, "legacy handler without auth (standard build only)"},
	{"rest", "GET", "/api/v1/knowledge/status", Pending, "legacy handler without auth (standard build only)"},
	{"rest", "GET", "/api/v1/knowledge/context/:type", Pending, "legacy handler without auth (standard build only)"},
	{"rest", "POST", "/api/v1/knowledge/reload", Pending, "legacy handler without auth (standard build only); reloads knowledge files"},
	{"rest", "GET", "/api/v1/validation/rules", Pending, "legacy handler without auth (standard build only)"},
	{"rest", "GET", "/api/v1/validation/circuit-breakers", Pending, "legacy handler without auth (standard build only)"},
	{"rest", "POST", "/api/v1/validation/test", Pending, "legacy handler without auth (standard build only)"},
	{"rest", "GET", "/api/v1/strategies", Pending, "legacy handler without auth (standard build only)"},
	{"rest", "POST", "/api/v1/strategies", Pending, "legacy handler without auth (standard build only); creates a strategy"},
	{"rest", "GET", "/api/v1/strategies/type/:type", Pending, "legacy handler without auth (standard build only)"},
	{"rest", "GET", "/api/v1/strategies/:name", Pending, "legacy handler without auth (standard build only)"},
	{"rest", "PUT", "/api/v1/strategies/:name", Pending, "legacy handler without auth (standard build only); edits a strategy"},
	{"rest", "DELETE", "/api/v1/strategies/:name", Pending, "legacy handler without auth (standard build only); deletes a strategy"},
	{"rest", "GET", "/api/v1/metrics/type/:type", Pending, "legacy handler without auth (standard build only)"},
	{"rest", "POST", "/api/v1/metrics/calculate/:style", Pending, "legacy handler without auth (standard build only)"},
	{"rest", "GET", "/api/v1/metrics/:style/:timeframe", Pending, "legacy handler without auth (standard build only)"},
	{"rest", "GET", "/api/v1/metrics/:style", Pending, "legacy handler without auth (standard build only)"},
	{"rest", "GET", "/api/v1/trades", Pending, "legacy handler without auth (standard build only)"},
	{"rest", "GET", "/api/v1/trades/open", Pending, "legacy handler without auth (standard build only)"},
	{"rest", "GET", "/api/v1/trades/closed", Pending, "legacy handler without auth (standard build only)"},
	{"rest", "GET", "/api/v1/trades/style/:style", Pending, "legacy handler without auth (standard build only)"},
	{"rest", "GET", "/api/v1/trades/timeframe/:timeframe", Pending, "legacy handler without auth (standard build only)"},
	{"rest", "GET", "/api/v1/trades/:id", Pending, "legacy handler without auth (standard build only)"},
	{"rest", "GET", "/api/v1/storage/stats", Pending, "legacy handler without auth (standard build only)"},
	{"rest", "GET", "/api/v1/storage/cache/stats", Pending, "legacy handler without auth (standard build only)"},
	{"rest", "POST", "/api/v1/storage/cache/clean", Pending, "legacy handler without auth (standard build only); cleans the cache"},
}

// Find returns the rule for a route, if it is in the allow-list.
func Find(listener, method, path string) (Rule, bool) {
	for _, r := range Open {
		if r.Listener == listener && r.Path == path && (r.Method == "*" || r.Method == method) {
			return r, true
		}
	}
	return Rule{}, false
}
