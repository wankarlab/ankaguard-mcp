// Package subscription provides subscription management for the trading-mcp SaaS platform.
// It handles subscription tiers, feature access, usage limits, and Odoo integration.
package subscription

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// SubscriptionTier represents the subscription level for a user
type SubscriptionTier string

const (
	// TierFree is the free tier with limited features
	TierFree SubscriptionTier = "FREE"
	// TierBasic is the basic paid tier ($19/mo)
	TierBasic SubscriptionTier = "BASIC"
	// TierPro is the professional tier ($49/mo)
	TierPro SubscriptionTier = "PRO"
	// TierEnterprise is the enterprise tier ($199/mo)
	TierEnterprise SubscriptionTier = "ENTERPRISE"
)

// AllTiers returns all available subscription tiers in order
func AllTiers() []SubscriptionTier {
	return []SubscriptionTier{TierFree, TierBasic, TierPro, TierEnterprise}
}

// TierLevel returns the numeric level for a tier (for comparison)
func (t SubscriptionTier) TierLevel() int {
	switch t {
	case TierFree:
		return 1
	case TierBasic:
		return 2
	case TierPro:
		return 3
	case TierEnterprise:
		return 4
	default:
		return 0
	}
}

// IsAtLeast checks if this tier is at least the given minimum tier
func (t SubscriptionTier) IsAtLeast(minTier SubscriptionTier) bool {
	return t.TierLevel() >= minTier.TierLevel()
}

// String returns the string representation of the tier
func (t SubscriptionTier) String() string {
	return string(t)
}

// DisplayName returns a user-friendly display name
func (t SubscriptionTier) DisplayName() string {
	switch t {
	case TierFree:
		return "Free"
	case TierBasic:
		return "Basic"
	case TierPro:
		return "Pro"
	case TierEnterprise:
		return "Enterprise"
	default:
		return "Unknown"
	}
}

// MonthlyPrice returns the monthly price in USD for the tier
func (t SubscriptionTier) MonthlyPrice() float64 {
	switch t {
	case TierFree:
		return 0
	case TierBasic:
		return 19.00
	case TierPro:
		return 49.00
	case TierEnterprise:
		return 199.00
	default:
		return 0
	}
}

// ParseSubscriptionTier parses a string into a SubscriptionTier
func ParseSubscriptionTier(s string) (SubscriptionTier, bool) {
	switch s {
	case "FREE", "free":
		return TierFree, true
	case "BASIC", "basic":
		return TierBasic, true
	case "PRO", "pro":
		return TierPro, true
	case "ENTERPRISE", "enterprise":
		return TierEnterprise, true
	default:
		return TierFree, false
	}
}

// Feature represents a specific feature that can be enabled/disabled
type Feature string

const (
	// FeaturePaperTrading allows paper/simulated trading
	FeaturePaperTrading Feature = "paper_trading"
	// FeatureRealTrading allows real trading with live funds
	FeatureRealTrading Feature = "real_trading"
	// FeatureBacktesting allows running backtests
	FeatureBacktesting Feature = "backtesting"
	// FeatureAIBot allows using AI trading bots
	FeatureAIBot Feature = "ai_bot"
	// FeatureTechnicalIndicators allows accessing technical indicators
	FeatureTechnicalIndicators Feature = "technical_indicators"
	// FeatureAdvancedIndicators allows advanced technical analysis
	FeatureAdvancedIndicators Feature = "advanced_indicators"
	// FeatureAlerts allows setting price/condition alerts
	FeatureAlerts Feature = "alerts"
	// FeatureWebhooks allows webhook integrations
	FeatureWebhooks Feature = "webhooks"
	// FeatureAPIAccess allows API access
	FeatureAPIAccess Feature = "api_access"
	// FeaturePrioritySupport grants priority support
	FeaturePrioritySupport Feature = "priority_support"
	// FeatureCustomStrategies allows custom trading strategies
	FeatureCustomStrategies Feature = "custom_strategies"
	// FeatureMultiExchange allows multiple exchange connections
	FeatureMultiExchange Feature = "multi_exchange"
	// FeatureAdvancedRisk allows advanced risk management features
	FeatureAdvancedRisk Feature = "advanced_risk"
	// FeatureBulkOperations allows bulk trading operations
	FeatureBulkOperations Feature = "bulk_operations"
	// FeatureWhiteLabel allows white-label customization
	FeatureWhiteLabel Feature = "white_label"
	// FeatureReview allows reviewing (but not approving) Spec promotions — level-3 governance role.
	FeatureReview Feature = "review"
	// FeatureArbitrate allows resolving Spec gate appeals and overriding promotion gates — level-4 risk role.
	FeatureArbitrate Feature = "arbitrate"
)

