package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// KYCVerifier queries the RantiPay Auth service to check a user's verified KYC level.
// RBAC-3: fires only when capital_flow != 'self' (default is 'self' = no KYC required).
type KYCVerifier struct {
	authBaseURL   string // e.g. "http://localhost:10100"
	internalToken string // X-Internal-Service-Token
	httpClient    *http.Client
}

// NewKYCVerifier creates a KYCVerifier.
// authBaseURL should point to the Auth service (port 10100).
// internalToken is the shared secret for service-to-service calls.
func NewKYCVerifier(authBaseURL, internalToken string) *KYCVerifier {
	return &KYCVerifier{
		authBaseURL:   authBaseURL,
		internalToken: internalToken,
		httpClient:    &http.Client{Timeout: 5 * time.Second},
	}
}

// KYCLevel returns the verified KYC level (0=unverified, 1=basic, 2=enhanced, 3=full)
// for userID by querying GET /api/v1/internal/permissions/:userID on the Auth service.
// Returns (0, error) on any network or parse error (fail-closed).
func (v *KYCVerifier) KYCLevel(ctx context.Context, userID string) (int, error) {
	url := fmt.Sprintf("%s/api/v1/internal/permissions/%s", v.authBaseURL, userID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("kyc: build request: %w", err)
	}
	if v.internalToken != "" {
		req.Header.Set("X-Internal-Service-Token", v.internalToken)
	}

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("kyc: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("kyc: auth returned status %d for user %q", resp.StatusCode, userID)
	}

	var body struct {
		Data struct {
			Level     int    `json:"level"`
			LevelName string `json:"level_name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, fmt.Errorf("kyc: parse response: %w", err)
	}
	return body.Data.Level, nil
}

// CheckLevel returns nil if the user's KYC level meets or exceeds required.
// If capitalFlow == "self" (the default), the check is skipped entirely (no KYC required).
func (v *KYCVerifier) CheckLevel(ctx context.Context, userID string, required int, capitalFlow string) error {
	if capitalFlow == "" || capitalFlow == "self" {
		return nil // RBAC-3: default 'self' = no KYC requirement
	}
	got, err := v.KYCLevel(ctx, userID)
	if err != nil {
		return fmt.Errorf("kyc check failed for user %q: %w", userID, err)
	}
	if got < required {
		return fmt.Errorf("kyc: user %q has level %d, required %d for capital_flow=%q",
			userID, got, required, capitalFlow)
	}
	return nil
}
