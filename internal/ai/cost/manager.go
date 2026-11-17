package cost

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rantipay/trading-mcp/internal/domain"
	"github.com/rantipay/trading-mcp/internal/repositories"
	"github.com/shopspring/decimal"
	"github.com/sirupsen/logrus"
)

// Manager handles API cost tracking and budget management
type Manager struct {
	repo   repositories.APICostRepository
	config *Config
	log    *logrus.Logger

	// In-memory cache for daily costs (to avoid DB queries on every call)
	mu         sync.RWMutex
	dailyCache map[string]*DailyCostCache
}

// Config holds cost management configuration
type Config struct {
	// Daily budget limits per provider (in USD)
	DailyLimits map[string]float64

	// Alert threshold percentage (e.g., 80 = alert at 80% of budget)
	AlertThresholdPercent float64

	// Whether to notify when limit is reached
	NotifyOnLimit bool

	// Whether to enforce budget limits (block calls when exceeded)
	EnforceLimits bool
}

// DailyCostCache holds cached daily cost data
type DailyCostCache struct {
	Provider  string
	Date      time.Time
	TotalCost decimal.Decimal
	CallCount int64
	UpdatedAt time.Time
}

// CostBreakdown represents cost breakdown by provider and date
type CostBreakdown struct {
	Provider   string
	Date       string
	TotalCost  decimal.Decimal
	CallCount  int64
	AvgCost    decimal.Decimal
	Percentage float64 // Percentage of daily budget
}

// BudgetStatus represents current budget status for a provider
type BudgetStatus struct {
	Provider        string
	DailyLimit      decimal.Decimal
	CurrentCost     decimal.Decimal
	RemainingBudget decimal.Decimal
	UsedPercent     float64
	CallCount       int64
	IsNearLimit     bool // True if >= alert threshold
	IsOverLimit     bool // True if >= 100% of budget
}

// NewManager creates a new cost manager
func NewManager(repo repositories.APICostRepository, config *Config, log *logrus.Logger) *Manager {
	if config == nil {
		config = DefaultConfig()
	}

	return &Manager{
		repo:       repo,
		config:     config,
		log:        log,
		dailyCache: make(map[string]*DailyCostCache),
	}
}

// DefaultConfig returns default cost management configuration
func DefaultConfig() *Config {
	return &Config{
		DailyLimits: map[string]float64{
			"openai":      5.0, // $5/day
			"anthropic":   3.0, // $3/day
			"google":      2.0, // $2/day
			"cryptopanic": 1.0, // $1/day
		},
		AlertThresholdPercent: 80.0, // Alert at 80%
		NotifyOnLimit:         true,
		EnforceLimits:         true,
	}
}

// TrackAPICall records an API call cost
func (m *Manager) TrackAPICall(ctx context.Context, provider string, cost float64, inputTokens, outputTokens int) error {
	if cost < 0 {
		return fmt.Errorf("cost cannot be negative")
	}

	// Create cost record
	apiCost := &domain.APICost{
		Provider:     provider,
		Cost:         decimal.NewFromFloat(cost),
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		Success:      true,
	}

	// Save to database
	if err := m.repo.Create(ctx, apiCost); err != nil {
		return fmt.Errorf("failed to track API call: %w", err)
	}

	// Update cache
	m.updateCache(provider, decimal.NewFromFloat(cost))

	// Log the cost
	m.log.WithFields(logrus.Fields{
		"provider":      provider,
		"cost":          fmt.Sprintf("$%.6f", cost),
		"input_tokens":  inputTokens,
		"output_tokens": outputTokens,
		"total_tokens":  inputTokens + outputTokens,
	}).Debug("API call cost tracked")

	// Check if approaching or exceeding budget
	status, err := m.GetBudgetStatus(ctx, provider)
	if err != nil {
		m.log.WithError(err).Warn("Failed to get budget status after tracking")
		return nil // Don't fail the tracking operation
	}

	// Alert if near limit
	if status.IsNearLimit && !status.IsOverLimit {
		m.log.WithFields(logrus.Fields{
			"provider":     provider,
			"used_percent": fmt.Sprintf("%.1f%%", status.UsedPercent),
			"remaining":    fmt.Sprintf("$%.2f", status.RemainingBudget.InexactFloat64()),
		}).Warn("⚠️ Approaching daily budget limit")
	}

	// Alert if over limit
	if status.IsOverLimit {
		m.log.WithFields(logrus.Fields{
			"provider":     provider,
			"used_percent": fmt.Sprintf("%.1f%%", status.UsedPercent),
			"overage":      fmt.Sprintf("$%.2f", status.CurrentCost.Sub(status.DailyLimit).InexactFloat64()),
		}).Error("🚨 Daily budget limit exceeded")
	}

	return nil
}

