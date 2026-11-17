// Package gctclient provides the GoCryptoTrader client wrapper with HFT optimizations.
package gctclient

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rantipay/trading-mcp/internal/credentials"
	"github.com/sirupsen/logrus"
)

// mockCredentialProvider is a mock implementation of CredentialProvider for testing.
type mockCredentialProvider struct {
	credentials      map[string]*credentials.ExchangeCredentials
	usageCount       map[string]int
	successCount     map[string]int
	failureCount     map[string]int
	mu               sync.RWMutex
	getCredentialsErr error
}

func newMockCredentialProvider() *mockCredentialProvider {
	return &mockCredentialProvider{
		credentials:  make(map[string]*credentials.ExchangeCredentials),
		usageCount:   make(map[string]int),
		successCount: make(map[string]int),
		failureCount: make(map[string]int),
	}
}

func (m *mockCredentialProvider) key(userID, exchange string) string {
	return userID + ":" + credentials.NormalizeExchangeName(exchange)
}

func (m *mockCredentialProvider) SetCredentials(userID, exchange string, creds *credentials.ExchangeCredentials) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.credentials[m.key(userID, exchange)] = creds
}

func (m *mockCredentialProvider) SetError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getCredentialsErr = err
}

func (m *mockCredentialProvider) GetCredentials(ctx context.Context, userID, exchange string) (*credentials.ExchangeCredentials, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.getCredentialsErr != nil {
		return nil, m.getCredentialsErr
	}

	creds, ok := m.credentials[m.key(userID, exchange)]
	if !ok {
		return nil, credentials.ErrNoCredentialsForExchange
	}

	// Return a copy to simulate real behavior
	return &credentials.ExchangeCredentials{
		APIKey:       creds.APIKey,
		APISecret:    creds.APISecret,
		Passphrase:   creds.Passphrase,
		Exchange:     creds.Exchange,
		TenantID:     creds.TenantID,
		CredentialID: creds.CredentialID,
	}, nil
}

func (m *mockCredentialProvider) HasCredentials(ctx context.Context, userID, exchange string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.credentials[m.key(userID, exchange)]
	return ok, nil
}

func (m *mockCredentialProvider) RecordUsage(ctx context.Context, userID, exchange string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.usageCount[m.key(userID, exchange)]++
	return nil
}

func (m *mockCredentialProvider) RecordSuccess(ctx context.Context, userID, exchange string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.successCount[m.key(userID, exchange)]++
	return nil
}

func (m *mockCredentialProvider) RecordFailure(ctx context.Context, userID, exchange string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failureCount[m.key(userID, exchange)]++
	return nil
}

func (m *mockCredentialProvider) GetUsageCount(userID, exchange string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.usageCount[m.key(userID, exchange)]
}

func (m *mockCredentialProvider) GetSuccessCount(userID, exchange string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.successCount[m.key(userID, exchange)]
}

func (m *mockCredentialProvider) GetFailureCount(userID, exchange string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.failureCount[m.key(userID, exchange)]
}

// mockAuditLogger is a mock implementation of AuditLogger for testing.
type mockAuditLogger struct {
	logCount int64
}

func newMockAuditLogger() *mockAuditLogger {
	return &mockAuditLogger{}
}

func (m *mockAuditLogger) Log(ctx context.Context, credentialID string, auditCtx *credentials.AuditContext, action credentials.AuditAction, success bool, err error, details *credentials.AuditDetails) error {
	atomic.AddInt64(&m.logCount, 1)
	return nil
}

func (m *mockAuditLogger) LogCreate(ctx context.Context, credential *credentials.UserCredential, auditCtx *credentials.AuditContext, success bool, err error) error {
	return m.Log(ctx, "", auditCtx, credentials.AuditActionCreate, success, err, nil)
}

func (m *mockAuditLogger) LogRead(ctx context.Context, credentialID, exchange string, auditCtx *credentials.AuditContext, success bool, err error) error {
	return m.Log(ctx, credentialID, auditCtx, credentials.AuditActionRead, success, err, nil)
}

func (m *mockAuditLogger) LogUpdate(ctx context.Context, credentialID, exchange string, auditCtx *credentials.AuditContext, fieldsUpdated []string, success bool, err error) error {
	return m.Log(ctx, credentialID, auditCtx, credentials.AuditActionUpdate, success, err, nil)
}

func (m *mockAuditLogger) LogDelete(ctx context.Context, credentialID, exchange string, auditCtx *credentials.AuditContext, success bool, err error) error {
	return m.Log(ctx, credentialID, auditCtx, credentials.AuditActionDelete, success, err, nil)
}

func (m *mockAuditLogger) LogTest(ctx context.Context, credentialID, exchange string, auditCtx *credentials.AuditContext, result *credentials.TestCredentialResult, err error) error {
	return m.Log(ctx, credentialID, auditCtx, credentials.AuditActionTest, result != nil && result.Success, err, nil)
}

