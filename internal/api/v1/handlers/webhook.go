// Package handlers contains HTTP handlers for REST API v1
package handlers

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/rantipay/trading-mcp/internal/subscription"
)

// WebhookHandler handles webhook-related endpoints
type WebhookHandler struct {
	processor *subscription.WebhookProcessor
	logger    *logrus.Logger
}

// WebhookHandlerConfig holds configuration for the webhook handler
type WebhookHandlerConfig struct {
	Processor *subscription.WebhookProcessor
	Logger    *logrus.Logger
}

// NewWebhookHandler creates a new webhook handler
func NewWebhookHandler(config *WebhookHandlerConfig) *WebhookHandler {
	if config == nil {
		config = &WebhookHandlerConfig{}
	}

	logger := config.Logger
	if logger == nil {
		logger = logrus.New()
	}

	return &WebhookHandler{
		processor: config.Processor,
		logger:    logger,
	}
}

// HandleOdooSubscriptionWebhook handles POST /api/v1/webhooks/odoo/subscription
// This endpoint receives subscription events from Odoo
func (h *WebhookHandler) HandleOdooSubscriptionWebhook(c *gin.Context) {
	startTime := time.Now()

	// Read the raw body for signature verification
	rawBody, err := io.ReadAll(c.Request.Body)
	if err != nil {
		h.logger.WithError(err).Error("Failed to read request body")
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_REQUEST", "Failed to read request body"))
		return
	}

	// Get the signature from headers
	signature := c.GetHeader("X-Odoo-Signature")
	if signature == "" {
		// Also check alternative header names
		signature = c.GetHeader("X-Webhook-Signature")
	}
	if signature == "" {
		signature = c.GetHeader("X-Hub-Signature-256")
	}

	// Log the incoming webhook
	h.logger.WithFields(logrus.Fields{
		"content_length": len(rawBody),
		"has_signature":  signature != "",
		"content_type":   c.ContentType(),
		"remote_addr":    c.ClientIP(),
		"user_agent":     c.Request.UserAgent(),
	}).Info("Received Odoo subscription webhook")

	// Parse the payload
	var payload subscription.OdooWebhookPayload
	if err := payload.FromJSON(rawBody); err != nil {
		h.logger.WithError(err).WithField("body", string(rawBody)).Error("Failed to parse webhook payload")
		c.JSON(http.StatusBadRequest, models.NewErrorResponseWithDetails("INVALID_PAYLOAD", "Failed to parse webhook payload", err.Error()))
		return
	}

	// Validate the payload
	if err := payload.Validate(); err != nil {
		h.logger.WithError(err).WithFields(logrus.Fields{
			"webhook_id": payload.WebhookID,
			"event_type": payload.EventType,
		}).Error("Invalid webhook payload")
		c.JSON(http.StatusBadRequest, models.NewErrorResponseWithDetails("VALIDATION_FAILED", "Webhook payload validation failed", err.Error()))
		return
	}

	// Check if processor is configured
	if h.processor == nil {
		h.logger.Error("Webhook processor not configured")
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("SERVICE_UNAVAILABLE", "Webhook processing is not configured"))
		return
	}

	// Process the webhook
	ctx := c.Request.Context()
	if err := h.processor.ProcessWebhook(ctx, &payload, signature, rawBody); err != nil {
		// Check error type for appropriate response
		var procErr *subscription.ProcessorError
		if pe, ok := err.(*subscription.ProcessorError); ok {
			procErr = pe
		}

		if procErr != nil {
			switch procErr.Code {
			case subscription.ErrCodeDuplicateEvent:
				// Duplicate events are not errors from Odoo's perspective
				h.logger.WithField("idempotency_key", payload.IdempotencyKey).Info("Duplicate webhook event")
				c.JSON(http.StatusOK, gin.H{
					"success":   true,
					"status":    "duplicate",
					"message":   "Event already processed",
					"timestamp": time.Now(),
				})
				return

			case subscription.ErrCodeSignatureInvalid:
				h.logger.WithField("webhook_id", payload.WebhookID).Warn("Invalid webhook signature")
				c.JSON(http.StatusUnauthorized, models.NewErrorResponse("SIGNATURE_INVALID", "Webhook signature verification failed"))
				return

			case subscription.ErrCodeInvalidPayload:
				c.JSON(http.StatusBadRequest, models.NewErrorResponseWithDetails("INVALID_PAYLOAD", "Invalid webhook payload", procErr.Message))
				return
			}
		}

		// Log the processing error
		h.logger.WithError(err).WithFields(logrus.Fields{
			"webhook_id":      payload.WebhookID,
			"event_type":      payload.EventType,
			"user_id":         payload.Data.UserID,
			"subscription_id": payload.Data.SubscriptionID,
		}).Error("Failed to process webhook")

		// Return 500 so Odoo will retry
		c.JSON(http.StatusInternalServerError, models.NewErrorResponseWithDetails("PROCESSING_FAILED", "Webhook processing failed", err.Error()))
		return
	}

	// Success response
	duration := time.Since(startTime)
	h.logger.WithFields(logrus.Fields{
		"webhook_id":  payload.WebhookID,
		"event_type":  payload.EventType,
		"duration_ms": duration.Milliseconds(),
	}).Info("Webhook processed successfully")

	c.JSON(http.StatusOK, gin.H{
		"success":     true,
		"status":      "processed",
		"webhook_id":  payload.WebhookID,
		"event_type":  payload.EventType,
		"duration_ms": duration.Milliseconds(),
		"timestamp":   time.Now(),
	})
}

