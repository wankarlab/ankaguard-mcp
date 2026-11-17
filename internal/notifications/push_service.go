package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

// PushService handles push notifications
type PushService struct {
	logger     *logrus.Logger
	httpClient *http.Client
	config     *PushConfig
}

// PushConfig contains push notification configuration
type PushConfig struct {
	FCMServerKey     string `json:"fcm_server_key"`
	FCMEndpoint      string `json:"fcm_endpoint"`
	APNSKeyID        string `json:"apns_key_id"`
	APNSTeamID       string `json:"apns_team_id"`
	APNSBundleID     string `json:"apns_bundle_id"`
	APNSEndpoint     string `json:"apns_endpoint"`
	WebhookURL       string `json:"webhook_url"`
	SlackWebhookURL  string `json:"slack_webhook_url"`
	TelegramBotToken string `json:"telegram_bot_token"`
	TelegramChatID   string `json:"telegram_chat_id"`
	Enabled          bool   `json:"enabled"`
}

// PushNotification represents a push notification
type PushNotification struct {
	Title    string                 `json:"title"`
	Body     string                 `json:"body"`
	Data     map[string]interface{} `json:"data,omitempty"`
	Priority string                 `json:"priority,omitempty"`
	Sound    string                 `json:"sound,omitempty"`
	Badge    int                    `json:"badge,omitempty"`
}

// FCMMessage represents a Firebase Cloud Messaging message
type FCMMessage struct {
	To              string                 `json:"to,omitempty"`
	RegistrationIDs []string               `json:"registration_ids,omitempty"`
	Notification    *FCMNotification       `json:"notification,omitempty"`
	Data            map[string]interface{} `json:"data,omitempty"`
	Priority        string                 `json:"priority,omitempty"`
}

// FCMNotification represents FCM notification payload
type FCMNotification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Sound string `json:"sound,omitempty"`
	Badge int    `json:"badge,omitempty"`
}

// SlackMessage represents a Slack webhook message
type SlackMessage struct {
	Text        string            `json:"text"`
	Username    string            `json:"username,omitempty"`
	IconEmoji   string            `json:"icon_emoji,omitempty"`
	Channel     string            `json:"channel,omitempty"`
	Attachments []SlackAttachment `json:"attachments,omitempty"`
}

// SlackAttachment represents a Slack message attachment
type SlackAttachment struct {
	Color     string       `json:"color,omitempty"`
	Title     string       `json:"title,omitempty"`
	Text      string       `json:"text,omitempty"`
	Fields    []SlackField `json:"fields,omitempty"`
	Timestamp int64        `json:"ts,omitempty"`
}

// SlackField represents a Slack attachment field
type SlackField struct {
	Title string `json:"title"`
	Value string `json:"value"`
	Short bool   `json:"short"`
}

// NewPushService creates a new push service
func NewPushService(logger *logrus.Logger, config *PushConfig) *PushService {
	return &PushService{
		logger: logger,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		config: config,
	}
}

// SendPushNotification sends a push notification to specified devices
func (ps *PushService) SendPushNotification(ctx context.Context, deviceTokens []string, notification *PushNotification) error {
	if !ps.config.Enabled {
		ps.logger.Debug("Push notifications disabled, skipping send")
		return nil
	}

	if len(deviceTokens) == 0 {
		ps.logger.Warn("No device tokens provided for push notification")
		return nil
	}

	// Send FCM notification
	if ps.config.FCMServerKey != "" && ps.config.FCMEndpoint != "" {
		if err := ps.sendFCMNotification(ctx, deviceTokens, notification); err != nil {
			ps.logger.WithError(err).Error("Failed to send FCM notification")
			return err
		}
	}

	// Send webhook notification
	if ps.config.WebhookURL != "" {
		if err := ps.sendWebhookNotification(ctx, notification); err != nil {
			ps.logger.WithError(err).Error("Failed to send webhook notification")
		}
	}

	// Send Slack notification
	if ps.config.SlackWebhookURL != "" {
		if err := ps.sendSlackNotification(ctx, notification); err != nil {
			ps.logger.WithError(err).Error("Failed to send Slack notification")
		}
	}

	// Send Telegram notification
	if ps.config.TelegramBotToken != "" && ps.config.TelegramChatID != "" {
		if err := ps.sendTelegramNotification(ctx, notification); err != nil {
			ps.logger.WithError(err).Error("Failed to send Telegram notification")
		}
	}

	return nil
}

// sendFCMNotification sends notification via Firebase Cloud Messaging
func (ps *PushService) sendFCMNotification(ctx context.Context, deviceTokens []string, notification *PushNotification) error {
	fcmMessage := &FCMMessage{
		RegistrationIDs: deviceTokens,
		Notification: &FCMNotification{
			Title: notification.Title,
			Body:  notification.Body,
			Sound: notification.Sound,
			Badge: notification.Badge,
		},
		Data:     notification.Data,
		Priority: notification.Priority,
	}

	if fcmMessage.Priority == "" {
		fcmMessage.Priority = "high"
	}

	jsonData, err := json.Marshal(fcmMessage)
	if err != nil {
		return fmt.Errorf("failed to marshal FCM message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", ps.config.FCMEndpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create FCM request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "key="+ps.config.FCMServerKey)

	resp, err := ps.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send FCM request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("FCM request failed with status: %d", resp.StatusCode)
	}

	ps.logger.WithFields(logrus.Fields{
		"device_count": len(deviceTokens),
		"title":        notification.Title,
	}).Info("FCM notification sent successfully")

	return nil
}

