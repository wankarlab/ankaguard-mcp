package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// ProcessorError represents an error during webhook processing
type ProcessorError struct {
	Code    string
	Message string
	Err     error
}

func (e *ProcessorError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *ProcessorError) Unwrap() error {
	return e.Err
}

// Error codes for webhook processing
const (
	ErrCodeDuplicateEvent    = "DUPLICATE_EVENT"
	ErrCodeInvalidPayload    = "INVALID_PAYLOAD"
	ErrCodeSignatureInvalid  = "SIGNATURE_INVALID"
	ErrCodeProcessingFailed  = "PROCESSING_FAILED"
	ErrCodeSubscriptionNotFound = "SUBSCRIPTION_NOT_FOUND"
	ErrCodeUserNotFound      = "USER_NOT_FOUND"
)

// WebhookProcessor handles webhook event processing
type WebhookProcessor struct {
	db             *gorm.DB
	logger         *logrus.Logger
	signatureVerifier *WebhookSignature

	// Event handlers
	handlers       map[string]EventHandler
	mu             sync.RWMutex

	// Configuration
	config         *ProcessorConfig
}

// ProcessorConfig holds configuration for the webhook processor
type ProcessorConfig struct {
	// WebhookSecret for signature verification
	WebhookSecret string

	// MaxRetries for failed event processing
	MaxRetries int

	// RetryDelay between retry attempts
	RetryDelay time.Duration

	// EventTTL is how long to keep processed events
	EventTTL time.Duration

	// SkipSignatureVerification for development/testing
	SkipSignatureVerification bool
}

// DefaultProcessorConfig returns default processor configuration
func DefaultProcessorConfig() *ProcessorConfig {
	return &ProcessorConfig{
		MaxRetries:    3,
		RetryDelay:    5 * time.Second,
		EventTTL:      30 * 24 * time.Hour, // 30 days
		SkipSignatureVerification: false,
	}
}

// EventHandler is a function that handles a specific event type
type EventHandler func(ctx context.Context, payload *OdooWebhookPayload) error

// NewWebhookProcessor creates a new webhook processor
func NewWebhookProcessor(db *gorm.DB, logger *logrus.Logger, config *ProcessorConfig) *WebhookProcessor {
	if config == nil {
		config = DefaultProcessorConfig()
	}

	if logger == nil {
		logger = logrus.New()
	}

	wp := &WebhookProcessor{
		db:       db,
		logger:   logger,
		config:   config,
		handlers: make(map[string]EventHandler),
	}

	// Initialize signature verifier if secret is configured
	if config.WebhookSecret != "" {
		wp.signatureVerifier = NewWebhookSignature(config.WebhookSecret)
	}

	// Register default event handlers
	wp.registerDefaultHandlers()

	return wp
}

// registerDefaultHandlers registers the default event handlers
func (wp *WebhookProcessor) registerDefaultHandlers() {
	wp.RegisterHandler(EventSubscriptionCreated, wp.ProcessSubscriptionCreated)
	wp.RegisterHandler(EventSubscriptionUpdated, wp.ProcessSubscriptionUpdated)
	wp.RegisterHandler(EventSubscriptionCancelled, wp.ProcessSubscriptionCancelled)
	wp.RegisterHandler(EventSubscriptionRenewed, wp.ProcessSubscriptionRenewed)
	wp.RegisterHandler(EventSubscriptionExpired, wp.processSubscriptionExpired)
	wp.RegisterHandler(EventSubscriptionSuspended, wp.processSubscriptionSuspended)
	wp.RegisterHandler(EventPaymentSucceeded, wp.processPaymentSucceeded)
	wp.RegisterHandler(EventPaymentFailed, wp.processPaymentFailed)
}

// RegisterHandler registers a handler for a specific event type
func (wp *WebhookProcessor) RegisterHandler(eventType string, handler EventHandler) {
	wp.mu.Lock()
	defer wp.mu.Unlock()
	wp.handlers[eventType] = handler
}

