package cost

import (
	"context"
	"testing"
	"time"

	"github.com/rantipay/trading-mcp/internal/domain"
	"github.com/shopspring/decimal"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockAPICostRepository is a mock implementation of APICostRepository
type MockAPICostRepository struct {
	mock.Mock
}

func (m *MockAPICostRepository) Create(ctx context.Context, cost *domain.APICost) error {
	args := m.Called(ctx, cost)
	return args.Error(0)
}

func (m *MockAPICostRepository) GetDailyCost(ctx context.Context, provider string, date time.Time) (decimal.Decimal, error) {
	args := m.Called(ctx, provider, date)
	return args.Get(0).(decimal.Decimal), args.Error(1)
}

func (m *MockAPICostRepository) GetMonthlyCost(ctx context.Context, provider string, year int, month time.Month) (decimal.Decimal, error) {
	args := m.Called(ctx, provider, year, month)
	return args.Get(0).(decimal.Decimal), args.Error(1)
}

func (m *MockAPICostRepository) GetTotalCostByDateRange(ctx context.Context, startDate, endDate time.Time) (decimal.Decimal, error) {
	args := m.Called(ctx, startDate, endDate)
	return args.Get(0).(decimal.Decimal), args.Error(1)
}

func (m *MockAPICostRepository) GetCostBreakdown(ctx context.Context, startDate, endDate time.Time) (map[string]map[string]decimal.Decimal, error) {
	args := m.Called(ctx, startDate, endDate)
	return args.Get(0).(map[string]map[string]decimal.Decimal), args.Error(1)
}

func (m *MockAPICostRepository) GetCallCount(ctx context.Context, provider string, date time.Time) (int64, error) {
	args := m.Called(ctx, provider, date)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockAPICostRepository) GetCostsByProvider(ctx context.Context, provider string, startDate, endDate time.Time) ([]*domain.APICost, error) {
	args := m.Called(ctx, provider, startDate, endDate)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.APICost), args.Error(1)
}

func TestNewManager(t *testing.T) {
	repo := new(MockAPICostRepository)
	config := DefaultConfig()
	log := logrus.New()

	manager := NewManager(repo, config, log)

	assert.NotNil(t, manager)
	assert.NotNil(t, manager.repo)
	assert.NotNil(t, manager.config)
	assert.NotNil(t, manager.dailyCache)
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	assert.NotNil(t, config)
	assert.NotNil(t, config.DailyLimits)
	assert.Equal(t, 5.0, config.DailyLimits["openai"])
	assert.Equal(t, 3.0, config.DailyLimits["anthropic"])
	assert.Equal(t, 2.0, config.DailyLimits["google"])
	assert.Equal(t, 80.0, config.AlertThresholdPercent)
	assert.True(t, config.NotifyOnLimit)
	assert.True(t, config.EnforceLimits)
}

func TestTrackAPICall_Success(t *testing.T) {
	repo := new(MockAPICostRepository)
	config := DefaultConfig()
	log := logrus.New()
	log.SetLevel(logrus.FatalLevel) // Silence logs
	manager := NewManager(repo, config, log)

	ctx := context.Background()

	// Mock repository calls - TrackAPICall will call Create and then GetBudgetStatus
	repo.On("Create", ctx, mock.Anything).Return(nil).Once()
	// GetBudgetStatus calls GetDailyCost and GetCallCount
	repo.On("GetDailyCost", ctx, "openai", mock.Anything).Return(decimal.NewFromFloat(0.05), nil).Maybe()
	repo.On("GetCallCount", ctx, "openai", mock.Anything).Return(int64(1), nil).Maybe()

	// Track API call
	err := manager.TrackAPICall(ctx, "openai", 0.05, 500, 200)

	require.NoError(t, err)
	// Only assert Create was called
	repo.AssertCalled(t, "Create", ctx, mock.Anything)

	// Verify cache was updated
	assert.Equal(t, 1, len(manager.dailyCache))
}

func TestTrackAPICall_NegativeCost(t *testing.T) {
	repo := new(MockAPICostRepository)
	config := DefaultConfig()
	log := logrus.New()
	manager := NewManager(repo, config, log)

	ctx := context.Background()

	// Try to track negative cost
	err := manager.TrackAPICall(ctx, "openai", -0.05, 500, 200)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be negative")
}