// GetWebhookStatus handles GET /api/v1/webhooks/status
// Returns the status of the webhook processing system
func (h *WebhookHandler) GetWebhookStatus(c *gin.Context) {
	if h.processor == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("SERVICE_UNAVAILABLE", "Webhook processing is not configured"))
		return
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"status":    "operational",
		"timestamp": time.Now(),
	}))
}

// GetSubscriptionStatus handles GET /api/v1/webhooks/subscription/:user_id
// Returns the subscription status for a user
func (h *WebhookHandler) GetSubscriptionStatus(c *gin.Context) {
	userID := c.Param("user_id")
	if userID == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_REQUEST", "User ID is required"))
		return
	}

	if h.processor == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("SERVICE_UNAVAILABLE", "Webhook processing is not configured"))
		return
	}

	ctx := c.Request.Context()
	sub, err := h.processor.GetSubscriptionByUserID(ctx, userID)
	if err != nil {
		h.logger.WithError(err).WithField("user_id", userID).Error("Failed to get subscription")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("SUBSCRIPTION_ERROR", "Failed to get subscription status"))
		return
	}

	if sub == nil {
		c.JSON(http.StatusNotFound, models.NewErrorResponse("NOT_FOUND", "No active subscription found for user"))
		return
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"subscription_id": sub.ID,
		"user_id":         sub.UserID,
		"tier":            sub.Tier,
		"kong_level":      sub.KongLevel,
		"status":          sub.Status,
		"start_date":      sub.StartDate,
		"end_date":        sub.EndDate,
		"renewal_date":    sub.RenewalDate,
		"is_active":       sub.IsActive(),
		"days_remaining":  sub.DaysUntilExpiration(),
		"limits":          sub.Limits,
	}))
}

// WebhookHealthResponse represents the health check response for webhooks
type WebhookHealthResponse struct {
	Status    string    `json:"status"`
	Service   string    `json:"service"`
	Timestamp time.Time `json:"timestamp"`
}

// HealthCheck handles GET /api/v1/webhooks/health
func (h *WebhookHandler) HealthCheck(c *gin.Context) {
	status := "healthy"
	if h.processor == nil {
		status = "degraded"
	}

	c.JSON(http.StatusOK, WebhookHealthResponse{
		Status:    status,
		Service:   "webhook-processor",
		Timestamp: time.Now(),
	})
}

// ValidateWebhookSignature is a middleware that validates webhook signatures
func ValidateWebhookSignature(secret string, logger *logrus.Logger) gin.HandlerFunc {
	verifier := subscription.NewWebhookSignature(secret)

	return func(c *gin.Context) {
		// Skip validation if no secret is configured
		if secret == "" {
			logger.Warn("Webhook signature validation skipped - no secret configured")
			c.Next()
			return
		}

		// Read the body
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_REQUEST", "Failed to read request body"))
			c.Abort()
			return
		}

		// Restore the body for later use
		c.Request.Body = io.NopCloser(strings.NewReader(string(body)))

		// Get signature
		signature := c.GetHeader("X-Odoo-Signature")
		if signature == "" {
			signature = c.GetHeader("X-Webhook-Signature")
		}
		if signature == "" {
			signature = c.GetHeader("X-Hub-Signature-256")
		}

		// Verify signature
		if err := verifier.Verify(body, signature); err != nil {
			logger.WithError(err).WithField("remote_addr", c.ClientIP()).Warn("Webhook signature verification failed")
			c.JSON(http.StatusUnauthorized, models.NewErrorResponse("SIGNATURE_INVALID", "Webhook signature verification failed"))
			c.Abort()
			return
		}

		c.Next()
	}
}

// RateLimitWebhooks is a middleware that rate limits webhook requests
func RateLimitWebhooks(requestsPerMinute int, logger *logrus.Logger) gin.HandlerFunc {
	// Simple in-memory rate limiter
	// For production, consider using Valkey-based rate limiting
	type clientInfo struct {
		count    int
		resetAt  time.Time
	}
	clients := make(map[string]*clientInfo)
	var mu sync.Mutex

	return func(c *gin.Context) {
		clientIP := c.ClientIP()

		mu.Lock()
		now := time.Now()

		info, exists := clients[clientIP]
		if !exists || now.After(info.resetAt) {
			clients[clientIP] = &clientInfo{
				count:   1,
				resetAt: now.Add(time.Minute),
			}
			mu.Unlock()
			c.Next()
			return
		}

		info.count++
		if info.count > requestsPerMinute {
			mu.Unlock()
			logger.WithFields(logrus.Fields{
				"client_ip": clientIP,
				"count":     info.count,
				"limit":     requestsPerMinute,
			}).Warn("Webhook rate limit exceeded")
			c.JSON(http.StatusTooManyRequests, models.NewErrorResponse("RATE_LIMIT_EXCEEDED", "Too many webhook requests"))
			c.Abort()
			return
		}

		mu.Unlock()
		c.Next()
	}
}
