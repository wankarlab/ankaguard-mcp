package kms

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// VaultConfig holds the configuration for the Vault Transit KMS provider.
type VaultConfig struct {
	// Address is the base URL of the Vault server (e.g. "https://vault.example.com").
	Address string
	// RoleID is the AppRole role_id used for authentication.
	RoleID string
	// SecretID is the AppRole secret_id used for authentication.
	SecretID string
	// TransitKeyName is the name of the Vault Transit key used to wrap/unwrap DEKs.
	TransitKeyName string
	// DEKCacheTTL is how long to cache the active plaintext DEK (default: 1h).
	// Set to 0 to disable caching.
	DEKCacheTTL time.Duration
}

// validate checks that all required fields are populated.
func (c VaultConfig) validate() error {
	if c.Address == "" {
		return errors.New("vault: Address is required")
	}
	if c.RoleID == "" {
		return errors.New("vault: RoleID is required")
	}
	if c.SecretID == "" {
		return errors.New("vault: SecretID is required")
	}
	if c.TransitKeyName == "" {
		return errors.New("vault: TransitKeyName is required")
	}
	return nil
}

// cachedDEK holds a plaintext DEK and the time it was fetched.
type cachedDEK struct {
	plaintext []byte
	wrapped   []byte
	keyID     string
	fetchedAt time.Time
}

// tokenCache holds a Vault client token and its lease duration.
type tokenCache struct {
	token     string
	expiresAt time.Time
}

const (
	maxRetries    = 3
	retryBaseWait = 200 * time.Millisecond
	// renewBeforeExpiry is how early we renew the Vault token before it expires.
	renewBeforeExpiry = 30 * time.Second
)

// VaultTransitProvider implements KMSProvider using HashiCorp Vault Transit secrets engine.
// It uses AppRole authentication and caches both the Vault token and the active DEK.
type VaultTransitProvider struct {
	cfg        VaultConfig
	httpClient *http.Client

	mu         sync.Mutex
	tokenCache *tokenCache
	dekCache   *cachedDEK
}

