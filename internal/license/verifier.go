// Package license implements the product license: a signed document that is
// verified offline with an embedded Ed25519 public key.
//
// Document format (D7): base64url(payload JSON) + "." + base64url(signature),
// where the signature is Ed25519 over the raw payload JSON bytes. The package
// never touches the network; HTTP handlers live in internal/license/httpapi.
package license

import (
	"crypto/ed25519"
	_ "embed" // go:embed for the public key
	"encoding/base64"
	"strings"
)

// embeddedPublicKeyFile holds the base64 (standard) encoding of the 32-byte
// Ed25519 public key. It is the release signing key (the private half lives
// outside the repository); an all-zero placeholder is rejected by the release
// check (see docs/product/licensing.md).
//
//go:embed pubkey.ed25519
var embeddedPublicKeyFile string

// Verifier validates license signatures with an Ed25519 public key.
type Verifier struct {
	publicKey ed25519.PublicKey
}

// NewVerifier returns a Verifier using the compiled-in public key. If the
// embedded key is unreadable the verifier rejects every license.
func NewVerifier() *Verifier {
	return &Verifier{publicKey: EmbeddedPublicKey()}
}

// NewVerifierWithKey returns a Verifier using an explicit public key. Tests
// use it with a pair generated in the test itself.
func NewVerifierWithKey(pubKey ed25519.PublicKey) *Verifier {
	return &Verifier{publicKey: pubKey}
}

// EmbeddedPublicKey decodes the key compiled into the binary. It returns nil
// when the embedded file is not a valid base64 32-byte key.
func EmbeddedPublicKey() ed25519.PublicKey {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(embeddedPublicKeyFile))
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil
	}
	return ed25519.PublicKey(raw)
}

// IsPlaceholderKey reports whether key is unusable or the all-zero
// placeholder shipped in the source repository.
func IsPlaceholderKey(key ed25519.PublicKey) bool {
	if len(key) != ed25519.PublicKeySize {
		return true
	}
	for _, b := range key {
		if b != 0 {
			return false
		}
	}
	return true
}

// usable reports whether the verifier holds a real-looking key.
func (v *Verifier) usable() bool {
	return !IsPlaceholderKey(v.publicKey)
}

// Verify returns true if sig is a valid signature of payload.
func (v *Verifier) Verify(payload, sig []byte) bool {
	if len(v.publicKey) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(v.publicKey, payload, sig)
}
