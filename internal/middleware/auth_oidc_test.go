package middleware_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rantipay/trading-mcp/internal/middleware"
)

// ─────────────────────────────────────────────
// Test helpers
// ─────────────────────────────────────────────

// generateRSAKey returns a fresh 2048-bit RSA key pair.
func generateRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err, "failed to generate RSA key pair")
	return key
}

// bigIntToBase64URL encodes a big.Int as a base64url string (no padding).
func bigIntToBase64URL(n *big.Int) string {
	return base64.RawURLEncoding.EncodeToString(n.Bytes())
}

// intToBase64URL encodes a small integer as a base64url string (no padding).
func intToBase64URL(e int) string {
	// Convert int to minimal big-endian byte slice.
	b := big.NewInt(int64(e))
	return base64.RawURLEncoding.EncodeToString(b.Bytes())
}

// jwksHandlerForKeys returns an http.HandlerFunc that serves a JWKS containing
// the public keys provided (each key must have a unique kid).
func jwksHandlerForKeys(keys map[string]*rsa.PublicKey) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		type jwkEntry struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			Alg string `json:"alg"`
			Use string `json:"use"`
			N   string `json:"n"`
			E   string `json:"e"`
		}
		type jwksResponse struct {
			Keys []jwkEntry `json:"keys"`
		}

		var entries []jwkEntry
		for kid, pub := range keys {
			entries = append(entries, jwkEntry{
				Kid: kid,
				Kty: "RSA",
				Alg: "RS256",
				Use: "sig",
				N:   bigIntToBase64URL(pub.N),
				E:   intToBase64URL(pub.E),
			})
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwksResponse{Keys: entries})
	}
}