// NewVaultProvider creates and validates a new VaultTransitProvider.
// Returns an error if the configuration is invalid.
func NewVaultProvider(cfg VaultConfig) (*VaultTransitProvider, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if cfg.DEKCacheTTL == 0 {
		cfg.DEKCacheTTL = time.Hour
	}
	return &VaultTransitProvider{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// GenerateDataKey asks Vault Transit to generate a new plaintext DEK and wrap it.
// It caches the result for cfg.DEKCacheTTL to reduce calls to Vault.
func (v *VaultTransitProvider) GenerateDataKey(ctx context.Context) (plaintext []byte, wrapped []byte, keyID string, err error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	// Return from cache if still fresh.
	if v.dekCache != nil && time.Since(v.dekCache.fetchedAt) < v.cfg.DEKCacheTTL {
		return v.dekCache.plaintext, v.dekCache.wrapped, v.dekCache.keyID, nil
	}

	token, err := v.getTokenLocked(ctx)
	if err != nil {
		return nil, nil, "", fmt.Errorf("vault: failed to get auth token: %w", err)
	}

	url := fmt.Sprintf("%s/v1/transit/datakey/plaintext/%s", v.cfg.Address, v.cfg.TransitKeyName)

	var result struct {
		Data struct {
			Plaintext  string `json:"plaintext"`
			Ciphertext string `json:"ciphertext"`
			KeyVersion int    `json:"key_version"`
		} `json:"data"`
	}

	if err := v.doWithRetry(ctx, http.MethodPost, url, token, nil, &result); err != nil {
		return nil, nil, "", fmt.Errorf("vault: datakey generation failed: %w", err)
	}

	plaintextBytes, err := base64.StdEncoding.DecodeString(result.Data.Plaintext)
	if err != nil {
		return nil, nil, "", fmt.Errorf("vault: failed to decode plaintext DEK: %w", err)
	}

	kID := fmt.Sprintf("%s:v%d", v.cfg.TransitKeyName, result.Data.KeyVersion)

	v.dekCache = &cachedDEK{
		plaintext: plaintextBytes,
		wrapped:   []byte(result.Data.Ciphertext),
		keyID:     kID,
		fetchedAt: time.Now(),
	}

	return plaintextBytes, []byte(result.Data.Ciphertext), kID, nil
}

// UnwrapDataKey calls Vault Transit decrypt to recover the plaintext DEK.
func (v *VaultTransitProvider) UnwrapDataKey(ctx context.Context, wrapped []byte, _ string) (plaintext []byte, err error) {
	v.mu.Lock()
	token, err := v.getTokenLocked(ctx)
	v.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("vault: failed to get auth token: %w", err)
	}

	url := fmt.Sprintf("%s/v1/transit/decrypt/%s", v.cfg.Address, v.cfg.TransitKeyName)
	body := map[string]string{"ciphertext": string(wrapped)}

	var result struct {
		Data struct {
			Plaintext string `json:"plaintext"`
		} `json:"data"`
	}

	if err := v.doWithRetry(ctx, http.MethodPost, url, token, body, &result); err != nil {
		return nil, fmt.Errorf("vault: decrypt failed: %w", err)
	}

	plaintextBytes, err := base64.StdEncoding.DecodeString(result.Data.Plaintext)
	if err != nil {
		return nil, fmt.Errorf("vault: failed to decode decrypted DEK: %w", err)
	}

	return plaintextBytes, nil
}

// Health performs a lightweight check against the Vault health endpoint.
func (v *VaultTransitProvider) Health(ctx context.Context) error {
	url := fmt.Sprintf("%s/v1/sys/health", v.cfg.Address)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("vault health: failed to create request: %w", err)
	}

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("vault health: request failed: %w", err)
	}
	defer resp.Body.Close()

	// Vault health endpoint returns 200 for initialized+unsealed, 429 for standby, etc.
	// Any 5xx indicates a problem.
	if resp.StatusCode >= 500 {
		return fmt.Errorf("vault health: server returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// ---- internal helpers ----

// getTokenLocked returns a valid Vault token, refreshing via AppRole if needed.
// Caller must hold v.mu.
func (v *VaultTransitProvider) getTokenLocked(ctx context.Context) (string, error) {
	now := time.Now()
	if v.tokenCache != nil && now.Before(v.tokenCache.expiresAt.Add(-renewBeforeExpiry)) {
		return v.tokenCache.token, nil
	}

	// Authenticate with AppRole.
	url := fmt.Sprintf("%s/v1/auth/approle/login", v.cfg.Address)
	body := map[string]string{
		"role_id":   v.cfg.RoleID,
		"secret_id": v.cfg.SecretID,
	}

	var result struct {
		Auth struct {
			ClientToken   string `json:"client_token"`
			LeaseDuration int    `json:"lease_duration"` // seconds
		} `json:"auth"`
	}

	if err := v.doOnce(ctx, http.MethodPost, url, "", body, &result); err != nil {
		return "", fmt.Errorf("approle login failed: %w", err)
	}

	leaseDuration := time.Duration(result.Auth.LeaseDuration) * time.Second
	if leaseDuration <= 0 {
		leaseDuration = time.Hour
	}

	v.tokenCache = &tokenCache{
		token:     result.Auth.ClientToken,
		expiresAt: now.Add(leaseDuration),
	}

	return v.tokenCache.token, nil
}

// doWithRetry executes an HTTP request with exponential backoff on transient errors.
func (v *VaultTransitProvider) doWithRetry(ctx context.Context, method, url, token string, body interface{}, out interface{}) error {
	var lastErr error
	wait := retryBaseWait
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
			wait *= 2
		}
		if err := v.doOnce(ctx, method, url, token, body, out); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return fmt.Errorf("after %d retries: %w", maxRetries, lastErr)
}

// doOnce executes a single HTTP request and JSON-decodes the response into out.
func (v *VaultTransitProvider) doOnce(ctx context.Context, method, url, token string, body interface{}, out interface{}) error {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("vault: failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return fmt.Errorf("vault: failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Vault-Token", token)
	}

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("vault: HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("vault: failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("vault: unexpected status %d: %s", resp.StatusCode, truncate(string(respBytes), 256))
	}

	if out != nil {
		if err := json.Unmarshal(respBytes, out); err != nil {
			return fmt.Errorf("vault: failed to decode response: %w", err)
		}
	}

	return nil
}

// truncate returns s if len(s) <= n, otherwise s[:n]+"…".
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