func TestCheckBudget_UnderLimit(t *testing.T) {
	repo := new(MockAPICostRepository)
	config := DefaultConfig()
	log := logrus.New()
	manager := NewManager(repo, config, log)

	ctx := context.Background()

	// Mock: Current cost is $1.00, limit is $5.00
	repo.On("GetDailyCost", ctx, "openai", mock.Anything).Return(decimal.NewFromFloat(1.0), nil)
	repo.On("GetCallCount", ctx, "openai", mock.Anything).Return(int64(10), nil)

	// Check budget
	allowed, err := manager.CheckBudget(ctx, "openai")

	require.NoError(t, err)
	assert.True(t, allowed)
	repo.AssertExpectations(t)
}

func TestCheckBudget_OverLimit(t *testing.T) {
	repo := new(MockAPICostRepository)
	config := DefaultConfig()
	log := logrus.New()
	manager := NewManager(repo, config, log)

	ctx := context.Background()

	// Mock: Current cost is $6.00, limit is $5.00
	repo.On("GetDailyCost", ctx, "openai", mock.Anything).Return(decimal.NewFromFloat(6.0), nil)
	repo.On("GetCallCount", ctx, "openai", mock.Anything).Return(int64(100), nil)

	// Check budget
	allowed, err := manager.CheckBudget(ctx, "openai")

	require.NoError(t, err)
	assert.False(t, allowed)
	repo.AssertExpectations(t)
}

func TestCheckBudget_LimitsNotEnforced(t *testing.T) {
	repo := new(MockAPICostRepository)
	config := DefaultConfig()
	config.EnforceLimits = false // Disable enforcement
	log := logrus.New()
	manager := NewManager(repo, config, log)

	ctx := context.Background()

	// Check budget (should always allow when not enforced)
	allowed, err := manager.CheckBudget(ctx, "openai")

	require.NoError(t, err)
	assert.True(t, allowed)
}

func TestGetBudgetStatus_UnderLimit(t *testing.T) {
	repo := new(MockAPICostRepository)
	config := DefaultConfig()
	log := logrus.New()
	manager := NewManager(repo, config, log)

	ctx := context.Background()

	// Mock: Current cost is $2.00, limit is $5.00 (40% used)
	repo.On("GetDailyCost", ctx, "openai", mock.Anything).Return(decimal.NewFromFloat(2.0), nil)
	repo.On("GetCallCount", ctx, "openai", mock.Anything).Return(int64(20), nil)

	// Get budget status
	status, err := manager.GetBudgetStatus(ctx, "openai")

	require.NoError(t, err)
	assert.Equal(t, "openai", status.Provider)
	assert.Equal(t, decimal.NewFromFloat(5.0), status.DailyLimit)
	assert.Equal(t, decimal.NewFromFloat(2.0), status.CurrentCost)
	assert.Equal(t, decimal.NewFromFloat(3.0), status.RemainingBudget)
	assert.InDelta(t, 40.0, status.UsedPercent, 0.1)
	assert.Equal(t, int64(20), status.CallCount)
	assert.False(t, status.IsNearLimit) // 40% < 80%
	assert.False(t, status.IsOverLimit)
	repo.AssertExpectations(t)
}

func TestGetBudgetStatus_NearLimit(t *testing.T) {
	repo := new(MockAPICostRepository)
	config := DefaultConfig()
	log := logrus.New()
	manager := NewManager(repo, config, log)

	ctx := context.Background()

	// Mock: Current cost is $4.50, limit is $5.00 (90% used)
	repo.On("GetDailyCost", ctx, "openai", mock.Anything).Return(decimal.NewFromFloat(4.5), nil)
	repo.On("GetCallCount", ctx, "openai", mock.Anything).Return(int64(50), nil)

	// Get budget status
	status, err := manager.GetBudgetStatus(ctx, "openai")

	require.NoError(t, err)
	assert.InDelta(t, 90.0, status.UsedPercent, 0.1)
	assert.True(t, status.IsNearLimit)  // 90% >= 80%
	assert.False(t, status.IsOverLimit) // 90% < 100%
	repo.AssertExpectations(t)
}

func TestGetBudgetStatus_OverLimit(t *testing.T) {
	repo := new(MockAPICostRepository)
	config := DefaultConfig()
	log := logrus.New()
	manager := NewManager(repo, config, log)

	ctx := context.Background()

	// Mock: Current cost is $6.00, limit is $5.00 (120% used)
	repo.On("GetDailyCost", ctx, "openai", mock.Anything).Return(decimal.NewFromFloat(6.0), nil)
	repo.On("GetCallCount", ctx, "openai", mock.Anything).Return(int64(100), nil)

	// Get budget status
	status, err := manager.GetBudgetStatus(ctx, "openai")

	require.NoError(t, err)
	assert.InDelta(t, 120.0, status.UsedPercent, 0.1)
	assert.True(t, status.IsNearLimit)
	assert.True(t, status.IsOverLimit) // 120% >= 100%
	repo.AssertExpectations(t)
}

