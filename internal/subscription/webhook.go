// Package subscription provides subscription management including webhook handling,
// usage metering, and integration with Odoo for subscription lifecycle events.
package subscription

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Subscription event types from Odoo
const (
	EventSubscriptionCreated   = "subscription.created"
	EventSubscriptionUpdated   = "subscription.updated"
	EventSubscriptionCancelled = "subscription.cancelled"
	EventSubscriptionRenewed   = "subscription.renewed"
	EventSubscriptionExpired   = "subscription.expired"
	EventSubscriptionSuspended = "subscription.suspended"
	EventPaymentSucceeded      = "payment.succeeded"
	EventPaymentFailed         = "payment.failed"
)

// OdooSubscriptionTier represents the subscription tier levels from Odoo
// These map to internal SubscriptionTier values
type OdooSubscriptionTier string

const (
	OdooTierBasic      OdooSubscriptionTier = "basic"
	OdooTierStandard   OdooSubscriptionTier = "standard"
	OdooTierPremium    OdooSubscriptionTier = "premium"
	OdooTierEnterprise OdooSubscriptionTier = "enterprise"
)

// OdooTierToInternal converts an Odoo tier to internal SubscriptionTier
func OdooTierToInternal(odooTier OdooSubscriptionTier) SubscriptionTier {
	switch odooTier {
	case OdooTierBasic:
		return TierFree // Basic Odoo maps to Free tier
	case OdooTierStandard:
		return TierBasic // Standard Odoo maps to Basic tier
	case OdooTierPremium:
		return TierPro // Premium Odoo maps to Pro tier
	case OdooTierEnterprise:
		return TierEnterprise
	default:
		return TierFree
	}
}

// ParseOdooPlan parses an Odoo plan name string to OdooSubscriptionTier
func ParseOdooPlan(plan string) OdooSubscriptionTier {
	switch plan {
	case "basic":
		return OdooTierBasic
	case "standard":
		return OdooTierStandard
	case "premium":
		return OdooTierPremium
	case "enterprise":
		return OdooTierEnterprise
	default:
		return OdooTierBasic
	}
}

// TierLimits defines the usage limits for each tier
type TierLimits struct {
	APICallsPerDay      int     `json:"api_calls_per_day"`
	BacktestsPerDay     int     `json:"backtests_per_day"`
	ActiveBotsLimit     int     `json:"active_bots_limit"`
	AIAnalysesPerDay    int     `json:"ai_analyses_per_day"`
	MaxPositionsPerBot  int     `json:"max_positions_per_bot"`
	MaxLeverageAllowed  float64 `json:"max_leverage_allowed"`
	RealTimeFeed        bool    `json:"real_time_feed"`
	AdvancedIndicators  bool    `json:"advanced_indicators"`
	PrioritySupport     bool    `json:"priority_support"`
	CustomStrategies    bool    `json:"custom_strategies"`
	WebhookIntegrations int     `json:"webhook_integrations"`
}

// DefaultOdooTierLimits returns the default limits for each Odoo tier
var DefaultOdooTierLimits = map[OdooSubscriptionTier]TierLimits{
	OdooTierBasic: {
		APICallsPerDay:      100,
		BacktestsPerDay:     5,
		ActiveBotsLimit:     1,
		AIAnalysesPerDay:    10,
		MaxPositionsPerBot:  3,
		MaxLeverageAllowed:  5.0,
		RealTimeFeed:        false,
		AdvancedIndicators:  false,
		PrioritySupport:     false,
		CustomStrategies:    false,
		WebhookIntegrations: 1,
	},
	OdooTierStandard: {
		APICallsPerDay:      1000,
		BacktestsPerDay:     25,
		ActiveBotsLimit:     5,
		AIAnalysesPerDay:    50,
		MaxPositionsPerBot:  10,
		MaxLeverageAllowed:  10.0,
		RealTimeFeed:        true,
		AdvancedIndicators:  true,
		PrioritySupport:     false,
		CustomStrategies:    false,
		WebhookIntegrations: 5,
	},
	OdooTierPremium: {
		APICallsPerDay:      10000,
		BacktestsPerDay:     100,
		ActiveBotsLimit:     20,
		AIAnalysesPerDay:    200,
		MaxPositionsPerBot:  50,
		MaxLeverageAllowed:  50.0,
		RealTimeFeed:        true,
		AdvancedIndicators:  true,
		PrioritySupport:     true,
		CustomStrategies:    true,
		WebhookIntegrations: 20,
	},
	OdooTierEnterprise: {
		APICallsPerDay:      -1, // Unlimited
		BacktestsPerDay:     -1, // Unlimited
		ActiveBotsLimit:     -1, // Unlimited
		AIAnalysesPerDay:    -1, // Unlimited
		MaxPositionsPerBot:  -1, // Unlimited
		MaxLeverageAllowed:  100.0,
		RealTimeFeed:        true,
		AdvancedIndicators:  true,
		PrioritySupport:     true,
		CustomStrategies:    true,
		WebhookIntegrations: -1, // Unlimited
	},
}

