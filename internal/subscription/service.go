package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// Service errors
var (
	ErrFeatureNotAllowed    = errors.New("feature not available in current subscription")
	ErrSubscriptionExpired  = errors.New("subscription has expired")
	ErrSubscriptionInactive = errors.New("subscription is not active")
	ErrUpgradeRequired      = errors.New("subscription upgrade required")
	ErrOdooSyncFailed       = errors.New("failed to sync with Odoo")
)

// OdooConfig holds configuration for Odoo integration
type OdooConfig struct {
	// GatewayURL is the Kong gateway URL for Odoo API
	GatewayURL string `json:"gateway_url"`
	// Timeout for HTTP requests
	Timeout time.Duration `json:"timeout"`
	// Enabled indicates if Odoo integration is active
	Enabled bool `json:"enabled"`
}

// Service provides subscription management functionality
type Service struct {
	repo       Repository
	logger     *logrus.Logger
	odooConfig *OdooConfig
	httpClient *http.Client
	cache      *subscriptionCache
	mu         sync.RWMutex
}

// subscriptionCache provides in-memory caching for subscription lookups
type subscriptionCache struct {
	subscriptions map[uuid.UUID]*cachedSubscription
	mu            sync.RWMutex
	ttl           time.Duration
}

type cachedSubscription struct {
	subscription *UserSubscription
	cachedAt     time.Time
}

// NewService creates a new subscription service
func NewService(repo Repository, logger *logrus.Logger, odooConfig *OdooConfig) *Service {
	if logger == nil {
		logger = logrus.New()
	}

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}
	if odooConfig != nil && odooConfig.Timeout > 0 {
		httpClient.Timeout = odooConfig.Timeout
	}

	return &Service{
		repo:       repo,
		logger:     logger,
		odooConfig: odooConfig,
		httpClient: httpClient,
		cache: &subscriptionCache{
			subscriptions: make(map[uuid.UUID]*cachedSubscription),
			ttl:           5 * time.Minute,
		},
	}
}

// GetSubscription retrieves a user's subscription
func (s *Service) GetSubscription(ctx context.Context, userID uuid.UUID) (*UserSubscription, error) {
	// Check cache first
	if cached := s.cache.get(userID); cached != nil {
		s.logger.WithField("user_id", userID).Debug("Subscription cache hit")
		return cached, nil
	}

	subscription, err := s.repo.GetByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrSubscriptionNotFound) {
			// Return a default free tier subscription
			return s.createDefaultSubscription(ctx, userID, uuid.Nil)
		}
		return nil, err
	}

	// Cache the result
	s.cache.set(userID, subscription)

	return subscription, nil
}

// GetSubscriptionWithTenant retrieves a user's subscription for a specific tenant
func (s *Service) GetSubscriptionWithTenant(ctx context.Context, userID, tenantID uuid.UUID) (*UserSubscription, error) {
	subscription, err := s.repo.GetByUserIDAndTenant(ctx, userID, tenantID)
	if err != nil {
		if errors.Is(err, ErrSubscriptionNotFound) {
			return s.createDefaultSubscription(ctx, userID, tenantID)
		}
		return nil, err
	}

	return subscription, nil
}

