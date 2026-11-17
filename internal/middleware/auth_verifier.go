package middleware

import (
	"context"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

// JWTVerifier validates JWT tokens and returns parsed claims.
// Multiple implementations: HMACVerifier (current), OIDCJWKSVerifier (H1.2).
type JWTVerifier interface {
	// Verify parses and validates the token string.
	// Returns the parsed claims on success, or an error if invalid/expired.
	Verify(ctx context.Context, tokenString string) (jwt.MapClaims, error)
}

// HMACVerifier validates JWT tokens using a shared HMAC secret (current behavior).
type HMACVerifier struct {
	secret []byte
}

// NewHMACVerifier creates a verifier with the given HMAC secret.
func NewHMACVerifier(secret string) *HMACVerifier {
	return &HMACVerifier{secret: []byte(secret)}
}

// Verify parses and validates an HMAC-signed JWT token.
// It enforces that the signing method is HMAC (HS256/HS384/HS512).
func (v *HMACVerifier) Verify(_ context.Context, tokenString string) (jwt.MapClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, jwt.MapClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return v.secret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to parse HMAC token: %w", err)
	}

	if !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("invalid token claims type")
	}

	return claims, nil
}
