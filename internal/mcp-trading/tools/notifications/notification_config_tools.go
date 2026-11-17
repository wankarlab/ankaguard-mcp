package notifications

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	mcpserver "github.com/rantipay/trading-mcp/internal/mcp-trading/server"
)

// resolveTargetUserID derives the user_id a notification-preferences tool
// should act on. By default it ignores any client-supplied "user_id" and
// uses the authenticated caller's own id (TOOL-04: don't trust body params
// for identity). An authenticated level>=4 admin may act on behalf of
// another user by supplying "user_id" explicitly; that override is logged.
func resolveTargetUserID(ctx context.Context, params map[string]interface{}, toolName string) (string, *models.MCPError) {
	callerID, hasCaller := mcpserver.UserIDFromContext(ctx)
	requested := getString(params, "user_id")

	if !hasCaller {
		// No authenticated identity in context (e.g. stdio mode, or JWT
		// auth disabled) — fall back to whatever was supplied, preserving
		// prior behavior for those deployment modes.
		if requested == "" {
			return "", &models.MCPError{Code: -32602, Message: "Invalid user_id parameter"}
		}
		return requested, nil
	}

	if requested == "" || requested == callerID {
		return callerID, nil
	}

	level, _ := mcpserver.UserLevelFromContext(ctx)
	if level < 4 {
		return "", &models.MCPError{
			Code:    -32001,
			Message: "Forbidden: cannot act on another user's notification settings",
		}
	}

	logrus.WithFields(logrus.Fields{
		"tool":        toolName,
		"admin_id":    callerID,
		"target_user": requested,
	}).Warn("Admin acting on behalf of another user (notification preferences)")

	return requested, nil
}

// ConfigureNotificationsTool configures push notification settings
type ConfigureNotificationsTool struct {
	BaseTool
}

func NewConfigureNotificationsTool(client *gctclient.Client) *ConfigureNotificationsTool {
	return &ConfigureNotificationsTool{BaseTool: BaseTool{client: client}}
}

func (t *ConfigureNotificationsTool) Name() string {
	return "configure_notifications"
}

func (t *ConfigureNotificationsTool) Description() string {
	return "Configure push notification settings including FCM and APNS"
}

func (t *ConfigureNotificationsTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"fcm_server_key": map[string]interface{}{
				"type":        "string",
				"description": "Firebase Cloud Messaging server key",
			},
			"fcm_endpoint": map[string]interface{}{
				"type":        "string",
				"description": "FCM endpoint URL (optional, uses default if not provided)",
			},
			"apns_key_id": map[string]interface{}{
				"type":        "string",
				"description": "Apple Push Notification Service key ID (optional)",
			},
			"apns_team_id": map[string]interface{}{
				"type":        "string",
				"description": "APNS team ID (optional)",
			},
			"apns_key_path": map[string]interface{}{
				"type":        "string",
				"description": "Path to APNS key file (optional)",
			},
			"enabled": map[string]interface{}{
				"type":        "boolean",
				"description": "Enable or disable notifications",
			},
		},
		"required": []string{"enabled"},
	}
}

func (t *ConfigureNotificationsTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	enabled, ok := params["enabled"].(bool)
	if !ok {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid enabled parameter",
		}
	}

	config := &models.NotificationConfig{
		Enabled:      enabled,
		FCMServerKey: getString(params, "fcm_server_key"),
		FCMEndpoint:  getString(params, "fcm_endpoint"),
		APNSKeyID:    getString(params, "apns_key_id"),
		APNSTeamID:   getString(params, "apns_team_id"),
		APNSKeyPath:  getString(params, "apns_key_path"),
	}

	// Set default FCM endpoint if not provided
	if config.FCMEndpoint == "" {
		config.FCMEndpoint = "https://fcm.googleapis.com/fcm/send"
	}

	// Configure notifications through client
	err := t.client.ConfigureNotifications(ctx, config)
	if err != nil {
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Failed to configure notifications",
			Data:    err.Error(),
		}
	}

	return map[string]interface{}{
		"success": true,
		"message": "Notifications configured successfully",
		"config": map[string]interface{}{
			"enabled":         config.Enabled,
			"fcm_configured":  config.FCMServerKey != "",
			"apns_configured": config.APNSKeyID != "",
		},
	}, nil
}

