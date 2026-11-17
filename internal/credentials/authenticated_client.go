// Package credentials provides secure storage and management of user exchange API credentials.
package credentials

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rantipay/trading-mcp/internal/gct"
)

// UserContextKey is the context key for storing user information.
type UserContextKey struct{}

// UserContext contains user identification for credential lookup.
type UserContext struct {
	UserID   string
	TenantID string
}

// ContextWithUser adds user context to a context.Context.
func ContextWithUser(ctx context.Context, userID, tenantID string) context.Context {
	return context.WithValue(ctx, UserContextKey{}, &UserContext{
		UserID:   userID,
		TenantID: tenantID,
	})
}

// UserFromContext extracts user context from a context.Context.
func UserFromContext(ctx context.Context) (*UserContext, bool) {
	user, ok := ctx.Value(UserContextKey{}).(*UserContext)
	return user, ok
}

// AuthenticatedGCTClient wraps a GCT client to use per-user credentials from the vault.
// It intercepts operations that require authentication and configures the underlying
// client with the appropriate user credentials before making the call.
type AuthenticatedGCTClient struct {
	baseClient         gct.GCTClient
	credentialProvider CredentialProvider
	logger             Logger
	mu                 sync.RWMutex
	lastCredentials    map[string]*credentialState // keyed by userID:exchange
}

// credentialState tracks the state of credentials configured in the client.
type credentialState struct {
	userID       string
	exchange     string
	configuredAt time.Time
}

// AuthenticatedClientConfig holds configuration for the authenticated client.
type AuthenticatedClientConfig struct {
	// BaseClient is the underlying GCT client (required)
	BaseClient gct.GCTClient

	// CredentialProvider is the credential provider for user credentials (required)
	CredentialProvider CredentialProvider

	// Logger is optional; if nil, logging is disabled
	Logger Logger
}

// NewAuthenticatedGCTClient creates a new authenticated GCT client wrapper.
func NewAuthenticatedGCTClient(cfg *AuthenticatedClientConfig) (*AuthenticatedGCTClient, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}
	if cfg.BaseClient == nil {
		return nil, errors.New("base client is required")
	}
	if cfg.CredentialProvider == nil {
		return nil, errors.New("credential provider is required")
	}

	return &AuthenticatedGCTClient{
		baseClient:         cfg.BaseClient,
		credentialProvider: cfg.CredentialProvider,
		logger:             cfg.Logger,
		lastCredentials:    make(map[string]*credentialState),
	}, nil
}

// ConfigureCredentials configures the underlying client with credentials for a specific user and exchange.
// This is the core method that enables per-user credential switching.
func (c *AuthenticatedGCTClient) ConfigureCredentials(ctx context.Context, userID, exchange string) error {
	if userID == "" {
		return ErrUserContextRequired
	}
	if exchange == "" {
		return ErrInvalidExchange
	}

	exchange = NormalizeExchangeName(exchange)
	stateKey := userID + ":" + exchange

	// Check if we already have the correct credentials configured
	c.mu.RLock()
	state := c.lastCredentials[stateKey]
	c.mu.RUnlock()

	// If credentials were configured recently (within 1 second), skip reconfiguration
	// This prevents unnecessary credential lookups for rapid successive calls
	if state != nil && time.Since(state.configuredAt) < time.Second {
		return nil
	}

	// Get credentials from the vault
	creds, err := c.credentialProvider.GetCredentials(ctx, userID, exchange)
	if err != nil {
		return fmt.Errorf("failed to get credentials for user=%s exchange=%s: %w", userID, exchange, err)
	}

	// Configure the underlying client based on exchange type
	if err := c.configureExchangeCredentials(creds); err != nil {
		return fmt.Errorf("failed to configure exchange credentials: %w", err)
	}

	// Update state
	c.mu.Lock()
	c.lastCredentials[stateKey] = &credentialState{
		userID:       userID,
		exchange:     exchange,
		configuredAt: time.Now(),
	}
	c.mu.Unlock()

	// Record usage asynchronously
	go func() {
		if err := c.credentialProvider.RecordUsage(context.Background(), userID, exchange); err != nil {
			if c.logger != nil {
				c.logger.Warnf("Failed to record credential usage: %v", err)
			}
		}
	}()

	if c.logger != nil {
		c.logger.Infof("Configured credentials for user=%s exchange=%s", userID, exchange)
	}

	return nil
}