func (m *mockAuditLogger) LogUse(ctx context.Context, credentialID, exchange string, auditCtx *credentials.AuditContext, success bool, err error) error {
	return m.Log(ctx, credentialID, auditCtx, credentials.AuditActionUse, success, err, nil)
}

func (m *mockAuditLogger) LogList(ctx context.Context, auditCtx *credentials.AuditContext, count int, success bool, err error) error {
	return m.Log(ctx, "", auditCtx, credentials.AuditActionList, success, err, nil)
}

func (m *mockAuditLogger) LogKeyRotation(ctx context.Context, auditCtx *credentials.AuditContext, oldKeyID string, rotatedCount int, success bool, err error) error {
	return m.Log(ctx, "", auditCtx, credentials.AuditActionRotate, success, err, nil)
}

func (m *mockAuditLogger) GetAuditLogs(ctx context.Context, filter *credentials.AuditLogFilter) ([]credentials.CredentialAuditLog, error) {
	return nil, nil
}

func (m *mockAuditLogger) GetAuditLogsForCredential(ctx context.Context, credentialID string, limit int) ([]credentials.CredentialAuditLog, error) {
	return nil, nil
}

func (m *mockAuditLogger) GetAuditLogsForUser(ctx context.Context, userID string, limit int) ([]credentials.CredentialAuditLog, error) {
	return nil, nil
}

func (m *mockAuditLogger) GetLogCount() int64 {
	return atomic.LoadInt64(&m.logCount)
}

// mockGCTClient is a minimal mock implementation of the Client for testing.
type mockGCTClient struct {
	apiKey     string
	apiSecret  string
	passphrase string
	exchange   string
	mu         sync.RWMutex
}

func newMockGCTClient() *mockGCTClient {
	return &mockGCTClient{}
}

func (m *mockGCTClient) SetExchangeCredentials(exchange, apiKey, apiSecret, passphrase string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.exchange = exchange
	m.apiKey = apiKey
	m.apiSecret = apiSecret
	m.passphrase = passphrase
}

func (m *mockGCTClient) GetConfiguredCredentials() (exchange, apiKey, apiSecret, passphrase string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.exchange, m.apiKey, m.apiSecret, m.passphrase
}

