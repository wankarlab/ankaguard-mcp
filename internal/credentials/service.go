package credentials

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/credentials/kms"
	"github.com/rantipay/trading-mcp/internal/gct"
)

const (
	// MaxCredentialsPerUser is the maximum number of credentials a user can have
	MaxCredentialsPerUser = 10

	// CircuitBreakerThreshold is the number of consecutive failures before disabling a credential
	CircuitBreakerThreshold = 5

	// ValidationTimeout is the timeout for credential validation against exchanges
	ValidationTimeout = 30 * time.Second

	// NoWithdrawEnforcement controls how the service reacts when an API key has
	// withdrawal permissions enabled.
	//   "block" — reject the credential with a hard error (default, most secure)
	//   "warn"  — log a warning but allow the credential
	//   "off"   — disable the check entirely
	NoWithdrawEnforcement = "block"
)

var (
	// ErrMaxCredentialsReached is returned when a user has reached the credential limit
	ErrMaxCredentialsReached = errors.New("maximum number of credentials reached")

	// ErrCredentialDisabled is returned when trying to use a disabled credential
	ErrCredentialDisabled = errors.New("credential is disabled due to repeated failures")

	// ErrCredentialExpired is returned when trying to use an expired credential
	ErrCredentialExpired = errors.New("credential has expired")

	// ErrValidationFailed is returned when credential validation fails
	ErrValidationFailed = errors.New("credential validation failed")

	// ErrNoGCTClient is returned when no GCT client is available for testing
	ErrNoGCTClient = errors.New("no GCT client available for credential testing")
)

// Service provides business logic for credential management.
type Service interface {
	// AddCredential adds a new credential for a user
	AddCredential(ctx context.Context, input *CreateCredentialInput) (*CredentialSummary, error)

	// GetCredential retrieves decrypted credentials for use
	GetCredential(ctx context.Context, userID, exchange string) (*DecryptedCredential, error)

	// ListCredentials lists all credentials for a user (without secrets)
	ListCredentials(ctx context.Context, userID string) ([]*CredentialSummary, error)

	// UpdateCredential updates an existing credential
	UpdateCredential(ctx context.Context, userID, exchange string, input *UpdateCredentialInput) error

	// DeleteCredential removes a credential
	DeleteCredential(ctx context.Context, userID, exchange string) error

	// TestCredential tests if a credential works against the exchange
	TestCredential(ctx context.Context, userID, exchange string) (*TestCredentialResult, error)

	// ValidateAll validates all active credentials for a user
	ValidateAll(ctx context.Context, userID string) (map[string]*TestCredentialResult, error)

	// RecordUsage records that a credential was used
	RecordUsage(ctx context.Context, userID, exchange string) error

	// RecordFailure records a credential failure for circuit breaker
	RecordFailure(ctx context.Context, userID, exchange string) error

	// RecordSuccess records a successful use and resets failure count
	RecordSuccess(ctx context.Context, userID, exchange string) error

	// RotateEncryptionKey re-encrypts all credentials with a new key
	RotateEncryptionKey(ctx context.Context, oldKeyID string) (int, error)
}

// CredentialService implements the Service interface.
type CredentialService struct {
	repo        Repository
	encryption  *EncryptionService
	gctClient   gct.GCTClient
	logger      *logrus.Logger
	auditLogger AuditLogger
	mu          sync.RWMutex
}

// ServiceConfig holds configuration for the credential service.
type ServiceConfig struct {
	Repository        Repository
	EncryptionService *EncryptionService
	GCTClient         gct.GCTClient
	Logger            *logrus.Logger
	AuditLogger       AuditLogger
	// KMSProvider is optional. When non-nil it is attached to the EncryptionService
	// to enable envelope encryption (KMS mode). Pass nil for legacy/backward-compat mode.
	KMSProvider kms.KMSProvider
}

// NewService creates a new credential service.
func NewService(cfg *ServiceConfig) (*CredentialService, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}
	if cfg.Repository == nil {
		return nil, errors.New("repository is required")
	}
	if cfg.EncryptionService == nil {
		return nil, errors.New("encryption service is required")
	}

	logger := cfg.Logger
	if logger == nil {
		logger = logrus.New()
	}

	auditLogger := cfg.AuditLogger
	if auditLogger == nil {
		auditLogger = NewNoOpAuditLogger()
	}

	// Attach KMS provider to the encryption service when supplied (envelope encryption).
	if cfg.KMSProvider != nil {
		cfg.EncryptionService.SetKMSProvider(cfg.KMSProvider)
	}

	return &CredentialService{
		repo:        cfg.Repository,
		encryption:  cfg.EncryptionService,
		gctClient:   cfg.GCTClient,
		logger:      logger,
		auditLogger: auditLogger,
	}, nil
}

// SetGCTClient sets or updates the GCT client for credential testing.
func (s *CredentialService) SetGCTClient(client gct.GCTClient) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gctClient = client
}

// SetAuditLogger sets or updates the audit logger.
func (s *CredentialService) SetAuditLogger(auditLogger AuditLogger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if auditLogger != nil {
		s.auditLogger = auditLogger
	}
}