// configureExchangeCredentials sets up the underlying client with exchange-specific credentials.
func (c *AuthenticatedGCTClient) configureExchangeCredentials(creds *ExchangeCredentials) error {
	switch NormalizeExchangeName(creds.Exchange) {
	case ExchangeKuCoin:
		c.baseClient.SetKuCoinCredentials(creds.APIKey, creds.APISecret, creds.Passphrase)
	case ExchangeBinance, ExchangeCoinbase, ExchangeKraken, ExchangeBybit, ExchangeOKX, ExchangeBitget:
		// For other exchanges, use SetKuCoinCredentials as a generic method
		// In a more complete implementation, each exchange would have its own method
		c.baseClient.SetKuCoinCredentials(creds.APIKey, creds.APISecret, creds.Passphrase)
	default:
		// Generic fallback
		c.baseClient.SetKuCoinCredentials(creds.APIKey, creds.APISecret, creds.Passphrase)
	}
	return nil
}

// WithUserCredentials is a helper method that configures credentials from context and returns the client.
// Usage: client.WithUserCredentials(ctx, "kucoin").SubmitOrder(ctx, req)
func (c *AuthenticatedGCTClient) WithUserCredentials(ctx context.Context, exchange string) (*AuthenticatedGCTClient, error) {
	user, ok := UserFromContext(ctx)
	if !ok {
		return nil, ErrUserContextRequired
	}

	if err := c.ConfigureCredentials(ctx, user.UserID, exchange); err != nil {
		return nil, err
	}

	return c, nil
}

// RecordAPISuccess should be called after a successful API call to reset failure count.
func (c *AuthenticatedGCTClient) RecordAPISuccess(ctx context.Context, exchange string) {
	user, ok := UserFromContext(ctx)
	if !ok {
		return
	}

	go func() {
		if err := c.credentialProvider.RecordSuccess(context.Background(), user.UserID, exchange); err != nil {
			if c.logger != nil {
				c.logger.Warnf("Failed to record API success: %v", err)
			}
		}
	}()
}

// RecordAPIFailure should be called after a failed API call to increment failure count.
func (c *AuthenticatedGCTClient) RecordAPIFailure(ctx context.Context, exchange string) {
	user, ok := UserFromContext(ctx)
	if !ok {
		return
	}

	go func() {
		if err := c.credentialProvider.RecordFailure(context.Background(), user.UserID, exchange); err != nil {
			if c.logger != nil {
				c.logger.Warnf("Failed to record API failure: %v", err)
			}
		}
	}()
}

// BaseClient returns the underlying GCT client for operations that don't require authentication.
func (c *AuthenticatedGCTClient) BaseClient() gct.GCTClient {
	return c.baseClient
}

// Authenticated execution wrappers follow.
// These methods configure credentials before delegating to the base client.

// SubmitOrderWithCredentials submits an order using user credentials from context.
func (c *AuthenticatedGCTClient) SubmitOrderWithCredentials(ctx context.Context, req *gct.SubmitOrderRequest) (*gct.Order, error) {
	if err := c.ensureCredentials(ctx, req.Exchange); err != nil {
		return nil, err
	}

	order, err := c.baseClient.SubmitOrder(ctx, req)
	if err != nil {
		c.RecordAPIFailure(ctx, req.Exchange)
		return nil, err
	}

	c.RecordAPISuccess(ctx, req.Exchange)
	return order, nil
}

// SubmitFuturesOrderWithCredentials submits a futures order using user credentials from context.
func (c *AuthenticatedGCTClient) SubmitFuturesOrderWithCredentials(ctx context.Context, req *gct.SubmitOrderRequest) (*gct.Order, error) {
	if err := c.ensureCredentials(ctx, req.Exchange); err != nil {
		return nil, err
	}

	order, err := c.baseClient.SubmitFuturesOrder(ctx, req)
	if err != nil {
		c.RecordAPIFailure(ctx, req.Exchange)
		return nil, err
	}

	c.RecordAPISuccess(ctx, req.Exchange)
	return order, nil
}

// GetAccountInfoWithCredentials retrieves account info using user credentials from context.
func (c *AuthenticatedGCTClient) GetAccountInfoWithCredentials(ctx context.Context, exchange string) (*gct.AccountInfo, error) {
	if err := c.ensureCredentials(ctx, exchange); err != nil {
		return nil, err
	}

	info, err := c.baseClient.GetAccountInfo(ctx, exchange)
	if err != nil {
		c.RecordAPIFailure(ctx, exchange)
		return nil, err
	}

	c.RecordAPISuccess(ctx, exchange)
	return info, nil
}

