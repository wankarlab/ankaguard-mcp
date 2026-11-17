package subscription

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// Common errors
var (
	ErrSubscriptionNotFound = errors.New("subscription not found")
	ErrUsageNotFound        = errors.New("usage record not found")
	ErrInvalidUserID        = errors.New("invalid user ID")
	ErrInvalidTenantID      = errors.New("invalid tenant ID")
	ErrLimitExceeded        = errors.New("subscription limit exceeded")
)

// Repository defines the interface for subscription data access
type Repository interface {
	// Subscription CRUD
	Create(ctx context.Context, subscription *UserSubscription) error
	GetByID(ctx context.Context, id uuid.UUID) (*UserSubscription, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) (*UserSubscription, error)
	GetByUserIDAndTenant(ctx context.Context, userID, tenantID uuid.UUID) (*UserSubscription, error)
	Update(ctx context.Context, subscription *UserSubscription) error
	Delete(ctx context.Context, id uuid.UUID) error

	// Usage tracking
	GetUsage(ctx context.Context, subscriptionID uuid.UUID) (*SubscriptionUsage, error)
	GetUsageByUserID(ctx context.Context, userID uuid.UUID) (*SubscriptionUsage, error)
	CreateUsage(ctx context.Context, usage *SubscriptionUsage) error
	UpdateUsage(ctx context.Context, usage *SubscriptionUsage) error
	IncrementUsage(ctx context.Context, subscriptionID uuid.UUID, usageType UsageType, amount int) error
	ResetDailyUsage(ctx context.Context, subscriptionID uuid.UUID) error
	ResetPeriodUsage(ctx context.Context, subscriptionID uuid.UUID) error

	// Feature and limit checks
	IsFeatureEnabled(ctx context.Context, userID uuid.UUID, feature Feature) (bool, error)
	CheckLimit(ctx context.Context, userID uuid.UUID, usageType UsageType) (allowed bool, current int, limit int, err error)

	// Events
	LogEvent(ctx context.Context, event *SubscriptionEvent) error
	GetEvents(ctx context.Context, subscriptionID uuid.UUID, limit int) ([]SubscriptionEvent, error)

	// Queries
	GetExpiringSoon(ctx context.Context, days int) ([]UserSubscription, error)
	GetByTier(ctx context.Context, tier SubscriptionTier, limit, offset int) ([]UserSubscription, int64, error)
	GetActiveSubscriptions(ctx context.Context, tenantID uuid.UUID, limit, offset int) ([]UserSubscription, int64, error)

	// Odoo integration
	GetByOdooID(ctx context.Context, odooID string) (*UserSubscription, error)
	UpdateFromOdoo(ctx context.Context, userID uuid.UUID, tier SubscriptionTier, expiresAt *time.Time) error

	// Database migration
	AutoMigrate() error
}

// PostgresRepository implements Repository using PostgreSQL with GORM
type PostgresRepository struct {
	db     *gorm.DB
	logger *logrus.Logger
}

// NewPostgresRepository creates a new PostgreSQL repository
func NewPostgresRepository(db *gorm.DB, logger *logrus.Logger) *PostgresRepository {
	if logger == nil {
		logger = logrus.New()
	}
	return &PostgresRepository{
		db:     db,
		logger: logger,
	}
}

// AutoMigrate runs database migrations
func (r *PostgresRepository) AutoMigrate() error {
	return r.db.AutoMigrate(
		&UserSubscription{},
		&SubscriptionUsage{},
		&SubscriptionEvent{},
	)
}

// Create creates a new subscription
func (r *PostgresRepository) Create(ctx context.Context, subscription *UserSubscription) error {
	if subscription.ID == uuid.Nil {
		subscription.ID = uuid.New()
	}
	if subscription.CreatedAt.IsZero() {
		subscription.CreatedAt = time.Now()
	}
	subscription.UpdatedAt = time.Now()

	result := r.db.WithContext(ctx).Create(subscription)
	if result.Error != nil {
		r.logger.WithError(result.Error).WithFields(logrus.Fields{
			"user_id":   subscription.UserID,
			"tenant_id": subscription.TenantID,
			"tier":      subscription.Tier,
		}).Error("Failed to create subscription")
		return fmt.Errorf("failed to create subscription: %w", result.Error)
	}

	r.logger.WithFields(logrus.Fields{
		"subscription_id": subscription.ID,
		"user_id":         subscription.UserID,
		"tier":            subscription.Tier,
	}).Info("Subscription created")

	return nil
}