// SubscriptionLimits defines the limits for each subscription tier
type SubscriptionLimits struct {
	// MaxExchanges is the maximum number of exchange connections allowed
	MaxExchanges int `json:"max_exchanges"`
	// MaxBots is the maximum number of AI trading bots allowed
	MaxBots int `json:"max_bots"`
	// BacktestsPerDay is the maximum number of backtests per day
	BacktestsPerDay int `json:"backtests_per_day"`
	// AlertsLimit is the maximum number of active alerts
	AlertsLimit int `json:"alerts_limit"`
	// APICallsPerMinute is the rate limit for API calls
	APICallsPerMinute int `json:"api_calls_per_minute"`
	// DataRetentionDays is how long historical data is retained
	DataRetentionDays int `json:"data_retention_days"`
	// MaxConcurrentPositions is the maximum number of concurrent positions
	MaxConcurrentPositions int `json:"max_concurrent_positions"`
	// MaxOrderSize is the maximum order size in USD (0 = unlimited)
	MaxOrderSize float64 `json:"max_order_size"`
	// MaxDailyVolume is the maximum daily trading volume in USD (0 = unlimited)
	MaxDailyVolume float64 `json:"max_daily_volume"`
	// WebhooksLimit is the maximum number of webhook integrations
	WebhooksLimit int `json:"webhooks_limit"`
	// CustomStrategiesLimit is the maximum number of custom strategies
	CustomStrategiesLimit int `json:"custom_strategies_limit"`
}

// DefaultLimitsForTier returns the default limits for a given tier
func DefaultLimitsForTier(tier SubscriptionTier) SubscriptionLimits {
	switch tier {
	case TierFree:
		return SubscriptionLimits{
			MaxExchanges:           1,
			MaxBots:                0,
			BacktestsPerDay:        5,
			AlertsLimit:            3,
			APICallsPerMinute:      30,
			DataRetentionDays:      7,
			MaxConcurrentPositions: 1,
			MaxOrderSize:           100,   // $100 max order (paper trading only)
			MaxDailyVolume:         500,   // $500 daily limit
			WebhooksLimit:          0,
			CustomStrategiesLimit:  0,
		}
	case TierBasic:
		return SubscriptionLimits{
			MaxExchanges:           2,
			MaxBots:                1,
			BacktestsPerDay:        50,
			AlertsLimit:            10,
			APICallsPerMinute:      100,
			DataRetentionDays:      30,
			MaxConcurrentPositions: 5,
			MaxOrderSize:           10000,  // $10k max order
			MaxDailyVolume:         50000,  // $50k daily limit
			WebhooksLimit:          2,
			CustomStrategiesLimit:  1,
		}
	case TierPro:
		return SubscriptionLimits{
			MaxExchanges:           5,
			MaxBots:                5,
			BacktestsPerDay:        -1, // Unlimited
			AlertsLimit:            50,
			APICallsPerMinute:      500,
			DataRetentionDays:      90,
			MaxConcurrentPositions: 20,
			MaxOrderSize:           100000,   // $100k max order
			MaxDailyVolume:         500000,   // $500k daily limit
			WebhooksLimit:          10,
			CustomStrategiesLimit:  10,
		}
	case TierEnterprise:
		return SubscriptionLimits{
			MaxExchanges:           -1, // Unlimited
			MaxBots:                -1, // Unlimited
			BacktestsPerDay:        -1, // Unlimited
			AlertsLimit:            -1, // Unlimited
			APICallsPerMinute:      -1, // Unlimited
			DataRetentionDays:      365,
			MaxConcurrentPositions: -1, // Unlimited
			MaxOrderSize:           0,  // Unlimited
			MaxDailyVolume:         0,  // Unlimited
			WebhooksLimit:          -1, // Unlimited
			CustomStrategiesLimit:  -1, // Unlimited
		}
	default:
		return DefaultLimitsForTier(TierFree)
	}
}

