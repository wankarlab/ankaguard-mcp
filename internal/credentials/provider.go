// Package credentials provides secure storage and management of user exchange API credentials.
package credentials

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// ErrNoCredentialsForExchange is returned when no credentials exist for the requested exchange
	ErrNoCredentialsForExchange = errors.New("no credentials found for exchange")

	// ErrUserContextRequired is returned when user context is missing from the request
	ErrUserContextRequired = errors.New("user context is required for authenticated operations")
)

// ExchangeCredentials represents the decrypted credentials needed to authenticate with an exchange.
// This is a simplified struct optimized for passing to exchange clients.
type ExchangeCredentials struct {
	APIKey       string
	APISecret    string
	Passphrase   string
	Exchange     string
	UserID       string
	TenantID     string
	CredentialID string // Unique identifier for the credential record
}

// CredentialProvider defines the interface for retrieving user credentials.
// This interface allows the GCT client to obtain per-user credentials dynamically.
type CredentialProvider interface {
	// GetCredentials retrieves the decrypted credentials for a user and exchange.
	// Returns ErrNoCredentialsForExchange if no credentials exist.
	GetCredentials(ctx context.Context, userID, exchange string) (*ExchangeCredentials, error)

	// HasCredentials checks if credentials exist for a user and exchange without decrypting.
	HasCredentials(ctx context.Context, userID, exchange string) (bool, error)

	// RecordUsage records that credentials were used for an API call.
	RecordUsage(ctx context.Context, userID, exchange string) error

	// RecordSuccess records a successful API call (resets failure count).
	RecordSuccess(ctx context.Context, userID, exchange string) error

	// RecordFailure records a failed API call (increments failure count for circuit breaker).
	RecordFailure(ctx context.Context, userID, exchange string) error
}

// CredentialProviderImpl implements CredentialProvider using the CredentialService.
type CredentialProviderImpl struct {
	service Service
	cache   *credentialCache
	logger  Logger
}

// Logger is a minimal logging interface for the credential provider.
type Logger interface {
	Infof(format string, args ...interface{})
	Warnf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
}

// credentialCache provides optional in-memory caching of decrypted credentials.
// Credentials are cached for a short TTL to reduce database and decryption overhead.
type credentialCache struct {
	mu       sync.RWMutex
	entries  map[string]*cacheEntry
	ttl      time.Duration
	maxSize  int
	enabled  bool
}

type cacheEntry struct {
	credentials *ExchangeCredentials
	expiresAt   time.Time
}

// ProviderConfig holds configuration for the credential provider.
type ProviderConfig struct {
	// Service is the underlying credential service (required)
	Service Service

	// Logger is optional; if nil, logging is disabled
	Logger Logger

	// EnableCache enables in-memory caching of decrypted credentials
	EnableCache bool

	// CacheTTL is the time-to-live for cached credentials (default: 5 minutes)
	CacheTTL time.Duration

	// CacheMaxSize is the maximum number of cached credentials (default: 1000)
	CacheMaxSize int
}

// NewCredentialProvider creates a new credential provider.
func NewCredentialProvider(cfg *ProviderConfig) (*CredentialProviderImpl, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}
	if cfg.Service == nil {
		return nil, errors.New("service is required")
	}

	provider := &CredentialProviderImpl{
		service: cfg.Service,
		logger:  cfg.Logger,
	}

	// Initialize cache if enabled
	if cfg.EnableCache {
		ttl := cfg.CacheTTL
		if ttl == 0 {
			ttl = 5 * time.Minute
		}
		maxSize := cfg.CacheMaxSize
		if maxSize == 0 {
			maxSize = 1000
		}
		provider.cache = &credentialCache{
			entries: make(map[string]*cacheEntry),
			ttl:     ttl,
			maxSize: maxSize,
			enabled: true,
		}
		// Start cache cleanup goroutine
		go provider.cacheCleanupLoop()
	}

	return provider, nil
}

// GetCredentials retrieves the decrypted credentials for a user and exchange.
func (p *CredentialProviderImpl) GetCredentials(ctx context.Context, userID, exchange string) (*ExchangeCredentials, error) {
	if userID == "" {
		return nil, ErrUserContextRequired
	}
	if exchange == "" {
		return nil, ErrInvalidExchange
	}

	exchange = NormalizeExchangeName(exchange)
	cacheKey := p.cacheKey(userID, exchange)

	// Check cache first
	if p.cache != nil && p.cache.enabled {
		if creds := p.getFromCache(cacheKey); creds != nil {
			if p.logger != nil {
				p.logger.Infof("Credential cache hit for user=%s exchange=%s", userID, exchange)
			}
			return creds, nil
		}
	}

	// Fetch from service
	decrypted, err := p.service.GetCredential(ctx, userID, exchange)
	if err != nil {
		if errors.Is(err, ErrCredentialNotFound) {
			return nil, ErrNoCredentialsForExchange
		}
		return nil, fmt.Errorf("failed to get credentials: %w", err)
	}

	// Convert to ExchangeCredentials
	creds := &ExchangeCredentials{
		APIKey:       decrypted.APIKey,
		APISecret:    decrypted.APISecret,
		Passphrase:   decrypted.Passphrase,
		Exchange:     decrypted.ExchangeName,
		UserID:       decrypted.UserID,
		TenantID:     decrypted.TenantID,
		CredentialID: decrypted.ID,
	}

	// Store in cache
	if p.cache != nil && p.cache.enabled {
		p.storeInCache(cacheKey, creds)
	}

	if p.logger != nil {
		p.logger.Infof("Retrieved credentials for user=%s exchange=%s", userID, exchange)
	}

	return creds, nil
}