// GetByID retrieves a subscription by ID
func (r *PostgresRepository) GetByID(ctx context.Context, id uuid.UUID) (*UserSubscription, error) {
	var subscription UserSubscription
	result := r.db.WithContext(ctx).Where("id = ?", id).First(&subscription)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, fmt.Errorf("failed to get subscription: %w", result.Error)
	}
	return &subscription, nil
}

// GetByUserID retrieves a subscription by user ID
func (r *PostgresRepository) GetByUserID(ctx context.Context, userID uuid.UUID) (*UserSubscription, error) {
	if userID == uuid.Nil {
		return nil, ErrInvalidUserID
	}

	var subscription UserSubscription
	result := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&subscription)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, fmt.Errorf("failed to get subscription by user ID: %w", result.Error)
	}
	return &subscription, nil
}

// GetByUserIDAndTenant retrieves a subscription by user ID and tenant ID
func (r *PostgresRepository) GetByUserIDAndTenant(ctx context.Context, userID, tenantID uuid.UUID) (*UserSubscription, error) {
	if userID == uuid.Nil {
		return nil, ErrInvalidUserID
	}
	if tenantID == uuid.Nil {
		return nil, ErrInvalidTenantID
	}

	var subscription UserSubscription
	result := r.db.WithContext(ctx).
		Where("user_id = ? AND tenant_id = ?", userID, tenantID).
		First(&subscription)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, fmt.Errorf("failed to get subscription: %w", result.Error)
	}
	return &subscription, nil
}

// Update updates an existing subscription
func (r *PostgresRepository) Update(ctx context.Context, subscription *UserSubscription) error {
	subscription.UpdatedAt = time.Now()

	result := r.db.WithContext(ctx).Save(subscription)
	if result.Error != nil {
		r.logger.WithError(result.Error).WithFields(logrus.Fields{
			"subscription_id": subscription.ID,
			"user_id":         subscription.UserID,
		}).Error("Failed to update subscription")
		return fmt.Errorf("failed to update subscription: %w", result.Error)
	}

	r.logger.WithFields(logrus.Fields{
		"subscription_id": subscription.ID,
		"tier":            subscription.Tier,
	}).Debug("Subscription updated")

	return nil
}

// Delete deletes a subscription
func (r *PostgresRepository) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&UserSubscription{}, "id = ?", id)
	if result.Error != nil {
		return fmt.Errorf("failed to delete subscription: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrSubscriptionNotFound
	}
	return nil
}

// GetUsage retrieves usage for a subscription
func (r *PostgresRepository) GetUsage(ctx context.Context, subscriptionID uuid.UUID) (*SubscriptionUsage, error) {
	var usage SubscriptionUsage
	now := time.Now()

	result := r.db.WithContext(ctx).
		Where("subscription_id = ? AND period_start <= ? AND period_end >= ?", subscriptionID, now, now).
		First(&usage)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, ErrUsageNotFound
		}
		return nil, fmt.Errorf("failed to get usage: %w", result.Error)
	}
	return &usage, nil
}

// GetUsageByUserID retrieves current usage for a user
func (r *PostgresRepository) GetUsageByUserID(ctx context.Context, userID uuid.UUID) (*SubscriptionUsage, error) {
	var usage SubscriptionUsage
	now := time.Now()

	result := r.db.WithContext(ctx).
		Where("user_id = ? AND period_start <= ? AND period_end >= ?", userID, now, now).
		First(&usage)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, ErrUsageNotFound
		}
		return nil, fmt.Errorf("failed to get usage by user ID: %w", result.Error)
	}
	return &usage, nil
}