// IsUnlimited checks if a limit value represents unlimited
func IsUnlimited(value int) bool {
	return value < 0
}

// DefaultFeaturesForTier returns the default enabled features for a given tier
func DefaultFeaturesForTier(tier SubscriptionTier) []Feature {
	switch tier {
	case TierFree:
		return []Feature{
			FeaturePaperTrading,
			FeatureTechnicalIndicators,
		}
	case TierBasic:
		return []Feature{
			FeaturePaperTrading,
			FeatureRealTrading,
			FeatureBacktesting,
			FeatureTechnicalIndicators,
			FeatureAlerts,
			FeatureAPIAccess,
		}
	case TierPro:
		return []Feature{
			FeaturePaperTrading,
			FeatureRealTrading,
			FeatureBacktesting,
			FeatureAIBot,
			FeatureTechnicalIndicators,
			FeatureAdvancedIndicators,
			FeatureAlerts,
			FeatureWebhooks,
			FeatureAPIAccess,
			FeatureCustomStrategies,
			FeatureMultiExchange,
			FeatureAdvancedRisk,
			FeatureReview, // RBAC-1: Pro users can review (but not approve) Spec promotions
		}
	case TierEnterprise:
		return []Feature{
			FeaturePaperTrading,
			FeatureRealTrading,
			FeatureBacktesting,
			FeatureAIBot,
			FeatureTechnicalIndicators,
			FeatureAdvancedIndicators,
			FeatureAlerts,
			FeatureWebhooks,
			FeatureAPIAccess,
			FeaturePrioritySupport,
			FeatureCustomStrategies,
			FeatureMultiExchange,
			FeatureAdvancedRisk,
			FeatureBulkOperations,
			FeatureWhiteLabel,
			FeatureReview,    // RBAC-1: Enterprise inherits review
			FeatureArbitrate, // RBAC-1: only Enterprise can arbitrate gate appeals
		}
	default:
		return DefaultFeaturesForTier(TierFree)
	}
}

// UserSubscription represents a user's subscription status
type UserSubscription struct {
	// ID is the unique identifier for this subscription
	ID uuid.UUID `json:"id" gorm:"type:uuid;primary_key"`

	// UserID is the user this subscription belongs to
	UserID uuid.UUID `json:"user_id" gorm:"type:uuid;not null;index"`

	// TenantID for multi-tenant isolation
	TenantID uuid.UUID `json:"tenant_id" gorm:"type:uuid;not null;index"`

	// Tier is the subscription tier
	Tier SubscriptionTier `json:"tier" gorm:"type:varchar(20);not null;default:'FREE'"`

	// Features is a JSON array of enabled features
	Features []Feature `json:"features" gorm:"type:jsonb;serializer:json"`

	// Limits contains the subscription limits
	Limits SubscriptionLimits `json:"limits" gorm:"type:jsonb;serializer:json"`

	// IsActive indicates if the subscription is currently active
	IsActive bool `json:"is_active" gorm:"not null;default:true"`

	// TrialEnd is when the trial period ends (nil if not on trial)
	TrialEnd *time.Time `json:"trial_end,omitempty" gorm:"type:timestamptz"`

	// ExpiresAt is when the subscription expires (nil for never-expiring free tier)
	ExpiresAt *time.Time `json:"expires_at,omitempty" gorm:"type:timestamptz"`

	// BillingCycleStart is the start of the current billing cycle
	BillingCycleStart *time.Time `json:"billing_cycle_start,omitempty" gorm:"type:timestamptz"`

	// OdooSubscriptionID links to the Odoo subscription record
	OdooSubscriptionID string `json:"odoo_subscription_id,omitempty" gorm:"type:varchar(50);index"`

	// PaymentMethod stores the payment method type
	PaymentMethod string `json:"payment_method,omitempty" gorm:"type:varchar(50)"`

	// LastPaymentAt is when the last payment was made
	LastPaymentAt *time.Time `json:"last_payment_at,omitempty" gorm:"type:timestamptz"`

	// NextPaymentAt is when the next payment is due
	NextPaymentAt *time.Time `json:"next_payment_at,omitempty" gorm:"type:timestamptz"`

	// CancelledAt is when the subscription was cancelled (nil if active)
	CancelledAt *time.Time `json:"cancelled_at,omitempty" gorm:"type:timestamptz"`

	// CancellationReason stores the reason for cancellation
	CancellationReason string `json:"cancellation_reason,omitempty" gorm:"type:text"`

	// Metadata stores additional flexible data
	Metadata json.RawMessage `json:"metadata,omitempty" gorm:"type:jsonb"`

	// CreatedAt is when the subscription was created
	CreatedAt time.Time `json:"created_at" gorm:"type:timestamptz;not null"`

	// UpdatedAt is when the subscription was last updated
	UpdatedAt time.Time `json:"updated_at" gorm:"type:timestamptz;not null"`
}

