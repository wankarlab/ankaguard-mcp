// Package redact removes secrets from values before they are logged or
// audited (openspec guarded-mcp-product, task 6.15). Passwords, tokens,
// exchange API keys and secrets, passphrases and licence documents must never
// reach a log line, an error response or the audit trail.
package redact

import (
	"regexp"
	"strings"
)

// Placeholder replaces every redacted value.
const Placeholder = "[REDACTED]"

// sensitiveFragments are matched against the normalized key (lower case, with
// '_', '-' and spaces removed): a key containing any of them is redacted.
var sensitiveFragments = []string{
	"apikey", "apisecret", "secret", "passphrase", "password", "passwd",
	"token", "license", "licence", "authorization", "privatekey", "credential",
	"mnemonic", "seedphrase", "signature",
}

// jwtPattern matches a compact JWT anywhere in a string value.
var jwtPattern = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]*`)

// bearerPattern matches "Bearer <token>".
var bearerPattern = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]{8,}`)

// IsSensitiveKey reports whether a field name denotes a secret.
func IsSensitiveKey(key string) bool {
	k := strings.ToLower(key)
	k = strings.NewReplacer("_", "", "-", "", " ", "", ".", "").Replace(k)
	for _, f := range sensitiveFragments {
		if strings.Contains(k, f) {
			return true
		}
	}
	return false
}

// String redacts secrets embedded in free text (JWTs and bearer tokens).
func String(s string) string {
	s = bearerPattern.ReplaceAllString(s, "${1}"+Placeholder)
	return jwtPattern.ReplaceAllString(s, Placeholder)
}

// Value returns a copy of v with every value under a sensitive key replaced and
// secrets inside strings masked. Maps and slices are copied; other values are
// returned as they are.
func Value(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		return Map(t)
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, e := range t {
			out[i] = Value(e)
		}
		return out
	case string:
		return String(t)
	default:
		return v
	}
}

// Map returns a copy of m with sensitive values replaced. A nil map stays nil.
func Map(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return nil
	}
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		if IsSensitiveKey(k) {
			out[k] = Placeholder
			continue
		}
		out[k] = Value(v)
	}
	return out
}