// VerifySignature verifies the webhook signature
func (wp *WebhookProcessor) VerifySignature(payload []byte, signature string) error {
	if wp.config.SkipSignatureVerification {
		wp.logger.Warn("Signature verification is disabled")
		return nil
	}

	if wp.signatureVerifier == nil {
		return &ProcessorError{
			Code:    ErrCodeSignatureInvalid,
			Message: "webhook secret not configured",
		}
	}

	if err := wp.signatureVerifier.Verify(payload, signature); err != nil {
		return &ProcessorError{
			Code:    ErrCodeSignatureInvalid,
			Message: "signature verification failed",
			Err:     err,
		}
	}

	return nil
}

// ProcessWebhook is the main entry point for processing a webhook
func (wp *WebhookProcessor) ProcessWebhook(ctx context.Context, payload *OdooWebhookPayload, signature string, rawPayload []byte) error {
	// Create webhook event record
	event := &WebhookEvent{
		ID:             uuid.New().String(),
		WebhookID:      payload.WebhookID,
		IdempotencyKey: payload.IdempotencyKey,
		EventType:      payload.EventType,
		UserID:         payload.Data.UserID,
		TenantID:       payload.Data.TenantID,
		Status:         "received",
		ReceivedAt:     time.Now().UTC(),
		CreatedAt:      time.Now().UTC(),
	}

	// Store raw payload
	if payloadBytes, err := json.Marshal(payload); err == nil {
		event.Payload = string(payloadBytes)
	}

	// Check for duplicate event (idempotency)
	isDuplicate, err := wp.checkDuplicateEvent(ctx, payload.IdempotencyKey)
	if err != nil {
		wp.logger.WithError(err).Error("Failed to check duplicate event")
		// Continue processing - better to potentially process twice than miss an event
	}

	if isDuplicate {
		wp.logger.WithField("idempotency_key", payload.IdempotencyKey).Info("Duplicate event detected, skipping")
		return nil // Not an error, just skip
	}

	// Record the event
	if err := wp.recordEvent(ctx, event); err != nil {
		wp.logger.WithError(err).Error("Failed to record webhook event")
		// Continue processing
	}

	// Verify signature
	if err := wp.VerifySignature(rawPayload, signature); err != nil {
		event.Status = "failed"
		event.ErrorMessage = err.Error()
		wp.updateEventStatus(ctx, event)
		return err
	}

	// Validate payload
	if err := payload.Validate(); err != nil {
		event.Status = "failed"
		event.ErrorMessage = err.Error()
		wp.updateEventStatus(ctx, event)
		return &ProcessorError{
			Code:    ErrCodeInvalidPayload,
			Message: "payload validation failed",
			Err:     err,
		}
	}

	// Process the event
	event.Status = "processing"
	wp.updateEventStatus(ctx, event)

	if err := wp.dispatchEvent(ctx, payload); err != nil {
		event.Status = "failed"
		event.ErrorMessage = err.Error()
		event.RetryCount++
		wp.updateEventStatus(ctx, event)
		return err
	}

	// Mark as processed
	now := time.Now().UTC()
	event.Status = "processed"
	event.ProcessedAt = &now
	wp.updateEventStatus(ctx, event)

	wp.logger.WithFields(logrus.Fields{
		"webhook_id":      payload.WebhookID,
		"event_type":      payload.EventType,
		"user_id":         payload.Data.UserID,
		"subscription_id": payload.Data.SubscriptionID,
	}).Info("Webhook processed successfully")

	return nil
}

// dispatchEvent dispatches the event to the appropriate handler
func (wp *WebhookProcessor) dispatchEvent(ctx context.Context, payload *OdooWebhookPayload) error {
	wp.mu.RLock()
	handler, exists := wp.handlers[payload.EventType]
	wp.mu.RUnlock()

	if !exists {
		wp.logger.WithField("event_type", payload.EventType).Warn("No handler registered for event type")
		return nil // Not an error, just no handler
	}

	return handler(ctx, payload)
}

// checkDuplicateEvent checks if an event with the same idempotency key exists
func (wp *WebhookProcessor) checkDuplicateEvent(ctx context.Context, idempotencyKey string) (bool, error) {
	var count int64
	err := wp.db.WithContext(ctx).Model(&WebhookEvent{}).
		Where("idempotency_key = ? AND status = ?", idempotencyKey, "processed").
		Count(&count).Error

	if err != nil {
		return false, err
	}

	return count > 0, nil
}

