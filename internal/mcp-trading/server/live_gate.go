package server

import (
	"context"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/toolpolicy"
	"github.com/rantipay/trading-mcp/internal/redact"
)

const (
	// ErrCodeLiveTradingDisabled is the JSON-RPC code for LIVE_TRADING_DISABLED.
	ErrCodeLiveTradingDisabled = -32007
	// LiveTradingDisabledCode is the data.code of a call rejected because
	// trading.live_enabled is false.
	LiveTradingDisabledCode = "LIVE_TRADING_DISABLED"

	// ErrCodeLicenseRequired is the JSON-RPC code for LICENSE_REQUIRED.
	ErrCodeLicenseRequired = -32006
	// LicenseRequiredCode is the data.code of a live call rejected because the
	// license does not allow real-money trading.
	LicenseRequiredCode = "LICENSE_REQUIRED"
)

// SetLicenseGate wires the license check of the product edition. The function
// is evaluated on every call so an installed, expired or renewed license
// applies without a restart. When it is never set (standard build) the gate
// is open, exactly like before licensing existed.
func (s *MCPServer) SetLicenseGate(allowsLive func() bool) {
	s.liveMu.Lock()
	s.licenseAllowsLive = allowsLive
	s.liveMu.Unlock()
}

// checkLicense is the license step of the dispatch order (D8): with a live
// profile, trade_increase and control calls need a Pro license in state valid
// or grace. Reductions, closes, safety tools, reads and the paper profile are
// never blocked, so an expired license can always unwind exposure.
func (s *MCPServer) checkLicense(ctx context.Context, tool string, class toolpolicy.Class) *models.MCPError {
	if class != toolpolicy.ClassTradeIncrease && class != toolpolicy.ClassControl {
		return nil
	}
	s.liveMu.RLock()
	gate := s.licenseAllowsLive
	s.liveMu.RUnlock()
	if gate == nil {
		return nil
	}
	profile, tokenID := s.callerFromContext(ctx)
	if profile != toolpolicy.ProfileLive || gate() {
		return nil
	}
	s.auditPermissionDenied(tool, class, profile, tokenID, "license does not allow live trading")
	return &models.MCPError{
		Code:    ErrCodeLicenseRequired,
		Message: LicenseRequiredCode,
		Data: map[string]interface{}{
			"code":    LicenseRequiredCode,
			"tool":    tool,
			"class":   string(class),
			"profile": string(profile),
			"reason":  "a valid Pro license is required for real-money calls that increase risk or change configuration; reducing and closing positions stays available",
		},
	}
}

// SetLiveEnabledFunc wires trading.live_enabled. The function is evaluated on
// every call, so a change applies without a restart. When it is never set the
// gate is open, exactly like before the parameter existed.
func (s *MCPServer) SetLiveEnabledFunc(f func() bool) {
	s.liveMu.Lock()
	s.liveEnabled = f
	s.liveMu.Unlock()
}

// LiveEnabled reports the current value of trading.live_enabled.
func (s *MCPServer) LiveEnabled() bool {
	s.liveMu.RLock()
	f := s.liveEnabled
	s.liveMu.RUnlock()
	return f == nil || f()
}

// checkLiveEnabled is the "real money enabled" step of the dispatch order. It
// applies to tools/call only, to trade_increase and control calls with a live
// profile. Reductions, closes, safety tools, reads and the whole paper profile
// are never blocked, so an operator can always unwind exposure.
//
// Decision (task 6.12): this gate does NOT stop the internal AI bot, scalping
// engine or XSEC: those do not go through /mcp and are governed by the global
// stop and their own limits. Making them honour trading.live_enabled is a
// separate decision for the operator.
func (s *MCPServer) checkLiveEnabled(ctx context.Context, tool string, class toolpolicy.Class) *models.MCPError {
	if class != toolpolicy.ClassTradeIncrease && class != toolpolicy.ClassControl {
		return nil
	}
	profile, tokenID := s.callerFromContext(ctx)
	if profile != toolpolicy.ProfileLive || s.LiveEnabled() {
		return nil
	}
	s.auditPermissionDenied(tool, class, profile, tokenID, "trading.live_enabled is false")
	return &models.MCPError{
		Code:    ErrCodeLiveTradingDisabled,
		Message: LiveTradingDisabledCode,
		Data: map[string]interface{}{
			"code":    LiveTradingDisabledCode,
			"tool":    tool,
			"class":   string(class),
			"profile": string(profile),
			"reason":  "real-money trading is not enabled (trading.live_enabled=false); use a paper token or enable it explicitly",
		},
	}
}

// redactParams masks secrets in tool arguments before they are logged.
func redactParams(args map[string]interface{}) map[string]interface{} {
	return redact.Map(args)
}