// CheckBudget verifies if a provider has budget available
func (m *Manager) CheckBudget(ctx context.Context, provider string) (bool, error) {
	// If limits not enforced, always allow
	if !m.config.EnforceLimits {
		return true, nil
	}

	status, err := m.GetBudgetStatus(ctx, provider)
	if err != nil {
		return false, fmt.Errorf("failed to check budget: %w", err)
	}

	// Allow if under limit
	if !status.IsOverLimit {
		return true, nil
	}

	m.log.WithFields(logrus.Fields{
		"provider":     provider,
		"used_percent": fmt.Sprintf("%.1f%%", status.UsedPercent),
		"daily_limit":  fmt.Sprintf("$%.2f", status.DailyLimit.InexactFloat64()),
	}).Warn("🚫 Budget limit reached - blocking API call")

	return false, nil
}

// GetBudgetStatus returns current budget status for a provider
func (m *Manager) GetBudgetStatus(ctx context.Context, provider string) (*BudgetStatus, error) {
	// Get daily limit
	dailyLimit, exists := m.config.DailyLimits[provider]
	if !exists {
		// No limit configured - allow unlimited
		return &BudgetStatus{
			Provider:        provider,
			DailyLimit:      decimal.NewFromFloat(999999), // Effectively unlimited
			CurrentCost:     decimal.Zero,
			RemainingBudget: decimal.NewFromFloat(999999),
			UsedPercent:     0,
			CallCount:       0,
			IsNearLimit:     false,
			IsOverLimit:     false,
		}, nil
	}

	// Get current daily cost
	today := time.Now()
	currentCost, err := m.getDailyCost(ctx, provider, today)
	if err != nil {
		return nil, fmt.Errorf("failed to get daily cost: %w", err)
	}

	// Get call count
	callCount, err := m.repo.GetCallCount(ctx, provider, today)
	if err != nil {
		m.log.WithError(err).Warn("Failed to get call count")
		callCount = 0
	}

	// Calculate status
	dailyLimitDec := decimal.NewFromFloat(dailyLimit)
	remainingBudget := dailyLimitDec.Sub(currentCost)
	usedPercent := 0.0
	if dailyLimit > 0 {
		usedPercent = currentCost.Div(dailyLimitDec).Mul(decimal.NewFromInt(100)).InexactFloat64()
	}

	status := &BudgetStatus{
		Provider:        provider,
		DailyLimit:      dailyLimitDec,
		CurrentCost:     currentCost,
		RemainingBudget: remainingBudget,
		UsedPercent:     usedPercent,
		CallCount:       callCount,
		IsNearLimit:     usedPercent >= m.config.AlertThresholdPercent,
		IsOverLimit:     usedPercent >= 100.0,
	}

	return status, nil
}

// GetAllBudgetStatuses returns budget status for all configured providers
func (m *Manager) GetAllBudgetStatuses(ctx context.Context) (map[string]*BudgetStatus, error) {
	statuses := make(map[string]*BudgetStatus)

	for provider := range m.config.DailyLimits {
		status, err := m.GetBudgetStatus(ctx, provider)
		if err != nil {
			m.log.WithError(err).WithField("provider", provider).Warn("Failed to get budget status")
			continue
		}
		statuses[provider] = status
	}

	return statuses, nil
}

// GetCostBreakdown returns cost breakdown for a date range
func (m *Manager) GetCostBreakdown(ctx context.Context, startDate, endDate time.Time) ([]*CostBreakdown, error) {
	// Get breakdown from repository
	breakdown, err := m.repo.GetCostBreakdown(ctx, startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("failed to get cost breakdown: %w", err)
	}

	// Convert to structured format
	var results []*CostBreakdown
	for provider, dates := range breakdown {
		for date, cost := range dates {
			// Parse date
			parsedDate, err := time.Parse("2006-01-02", date)
			if err != nil {
				m.log.WithError(err).WithField("date", date).Warn("Failed to parse date")
				continue
			}

			// Get call count
			callCount, err := m.repo.GetCallCount(ctx, provider, parsedDate)
			if err != nil {
				m.log.WithError(err).Warn("Failed to get call count")
				callCount = 0
			}

			// Calculate average cost
			avgCost := decimal.Zero
			if callCount > 0 {
				avgCost = cost.Div(decimal.NewFromInt(callCount))
			}

			// Calculate percentage of daily budget
			percentage := 0.0
			if dailyLimit, exists := m.config.DailyLimits[provider]; exists && dailyLimit > 0 {
				percentage = cost.Div(decimal.NewFromFloat(dailyLimit)).Mul(decimal.NewFromInt(100)).InexactFloat64()
			}

			results = append(results, &CostBreakdown{
				Provider:   provider,
				Date:       date,
				TotalCost:  cost,
				CallCount:  callCount,
				AvgCost:    avgCost,
				Percentage: percentage,
			})
		}
	}

	return results, nil
}