// SetNotificationPreferencesTool sets user notification preferences
type SetNotificationPreferencesTool struct {
	BaseTool
}

func NewSetNotificationPreferencesTool(client *gctclient.Client) *SetNotificationPreferencesTool {
	return &SetNotificationPreferencesTool{BaseTool: BaseTool{client: client}}
}

func (t *SetNotificationPreferencesTool) Name() string {
	return "set_notification_preferences"
}

func (t *SetNotificationPreferencesTool) Description() string {
	return "Set user notification preferences and device tokens"
}

func (t *SetNotificationPreferencesTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"user_id": map[string]interface{}{
				"type":        "string",
				"description": "User ID",
			},
			"device_tokens": map[string]interface{}{
				"type":        "array",
				"description": "Array of device tokens for push notifications",
				"items": map[string]interface{}{
					"type": "string",
				},
			},
			"enabled_channels": map[string]interface{}{
				"type":        "array",
				"description": "Enabled notification channels",
				"items": map[string]interface{}{
					"type": "string",
					"enum": []string{"push", "email", "sms", "webhook"},
				},
			},
			"trading_alerts": map[string]interface{}{
				"type":        "boolean",
				"description": "Enable trading alerts",
			},
			"price_alerts": map[string]interface{}{
				"type":        "boolean",
				"description": "Enable price alerts",
			},
			"risk_alerts": map[string]interface{}{
				"type":        "boolean",
				"description": "Enable risk alerts",
			},
			"system_alerts": map[string]interface{}{
				"type":        "boolean",
				"description": "Enable system alerts",
			},
			"quiet_hours_start": map[string]interface{}{
				"type":        "string",
				"description": "Quiet hours start time (HH:MM format)",
			},
			"quiet_hours_end": map[string]interface{}{
				"type":        "string",
				"description": "Quiet hours end time (HH:MM format)",
			},
			"timezone": map[string]interface{}{
				"type":        "string",
				"description": "User timezone (e.g., 'America/New_York')",
			},
		},
		"required": []string{"user_id"},
	}
}

func (t *SetNotificationPreferencesTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	userID, mcpErr := resolveTargetUserID(ctx, params, "set_notification_preferences")
	if mcpErr != nil {
		return nil, mcpErr
	}

	// Parse device tokens
	var deviceTokens []string
	if tokens, ok := params["device_tokens"].([]interface{}); ok {
		deviceTokens = make([]string, 0, len(tokens))
		for _, token := range tokens {
			if tokenStr, ok := token.(string); ok {
				deviceTokens = append(deviceTokens, tokenStr)
			}
		}
	}

	// Parse enabled channels
	var enabledChannels []string
	if channels, ok := params["enabled_channels"].([]interface{}); ok {
		enabledChannels = make([]string, 0, len(channels))
		for _, channel := range channels {
			if channelStr, ok := channel.(string); ok {
				enabledChannels = append(enabledChannels, channelStr)
			}
		}
	}

	preferences := &models.NotificationPreferences{
		UserID:          userID,
		DeviceTokens:    deviceTokens,
		EnabledChannels: enabledChannels,
		TradingAlerts:   getBool(params, "trading_alerts"),
		PriceAlerts:     getBool(params, "price_alerts"),
		RiskAlerts:      getBool(params, "risk_alerts"),
		SystemAlerts:    getBool(params, "system_alerts"),
		QuietHoursStart: getString(params, "quiet_hours_start"),
		QuietHoursEnd:   getString(params, "quiet_hours_end"),
		Timezone:        getString(params, "timezone"),
	}

	// Set preferences through client
	err := t.client.SetNotificationPreferences(ctx, preferences)
	if err != nil {
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Failed to set notification preferences",
			Data:    err.Error(),
		}
	}

	return map[string]interface{}{
		"success":     true,
		"message":     "Notification preferences set successfully",
		"preferences": preferences,
	}, nil
}

