// Package gctclient provides the GoCryptoTrader client wrapper with HFT optimizations.
//
// Deprecated (UserClient): UserClient.ConfigureFromContext mutates the global
// KuCoinRESTClient singleton, which causes cross-trading when multiple users
// operate concurrently. New code should obtain an isolated *gct.ExchangeSession
// via gct.NewSessionManager and call the *ForUser methods on *Client instead.
// UserClient is retained for backward compatibility; do not add new callers.
package gctclient

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rantipay/trading-mcp/internal/credentials"
	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/sirupsen/logrus"
)

var (
	// ErrNoUserContext is returned when user context is missing from the request
	ErrNoUserContext = errors.New("user context is required for authenticated operations")

	// ErrNoCredentials is returned when no credentials are found for the user/exchange
	ErrNoCredentials = errors.New("no credentials found for user and exchange")

	// ErrCredentialProviderNotConfigured is returned when credential provider is not set
	ErrCredentialProviderNotConfigured = errors.New("credential provider not configured")

	// ErrCredentialsInactive is returned when credentials are inactive or expired
	ErrCredentialsInactive = errors.New("credentials are inactive or expired")

	// ErrCredentialsCircuitOpen is returned when too many failures have occurred
	ErrCredentialsCircuitOpen = errors.New("credential circuit breaker is open - too many failures")
)

// credentialCacheEntry holds cached credential state to prevent repeated lookups.
type credentialCacheEntry struct {
	userID       string
	exchange     string
	tenantID     string
	credentialID string
	configuredAt time.Time
}

// UserClient wraps the GCT client with per-user credential support.
// It provides the same API as Client but automatically configures credentials
// based on the user context in each request.
type UserClient struct {
	*Client
	credentialProvider credentials.CredentialProvider
	auditLogger        credentials.AuditLogger
	log                *logrus.Logger
	mu                 sync.RWMutex
	credentialCache    map[string]*credentialCacheEntry
	cacheTTL           time.Duration
}

// UserClientConfig holds configuration for the user client.
type UserClientConfig struct {
	// GCTClient is the underlying GCT client (if nil, BaseClient is used)
	GCTClient *Client

	// CredentialProvider is the provider for user credentials (required)
	CredentialProvider credentials.CredentialProvider

	// AuditLogger is the audit logger for credential operations (optional)
	AuditLogger credentials.AuditLogger

	// Logger is optional; if nil, a default logger is created
	Logger *logrus.Logger

	// CacheTTL is the time-to-live for cached credential configuration (default: 1 second)
	CacheTTL time.Duration
}

// NewUserClient creates a new user-aware GCT client wrapper.
func NewUserClient(cfg *UserClientConfig) (*UserClient, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}
	if cfg.CredentialProvider == nil {
		return nil, errors.New("credential provider is required")
	}
	if cfg.GCTClient == nil {
		return nil, errors.New("gct client is required")
	}

	logger := cfg.Logger
	if logger == nil {
		logger = logrus.New()
	}

	auditLogger := cfg.AuditLogger
	if auditLogger == nil {
		auditLogger = credentials.NewNoOpAuditLogger()
	}

	cacheTTL := cfg.CacheTTL
	if cacheTTL == 0 {
		cacheTTL = time.Second // Default to 1 second cache
	}

	return &UserClient{
		Client:             cfg.GCTClient,
		credentialProvider: cfg.CredentialProvider,
		auditLogger:        auditLogger,
		log:                logger,
		credentialCache:    make(map[string]*credentialCacheEntry),
		cacheTTL:           cacheTTL,
	}, nil
}

// cacheKey generates a unique cache key for user/exchange combination.
func (c *UserClient) cacheKey(userID, exchange string) string {
	return userID + ":" + exchange
}

// isCached checks if credentials are already configured and cache is still valid.
func (c *UserClient) isCached(userID, exchange string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := c.cacheKey(userID, exchange)
	entry, exists := c.credentialCache[key]
	if !exists {
		return false
	}

	return time.Since(entry.configuredAt) < c.cacheTTL
}

// updateCache updates the credential cache after successful configuration.
func (c *UserClient) updateCache(userID, exchange, tenantID, credentialID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := c.cacheKey(userID, exchange)
	c.credentialCache[key] = &credentialCacheEntry{
		userID:       userID,
		exchange:     exchange,
		tenantID:     tenantID,
		credentialID: credentialID,
		configuredAt: time.Now(),
	}
}