// GetBalanceWithCredentials retrieves balance using user credentials from context.
func (c *AuthenticatedGCTClient) GetBalanceWithCredentials(ctx context.Context, exchange, currency string) (map[string]interface{}, error) {
	if err := c.ensureCredentials(ctx, exchange); err != nil {
		return nil, err
	}

	balance, err := c.baseClient.GetBalance(ctx, exchange, currency)
	if err != nil {
		c.RecordAPIFailure(ctx, exchange)
		return nil, err
	}

	c.RecordAPISuccess(ctx, exchange)
	return balance, nil
}

// GetPortfolioWithCredentials retrieves portfolio using user credentials from context.
func (c *AuthenticatedGCTClient) GetPortfolioWithCredentials(ctx context.Context, exchange string) (*gct.Portfolio, error) {
	if err := c.ensureCredentials(ctx, exchange); err != nil {
		return nil, err
	}

	portfolio, err := c.baseClient.GetPortfolio(ctx)
	if err != nil {
		c.RecordAPIFailure(ctx, exchange)
		return nil, err
	}

	c.RecordAPISuccess(ctx, exchange)
	return portfolio, nil
}

// GetOrdersWithCredentials retrieves orders using user credentials from context.
func (c *AuthenticatedGCTClient) GetOrdersWithCredentials(ctx context.Context, exchange, pair, orderType, assetType string, startTime, endTime *time.Time) ([]*gct.Order, error) {
	if err := c.ensureCredentials(ctx, exchange); err != nil {
		return nil, err
	}

	orders, err := c.baseClient.GetOrders(ctx, exchange, pair, orderType, assetType, startTime, endTime)
	if err != nil {
		c.RecordAPIFailure(ctx, exchange)
		return nil, err
	}

	c.RecordAPISuccess(ctx, exchange)
	return orders, nil
}

// CancelOrderWithCredentials cancels an order using user credentials from context.
func (c *AuthenticatedGCTClient) CancelOrderWithCredentials(ctx context.Context, exchange, orderID string) error {
	if err := c.ensureCredentials(ctx, exchange); err != nil {
		return err
	}

	err := c.baseClient.CancelOrder(ctx, exchange, orderID)
	if err != nil {
		c.RecordAPIFailure(ctx, exchange)
		return err
	}

	c.RecordAPISuccess(ctx, exchange)
	return nil
}

// GetFuturesPositionsWithCredentials retrieves futures positions using user credentials from context.
func (c *AuthenticatedGCTClient) GetFuturesPositionsWithCredentials(ctx context.Context, exchange string) ([]*gct.Position, error) {
	if err := c.ensureCredentials(ctx, exchange); err != nil {
		return nil, err
	}

	positions, err := c.baseClient.GetFuturesPositions(ctx, exchange)
	if err != nil {
		c.RecordAPIFailure(ctx, exchange)
		return nil, err
	}

	c.RecordAPISuccess(ctx, exchange)
	return positions, nil
}

// CloseFuturesPositionWithCredentials closes a futures position using user credentials from context.
func (c *AuthenticatedGCTClient) CloseFuturesPositionWithCredentials(ctx context.Context, exchange, symbol string) error {
	if err := c.ensureCredentials(ctx, exchange); err != nil {
		return err
	}

	err := c.baseClient.CloseFuturesPosition(ctx, exchange, symbol)
	if err != nil {
		c.RecordAPIFailure(ctx, exchange)
		return err
	}

	c.RecordAPISuccess(ctx, exchange)
	return nil
}

// SetLeverageWithCredentials sets leverage using user credentials from context.
func (c *AuthenticatedGCTClient) SetLeverageWithCredentials(ctx context.Context, exchange, symbol string, leverage int) error {
	if err := c.ensureCredentials(ctx, exchange); err != nil {
		return err
	}

	err := c.baseClient.SetLeverage(ctx, exchange, symbol, leverage)
	if err != nil {
		c.RecordAPIFailure(ctx, exchange)
		return err
	}

	c.RecordAPISuccess(ctx, exchange)
	return nil
}

// ensureCredentials ensures that the client is configured with the correct user credentials.
func (c *AuthenticatedGCTClient) ensureCredentials(ctx context.Context, exchange string) error {
	user, ok := UserFromContext(ctx)
	if !ok {
		return ErrUserContextRequired
	}

	return c.ConfigureCredentials(ctx, user.UserID, exchange)
}

// HasUserCredentials checks if credentials exist for the user in context and the specified exchange.
func (c *AuthenticatedGCTClient) HasUserCredentials(ctx context.Context, exchange string) (bool, error) {
	user, ok := UserFromContext(ctx)
	if !ok {
		return false, nil
	}

	return c.credentialProvider.HasCredentials(ctx, user.UserID, exchange)
}