// GetLimitsForOdooTier returns the limits for a given Odoo tier
func GetLimitsForOdooTier(tier OdooSubscriptionTier) TierLimits {
	if limits, ok := DefaultOdooTierLimits[tier]; ok {
		return limits
	}
	return DefaultOdooTierLimits[OdooTierBasic]
}

// KongLevelForOdooTier returns the Kong auth level for an Odoo subscription tier
func KongLevelForOdooTier(tier OdooSubscriptionTier) int {
	switch tier {
	case OdooTierBasic:
		return 1
	case OdooTierStandard:
		return 2
	case OdooTierPremium:
		return 3
	case OdooTierEnterprise:
		return 4
	default:
		return 1
	}
}

// OdooTierForKongLevel returns the Odoo subscription tier for a Kong auth level
func OdooTierForKongLevel(level int) OdooSubscriptionTier {
	switch level {
	case 1:
		return OdooTierBasic
	case 2:
		return OdooTierStandard
	case 3:
		return OdooTierPremium
	case 4:
		return OdooTierEnterprise
	default:
		return OdooTierBasic
	}
}

// KongLevelForInternalTier returns the Kong auth level for an internal SubscriptionTier
func KongLevelForInternalTier(tier SubscriptionTier) int {
	switch tier {
	case TierFree:
		return 1
	case TierBasic:
		return 2
	case TierPro:
		return 3
	case TierEnterprise:
		return 4
	default:
		return 1
	}
}

// OdooWebhookPayload represents the incoming webhook payload from Odoo
type OdooWebhookPayload struct {
	// Webhook metadata
	WebhookID   string `json:"webhook_id"`
	EventType   string `json:"event_type"`
	Timestamp   string `json:"timestamp"`
	Version     string `json:"version,omitempty"`
	Environment string `json:"environment,omitempty"` // production, staging, development

	// Event data
	Data SubscriptionEventData `json:"data"`

	// Idempotency
	IdempotencyKey string `json:"idempotency_key"`
}

