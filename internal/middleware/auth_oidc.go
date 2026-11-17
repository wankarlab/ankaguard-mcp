package middleware

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// OIDCJWKSVerifier validates JWT tokens using JWKS from an OIDC provider.
// Caches the JWKS for cacheTTL and refreshes on key-not-found errors.
type OIDCJWKSVerifier struct {
	issuer     string
	audience   string
	jwksURL    string
	httpClient *http.Client

	mu          sync.RWMutex
	cachedKeys  map[string]*rsa.PublicKey // kid -> parsed RSA public key
	cacheExpiry time.Time
	cacheTTL    time.Duration
}

// JWKSet is the JSON structure of a JWKS endpoint response.
type JWKSet struct {
	Keys []JWK `json:"keys"`
}

// JWK represents a single JSON Web Key entry.
type JWK struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"` // RSA modulus (base64url)
	E   string `json:"e"` // RSA exponent (base64url)
}

// oidcDiscovery represents the relevant fields from an OIDC discovery document.
type oidcDiscovery struct {
	JWKSURI string `json:"jwks_uri"`
}

// NewOIDCJWKSVerifier creates a new OIDC JWKS verifier.
// It does not perform network calls at construction time; discovery is lazy.
func NewOIDCJWKSVerifier(issuer, audience string, cacheTTL time.Duration) *OIDCJWKSVerifier {
	if cacheTTL <= 0 {
		cacheTTL = time.Hour
	}
	return &OIDCJWKSVerifier{
		issuer:   issuer,
		audience: audience,
		cacheTTL: cacheTTL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// discoverJWKSURL fetches the OIDC discovery document and extracts the jwks_uri.
func (v *OIDCJWKSVerifier) discoverJWKSURL(ctx context.Context) error {
	discoveryURL := v.issuer + "/.well-known/openid-configuration"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create discovery request: %w", err)
	}

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch OIDC discovery document: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("OIDC discovery returned status %d", resp.StatusCode)
	}

	var discovery oidcDiscovery
	if err := json.NewDecoder(resp.Body).Decode(&discovery); err != nil {
		return fmt.Errorf("failed to decode OIDC discovery document: %w", err)
	}

	if discovery.JWKSURI == "" {
		return fmt.Errorf("OIDC discovery document missing jwks_uri")
	}

	v.jwksURL = discovery.JWKSURI
	return nil
}

// fetchKeys fetches the JWKS from jwksURL, parses RSA keys, and updates the cache.
// The caller must hold the write lock or ensure exclusive access.
func (v *OIDCJWKSVerifier) fetchKeys(ctx context.Context) error {
	// Lazily discover JWKS URL if not yet known.
	if v.jwksURL == "" {
		if err := v.discoverJWKSURL(ctx); err != nil {
			return err
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create JWKS request: %w", err)
	}

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch JWKS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("JWKS endpoint returned status %d", resp.StatusCode)
	}

	var keySet JWKSet
	if err := json.NewDecoder(resp.Body).Decode(&keySet); err != nil {
		return fmt.Errorf("failed to decode JWKS: %w", err)
	}

	parsed := make(map[string]*rsa.PublicKey, len(keySet.Keys))
	for _, jwk := range keySet.Keys {
		if jwk.Kty != "RSA" {
			// Only RSA keys are supported; skip others.
			continue
		}

		pubKey, err := parseRSAPublicKey(jwk)
		if err != nil {
			// Log and skip malformed keys rather than failing the entire set.
			continue
		}
		parsed[jwk.Kid] = pubKey
	}

	v.mu.Lock()
	v.cachedKeys = parsed
	v.cacheExpiry = time.Now().Add(v.cacheTTL)
	v.mu.Unlock()

	return nil
}

