// Package twofa implements TOTP-based two-factor authentication following RFC 6238.
// Uses only stdlib crypto primitives (crypto/hmac, crypto/sha1, crypto/rand)
// since github.com/pquerna/otp is not in go.mod.
package twofa

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // TOTP RFC 6238 mandates SHA-1 for HOTP base
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	totpPeriod    = 30  // seconds per TOTP period (RFC 6238)
	totpDigits    = 6   // OTP digits
	totpWindow    = 1   // ±1 period tolerance
	secretBytes   = 20  // 160-bit secret → 32 base32 chars
	recoveryCount = 10  // number of recovery codes to generate
	recoveryLen   = 8   // characters per recovery code
)

// recoveryAlphabet is the character set for recovery codes (alphanumeric, no ambiguous chars).
const recoveryAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// TOTPService manages TOTP enrollment and verification for users.
type TOTPService struct {
	issuer string
}

// EnrollResult contains QR code URL and recovery codes for new enrollment.
type EnrollResult struct {
	Secret        string   // base32-encoded TOTP secret
	QRCodeURL     string   // otpauth:// URL for QR code generation
	RecoveryCodes []string // 10 single-use recovery codes (plain; hash before storage)
}

// NewTOTPService creates a TOTP service with the given issuer name.
func NewTOTPService(issuer string) *TOTPService {
	return &TOTPService{issuer: issuer}
}

// Enroll generates a new TOTP secret and recovery codes for a user.
// The returned RecoveryCodes are plain-text; callers must call HashRecoveryCodes
// before persisting them.
func (s *TOTPService) Enroll(userID string) (*EnrollResult, error) {
	rawSecret := make([]byte, secretBytes)
	if _, err := rand.Read(rawSecret); err != nil {
		return nil, fmt.Errorf("failed to generate TOTP secret: %w", err)
	}

	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(rawSecret)

	qrURL := fmt.Sprintf(
		"otpauth://totp/%s:%s?secret=%s&issuer=%s&algorithm=SHA1&digits=%d&period=%d",
		urlEncode(s.issuer), urlEncode(userID), secret, urlEncode(s.issuer), totpDigits, totpPeriod,
	)

	codes, err := generateRecoveryCodes(recoveryCount, recoveryLen)
	if err != nil {
		return nil, fmt.Errorf("failed to generate recovery codes: %w", err)
	}

	return &EnrollResult{
		Secret:        secret,
		QRCodeURL:     qrURL,
		RecoveryCodes: codes,
	}, nil
}

// Verify validates a TOTP code against the given base32 secret.
// It accepts codes from the current period and ±totpWindow periods to handle
// clock skew between client and server.
func (s *TOTPService) Verify(secret, code string) bool {
	if len(code) != totpDigits {
		return false
	}

	now := time.Now().Unix()
	counter := now / totpPeriod

	for delta := int64(-totpWindow); delta <= int64(totpWindow); delta++ {
		expected, err := generateTOTP(secret, counter+delta)
		if err != nil {
			continue
		}
		// Constant-time string comparison via subtle.ConstantTimeCompare.
		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			return true
		}
	}
	return false
}

// VerifyRecoveryCode checks whether code matches any entry in hashedCodes using
// constant-time comparison.  Returns valid=true and the remaining (unused)
// hashed codes when a match is found; the matched code is consumed.
func (s *TOTPService) VerifyRecoveryCode(hashedCodes []string, code string) (valid bool, remaining []string) {
	codeHash := hashRecoveryCode(code)

	remaining = make([]string, 0, len(hashedCodes))
	for _, h := range hashedCodes {
		// Constant-time comparison to prevent timing side-channels.
		if subtle.ConstantTimeCompare([]byte(codeHash), []byte(h)) == 1 {
			valid = true
			// Do not append: this code is consumed.
			continue
		}
		remaining = append(remaining, h)
	}

	if !valid {
		// Return original slice unchanged.
		return false, hashedCodes
	}
	return true, remaining
}

// HashRecoveryCodes returns SHA-256 hex hashes of the recovery codes.
// Store these instead of plain-text codes.
func HashRecoveryCodes(codes []string) []string {
	hashed := make([]string, len(codes))
	for i, c := range codes {
		hashed[i] = hashRecoveryCode(c)
	}
	return hashed
}

// =============================================================================
// Internal helpers
// =============================================================================

// generateTOTP computes a 6-digit HOTP value for the given secret and counter
// following RFC 4226 (HOTP) as extended by RFC 6238 (TOTP).
func generateTOTP(secret string, counter int64) (string, error) {
	// Decode base32 secret (accept both padded and unpadded).
	normalized := strings.ToUpper(strings.TrimSpace(secret))
	// Pad to multiple of 8 if needed.
	if pad := len(normalized) % 8; pad != 0 {
		normalized += strings.Repeat("=", 8-pad)
	}

	key, err := base32.StdEncoding.DecodeString(normalized)
	if err != nil {
		return "", fmt.Errorf("invalid TOTP secret: %w", err)
	}

	// Encode counter as big-endian uint64.
	msg := make([]byte, 8)
	binary.BigEndian.PutUint64(msg, uint64(counter)) //nolint:gosec

	// HMAC-SHA1 (RFC 4226 §5).
	mac := hmac.New(sha1.New, key)
	mac.Write(msg)
	h := mac.Sum(nil)

	// Dynamic truncation (RFC 4226 §5.3).
	offset := h[len(h)-1] & 0x0f
	binCode := (uint32(h[offset]&0x7f) << 24) |
		(uint32(h[offset+1]&0xff) << 16) |
		(uint32(h[offset+2]&0xff) << 8) |
		(uint32(h[offset+3] & 0xff))

	otp := binCode % uint32(math.Pow10(totpDigits))
	return fmt.Sprintf("%0*d", totpDigits, otp), nil
}

// generateRecoveryCodes creates n random codes of length l using recoveryAlphabet.
func generateRecoveryCodes(n, l int) ([]string, error) {
	codes := make([]string, n)
	buf := make([]byte, l)
	alphabetLen := byte(len(recoveryAlphabet))

	for i := range codes {
		if _, err := rand.Read(buf); err != nil {
			return nil, err
		}
		code := make([]byte, l)
		for j := range code {
			code[j] = recoveryAlphabet[buf[j]%alphabetLen]
		}
		codes[i] = string(code)
	}
	return codes, nil
}

// hashRecoveryCode returns a lowercase hex SHA-256 digest of the code.
func hashRecoveryCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// urlEncode performs minimal percent-encoding suitable for otpauth:// URIs.
func urlEncode(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z',
			r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.', r == '~':
			b.WriteRune(r)
		default:
			for _, byteVal := range []byte(string(r)) {
				fmt.Fprintf(&b, "%%%02X", byteVal)
			}
		}
	}
	return b.String()
}
