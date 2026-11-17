// Package credentials provides secure storage and management of user exchange API credentials
// for the trading-mcp SaaS platform.
package credentials

import (
	"errors"
	"strings"
	"time"
)

// Supported exchanges for the credential vault
const (
	ExchangeKuCoin   = "kucoin"
	ExchangeBinance  = "binance"
	ExchangeCoinbase = "coinbase"
	ExchangeKraken   = "kraken"
	ExchangeBybit    = "bybit"
	ExchangeOKX      = "okx"
	ExchangeBitget   = "bitget"
)

// UserCredential represents encrypted exchange API credentials for a user.
// All sensitive fields (API key, secret, passphrase) are stored encrypted at rest.
type UserCredential struct {
	// ID is the unique identifier for this credential record
	ID string `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`

	// UserID is the ID of the user who owns these credentials
	UserID string `json:"user_id" gorm:"index:idx_user_exchange,unique;not null"`

	// TenantID is the tenant ID for multi-tenant isolation
	TenantID string `json:"tenant_id" gorm:"index;not null"`

	// ExchangeName is the name of the exchange (e.g., "kucoin", "binance")
	ExchangeName string `json:"exchange_name" gorm:"index:idx_user_exchange,unique;not null"`

	// APIKeyEncrypted is the AES-256-GCM encrypted API key
	APIKeyEncrypted []byte `json:"-" gorm:"not null"`

	// APISecretEncrypted is the AES-256-GCM encrypted API secret
	APISecretEncrypted []byte `json:"-" gorm:"not null"`

	// PassphraseEncrypted is the AES-256-GCM encrypted passphrase (optional, used by some exchanges like KuCoin)
	PassphraseEncrypted []byte `json:"-"`

	// EncryptionKeyID identifies which encryption key version was used
	// This supports key rotation without re-encrypting all credentials immediately
	EncryptionKeyID string `json:"encryption_key_id" gorm:"not null"`

	// CreatedAt is the timestamp when the credential was created
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`

	// UpdatedAt is the timestamp when the credential was last updated
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	// ExpiresAt is an optional expiration date for the credentials
	ExpiresAt *time.Time `json:"expires_at,omitempty"`

	// LastUsedAt is the timestamp when the credential was last used for API calls
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`

	// IsActive indicates whether the credential is currently active
	IsActive bool `json:"is_active" gorm:"default:true"`

	// Label is an optional user-defined label for the credential
	Label string `json:"label,omitempty" gorm:"size:255"`

	// Permissions stores the API key permissions as a JSON array (e.g., ["spot", "futures"])
	Permissions string `json:"permissions,omitempty" gorm:"type:text"`

	// LastValidatedAt is the timestamp when the credential was last validated against the exchange
	LastValidatedAt *time.Time `json:"last_validated_at,omitempty"`

	// ValidationStatus is the result of the last validation ("valid", "invalid", "unknown")
	ValidationStatus string `json:"validation_status,omitempty" gorm:"size:50;default:'unknown'"`

	// FailureCount tracks consecutive API failures for circuit breaker logic
	FailureCount int `json:"failure_count" gorm:"default:0"`
}

// TableName returns the table name for GORM
func (UserCredential) TableName() string {
	return "user_credentials"
}

// DecryptedCredential represents the decrypted credentials returned to authorized callers.
// This struct is never persisted to the database.
type DecryptedCredential struct {
	ID           string     `json:"id"`
	UserID       string     `json:"user_id"`
	TenantID     string     `json:"tenant_id"`
	ExchangeName string     `json:"exchange_name"`
	APIKey       string     `json:"api_key"`
	APISecret    string     `json:"api_secret"`
	Passphrase   string     `json:"passphrase,omitempty"`
	Label        string     `json:"label,omitempty"`
	Permissions  []string   `json:"permissions,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty"`
	IsActive     bool       `json:"is_active"`
}

// CredentialSummary represents a credential without sensitive data, suitable for listing.
type CredentialSummary struct {
	ID               string     `json:"id"`
	UserID           string     `json:"user_id"`
	TenantID         string     `json:"tenant_id"`
	ExchangeName     string     `json:"exchange_name"`
	Label            string     `json:"label,omitempty"`
	Permissions      []string   `json:"permissions,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	LastUsedAt       *time.Time `json:"last_used_at,omitempty"`
	IsActive         bool       `json:"is_active"`
	ValidationStatus string     `json:"validation_status"`
	APIKeyPrefix     string     `json:"api_key_prefix"` // First 4 characters for identification
	HasPassphrase    bool       `json:"has_passphrase"` // Whether passphrase is set
}

// CreateCredentialInput represents the input for creating a new credential.
type CreateCredentialInput struct {
	UserID       string     `json:"user_id" validate:"required"`
	TenantID     string     `json:"tenant_id" validate:"required"`
	ExchangeName string     `json:"exchange_name" validate:"required"`
	APIKey       string     `json:"api_key" validate:"required"`
	APISecret    string     `json:"api_secret" validate:"required"`
	Passphrase   string     `json:"passphrase,omitempty"`
	Label        string     `json:"label,omitempty" validate:"max=255"`
	Permissions  []string   `json:"permissions,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
}

