package credentials

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	// ErrCredentialNotFound is returned when a credential is not found
	ErrCredentialNotFound = errors.New("credential not found")

	// ErrDuplicateCredential is returned when trying to create a duplicate credential
	ErrDuplicateCredential = errors.New("credential already exists for this user and exchange")

	// ErrInvalidUserID is returned when the user ID is empty
	ErrInvalidUserID = errors.New("user ID is required")

	// ErrInvalidExchange is returned when the exchange name is empty or invalid
	ErrInvalidExchange = errors.New("exchange name is required and must be valid")
)

// Repository defines the interface for credential storage operations.
type Repository interface {
	// Create stores a new credential in the database
	Create(ctx context.Context, credential *UserCredential) error

	// GetByID retrieves a credential by its ID
	GetByID(ctx context.Context, id string) (*UserCredential, error)

	// GetByUserAndExchange retrieves a credential for a specific user and exchange
	GetByUserAndExchange(ctx context.Context, userID, exchange string) (*UserCredential, error)

	// ListByUser retrieves all credentials for a user
	ListByUser(ctx context.Context, userID string) ([]UserCredential, error)

	// ListByTenant retrieves all credentials for a tenant
	ListByTenant(ctx context.Context, tenantID string) ([]UserCredential, error)

	// Update updates an existing credential
	Update(ctx context.Context, credential *UserCredential) error

	// Delete removes a credential by user and exchange
	Delete(ctx context.Context, userID, exchange string) error

	// DeleteByID removes a credential by its ID
	DeleteByID(ctx context.Context, id string) error

	// UpdateLastUsed updates the last used timestamp for a credential
	UpdateLastUsed(ctx context.Context, userID, exchange string) error

	// UpdateValidationStatus updates the validation status and timestamp
	UpdateValidationStatus(ctx context.Context, userID, exchange string, status string, success bool) error

	// IncrementFailureCount increments the failure counter for circuit breaker logic
	IncrementFailureCount(ctx context.Context, userID, exchange string) error

	// ResetFailureCount resets the failure counter after a successful operation
	ResetFailureCount(ctx context.Context, userID, exchange string) error

	// ListExpiring retrieves credentials that will expire within the given duration
	ListExpiring(ctx context.Context, within time.Duration) ([]UserCredential, error)

	// ListInactive retrieves credentials that haven't been used for the given duration
	ListInactive(ctx context.Context, unusedFor time.Duration) ([]UserCredential, error)

	// CountByUser returns the number of credentials for a user
	CountByUser(ctx context.Context, userID string) (int64, error)

	// CountByTenant returns the number of credentials for a tenant
	CountByTenant(ctx context.Context, tenantID string) (int64, error)
}

// PostgresRepository implements Repository using PostgreSQL with GORM.
type PostgresRepository struct {
	db *gorm.DB
}