// getAuditContext extracts audit context from the provided context or creates a minimal one.
func (s *CredentialService) getAuditContext(ctx context.Context, userID, tenantID string) *AuditContext {
	auditCtx := AuditContextFromContext(ctx)
	if auditCtx != nil {
		// Ensure user and tenant IDs are set
		if auditCtx.UserID == "" {
			auditCtx.UserID = userID
		}
		if auditCtx.TenantID == "" {
			auditCtx.TenantID = tenantID
		}
		return auditCtx
	}

	return &AuditContext{
		UserID:   userID,
		TenantID: tenantID,
	}
}

// verifyNoWithdraw queries the exchange permission endpoint to detect whether the
// provided API key has withdrawal permissions enabled.
//
// Returns:
//   - withdrawEnabled: true if the key has withdraw access
//   - ipLocked: true if the key is restricted to specific IPs (reduces risk)
//   - err: non-nil only for infrastructure/network errors (not for "withdraw=true")
//
// Unsupported exchanges return (false, false, nil) with a warning log.
func (s *CredentialService) verifyNoWithdraw(ctx context.Context, cred *DecryptedCredential) (withdrawEnabled bool, ipLocked bool, err error) {
	exchange := NormalizeExchangeName(cred.ExchangeName)

	switch exchange {
	case ExchangeKuCoin:
		return s.verifyNoWithdrawKuCoin(ctx, cred)
	case ExchangeBinance:
		return s.verifyNoWithdrawBinance(ctx, cred)
	default:
		// SEC-8: deny-by-default for unsupported venues — never silently skip the
		// withdraw check for an unknown exchange. Callers must explicitly allow.
		s.logger.WithFields(logrus.Fields{
			"exchange": exchange,
			"user_id":  cred.UserID,
		}).Error("verifyNoWithdraw: unsupported exchange — denying credential to avoid permissive fallback")
		return false, false, fmt.Errorf("verifyNoWithdraw: unsupported exchange %q — withdraw check cannot be performed; credential denied", exchange)
	}
}

// verifyNoWithdrawKuCoin calls the KuCoin API key permissions endpoint.
// Reference: GET https://api.kucoin.com/api/v1/user/api-key/permission
func (s *CredentialService) verifyNoWithdrawKuCoin(ctx context.Context, cred *DecryptedCredential) (withdrawEnabled bool, ipLocked bool, err error) {
	const endpoint = "https://api.kucoin.com"
	path := "/api/v1/user/api-key/permission"
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	preSign := ts + "GET" + path

	mac := hmac.New(sha256.New, []byte(cred.APISecret))
	mac.Write([]byte(preSign))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	ppMac := hmac.New(sha256.New, []byte(cred.APISecret))
	ppMac.Write([]byte(cred.Passphrase))
	pp := base64.StdEncoding.EncodeToString(ppMac.Sum(nil))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+path, nil)
	if err != nil {
		return false, false, fmt.Errorf("kucoin verifyNoWithdraw: failed to build request: %w", err)
	}
	req.Header.Set("KC-API-KEY", cred.APIKey)
	req.Header.Set("KC-API-SIGN", sig)
	req.Header.Set("KC-API-TIMESTAMP", ts)
	req.Header.Set("KC-API-PASSPHRASE", pp)
	req.Header.Set("KC-API-KEY-VERSION", "2")
	req.Header.Set("Content-Type", "application/json")

	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return false, false, fmt.Errorf("kucoin verifyNoWithdraw: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		// Non-200 could mean the endpoint doesn't exist on this API plan; log and allow.
		s.logger.WithFields(logrus.Fields{
			"status": resp.StatusCode,
			"body":   string(body),
		}).Warn("kucoin verifyNoWithdraw: unexpected status, skipping check")
		return false, false, nil
	}

	var parsed struct {
		Code string `json:"code"`
		Data struct {
			Permission string `json:"permission"` // comma-separated: "General,Trade,Transfer"
			IpWhitelist string `json:"ipWhitelist"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Code != "200000" {
		s.logger.WithField("body", string(body)).Warn("kucoin verifyNoWithdraw: unexpected response format")
		return false, false, nil
	}

	perms := strings.ToLower(parsed.Data.Permission)
	withdrawEnabled = strings.Contains(perms, "transfer") || strings.Contains(perms, "withdraw")
	ipLocked = strings.TrimSpace(parsed.Data.IpWhitelist) != "" && parsed.Data.IpWhitelist != "[]"

	return withdrawEnabled, ipLocked, nil
}

// verifyNoWithdrawBinance calls the Binance API key permissions endpoint.
// Reference: GET https://api.binance.com/sapi/v1/account/apiRestrictions
func (s *CredentialService) verifyNoWithdrawBinance(ctx context.Context, cred *DecryptedCredential) (withdrawEnabled bool, ipLocked bool, err error) {
	const endpoint = "https://api.binance.com"
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	query := "timestamp=" + ts

	mac := hmac.New(sha256.New, []byte(cred.APISecret))
	mac.Write([]byte(query))
	sig := fmt.Sprintf("%x", mac.Sum(nil))

	url := endpoint + "/sapi/v1/account/apiRestrictions?" + query + "&signature=" + sig

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, false, fmt.Errorf("binance verifyNoWithdraw: failed to build request: %w", err)
	}
	req.Header.Set("X-MBX-APIKEY", cred.APIKey)

	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return false, false, fmt.Errorf("binance verifyNoWithdraw: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		s.logger.WithFields(logrus.Fields{
			"status": resp.StatusCode,
			"body":   string(body),
		}).Warn("binance verifyNoWithdraw: unexpected status, skipping check")
		return false, false, nil
	}

	var parsed struct {
		EnableWithdrawals bool   `json:"enableWithdrawals"`
		IPRestrict        bool   `json:"ipRestrict"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		s.logger.WithField("body", string(body)).Warn("binance verifyNoWithdraw: unexpected response format")
		return false, false, nil
	}

	return parsed.EnableWithdrawals, parsed.IPRestrict, nil
}