// CreateUsage creates a new usage record
func (r *PostgresRepository) CreateUsage(ctx context.Context, usage *SubscriptionUsage) error {
	if usage.ID == uuid.Nil {
		usage.ID = uuid.New()
	}
	if usage.CreatedAt.IsZero() {
		usage.CreatedAt = time.Now()
	}
	usage.UpdatedAt = time.Now()

	result := r.db.WithContext(ctx).Create(usage)
	if result.Error != nil {
		return fmt.Errorf("failed to create usage: %w", result.Error)
	}
	return nil
}

// UpdateUsage updates a usage record
func (r *PostgresRepository) UpdateUsage(ctx context.Context, usage *SubscriptionUsage) error {
	usage.UpdatedAt = time.Now()

	result := r.db.WithContext(ctx).Save(usage)
	if result.Error != nil {
		return fmt.Errorf("failed to update usage: %w", result.Error)
	}
	return nil
}

// IncrementUsage increments a specific usage counter
func (r *PostgresRepository) IncrementUsage(ctx context.Context, subscriptionID uuid.UUID, usageType UsageType, amount int) error {
	now := time.Now()

	var column string
	switch usageType {
	case UsageBacktests:
		column = "backtests_used"
	case UsageExchanges:
		column = "exchanges_connected"
	case UsageBots:
		column = "active_bots"
	case UsageAlerts:
		column = "active_alerts"
	case UsageAPICalls:
		column = "api_calls_count"
	case UsageWebhooks:
		column = "webhooks_used"
	case UsageCustomStrategies:
		column = "custom_strategies_used"
	case UsagePositions:
		column = "active_positions"
	default:
		return fmt.Errorf("unknown usage type: %s", usageType)
	}

	result := r.db.WithContext(ctx).
		Model(&SubscriptionUsage{}).
		Where("subscription_id = ? AND period_start <= ? AND period_end >= ?", subscriptionID, now, now).
		UpdateColumn(column, gorm.Expr(column+" + ?", amount))

	if result.Error != nil {
		return fmt.Errorf("failed to increment usage: %w", result.Error)
	}

	return nil
}

// ResetDailyUsage resets daily usage counters
func (r *PostgresRepository) ResetDailyUsage(ctx context.Context, subscriptionID uuid.UUID) error {
	now := time.Now()

	result := r.db.WithContext(ctx).
		Model(&SubscriptionUsage{}).
		Where("subscription_id = ? AND period_start <= ? AND period_end >= ?", subscriptionID, now, now).
		Updates(map[string]interface{}{
			"daily_volume":    0,
			"api_calls_count": 0,
			"updated_at":      time.Now(),
		})

	if result.Error != nil {
		return fmt.Errorf("failed to reset daily usage: %w", result.Error)
	}

	return nil
}

// ResetPeriodUsage creates a new usage period
func (r *PostgresRepository) ResetPeriodUsage(ctx context.Context, subscriptionID uuid.UUID) error {
	subscription, err := r.GetByID(ctx, subscriptionID)
	if err != nil {
		return err
	}

	now := time.Now()
	usage := &SubscriptionUsage{
		ID:             uuid.New(),
		SubscriptionID: subscriptionID,
		UserID:         subscription.UserID,
		TenantID:       subscription.TenantID,
		PeriodStart:    now,
		PeriodEnd:      now.AddDate(0, 1, 0), // 1 month period
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	return r.CreateUsage(ctx, usage)
}

// IsFeatureEnabled checks if a feature is enabled for a user
func (r *PostgresRepository) IsFeatureEnabled(ctx context.Context, userID uuid.UUID, feature Feature) (bool, error) {
	subscription, err := r.GetByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrSubscriptionNotFound) {
			// No subscription means free tier - check if feature is in free tier
			freeFeatures := DefaultFeaturesForTier(TierFree)
			for _, f := range freeFeatures {
				if f == feature {
					return true, nil
				}
			}
			return false, nil
		}
		return false, err
	}

	if !subscription.CanAccess() {
		return false, nil
	}

	return subscription.HasFeature(feature), nil
}

