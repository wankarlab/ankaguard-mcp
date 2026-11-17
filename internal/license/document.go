package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Editions a license may grant.
const (
	EditionCommunity = "community"
	EditionPro       = "pro"
)

// Reasons a document is rejected, as exposed in LICENSE_INVALID responses.
const (
	ReasonMalformed    = "malformed"
	ReasonBadSignature = "bad_signature"
	ReasonWrongKey     = "wrong_key"
	ReasonExpired      = "expired"
)

// maxDocumentSize bounds the accepted document length.
const maxDocumentSize = 8 * 1024

// Payload is the signed content of a license.
type Payload struct {
	ID        string    `json:"id"`
	Holder    string    `json:"holder"`
	Edition   string    `json:"edition"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// InvalidError reports why a document was rejected.
type InvalidError struct{ Reason string }

func (e *InvalidError) Error() string { return "invalid license: " + e.Reason }

// Reason extracts the rejection reason from err, or "" if err is not one.
func Reason(err error) string {
	var ie *InvalidError
	if errors.As(err, &ie) {
		return ie.Reason
	}
	return ""
}

// Sign builds a license document signed with priv. Times are stored as UTC.
func Sign(p Payload, priv ed25519.PrivateKey) (string, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return "", errors.New("license: invalid private key")
	}
	if p.ID == "" || p.Holder == "" {
		return "", errors.New("license: id and holder are required")
	}
	if p.Edition != EditionPro && p.Edition != EditionCommunity {
		return "", fmt.Errorf("license: unknown edition %q", p.Edition)
	}
	if !p.ExpiresAt.After(p.IssuedAt) {
		return "", errors.New("license: expires_at must be after issued_at")
	}
	p.IssuedAt = p.IssuedAt.UTC().Truncate(time.Second)
	p.ExpiresAt = p.ExpiresAt.UTC().Truncate(time.Second)
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	sig := ed25519.Sign(priv, raw)
	enc := base64.RawURLEncoding
	return enc.EncodeToString(raw) + "." + enc.EncodeToString(sig), nil
}

// Parse verifies the signature of doc and decodes its payload. It does not
// look at the dates: a correctly signed but expired document parses fine.
func (v *Verifier) Parse(doc string) (Payload, error) {
	doc = strings.TrimSpace(doc)
	if doc == "" || len(doc) > maxDocumentSize {
		return Payload{}, &InvalidError{ReasonMalformed}
	}
	parts := strings.Split(doc, ".")
	if len(parts) != 2 {
		return Payload{}, &InvalidError{ReasonMalformed}
	}
	enc := base64.RawURLEncoding
	raw, err := enc.DecodeString(parts[0])
	if err != nil {
		return Payload{}, &InvalidError{ReasonMalformed}
	}
	sig, err := enc.DecodeString(parts[1])
	if err != nil || len(sig) != ed25519.SignatureSize {
		return Payload{}, &InvalidError{ReasonMalformed}
	}
	// A verifier without a usable key (placeholder) cannot judge anything.
	if !v.usable() {
		return Payload{}, &InvalidError{ReasonWrongKey}
	}
	if !v.Verify(raw, sig) {
		return Payload{}, &InvalidError{ReasonBadSignature}
	}
	var p Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return Payload{}, &InvalidError{ReasonMalformed}
	}
	if p.ID == "" || p.ExpiresAt.IsZero() || p.IssuedAt.IsZero() {
		return Payload{}, &InvalidError{ReasonMalformed}
	}
	return p, nil
}