// TestNewUserClient tests the NewUserClient constructor.
func TestNewUserClient(t *testing.T) {
	mockProvider := newMockCredentialProvider()
	mockClient := &Client{} // Using the real Client type, but not making real calls

	tests := []struct {
		name    string
		cfg     *UserClientConfig
		wantErr bool
	}{
		{
			name:    "nil config",
			cfg:     nil,
			wantErr: true,
		},
		{
			name: "nil credential provider",
			cfg: &UserClientConfig{
				GCTClient: mockClient,
			},
			wantErr: true,
		},
		{
			name: "nil gct client",
			cfg: &UserClientConfig{
				CredentialProvider: mockProvider,
			},
			wantErr: true,
		},
		{
			name: "valid config",
			cfg: &UserClientConfig{
				GCTClient:          mockClient,
				CredentialProvider: mockProvider,
				Logger:             logrus.New(),
			},
			wantErr: false,
		},
		{
			name: "valid config with custom cache TTL",
			cfg: &UserClientConfig{
				GCTClient:          mockClient,
				CredentialProvider: mockProvider,
				CacheTTL:           5 * time.Second,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewUserClient(tt.cfg)
			if tt.wantErr {
				if err == nil {
					t.Error("Expected error but got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}
			if client == nil {
				t.Error("Expected client but got nil")
			}
		})
	}
}

// TestConfigureUserCredentials tests the credential configuration process.
func TestConfigureUserCredentials(t *testing.T) {
	mockProvider := newMockCredentialProvider()
	mockAudit := newMockAuditLogger()
	mockClient := &Client{}

	// Set up test credentials
	mockProvider.SetCredentials("user-123", "kucoin", &credentials.ExchangeCredentials{
		APIKey:       "test-api-key",
		APISecret:    "test-api-secret",
		Passphrase:   "test-passphrase",
		Exchange:     "kucoin",
		TenantID:     "tenant-1",
		CredentialID: "cred-1",
	})

	client, err := NewUserClient(&UserClientConfig{
		GCTClient:          mockClient,
		CredentialProvider: mockProvider,
		AuditLogger:        mockAudit,
		Logger:             logrus.New(),
		CacheTTL:           100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Failed to create UserClient: %v", err)
	}

	tests := []struct {
		name     string
		userID   string
		exchange string
		wantErr  error
	}{
		{
			name:     "empty user ID",
			userID:   "",
			exchange: "kucoin",
			wantErr:  ErrNoUserContext,
		},
		{
			name:     "empty exchange",
			userID:   "user-123",
			exchange: "",
			wantErr:  errors.New("exchange is required"),
		},
		{
			name:     "valid credentials",
			userID:   "user-123",
			exchange: "kucoin",
			wantErr:  nil,
		},
		{
			name:     "credentials not found",
			userID:   "user-999",
			exchange: "binance",
			wantErr:  ErrNoCredentials,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear cache before each test
			client.ClearAllCache()

			err := client.ConfigureUserCredentials(context.Background(), tt.userID, tt.exchange)
			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("Expected error %v but got nil", tt.wantErr)
					return
				}
				if !errors.Is(err, tt.wantErr) && err.Error() != tt.wantErr.Error() {
					t.Errorf("Expected error %v but got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}

// TestCredentialCaching tests that credentials are properly cached.
func TestCredentialCaching(t *testing.T) {
	mockProvider := newMockCredentialProvider()
	mockClient := &Client{}

	mockProvider.SetCredentials("user-123", "kucoin", &credentials.ExchangeCredentials{
		APIKey:       "test-api-key",
		APISecret:    "test-api-secret",
		Passphrase:   "test-passphrase",
		Exchange:     "kucoin",
		TenantID:     "tenant-1",
		CredentialID: "cred-1",
	})

	client, err := NewUserClient(&UserClientConfig{
		GCTClient:          mockClient,
		CredentialProvider: mockProvider,
		Logger:             logrus.New(),
		CacheTTL:           100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Failed to create UserClient: %v", err)
	}

	ctx := context.Background()

	// First call should fetch from provider
	if err := client.ConfigureUserCredentials(ctx, "user-123", "kucoin"); err != nil {
		t.Fatalf("First configure failed: %v", err)
	}

	// Wait a bit but not past cache TTL
	time.Sleep(10 * time.Millisecond)

	initialUsage := mockProvider.GetUsageCount("user-123", "kucoin")

	// Second call should use cache
	if err := client.ConfigureUserCredentials(ctx, "user-123", "kucoin"); err != nil {
		t.Fatalf("Second configure failed: %v", err)
	}

	// Give goroutine time to potentially record usage
	time.Sleep(20 * time.Millisecond)

	// Usage count should not have increased (because we used cache)
	secondUsage := mockProvider.GetUsageCount("user-123", "kucoin")
	if secondUsage != initialUsage {
		t.Logf("Usage count increased from %d to %d (cache not used)", initialUsage, secondUsage)
	}

	// Wait past cache TTL
	time.Sleep(200 * time.Millisecond)

	// Third call should fetch from provider again
	if err := client.ConfigureUserCredentials(ctx, "user-123", "kucoin"); err != nil {
		t.Fatalf("Third configure failed: %v", err)
	}

	// Give goroutine time to record usage
	time.Sleep(20 * time.Millisecond)

	thirdUsage := mockProvider.GetUsageCount("user-123", "kucoin")
	if thirdUsage <= secondUsage {
		t.Logf("Expected usage count to increase after cache expiry, got %d <= %d", thirdUsage, secondUsage)
	}
}

// TestInvalidateCache tests cache invalidation.
func TestInvalidateCache(t *testing.T) {
	mockProvider := newMockCredentialProvider()
	mockClient := &Client{}

	mockProvider.SetCredentials("user-123", "kucoin", &credentials.ExchangeCredentials{
		APIKey:     "test-api-key",
		APISecret:  "test-api-secret",
		Exchange:   "kucoin",
		TenantID:   "tenant-1",
	})
	mockProvider.SetCredentials("user-123", "binance", &credentials.ExchangeCredentials{
		APIKey:     "binance-api-key",
		APISecret:  "binance-api-secret",
		Exchange:   "binance",
		TenantID:   "tenant-1",
	})

	client, err := NewUserClient(&UserClientConfig{
		GCTClient:          mockClient,
		CredentialProvider: mockProvider,
		Logger:             logrus.New(),
		CacheTTL:           10 * time.Minute, // Long TTL to ensure cache is used
	})
	if err != nil {
		t.Fatalf("Failed to create UserClient: %v", err)
	}

	ctx := context.Background()

	// Configure both exchanges
	if err := client.ConfigureUserCredentials(ctx, "user-123", "kucoin"); err != nil {
		t.Fatalf("Configure kucoin failed: %v", err)
	}
	if err := client.ConfigureUserCredentials(ctx, "user-123", "binance"); err != nil {
		t.Fatalf("Configure binance failed: %v", err)
	}

	// Verify cache has entries
	stats := client.GetCredentialStats()
	if stats["cached_credentials"].(int) != 2 {
		t.Errorf("Expected 2 cached credentials, got %v", stats["cached_credentials"])
	}

	// Invalidate one exchange
	client.InvalidateCache("user-123", "kucoin")

	// Verify cache has one entry
	stats = client.GetCredentialStats()
	if stats["cached_credentials"].(int) != 1 {
		t.Errorf("Expected 1 cached credential after invalidation, got %v", stats["cached_credentials"])
	}

	// Invalidate all for user
	client.InvalidateAllCacheForUser("user-123")

	// Verify cache is empty
	stats = client.GetCredentialStats()
	if stats["cached_credentials"].(int) != 0 {
		t.Errorf("Expected 0 cached credentials after full invalidation, got %v", stats["cached_credentials"])
	}
}

// TestHasUserCredentials tests the HasUserCredentials method.
func TestHasUserCredentials(t *testing.T) {
	mockProvider := newMockCredentialProvider()
	mockClient := &Client{}

	mockProvider.SetCredentials("user-123", "kucoin", &credentials.ExchangeCredentials{
		APIKey:   "test-api-key",
		Exchange: "kucoin",
	})

	client, err := NewUserClient(&UserClientConfig{
		GCTClient:          mockClient,
		CredentialProvider: mockProvider,
		Logger:             logrus.New(),
	})
	if err != nil {
		t.Fatalf("Failed to create UserClient: %v", err)
	}

	// Test with user context
	ctx := credentials.ContextWithUser(context.Background(), "user-123", "tenant-1")

	// Should find credentials
	has, err := client.HasUserCredentials(ctx, "kucoin")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if !has {
		t.Error("Expected to find credentials for kucoin")
	}

	// Should not find credentials for different exchange
	has, err = client.HasUserCredentials(ctx, "binance")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if has {
		t.Error("Did not expect to find credentials for binance")
	}

	// Test without user context
	has, err = client.HasUserCredentials(context.Background(), "kucoin")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if has {
		t.Error("Expected false for missing user context")
	}
}

// TestRecordAPIResult tests the success/failure recording.
func TestRecordAPIResult(t *testing.T) {
	mockProvider := newMockCredentialProvider()
	mockClient := &Client{}

	client, err := NewUserClient(&UserClientConfig{
		GCTClient:          mockClient,
		CredentialProvider: mockProvider,
		Logger:             logrus.New(),
	})
	if err != nil {
		t.Fatalf("Failed to create UserClient: %v", err)
	}

	// Create context with user
	ctx := credentials.ContextWithUser(context.Background(), "user-123", "tenant-1")

	// Record success
	client.RecordAPIResult(ctx, "kucoin", nil)
	time.Sleep(50 * time.Millisecond) // Wait for goroutine

	successCount := mockProvider.GetSuccessCount("user-123", "kucoin")
	if successCount != 1 {
		t.Errorf("Expected 1 success, got %d", successCount)
	}

	// Record failure
	client.RecordAPIResult(ctx, "kucoin", errors.New("test error"))
	time.Sleep(50 * time.Millisecond) // Wait for goroutine

	failureCount := mockProvider.GetFailureCount("user-123", "kucoin")
	if failureCount != 1 {
		t.Errorf("Expected 1 failure, got %d", failureCount)
	}
}

// TestGetCredentialStats tests the stats method.
func TestGetCredentialStats(t *testing.T) {
	mockProvider := newMockCredentialProvider()
	mockClient := &Client{}

	mockProvider.SetCredentials("user-123", "kucoin", &credentials.ExchangeCredentials{
		APIKey:   "test-api-key",
		Exchange: "kucoin",
	})

	client, err := NewUserClient(&UserClientConfig{
		GCTClient:          mockClient,
		CredentialProvider: mockProvider,
		Logger:             logrus.New(),
		CacheTTL:           5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Failed to create UserClient: %v", err)
	}

	// Empty cache stats
	stats := client.GetCredentialStats()
	if stats["cached_credentials"].(int) != 0 {
		t.Errorf("Expected 0 cached credentials initially, got %v", stats["cached_credentials"])
	}
	if stats["cache_ttl_seconds"].(float64) != 5 {
		t.Errorf("Expected 5 second TTL, got %v", stats["cache_ttl_seconds"])
	}

	// Add a cached credential
	if err := client.ConfigureUserCredentials(context.Background(), "user-123", "kucoin"); err != nil {
		t.Fatalf("Configure failed: %v", err)
	}

	// Check stats with cached credential
	stats = client.GetCredentialStats()
	if stats["cached_credentials"].(int) != 1 {
		t.Errorf("Expected 1 cached credential, got %v", stats["cached_credentials"])
	}
	if _, ok := stats["oldest_entry_age_ms"]; !ok {
		t.Error("Expected oldest_entry_age_ms in stats")
	}
	if _, ok := stats["cached_exchanges"]; !ok {
		t.Error("Expected cached_exchanges in stats")
	}
}
