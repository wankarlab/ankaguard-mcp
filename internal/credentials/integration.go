// Package credentials provides secure storage and management of user exchange API credentials.
package credentials

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// Integration provides a complete setup for the credential vault system.
// It manages all components: repository, encryption, service, and provider.
type Integration struct {
	Repository         *PostgresRepository
	EncryptionService  *EncryptionService
	CredentialService  *CredentialService
	CredentialProvider *CredentialProviderImpl
	Logger             *logrus.Logger
}

// IntegrationConfig holds configuration for setting up the credential integration.
type IntegrationConfig struct {
	// DB is the GORM database connection (required)
	DB *gorm.DB

	// MasterKey is the base64-encoded master encryption key (required)
	// Must be 32 bytes when decoded for AES-256
	MasterKeyBase64 string

	// Logger is optional; if nil, a default logger is created
	Logger *logrus.Logger

	// EnableCache enables in-memory caching of decrypted credentials
	EnableCache bool

	// CacheConfig provides cache configuration options
	CacheConfig *CacheConfig

	// AutoMigrate runs database migrations on startup
	AutoMigrate bool
}

// CacheConfig holds cache configuration options.
type CacheConfig struct {
	// Enabled enables the cache
	Enabled bool

	// TTLSeconds is the time-to-live for cached entries in seconds (default: 300)
	TTLSeconds int

	// MaxSize is the maximum number of cached entries (default: 1000)
	MaxSize int
}

// NewIntegration creates a complete credential integration setup.
// This is the recommended way to set up the credential vault system.
func NewIntegration(cfg *IntegrationConfig) (*Integration, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}
	if cfg.DB == nil {
		return nil, errors.New("database connection is required")
	}
	if cfg.MasterKeyBase64 == "" {
		return nil, errors.New("master encryption key is required")
	}

	logger := cfg.Logger
	if logger == nil {
		logger = logrus.New()
	}

	// Create encryption service
	encryptionService, err := NewEncryptionServiceFromBase64(cfg.MasterKeyBase64)
	if err != nil {
		return nil, fmt.Errorf("failed to create encryption service: %w", err)
	}

	// Create repository
	repository := NewPostgresRepository(cfg.DB)

	// Run migrations if requested
	if cfg.AutoMigrate {
		if err := repository.Migrate(); err != nil {
			return nil, fmt.Errorf("failed to run migrations: %w", err)
		}
		logger.Info("Credential vault database migrations completed")
	}

	// Create credential service
	credentialService, err := NewService(&ServiceConfig{
		Repository:        repository,
		EncryptionService: encryptionService,
		Logger:            logger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create credential service: %w", err)
	}

	// Create credential provider with optional caching
	providerCfg := &ProviderConfig{
		Service:     credentialService,
		Logger:      &logrusAdapter{logger},
		EnableCache: cfg.EnableCache,
	}

	if cfg.CacheConfig != nil {
		providerCfg.EnableCache = cfg.CacheConfig.Enabled
		if cfg.CacheConfig.TTLSeconds > 0 {
			providerCfg.CacheTTL = secondsToDuration(cfg.CacheConfig.TTLSeconds)
		}
		if cfg.CacheConfig.MaxSize > 0 {
			providerCfg.CacheMaxSize = cfg.CacheConfig.MaxSize
		}
	}

	credentialProvider, err := NewCredentialProvider(providerCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create credential provider: %w", err)
	}

	logger.Info("Credential vault integration initialized successfully")

	return &Integration{
		Repository:         repository,
		EncryptionService:  encryptionService,
		CredentialService:  credentialService,
		CredentialProvider: credentialProvider,
		Logger:             logger,
	}, nil
}

// Provider returns the credential provider interface.
func (i *Integration) Provider() CredentialProvider {
	return i.CredentialProvider
}

// Service returns the credential service interface.
func (i *Integration) Service() Service {
	return i.CredentialService
}

// AddCredential adds a new credential for a user.
// This is a convenience method that delegates to the service.
func (i *Integration) AddCredential(ctx context.Context, input *CreateCredentialInput) (*CredentialSummary, error) {
	summary, err := i.CredentialService.AddCredential(ctx, input)
	if err != nil {
		return nil, err
	}

	// Invalidate cache if enabled
	i.CredentialProvider.InvalidateCache(input.UserID, input.ExchangeName)

	return summary, nil
}

// UpdateCredential updates an existing credential.
// This is a convenience method that delegates to the service.
func (i *Integration) UpdateCredential(ctx context.Context, userID, exchange string, input *UpdateCredentialInput) error {
	if err := i.CredentialService.UpdateCredential(ctx, userID, exchange, input); err != nil {
		return err
	}

	// Invalidate cache
	i.CredentialProvider.InvalidateCache(userID, exchange)

	return nil
}

// DeleteCredential deletes a credential.
// This is a convenience method that delegates to the service.
func (i *Integration) DeleteCredential(ctx context.Context, userID, exchange string) error {
	if err := i.CredentialService.DeleteCredential(ctx, userID, exchange); err != nil {
		return err
	}

	// Invalidate cache
	i.CredentialProvider.InvalidateCache(userID, exchange)

	return nil
}

// TestCredential tests if a credential works against the exchange.
// This is a convenience method that delegates to the service.
func (i *Integration) TestCredential(ctx context.Context, userID, exchange string) (*TestCredentialResult, error) {
	return i.CredentialService.TestCredential(ctx, userID, exchange)
}

// ListCredentials lists all credentials for a user.
// This is a convenience method that delegates to the service.
func (i *Integration) ListCredentials(ctx context.Context, userID string) ([]*CredentialSummary, error) {
	return i.CredentialService.ListCredentials(ctx, userID)
}

// logrusAdapter adapts logrus.Logger to the Logger interface.
type logrusAdapter struct {
	*logrus.Logger
}

func (l *logrusAdapter) Infof(format string, args ...interface{}) {
	l.Logger.Infof(format, args...)
}

func (l *logrusAdapter) Warnf(format string, args ...interface{}) {
	l.Logger.Warnf(format, args...)
}

func (l *logrusAdapter) Errorf(format string, args ...interface{}) {
	l.Logger.Errorf(format, args...)
}

// secondsToDuration converts seconds to time.Duration.
func secondsToDuration(seconds int) time.Duration {
	return time.Duration(seconds) * time.Second
}

// Helper function to create context with user information.
// This is a convenience re-export of ContextWithUser for easier use.
func WithUser(ctx context.Context, userID, tenantID string) context.Context {
	return ContextWithUser(ctx, userID, tenantID)
}

// Helper function to extract user from context.
// This is a convenience re-export of UserFromContext for easier use.
func GetUser(ctx context.Context) (*UserContext, bool) {
	return UserFromContext(ctx)
}

// MustGetUser extracts user from context, panicking if not present.
// Use this in handlers where user context is guaranteed to exist.
func MustGetUser(ctx context.Context) *UserContext {
	user, ok := UserFromContext(ctx)
	if !ok {
		panic("user context not found - ensure authentication middleware is applied")
	}
	return user
}