// HasCredentials checks if credentials exist for a user and exchange without decrypting.
func (p *CredentialProviderImpl) HasCredentials(ctx context.Context, userID, exchange string) (bool, error) {
	if userID == "" {
		return false, ErrUserContextRequired
	}
	if exchange == "" {
		return false, ErrInvalidExchange
	}

	exchange = NormalizeExchangeName(exchange)

	// Check cache first
	if p.cache != nil && p.cache.enabled {
		cacheKey := p.cacheKey(userID, exchange)
		if p.getFromCache(cacheKey) != nil {
			return true, nil
		}
	}

	// Check in database
	_, err := p.service.GetCredential(ctx, userID, exchange)
	if err != nil {
		if errors.Is(err, ErrCredentialNotFound) {
			return false, nil
		}
		if errors.Is(err, ErrCredentialDisabled) {
			return false, nil
		}
		if errors.Is(err, ErrCredentialExpired) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check credentials: %w", err)
	}

	return true, nil
}

// RecordUsage records that credentials were used for an API call.
func (p *CredentialProviderImpl) RecordUsage(ctx context.Context, userID, exchange string) error {
	return p.service.RecordUsage(ctx, userID, exchange)
}

// RecordSuccess records a successful API call (resets failure count).
func (p *CredentialProviderImpl) RecordSuccess(ctx context.Context, userID, exchange string) error {
	return p.service.RecordSuccess(ctx, userID, exchange)
}

// RecordFailure records a failed API call (increments failure count for circuit breaker).
func (p *CredentialProviderImpl) RecordFailure(ctx context.Context, userID, exchange string) error {
	return p.service.RecordFailure(ctx, userID, exchange)
}

// InvalidateCache removes cached credentials for a user and exchange.
// Call this after credentials are updated or deleted.
func (p *CredentialProviderImpl) InvalidateCache(userID, exchange string) {
	if p.cache == nil || !p.cache.enabled {
		return
	}

	exchange = NormalizeExchangeName(exchange)
	cacheKey := p.cacheKey(userID, exchange)

	p.cache.mu.Lock()
	delete(p.cache.entries, cacheKey)
	p.cache.mu.Unlock()

	if p.logger != nil {
		p.logger.Infof("Invalidated credential cache for user=%s exchange=%s", userID, exchange)
	}
}

// InvalidateUserCache removes all cached credentials for a user.
func (p *CredentialProviderImpl) InvalidateUserCache(userID string) {
	if p.cache == nil || !p.cache.enabled {
		return
	}

	prefix := userID + ":"

	p.cache.mu.Lock()
	for key := range p.cache.entries {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			delete(p.cache.entries, key)
		}
	}
	p.cache.mu.Unlock()

	if p.logger != nil {
		p.logger.Infof("Invalidated all cached credentials for user=%s", userID)
	}
}

// cacheKey generates a cache key for a user and exchange.
func (p *CredentialProviderImpl) cacheKey(userID, exchange string) string {
	return userID + ":" + exchange
}

// getFromCache retrieves credentials from cache if not expired.
func (p *CredentialProviderImpl) getFromCache(key string) *ExchangeCredentials {
	p.cache.mu.RLock()
	entry, exists := p.cache.entries[key]
	p.cache.mu.RUnlock()

	if !exists {
		return nil
	}

	if time.Now().After(entry.expiresAt) {
		// Entry expired, remove it
		p.cache.mu.Lock()
		delete(p.cache.entries, key)
		p.cache.mu.Unlock()
		return nil
	}

	return entry.credentials
}

// storeInCache stores credentials in the cache.
func (p *CredentialProviderImpl) storeInCache(key string, creds *ExchangeCredentials) {
	p.cache.mu.Lock()
	defer p.cache.mu.Unlock()

	// Evict oldest entries if at max size
	if len(p.cache.entries) >= p.cache.maxSize {
		p.evictOldestEntry()
	}

	p.cache.entries[key] = &cacheEntry{
		credentials: creds,
		expiresAt:   time.Now().Add(p.cache.ttl),
	}
}

// evictOldestEntry removes the oldest cache entry.
// Must be called with cache mutex held.
func (p *CredentialProviderImpl) evictOldestEntry() {
	var oldestKey string
	var oldestTime time.Time

	for key, entry := range p.cache.entries {
		if oldestKey == "" || entry.expiresAt.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.expiresAt
		}
	}

	if oldestKey != "" {
		delete(p.cache.entries, oldestKey)
	}
}

// cacheCleanupLoop periodically removes expired cache entries.
func (p *CredentialProviderImpl) cacheCleanupLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		if p.cache == nil || !p.cache.enabled {
			return
		}

		now := time.Now()
		var expiredKeys []string

		p.cache.mu.RLock()
		for key, entry := range p.cache.entries {
			if now.After(entry.expiresAt) {
				expiredKeys = append(expiredKeys, key)
			}
		}
		p.cache.mu.RUnlock()

		if len(expiredKeys) > 0 {
			p.cache.mu.Lock()
			for _, key := range expiredKeys {
				delete(p.cache.entries, key)
			}
			p.cache.mu.Unlock()

			if p.logger != nil {
				p.logger.Infof("Cleaned up %d expired credential cache entries", len(expiredKeys))
			}
		}
	}
}