// recordEvent records a webhook event
func (wp *WebhookProcessor) recordEvent(ctx context.Context, event *WebhookEvent) error {
	return wp.db.WithContext(ctx).Create(event).Error
}

// updateEventStatus updates the status of a webhook event
func (wp *WebhookProcessor) updateEventStatus(ctx context.Context, event *WebhookEvent) error {
	return wp.db.WithContext(ctx).Model(event).Updates(map[string]interface{}{
		"status":        event.Status,
		"error_message": event.ErrorMessage,
		"processed_at":  event.ProcessedAt,
		"retry_count":   event.RetryCount,
	}).Error
}

// ProcessSubscriptionCreated handles subscription.created events
func (wp *WebhookProcessor) ProcessSubscriptionCreated(ctx context.Context, payload *OdooWebhookPayload) error {
	data := &payload.Data

	wp.logger.WithFields(logrus.Fields{
		"user_id":         data.UserID,
		"subscription_id": data.SubscriptionID,
		"plan":            data.OdooPlan,
		"kong_level":      data.KongLevel,
	}).Info("Processing subscription.created event")

	// Parse tier from Odoo plan
	tier := OdooSubscriptionTier(data.OdooPlan)
	if tier == "" {
		tier = OdooTierForKongLevel(data.KongLevel)
	}

	// Parse dates
	startDate := time.Now().UTC()
	if data.StartDate != "" {
		if parsed, err := time.Parse(time.RFC3339, data.StartDate); err == nil {
			startDate = parsed
		}
	}

	var endDate *time.Time
	if data.EndDate != "" {
		if parsed, err := time.Parse(time.RFC3339, data.EndDate); err == nil {
			endDate = &parsed
		}
	}

	var renewalDate *time.Time
	if data.RenewalDate != "" {
		if parsed, err := time.Parse(time.RFC3339, data.RenewalDate); err == nil {
			renewalDate = &parsed
		}
	}

	// Create subscription record
	subscription := &SubscriptionRecord{
		ID:                 uuid.New().String(),
		UserID:             data.UserID,
		TenantID:           data.TenantID,
		CompanyID:          data.CompanyID,
		OdooSubscriptionID: data.SubscriptionID,
		Tier:               tier,
		KongLevel:          data.KongLevel,
		Status:             "active",
		BillingAmount:      data.BillingAmount,
		BillingCurrency:    data.BillingCurrency,
		StartDate:          startDate,
		EndDate:            endDate,
		RenewalDate:        renewalDate,
		Limits:             GetLimitsForOdooTier(tier),
		CreatedAt:          time.Now().UTC(),
		UpdatedAt:          time.Now().UTC(),
		Version:            1,
	}

	if subscription.BillingCurrency == "" {
		subscription.BillingCurrency = "USD"
	}

	// Check if subscription already exists
	var existing SubscriptionRecord
	err := wp.db.WithContext(ctx).
		Where("odoo_subscription_id = ?", data.SubscriptionID).
		First(&existing).Error

	if err == nil {
		// Subscription already exists, update it
		return wp.ProcessSubscriptionUpdated(ctx, payload)
	}

	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return &ProcessorError{
			Code:    ErrCodeProcessingFailed,
			Message: "failed to check existing subscription",
			Err:     err,
		}
	}

	// Create new subscription
	if err := wp.db.WithContext(ctx).Create(subscription).Error; err != nil {
		return &ProcessorError{
			Code:    ErrCodeProcessingFailed,
			Message: "failed to create subscription record",
			Err:     err,
		}
	}

	wp.logger.WithFields(logrus.Fields{
		"subscription_id": subscription.ID,
		"user_id":         data.UserID,
		"tier":            tier,
	}).Info("Subscription created successfully")

	return nil
}