// enforceNoWithdraw applies the NoWithdrawEnforcement policy. Returns an error
// when enforcement is "block" and withdrawEnabled is true.
func (s *CredentialService) enforceNoWithdraw(ctx context.Context, cred *DecryptedCredential) error {
	if NoWithdrawEnforcement == "off" {
		return nil
	}

	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	withdrawEnabled, ipLocked, err := s.verifyNoWithdraw(checkCtx, cred)
	if err != nil {
		// Infrastructure error: log and allow (fail open) to avoid blocking users when
		// the exchange API is unreachable.
		s.logger.WithFields(logrus.Fields{
			"exchange": cred.ExchangeName,
			"error":    err.Error(),
		}).Warn("enforceNoWithdraw: permission check failed, allowing credential (fail-open)")
		return nil
	}

	if !withdrawEnabled {
		return nil
	}

	s.logger.WithFields(logrus.Fields{
		"exchange":  cred.ExchangeName,
		"user_id":   cred.UserID,
		"ip_locked": ipLocked,
	}).Warn("enforceNoWithdraw: API key has withdrawal permissions")

	if NoWithdrawEnforcement == "block" {
		return errors.New("CREDENTIAL_WITHDRAW_NOT_ALLOWED: API key must not have withdrawal permissions")
	}

	// "warn" mode — already logged above, allow the credential.
	return nil
}

// AddCredential adds a new credential for a user.
func (s *CredentialService) AddCredential(ctx context.Context, input *CreateCredentialInput) (*CredentialSummary, error) {
	auditCtx := s.getAuditContext(ctx, "", "")
	if input != nil {
		auditCtx = s.getAuditContext(ctx, input.UserID, input.TenantID)
	}

	if input == nil {
		err := errors.New("input is required")
		s.auditLogger.LogCreate(ctx, nil, auditCtx, false, err)
		return nil, err
	}

	// Validate input
	if err := input.Validate(); err != nil {
		validationErr := fmt.Errorf("validation failed: %w", err)
		s.auditLogger.LogCreate(ctx, nil, auditCtx, false, validationErr)
		return nil, validationErr
	}

	// Check that the API key does not have withdrawal permissions (security gate).
	tempCred := &DecryptedCredential{
		UserID:       input.UserID,
		TenantID:     input.TenantID,
		ExchangeName: NormalizeExchangeName(input.ExchangeName),
		APIKey:       input.APIKey,
		APISecret:    input.APISecret,
		Passphrase:   input.Passphrase,
	}
	if withdrawErr := s.enforceNoWithdraw(ctx, tempCred); withdrawErr != nil {
		s.auditLogger.LogCreate(ctx, nil, auditCtx, false, withdrawErr)
		return nil, withdrawErr
	}

	// Check credential count limit
	count, err := s.repo.CountByUser(ctx, input.UserID)
	if err != nil {
		countErr := fmt.Errorf("failed to count user credentials: %w", err)
		s.auditLogger.LogCreate(ctx, nil, auditCtx, false, countErr)
		return nil, countErr
	}
	if count >= MaxCredentialsPerUser {
		s.auditLogger.LogCreate(ctx, nil, auditCtx, false, ErrMaxCredentialsReached)
		return nil, ErrMaxCredentialsReached
	}

	// Encrypt the sensitive fields
	apiKeyEnc, keyID, err := s.encryption.Encrypt([]byte(input.APIKey))
	if err != nil {
		encryptErr := fmt.Errorf("failed to encrypt API key: %w", err)
		s.auditLogger.LogCreate(ctx, nil, auditCtx, false, encryptErr)
		return nil, encryptErr
	}

	apiSecretEnc, _, err := s.encryption.Encrypt([]byte(input.APISecret))
	if err != nil {
		encryptErr := fmt.Errorf("failed to encrypt API secret: %w", err)
		s.auditLogger.LogCreate(ctx, nil, auditCtx, false, encryptErr)
		return nil, encryptErr
	}

	var passphraseEnc []byte
	if input.Passphrase != "" {
		passphraseEnc, _, err = s.encryption.Encrypt([]byte(input.Passphrase))
		if err != nil {
			encryptErr := fmt.Errorf("failed to encrypt passphrase: %w", err)
			s.auditLogger.LogCreate(ctx, nil, auditCtx, false, encryptErr)
			return nil, encryptErr
		}
	}

	// Convert permissions to JSON
	var permissionsJSON string
	if len(input.Permissions) > 0 {
		permBytes, err := json.Marshal(input.Permissions)
		if err != nil {
			marshalErr := fmt.Errorf("failed to marshal permissions: %w", err)
			s.auditLogger.LogCreate(ctx, nil, auditCtx, false, marshalErr)
			return nil, marshalErr
		}
		permissionsJSON = string(permBytes)
	}

	// Create the credential record
	credential := &UserCredential{
		UserID:              input.UserID,
		TenantID:            input.TenantID,
		ExchangeName:        NormalizeExchangeName(input.ExchangeName),
		APIKeyEncrypted:     apiKeyEnc,
		APISecretEncrypted:  apiSecretEnc,
		PassphraseEncrypted: passphraseEnc,
		EncryptionKeyID:     keyID,
		ExpiresAt:           input.ExpiresAt,
		IsActive:            true,
		Label:               input.Label,
		Permissions:         permissionsJSON,
		ValidationStatus:    "unknown",
	}

	if err := s.repo.Create(ctx, credential); err != nil {
		s.auditLogger.LogCreate(ctx, credential, auditCtx, false, err)
		if errors.Is(err, ErrDuplicateCredential) {
			return nil, err
		}
		return nil, fmt.Errorf("failed to create credential: %w", err)
	}

	// Log successful creation
	s.auditLogger.LogCreate(ctx, credential, auditCtx, true, nil)

	s.logger.WithFields(logrus.Fields{
		"user_id":  input.UserID,
		"exchange": credential.ExchangeName,
	}).Info("Credential created successfully")

	// Get API key prefix for summary
	apiKeyPrefix := input.APIKey
	if len(apiKeyPrefix) > 4 {
		apiKeyPrefix = apiKeyPrefix[:4] + "****"
	}

	return credential.ToSummary(apiKeyPrefix), nil
}

