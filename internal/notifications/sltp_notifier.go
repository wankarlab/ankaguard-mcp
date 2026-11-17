package notifications

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/domain"
)

// SLTPNotifier handles notifications for stop loss and take profit executions
type SLTPNotifier struct {
	notificationManager *NotificationManager
	logger              *logrus.Logger
}

// SLTPExecutionEvent represents an SL/TP execution event
type SLTPExecutionEvent struct {
	Type           string    `json:"type"` // "stop_loss" or "take_profit"
	PositionID     string    `json:"position_id"`
	Symbol         string    `json:"symbol"`
	Exchange       string    `json:"exchange"`
	TriggerPrice   float64   `json:"trigger_price"`
	ExecutionPrice float64   `json:"execution_price"`
	Size           float64   `json:"size"`
	PnL            float64   `json:"pnl"`
	Reason         string    `json:"reason"`
	UserID         string    `json:"user_id,omitempty"`
	ExecutedAt     time.Time `json:"executed_at"`
}

// WebhookConfig represents webhook configuration for notifications
type WebhookConfig struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Enabled bool              `json:"enabled"`
}

// NotificationConfig represents notification configuration
type NotificationConfig struct {
	WebhookEnabled bool           `json:"webhook_enabled"`
	WebhookConfig  *WebhookConfig `json:"webhook_config,omitempty"`
	LogEnabled     bool           `json:"log_enabled"`
	WSEnabled      bool           `json:"ws_enabled"`
}

// NewSLTPNotifier creates a new SLTP notifier
func NewSLTPNotifier(notificationManager *NotificationManager, logger *logrus.Logger) *SLTPNotifier {
	if logger == nil {
		logger = logrus.New()
	}

	return &SLTPNotifier{
		notificationManager: notificationManager,
		logger:              logger,
	}
}

// NotifyExecution sends notifications for SL/TP execution
func (n *SLTPNotifier) NotifyExecution(ctx context.Context, event *SLTPExecutionEvent, config *NotificationConfig) error {
	// Create notification
	notification := n.createNotification(event)

	// Send WebSocket notification if enabled
	if config.WSEnabled {
		if err := n.sendWebSocketNotification(notification); err != nil {
			n.logger.WithError(err).Error("Failed to send WebSocket notification")
		}
	}

	// Send webhook notification if enabled
	if config.WebhookEnabled && config.WebhookConfig != nil && config.WebhookConfig.Enabled {
		if err := n.sendWebhookNotification(ctx, event, config.WebhookConfig); err != nil {
			n.logger.WithError(err).Error("Failed to send webhook notification")
		}
	}

	// Log notification if enabled
	if config.LogEnabled {
		n.logExecution(event)
	}

	return nil
}

// NotifyStopLoss sends notification for stop loss execution
func (n *SLTPNotifier) NotifyStopLoss(ctx context.Context, executionHistory *domain.ExecutionHistory, config *NotificationConfig) error {
	event := &SLTPExecutionEvent{
		Type:           "stop_loss",
		PositionID:     executionHistory.PositionID,
		Symbol:         executionHistory.Symbol,
		Exchange:       executionHistory.Exchange,
		TriggerPrice:   executionHistory.TriggerPrice.InexactFloat64(),
		ExecutionPrice: executionHistory.ExecutionPrice.InexactFloat64(),
		Size:           executionHistory.Size.InexactFloat64(),
		PnL:            executionHistory.PnL.InexactFloat64(),
		Reason:         executionHistory.Reason,
		ExecutedAt:     executionHistory.ExecutedAt,
	}

	return n.NotifyExecution(ctx, event, config)
}

// NotifyTakeProfit sends notification for take profit execution
func (n *SLTPNotifier) NotifyTakeProfit(ctx context.Context, executionHistory *domain.ExecutionHistory, config *NotificationConfig) error {
	event := &SLTPExecutionEvent{
		Type:           "take_profit",
		PositionID:     executionHistory.PositionID,
		Symbol:         executionHistory.Symbol,
		Exchange:       executionHistory.Exchange,
		TriggerPrice:   executionHistory.TriggerPrice.InexactFloat64(),
		ExecutionPrice: executionHistory.ExecutionPrice.InexactFloat64(),
		Size:           executionHistory.Size.InexactFloat64(),
		PnL:            executionHistory.PnL.InexactFloat64(),
		Reason:         executionHistory.Reason,
		ExecutedAt:     executionHistory.ExecutedAt,
	}

	return n.NotifyExecution(ctx, event, config)
}

