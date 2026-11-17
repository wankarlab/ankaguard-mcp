package repositories

import (
	"context"
	"testing"
	"time"

	"github.com/rantipay/trading-mcp/internal/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	err = db.AutoMigrate(&domain.APICost{})
	require.NoError(t, err)

	return db
}

func TestAPICostRepository_Create(t *testing.T) {
	db := setupTestDB(t)
	repo := NewAPICostRepository(db)
	ctx := context.Background()

	cost := &domain.APICost{
		Provider:     "openai",
		Cost:         decimal.NewFromFloat(0.05),
		CallType:     "market_analysis",
		InputTokens:  500,
		OutputTokens: 200,
		Model:        "gpt-4",
		Success:      true,
		CreatedAt:    time.Now(),
	}

	err := repo.Create(ctx, cost)
	assert.NoError(t, err)
	assert.NotZero(t, cost.ID)
}

func TestAPICostRepository_GetDailyCost(t *testing.T) {
	db := setupTestDB(t)
	repo := NewAPICostRepository(db)
	ctx := context.Background()

	today := time.Now()

	// Create test costs
	costs := []*domain.APICost{
		{
			Provider:  "openai",
			Cost:      decimal.NewFromFloat(0.05),
			CreatedAt: today,
		},
		{
			Provider:  "openai",
			Cost:      decimal.NewFromFloat(0.03),
			CreatedAt: today,
		},
		{
			Provider:  "anthropic",
			Cost:      decimal.NewFromFloat(0.02),
			CreatedAt: today,
		},
	}

	for _, cost := range costs {
		err := repo.Create(ctx, cost)
		require.NoError(t, err)
	}

	// Test OpenAI daily cost
	dailyCost, err := repo.GetDailyCost(ctx, "openai", today)
	assert.NoError(t, err)
	expected := decimal.NewFromFloat(0.08)
	assert.True(t, dailyCost.Equal(expected), "Expected %s, got %s", expected, dailyCost)

	// Test Anthropic daily cost
	dailyCost, err = repo.GetDailyCost(ctx, "anthropic", today)
	assert.NoError(t, err)
	expected = decimal.NewFromFloat(0.02)
	assert.True(t, dailyCost.Equal(expected), "Expected %s, got %s", expected, dailyCost)
}

func TestAPICostRepository_GetCallCount(t *testing.T) {
	db := setupTestDB(t)
	repo := NewAPICostRepository(db)
	ctx := context.Background()

	today := time.Now()

	// Create test costs
	for i := 0; i < 5; i++ {
		cost := &domain.APICost{
			Provider:  "openai",
			Cost:      decimal.NewFromFloat(0.01),
			CreatedAt: today,
		}
		err := repo.Create(ctx, cost)
		require.NoError(t, err)
	}

	count, err := repo.GetCallCount(ctx, "openai", today)
	assert.NoError(t, err)
	assert.Equal(t, int64(5), count)
}

func TestAPICostRepository_GetCostBreakdown(t *testing.T) {
	db := setupTestDB(t)
	repo := NewAPICostRepository(db)
	ctx := context.Background()

	today := time.Now()
	yesterday := today.AddDate(0, 0, -1)

	// Create test costs
	costs := []*domain.APICost{
		{Provider: "openai", Cost: decimal.NewFromFloat(0.05), CreatedAt: today},
		{Provider: "openai", Cost: decimal.NewFromFloat(0.03), CreatedAt: yesterday},
		{Provider: "anthropic", Cost: decimal.NewFromFloat(0.02), CreatedAt: today},
	}

	for _, cost := range costs {
		err := repo.Create(ctx, cost)
		require.NoError(t, err)
	}

	// Get breakdown for last 2 days
	startDate := yesterday.Add(-time.Hour)
	endDate := today.Add(time.Hour)
	breakdown, err := repo.GetCostBreakdown(ctx, startDate, endDate)
	assert.NoError(t, err)
	assert.NotEmpty(t, breakdown)
	assert.Contains(t, breakdown, "openai")
	assert.Contains(t, breakdown, "anthropic")
}

func TestAPICostRepository_GetMonthlyCost(t *testing.T) {
	db := setupTestDB(t)
	repo := NewAPICostRepository(db)
	ctx := context.Background()

	now := time.Now()
	year := now.Year()
	month := now.Month()

	// Use fixed dates within the same month to avoid month-boundary issues when
	// the test runs in the first few days of the month (e.g. day 4: -5d → prev month).
	day1 := time.Date(year, month, 1, 12, 0, 0, 0, time.UTC)
	day2 := time.Date(year, month, 2, 12, 0, 0, 0, time.UTC)
	day3 := time.Date(year, month, 3, 12, 0, 0, 0, time.UTC)
	_ = now

	costs := []*domain.APICost{
		{Provider: "openai", Cost: decimal.NewFromFloat(1.50), CreatedAt: day1},
		{Provider: "openai", Cost: decimal.NewFromFloat(2.30), CreatedAt: day2},
		{Provider: "openai", Cost: decimal.NewFromFloat(0.80), CreatedAt: day3},
	}

	for _, cost := range costs {
		err := repo.Create(ctx, cost)
		require.NoError(t, err)
	}

	monthlyCost, err := repo.GetMonthlyCost(ctx, "openai", year, month)
	assert.NoError(t, err)
	expected := decimal.NewFromFloat(4.60)
	assert.True(t, monthlyCost.Equal(expected), "Expected %s, got %s", expected, monthlyCost)
}

func TestAPICostRepository_GetTotalCostByDateRange(t *testing.T) {
	db := setupTestDB(t)
	repo := NewAPICostRepository(db)
	ctx := context.Background()

	today := time.Now()

	// Create test costs from multiple providers
	costs := []*domain.APICost{
		{Provider: "openai", Cost: decimal.NewFromFloat(1.00), CreatedAt: today},
		{Provider: "anthropic", Cost: decimal.NewFromFloat(0.50), CreatedAt: today},
		{Provider: "google", Cost: decimal.NewFromFloat(0.30), CreatedAt: today},
	}

	for _, cost := range costs {
		err := repo.Create(ctx, cost)
		require.NoError(t, err)
	}

	startDate := today.Add(-time.Hour)
	endDate := today.Add(time.Hour)
	totalCost, err := repo.GetTotalCostByDateRange(ctx, startDate, endDate)
	assert.NoError(t, err)
	expected := decimal.NewFromFloat(1.80)
	assert.True(t, totalCost.Equal(expected), "Expected %s, got %s", expected, totalCost)
}