// sendWebhookNotification sends notification via webhook
func (ps *PushService) sendWebhookNotification(ctx context.Context, notification *PushNotification) error {
	webhookData := map[string]interface{}{
		"title":     notification.Title,
		"body":      notification.Body,
		"data":      notification.Data,
		"timestamp": time.Now().Unix(),
	}

	jsonData, err := json.Marshal(webhookData)
	if err != nil {
		return fmt.Errorf("failed to marshal webhook data: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", ps.config.WebhookURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create webhook request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := ps.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send webhook request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook request failed with status: %d", resp.StatusCode)
	}

	ps.logger.WithField("title", notification.Title).Info("Webhook notification sent successfully")
	return nil
}

// sendSlackNotification sends notification to Slack
func (ps *PushService) sendSlackNotification(ctx context.Context, notification *PushNotification) error {
	color := "good"
	if notification.Priority == "high" {
		color = "warning"
	}

	slackMessage := &SlackMessage{
		Username:  "Trading Bot",
		IconEmoji: ":chart_with_upwards_trend:",
		Attachments: []SlackAttachment{
			{
				Color:     color,
				Title:     notification.Title,
				Text:      notification.Body,
				Timestamp: time.Now().Unix(),
				Fields:    []SlackField{},
			},
		},
	}

	// Add data fields if present
	if notification.Data != nil {
		for key, value := range notification.Data {
			field := SlackField{
				Title: key,
				Value: fmt.Sprintf("%v", value),
				Short: true,
			}
			slackMessage.Attachments[0].Fields = append(slackMessage.Attachments[0].Fields, field)
		}
	}

	jsonData, err := json.Marshal(slackMessage)
	if err != nil {
		return fmt.Errorf("failed to marshal Slack message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", ps.config.SlackWebhookURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create Slack request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := ps.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send Slack request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Slack request failed with status: %d", resp.StatusCode)
	}

	ps.logger.WithField("title", notification.Title).Info("Slack notification sent successfully")
	return nil
}

// SendTradingAlert sends a trading-specific alert
func (ps *PushService) SendTradingAlert(ctx context.Context, deviceTokens []string, alertType, symbol, message string, data map[string]interface{}) error {
	var title string
	var priority string

	switch alertType {
	case "price_alert":
		title = fmt.Sprintf("Price Alert - %s", symbol)
		priority = "normal"
	case "order_filled":
		title = fmt.Sprintf("Order Filled - %s", symbol)
		priority = "high"
	case "risk_alert":
		title = "Risk Alert"
		priority = "high"
	case "strategy_alert":
		title = "Strategy Alert"
		priority = "normal"
	default:
		title = "Trading Alert"
		priority = "normal"
	}

	notification := &PushNotification{
		Title:    title,
		Body:     message,
		Data:     data,
		Priority: priority,
		Sound:    "default",
	}

	return ps.SendPushNotification(ctx, deviceTokens, notification)
}

// SendSystemAlert sends a system-level alert
func (ps *PushService) SendSystemAlert(ctx context.Context, deviceTokens []string, severity, message string, data map[string]interface{}) error {
	var title string
	var priority string

	switch severity {
	case "critical":
		title = "🚨 Critical Alert"
		priority = "high"
	case "warning":
		title = "⚠️ Warning"
		priority = "normal"
	case "info":
		title = "ℹ️ Information"
		priority = "normal"
	default:
		title = "System Alert"
		priority = "normal"
	}

	notification := &PushNotification{
		Title:    title,
		Body:     message,
		Data:     data,
		Priority: priority,
		Sound:    "default",
	}

	return ps.SendPushNotification(ctx, deviceTokens, notification)
}

// SendWebhook sends a webhook notification to a custom URL
func (ps *PushService) SendWebhook(ctx context.Context, url string, headers map[string]string, notification *PushNotification) error {
	webhookData := map[string]interface{}{
		"title":     notification.Title,
		"body":      notification.Body,
		"data":      notification.Data,
		"timestamp": time.Now().Unix(),
	}

	jsonData, err := json.Marshal(webhookData)
	if err != nil {
		return fmt.Errorf("failed to marshal webhook data: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create webhook request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// Add custom headers
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := ps.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send webhook request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook request failed with status: %d", resp.StatusCode)
	}

	ps.logger.WithFields(logrus.Fields{
		"url":   url,
		"title": notification.Title,
	}).Info("Custom webhook notification sent successfully")

	return nil
}

// sendTelegramNotification sends notification via Telegram Bot API
func (ps *PushService) sendTelegramNotification(ctx context.Context, notification *PushNotification) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", ps.config.TelegramBotToken)

	// Format message with HTML
	emoji := "📊"
	if notification.Priority == "high" {
		emoji = "🚨"
	}

	message := fmt.Sprintf("<b>%s %s</b>\n\n%s", emoji, notification.Title, notification.Body)

	// Add data fields if present
	if notification.Data != nil && len(notification.Data) > 0 {
		message += "\n\n<b>Details:</b>"
		for key, value := range notification.Data {
			message += fmt.Sprintf("\n• %s: <code>%v</code>", key, value)
		}
	}

	payload := map[string]interface{}{
		"chat_id":    ps.config.TelegramChatID,
		"text":       message,
		"parse_mode": "HTML",
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal Telegram payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create Telegram request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := ps.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send Telegram request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Telegram API returned status %d", resp.StatusCode)
	}

	ps.logger.WithField("title", notification.Title).Info("Telegram notification sent successfully")
	return nil
}

// GetDefaultConfig returns default push service configuration
func GetDefaultPushConfig() *PushConfig {
	return &PushConfig{
		FCMEndpoint:  "https://fcm.googleapis.com/fcm/send",
		APNSEndpoint: "https://api.push.apple.com",
		Enabled:      false, // Disabled by default
	}
}