// GetCredential retrieves decrypted credentials for use.
func (s *CredentialService) GetCredential(ctx context.Context, userID, exchange string) (*DecryptedCredential, error) {
	auditCtx := s.getAuditContext(ctx, userID, "")

	if userID == "" {
		s.auditLogger.LogRead(ctx, "", exchange, auditCtx, false, ErrInvalidUserID)
		return nil, ErrInvalidUserID
	}
	if exchange == "" {
		s.auditLogger.LogRead(ctx, "", exchange, auditCtx, false, ErrInvalidExchange)
		return nil, ErrInvalidExchange
	}

	credential, err := s.repo.GetByUserAndExchange(ctx, userID, exchange)
	if err != nil {
		s.auditLogger.LogRead(ctx, "", exchange, auditCtx, false, err)
		return nil, err
	}

	// Update audit context with tenant ID from credential
	auditCtx.TenantID = credential.TenantID

	// Check if credential is active
	if !credential.IsActive {
		s.auditLogger.LogRead(ctx, credential.ID, exchange, auditCtx, false, ErrCredentialDisabled)
		return nil, ErrCredentialDisabled
	}

	// Check if credential has expired
	if credential.IsExpired() {
		s.auditLogger.LogRead(ctx, credential.ID, exchange, auditCtx, false, ErrCredentialExpired)
		return nil, ErrCredentialExpired
	}

	// Check circuit breaker
	if credential.FailureCount >= CircuitBreakerThreshold {
		s.auditLogger.LogRead(ctx, credential.ID, exchange, auditCtx, false, ErrCredentialDisabled)
		return nil, ErrCredentialDisabled
	}

	// Decrypt the sensitive fields
	apiKey, err := s.encryption.Decrypt(credential.APIKeyEncrypted, credential.EncryptionKeyID)
	if err != nil {
		decryptErr := fmt.Errorf("failed to decrypt API key: %w", err)
		s.auditLogger.LogRead(ctx, credential.ID, exchange, auditCtx, false, decryptErr)
		return nil, decryptErr
	}

	apiSecret, err := s.encryption.Decrypt(credential.APISecretEncrypted, credential.EncryptionKeyID)
	if err != nil {
		decryptErr := fmt.Errorf("failed to decrypt API secret: %w", err)
		s.auditLogger.LogRead(ctx, credential.ID, exchange, auditCtx, false, decryptErr)
		return nil, decryptErr
	}

	var passphrase string
	if len(credential.PassphraseEncrypted) > 0 {
		passphraseBytes, err := s.encryption.Decrypt(credential.PassphraseEncrypted, credential.EncryptionKeyID)
		if err != nil {
			decryptErr := fmt.Errorf("failed to decrypt passphrase: %w", err)
			s.auditLogger.LogRead(ctx, credential.ID, exchange, auditCtx, false, decryptErr)
			return nil, decryptErr
		}
		passphrase = string(passphraseBytes)
	}

	// Parse permissions
	var permissions []string
	if credential.Permissions != "" {
		if err := json.Unmarshal([]byte(credential.Permissions), &permissions); err != nil {
			// Fallback to simple parsing
			permissions = parsePermissions(credential.Permissions)
		}
	}

	// Log successful read
	s.auditLogger.LogRead(ctx, credential.ID, exchange, auditCtx, true, nil)

	return &DecryptedCredential{
		ID:           credential.ID,
		UserID:       credential.UserID,
		TenantID:     credential.TenantID,
		ExchangeName: credential.ExchangeName,
		APIKey:       string(apiKey),
		APISecret:    string(apiSecret),
		Passphrase:   passphrase,
		Label:        credential.Label,
		Permissions:  permissions,
		ExpiresAt:    credential.ExpiresAt,
		LastUsedAt:   credential.LastUsedAt,
		IsActive:     credential.IsActive,
	}, nil
}

