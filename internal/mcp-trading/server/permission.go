package server

import (
	"context"
	"sort"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/toolpolicy"
	"github.com/sirupsen/logrus"
)

// Application error codes returned in the JSON-RPC error (the symbolic name is
// in error.data.code).
const (
	// ErrCodePermissionDenied is the JSON-RPC code for PERMISSION_DENIED.
	ErrCodePermissionDenied = -32003
	// ErrCodePaperUnavailable is the JSON-RPC code for PAPER_UNAVAILABLE.
	ErrCodePaperUnavailable = -32004

	// PermissionDeniedCode is the data.code of a profile/class rejection.
	PermissionDeniedCode = "PERMISSION_DENIED"
	// PaperUnavailableCode is the data.code returned for paper-profile trade
	// calls while no paper broker is wired in.
	PaperUnavailableCode = "PAPER_UNAVAILABLE"
)

const (
	stdioTokenID   = "stdio"
	noAuthTokenID  = "auth-disabled"
	unauthTokenID  = "unauthenticated"
	unclassifiedID = "unclassified"
)

// withStdioIdentity marks requests that arrive over the local stdio transport.
// Stdio is a same-user local pipe with no token, so it is treated as an
// operator session (live), exactly like before profiles existed.
func withStdioIdentity(ctx context.Context) context.Context {
	ctx = context.WithValue(ctx, ProfileContextKey, toolpolicy.ProfileLive)
	return context.WithValue(ctx, TokenIDContextKey, stdioTokenID)
}

// callerFromContext resolves the permission profile and audit identifier of the
// caller.
//
//   - requireJWTAuth already resolved both from the token: use them.
//   - JWT auth is disabled (ENABLE_JWT_AUTH=false, development only): there is
//     no identity at all, so behave as before profiles existed (live).
//   - JWT auth is enabled but the context carries no identity (the request did
//     not go through requireJWTAuth): fail closed as read.
func (s *MCPServer) callerFromContext(ctx context.Context) (toolpolicy.Profile, string) {
	tokenID, _ := TokenIDFromContext(ctx)
	if p, ok := ProfileFromContext(ctx); ok {
		if tokenID == "" {
			tokenID = unauthTokenID
		}
		return p, tokenID
	}
	if !s.jwtAuthEnabled {
		return toolpolicy.ProfileLive, noAuthTokenID
	}
	return toolpolicy.ProfileRead, unauthTokenID
}

// paperBrokerReady reports whether paper-profile trade calls can be served by
// the simulated broker: true once main.go has built it and wired it with
// SetPaperBroker. Until then every trade_increase / trade_reduce call from a
// paper token is rejected with PAPER_UNAVAILABLE BEFORE the tool runs, so a
// paper token can never reach the real exchange. The tools reach the broker
// through gctclient.Client, which picks it by the paper holder that
// paperEnvelope puts in the request context.
func (s *MCPServer) paperBrokerReady() bool {
	return s.paperBrokerInstance() != nil
}

// toolListedFor reports whether a tool appears in tools/list for the profile:
// it must be classified and its class must be allowed for the profile.
func toolListedFor(name string, profile toolpolicy.Profile) bool {
	class, ok := toolpolicy.ClassOf(name)
	if !ok {
		return false
	}
	return toolpolicy.Permits(profile, class)
}

// permissionError builds the PERMISSION_DENIED JSON-RPC error. It carries only
// the tool, class and profile, never market or account data.
func permissionError(tool string, class toolpolicy.Class, profile toolpolicy.Profile, reason string) *models.MCPError {
	classStr := string(class)
	if classStr == "" {
		classStr = unclassifiedID
	}
	return &models.MCPError{
		Code:    ErrCodePermissionDenied,
		Message: PermissionDeniedCode,
		Data: map[string]interface{}{
			"code":    PermissionDeniedCode,
			"tool":    tool,
			"class":   classStr,
			"profile": string(profile),
			"reason":  reason,
		},
	}
}

// paperUnavailableError builds the PAPER_UNAVAILABLE JSON-RPC error.
func paperUnavailableError(tool string, class toolpolicy.Class) *models.MCPError {
	return &models.MCPError{
		Code:    ErrCodePaperUnavailable,
		Message: PaperUnavailableCode,
		Data: map[string]interface{}{
			"code":    PaperUnavailableCode,
			"tool":    tool,
			"class":   string(class),
			"profile": string(toolpolicy.ProfilePaper),
			"reason":  "paper broker is not available yet; the call was not executed",
		},
	}
}

// checkToolPermission applies steps "class" and "permission" of the dispatch
// order (auth -> class -> permission -> kill switch -> execution). It returns a
// non-nil error when the call must be rejected, and the class evaluated for
// the call so the next steps do not classify twice.
func (s *MCPServer) checkToolPermission(ctx context.Context, tool string, args map[string]interface{}) (toolpolicy.Class, *models.MCPError) {
	profile, tokenID := s.callerFromContext(ctx)

	class, ok := toolpolicy.ClassForCall(tool, args)
	if !ok {
		s.auditPermissionDenied(tool, "", profile, tokenID, "tool has no classification")
		return "", permissionError(tool, "", profile, "tool has no classification")
	}

	switch toolpolicy.Decide(profile, class) {
	case toolpolicy.Deny:
		reason := "profile " + string(profile) + " cannot call class " + string(class)
		s.auditPermissionDenied(tool, class, profile, tokenID, reason)
		return class, permissionError(tool, class, profile, reason)
	case toolpolicy.AllowPaper:
		if !s.paperBrokerReady() {
			s.auditPermissionDenied(tool, class, profile, tokenID, "paper broker unavailable")
			return class, paperUnavailableError(tool, class)
		}
	}
	return class, nil
}

// auditPermissionDenied records a rejected call with the token identifier.
func (s *MCPServer) auditPermissionDenied(tool string, class toolpolicy.Class, profile toolpolicy.Profile, tokenID, reason string) {
	s.log.WithFields(logrus.Fields{
		"tool":     tool,
		"class":    string(class),
		"profile":  string(profile),
		"token_id": tokenID,
		"reason":   reason,
	}).Warn("MCP tool call rejected by permission policy")
	if s.auditLogger != nil {
		s.auditLogger.LogPermissionDenied(tool, string(class), string(profile), tokenID, reason)
	}
}

// UnclassifiedTools returns the names of registered tools that have no entry in
// the classification table, sorted. Such tools are neither listed nor
// executable.
func (s *MCPServer) UnclassifiedTools() []string {
	var missing []string
	for _, info := range s.registry.List() {
		if _, ok := toolpolicy.ClassOf(info.Name); !ok {
			missing = append(missing, info.Name)
		}
	}
	sort.Strings(missing)
	return missing
}

// AuditToolClassification logs one error per registered tool that has no
// class. Call it once after every tool has been registered.
func (s *MCPServer) AuditToolClassification() []string {
	missing := s.UnclassifiedTools()
	for _, name := range missing {
		s.log.WithField("tool", name).Error("MCP tool has no permission class: it will not be listed and every call to it will be rejected")
	}
	return missing
}