// GetNotificationPreferencesTool gets user notification preferences
type GetNotificationPreferencesTool struct {
	BaseTool
}

func NewGetNotificationPreferencesTool(client *gctclient.Client) *GetNotificationPreferencesTool {
	return &GetNotificationPreferencesTool{BaseTool: BaseTool{client: client}}
}

func (t *GetNotificationPreferencesTool) Name() string {
	return "get_notification_preferences"
}

func (t *GetNotificationPreferencesTool) Description() string {
	return "Get user notification preferences"
}

func (t *GetNotificationPreferencesTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"user_id": map[string]interface{}{
				"type":        "string",
				"description": "User ID",
			},
		},
		"required": []string{"user_id"},
	}
}

func (t *GetNotificationPreferencesTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	userID, mcpErr := resolveTargetUserID(ctx, params, "get_notification_preferences")
	if mcpErr != nil {
		return nil, mcpErr
	}

	preferences, err := t.client.GetNotificationPreferences(ctx, userID)
	if err != nil {
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Failed to get notification preferences",
			Data:    err.Error(),
		}
	}

	return map[string]interface{}{
		"success":     true,
		"preferences": preferences,
	}, nil
}

// TestNotificationTool sends a test notification
type TestNotificationTool struct {
	BaseTool
}

func NewTestNotificationTool(client *gctclient.Client) *TestNotificationTool {
	return &TestNotificationTool{BaseTool: BaseTool{client: client}}
}

func (t *TestNotificationTool) Name() string {
	return "test_notification"
}

func (t *TestNotificationTool) Description() string {
	return "Send a test notification to verify configuration"
}

func (t *TestNotificationTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"device_tokens": map[string]interface{}{
				"type":        "array",
				"description": "Array of device tokens to send test to",
				"items": map[string]interface{}{
					"type": "string",
				},
			},
			"title": map[string]interface{}{
				"type":        "string",
				"description": "Test notification title",
			},
			"body": map[string]interface{}{
				"type":        "string",
				"description": "Test notification body",
			},
			"channel": map[string]interface{}{
				"type":        "string",
				"description": "Notification channel to test",
				"enum":        []string{"push", "webhook", "email"},
			},
		},
		"required": []string{"device_tokens", "title", "body"},
	}
}

func (t *TestNotificationTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	// Parse device tokens
	tokensData, ok := params["device_tokens"].([]interface{})
	if !ok {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid device_tokens parameter",
		}
	}

	deviceTokens := make([]string, 0, len(tokensData))
	for _, token := range tokensData {
		if tokenStr, ok := token.(string); ok {
			deviceTokens = append(deviceTokens, tokenStr)
		}
	}

	title, ok := params["title"].(string)
	if !ok {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid title parameter",
		}
	}

	body, ok := params["body"].(string)
	if !ok {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid body parameter",
		}
	}

	channel := getString(params, "channel")
	if channel == "" {
		channel = "push"
	}

	// Send test notification through client
	result, err := t.client.SendTestNotification(ctx, deviceTokens, title, body, channel)
	if err != nil {
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Failed to send test notification",
			Data:    err.Error(),
		}
	}

	return map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Test notification sent via %s", channel),
		"result":  result,
	}, nil
}

// CreateFundingRateAlertTool creates funding rate alerts
type CreateFundingRateAlertTool struct {
	BaseTool
}

func NewCreateFundingRateAlertTool(client *gctclient.Client) *CreateFundingRateAlertTool {
	return &CreateFundingRateAlertTool{BaseTool: BaseTool{client: client}}
}

func (t *CreateFundingRateAlertTool) Name() string {
	return "create_funding_rate_alert"
}