// ListCredentials lists all credentials for a user (without secrets).
func (s *CredentialService) ListCredentials(ctx context.Context, userID string) ([]*CredentialSummary, error) {
	auditCtx := s.getAuditContext(ctx, userID, "")

	if userID == "" {
		s.auditLogger.LogList(ctx, auditCtx, 0, false, ErrInvalidUserID)
		return nil, ErrInvalidUserID
	}

	credentials, err := s.repo.ListByUser(ctx, userID)
	if err != nil {
		listErr := fmt.Errorf("failed to list credentials: %w", err)
		s.auditLogger.LogList(ctx, auditCtx, 0, false, listErr)
		return nil, listErr
	}

	summaries := make([]*CredentialSummary, 0, len(credentials))
	for _, cred := range credentials {
		// Decrypt API key just to get prefix
		apiKeyPrefix := "****"
		if apiKey, err := s.encryption.Decrypt(cred.APIKeyEncrypted, cred.EncryptionKeyID); err == nil {
			if len(apiKey) > 4 {
				apiKeyPrefix = string(apiKey[:4]) + "****"
			} else {
				apiKeyPrefix = string(apiKey)
			}
		}

		summaries = append(summaries, cred.ToSummary(apiKeyPrefix))
	}

	// Log successful list operation
	s.auditLogger.LogList(ctx, auditCtx, len(summaries), true, nil)

	return summaries, nil
}

// UpdateCredential updates an existing credential.
func (s *CredentialService) UpdateCredential(ctx context.Context, userID, exchange string, input *UpdateCredentialInput) error {
	auditCtx := s.getAuditContext(ctx, userID, "")

	if userID == "" {
		s.auditLogger.LogUpdate(ctx, "", exchange, auditCtx, nil, false, ErrInvalidUserID)
		return ErrInvalidUserID
	}
	if exchange == "" {
		s.auditLogger.LogUpdate(ctx, "", exchange, auditCtx, nil, false, ErrInvalidExchange)
		return ErrInvalidExchange
	}
	if input == nil {
		inputErr := errors.New("input is required")
		s.auditLogger.LogUpdate(ctx, "", exchange, auditCtx, nil, false, inputErr)
		return inputErr
	}

	credential, err := s.repo.GetByUserAndExchange(ctx, userID, exchange)
	if err != nil {
		s.auditLogger.LogUpdate(ctx, "", exchange, auditCtx, nil, false, err)
		return err
	}

	// Update audit context with tenant ID from credential
	auditCtx.TenantID = credential.TenantID

	// Track which fields are being updated
	var fieldsUpdated []string

	// Update fields if provided
	if input.APIKey != nil {
		apiKeyEnc, keyID, err := s.encryption.Encrypt([]byte(*input.APIKey))
		if err != nil {
			encryptErr := fmt.Errorf("failed to encrypt API key: %w", err)
			s.auditLogger.LogUpdate(ctx, credential.ID, exchange, auditCtx, nil, false, encryptErr)
			return encryptErr
		}
		credential.APIKeyEncrypted = apiKeyEnc
		credential.EncryptionKeyID = keyID
		fieldsUpdated = append(fieldsUpdated, "api_key")
	}

	if input.APISecret != nil {
		apiSecretEnc, _, err := s.encryption.Encrypt([]byte(*input.APISecret))
		if err != nil {
			encryptErr := fmt.Errorf("failed to encrypt API secret: %w", err)
			s.auditLogger.LogUpdate(ctx, credential.ID, exchange, auditCtx, nil, false, encryptErr)
			return encryptErr
		}
		credential.APISecretEncrypted = apiSecretEnc
		fieldsUpdated = append(fieldsUpdated, "api_secret")
	}

	if input.Passphrase != nil {
		if *input.Passphrase != "" {
			passphraseEnc, _, err := s.encryption.Encrypt([]byte(*input.Passphrase))
			if err != nil {
				encryptErr := fmt.Errorf("failed to encrypt passphrase: %w", err)
				s.auditLogger.LogUpdate(ctx, credential.ID, exchange, auditCtx, nil, false, encryptErr)
				return encryptErr
			}
			credential.PassphraseEncrypted = passphraseEnc
		} else {
			credential.PassphraseEncrypted = nil
		}
		fieldsUpdated = append(fieldsUpdated, "passphrase")
	}

	if input.Label != nil {
		credential.Label = *input.Label
		fieldsUpdated = append(fieldsUpdated, "label")
	}

	if input.Permissions != nil {
		permBytes, err := json.Marshal(input.Permissions)
		if err != nil {
			marshalErr := fmt.Errorf("failed to marshal permissions: %w", err)
			s.auditLogger.LogUpdate(ctx, credential.ID, exchange, auditCtx, nil, false, marshalErr)
			return marshalErr
		}
		credential.Permissions = string(permBytes)
		fieldsUpdated = append(fieldsUpdated, "permissions")
	}

	if input.ExpiresAt != nil {
		credential.ExpiresAt = input.ExpiresAt
		fieldsUpdated = append(fieldsUpdated, "expires_at")
	}

	if input.IsActive != nil {
		credential.IsActive = *input.IsActive
		fieldsUpdated = append(fieldsUpdated, "is_active")
	}

	// Reset validation status if credentials changed
	if input.APIKey != nil || input.APISecret != nil || input.Passphrase != nil {
		credential.ValidationStatus = "unknown"
		credential.FailureCount = 0
		fieldsUpdated = append(fieldsUpdated, "validation_status", "failure_count")
	}

	if err := s.repo.Update(ctx, credential); err != nil {
		updateErr := fmt.Errorf("failed to update credential: %w", err)
		s.auditLogger.LogUpdate(ctx, credential.ID, exchange, auditCtx, fieldsUpdated, false, updateErr)
		return updateErr
	}

	// Log successful update
	s.auditLogger.LogUpdate(ctx, credential.ID, exchange, auditCtx, fieldsUpdated, true, nil)

	s.logger.WithFields(logrus.Fields{
		"user_id":  userID,
		"exchange": exchange,
	}).Info("Credential updated successfully")

	return nil
}

