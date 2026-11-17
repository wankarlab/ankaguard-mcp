package localauth

import (
	tradingmw "github.com/rantipay/trading-mcp/internal/trading/middleware"
)

// Authority adapts the service to the JWT validators of
// internal/trading/middleware (mux :10600, gin middleware, gateway :9090,
// WebSocket handlers). Install it once with
// tradingmw.SetLocalAuthority(svc.Authority()) when the mode is standalone:
// from then on every layer reads Secret() per request (so a rotation applies
// without a restart) and calls CheckClaims after the signature check.
func (s *Service) Authority() tradingmw.LocalAuthority { return authority{s: s} }

type authority struct{ s *Service }

func (a authority) Secret() string { return a.s.Secret() }

func (a authority) Authorize(c *tradingmw.KongJWTClaims) error {
	sub := c.UserID
	if sub == "" {
		sub = c.Subject
	}
	_, err := a.s.CheckClaims(&Claims{
		UserID:           sub,
		UserLevel:        c.UserLevel,
		TenantID:         c.TenantID,
		CompanyID:        c.CompanyID,
		MCPProfile:       c.MCPProfile,
		TokenUse:         c.TokenUse,
		SessionEpoch:     c.SessionEpoch,
		RegisteredClaims: c.RegisteredClaims,
	})
	return err
}