// ProcessSubscriptionUpdated handles subscription.updated events
func (wp *WebhookProcessor) ProcessSubscriptionUpdated(ctx context.Context, payload *OdooWebhookPayload) error {
	data := &payload.Data

	wp.logger.WithFields(logrus.Fields{
		"user_id":         data.UserID,
		"subscription_id": data.SubscriptionID,
		"new_plan":        data.OdooPlan,
		"previous_plan":   data.PreviousPlan,
		"kong_level":      data.KongLevel,
	}).Info("Processing subscription.updated event")

	// Find existing subscription
	var subscription SubscriptionRecord
	err := wp.db.WithContext(ctx).
		Where("odoo_subscription_id = ?", data.SubscriptionID).
		First(&subscription).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Subscription doesn't exist, create it
			return wp.ProcessSubscriptionCreated(ctx, payload)
		}
		return &ProcessorError{
			Code:    ErrCodeSubscriptionNotFound,
			Message: "subscription not found",
			Err:     err,
		}
	}

	// Parse new tier
	newTier := OdooSubscriptionTier(data.OdooPlan)
	if newTier == "" {
		newTier = OdooTierForKongLevel(data.KongLevel)
	}

	// Update subscription
	updates := map[string]interface{}{
		"tier":             newTier,
		"kong_level":       data.KongLevel,
		"limits":           GetLimitsForOdooTier(newTier),
		"billing_amount":   data.BillingAmount,
		"updated_at":       time.Now().UTC(),
		"version":          subscription.Version + 1,
	}

	if data.EndDate != "" {
		if parsed, err := time.Parse(time.RFC3339, data.EndDate); err == nil {
			updates["end_date"] = parsed
		}
	}

	if data.RenewalDate != "" {
		if parsed, err := time.Parse(time.RFC3339, data.RenewalDate); err == nil {
			updates["renewal_date"] = parsed
		}
	}

	result := wp.db.WithContext(ctx).Model(&subscription).
		Where("version = ?", subscription.Version).
		Updates(updates)

	if result.Error != nil {
		return &ProcessorError{
			Code:    ErrCodeProcessingFailed,
			Message: "failed to update subscription",
			Err:     result.Error,
		}
	}

	if result.RowsAffected == 0 {
		wp.logger.Warn("Subscription update had no effect (possible version conflict)")
	}

	wp.logger.WithFields(logrus.Fields{
		"subscription_id": subscription.ID,
		"user_id":         data.UserID,
		"old_tier":        subscription.Tier,
		"new_tier":        newTier,
	}).Info("Subscription updated successfully")

	return nil
}

// ProcessSubscriptionCancelled handles subscription.cancelled events
func (wp *WebhookProcessor) ProcessSubscriptionCancelled(ctx context.Context, payload *OdooWebhookPayload) error {
	data := &payload.Data

	wp.logger.WithFields(logrus.Fields{
		"user_id":         data.UserID,
		"subscription_id": data.SubscriptionID,
		"reason":          data.CancellationReason,
		"immediate":       data.ImmediateCancellation,
	}).Info("Processing subscription.cancelled event")

	// Find existing subscription
	var subscription SubscriptionRecord
	err := wp.db.WithContext(ctx).
		Where("odoo_subscription_id = ?", data.SubscriptionID).
		First(&subscription).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			wp.logger.WithField("subscription_id", data.SubscriptionID).Warn("Subscription not found for cancellation")
			return nil // Not an error, subscription may have been deleted
		}
		return &ProcessorError{
			Code:    ErrCodeSubscriptionNotFound,
			Message: "subscription not found",
			Err:     err,
		}
	}

	now := time.Now().UTC()
	updates := map[string]interface{}{
		"cancelled_at": now,
		"updated_at":   now,
		"version":      subscription.Version + 1,
	}

	// If immediate cancellation, deactivate now
	if data.ImmediateCancellation {
		updates["status"] = "cancelled"
		updates["end_date"] = now
	} else {
		// Mark as pending cancellation, will be deactivated at end_date
		updates["status"] = "pending_cancellation"
	}

	result := wp.db.WithContext(ctx).Model(&subscription).
		Where("version = ?", subscription.Version).
		Updates(updates)

	if result.Error != nil {
		return &ProcessorError{
			Code:    ErrCodeProcessingFailed,
			Message: "failed to cancel subscription",
			Err:     result.Error,
		}
	}

	wp.logger.WithFields(logrus.Fields{
		"subscription_id": subscription.ID,
		"user_id":         data.UserID,
		"immediate":       data.ImmediateCancellation,
	}).Info("Subscription cancelled successfully")

	return nil
}