// oidcServerWithJWKS spins up a combined OIDC discovery + JWKS test server.
// The JWKS handler is swappable via the atomic pointer to allow rotation tests.
func oidcServerWithJWKS(t *testing.T, initialKeys map[string]*rsa.PublicKey) (issuerURL string, swapKeys func(map[string]*rsa.PublicKey)) {
	t.Helper()

	var current atomic.Pointer[map[string]*rsa.PublicKey]
	current.Store(&initialKeys)

	mux := http.NewServeMux()

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		srv := r.Host
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":   "http://" + srv,
			"jwks_uri": "http://" + srv + "/.well-known/jwks.json",
		})
	})

	mux.HandleFunc("/.well-known/jwks.json", func(w http.ResponseWriter, r *http.Request) {
		keys := *current.Load()
		jwksHandlerForKeys(keys)(w, r)
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	swap := func(newKeys map[string]*rsa.PublicKey) {
		current.Store(&newKeys)
	}

	return ts.URL, swap
}

// signRS256Token creates a signed RS256 JWT with the given claims and private key.
func signRS256Token(t *testing.T, privKey *rsa.PrivateKey, kid string, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid

	signed, err := token.SignedString(privKey)
	require.NoError(t, err, "failed to sign RS256 token")
	return signed
}

// ─────────────────────────────────────────────
// H1.2 tests — OIDCJWKSVerifier
// ─────────────────────────────────────────────

// TestOIDCJWKSVerifier_ValidToken verifies that a properly signed RS256 token
// with correct audience is accepted and its claims are returned intact.
func TestOIDCJWKSVerifier_ValidToken(t *testing.T) {
	privKey := generateRSAKey(t)
	const kid = "key-1"
	const audience = "trading-service"
	const subject = "user-abc"

	issuerURL, _ := oidcServerWithJWKS(t, map[string]*rsa.PublicKey{kid: &privKey.PublicKey})

	verifier := middleware.NewOIDCJWKSVerifier(issuerURL, audience, time.Hour)

	claims := jwt.MapClaims{
		"sub": subject,
		"aud": audience,
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
	}
	tokenStr := signRS256Token(t, privKey, kid, claims)

	got, err := verifier.Verify(t.Context(), tokenStr)

	require.NoError(t, err, "expected valid token to be accepted")
	assert.Equal(t, subject, got["sub"])
}

// TestOIDCJWKSVerifier_ExpiredToken verifies that an expired token is rejected.
func TestOIDCJWKSVerifier_ExpiredToken(t *testing.T) {
	privKey := generateRSAKey(t)
	const kid = "key-exp"
	const audience = "trading-service"

	issuerURL, _ := oidcServerWithJWKS(t, map[string]*rsa.PublicKey{kid: &privKey.PublicKey})

	verifier := middleware.NewOIDCJWKSVerifier(issuerURL, audience, time.Hour)

	claims := jwt.MapClaims{
		"sub": "user-expired",
		"aud": audience,
		// exp set 2 hours in the past.
		"exp": time.Now().Add(-2 * time.Hour).Unix(),
		"iat": time.Now().Add(-3 * time.Hour).Unix(),
	}
	tokenStr := signRS256Token(t, privKey, kid, claims)

	_, err := verifier.Verify(t.Context(), tokenStr)

	require.Error(t, err, "expected expired token to be rejected")
	assert.Contains(t, err.Error(), "token is expired",
		"error should mention token expiration")
}

// TestJWKRotation verifies that the verifier transparently handles JWKS key rotation:
// it starts with key A, a token signed with key B fails initially, triggers a JWKS
// refresh that now returns key B, and the second attempt succeeds.
//
// Acceptance criterion: go test -run JWKRotation
func TestJWKRotation(t *testing.T) {
	keyA := generateRSAKey(t)
	keyB := generateRSAKey(t)

	const kidA = "key-a"
	const kidB = "key-b"
	const audience = "trading-service"

	// Server starts serving only key A.
	issuerURL, swapKeys := oidcServerWithJWKS(t, map[string]*rsa.PublicKey{kidA: &keyA.PublicKey})

	// Use a very short TTL so the cache expires quickly, enabling the retry path.
	verifier := middleware.NewOIDCJWKSVerifier(issuerURL, audience, time.Millisecond)

	// Step 1: verify a token signed with key A — must succeed.
	claimsA := jwt.MapClaims{
		"sub": "user-a",
		"aud": audience,
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	tokenA := signRS256Token(t, keyA, kidA, claimsA)

	gotA, err := verifier.Verify(t.Context(), tokenA)
	require.NoError(t, err, "token signed with key A should be valid before rotation")
	assert.Equal(t, "user-a", gotA["sub"])

	// Step 2: rotate — server now only has key B.
	swapKeys(map[string]*rsa.PublicKey{kidB: &keyB.PublicKey})

	// Allow the in-memory cache TTL to lapse so fetchKeys is triggered.
	time.Sleep(5 * time.Millisecond)

	// Step 3: verify a token signed with key B — must succeed after transparent refresh.
	claimsB := jwt.MapClaims{
		"sub": "user-b",
		"aud": audience,
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	tokenB := signRS256Token(t, keyB, kidB, claimsB)

	gotB, err := verifier.Verify(t.Context(), tokenB)
	require.NoError(t, err, "token signed with rotated key B should be valid after refresh")
	assert.Equal(t, "user-b", gotB["sub"])

	// Step 4: old token (key A) must now be rejected since key A is no longer served.
	_, err = verifier.Verify(t.Context(), tokenA)
	require.Error(t, err, "token signed with rotated-out key A should be rejected")
}

// ─────────────────────────────────────────────
// H1.1 tests — HMACVerifier
// ─────────────────────────────────────────────

// TestHMACVerifier_ValidToken verifies that a valid HS256 token is accepted
// and its claims are returned correctly.
func TestHMACVerifier_ValidToken(t *testing.T) {
	const secret = "super-secret-hmac-key"
	const subject = "hmac-user"

	verifier := middleware.NewHMACVerifier(secret)

	claims := jwt.MapClaims{
		"sub":  subject,
		"role": "trader",
		"exp":  time.Now().Add(time.Hour).Unix(),
		"iat":  time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, err := token.SignedString([]byte(secret))
	require.NoError(t, err)

	got, err := verifier.Verify(t.Context(), tokenStr)

	require.NoError(t, err, "expected valid HMAC token to be accepted")
	assert.Equal(t, subject, got["sub"])
	assert.Equal(t, "trader", got["role"])
}

// TestHMACVerifier_InvalidSignature verifies that a token signed with a different
// secret is rejected with an error.
func TestHMACVerifier_InvalidSignature(t *testing.T) {
	const correctSecret = "correct-secret"
	const wrongSecret = "wrong-secret"

	verifier := middleware.NewHMACVerifier(correctSecret)

	// Sign the token with the wrong secret.
	claims := jwt.MapClaims{
		"sub": "attacker",
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, err := token.SignedString([]byte(wrongSecret))
	require.NoError(t, err)

	_, err = verifier.Verify(t.Context(), tokenStr)

	require.Error(t, err, "expected token with wrong signature to be rejected")
	assert.Contains(t, err.Error(), "failed to parse HMAC token",
		"error should indicate HMAC parse failure")
}