// GetDailyCostSummary returns a summary of today's costs
func (m *Manager) GetDailyCostSummary(ctx context.Context) (map[string]interface{}, error) {
	today := time.Now()
	startOfDay := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location())
	endOfDay := startOfDay.Add(24 * time.Hour)

	// Get total cost for today
	totalCost, err := m.repo.GetTotalCostByDateRange(ctx, startOfDay, endOfDay)
	if err != nil {
		return nil, fmt.Errorf("failed to get total cost: %w", err)
	}

	// Get budget statuses
	statuses, err := m.GetAllBudgetStatuses(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get budget statuses: %w", err)
	}

	// Calculate totals
	totalBudget := decimal.Zero
	totalCalls := int64(0)
	providersNearLimit := 0
	providersOverLimit := 0

	providerDetails := make(map[string]interface{})
	for provider, status := range statuses {
		totalBudget = totalBudget.Add(status.DailyLimit)
		totalCalls += status.CallCount

		if status.IsOverLimit {
			providersOverLimit++
		} else if status.IsNearLimit {
			providersNearLimit++
		}

		providerDetails[provider] = map[string]interface{}{
			"current_cost":     fmt.Sprintf("$%.4f", status.CurrentCost.InexactFloat64()),
			"daily_limit":      fmt.Sprintf("$%.2f", status.DailyLimit.InexactFloat64()),
			"remaining_budget": fmt.Sprintf("$%.4f", status.RemainingBudget.InexactFloat64()),
			"used_percent":     fmt.Sprintf("%.1f%%", status.UsedPercent),
			"call_count":       status.CallCount,
			"is_near_limit":    status.IsNearLimit,
			"is_over_limit":    status.IsOverLimit,
		}
	}

	summary := map[string]interface{}{
		"date":                 today.Format("2006-01-02"),
		"total_cost":           fmt.Sprintf("$%.4f", totalCost.InexactFloat64()),
		"total_budget":         fmt.Sprintf("$%.2f", totalBudget.InexactFloat64()),
		"total_calls":          totalCalls,
		"providers_near_limit": providersNearLimit,
		"providers_over_limit": providersOverLimit,
		"provider_details":     providerDetails,
	}

	return summary, nil
}

// ResetDailyCache clears the daily cost cache (call at midnight)
func (m *Manager) ResetDailyCache() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.dailyCache = make(map[string]*DailyCostCache)
	m.log.Info("Daily cost cache reset")
}

// getDailyCost gets daily cost with caching
func (m *Manager) getDailyCost(ctx context.Context, provider string, date time.Time) (decimal.Decimal, error) {
	cacheKey := fmt.Sprintf("%s_%s", provider, date.Format("2006-01-02"))

	// Check cache first
	m.mu.RLock()
	if cached, exists := m.dailyCache[cacheKey]; exists {
		// Cache valid for 1 minute
		if time.Since(cached.UpdatedAt) < 1*time.Minute {
			m.mu.RUnlock()
			return cached.TotalCost, nil
		}
	}
	m.mu.RUnlock()

	// Cache miss or expired - fetch from DB
	cost, err := m.repo.GetDailyCost(ctx, provider, date)
	if err != nil {
		return decimal.Zero, err
	}

	// Update cache
	m.mu.Lock()
	m.dailyCache[cacheKey] = &DailyCostCache{
		Provider:  provider,
		Date:      date,
		TotalCost: cost,
		UpdatedAt: time.Now(),
	}
	m.mu.Unlock()

	return cost, nil
}

// updateCache updates the in-memory cache after tracking a cost
func (m *Manager) updateCache(provider string, cost decimal.Decimal) {
	today := time.Now()
	cacheKey := fmt.Sprintf("%s_%s", provider, today.Format("2006-01-02"))

	m.mu.Lock()
	defer m.mu.Unlock()

	if cached, exists := m.dailyCache[cacheKey]; exists {
		cached.TotalCost = cached.TotalCost.Add(cost)
		cached.CallCount++
		cached.UpdatedAt = time.Now()
	} else {
		m.dailyCache[cacheKey] = &DailyCostCache{
			Provider:  provider,
			Date:      today,
			TotalCost: cost,
			CallCount: 1,
			UpdatedAt: time.Now(),
		}
	}
}