// parseRSAPublicKey converts a JWK entry to an *rsa.PublicKey.
func parseRSAPublicKey(jwk JWK) (*rsa.PublicKey, error) {
	// Decode base64url-encoded modulus N.
	nBytes, err := base64.RawURLEncoding.DecodeString(jwk.N)
	if err != nil {
		return nil, fmt.Errorf("failed to decode JWK modulus: %w", err)
	}

	// Decode base64url-encoded exponent E.
	eBytes, err := base64.RawURLEncoding.DecodeString(jwk.E)
	if err != nil {
		return nil, fmt.Errorf("failed to decode JWK exponent: %w", err)
	}

	n := new(big.Int).SetBytes(nBytes)

	// Convert exponent bytes to int.
	var eInt int
	for _, b := range eBytes {
		eInt = eInt<<8 | int(b)
	}

	return &rsa.PublicKey{N: n, E: eInt}, nil
}

// getKey returns the cached RSA public key for the given kid.
// Returns (nil, false) if the cache is expired or the kid is not found.
func (v *OIDCJWKSVerifier) getKey(kid string) (*rsa.PublicKey, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if time.Now().After(v.cacheExpiry) {
		return nil, false
	}

	key, ok := v.cachedKeys[kid]
	return key, ok
}

// Verify parses and validates an RS256 JWT using keys from the OIDC provider's JWKS.
// It validates the audience claim and performs one automatic key-rotation retry.
func (v *OIDCJWKSVerifier) Verify(ctx context.Context, tokenString string) (jwt.MapClaims, error) {
	return v.verifyWithRetry(ctx, tokenString, false)
}

func (v *OIDCJWKSVerifier) verifyWithRetry(ctx context.Context, tokenString string, isRetry bool) (jwt.MapClaims, error) {
	// Parse the token header without full validation to extract the kid.
	unverified, _, err := jwt.NewParser().ParseUnverified(tokenString, jwt.MapClaims{})
	if err != nil {
		return nil, fmt.Errorf("failed to parse token header: %w", err)
	}

	kid, _ := unverified.Header["kid"].(string)

	// Look up the key in cache; fetch if missing or expired.
	pubKey, found := v.getKey(kid)
	if !found {
		if err := v.fetchKeys(ctx); err != nil {
			return nil, fmt.Errorf("failed to refresh JWKS: %w", err)
		}
		pubKey, found = v.getKey(kid)
		if !found {
			if !isRetry {
				// Force a second fetch in case of concurrent rotation.
				return v.verifyWithRetry(ctx, tokenString, true)
			}
			return nil, fmt.Errorf("no matching key found for kid %q", kid)
		}
	}

	// Validate the token with the located RSA public key.
	token, err := jwt.ParseWithClaims(tokenString, jwt.MapClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return pubKey, nil
	})
	if err != nil {
		// On first attempt, a signature failure may indicate key rotation; retry once.
		if !isRetry {
			if refreshErr := v.fetchKeys(ctx); refreshErr == nil {
				return v.verifyWithRetry(ctx, tokenString, true)
			}
		}
		return nil, fmt.Errorf("failed to validate RS256 token: %w", err)
	}

	if !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("invalid token claims type")
	}

	// Validate audience claim.
	if v.audience != "" {
		if err := validateAudience(claims, v.audience); err != nil {
			return nil, err
		}
	}

	return claims, nil
}

// validateAudience checks that the token's "aud" claim contains the expected audience.
func validateAudience(claims jwt.MapClaims, expected string) error {
	aud, ok := claims["aud"]
	if !ok {
		return fmt.Errorf("token missing audience claim")
	}

	switch v := aud.(type) {
	case string:
		if v != expected {
			return fmt.Errorf("token audience %q does not match expected %q", v, expected)
		}
	case []interface{}:
		for _, a := range v {
			if s, ok := a.(string); ok && s == expected {
				return nil
			}
		}
		return fmt.Errorf("token audience does not contain expected value %q", expected)
	default:
		return fmt.Errorf("unexpected audience claim type %T", aud)
	}

	return nil
}