// NewPostgresRepository creates a new PostgresRepository.
func NewPostgresRepository(db *gorm.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

// Migrate runs the database migrations for the credential tables.
// Uses raw SQL that is compatible with both SQLite and PostgreSQL.
func (r *PostgresRepository) Migrate() error {
	sql := `CREATE TABLE IF NOT EXISTS user_credentials (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		tenant_id TEXT NOT NULL DEFAULT '',
		exchange_name TEXT NOT NULL,
		api_key_encrypted BLOB NOT NULL,
		api_secret_encrypted BLOB NOT NULL,
		passphrase_encrypted BLOB,
		encryption_key_id TEXT NOT NULL,
		created_at DATETIME,
		updated_at DATETIME,
		expires_at DATETIME,
		last_used_at DATETIME,
		is_active INTEGER NOT NULL DEFAULT 1,
		label TEXT,
		permissions TEXT,
		last_validated_at DATETIME,
		validation_status TEXT DEFAULT 'unknown',
		failure_count INTEGER NOT NULL DEFAULT 0,
		UNIQUE(user_id, exchange_name)
	)`
	return r.db.Exec(sql).Error
}

// Create stores a new credential in the database.
func (r *PostgresRepository) Create(ctx context.Context, credential *UserCredential) error {
	if credential == nil {
		return errors.New("credential cannot be nil")
	}
	if credential.UserID == "" {
		return ErrInvalidUserID
	}
	if credential.ExchangeName == "" {
		return ErrInvalidExchange
	}

	// Generate UUID if not provided (for SQLite compatibility and explicit ID generation)
	if credential.ID == "" {
		credential.ID = uuid.New().String()
	}

	// Normalize exchange name
	credential.ExchangeName = NormalizeExchangeName(credential.ExchangeName)

	// Check for existing credential
	var existing UserCredential
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND exchange_name = ?", credential.UserID, credential.ExchangeName).
		First(&existing).Error

	if err == nil {
		return ErrDuplicateCredential
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("failed to check for existing credential: %w", err)
	}

	// Create the credential
	if err := r.db.WithContext(ctx).Create(credential).Error; err != nil {
		return fmt.Errorf("failed to create credential: %w", err)
	}

	return nil
}

// GetByID retrieves a credential by its ID.
func (r *PostgresRepository) GetByID(ctx context.Context, id string) (*UserCredential, error) {
	if id == "" {
		return nil, errors.New("id is required")
	}

	var credential UserCredential
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&credential).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrCredentialNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get credential by ID: %w", err)
	}

	return &credential, nil
}

// GetByUserAndExchange retrieves a credential for a specific user and exchange.
func (r *PostgresRepository) GetByUserAndExchange(ctx context.Context, userID, exchange string) (*UserCredential, error) {
	if userID == "" {
		return nil, ErrInvalidUserID
	}
	if exchange == "" {
		return nil, ErrInvalidExchange
	}

	exchange = NormalizeExchangeName(exchange)

	var credential UserCredential
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND exchange_name = ?", userID, exchange).
		First(&credential).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrCredentialNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get credential: %w", err)
	}

	return &credential, nil
}

// ListByUser retrieves all credentials for a user.
func (r *PostgresRepository) ListByUser(ctx context.Context, userID string) ([]UserCredential, error) {
	if userID == "" {
		return nil, ErrInvalidUserID
	}

	var credentials []UserCredential
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("exchange_name ASC").
		Find(&credentials).Error

	if err != nil {
		return nil, fmt.Errorf("failed to list credentials: %w", err)
	}

	return credentials, nil
}

// ListByTenant retrieves all credentials for a tenant.
func (r *PostgresRepository) ListByTenant(ctx context.Context, tenantID string) ([]UserCredential, error) {
	if tenantID == "" {
		return nil, errors.New("tenant ID is required")
	}

	var credentials []UserCredential
	err := r.db.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Order("user_id ASC, exchange_name ASC").
		Find(&credentials).Error

	if err != nil {
		return nil, fmt.Errorf("failed to list credentials by tenant: %w", err)
	}

	return credentials, nil
}