// createDefaultSubscription creates a free tier subscription for a new user
func (s *Service) createDefaultSubscription(ctx context.Context, userID, tenantID uuid.UUID) (*UserSubscription, error) {
	subscription := &UserSubscription{
		ID:        uuid.New(),
		UserID:    userID,
		TenantID:  tenantID,
		Tier:      TierFree,
		Features:  DefaultFeaturesForTier(TierFree),
		Limits:    DefaultLimitsForTier(TierFree),
		IsActive:  true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := s.repo.Create(ctx, subscription); err != nil {
		s.logger.WithError(err).WithField("user_id", userID).Error("Failed to create default subscription")
		return nil, err
	}

	// Log the event
	event := &SubscriptionEvent{
		SubscriptionID: subscription.ID,
		UserID:         userID,
		TenantID:       tenantID,
		EventType:      EventTypeCreated,
		NewTier:        TierFree,
		Description:    "Default free subscription created",
	}
	if err := s.repo.LogEvent(ctx, event); err != nil {
		s.logger.WithError(err).Warn("Failed to log subscription creation event")
	}

	return subscription, nil
}

// ValidateSubscription checks if a subscription is valid and active
func (s *Service) ValidateSubscription(ctx context.Context, userID uuid.UUID) error {
	subscription, err := s.GetSubscription(ctx, userID)
	if err != nil {
		return err
	}

	if !subscription.IsActive {
		return ErrSubscriptionInactive
	}

	if subscription.IsExpired() {
		return ErrSubscriptionExpired
	}

	return nil
}

// CheckFeatureAccess checks if a user has access to a specific feature
func (s *Service) CheckFeatureAccess(ctx context.Context, userID uuid.UUID, feature Feature) error {
	subscription, err := s.GetSubscription(ctx, userID)
	if err != nil {
		return err
	}

	if !subscription.CanAccess() {
		if subscription.IsExpired() {
			return ErrSubscriptionExpired
		}
		return ErrSubscriptionInactive
	}

	if !subscription.HasFeature(feature) {
		return fmt.Errorf("%w: %s requires %s tier or higher",
			ErrFeatureNotAllowed, feature, s.getMinimumTierForFeature(feature))
	}

	return nil
}

// getMinimumTierForFeature returns the minimum tier required for a feature
func (s *Service) getMinimumTierForFeature(feature Feature) SubscriptionTier {
	for _, tier := range AllTiers() {
		features := DefaultFeaturesForTier(tier)
		for _, f := range features {
			if f == feature {
				return tier
			}
		}
	}
	return TierEnterprise
}

// CheckLimit checks if a usage limit allows the operation
func (s *Service) CheckLimit(ctx context.Context, userID uuid.UUID, usageType UsageType) error {
	allowed, current, limit, err := s.repo.CheckLimit(ctx, userID, usageType)
	if err != nil {
		return err
	}

	if !allowed {
		return fmt.Errorf("%w: %s limit reached (%d/%d)",
			ErrLimitExceeded, usageType, current, limit)
	}

	return nil
}

// IncrementUsage increments a usage counter and checks limits
func (s *Service) IncrementUsage(ctx context.Context, userID uuid.UUID, usageType UsageType, amount int) error {
	subscription, err := s.GetSubscription(ctx, userID)
	if err != nil {
		return err
	}

	// Ensure usage period exists
	if _, err := s.repo.(*PostgresRepository).EnsureUsagePeriod(ctx, subscription); err != nil {
		s.logger.WithError(err).Warn("Failed to ensure usage period")
	}

	// Check limit before incrementing
	if err := s.CheckLimit(ctx, userID, usageType); err != nil {
		// Log limit hit event
		event := &SubscriptionEvent{
			SubscriptionID: subscription.ID,
			UserID:         userID,
			TenantID:       subscription.TenantID,
			EventType:      EventTypeLimitHit,
			Description:    fmt.Sprintf("Limit hit for %s", usageType),
		}
		if logErr := s.repo.LogEvent(ctx, event); logErr != nil {
			s.logger.WithError(logErr).Warn("Failed to log limit hit event")
		}
		return err
	}

	return s.repo.IncrementUsage(ctx, subscription.ID, usageType, amount)
}

// GetUsage retrieves usage statistics for a user
func (s *Service) GetUsage(ctx context.Context, userID uuid.UUID) (*UsageDTO, error) {
	subscription, err := s.GetSubscription(ctx, userID)
	if err != nil {
		return nil, err
	}

	usage, err := s.repo.GetUsageByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrUsageNotFound) {
			// Return empty usage with limits
			return s.emptyUsageDTO(subscription), nil
		}
		return nil, err
	}

	dto := &UsageDTO{
		BacktestsUsed:         usage.BacktestsUsed,
		BacktestsLimit:        subscription.Limits.BacktestsPerDay,
		ExchangesConnected:    usage.ExchangesConnected,
		ExchangesLimit:        subscription.Limits.MaxExchanges,
		ActiveBots:            usage.ActiveBots,
		BotsLimit:             subscription.Limits.MaxBots,
		ActiveAlerts:          usage.ActiveAlerts,
		AlertsLimit:           subscription.Limits.AlertsLimit,
		APICallsToday:         usage.APICallsCount,
		APICallsLimit:         subscription.Limits.APICallsPerMinute,
		DailyVolume:           usage.DailyVolume,
		DailyVolumeLimit:      subscription.Limits.MaxDailyVolume,
		ActivePositions:       usage.ActivePositions,
		PositionsLimit:        subscription.Limits.MaxConcurrentPositions,
		WebhooksUsed:          usage.WebhooksUsed,
		WebhooksLimit:         subscription.Limits.WebhooksLimit,
		CustomStrategiesUsed:  usage.CustomStrategiesUsed,
		CustomStrategiesLimit: subscription.Limits.CustomStrategiesLimit,
		PeriodStart:           usage.PeriodStart,
		PeriodEnd:             usage.PeriodEnd,
	}

	// Calculate remaining and add warnings
	dto.BacktestsRemaining = s.calculateRemaining(dto.BacktestsLimit, dto.BacktestsUsed)
	dto.Warnings = s.generateUsageWarnings(dto)

	return dto, nil
}

