package middleware

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// TokenUseAgent is the token_use claim of the agent tokens issued by
// internal/localauth (kept here as a literal to avoid an import cycle; the
// localauth tests assert both constants stay equal).
const TokenUseAgent = "agent"

// OperatorRequiredCode is the error code answered when an agent token is
// presented on a surface reserved to the operator.
const OperatorRequiredCode = "OPERATOR_REQUIRED"

// ErrOperatorRequired is returned by AuthenticateHTTPRequest when an agent
// token is presented outside the JSON-RPC (MCP) endpoints in standalone mode.
var ErrOperatorRequired = errors.New("agent tokens are only accepted on the MCP endpoint; an operator session is required")

// agentAllowedPaths are the only HTTP paths where an agent token is accepted
// in standalone mode: the JSON-RPC endpoints of the MCP listener (:10600) and
// their gateway aliases (:9090, "/api/mcp" is rewritten to "/"). The read-only
// WebSocket stream validates its token separately (ValidateToken) and stays
// open to agents.
var agentAllowedPaths = map[string]bool{
	"/":         true,
	"/mcp":      true,
	"/jsonrpc":  true,
	"/api/mcp":  true,
	"/api/mcp/": true,
}

// AgentPathAllowed reports whether an agent token may be used on path.
func AgentPathAllowed(path string) bool { return agentAllowedPaths[path] }

// agentScopeViolation reports whether claims belong to an agent token used
// outside the MCP endpoints. It only applies with a local authority installed
// (auth.mode=standalone): in external mode tokens never carry token_use and
// behaviour is unchanged.
func agentScopeViolation(claims *KongJWTClaims, path string) bool {
	if claims == nil || claims.TokenUse != TokenUseAgent || currentAuthority() == nil {
		return false
	}
	return !AgentPathAllowed(path)
}

// abortOperatorRequired answers 403 OPERATOR_REQUIRED on a gin context.
func abortOperatorRequired(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
		"error": ErrOperatorRequired.Error(),
		"code":  OperatorRequiredCode,
	})
}