// InvalidateCache removes a specific cache entry.
func (c *UserClient) InvalidateCache(userID, exchange string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	exchange = credentials.NormalizeExchangeName(exchange)
	key := c.cacheKey(userID, exchange)
	delete(c.credentialCache, key)

	c.log.WithFields(logrus.Fields{
		"user_id":  userID,
		"exchange": exchange,
	}).Debug("Invalidated credential cache")
}

// InvalidateAllCacheForUser removes all cache entries for a user.
func (c *UserClient) InvalidateAllCacheForUser(userID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	prefix := userID + ":"
	for key := range c.credentialCache {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			delete(c.credentialCache, key)
		}
	}

	c.log.WithField("user_id", userID).Debug("Invalidated all credential cache for user")
}

// secureZeroBytes zeros out a byte slice to clear sensitive data from memory.
func secureZeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// clearCredentials securely clears credential data from memory.
// Note: Due to Go's string immutability, this is a best-effort approach.
// For maximum security, the credentials package should use []byte internally.
func (c *UserClient) clearCredentials(creds *credentials.ExchangeCredentials) {
	if creds == nil {
		return
	}

	// Clear sensitive fields by setting them to empty strings.
	// This allows the GC to collect the original strings.
	creds.APIKey = ""
	creds.APISecret = ""
	creds.Passphrase = ""
}

// getAuditContext creates an audit context from the request context.
func (c *UserClient) getAuditContext(ctx context.Context, userID, tenantID string) *credentials.AuditContext {
	auditCtx := credentials.AuditContextFromContext(ctx)
	if auditCtx != nil {
		if auditCtx.UserID == "" {
			auditCtx.UserID = userID
		}
		if auditCtx.TenantID == "" {
			auditCtx.TenantID = tenantID
		}
		return auditCtx
	}

	return &credentials.AuditContext{
		UserID:   userID,
		TenantID: tenantID,
	}
}

// ConfigureUserCredentials configures the underlying client with credentials for a specific user and exchange.
// It retrieves credentials from the vault, configures them for use, and clears sensitive data after configuration.
func (c *UserClient) ConfigureUserCredentials(ctx context.Context, userID, exchange string) error {
	if userID == "" {
		return ErrNoUserContext
	}
	if exchange == "" {
		return errors.New("exchange is required")
	}

	exchange = credentials.NormalizeExchangeName(exchange)

	// Check if we have a valid cache entry to prevent repeated lookups
	if c.isCached(userID, exchange) {
		c.log.WithFields(logrus.Fields{
			"user_id":  userID,
			"exchange": exchange,
		}).Trace("Using cached credential configuration")
		return nil
	}

	// Get credentials from the vault
	creds, err := c.credentialProvider.GetCredentials(ctx, userID, exchange)
	if err != nil {
		// Log audit event for failed credential access
		go c.auditLogger.LogRead(context.Background(), "", exchange, c.getAuditContext(ctx, userID, ""), false, err)

		if errors.Is(err, credentials.ErrNoCredentialsForExchange) {
			return ErrNoCredentials
		}
		return fmt.Errorf("failed to get credentials: %w", err)
	}

	// Ensure credentials are cleared from memory after we're done
	defer c.clearCredentials(creds)

	// Configure the underlying client
	c.SetExchangeCredentials(exchange, creds.APIKey, creds.APISecret, creds.Passphrase)

	// Update cache with successful configuration
	c.updateCache(userID, exchange, creds.TenantID, creds.CredentialID)

	// Record usage asynchronously
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := c.credentialProvider.RecordUsage(ctx, userID, exchange); err != nil {
			c.log.WithFields(logrus.Fields{
				"user_id":  userID,
				"exchange": exchange,
				"error":    err.Error(),
			}).Warn("Failed to record credential usage")
		}
	}()

	// Log audit event for successful credential access
	go func() {
		auditCtx := c.getAuditContext(ctx, userID, creds.TenantID)
		if err := c.auditLogger.LogUse(context.Background(), creds.CredentialID, exchange, auditCtx, true, nil); err != nil {
			c.log.WithFields(logrus.Fields{
				"user_id":       userID,
				"credential_id": creds.CredentialID,
				"error":         err.Error(),
			}).Warn("Failed to log credential usage to audit")
		}
	}()

	c.log.WithFields(logrus.Fields{
		"user_id":       userID,
		"exchange":      exchange,
		"credential_id": creds.CredentialID,
	}).Debug("Configured user credentials from vault")

	return nil
}