// SubscriptionEventData contains the subscription event details
type SubscriptionEventData struct {
	// User identification
	UserID    string `json:"user_id"`
	TenantID  string `json:"tenant_id"`
	CompanyID string `json:"company_id,omitempty"`
	Email     string `json:"email,omitempty"`

	// Subscription details
	SubscriptionID   string `json:"subscription_id"`
	OdooPlan         string `json:"odoo_plan"`
	PreviousPlan     string `json:"previous_plan,omitempty"`
	KongLevel        int    `json:"kong_level"`
	PreviousLevel    int    `json:"previous_level,omitempty"`

	// Billing information
	BillingAmount   float64 `json:"billing_amount,omitempty"`
	BillingCurrency string  `json:"billing_currency,omitempty"`
	PaymentMethod   string  `json:"payment_method,omitempty"`
	InvoiceID       string  `json:"invoice_id,omitempty"`

	// Dates
	StartDate      string `json:"start_date,omitempty"`
	EndDate        string `json:"end_date,omitempty"`
	RenewalDate    string `json:"renewal_date,omitempty"`
	CancellationDate string `json:"cancellation_date,omitempty"`

	// Cancellation details
	CancellationReason string `json:"cancellation_reason,omitempty"`
	ImmediateCancellation bool  `json:"immediate_cancellation,omitempty"`

	// Additional metadata
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// Validate validates the webhook payload
func (p *OdooWebhookPayload) Validate() error {
	if p.WebhookID == "" {
		return errors.New("webhook_id is required")
	}
	if p.EventType == "" {
		return errors.New("event_type is required")
	}
	if p.IdempotencyKey == "" {
		return errors.New("idempotency_key is required")
	}
	if !isValidEventType(p.EventType) {
		return fmt.Errorf("invalid event_type: %s", p.EventType)
	}

	// Validate event data
	if err := p.Data.Validate(); err != nil {
		return fmt.Errorf("invalid event data: %w", err)
	}

	return nil
}

// Validate validates the subscription event data
func (d *SubscriptionEventData) Validate() error {
	if d.UserID == "" {
		return errors.New("user_id is required")
	}
	if d.TenantID == "" {
		return errors.New("tenant_id is required")
	}
	if d.SubscriptionID == "" {
		return errors.New("subscription_id is required")
	}
	if d.OdooPlan == "" {
		return errors.New("odoo_plan is required")
	}

	// Validate user_id is a valid UUID
	if _, err := uuid.Parse(d.UserID); err != nil {
		return fmt.Errorf("invalid user_id format: %w", err)
	}

	// Validate tenant_id is a valid UUID
	if _, err := uuid.Parse(d.TenantID); err != nil {
		return fmt.Errorf("invalid tenant_id format: %w", err)
	}

	return nil
}

// isValidEventType checks if the event type is valid
func isValidEventType(eventType string) bool {
	validTypes := []string{
		EventSubscriptionCreated,
		EventSubscriptionUpdated,
		EventSubscriptionCancelled,
		EventSubscriptionRenewed,
		EventSubscriptionExpired,
		EventSubscriptionSuspended,
		EventPaymentSucceeded,
		EventPaymentFailed,
	}

	for _, t := range validTypes {
		if eventType == t {
			return true
		}
	}
	return false
}

// WebhookSignature handles webhook signature verification
type WebhookSignature struct {
	secret []byte
}

// NewWebhookSignature creates a new webhook signature verifier
func NewWebhookSignature(secret string) *WebhookSignature {
	return &WebhookSignature{
		secret: []byte(secret),
	}
}

// Verify verifies the webhook signature
// The signature should be in the format: sha256=<hex_digest>
func (ws *WebhookSignature) Verify(payload []byte, signature string) error {
	if len(ws.secret) == 0 {
		return errors.New("webhook secret is not configured")
	}

	if signature == "" {
		return errors.New("signature is required")
	}

	// Parse the signature format
	parts := strings.SplitN(signature, "=", 2)
	if len(parts) != 2 {
		return errors.New("invalid signature format")
	}

	algorithm := parts[0]
	providedDigest := parts[1]

	if algorithm != "sha256" {
		return fmt.Errorf("unsupported signature algorithm: %s", algorithm)
	}

	// Calculate expected signature
	mac := hmac.New(sha256.New, ws.secret)
	mac.Write(payload)
	expectedDigest := hex.EncodeToString(mac.Sum(nil))

	// Constant-time comparison to prevent timing attacks
	if !hmac.Equal([]byte(expectedDigest), []byte(providedDigest)) {
		return errors.New("signature verification failed")
	}

	return nil
}

// Sign creates a signature for a payload
func (ws *WebhookSignature) Sign(payload []byte) string {
	mac := hmac.New(sha256.New, ws.secret)
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// SubscriptionRecord represents a local subscription record synced from Odoo
type SubscriptionRecord struct {
	ID               string               `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	UserID           string               `json:"user_id" gorm:"type:uuid;not null;index"`
	TenantID         string               `json:"tenant_id" gorm:"type:uuid;not null;index"`
	CompanyID        string               `json:"company_id,omitempty" gorm:"type:uuid"`
	OdooSubscriptionID string             `json:"odoo_subscription_id" gorm:"uniqueIndex"`

	// Subscription details (uses Odoo tier for compatibility with webhook data)
	Tier             OdooSubscriptionTier `json:"tier" gorm:"type:varchar(20);not null"`
	KongLevel        int                  `json:"kong_level" gorm:"not null"`
	Status           string               `json:"status" gorm:"type:varchar(20);not null;default:'active'"`

	// Billing
	BillingAmount    float64          `json:"billing_amount,omitempty"`
	BillingCurrency  string           `json:"billing_currency,omitempty" gorm:"default:'USD'"`

	// Dates
	StartDate        time.Time        `json:"start_date"`
	EndDate          *time.Time       `json:"end_date,omitempty"`
	RenewalDate      *time.Time       `json:"renewal_date,omitempty"`
	CancelledAt      *time.Time       `json:"cancelled_at,omitempty"`

	// Limits (stored for quick access)
	Limits           TierLimits       `json:"limits" gorm:"serializer:json"`

	// Audit fields
	CreatedAt        time.Time        `json:"created_at"`
	UpdatedAt        time.Time        `json:"updated_at"`
	Version          int              `json:"version" gorm:"default:1"`
}

// TableName returns the table name for GORM
func (SubscriptionRecord) TableName() string {
	return "subscriptions"
}

// IsActive returns true if the subscription is currently active
func (s *SubscriptionRecord) IsActive() bool {
	if s.Status != "active" {
		return false
	}
	if s.EndDate != nil && s.EndDate.Before(time.Now()) {
		return false
	}
	return true
}

// IsExpired returns true if the subscription has expired
func (s *SubscriptionRecord) IsExpired() bool {
	if s.EndDate == nil {
		return false
	}
	return s.EndDate.Before(time.Now())
}

// DaysUntilExpiration returns the number of days until expiration
func (s *SubscriptionRecord) DaysUntilExpiration() int {
	if s.EndDate == nil {
		return -1 // No expiration
	}
	days := int(time.Until(*s.EndDate).Hours() / 24)
	if days < 0 {
		return 0
	}
	return days
}

// WebhookEvent represents a processed webhook event for audit/replay
type WebhookEvent struct {
	ID             string    `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	WebhookID      string    `json:"webhook_id" gorm:"index"`
	IdempotencyKey string    `json:"idempotency_key" gorm:"uniqueIndex"`
	EventType      string    `json:"event_type" gorm:"index"`
	UserID         string    `json:"user_id" gorm:"type:uuid;index"`
	TenantID       string    `json:"tenant_id" gorm:"type:uuid;index"`
	Payload        string    `json:"payload" gorm:"type:text"`
	Status         string    `json:"status" gorm:"type:varchar(20);default:'received'"` // received, processing, processed, failed
	ErrorMessage   string    `json:"error_message,omitempty" gorm:"type:text"`
	ProcessedAt    *time.Time `json:"processed_at,omitempty"`
	RetryCount     int       `json:"retry_count" gorm:"default:0"`
	ReceivedAt     time.Time `json:"received_at"`
	CreatedAt      time.Time `json:"created_at"`
}

// TableName returns the table name for GORM
func (WebhookEvent) TableName() string {
	return "webhook_events"
}

// ToJSON serializes the payload to JSON
func (p *OdooWebhookPayload) ToJSON() ([]byte, error) {
	return json.Marshal(p)
}

// FromJSON deserializes the payload from JSON
func (p *OdooWebhookPayload) FromJSON(data []byte) error {
	return json.Unmarshal(data, p)
}

// ParseTimestamp parses the timestamp string to time.Time
func (p *OdooWebhookPayload) ParseTimestamp() (time.Time, error) {
	if p.Timestamp == "" {
		return time.Time{}, errors.New("timestamp is empty")
	}

	// Try multiple formats
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02 15:04:05",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, p.Timestamp); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("unable to parse timestamp: %s", p.Timestamp)
}