// TableName returns the database table name
func (UserSubscription) TableName() string {
	return "user_subscriptions"
}

// IsExpired checks if the subscription has expired
func (s *UserSubscription) IsExpired() bool {
	if s.ExpiresAt == nil {
		return false // Free tier never expires
	}
	return time.Now().After(*s.ExpiresAt)
}

// IsOnTrial checks if the subscription is currently on trial
func (s *UserSubscription) IsOnTrial() bool {
	if s.TrialEnd == nil {
		return false
	}
	return time.Now().Before(*s.TrialEnd)
}

// DaysUntilExpiry returns the number of days until expiry (-1 if no expiry)
func (s *UserSubscription) DaysUntilExpiry() int {
	if s.ExpiresAt == nil {
		return -1
	}
	duration := time.Until(*s.ExpiresAt)
	if duration < 0 {
		return 0
	}
	return int(duration.Hours() / 24)
}

// HasFeature checks if a specific feature is enabled for this subscription
func (s *UserSubscription) HasFeature(feature Feature) bool {
	for _, f := range s.Features {
		if f == feature {
			return true
		}
	}
	return false
}

// CanAccess checks if the subscription grants access (active and not expired)
func (s *UserSubscription) CanAccess() bool {
	return s.IsActive && !s.IsExpired()
}

// SubscriptionUsage tracks the usage metrics for a subscription
type SubscriptionUsage struct {
	// ID is the unique identifier
	ID uuid.UUID `json:"id" gorm:"type:uuid;primary_key"`

	// SubscriptionID references the subscription
	SubscriptionID uuid.UUID `json:"subscription_id" gorm:"type:uuid;not null;index"`

	// UserID for quick lookups
	UserID uuid.UUID `json:"user_id" gorm:"type:uuid;not null;index"`

	// TenantID for multi-tenant isolation
	TenantID uuid.UUID `json:"tenant_id" gorm:"type:uuid;not null;index"`

	// PeriodStart is the start of this usage period
	PeriodStart time.Time `json:"period_start" gorm:"type:timestamptz;not null"`

	// PeriodEnd is the end of this usage period
	PeriodEnd time.Time `json:"period_end" gorm:"type:timestamptz;not null"`

	// BacktestsUsed is the number of backtests run in this period
	BacktestsUsed int `json:"backtests_used" gorm:"not null;default:0"`

	// ExchangesConnected is the current number of connected exchanges
	ExchangesConnected int `json:"exchanges_connected" gorm:"not null;default:0"`

	// ActiveBots is the current number of active trading bots
	ActiveBots int `json:"active_bots" gorm:"not null;default:0"`

	// ActiveAlerts is the current number of active alerts
	ActiveAlerts int `json:"active_alerts" gorm:"not null;default:0"`

	// APICallsCount is the total API calls made in this period
	APICallsCount int64 `json:"api_calls_count" gorm:"not null;default:0"`

	// DailyVolume is the trading volume in USD for today
	DailyVolume float64 `json:"daily_volume" gorm:"not null;default:0"`

	// TotalVolume is the total trading volume in USD for this period
	TotalVolume float64 `json:"total_volume" gorm:"not null;default:0"`

	// TradesExecuted is the number of trades executed
	TradesExecuted int `json:"trades_executed" gorm:"not null;default:0"`

	// WebhooksUsed is the number of webhook integrations
	WebhooksUsed int `json:"webhooks_used" gorm:"not null;default:0"`

	// CustomStrategiesUsed is the number of custom strategies
	CustomStrategiesUsed int `json:"custom_strategies_used" gorm:"not null;default:0"`

	// ActivePositions is the current number of open positions
	ActivePositions int `json:"active_positions" gorm:"not null;default:0"`

	// CreatedAt is when this record was created
	CreatedAt time.Time `json:"created_at" gorm:"type:timestamptz;not null"`

	// UpdatedAt is when this record was last updated
	UpdatedAt time.Time `json:"updated_at" gorm:"type:timestamptz;not null"`
}

