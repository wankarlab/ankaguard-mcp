package middleware

import (
	"errors"
	"sync/atomic"
)

// LocalAuthority takes over the JWT validation of every layer that uses this
// package (the raw :10600 mux, the gin middleware of the REST router and the
// gateway on :9090) when the server runs with auth.mode=standalone.
//
// It changes two things and nothing else:
//   - the signing secret is read from the authority on EVERY request, so a
//     rotation by the operator applies without a restart (the secret passed
//     when the middleware was built is ignored);
//   - after the signature and expiry checks, Authorize applies the stateful
//     checks (operator session epoch, agent token revocation).
//
// With no authority installed (auth.mode=external, the default) the code paths
// are exactly the ones that existed before.
type LocalAuthority interface {
	// Secret returns the current HS256 signing secret.
	Secret() string
	// Authorize applies the session epoch and revocation checks to claims whose
	// signature was already verified. A non-nil error rejects the request.
	Authorize(claims *KongJWTClaims) error
}

type authorityHolder struct{ a LocalAuthority }

var localAuthority atomic.Pointer[authorityHolder]

// SetLocalAuthority installs (or, with nil, removes) the local authority.
func SetLocalAuthority(a LocalAuthority) {
	if a == nil {
		localAuthority.Store(nil)
		return
	}
	localAuthority.Store(&authorityHolder{a: a})
}

func currentAuthority() LocalAuthority {
	if h := localAuthority.Load(); h != nil {
		return h.a
	}
	return nil
}

// EffectiveSecret returns the signing secret to use: the local authority's
// current secret in standalone mode, otherwise fallback.
func EffectiveSecret(fallback string) string {
	if a := currentAuthority(); a != nil {
		return a.Secret()
	}
	return fallback
}

// ValidateToken validates a raw bearer token with the same rules as the
// middleware (signature, expiry and, in standalone mode, the local stateful
// checks). It is for validators that are not HTTP middleware, such as the
// WebSocket handlers.
func ValidateToken(token, fallbackSecret string) (*KongJWTClaims, error) {
	if token == "" {
		return nil, errors.New("missing token")
	}
	return validateJWT(token, fallbackSecret)
}

// HasLocalAuthority reports whether a local authority (standalone mode) is
// installed. Layers that only enable JWT checks when a static secret is
// configured use it to also enable them in standalone mode.
func HasLocalAuthority() bool {
	return currentAuthority() != nil
}