func TestGetBudgetStatus_NoLimitConfigured(t *testing.T) {
	repo := new(MockAPICostRepository)
	config := DefaultConfig()
	log := logrus.New()
	manager := NewManager(repo, config, log)

	ctx := context.Background()

	// Get status for provider with no limit configured
	status, err := manager.GetBudgetStatus(ctx, "unknown-provider")

	require.NoError(t, err)
	assert.Equal(t, "unknown-provider", status.Provider)
	assert.False(t, status.IsNearLimit)
	assert.False(t, status.IsOverLimit)
	// Should have effectively unlimited budget
	assert.True(t, status.DailyLimit.GreaterThan(decimal.NewFromInt(1000)))
}

func TestGetAllBudgetStatuses(t *testing.T) {
	repo := new(MockAPICostRepository)
	config := DefaultConfig()
	log := logrus.New()
	manager := NewManager(repo, config, log)

	ctx := context.Background()

	// Mock calls for all providers
	for provider := range config.DailyLimits {
		repo.On("GetDailyCost", ctx, provider, mock.Anything).Return(decimal.NewFromFloat(1.0), nil)
		repo.On("GetCallCount", ctx, provider, mock.Anything).Return(int64(10), nil)
	}

	// Get all statuses
	statuses, err := manager.GetAllBudgetStatuses(ctx)

	require.NoError(t, err)
	assert.Equal(t, len(config.DailyLimits), len(statuses))
	assert.Contains(t, statuses, "openai")
	assert.Contains(t, statuses, "anthropic")
	assert.Contains(t, statuses, "google")
	repo.AssertExpectations(t)
}

func TestResetDailyCache(t *testing.T) {
	repo := new(MockAPICostRepository)
	config := DefaultConfig()
	log := logrus.New()
	manager := NewManager(repo, config, log)

	// Add some cache entries
	manager.dailyCache["test_key"] = &DailyCostCache{
		Provider:  "openai",
		Date:      time.Now(),
		TotalCost: decimal.NewFromFloat(1.0),
		UpdatedAt: time.Now(),
	}

	assert.Equal(t, 1, len(manager.dailyCache))

	// Reset cache
	manager.ResetDailyCache()

	assert.Equal(t, 0, len(manager.dailyCache))
}

func TestUpdateCache(t *testing.T) {
	repo := new(MockAPICostRepository)
	config := DefaultConfig()
	log := logrus.New()
	manager := NewManager(repo, config, log)

	// Update cache
	manager.updateCache("openai", decimal.NewFromFloat(0.05))

	// Verify cache entry was created
	assert.Equal(t, 1, len(manager.dailyCache))

	// Update again
	manager.updateCache("openai", decimal.NewFromFloat(0.03))

	// Should still be 1 entry, but cost should be summed
	assert.Equal(t, 1, len(manager.dailyCache))

	// Find the cache entry
	var found *DailyCostCache
	for _, entry := range manager.dailyCache {
		if entry.Provider == "openai" {
			found = entry
			break
		}
	}

	require.NotNil(t, found)
	assert.Equal(t, decimal.NewFromFloat(0.08), found.TotalCost)
	assert.Equal(t, int64(2), found.CallCount)
}

func BenchmarkTrackAPICall(b *testing.B) {
	repo := new(MockAPICostRepository)
	config := DefaultConfig()
	log := logrus.New()
	manager := NewManager(repo, config, log)

	ctx := context.Background()

	// Mock repository calls
	repo.On("Create", ctx, mock.Anything).Return(nil)
	repo.On("GetDailyCost", ctx, "openai", mock.Anything).Return(decimal.NewFromFloat(1.0), nil)
	repo.On("GetCallCount", ctx, "openai", mock.Anything).Return(int64(10), nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.TrackAPICall(ctx, "openai", 0.05, 500, 200)
	}
}

func BenchmarkCheckBudget(b *testing.B) {
	repo := new(MockAPICostRepository)
	config := DefaultConfig()
	log := logrus.New()
	manager := NewManager(repo, config, log)

	ctx := context.Background()

	// Mock repository calls
	repo.On("GetDailyCost", ctx, "openai", mock.Anything).Return(decimal.NewFromFloat(1.0), nil)
	repo.On("GetCallCount", ctx, "openai", mock.Anything).Return(int64(10), nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.CheckBudget(ctx, "openai")
	}
}