// ProcessSubscriptionRenewed handles subscription.renewed events
func (wp *WebhookProcessor) ProcessSubscriptionRenewed(ctx context.Context, payload *OdooWebhookPayload) error {
	data := &payload.Data

	wp.logger.WithFields(logrus.Fields{
		"user_id":         data.UserID,
		"subscription_id": data.SubscriptionID,
		"plan":            data.OdooPlan,
	}).Info("Processing subscription.renewed event")

	// Find existing subscription
	var subscription SubscriptionRecord
	err := wp.db.WithContext(ctx).
		Where("odoo_subscription_id = ?", data.SubscriptionID).
		First(&subscription).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Subscription doesn't exist, create it
			return wp.ProcessSubscriptionCreated(ctx, payload)
		}
		return &ProcessorError{
			Code:    ErrCodeSubscriptionNotFound,
			Message: "subscription not found",
			Err:     err,
		}
	}

	now := time.Now().UTC()
	updates := map[string]interface{}{
		"status":       "active",
		"cancelled_at": nil,
		"updated_at":   now,
		"version":      subscription.Version + 1,
	}

	// Update end date if provided
	if data.EndDate != "" {
		if parsed, err := time.Parse(time.RFC3339, data.EndDate); err == nil {
			updates["end_date"] = parsed
		}
	}

	// Update renewal date if provided
	if data.RenewalDate != "" {
		if parsed, err := time.Parse(time.RFC3339, data.RenewalDate); err == nil {
			updates["renewal_date"] = parsed
		}
	}

	result := wp.db.WithContext(ctx).Model(&subscription).
		Where("version = ?", subscription.Version).
		Updates(updates)

	if result.Error != nil {
		return &ProcessorError{
			Code:    ErrCodeProcessingFailed,
			Message: "failed to renew subscription",
			Err:     result.Error,
		}
	}

	wp.logger.WithFields(logrus.Fields{
		"subscription_id": subscription.ID,
		"user_id":         data.UserID,
	}).Info("Subscription renewed successfully")

	return nil
}

// processSubscriptionExpired handles subscription.expired events
func (wp *WebhookProcessor) processSubscriptionExpired(ctx context.Context, payload *OdooWebhookPayload) error {
	data := &payload.Data

	wp.logger.WithFields(logrus.Fields{
		"user_id":         data.UserID,
		"subscription_id": data.SubscriptionID,
	}).Info("Processing subscription.expired event")

	// Find existing subscription
	var subscription SubscriptionRecord
	err := wp.db.WithContext(ctx).
		Where("odoo_subscription_id = ?", data.SubscriptionID).
		First(&subscription).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return &ProcessorError{
			Code:    ErrCodeSubscriptionNotFound,
			Message: "subscription not found",
			Err:     err,
		}
	}

	now := time.Now().UTC()
	result := wp.db.WithContext(ctx).Model(&subscription).
		Where("version = ?", subscription.Version).
		Updates(map[string]interface{}{
			"status":     "expired",
			"end_date":   now,
			"updated_at": now,
			"version":    subscription.Version + 1,
		})

	if result.Error != nil {
		return &ProcessorError{
			Code:    ErrCodeProcessingFailed,
			Message: "failed to expire subscription",
			Err:     result.Error,
		}
	}

	return nil
}

// processSubscriptionSuspended handles subscription.suspended events
func (wp *WebhookProcessor) processSubscriptionSuspended(ctx context.Context, payload *OdooWebhookPayload) error {
	data := &payload.Data

	wp.logger.WithFields(logrus.Fields{
		"user_id":         data.UserID,
		"subscription_id": data.SubscriptionID,
	}).Info("Processing subscription.suspended event")

	// Find existing subscription
	var subscription SubscriptionRecord
	err := wp.db.WithContext(ctx).
		Where("odoo_subscription_id = ?", data.SubscriptionID).
		First(&subscription).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return &ProcessorError{
			Code:    ErrCodeSubscriptionNotFound,
			Message: "subscription not found",
			Err:     err,
		}
	}

	now := time.Now().UTC()
	result := wp.db.WithContext(ctx).Model(&subscription).
		Where("version = ?", subscription.Version).
		Updates(map[string]interface{}{
			"status":     "suspended",
			"updated_at": now,
			"version":    subscription.Version + 1,
		})

	if result.Error != nil {
		return &ProcessorError{
			Code:    ErrCodeProcessingFailed,
			Message: "failed to suspend subscription",
			Err:     result.Error,
		}
	}

	return nil
}

