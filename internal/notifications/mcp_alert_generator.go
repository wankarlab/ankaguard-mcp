package notifications

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"
)

// MCPAlertGenerator generates alerts based on MCP tool results
type MCPAlertGenerator struct {
	logger              *logrus.Logger
	notificationManager *NotificationManager
	alertRules          map[string]*AlertRule
	ctx                 context.Context
}

// AlertRule is the runtime alert rule held by MCPAlertGenerator.
// It is richer than config.AlertRuleConfig (which is the YAML representation):
// AlertRule carries a typed AlertConfiguration, resolved time.Duration for
// cooldown, live timestamps, and per-user metadata. Config loading code
// converts config.AlertRuleConfig → AlertRule before calling AddAlertRule.
type AlertRule struct {
	ID          string                 `json:"id"`
	ToolName    string                 `json:"tool_name"`
	Conditions  []AlertCondition       `json:"conditions"`
	AlertConfig *AlertConfiguration    `json:"alert_config"`
	UserID      string                 `json:"user_id"`
	Active      bool                   `json:"active"`
	CreatedAt   time.Time              `json:"created_at"`
	LastTriggered *time.Time           `json:"last_triggered,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// AlertCondition is the runtime counterpart of config.AlertConditionConfig.
type AlertCondition struct {
	Field    string      `json:"field"`     // Field path in the result (e.g., "price", "rsi.value", "portfolio.total_value")
	Operator string      `json:"operator"`  // "gt", "lt", "eq", "gte", "lte", "contains", "not_contains"
	Value    interface{} `json:"value"`     // Value to compare against
	Type     string      `json:"type"`      // "number", "string", "boolean"
}

// AlertConfiguration defines how the alert should be sent
type AlertConfiguration struct {
	Title       string                 `json:"title"`
	MessageTemplate string             `json:"message_template"`
	Severity    AlertSeverity          `json:"severity"`
	Channels    []string               `json:"channels"` // "websocket", "push", "slack", "webhook"
	Cooldown    time.Duration          `json:"cooldown"` // Minimum time between alerts
	Data        map[string]interface{} `json:"data,omitempty"`
}

// NewMCPAlertGenerator creates a new MCP alert generator
func NewMCPAlertGenerator(logger *logrus.Logger, notificationManager *NotificationManager) *MCPAlertGenerator {
	return &MCPAlertGenerator{
		logger:              logger,
		notificationManager: notificationManager,
		alertRules:          make(map[string]*AlertRule),
		ctx:                 context.Background(),
	}
}

// AddAlertRule adds a new alert rule
func (mag *MCPAlertGenerator) AddAlertRule(rule *AlertRule) error {
	if rule.ID == "" {
		rule.ID = fmt.Sprintf("rule_%d", time.Now().UnixNano())
	}
	
	if rule.CreatedAt.IsZero() {
		rule.CreatedAt = time.Now()
	}

	mag.alertRules[rule.ID] = rule

	mag.logger.WithFields(logrus.Fields{
		"rule_id":   rule.ID,
		"tool_name": rule.ToolName,
		"user_id":   rule.UserID,
	}).Info("Added MCP alert rule")

	return nil
}

// RemoveAlertRule removes an alert rule
func (mag *MCPAlertGenerator) RemoveAlertRule(ruleID string) error {
	if _, exists := mag.alertRules[ruleID]; !exists {
		return fmt.Errorf("alert rule not found: %s", ruleID)
	}

	delete(mag.alertRules, ruleID)

	mag.logger.WithField("rule_id", ruleID).Info("Removed MCP alert rule")
	return nil
}

// ProcessMCPResult processes an MCP tool result and checks for alert conditions
func (mag *MCPAlertGenerator) ProcessMCPResult(toolName string, result map[string]interface{}, userID string) error {
	// Find applicable rules for this tool and user
	applicableRules := make([]*AlertRule, 0)
	for _, rule := range mag.alertRules {
		if rule.ToolName == toolName && rule.UserID == userID && rule.Active {
			applicableRules = append(applicableRules, rule)
		}
	}

	if len(applicableRules) == 0 {
		return nil // No rules to process
	}

	mag.logger.WithFields(logrus.Fields{
		"tool_name":        toolName,
		"user_id":          userID,
		"applicable_rules": len(applicableRules),
	}).Debug("Processing MCP result for alerts")

	// Check each rule
	for _, rule := range applicableRules {
		if mag.shouldTriggerAlert(rule, result) {
			if err := mag.triggerAlert(rule, result); err != nil {
				mag.logger.WithError(err).WithField("rule_id", rule.ID).Error("Failed to trigger alert")
			}
		}
	}

	return nil
}

// shouldTriggerAlert checks if an alert should be triggered based on conditions
func (mag *MCPAlertGenerator) shouldTriggerAlert(rule *AlertRule, result map[string]interface{}) bool {
	// Check cooldown
	if rule.LastTriggered != nil && rule.AlertConfig.Cooldown > 0 {
		if time.Since(*rule.LastTriggered) < rule.AlertConfig.Cooldown {
			return false
		}
	}

	// Check all conditions (AND logic)
	for _, condition := range rule.Conditions {
		if !mag.evaluateCondition(condition, result) {
			return false
		}
	}

	return true
}

// evaluateCondition evaluates a single alert condition
func (mag *MCPAlertGenerator) evaluateCondition(condition AlertCondition, result map[string]interface{}) bool {
	// Get field value from result using dot notation
	fieldValue := mag.getFieldValue(condition.Field, result)
	if fieldValue == nil {
		return false
	}

	// Convert values for comparison
	switch condition.Type {
	case "number":
		return mag.compareNumbers(fieldValue, condition.Value, condition.Operator)
	case "string":
		return mag.compareStrings(fieldValue, condition.Value, condition.Operator)
	case "boolean":
		return mag.compareBooleans(fieldValue, condition.Value, condition.Operator)
	default:
		mag.logger.WithField("type", condition.Type).Warn("Unknown condition type")
		return false
	}
}

// getFieldValue extracts a field value using dot notation (e.g., \"portfolio.total_value\")
func (mag *MCPAlertGenerator) getFieldValue(fieldPath string, data map[string]interface{}) interface{} {
	// Simple implementation - can be extended for complex nested paths
	if value, exists := data[fieldPath]; exists {
		return value
	}

	// Handle simple nested paths (one level deep)
	// For more complex paths, you'd want to use a proper JSON path library
	for _, value := range data {
		if nestedMap, ok := value.(map[string]interface{}); ok {
			if nestedValue, exists := nestedMap[fieldPath]; exists {
				return nestedValue
			}
		}
	}

	return nil
}

// compareNumbers compares numeric values
func (mag *MCPAlertGenerator) compareNumbers(fieldValue, conditionValue interface{}, operator string) bool {
	var fVal, cVal float64
	var ok bool

	// Convert field value to float64
	switch v := fieldValue.(type) {
	case float64:
		fVal = v
	case float32:
		fVal = float64(v)
	case int:
		fVal = float64(v)
	case int64:
		fVal = float64(v)
	case string:
		if fVal, ok = parseFloat(v); !ok {
			return false
		}
	default:
		return false
	}

	// Convert condition value to float64
	switch v := conditionValue.(type) {
	case float64:
		cVal = v
	case float32:
		cVal = float64(v)
	case int:
		cVal = float64(v)
	case int64:
		cVal = float64(v)
	case string:
		if cVal, ok = parseFloat(v); !ok {
			return false
		}
	default:
		return false
	}

	// Compare based on operator
	switch operator {
	case "gt":
		return fVal > cVal
	case "gte":
		return fVal >= cVal
	case "lt":
		return fVal < cVal
	case "lte":
		return fVal <= cVal
	case "eq":
		return fVal == cVal
	default:
		return false
	}
}

// compareStrings compares string values
func (mag *MCPAlertGenerator) compareStrings(fieldValue, conditionValue interface{}, operator string) bool {
	fVal := fmt.Sprintf("%v", fieldValue)
	cVal := fmt.Sprintf("%v", conditionValue)

	switch operator {
	case "eq":
		return fVal == cVal
	case "contains":
		return contains(fVal, cVal)
	case "not_contains":
		return !contains(fVal, cVal)
	default:
		return false
	}
}

// compareBooleans compares boolean values
func (mag *MCPAlertGenerator) compareBooleans(fieldValue, conditionValue interface{}, operator string) bool {
	fVal, fOk := fieldValue.(bool)
	cVal, cOk := conditionValue.(bool)

	if !fOk || !cOk {
		return false
	}

	switch operator {
	case "eq":
		return fVal == cVal
	default:
		return false
	}
}

// triggerAlert triggers an alert based on a rule
func (mag *MCPAlertGenerator) triggerAlert(rule *AlertRule, result map[string]interface{}) error {
	// Update last triggered time
	now := time.Now()
	rule.LastTriggered = &now

	// Generate alert message
	message := mag.generateAlertMessage(rule.AlertConfig.MessageTemplate, result)

	// Create notification data
	notificationData := map[string]interface{}{
		"rule_id":   rule.ID,
		"tool_name": rule.ToolName,
		"result":    result,
		"timestamp": now,
	}

	// Add custom data
	if rule.AlertConfig.Data != nil {
		for k, v := range rule.AlertConfig.Data {
			notificationData[k] = v
		}
	}

	// Send notifications based on configured channels
	for _, channel := range rule.AlertConfig.Channels {
		switch channel {
		case "websocket":
			if err := mag.sendWebSocketAlert(rule, message, notificationData); err != nil {
				mag.logger.WithError(err).Error("Failed to send WebSocket alert")
			}
		case "push":
			if err := mag.sendPushAlert(rule, message, notificationData); err != nil {
				mag.logger.WithError(err).Error("Failed to send push alert")
			}
		case "slack":
			if err := mag.sendSlackAlert(rule, message, notificationData); err != nil {
				mag.logger.WithError(err).Error("Failed to send Slack alert")
			}
		}
	}

	mag.logger.WithFields(logrus.Fields{
		"rule_id":   rule.ID,
		"tool_name": rule.ToolName,
		"user_id":   rule.UserID,
		"channels":  rule.AlertConfig.Channels,
	}).Info("Triggered MCP alert")

	return nil
}

// generateAlertMessage generates an alert message from a template
func (mag *MCPAlertGenerator) generateAlertMessage(template string, result map[string]interface{}) string {
	// Simple template replacement - can be extended with a proper template engine
	message := template

	// Replace common placeholders
	for key, value := range result {
		placeholder := fmt.Sprintf("{{%s}}", key)
		replacement := fmt.Sprintf("%v", value)
		message = replaceAll(message, placeholder, replacement)
	}

	return message
}

// sendWebSocketAlert sends alert via WebSocket
func (mag *MCPAlertGenerator) sendWebSocketAlert(rule *AlertRule, message string, data map[string]interface{}) error {
	return mag.notificationManager.wsManager.SendNotificationToUser(rule.UserID, "alert", data)
}

// sendPushAlert sends alert via push notification
func (mag *MCPAlertGenerator) sendPushAlert(rule *AlertRule, message string, data map[string]interface{}) error {
	// Get user device tokens (placeholder - would come from user service)
	deviceTokens := []string{} // Would be retrieved from user preferences

	return mag.notificationManager.pushService.SendTradingAlert(
		mag.ctx,
		deviceTokens,
		"mcp_alert",
		rule.ToolName,
		message,
		data,
	)
}

// sendSlackAlert sends alert via Slack
func (mag *MCPAlertGenerator) sendSlackAlert(rule *AlertRule, message string, data map[string]interface{}) error {
	// Use push service for Slack integration
	notification := &PushNotification{
		Title: rule.AlertConfig.Title,
		Body:  message,
		Data:  data,
	}

	return mag.notificationManager.pushService.sendSlackNotification(mag.ctx, notification)
}

// CreatePriceAlertRule creates a price alert rule for MCP tools
func (mag *MCPAlertGenerator) CreatePriceAlertRule(userID, symbol string, threshold float64, operator string) *AlertRule {
	return &AlertRule{
		ID:       fmt.Sprintf("price_alert_%s_%s_%d", userID, symbol, time.Now().Unix()),
		ToolName: "get_price",
		UserID:   userID,
		Active:   true,
		Conditions: []AlertCondition{
			{
				Field:    "price",
				Operator: operator,
				Value:    threshold,
				Type:     "number",
			},
		},
		AlertConfig: &AlertConfiguration{
			Title:           fmt.Sprintf("Price Alert - %s", symbol),
			MessageTemplate: fmt.Sprintf("%s price is {{price}} (threshold: %.2f)", symbol, threshold),
			Severity:        SeverityInfo,
			Channels:        []string{"websocket", "push"},
			Cooldown:        5 * time.Minute,
		},
		Metadata: map[string]interface{}{
			"symbol":    symbol,
			"threshold": threshold,
		},
	}
}

// CreateRSIAlertRule creates an RSI alert rule
func (mag *MCPAlertGenerator) CreateRSIAlertRule(userID, symbol string, rsiThreshold float64, operator string) *AlertRule {
	return &AlertRule{
		ID:       fmt.Sprintf("rsi_alert_%s_%s_%d", userID, symbol, time.Now().Unix()),
		ToolName: "get_rsi",
		UserID:   userID,
		Active:   true,
		Conditions: []AlertCondition{
			{
				Field:    "rsi",
				Operator: operator,
				Value:    rsiThreshold,
				Type:     "number",
			},
		},
		AlertConfig: &AlertConfiguration{
			Title:           fmt.Sprintf("RSI Alert - %s", symbol),
			MessageTemplate: fmt.Sprintf("%s RSI is {{rsi}} (threshold: %.2f)", symbol, rsiThreshold),
			Severity:        SeverityWarning,
			Channels:        []string{"websocket", "push"},
			Cooldown:        10 * time.Minute,
		},
		Metadata: map[string]interface{}{
			"symbol":       symbol,
			"rsi_threshold": rsiThreshold,
		},
	}
}

// GetAlertRules returns all alert rules for a user
func (mag *MCPAlertGenerator) GetAlertRules(userID string) []*AlertRule {
	rules := make([]*AlertRule, 0)
	for _, rule := range mag.alertRules {
		if rule.UserID == userID {
			rules = append(rules, rule)
		}
	}
	return rules
}

// Helper functions
func parseFloat(s string) (float64, bool) {
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f, true
	}
	return 0, false
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || 
		(len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func replaceAll(s, old, new string) string {
	result := ""
	for {
		i := findIndex(s, old)
		if i == -1 {
			result += s
			break
		}
		result += s[:i] + new
		s = s[i+len(old):]
	}
	return result
}

func findIndex(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