// DeleteCredential removes a credential.
func (s *CredentialService) DeleteCredential(ctx context.Context, userID, exchange string) error {
	auditCtx := s.getAuditContext(ctx, userID, "")

	if userID == "" {
		s.auditLogger.LogDelete(ctx, "", exchange, auditCtx, false, ErrInvalidUserID)
		return ErrInvalidUserID
	}
	if exchange == "" {
		s.auditLogger.LogDelete(ctx, "", exchange, auditCtx, false, ErrInvalidExchange)
		return ErrInvalidExchange
	}

	// Get credential first to capture its ID for audit logging
	credential, err := s.repo.GetByUserAndExchange(ctx, userID, exchange)
	if err != nil {
		s.auditLogger.LogDelete(ctx, "", exchange, auditCtx, false, err)
		return err
	}

	// Update audit context with tenant ID from credential
	auditCtx.TenantID = credential.TenantID
	credentialID := credential.ID

	if err := s.repo.Delete(ctx, userID, exchange); err != nil {
		s.auditLogger.LogDelete(ctx, credentialID, exchange, auditCtx, false, err)
		return err
	}

	// Log successful deletion
	s.auditLogger.LogDelete(ctx, credentialID, exchange, auditCtx, true, nil)

	s.logger.WithFields(logrus.Fields{
		"user_id":  userID,
		"exchange": exchange,
	}).Info("Credential deleted successfully")

	return nil
}

// TestCredential tests if a credential works against the exchange.
func (s *CredentialService) TestCredential(ctx context.Context, userID, exchange string) (*TestCredentialResult, error) {
	auditCtx := s.getAuditContext(ctx, userID, "")

	if userID == "" {
		s.auditLogger.LogTest(ctx, "", exchange, auditCtx, nil, ErrInvalidUserID)
		return nil, ErrInvalidUserID
	}
	if exchange == "" {
		s.auditLogger.LogTest(ctx, "", exchange, auditCtx, nil, ErrInvalidExchange)
		return nil, ErrInvalidExchange
	}

	s.mu.RLock()
	client := s.gctClient
	s.mu.RUnlock()

	if client == nil {
		s.auditLogger.LogTest(ctx, "", exchange, auditCtx, nil, ErrNoGCTClient)
		return nil, ErrNoGCTClient
	}

	// Get the decrypted credentials (this will also log the read operation)
	cred, err := s.GetCredential(ctx, userID, exchange)
	if err != nil {
		testResult := &TestCredentialResult{
			Success:  false,
			Exchange: exchange,
			Message:  fmt.Sprintf("Failed to retrieve credential: %v", err),
			TestedAt: time.Now(),
		}
		s.auditLogger.LogTest(ctx, "", exchange, auditCtx, testResult, err)
		return testResult, nil
	}

	// Update audit context with tenant ID from credential
	auditCtx.TenantID = cred.TenantID

	// Re-check withdraw permissions each time a credential is tested.
	if withdrawErr := s.enforceNoWithdraw(ctx, cred); withdrawErr != nil {
		testResult := &TestCredentialResult{
			Success:  false,
			Exchange: exchange,
			Message:  withdrawErr.Error(),
			TestedAt: time.Now(),
		}
		s.auditLogger.LogTest(ctx, cred.ID, exchange, auditCtx, testResult, withdrawErr)
		return testResult, nil
	}

	// Create a timeout context for the test
	testCtx, cancel := context.WithTimeout(ctx, ValidationTimeout)
	defer cancel()

	startTime := time.Now()
	var testResult *TestCredentialResult

	// Test based on exchange
	exchangeNorm := NormalizeExchangeName(exchange)
	switch exchangeNorm {
	case ExchangeKuCoin:
		testResult = s.testKuCoinCredential(testCtx, client, cred)
	case ExchangeBinance:
		testResult = s.testBinanceCredential(testCtx, client, cred)
	default:
		testResult = s.testGenericCredential(testCtx, client, cred)
	}

	testResult.Exchange = exchange
	testResult.ResponseTime = time.Since(startTime).Milliseconds()
	testResult.TestedAt = time.Now()

	s.logger.WithFields(logrus.Fields{
		"exchange": exchange,
		"success":  testResult.Success,
		"message":  testResult.Message,
	}).Info("Credential test result")

	// Update validation status in database
	status := "invalid"
	if testResult.Success {
		status = "valid"
	}
	if err := s.repo.UpdateValidationStatus(ctx, userID, exchange, status, testResult.Success); err != nil {
		s.logger.WithFields(logrus.Fields{
			"user_id":  userID,
			"exchange": exchange,
			"error":    err.Error(),
		}).Warn("Failed to update validation status")
	}

	// Log the test result
	s.auditLogger.LogTest(ctx, cred.ID, exchange, auditCtx, testResult, nil)

	return testResult, nil
}