// processPaymentSucceeded handles payment.succeeded events
func (wp *WebhookProcessor) processPaymentSucceeded(ctx context.Context, payload *OdooWebhookPayload) error {
	data := &payload.Data

	wp.logger.WithFields(logrus.Fields{
		"user_id":    data.UserID,
		"invoice_id": data.InvoiceID,
		"amount":     data.BillingAmount,
	}).Info("Processing payment.succeeded event")

	// Update subscription if it was suspended due to payment failure
	var subscription SubscriptionRecord
	err := wp.db.WithContext(ctx).
		Where("odoo_subscription_id = ?", data.SubscriptionID).
		First(&subscription).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil // No subscription to update
		}
		return &ProcessorError{
			Code:    ErrCodeSubscriptionNotFound,
			Message: "subscription not found",
			Err:     err,
		}
	}

	// If subscription was suspended, reactivate it
	if subscription.Status == "suspended" {
		now := time.Now().UTC()
		wp.db.WithContext(ctx).Model(&subscription).
			Updates(map[string]interface{}{
				"status":     "active",
				"updated_at": now,
				"version":    subscription.Version + 1,
			})

	}

	return nil
}

// processPaymentFailed handles payment.failed events
func (wp *WebhookProcessor) processPaymentFailed(ctx context.Context, payload *OdooWebhookPayload) error {
	data := &payload.Data

	wp.logger.WithFields(logrus.Fields{
		"user_id":    data.UserID,
		"invoice_id": data.InvoiceID,
	}).Warn("Processing payment.failed event")

	// This could trigger a grace period or suspension
	// For now, just log the event - actual suspension should come from Odoo
	// via subscription.suspended event

	return nil
}

// GetSubscriptionByUserID retrieves a subscription by user ID
func (wp *WebhookProcessor) GetSubscriptionByUserID(ctx context.Context, userID string) (*SubscriptionRecord, error) {
	var subscription SubscriptionRecord
	err := wp.db.WithContext(ctx).
		Where("user_id = ? AND status IN ?", userID, []string{"active", "pending_cancellation"}).
		Order("created_at DESC").
		First(&subscription).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return &subscription, nil
}

// GetSubscriptionByOdooID retrieves a subscription by Odoo subscription ID
func (wp *WebhookProcessor) GetSubscriptionByOdooID(ctx context.Context, odooID string) (*SubscriptionRecord, error) {
	var subscription SubscriptionRecord
	err := wp.db.WithContext(ctx).
		Where("odoo_subscription_id = ?", odooID).
		First(&subscription).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return &subscription, nil
}

// CleanupOldEvents removes processed events older than the configured TTL
func (wp *WebhookProcessor) CleanupOldEvents(ctx context.Context) (int64, error) {
	cutoff := time.Now().Add(-wp.config.EventTTL)

	result := wp.db.WithContext(ctx).
		Where("status = ? AND processed_at < ?", "processed", cutoff).
		Delete(&WebhookEvent{})

	if result.Error != nil {
		return 0, result.Error
	}

	return result.RowsAffected, nil
}

// RetryFailedEvents retries failed events
func (wp *WebhookProcessor) RetryFailedEvents(ctx context.Context) (int, error) {
	var failedEvents []WebhookEvent
	err := wp.db.WithContext(ctx).
		Where("status = ? AND retry_count < ?", "failed", wp.config.MaxRetries).
		Find(&failedEvents).Error

	if err != nil {
		return 0, err
	}

	retried := 0
	for _, event := range failedEvents {
		var payload OdooWebhookPayload
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			wp.logger.WithError(err).WithField("event_id", event.ID).Error("Failed to parse event payload for retry")
			continue
		}

		if err := wp.dispatchEvent(ctx, &payload); err != nil {
			wp.logger.WithError(err).WithField("event_id", event.ID).Error("Retry failed")
			event.RetryCount++
			event.ErrorMessage = err.Error()
			wp.db.WithContext(ctx).Save(&event)
			continue
		}

		now := time.Now().UTC()
		event.Status = "processed"
		event.ProcessedAt = &now
		wp.db.WithContext(ctx).Save(&event)
		retried++
	}

	return retried, nil
}