// CheckLimit checks if a usage limit has been reached
func (r *PostgresRepository) CheckLimit(ctx context.Context, userID uuid.UUID, usageType UsageType) (allowed bool, current int, limit int, err error) {
	subscription, err := r.GetByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrSubscriptionNotFound) {
			// Apply free tier limits
			subscription = &UserSubscription{
				Tier:   TierFree,
				Limits: DefaultLimitsForTier(TierFree),
			}
		} else {
			return false, 0, 0, err
		}
	}

	usage, err := r.GetUsageByUserID(ctx, userID)
	if err != nil && !errors.Is(err, ErrUsageNotFound) {
		return false, 0, 0, err
	}

	// If no usage record exists, assume 0 usage
	if usage == nil {
		usage = &SubscriptionUsage{}
	}

	limits := subscription.Limits

	switch usageType {
	case UsageBacktests:
		current = usage.BacktestsUsed
		limit = limits.BacktestsPerDay
	case UsageExchanges:
		current = usage.ExchangesConnected
		limit = limits.MaxExchanges
	case UsageBots:
		current = usage.ActiveBots
		limit = limits.MaxBots
	case UsageAlerts:
		current = usage.ActiveAlerts
		limit = limits.AlertsLimit
	case UsageAPICalls:
		current = int(usage.APICallsCount)
		limit = limits.APICallsPerMinute
	case UsageWebhooks:
		current = usage.WebhooksUsed
		limit = limits.WebhooksLimit
	case UsageCustomStrategies:
		current = usage.CustomStrategiesUsed
		limit = limits.CustomStrategiesLimit
	case UsagePositions:
		current = usage.ActivePositions
		limit = limits.MaxConcurrentPositions
	default:
		return false, 0, 0, fmt.Errorf("unknown usage type: %s", usageType)
	}

	// -1 means unlimited
	if limit < 0 {
		return true, current, -1, nil
	}

	return current < limit, current, limit, nil
}

// LogEvent records a subscription event
func (r *PostgresRepository) LogEvent(ctx context.Context, event *SubscriptionEvent) error {
	if event.ID == uuid.Nil {
		event.ID = uuid.New()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now()
	}

	result := r.db.WithContext(ctx).Create(event)
	if result.Error != nil {
		r.logger.WithError(result.Error).WithFields(logrus.Fields{
			"subscription_id": event.SubscriptionID,
			"event_type":      event.EventType,
		}).Error("Failed to log subscription event")
		return fmt.Errorf("failed to log event: %w", result.Error)
	}

	r.logger.WithFields(logrus.Fields{
		"event_id":        event.ID,
		"subscription_id": event.SubscriptionID,
		"event_type":      event.EventType,
	}).Debug("Subscription event logged")

	return nil
}

// GetEvents retrieves events for a subscription
func (r *PostgresRepository) GetEvents(ctx context.Context, subscriptionID uuid.UUID, limit int) ([]SubscriptionEvent, error) {
	var events []SubscriptionEvent

	query := r.db.WithContext(ctx).
		Where("subscription_id = ?", subscriptionID).
		Order("created_at DESC")

	if limit > 0 {
		query = query.Limit(limit)
	}

	result := query.Find(&events)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get events: %w", result.Error)
	}

	return events, nil
}

// GetExpiringSoon retrieves subscriptions expiring within given days
func (r *PostgresRepository) GetExpiringSoon(ctx context.Context, days int) ([]UserSubscription, error) {
	var subscriptions []UserSubscription
	deadline := time.Now().AddDate(0, 0, days)

	result := r.db.WithContext(ctx).
		Where("is_active = ? AND expires_at IS NOT NULL AND expires_at <= ?", true, deadline).
		Find(&subscriptions)

	if result.Error != nil {
		return nil, fmt.Errorf("failed to get expiring subscriptions: %w", result.Error)
	}

	return subscriptions, nil
}

// GetByTier retrieves subscriptions by tier with pagination
func (r *PostgresRepository) GetByTier(ctx context.Context, tier SubscriptionTier, limit, offset int) ([]UserSubscription, int64, error) {
	var subscriptions []UserSubscription
	var total int64

	countResult := r.db.WithContext(ctx).
		Model(&UserSubscription{}).
		Where("tier = ?", tier).
		Count(&total)
	if countResult.Error != nil {
		return nil, 0, fmt.Errorf("failed to count subscriptions: %w", countResult.Error)
	}

	result := r.db.WithContext(ctx).
		Where("tier = ?", tier).
		Limit(limit).
		Offset(offset).
		Order("created_at DESC").
		Find(&subscriptions)

	if result.Error != nil {
		return nil, 0, fmt.Errorf("failed to get subscriptions by tier: %w", result.Error)
	}

	return subscriptions, total, nil
}