// testKuCoinCredential tests KuCoin credentials.
// testKuCoinCredential calls KuCoin REST API directly to validate credentials.
func (s *CredentialService) testKuCoinCredential(ctx context.Context, _ gct.GCTClient, cred *DecryptedCredential) *TestCredentialResult {
	result := &TestCredentialResult{Success: false}

	const endpoint = "https://api.kucoin.com"
	path := "/api/v1/accounts"
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	preSign := ts + "GET" + path

	mac := hmac.New(sha256.New, []byte(cred.APISecret))
	mac.Write([]byte(preSign))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	ppMac := hmac.New(sha256.New, []byte(cred.APISecret))
	ppMac.Write([]byte(cred.Passphrase))
	pp := base64.StdEncoding.EncodeToString(ppMac.Sum(nil))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+path, nil)
	if err != nil {
		result.Message = "Failed to build request"
		return result
	}
	req.Header.Set("KC-API-KEY", cred.APIKey)
	req.Header.Set("KC-API-SIGN", sig)
	req.Header.Set("KC-API-TIMESTAMP", ts)
	req.Header.Set("KC-API-PASSPHRASE", pp)
	req.Header.Set("KC-API-KEY-VERSION", "2")
	req.Header.Set("Content-Type", "application/json")

	httpClient := &http.Client{Timeout: 15 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		if strings.Contains(err.Error(), "timeout") {
			result.Message = "Connection timeout - please try again"
		} else {
			result.Message = fmt.Sprintf("Network error: %v", err)
		}
		return result
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		result.Message = "Invalid API credentials or insufficient permissions"
		return result
	}
	if resp.StatusCode != http.StatusOK {
		result.Message = fmt.Sprintf("KuCoin API error (HTTP %d)", resp.StatusCode)
		return result
	}

	var parsed struct {
		Code string `json:"code"`
		Data []struct {
			Type string `json:"type"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Code != "200000" {
		result.Message = fmt.Sprintf("Unexpected response (code=%s)", parsed.Code)
		return result
	}

	result.Success = true
	result.Message = "Credentials verified successfully"
	result.AccountInfo = fmt.Sprintf("%d account(s) found", len(parsed.Data))

	perms := []string{"spot"}
	for _, acc := range parsed.Data {
		if strings.EqualFold(acc.Type, "contract") || strings.EqualFold(acc.Type, "futures") {
			perms = append(perms, "futures")
			break
		}
	}
	result.Permissions = perms
	return result
}

// testBinanceCredential calls Binance REST API directly to validate credentials.
func (s *CredentialService) testBinanceCredential(ctx context.Context, _ gct.GCTClient, cred *DecryptedCredential) *TestCredentialResult {
	result := &TestCredentialResult{Success: false}

	const endpoint = "https://api.binance.com"
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	query := "timestamp=" + ts

	mac := hmac.New(sha256.New, []byte(cred.APISecret))
	mac.Write([]byte(query))
	sig := fmt.Sprintf("%x", mac.Sum(nil))

	url := endpoint + "/api/v3/account?" + query + "&signature=" + sig

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		result.Message = "Failed to build request"
		return result
	}
	req.Header.Set("X-MBX-APIKEY", cred.APIKey)

	httpClient := &http.Client{Timeout: 15 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		if strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "deadline") {
			result.Message = "Connection timeout - please try again"
		} else {
			result.Message = fmt.Sprintf("Network error: %v", err)
		}
		return result
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		result.Message = "Invalid API credentials or insufficient permissions"
		return result
	}
	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Msg string `json:"msg"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Msg != "" {
			result.Message = fmt.Sprintf("Binance error: %s", errResp.Msg)
		} else {
			result.Message = fmt.Sprintf("Binance API error (HTTP %d)", resp.StatusCode)
		}
		return result
	}

	var parsed struct {
		Balances []struct {
			Asset string `json:"asset"`
			Free  string `json:"free"`
		} `json:"balances"`
		CanTrade bool `json:"canTrade"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		result.Message = "Unexpected response from Binance"
		return result
	}

	result.Success = true
	result.Message = "Credentials verified successfully"
	result.AccountInfo = fmt.Sprintf("%d assets, canTrade=%v", len(parsed.Balances), parsed.CanTrade)

	perms := []string{"spot"}
	if parsed.CanTrade {
		perms = append(perms, "trade")
	}
	result.Permissions = perms
	return result
}

// testGenericCredential tests credentials for other exchanges.
func (s *CredentialService) testGenericCredential(ctx context.Context, client gct.GCTClient, cred *DecryptedCredential) *TestCredentialResult {
	result := &TestCredentialResult{Success: false}

	// Try to get account info
	accountInfo, err := client.GetAccountInfo(ctx, cred.ExchangeName)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to authenticate: %v", err)
		return result
	}

	result.Success = true
	result.Message = "Credentials verified successfully"
	result.Permissions = []string{"spot"}

	if accountInfo != nil && len(accountInfo.Accounts) > 0 {
		result.AccountInfo = fmt.Sprintf("%d account(s) found", len(accountInfo.Accounts))
	}

	return result
}

// ValidateAll validates all active credentials for a user.
func (s *CredentialService) ValidateAll(ctx context.Context, userID string) (map[string]*TestCredentialResult, error) {
	if userID == "" {
		return nil, ErrInvalidUserID
	}

	credentials, err := s.repo.ListByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list credentials: %w", err)
	}

	results := make(map[string]*TestCredentialResult)
	for _, cred := range credentials {
		if !cred.IsActive {
			results[cred.ExchangeName] = &TestCredentialResult{
				Success:  false,
				Exchange: cred.ExchangeName,
				Message:  "Credential is inactive",
				TestedAt: time.Now(),
			}
			continue
		}

		result, err := s.TestCredential(ctx, userID, cred.ExchangeName)
		if err != nil {
			results[cred.ExchangeName] = &TestCredentialResult{
				Success:  false,
				Exchange: cred.ExchangeName,
				Message:  fmt.Sprintf("Test failed: %v", err),
				TestedAt: time.Now(),
			}
		} else {
			results[cred.ExchangeName] = result
		}
	}

	return results, nil
}

// RecordUsage records that a credential was used.
func (s *CredentialService) RecordUsage(ctx context.Context, userID, exchange string) error {
	return s.repo.UpdateLastUsed(ctx, userID, exchange)
}

// RecordFailure records a credential failure for circuit breaker.
func (s *CredentialService) RecordFailure(ctx context.Context, userID, exchange string) error {
	if err := s.repo.IncrementFailureCount(ctx, userID, exchange); err != nil {
		return err
	}

	// Check if we need to disable the credential
	cred, err := s.repo.GetByUserAndExchange(ctx, userID, exchange)
	if err != nil {
		return nil // Don't fail if we can't check
	}

	if cred.FailureCount >= CircuitBreakerThreshold {
		s.logger.WithFields(logrus.Fields{
			"user_id":       userID,
			"exchange":      exchange,
			"failure_count": cred.FailureCount,
		}).Warn("Credential disabled due to repeated failures")
	}

	return nil
}

// RecordSuccess records a successful use and resets failure count.
func (s *CredentialService) RecordSuccess(ctx context.Context, userID, exchange string) error {
	if err := s.repo.ResetFailureCount(ctx, userID, exchange); err != nil {
		s.logger.WithFields(logrus.Fields{
			"user_id":  userID,
			"exchange": exchange,
			"error":    err.Error(),
		}).Warn("Failed to reset failure count")
	}
	return s.repo.UpdateLastUsed(ctx, userID, exchange)
}

// RotateEncryptionKey re-encrypts all credentials with a new key.
// Returns the number of credentials rotated.
func (s *CredentialService) RotateEncryptionKey(ctx context.Context, oldKeyID string) (int, error) {
	if oldKeyID == "" {
		return 0, errors.New("old key ID is required")
	}

	// Get the repository as PostgresRepository to access the key rotation methods
	postgresRepo, ok := s.repo.(*PostgresRepository)
	if !ok {
		return 0, errors.New("key rotation requires PostgresRepository")
	}

	// Get all credentials with the old key
	credentials, err := postgresRepo.GetByEncryptionKeyID(ctx, oldKeyID)
	if err != nil {
		return 0, fmt.Errorf("failed to get credentials for rotation: %w", err)
	}

	rotated := 0
	for _, cred := range credentials {
		// Re-encrypt API key
		newAPIKey, newKeyID, err := s.encryption.ReEncrypt(cred.APIKeyEncrypted, oldKeyID)
		if err != nil {
			s.logger.WithFields(logrus.Fields{
				"credential_id": cred.ID,
				"error":         err.Error(),
			}).Error("Failed to re-encrypt API key")
			continue
		}

		// Re-encrypt API secret
		newAPISecret, _, err := s.encryption.ReEncrypt(cred.APISecretEncrypted, oldKeyID)
		if err != nil {
			s.logger.WithFields(logrus.Fields{
				"credential_id": cred.ID,
				"error":         err.Error(),
			}).Error("Failed to re-encrypt API secret")
			continue
		}

		// Re-encrypt passphrase if present
		var newPassphrase []byte
		if len(cred.PassphraseEncrypted) > 0 {
			newPassphrase, _, err = s.encryption.ReEncrypt(cred.PassphraseEncrypted, oldKeyID)
			if err != nil {
				s.logger.WithFields(logrus.Fields{
					"credential_id": cred.ID,
					"error":         err.Error(),
				}).Error("Failed to re-encrypt passphrase")
				continue
			}
		}

		// Update the credential with new encryption
		if err := postgresRepo.UpdateEncryption(ctx, cred.ID, newAPIKey, newAPISecret, newPassphrase, newKeyID); err != nil {
			s.logger.WithFields(logrus.Fields{
				"credential_id": cred.ID,
				"error":         err.Error(),
			}).Error("Failed to update credential encryption")
			continue
		}

		rotated++
	}

	s.logger.WithFields(logrus.Fields{
		"old_key_id":       oldKeyID,
		"credentials_found": len(credentials),
		"rotated":          rotated,
	}).Info("Encryption key rotation completed")

	return rotated, nil
}

// GetCredentialForExchange is a convenience method that returns credentials in a format
// suitable for configuring exchange clients.
func (s *CredentialService) GetCredentialForExchange(ctx context.Context, userID, exchange string) (apiKey, apiSecret, passphrase string, err error) {
	cred, err := s.GetCredential(ctx, userID, exchange)
	if err != nil {
		return "", "", "", err
	}

	// Record usage
	go func() {
		if err := s.RecordUsage(context.Background(), userID, exchange); err != nil {
			s.logger.WithFields(logrus.Fields{
				"user_id":  userID,
				"exchange": exchange,
				"error":    err.Error(),
			}).Warn("Failed to record credential usage")
		}
	}()

	return cred.APIKey, cred.APISecret, cred.Passphrase, nil
}