// Validate validates the CreateCredentialInput
func (c *CreateCredentialInput) Validate() error {
	if c.UserID == "" {
		return errors.New("user_id is required")
	}
	if c.TenantID == "" {
		return errors.New("tenant_id is required")
	}
	if c.ExchangeName == "" {
		return errors.New("exchange_name is required")
	}
	if !IsValidExchange(c.ExchangeName) {
		return errors.New("invalid exchange_name: must be one of kucoin, binance, coinbase, kraken, bybit, okx, bitget")
	}
	if c.APIKey == "" {
		return errors.New("api_key is required")
	}
	if c.APISecret == "" {
		return errors.New("api_secret is required")
	}
	// Some exchanges like KuCoin require a passphrase
	if RequiresPassphrase(c.ExchangeName) && c.Passphrase == "" {
		return errors.New("passphrase is required for " + c.ExchangeName)
	}
	if c.ExpiresAt != nil && c.ExpiresAt.Before(time.Now()) {
		return errors.New("expires_at must be in the future")
	}
	return nil
}

// UpdateCredentialInput represents the input for updating an existing credential.
type UpdateCredentialInput struct {
	APIKey      *string    `json:"api_key,omitempty"`
	APISecret   *string    `json:"api_secret,omitempty"`
	Passphrase  *string    `json:"passphrase,omitempty"`
	Label       *string    `json:"label,omitempty"`
	Permissions []string   `json:"permissions,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	IsActive    *bool      `json:"is_active,omitempty"`
}

// TestCredentialResult represents the result of testing a credential against an exchange.
type TestCredentialResult struct {
	Success      bool      `json:"success"`
	Exchange     string    `json:"exchange"`
	Message      string    `json:"message"`
	Permissions  []string  `json:"permissions,omitempty"`
	AccountInfo  string    `json:"account_info,omitempty"`
	ResponseTime int64     `json:"response_time_ms"`
	TestedAt     time.Time `json:"tested_at"`
}

// IsValidExchange checks if the exchange name is supported.
func IsValidExchange(exchange string) bool {
	normalized := strings.ToLower(strings.TrimSpace(exchange))
	switch normalized {
	case ExchangeKuCoin, ExchangeBinance, ExchangeCoinbase, ExchangeKraken, ExchangeBybit, ExchangeOKX, ExchangeBitget:
		return true
	default:
		return false
	}
}

// NormalizeExchangeName normalizes the exchange name to lowercase.
func NormalizeExchangeName(exchange string) string {
	return strings.ToLower(strings.TrimSpace(exchange))
}

// RequiresPassphrase returns true if the exchange requires a passphrase.
func RequiresPassphrase(exchange string) bool {
	normalized := NormalizeExchangeName(exchange)
	switch normalized {
	case ExchangeKuCoin, ExchangeCoinbase, ExchangeOKX, ExchangeBitget:
		return true
	default:
		return false
	}
}

// ToSummary converts a UserCredential to a CredentialSummary (without decrypting secrets).
func (c *UserCredential) ToSummary(apiKeyPrefix string) *CredentialSummary {
	var permissions []string
	if c.Permissions != "" {
		// Simple JSON array parsing - in production you'd use json.Unmarshal
		permissions = parsePermissions(c.Permissions)
	}

	return &CredentialSummary{
		ID:               c.ID,
		UserID:           c.UserID,
		TenantID:         c.TenantID,
		ExchangeName:     c.ExchangeName,
		Label:            c.Label,
		Permissions:      permissions,
		CreatedAt:        c.CreatedAt,
		UpdatedAt:        c.UpdatedAt,
		ExpiresAt:        c.ExpiresAt,
		LastUsedAt:       c.LastUsedAt,
		IsActive:         c.IsActive,
		ValidationStatus: c.ValidationStatus,
		APIKeyPrefix:     apiKeyPrefix,
		HasPassphrase:    len(c.PassphraseEncrypted) > 0,
	}
}

// IsExpired returns true if the credential has expired.
func (c *UserCredential) IsExpired() bool {
	if c.ExpiresAt == nil {
		return false
	}
	return c.ExpiresAt.Before(time.Now())
}

// parsePermissions parses a JSON array string into a slice of strings.
func parsePermissions(permissions string) []string {
	// Simple parsing - remove brackets and split by comma
	// In production, use json.Unmarshal
	permissions = strings.TrimPrefix(permissions, "[")
	permissions = strings.TrimSuffix(permissions, "]")
	permissions = strings.TrimSpace(permissions)

	if permissions == "" {
		return nil
	}

	parts := strings.Split(permissions, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, `"'`)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