// createNotification creates a notification from an execution event
func (n *SLTPNotifier) createNotification(event *SLTPExecutionEvent) *Notification {
	severity := SeverityInfo
	if event.PnL < 0 {
		severity = SeverityWarning
	}

	title := "Stop Loss Executed"
	if event.Type == "take_profit" {
		title = "Take Profit Executed"
	}

	message := n.formatMessage(event)

	return &Notification{
		ID:       fmt.Sprintf("sltp_%s_%d", event.Type, time.Now().UnixNano()),
		AlertID:  fmt.Sprintf("sltp_auto_%s", event.PositionID),
		Type:     AlertType(fmt.Sprintf("sltp_%s", event.Type)),
		Severity: severity,
		Title:    title,
		Message:  message,
		Data: map[string]interface{}{
			"event": event,
		},
		UserID:    event.UserID,
		Timestamp: event.ExecutedAt,
		Read:      false,
	}
}

// formatMessage formats the notification message
func (n *SLTPNotifier) formatMessage(event *SLTPExecutionEvent) string {
	pnlSign := "+"
	if event.PnL < 0 {
		pnlSign = ""
	}

	actionType := "Stop Loss"
	if event.Type == "take_profit" {
		actionType = "Take Profit"
	}

	return fmt.Sprintf(
		"%s executed for %s: %.8f %s at $%.6f (trigger: $%.6f) | PnL: %s$%.2f | Reason: %s",
		actionType,
		event.Symbol,
		event.Size,
		event.Symbol,
		event.ExecutionPrice,
		event.TriggerPrice,
		pnlSign,
		event.PnL,
		event.Reason,
	)
}

// sendWebSocketNotification sends notification via WebSocket
func (n *SLTPNotifier) sendWebSocketNotification(notification *Notification) error {
	return n.notificationManager.wsManager.BroadcastNotification("sltp_notification", notification)
}

// sendWebhookNotification sends notification via webhook
func (n *SLTPNotifier) sendWebhookNotification(ctx context.Context, event *SLTPExecutionEvent, config *WebhookConfig) error {
	// Create webhook payload
	payload := map[string]interface{}{
		"type":      "sltp_execution",
		"timestamp": event.ExecutedAt.Format(time.RFC3339),
		"event":     event,
	}

	// Use the push service to send webhook
	webhookNotification := &PushNotification{
		Title: fmt.Sprintf("SL/TP Executed - %s", event.Symbol),
		Body:  n.formatMessage(event),
		Data:  payload,
	}

	// Send via push service (which handles webhooks)
	return n.notificationManager.pushService.SendWebhook(ctx, config.URL, config.Headers, webhookNotification)
}

// logExecution logs the execution event
func (n *SLTPNotifier) logExecution(event *SLTPExecutionEvent) {
	n.logger.WithFields(logrus.Fields{
		"type":            event.Type,
		"position_id":     event.PositionID,
		"symbol":          event.Symbol,
		"exchange":        event.Exchange,
		"trigger_price":   event.TriggerPrice,
		"execution_price": event.ExecutionPrice,
		"size":            event.Size,
		"pnl":             event.PnL,
		"reason":          event.Reason,
		"executed_at":     event.ExecutedAt,
	}).Info("SL/TP execution notification")
}

// GetDefaultConfig returns default notification configuration
func GetDefaultSLTPNotificationConfig() *NotificationConfig {
	return &NotificationConfig{
		WebhookEnabled: false,
		WebhookConfig: &WebhookConfig{
			URL:     "",
			Headers: make(map[string]string),
			Enabled: false,
		},
		LogEnabled: true,
		WSEnabled:  true,
	}
}

// ValidateConfig validates notification configuration
func ValidateNotificationConfig(config *NotificationConfig) error {
	if config == nil {
		return fmt.Errorf("notification config cannot be nil")
	}

	if config.WebhookEnabled {
		if config.WebhookConfig == nil {
			return fmt.Errorf("webhook config is required when webhook is enabled")
		}
		if config.WebhookConfig.URL == "" {
			return fmt.Errorf("webhook URL is required when webhook is enabled")
		}
	}

	return nil
}
