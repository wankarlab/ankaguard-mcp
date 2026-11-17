package repositories

import (
	"context"
	"fmt"
	"time"

	"github.com/rantipay/trading-mcp/internal/domain"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// APICostRepository handles API cost data persistence
type APICostRepository interface {
	// Create inserts a new API cost record
	Create(ctx context.Context, cost *domain.APICost) error

	// GetDailyCost returns total cost for a provider on a specific date
	GetDailyCost(ctx context.Context, provider string, date time.Time) (decimal.Decimal, error)

	// GetMonthlyCost returns total cost for a provider in a specific month
	GetMonthlyCost(ctx context.Context, provider string, year int, month time.Month) (decimal.Decimal, error)

	// GetCostBreakdown returns cost breakdown by provider and day
	GetCostBreakdown(ctx context.Context, startDate, endDate time.Time) (map[string]map[string]decimal.Decimal, error)

	// GetTotalCostByDateRange returns total cost across all providers in date range
	GetTotalCostByDateRange(ctx context.Context, startDate, endDate time.Time) (decimal.Decimal, error)

	// GetCallCount returns number of API calls for a provider on a specific date
	GetCallCount(ctx context.Context, provider string, date time.Time) (int64, error)

	// GetCostsByProvider returns all costs for a specific provider in date range
	GetCostsByProvider(ctx context.Context, provider string, startDate, endDate time.Time) ([]*domain.APICost, error)
}

type apiCostRepository struct {
	db *gorm.DB
}

// NewAPICostRepository creates a new API cost repository
func NewAPICostRepository(db *gorm.DB) APICostRepository {
	return &apiCostRepository{db: db}
}

// Create inserts a new API cost record
func (r *apiCostRepository) Create(ctx context.Context, cost *domain.APICost) error {
	if cost == nil {
		return fmt.Errorf("cost cannot be nil")
	}

	result := r.db.WithContext(ctx).Create(cost)
	if result.Error != nil {
		return fmt.Errorf("failed to create API cost: %w", result.Error)
	}

	return nil
}

// GetDailyCost returns total cost for a provider on a specific date
func (r *apiCostRepository) GetDailyCost(ctx context.Context, provider string, date time.Time) (decimal.Decimal, error) {
	startOfDay := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())
	endOfDay := startOfDay.Add(24 * time.Hour)

	var totalCost decimal.Decimal
	result := r.db.WithContext(ctx).
		Model(&domain.APICost{}).
		Select("COALESCE(SUM(cost), 0)").
		Where("provider = ? AND created_at >= ? AND created_at < ?", provider, startOfDay, endOfDay).
		Scan(&totalCost)

	if result.Error != nil {
		return decimal.Zero, fmt.Errorf("failed to get daily cost: %w", result.Error)
	}

	return totalCost, nil
}

// GetMonthlyCost returns total cost for a provider in a specific month
func (r *apiCostRepository) GetMonthlyCost(ctx context.Context, provider string, year int, month time.Month) (decimal.Decimal, error) {
	startOfMonth := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	endOfMonth := startOfMonth.AddDate(0, 1, 0)

	var totalCost decimal.Decimal
	result := r.db.WithContext(ctx).
		Model(&domain.APICost{}).
		Select("COALESCE(SUM(cost), 0)").
		Where("provider = ? AND created_at >= ? AND created_at < ?", provider, startOfMonth, endOfMonth).
		Scan(&totalCost)

	if result.Error != nil {
		return decimal.Zero, fmt.Errorf("failed to get monthly cost: %w", result.Error)
	}

	return totalCost, nil
}

// GetCostBreakdown returns cost breakdown by provider and day
func (r *apiCostRepository) GetCostBreakdown(ctx context.Context, startDate, endDate time.Time) (map[string]map[string]decimal.Decimal, error) {
	type Result struct {
		Provider string
		Date     string
		Total    decimal.Decimal
	}

	var results []Result
	query := r.db.WithContext(ctx).
		Model(&domain.APICost{}).
		Select("provider, DATE(created_at) as date, SUM(cost) as total").
		Where("created_at >= ? AND created_at < ?", startDate, endDate).
		Group("provider, DATE(created_at)").
		Order("date DESC, provider")

	if err := query.Scan(&results).Error; err != nil {
		return nil, fmt.Errorf("failed to get cost breakdown: %w", err)
	}

	// Build nested map: provider -> date -> cost
	breakdown := make(map[string]map[string]decimal.Decimal)
	for _, result := range results {
		if _, exists := breakdown[result.Provider]; !exists {
			breakdown[result.Provider] = make(map[string]decimal.Decimal)
		}
		breakdown[result.Provider][result.Date] = result.Total
	}

	return breakdown, nil
}

// GetTotalCostByDateRange returns total cost across all providers in date range
func (r *apiCostRepository) GetTotalCostByDateRange(ctx context.Context, startDate, endDate time.Time) (decimal.Decimal, error) {
	var totalCost decimal.Decimal
	result := r.db.WithContext(ctx).
		Model(&domain.APICost{}).
		Select("COALESCE(SUM(cost), 0)").
		Where("created_at >= ? AND created_at < ?", startDate, endDate).
		Scan(&totalCost)

	if result.Error != nil {
		return decimal.Zero, fmt.Errorf("failed to get total cost: %w", result.Error)
	}

	return totalCost, nil
}

// GetCallCount returns number of API calls for a provider on a specific date
func (r *apiCostRepository) GetCallCount(ctx context.Context, provider string, date time.Time) (int64, error) {
	startOfDay := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())
	endOfDay := startOfDay.Add(24 * time.Hour)

	var count int64
	result := r.db.WithContext(ctx).
		Model(&domain.APICost{}).
		Where("provider = ? AND created_at >= ? AND created_at < ?", provider, startOfDay, endOfDay).
		Count(&count)

	if result.Error != nil {
		return 0, fmt.Errorf("failed to get call count: %w", result.Error)
	}

	return count, nil
}

// GetCostsByProvider returns all costs for a specific provider in date range
func (r *apiCostRepository) GetCostsByProvider(ctx context.Context, provider string, startDate, endDate time.Time) ([]*domain.APICost, error) {
	var costs []*domain.APICost
	result := r.db.WithContext(ctx).
		Where("provider = ? AND created_at >= ? AND created_at < ?", provider, startDate, endDate).
		Order("created_at DESC").
		Find(&costs)

	if result.Error != nil {
		return nil, fmt.Errorf("failed to get costs by provider: %w", result.Error)
	}

	return costs, nil
}