// TableName returns the database table name
func (SubscriptionUsage) TableName() string {
	return "subscription_usage"
}

// UsageType represents different types of usage that can be tracked/limited
type UsageType string

const (
	UsageBacktests        UsageType = "backtests"
	UsageExchanges        UsageType = "exchanges"
	UsageBots             UsageType = "bots"
	UsageAlerts           UsageType = "alerts"
	UsageAPICalls         UsageType = "api_calls"
	UsageDailyVolume      UsageType = "daily_volume"
	UsageWebhooks         UsageType = "webhooks"
	UsageCustomStrategies UsageType = "custom_strategies"
	UsagePositions        UsageType = "positions"
)

// SubscriptionEvent records subscription lifecycle events
type SubscriptionEvent struct {
	// ID is the unique identifier
	ID uuid.UUID `json:"id" gorm:"type:uuid;primary_key"`

	// SubscriptionID references the subscription
	SubscriptionID uuid.UUID `json:"subscription_id" gorm:"type:uuid;not null;index"`

	// UserID for quick lookups
	UserID uuid.UUID `json:"user_id" gorm:"type:uuid;not null;index"`

	// TenantID for multi-tenant isolation
	TenantID uuid.UUID `json:"tenant_id" gorm:"type:uuid;not null;index"`

	// EventType describes the type of event
	EventType string `json:"event_type" gorm:"type:varchar(50);not null;index"`

	// PreviousTier is the tier before the event (for upgrades/downgrades)
	PreviousTier SubscriptionTier `json:"previous_tier,omitempty" gorm:"type:varchar(20)"`

	// NewTier is the tier after the event (for upgrades/downgrades)
	NewTier SubscriptionTier `json:"new_tier,omitempty" gorm:"type:varchar(20)"`

	// Amount is the transaction amount if applicable
	Amount float64 `json:"amount,omitempty"`

	// Currency is the transaction currency
	Currency string `json:"currency,omitempty" gorm:"type:varchar(10)"`

	// Description provides additional context
	Description string `json:"description,omitempty" gorm:"type:text"`

	// Metadata stores additional event data
	Metadata json.RawMessage `json:"metadata,omitempty" gorm:"type:jsonb"`

	// IPAddress records the IP for audit purposes
	IPAddress string `json:"ip_address,omitempty" gorm:"type:inet"`

	// UserAgent records the user agent for audit purposes
	UserAgent string `json:"user_agent,omitempty" gorm:"type:text"`

	// CreatedAt is when the event occurred
	CreatedAt time.Time `json:"created_at" gorm:"type:timestamptz;not null"`
}

// TableName returns the database table name
func (SubscriptionEvent) TableName() string {
	return "subscription_events"
}

// Event types
const (
	EventTypeCreated     = "created"
	EventTypeUpgraded    = "upgraded"
	EventTypeDowngraded  = "downgraded"
	EventTypeRenewed     = "renewed"
	EventTypeCancelled   = "cancelled"
	EventTypeReactivated = "reactivated"
	EventTypeExpired     = "expired"
	EventTypePayment     = "payment"
	EventTypePaymentFail = "payment_failed"
	EventTypeTrialStart  = "trial_started"
	EventTypeTrialEnd    = "trial_ended"
	EventTypeLimitHit    = "limit_hit"
)