// ConfigureFromContext configures credentials using user context from ctx.
func (c *UserClient) ConfigureFromContext(ctx context.Context, exchange string) error {
	user, ok := credentials.UserFromContext(ctx)
	if !ok {
		return ErrNoUserContext
	}

	return c.ConfigureUserCredentials(ctx, user.UserID, exchange)
}

// recordSuccess records a successful API call and resets the failure counter.
func (c *UserClient) recordSuccess(ctx context.Context, exchange string) {
	user, ok := credentials.UserFromContext(ctx)
	if !ok {
		return
	}

	exchange = credentials.NormalizeExchangeName(exchange)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := c.credentialProvider.RecordSuccess(ctx, user.UserID, exchange); err != nil {
			c.log.WithFields(logrus.Fields{
				"user_id":  user.UserID,
				"exchange": exchange,
				"error":    err.Error(),
			}).Warn("Failed to record API success")
		}
	}()
}

// recordFailure records a failed API call and increments the failure counter.
func (c *UserClient) recordFailure(ctx context.Context, exchange string) {
	user, ok := credentials.UserFromContext(ctx)
	if !ok {
		return
	}

	exchange = credentials.NormalizeExchangeName(exchange)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := c.credentialProvider.RecordFailure(ctx, user.UserID, exchange); err != nil {
			c.log.WithFields(logrus.Fields{
				"user_id":  user.UserID,
				"exchange": exchange,
				"error":    err.Error(),
			}).Warn("Failed to record API failure")
		}
	}()
}

// RecordAPIResult is a helper method to record success or failure based on error.
func (c *UserClient) RecordAPIResult(ctx context.Context, exchange string, err error) {
	if err != nil {
		c.recordFailure(ctx, exchange)
	} else {
		c.recordSuccess(ctx, exchange)
	}
}

// Authenticated method wrappers.
// These methods configure credentials before delegating to the base client.

// SubmitOrderForUser submits an order using credentials for the user in context.
func (c *UserClient) SubmitOrderForUser(ctx context.Context, req *models.OrderRequest) (*models.OrderResponse, error) {
	if err := c.ConfigureFromContext(ctx, req.Exchange); err != nil {
		return nil, fmt.Errorf("failed to configure credentials: %w", err)
	}

	resp, err := c.Client.SubmitOrder(ctx, req)
	if err != nil {
		c.recordFailure(ctx, req.Exchange)
		return nil, err
	}

	c.recordSuccess(ctx, req.Exchange)
	return resp, nil
}

// SubmitFuturesOrderForUser submits a futures order using credentials for the user in context.
func (c *UserClient) SubmitFuturesOrderForUser(ctx context.Context, req *gct.SubmitOrderRequest) (*gct.Order, error) {
	if err := c.ConfigureFromContext(ctx, req.Exchange); err != nil {
		return nil, fmt.Errorf("failed to configure credentials: %w", err)
	}

	resp, err := c.Client.SubmitFuturesOrder(ctx, req)
	if err != nil {
		c.recordFailure(ctx, req.Exchange)
		return nil, err
	}

	c.recordSuccess(ctx, req.Exchange)
	return resp, nil
}

// GetPortfolioForUser retrieves portfolio using credentials for the user in context.
func (c *UserClient) GetPortfolioForUser(ctx context.Context, exchange string) (*models.Portfolio, error) {
	if err := c.ConfigureFromContext(ctx, exchange); err != nil {
		return nil, fmt.Errorf("failed to configure credentials: %w", err)
	}

	resp, err := c.Client.GetPortfolio(ctx)
	if err != nil {
		c.recordFailure(ctx, exchange)
		return nil, err
	}

	c.recordSuccess(ctx, exchange)
	return resp, nil
}

// GetBalanceForUser retrieves balance using credentials for the user in context.
func (c *UserClient) GetBalanceForUser(ctx context.Context, exchange, currency string) (*models.Balance, error) {
	if err := c.ConfigureFromContext(ctx, exchange); err != nil {
		return nil, fmt.Errorf("failed to configure credentials: %w", err)
	}

	resp, err := c.Client.GetBalance(ctx, exchange, currency)
	if err != nil {
		c.recordFailure(ctx, exchange)
		return nil, err
	}

	c.recordSuccess(ctx, exchange)
	return resp, nil
}