// GetActiveSubscriptions retrieves active subscriptions for a tenant
func (r *PostgresRepository) GetActiveSubscriptions(ctx context.Context, tenantID uuid.UUID, limit, offset int) ([]UserSubscription, int64, error) {
	var subscriptions []UserSubscription
	var total int64

	query := r.db.WithContext(ctx).Model(&UserSubscription{}).Where("is_active = ?", true)
	if tenantID != uuid.Nil {
		query = query.Where("tenant_id = ?", tenantID)
	}

	countResult := query.Count(&total)
	if countResult.Error != nil {
		return nil, 0, fmt.Errorf("failed to count active subscriptions: %w", countResult.Error)
	}

	result := r.db.WithContext(ctx).
		Where("is_active = ?", true)
	if tenantID != uuid.Nil {
		result = result.Where("tenant_id = ?", tenantID)
	}
	result = result.Limit(limit).
		Offset(offset).
		Order("created_at DESC").
		Find(&subscriptions)

	if result.Error != nil {
		return nil, 0, fmt.Errorf("failed to get active subscriptions: %w", result.Error)
	}

	return subscriptions, total, nil
}

// GetByOdooID retrieves a subscription by Odoo subscription ID
func (r *PostgresRepository) GetByOdooID(ctx context.Context, odooID string) (*UserSubscription, error) {
	var subscription UserSubscription
	result := r.db.WithContext(ctx).
		Where("odoo_subscription_id = ?", odooID).
		First(&subscription)

	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, fmt.Errorf("failed to get subscription by Odoo ID: %w", result.Error)
	}

	return &subscription, nil
}

// UpdateFromOdoo updates a subscription from Odoo sync
func (r *PostgresRepository) UpdateFromOdoo(ctx context.Context, userID uuid.UUID, tier SubscriptionTier, expiresAt *time.Time) error {
	subscription, err := r.GetByUserID(ctx, userID)
	if err != nil {
		return err
	}

	oldTier := subscription.Tier
	subscription.Tier = tier
	subscription.Features = DefaultFeaturesForTier(tier)
	subscription.Limits = DefaultLimitsForTier(tier)
	subscription.ExpiresAt = expiresAt
	subscription.UpdatedAt = time.Now()

	if err := r.Update(ctx, subscription); err != nil {
		return err
	}

	// Log the change event
	eventType := EventTypeUpgraded
	if tier.TierLevel() < oldTier.TierLevel() {
		eventType = EventTypeDowngraded
	}

	event := &SubscriptionEvent{
		SubscriptionID: subscription.ID,
		UserID:         subscription.UserID,
		TenantID:       subscription.TenantID,
		EventType:      eventType,
		PreviousTier:   oldTier,
		NewTier:        tier,
		Description:    "Subscription updated from Odoo",
	}

	return r.LogEvent(ctx, event)
}

// EnsureUsagePeriod ensures a usage record exists for the current period
func (r *PostgresRepository) EnsureUsagePeriod(ctx context.Context, subscription *UserSubscription) (*SubscriptionUsage, error) {
	usage, err := r.GetUsage(ctx, subscription.ID)
	if err == nil {
		return usage, nil
	}

	if !errors.Is(err, ErrUsageNotFound) {
		return nil, err
	}

	// Create new usage period
	now := time.Now()
	usage = &SubscriptionUsage{
		ID:             uuid.New(),
		SubscriptionID: subscription.ID,
		UserID:         subscription.UserID,
		TenantID:       subscription.TenantID,
		PeriodStart:    now,
		PeriodEnd:      now.AddDate(0, 1, 0),
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := r.CreateUsage(ctx, usage); err != nil {
		return nil, err
	}

	return usage, nil
}