// SubscriptionDTO is a data transfer object for API responses
type SubscriptionDTO struct {
	ID                 uuid.UUID          `json:"id"`
	UserID             uuid.UUID          `json:"user_id"`
	Tier               string             `json:"tier"`
	TierDisplayName    string             `json:"tier_display_name"`
	MonthlyPrice       float64            `json:"monthly_price"`
	Features           []string           `json:"features"`
	Limits             SubscriptionLimits `json:"limits"`
	IsActive           bool               `json:"is_active"`
	IsOnTrial          bool               `json:"is_trial"`
	TrialDaysRemaining int                `json:"trial_days_remaining,omitempty"`
	DaysUntilExpiry    int                `json:"days_until_expiry,omitempty"`
	ExpiresAt          *time.Time         `json:"expires_at,omitempty"`
	BillingCycleStart  *time.Time         `json:"billing_cycle_start,omitempty"`
	NextPaymentAt      *time.Time         `json:"next_payment_at,omitempty"`
	CreatedAt          time.Time          `json:"created_at"`
	UpdatedAt          time.Time          `json:"updated_at"`
}

// ToDTO converts a UserSubscription to a SubscriptionDTO
func (s *UserSubscription) ToDTO() SubscriptionDTO {
	features := make([]string, len(s.Features))
	for i, f := range s.Features {
		features[i] = string(f)
	}

	dto := SubscriptionDTO{
		ID:                s.ID,
		UserID:            s.UserID,
		Tier:              string(s.Tier),
		TierDisplayName:   s.Tier.DisplayName(),
		MonthlyPrice:      s.Tier.MonthlyPrice(),
		Features:          features,
		Limits:            s.Limits,
		IsActive:          s.IsActive && !s.IsExpired(),
		IsOnTrial:         s.IsOnTrial(),
		DaysUntilExpiry:   s.DaysUntilExpiry(),
		ExpiresAt:         s.ExpiresAt,
		BillingCycleStart: s.BillingCycleStart,
		NextPaymentAt:     s.NextPaymentAt,
		CreatedAt:         s.CreatedAt,
		UpdatedAt:         s.UpdatedAt,
	}

	if s.IsOnTrial() && s.TrialEnd != nil {
		days := int(time.Until(*s.TrialEnd).Hours() / 24)
		if days < 0 {
			days = 0
		}
		dto.TrialDaysRemaining = days
	}

	return dto
}

// UsageDTO is a data transfer object for usage API responses
type UsageDTO struct {
	BacktestsUsed        int                `json:"backtests_used"`
	BacktestsLimit       int                `json:"backtests_limit"`
	BacktestsRemaining   int                `json:"backtests_remaining"`
	ExchangesConnected   int                `json:"exchanges_connected"`
	ExchangesLimit       int                `json:"exchanges_limit"`
	ActiveBots           int                `json:"active_bots"`
	BotsLimit            int                `json:"bots_limit"`
	ActiveAlerts         int                `json:"active_alerts"`
	AlertsLimit          int                `json:"alerts_limit"`
	APICallsToday        int64              `json:"api_calls_today"`
	APICallsLimit        int                `json:"api_calls_limit"` // Per minute
	DailyVolume          float64            `json:"daily_volume"`
	DailyVolumeLimit     float64            `json:"daily_volume_limit"`
	ActivePositions      int                `json:"active_positions"`
	PositionsLimit       int                `json:"positions_limit"`
	WebhooksUsed         int                `json:"webhooks_used"`
	WebhooksLimit        int                `json:"webhooks_limit"`
	CustomStrategiesUsed int                `json:"custom_strategies_used"`
	CustomStrategiesLimit int               `json:"custom_strategies_limit"`
	PeriodStart          time.Time          `json:"period_start"`
	PeriodEnd            time.Time          `json:"period_end"`
	Warnings             []string           `json:"warnings,omitempty"`
}

// TierComparisonDTO shows differences between tiers for upgrade prompts
type TierComparisonDTO struct {
	FromTier       string             `json:"from_tier"`
	ToTier         string             `json:"to_tier"`
	PriceDiff      float64            `json:"price_difference"`
	NewFeatures    []string           `json:"new_features"`
	ImprovedLimits map[string]string  `json:"improved_limits"`
}