// GetOpenOrdersForUser retrieves open orders using credentials for the user in context.
func (c *UserClient) GetOpenOrdersForUser(ctx context.Context, exchange, pair string) ([]map[string]interface{}, error) {
	if err := c.ConfigureFromContext(ctx, exchange); err != nil {
		return nil, fmt.Errorf("failed to configure credentials: %w", err)
	}

	resp, err := c.Client.GetOpenOrders(ctx, exchange, pair)
	if err != nil {
		c.recordFailure(ctx, exchange)
		return nil, err
	}

	c.recordSuccess(ctx, exchange)
	return resp, nil
}

// GetOrderHistoryForUser retrieves order history using credentials for the user in context.
func (c *UserClient) GetOrderHistoryForUser(ctx context.Context, exchange, pair string, limit int) ([]map[string]interface{}, error) {
	if err := c.ConfigureFromContext(ctx, exchange); err != nil {
		return nil, fmt.Errorf("failed to configure credentials: %w", err)
	}

	resp, err := c.Client.GetOrderHistory(ctx, exchange, pair, limit)
	if err != nil {
		c.recordFailure(ctx, exchange)
		return nil, err
	}

	c.recordSuccess(ctx, exchange)
	return resp, nil
}

// GetTradeHistoryForUser retrieves trade history using credentials for the user in context.
func (c *UserClient) GetTradeHistoryForUser(ctx context.Context, exchange, pair string, limit int) ([]map[string]interface{}, error) {
	if err := c.ConfigureFromContext(ctx, exchange); err != nil {
		return nil, fmt.Errorf("failed to configure credentials: %w", err)
	}

	resp, err := c.Client.GetTradeHistory(ctx, exchange, pair, limit)
	if err != nil {
		c.recordFailure(ctx, exchange)
		return nil, err
	}

	c.recordSuccess(ctx, exchange)
	return resp, nil
}

// CancelOrderForUser cancels an order using credentials for the user in context.
func (c *UserClient) CancelOrderForUser(ctx context.Context, exchange, orderID string) error {
	if err := c.ConfigureFromContext(ctx, exchange); err != nil {
		return fmt.Errorf("failed to configure credentials: %w", err)
	}

	err := c.Client.CancelOrder(ctx, exchange, orderID)
	if err != nil {
		c.recordFailure(ctx, exchange)
		return err
	}

	c.recordSuccess(ctx, exchange)
	return nil
}

// GetFuturesPositionsForUser retrieves futures positions using credentials for the user in context.
func (c *UserClient) GetFuturesPositionsForUser(ctx context.Context, exchange string) ([]*gct.Position, error) {
	if err := c.ConfigureFromContext(ctx, exchange); err != nil {
		return nil, fmt.Errorf("failed to configure credentials: %w", err)
	}

	resp, err := c.Client.GetFuturesPositions(ctx, exchange)
	if err != nil {
		c.recordFailure(ctx, exchange)
		return nil, err
	}

	c.recordSuccess(ctx, exchange)
	return resp, nil
}

// HasUserCredentials checks if credentials exist for the user in context.
func (c *UserClient) HasUserCredentials(ctx context.Context, exchange string) (bool, error) {
	user, ok := credentials.UserFromContext(ctx)
	if !ok {
		return false, nil
	}

	return c.credentialProvider.HasCredentials(ctx, user.UserID, exchange)
}

// GetOrderHistoryWithTimeFilterForUser retrieves order history with time filter using credentials for the user in context.
func (c *UserClient) GetOrderHistoryWithTimeFilterForUser(ctx context.Context, exchange, pair string, limit int, startTime, endTime time.Time) ([]map[string]interface{}, error) {
	if err := c.ConfigureFromContext(ctx, exchange); err != nil {
		return nil, fmt.Errorf("failed to configure credentials: %w", err)
	}

	resp, err := c.Client.GetOrderHistoryWithTimeFilter(ctx, exchange, pair, limit, startTime, endTime)
	if err != nil {
		c.recordFailure(ctx, exchange)
		return nil, err
	}

	c.recordSuccess(ctx, exchange)
	return resp, nil
}

// GetTradeHistoryWithTimeFilterForUser retrieves trade history with time filter using credentials for the user in context.
func (c *UserClient) GetTradeHistoryWithTimeFilterForUser(ctx context.Context, exchange, pair string, limit int, startTime, endTime time.Time) ([]map[string]interface{}, error) {
	if err := c.ConfigureFromContext(ctx, exchange); err != nil {
		return nil, fmt.Errorf("failed to configure credentials: %w", err)
	}

	resp, err := c.Client.GetTradeHistoryWithTimeFilter(ctx, exchange, pair, limit, startTime, endTime)
	if err != nil {
		c.recordFailure(ctx, exchange)
		return nil, err
	}

	c.recordSuccess(ctx, exchange)
	return resp, nil
}