func (t *CreateFundingRateAlertTool) Description() string {
	return "Create an alert for funding rate thresholds"
}

func (t *CreateFundingRateAlertTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name",
			},
			"pair": map[string]interface{}{
				"type":        "string",
				"description": "Trading pair (e.g., 'BTC-USDT')",
			},
			"threshold": map[string]interface{}{
				"type":        "number",
				"description": "Funding rate threshold (e.g., 0.01 for 1%)",
			},
			"direction": map[string]interface{}{
				"type":        "string",
				"description": "Alert direction",
				"enum":        []string{"above", "below"},
			},
		},
		"required": []string{"exchange", "pair", "threshold", "direction"},
	}
}

func (t *CreateFundingRateAlertTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, ok := params["exchange"].(string)
	if !ok {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid exchange parameter",
		}
	}

	pair, ok := params["pair"].(string)
	if !ok {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid pair parameter",
		}
	}

	threshold, ok := params["threshold"].(float64)
	if !ok {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid threshold parameter",
		}
	}

	direction, ok := params["direction"].(string)
	if !ok {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid direction parameter",
		}
	}

	alert := &models.FundingRateAlert{
		Exchange:  exchange,
		Pair:      pair,
		Threshold: threshold,
		Direction: direction,
		Active:    true,
	}

	result, err := t.client.CreateFundingRateAlert(ctx, alert)
	if err != nil {
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Failed to create funding rate alert",
			Data:    err.Error(),
		}
	}

	return map[string]interface{}{
		"success":  true,
		"alert_id": result.ID,
		"message":  fmt.Sprintf("Funding rate alert created for %s %s %.4f", pair, direction, threshold),
		"alert":    result,
	}, nil
}

// RegisterFCMTokenTool stores a Flutter FCM device token
type RegisterFCMTokenTool struct {
	BaseTool
}

func NewRegisterFCMTokenTool(client *gctclient.Client) *RegisterFCMTokenTool {
	return &RegisterFCMTokenTool{BaseTool: BaseTool{client: client}}
}

func (t *RegisterFCMTokenTool) Name() string {
	return "register_fcm_token"
}

func (t *RegisterFCMTokenTool) Description() string {
	return "Register a Flutter FCM device token for push notifications"
}

func (t *RegisterFCMTokenTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"token": map[string]interface{}{
				"type":        "string",
				"description": "FCM device token",
			},
			"platform": map[string]interface{}{
				"type":        "string",
				"description": "Device platform (flutter, android, ios)",
			},
			"timestamp": map[string]interface{}{
				"type":        "string",
				"description": "ISO8601 registration timestamp",
			},
		},
		"required": []string{"token"},
	}
}

func (t *RegisterFCMTokenTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	token, ok := params["token"].(string)
	if !ok || token == "" {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid token parameter",
		}
	}

	platform := getString(params, "platform")
	if platform == "" {
		platform = "flutter"
	}

	// Store token in global registry (used by the notification system)
	RegisterDeviceToken(token, platform)

	return map[string]interface{}{
		"success":  true,
		"message":  fmt.Sprintf("FCM token registered for platform %s", platform),
		"platform": platform,
	}, nil
}

// deviceTokens holds registered FCM tokens in memory
var deviceTokens []string

// RegisterDeviceToken stores an FCM token for later use
func RegisterDeviceToken(token, platform string) {
	for _, t := range deviceTokens {
		if t == token {
			return
		}
	}
	deviceTokens = append(deviceTokens, token)
}

// GetDeviceTokens returns all registered FCM tokens
func GetDeviceTokens() []string {
	return deviceTokens
}

// Helper functions
func getString(params map[string]interface{}, key string) string {
	if val, ok := params[key].(string); ok {
		return val
	}
	return ""
}

func getBool(params map[string]interface{}, key string) bool {
	if val, ok := params[key].(bool); ok {
		return val
	}
	return false
}