func (s *Service) emptyUsageDTO(subscription *UserSubscription) *UsageDTO {
	now := time.Now()
	return &UsageDTO{
		BacktestsLimit:        subscription.Limits.BacktestsPerDay,
		ExchangesLimit:        subscription.Limits.MaxExchanges,
		BotsLimit:             subscription.Limits.MaxBots,
		AlertsLimit:           subscription.Limits.AlertsLimit,
		APICallsLimit:         subscription.Limits.APICallsPerMinute,
		DailyVolumeLimit:      subscription.Limits.MaxDailyVolume,
		PositionsLimit:        subscription.Limits.MaxConcurrentPositions,
		WebhooksLimit:         subscription.Limits.WebhooksLimit,
		CustomStrategiesLimit: subscription.Limits.CustomStrategiesLimit,
		PeriodStart:           now,
		PeriodEnd:             now.AddDate(0, 1, 0),
	}
}

func (s *Service) calculateRemaining(limit, used int) int {
	if limit < 0 { // Unlimited
		return -1
	}
	remaining := limit - used
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (s *Service) generateUsageWarnings(usage *UsageDTO) []string {
	var warnings []string

	// Check if approaching limits (>80%)
	if usage.BacktestsLimit > 0 {
		pct := float64(usage.BacktestsUsed) / float64(usage.BacktestsLimit) * 100
		if pct >= 80 {
			warnings = append(warnings, fmt.Sprintf("Backtests usage at %.0f%% of daily limit", pct))
		}
	}

	if usage.ExchangesLimit > 0 && usage.ExchangesConnected >= usage.ExchangesLimit {
		warnings = append(warnings, "Exchange connection limit reached")
	}

	if usage.BotsLimit >= 0 && usage.ActiveBots >= usage.BotsLimit {
		warnings = append(warnings, "AI bot limit reached")
	}

	if usage.DailyVolumeLimit > 0 {
		pct := usage.DailyVolume / usage.DailyVolumeLimit * 100
		if pct >= 80 {
			warnings = append(warnings, fmt.Sprintf("Daily trading volume at %.0f%% of limit", pct))
		}
	}

	return warnings
}

// UpgradeSubscription upgrades a user's subscription to a new tier
func (s *Service) UpgradeSubscription(ctx context.Context, userID uuid.UUID, newTier SubscriptionTier) (*UserSubscription, error) {
	subscription, err := s.GetSubscription(ctx, userID)
	if err != nil {
		return nil, err
	}

	if newTier.TierLevel() <= subscription.Tier.TierLevel() {
		return nil, fmt.Errorf("new tier must be higher than current tier")
	}

	oldTier := subscription.Tier
	subscription.Tier = newTier
	subscription.Features = DefaultFeaturesForTier(newTier)
	subscription.Limits = DefaultLimitsForTier(newTier)
	subscription.IsActive = true
	subscription.UpdatedAt = time.Now()

	// Set expiry for paid tiers (1 month from now)
	if newTier != TierFree {
		expires := time.Now().AddDate(0, 1, 0)
		subscription.ExpiresAt = &expires
		subscription.BillingCycleStart = &time.Time{}
		*subscription.BillingCycleStart = time.Now()
	}

	if err := s.repo.Update(ctx, subscription); err != nil {
		return nil, err
	}

	// Invalidate cache
	s.cache.invalidate(userID)

	// Log event
	event := &SubscriptionEvent{
		SubscriptionID: subscription.ID,
		UserID:         userID,
		TenantID:       subscription.TenantID,
		EventType:      EventTypeUpgraded,
		PreviousTier:   oldTier,
		NewTier:        newTier,
		Amount:         newTier.MonthlyPrice(),
		Currency:       "USD",
		Description:    fmt.Sprintf("Upgraded from %s to %s", oldTier, newTier),
	}
	if err := s.repo.LogEvent(ctx, event); err != nil {
		s.logger.WithError(err).Warn("Failed to log upgrade event")
	}

	s.logger.WithFields(logrus.Fields{
		"user_id":  userID,
		"old_tier": oldTier,
		"new_tier": newTier,
	}).Info("Subscription upgraded")

	return subscription, nil
}

// DowngradeSubscription downgrades a user's subscription
func (s *Service) DowngradeSubscription(ctx context.Context, userID uuid.UUID, newTier SubscriptionTier, reason string) (*UserSubscription, error) {
	subscription, err := s.GetSubscription(ctx, userID)
	if err != nil {
		return nil, err
	}

	if newTier.TierLevel() >= subscription.Tier.TierLevel() {
		return nil, fmt.Errorf("new tier must be lower than current tier")
	}

	oldTier := subscription.Tier
	subscription.Tier = newTier
	subscription.Features = DefaultFeaturesForTier(newTier)
	subscription.Limits = DefaultLimitsForTier(newTier)
	subscription.UpdatedAt = time.Now()

	// Free tier doesn't expire
	if newTier == TierFree {
		subscription.ExpiresAt = nil
	}

	if err := s.repo.Update(ctx, subscription); err != nil {
		return nil, err
	}

	// Invalidate cache
	s.cache.invalidate(userID)

	// Log event
	event := &SubscriptionEvent{
		SubscriptionID: subscription.ID,
		UserID:         userID,
		TenantID:       subscription.TenantID,
		EventType:      EventTypeDowngraded,
		PreviousTier:   oldTier,
		NewTier:        newTier,
		Description:    fmt.Sprintf("Downgraded from %s to %s: %s", oldTier, newTier, reason),
	}
	if err := s.repo.LogEvent(ctx, event); err != nil {
		s.logger.WithError(err).Warn("Failed to log downgrade event")
	}

	s.logger.WithFields(logrus.Fields{
		"user_id":  userID,
		"old_tier": oldTier,
		"new_tier": newTier,
		"reason":   reason,
	}).Info("Subscription downgraded")

	return subscription, nil
}

// CancelSubscription cancels a user's subscription
func (s *Service) CancelSubscription(ctx context.Context, userID uuid.UUID, reason string) error {
	subscription, err := s.GetSubscription(ctx, userID)
	if err != nil {
		return err
	}

	now := time.Now()
	subscription.CancelledAt = &now
	subscription.CancellationReason = reason
	subscription.UpdatedAt = now

	if err := s.repo.Update(ctx, subscription); err != nil {
		return err
	}

	// Invalidate cache
	s.cache.invalidate(userID)

	// Log event
	event := &SubscriptionEvent{
		SubscriptionID: subscription.ID,
		UserID:         userID,
		TenantID:       subscription.TenantID,
		EventType:      EventTypeCancelled,
		PreviousTier:   subscription.Tier,
		Description:    fmt.Sprintf("Subscription cancelled: %s", reason),
	}
	if err := s.repo.LogEvent(ctx, event); err != nil {
		s.logger.WithError(err).Warn("Failed to log cancellation event")
	}

	s.logger.WithFields(logrus.Fields{
		"user_id": userID,
		"tier":    subscription.Tier,
		"reason":  reason,
	}).Info("Subscription cancelled")

	return nil
}

// ValidateWithOdoo validates subscription status with Odoo
func (s *Service) ValidateWithOdoo(ctx context.Context, userID uuid.UUID) (*UserSubscription, error) {
	if s.odooConfig == nil || !s.odooConfig.Enabled {
		return s.GetSubscription(ctx, userID)
	}

	subscription, err := s.GetSubscription(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Call Odoo API to validate
	odooSub, err := s.fetchOdooSubscription(ctx, userID.String())
	if err != nil {
		s.logger.WithError(err).Warn("Failed to validate with Odoo, using local data")
		return subscription, nil
	}

	// Update if Odoo has different data
	if odooSub != nil {
		tier, ok := ParseSubscriptionTier(odooSub.Tier)
		if !ok {
			tier = TierFree
		}

		if tier != subscription.Tier || !s.timesEqual(odooSub.ExpiresAt, subscription.ExpiresAt) {
			if err := s.repo.UpdateFromOdoo(ctx, userID, tier, odooSub.ExpiresAt); err != nil {
				s.logger.WithError(err).Error("Failed to update subscription from Odoo")
			} else {
				// Invalidate cache and refetch
				s.cache.invalidate(userID)
				return s.GetSubscription(ctx, userID)
			}
		}
	}

	return subscription, nil
}

// odooSubscriptionResponse represents the Odoo API response
type odooSubscriptionResponse struct {
	Tier      string     `json:"tier"`
	IsActive  bool       `json:"is_active"`
	ExpiresAt *time.Time `json:"expires_at"`
	Features  []string   `json:"features"`
}

func (s *Service) fetchOdooSubscription(ctx context.Context, userID string) (*odooSubscriptionResponse, error) {
	url := fmt.Sprintf("%s/api/v1/subscription/%s", s.odooConfig.GatewayURL, userID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("odoo request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("odoo returned status %d", resp.StatusCode)
	}

	var result odooSubscriptionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode odoo response: %w", err)
	}

	return &result, nil
}

func (s *Service) timesEqual(a, b *time.Time) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Equal(*b)
}

// GetTierComparison returns a comparison between two tiers
func (s *Service) GetTierComparison(fromTier, toTier SubscriptionTier) *TierComparisonDTO {
	fromFeatures := DefaultFeaturesForTier(fromTier)
	toFeatures := DefaultFeaturesForTier(toTier)
	fromLimits := DefaultLimitsForTier(fromTier)
	toLimits := DefaultLimitsForTier(toTier)

	// Find new features
	var newFeatures []string
	fromFeaturesMap := make(map[Feature]bool)
	for _, f := range fromFeatures {
		fromFeaturesMap[f] = true
	}
	for _, f := range toFeatures {
		if !fromFeaturesMap[f] {
			newFeatures = append(newFeatures, string(f))
		}
	}

	// Compare limits
	improvedLimits := make(map[string]string)
	if toLimits.MaxExchanges > fromLimits.MaxExchanges || toLimits.MaxExchanges < 0 {
		improvedLimits["exchanges"] = s.formatLimitChange(fromLimits.MaxExchanges, toLimits.MaxExchanges)
	}
	if toLimits.MaxBots > fromLimits.MaxBots || toLimits.MaxBots < 0 {
		improvedLimits["bots"] = s.formatLimitChange(fromLimits.MaxBots, toLimits.MaxBots)
	}
	if toLimits.BacktestsPerDay > fromLimits.BacktestsPerDay || toLimits.BacktestsPerDay < 0 {
		improvedLimits["backtests_per_day"] = s.formatLimitChange(fromLimits.BacktestsPerDay, toLimits.BacktestsPerDay)
	}
	if toLimits.APICallsPerMinute > fromLimits.APICallsPerMinute || toLimits.APICallsPerMinute < 0 {
		improvedLimits["api_calls_per_minute"] = s.formatLimitChange(fromLimits.APICallsPerMinute, toLimits.APICallsPerMinute)
	}

	return &TierComparisonDTO{
		FromTier:       string(fromTier),
		ToTier:         string(toTier),
		PriceDiff:      toTier.MonthlyPrice() - fromTier.MonthlyPrice(),
		NewFeatures:    newFeatures,
		ImprovedLimits: improvedLimits,
	}
}

func (s *Service) formatLimitChange(from, to int) string {
	fromStr := fmt.Sprintf("%d", from)
	if from < 0 {
		fromStr = "unlimited"
	}
	toStr := fmt.Sprintf("%d", to)
	if to < 0 {
		toStr = "unlimited"
	}
	return fmt.Sprintf("%s -> %s", fromStr, toStr)
}

// Cache methods
func (c *subscriptionCache) get(userID uuid.UUID) *UserSubscription {
	c.mu.RLock()
	defer c.mu.RUnlock()

	cached, ok := c.subscriptions[userID]
	if !ok {
		return nil
	}

	if time.Since(cached.cachedAt) > c.ttl {
		return nil
	}

	return cached.subscription
}

func (c *subscriptionCache) set(userID uuid.UUID, subscription *UserSubscription) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.subscriptions[userID] = &cachedSubscription{
		subscription: subscription,
		cachedAt:     time.Now(),
	}
}

func (c *subscriptionCache) invalidate(userID uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.subscriptions, userID)
}

// RequireTier is a helper that returns an error if user doesn't have the minimum tier
func (s *Service) RequireTier(ctx context.Context, userID uuid.UUID, minTier SubscriptionTier) error {
	subscription, err := s.GetSubscription(ctx, userID)
	if err != nil {
		return err
	}

	if !subscription.CanAccess() {
		if subscription.IsExpired() {
			return ErrSubscriptionExpired
		}
		return ErrSubscriptionInactive
	}

	if !subscription.Tier.IsAtLeast(minTier) {
		return fmt.Errorf("%w: %s tier required, current tier is %s",
			ErrUpgradeRequired, minTier, subscription.Tier)
	}

	return nil
}

// GetEvents retrieves subscription events for a user
func (s *Service) GetEvents(ctx context.Context, userID uuid.UUID, limit int) ([]SubscriptionEvent, error) {
	subscription, err := s.GetSubscription(ctx, userID)
	if err != nil {
		return nil, err
	}

	return s.repo.GetEvents(ctx, subscription.ID, limit)
}