// Update updates an existing credential.
func (r *PostgresRepository) Update(ctx context.Context, credential *UserCredential) error {
	if credential == nil {
		return errors.New("credential cannot be nil")
	}
	if credential.ID == "" {
		return errors.New("credential ID is required for update")
	}

	// Use Select to explicitly include all fields we want to update,
	// including boolean fields that may be false (GORM ignores zero values by default)
	result := r.db.WithContext(ctx).
		Model(credential).
		Select("*").
		Omit("id", "created_at"). // Don't update these
		Updates(credential)
	if result.Error != nil {
		return fmt.Errorf("failed to update credential: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrCredentialNotFound
	}

	return nil
}

// Delete removes a credential by user and exchange.
func (r *PostgresRepository) Delete(ctx context.Context, userID, exchange string) error {
	if userID == "" {
		return ErrInvalidUserID
	}
	if exchange == "" {
		return ErrInvalidExchange
	}

	exchange = NormalizeExchangeName(exchange)

	result := r.db.WithContext(ctx).
		Where("user_id = ? AND exchange_name = ?", userID, exchange).
		Delete(&UserCredential{})

	if result.Error != nil {
		return fmt.Errorf("failed to delete credential: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrCredentialNotFound
	}

	return nil
}

// DeleteByID removes a credential by its ID.
func (r *PostgresRepository) DeleteByID(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("id is required")
	}

	result := r.db.WithContext(ctx).Where("id = ?", id).Delete(&UserCredential{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete credential: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrCredentialNotFound
	}

	return nil
}

// UpdateLastUsed updates the last used timestamp for a credential.
func (r *PostgresRepository) UpdateLastUsed(ctx context.Context, userID, exchange string) error {
	if userID == "" {
		return ErrInvalidUserID
	}
	if exchange == "" {
		return ErrInvalidExchange
	}

	exchange = NormalizeExchangeName(exchange)
	now := time.Now()

	result := r.db.WithContext(ctx).
		Model(&UserCredential{}).
		Where("user_id = ? AND exchange_name = ?", userID, exchange).
		Update("last_used_at", now)

	if result.Error != nil {
		return fmt.Errorf("failed to update last used: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrCredentialNotFound
	}

	return nil
}

// UpdateValidationStatus updates the validation status and timestamp.
func (r *PostgresRepository) UpdateValidationStatus(ctx context.Context, userID, exchange string, status string, success bool) error {
	if userID == "" {
		return ErrInvalidUserID
	}
	if exchange == "" {
		return ErrInvalidExchange
	}

	exchange = NormalizeExchangeName(exchange)
	now := time.Now()

	updates := map[string]interface{}{
		"validation_status":  status,
		"last_validated_at":  now,
		"updated_at":         now,
	}

	// Reset failure count on successful validation
	if success {
		updates["failure_count"] = 0
	}

	result := r.db.WithContext(ctx).
		Model(&UserCredential{}).
		Where("user_id = ? AND exchange_name = ?", userID, exchange).
		Updates(updates)

	if result.Error != nil {
		return fmt.Errorf("failed to update validation status: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrCredentialNotFound
	}

	return nil
}

// IncrementFailureCount increments the failure counter for circuit breaker logic.
func (r *PostgresRepository) IncrementFailureCount(ctx context.Context, userID, exchange string) error {
	if userID == "" {
		return ErrInvalidUserID
	}
	if exchange == "" {
		return ErrInvalidExchange
	}

	exchange = NormalizeExchangeName(exchange)

	result := r.db.WithContext(ctx).
		Model(&UserCredential{}).
		Where("user_id = ? AND exchange_name = ?", userID, exchange).
		UpdateColumn("failure_count", gorm.Expr("failure_count + 1"))

	if result.Error != nil {
		return fmt.Errorf("failed to increment failure count: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrCredentialNotFound
	}

	return nil
}

// ResetFailureCount resets the failure counter after a successful operation.
func (r *PostgresRepository) ResetFailureCount(ctx context.Context, userID, exchange string) error {
	if userID == "" {
		return ErrInvalidUserID
	}
	if exchange == "" {
		return ErrInvalidExchange
	}

	exchange = NormalizeExchangeName(exchange)

	result := r.db.WithContext(ctx).
		Model(&UserCredential{}).
		Where("user_id = ? AND exchange_name = ?", userID, exchange).
		Update("failure_count", 0)

	if result.Error != nil {
		return fmt.Errorf("failed to reset failure count: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrCredentialNotFound
	}

	return nil
}

// ListExpiring retrieves credentials that will expire within the given duration.
func (r *PostgresRepository) ListExpiring(ctx context.Context, within time.Duration) ([]UserCredential, error) {
	cutoff := time.Now().Add(within)

	var credentials []UserCredential
	err := r.db.WithContext(ctx).
		Where("expires_at IS NOT NULL AND expires_at <= ? AND is_active = ?", cutoff, true).
		Order("expires_at ASC").
		Find(&credentials).Error

	if err != nil {
		return nil, fmt.Errorf("failed to list expiring credentials: %w", err)
	}

	return credentials, nil
}

// ListInactive retrieves credentials that haven't been used for the given duration.
func (r *PostgresRepository) ListInactive(ctx context.Context, unusedFor time.Duration) ([]UserCredential, error) {
	cutoff := time.Now().Add(-unusedFor)

	var credentials []UserCredential
	err := r.db.WithContext(ctx).
		Where("(last_used_at IS NULL OR last_used_at <= ?) AND is_active = ?", cutoff, true).
		Order("last_used_at ASC NULLS FIRST").
		Find(&credentials).Error

	if err != nil {
		return nil, fmt.Errorf("failed to list inactive credentials: %w", err)
	}

	return credentials, nil
}

// CountByUser returns the number of credentials for a user.
func (r *PostgresRepository) CountByUser(ctx context.Context, userID string) (int64, error) {
	if userID == "" {
		return 0, ErrInvalidUserID
	}

	var count int64
	err := r.db.WithContext(ctx).
		Model(&UserCredential{}).
		Where("user_id = ?", userID).
		Count(&count).Error

	if err != nil {
		return 0, fmt.Errorf("failed to count credentials: %w", err)
	}

	return count, nil
}

// CountByTenant returns the number of credentials for a tenant.
func (r *PostgresRepository) CountByTenant(ctx context.Context, tenantID string) (int64, error) {
	if tenantID == "" {
		return 0, errors.New("tenant ID is required")
	}

	var count int64
	err := r.db.WithContext(ctx).
		Model(&UserCredential{}).
		Where("tenant_id = ?", tenantID).
		Count(&count).Error

	if err != nil {
		return 0, fmt.Errorf("failed to count credentials: %w", err)
	}

	return count, nil
}

// DeactivateExpired deactivates all expired credentials.
// Returns the number of credentials deactivated.
func (r *PostgresRepository) DeactivateExpired(ctx context.Context) (int64, error) {
	now := time.Now()

	result := r.db.WithContext(ctx).
		Model(&UserCredential{}).
		Where("expires_at IS NOT NULL AND expires_at <= ? AND is_active = ?", now, true).
		Update("is_active", false)

	if result.Error != nil {
		return 0, fmt.Errorf("failed to deactivate expired credentials: %w", result.Error)
	}

	return result.RowsAffected, nil
}

// GetByEncryptionKeyID retrieves all credentials encrypted with a specific key.
// This is useful for key rotation operations.
func (r *PostgresRepository) GetByEncryptionKeyID(ctx context.Context, keyID string) ([]UserCredential, error) {
	if keyID == "" {
		return nil, errors.New("key ID is required")
	}

	var credentials []UserCredential
	err := r.db.WithContext(ctx).
		Where("encryption_key_id = ?", keyID).
		Find(&credentials).Error

	if err != nil {
		return nil, fmt.Errorf("failed to get credentials by encryption key ID: %w", err)
	}

	return credentials, nil
}

// UpdateEncryption updates the encrypted fields and key ID for a credential.
// Used during key rotation.
func (r *PostgresRepository) UpdateEncryption(ctx context.Context, id string, apiKeyEnc, apiSecretEnc, passphraseEnc []byte, newKeyID string) error {
	if id == "" {
		return errors.New("id is required")
	}
	if newKeyID == "" {
		return errors.New("new key ID is required")
	}

	updates := map[string]interface{}{
		"api_key_encrypted":    apiKeyEnc,
		"api_secret_encrypted": apiSecretEnc,
		"encryption_key_id":    newKeyID,
		"updated_at":           time.Now(),
	}

	if passphraseEnc != nil {
		updates["passphrase_encrypted"] = passphraseEnc
	}

	result := r.db.WithContext(ctx).
		Model(&UserCredential{}).
		Where("id = ?", id).
		Updates(updates)

	if result.Error != nil {
		return fmt.Errorf("failed to update encryption: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrCredentialNotFound
	}

	return nil
}