// SetLeverageForUser sets leverage using credentials for the user in context.
func (c *UserClient) SetLeverageForUser(ctx context.Context, exchange, symbol string, leverage int) error {
	if err := c.ConfigureFromContext(ctx, exchange); err != nil {
		return fmt.Errorf("failed to configure credentials: %w", err)
	}

	err := c.Client.SetLeverage(ctx, exchange, symbol, leverage)
	c.RecordAPIResult(ctx, exchange, err)
	return err
}

// CloseFuturesPositionForUser closes a futures position using credentials for the user in context.
func (c *UserClient) CloseFuturesPositionForUser(ctx context.Context, exchange, symbol string) error {
	if err := c.ConfigureFromContext(ctx, exchange); err != nil {
		return fmt.Errorf("failed to configure credentials: %w", err)
	}

	err := c.Client.CloseFuturesPosition(ctx, exchange, symbol)
	c.RecordAPIResult(ctx, exchange, err)
	return err
}

// GetAccountInfoForUser retrieves account info using credentials for the user in context.
func (c *UserClient) GetAccountInfoForUser(ctx context.Context, exchange string) (*gct.AccountInfo, error) {
	if err := c.ConfigureFromContext(ctx, exchange); err != nil {
		return nil, fmt.Errorf("failed to configure credentials: %w", err)
	}

	info, err := c.Client.GetAccountInfo(ctx, exchange)
	c.RecordAPIResult(ctx, exchange, err)
	return info, err
}

// ValidateCredentialsForUser validates that credentials work for the user in context.
func (c *UserClient) ValidateCredentialsForUser(ctx context.Context, exchange string) error {
	// First, configure credentials
	if err := c.ConfigureFromContext(ctx, exchange); err != nil {
		return fmt.Errorf("failed to configure credentials: %w", err)
	}

	// Try to get account info as a validation check
	_, err := c.Client.GetAccountInfo(ctx, exchange)
	c.RecordAPIResult(ctx, exchange, err)

	if err != nil {
		return fmt.Errorf("credential validation failed: %w", err)
	}

	return nil
}

// GetCredentialStats returns statistics about the credential cache.
func (c *UserClient) GetCredentialStats() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	stats := map[string]interface{}{
		"cached_credentials": len(c.credentialCache),
		"cache_ttl_seconds":  c.cacheTTL.Seconds(),
	}

	if len(c.credentialCache) > 0 {
		var oldestAge, newestAge time.Duration
		var exchanges []string

		for key, entry := range c.credentialCache {
			age := time.Since(entry.configuredAt)
			if oldestAge == 0 || age > oldestAge {
				oldestAge = age
			}
			if newestAge == 0 || age < newestAge {
				newestAge = age
			}
			// Extract exchange from key (format: "userID:exchange")
			if idx := len(entry.userID) + 1; idx < len(key) {
				exchanges = append(exchanges, key[idx:])
			}
		}

		stats["oldest_entry_age_ms"] = oldestAge.Milliseconds()
		stats["newest_entry_age_ms"] = newestAge.Milliseconds()
		stats["cached_exchanges"] = exchanges
	}

	return stats
}

// ClearAllCache clears all credential cache entries.
func (c *UserClient) ClearAllCache() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.credentialCache = make(map[string]*credentialCacheEntry)
	c.log.Info("Cleared all credential cache entries")
}

// ExecuteWithCredentials is a generic method for executing any operation with user credentials.
// It handles credential configuration, execution, and result recording.
func (c *UserClient) ExecuteWithCredentials(ctx context.Context, exchange string, fn func() error) error {
	if err := c.ConfigureFromContext(ctx, exchange); err != nil {
		return fmt.Errorf("failed to configure credentials: %w", err)
	}

	err := fn()
	c.RecordAPIResult(ctx, exchange, err)
	return err
}

// ExecuteWithCredentialsAndResult is a generic function for executing operations that return a result.
// Note: This is a package-level function because Go methods cannot have type parameters.
func ExecuteWithCredentialsAndResult[T any](c *UserClient, ctx context.Context, exchange string, fn func() (T, error)) (T, error) {
	var zero T

	if err := c.ConfigureFromContext(ctx, exchange); err != nil {
		return zero, fmt.Errorf("failed to configure credentials: %w", err)
	}

	result, err := fn()
	c.RecordAPIResult(ctx, exchange, err)
	return result, err
}
