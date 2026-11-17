package notifications

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/gct"
)

// notificationBroadcaster is the only WebSocket capability NotificationManager needs.
type notificationBroadcaster interface {
	BroadcastNotification(msgType string, data interface{}) error
	SendNotificationToUser(userID, msgType string, data interface{}) error
}

// NotificationManager manages alerts and notifications
type NotificationManager struct {
	gctClient         gct.GCTClient
	wsManager         notificationBroadcaster
	pushService       *PushService
	mcpAlertGen       *MCPAlertGenerator
	gctAlertForwarder *GCTAlertForwarder
	logger            *logrus.Logger
	alerts            map[string]*Alert
	subscribers       map[string][]string // alert_id -> connection_ids
	mu                sync.RWMutex
	ctx               context.Context
	cancel            context.CancelFunc
	alertHandlers     map[AlertType]AlertHandler
}

// AlertType represents different types of alerts
type AlertType string

const (
	AlertTypePriceThreshold AlertType = "price_threshold"
	AlertTypeVolumeSpike    AlertType = "volume_spike"
	AlertTypeOrderFilled    AlertType = "order_filled"
	AlertTypeOrderCanceled  AlertType = "order_canceled"
	AlertTypePositionChange AlertType = "position_change"
	AlertTypeRiskLimit      AlertType = "risk_limit"
	AlertTypeCustom         AlertType = "custom"
	AlertTypeSystem         AlertType = "system"
)

// AlertSeverity represents alert severity levels
type AlertSeverity string

const (
	SeverityInfo     AlertSeverity = "info"
	SeverityWarning  AlertSeverity = "warning"
	SeverityError    AlertSeverity = "error"
	SeverityCritical AlertSeverity = "critical"
)

// Alert represents a notification alert
type Alert struct {
	ID          string                 `json:"id"`
	Type        AlertType              `json:"type"`
	Severity    AlertSeverity          `json:"severity"`
	Title       string                 `json:"title"`
	Message     string                 `json:"message"`
	Data        map[string]interface{} `json:"data"`
	Exchange    string                 `json:"exchange,omitempty"`
	Symbol      string                 `json:"symbol,omitempty"`
	UserID      string                 `json:"user_id,omitempty"`
	Conditions  map[string]interface{} `json:"conditions,omitempty"`
	Active      bool                   `json:"active"`
	Triggered   bool                   `json:"triggered"`
	CreatedAt   time.Time              `json:"created_at"`
	TriggeredAt *time.Time             `json:"triggered_at,omitempty"`
	ExpiresAt   *time.Time             `json:"expires_at,omitempty"`
}

// AlertHandler defines the interface for handling different alert types
type AlertHandler interface {
	CheckCondition(alert *Alert, data map[string]interface{}) bool
	FormatMessage(alert *Alert, data map[string]interface{}) string
}

// Notification represents a triggered notification
type Notification struct {
	ID        string                 `json:"id"`
	AlertID   string                 `json:"alert_id"`
	Type      AlertType              `json:"type"`
	Severity  AlertSeverity          `json:"severity"`
	Title     string                 `json:"title"`
	Message   string                 `json:"message"`
	Data      map[string]interface{} `json:"data"`
	UserID    string                 `json:"user_id,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
	Read      bool                   `json:"read"`
}

// NewNotificationManager creates a new notification manager
func NewNotificationManager(gctClient gct.GCTClient, wsManager notificationBroadcaster, logger *logrus.Logger) *NotificationManager {
	ctx, cancel := context.WithCancel(context.Background())

	nm := &NotificationManager{
		gctClient:     gctClient,
		wsManager:     wsManager,
		logger:        logger,
		alerts:        make(map[string]*Alert),
		subscribers:   make(map[string][]string),
		ctx:           ctx,
		cancel:        cancel,
		alertHandlers: make(map[AlertType]AlertHandler),
	}

	// Initialize push service with default config
	pushConfig := GetDefaultPushConfig()
	nm.pushService = NewPushService(logger, pushConfig)

	// Initialize MCP alert generator
	nm.mcpAlertGen = NewMCPAlertGenerator(logger, nm)

	// Initialize GCT alert forwarder
	nm.gctAlertForwarder = NewGCTAlertForwarder(logger, gctClient, nm)

	// Register default alert handlers
	nm.registerDefaultHandlers()

	return nm
}

// Start starts the notification manager
func (nm *NotificationManager) Start() error {
	nm.logger.Info("Starting notification manager")

	// Start GCT alert forwarder
	if err := nm.gctAlertForwarder.Start(); err != nil {
		return fmt.Errorf("failed to start GCT alert forwarder: %w", err)
	}

	// Start alert monitoring goroutines.
	// NOTE: order-fill/cancel alerts are intentionally NOT started here
	// (monitorOrderAlerts/checkOrderAlerts below are now dead code, kept only
	// for reference) because GCT's Order type carries no per-user attribution,
	// so this global monitor could only ever broadcast every user's fills/
	// cancellations to every connected WebSocket client (INFRA-03). Per-user
	// order alerts are already handled correctly, with real subscription-based
	// user attribution, by GCTAlertForwarder.checkOrderAlerts (see
	// gct_alert_forwarder.go), which nm.gctAlertForwarder.Start() below starts.
	go nm.monitorPriceAlerts()
	go nm.monitorSystemAlerts()
	go nm.cleanupExpiredAlerts()

	return nil
}

// Stop stops the notification manager
func (nm *NotificationManager) Stop() error {
	nm.logger.Info("Stopping notification manager")

	// Stop GCT alert forwarder
	if err := nm.gctAlertForwarder.Stop(); err != nil {
		nm.logger.WithError(err).Error("Failed to stop GCT alert forwarder")
	}

	nm.cancel()
	return nil
}

// CreateAlert creates a new alert
func (nm *NotificationManager) CreateAlert(alert *Alert) error {
	if alert.ID == "" {
		alert.ID = fmt.Sprintf("alert_%d", time.Now().UnixNano())
	}

	alert.CreatedAt = time.Now()
	alert.Active = true
	alert.Triggered = false

	nm.mu.Lock()
	nm.alerts[alert.ID] = alert
	nm.mu.Unlock()

	nm.logger.WithFields(logrus.Fields{
		"alert_id": alert.ID,
		"type":     alert.Type,
		"severity": alert.Severity,
		"title":    alert.Title,
	}).Info("Created new alert")

	return nil
}

// DeleteAlert deletes an alert
func (nm *NotificationManager) DeleteAlert(alertID string) error {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	if _, exists := nm.alerts[alertID]; !exists {
		return fmt.Errorf("alert not found: %s", alertID)
	}

	delete(nm.alerts, alertID)
	delete(nm.subscribers, alertID)

	nm.logger.WithField("alert_id", alertID).Info("Deleted alert")
	return nil
}

// SubscribeToAlert subscribes a connection to an alert
func (nm *NotificationManager) SubscribeToAlert(alertID, connectionID string) error {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	if _, exists := nm.alerts[alertID]; !exists {
		return fmt.Errorf("alert not found: %s", alertID)
	}

	subscribers := nm.subscribers[alertID]
	for _, id := range subscribers {
		if id == connectionID {
			return nil // Already subscribed
		}
	}

	nm.subscribers[alertID] = append(subscribers, connectionID)

	nm.logger.WithFields(logrus.Fields{
		"alert_id":      alertID,
		"connection_id": connectionID,
	}).Info("Subscribed to alert")

	return nil
}

// UnsubscribeFromAlert unsubscribes a connection from an alert
func (nm *NotificationManager) UnsubscribeFromAlert(alertID, connectionID string) error {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	subscribers := nm.subscribers[alertID]
	for i, id := range subscribers {
		if id == connectionID {
			nm.subscribers[alertID] = append(subscribers[:i], subscribers[i+1:]...)
			break
		}
	}

	nm.logger.WithFields(logrus.Fields{
		"alert_id":      alertID,
		"connection_id": connectionID,
	}).Info("Unsubscribed from alert")

	return nil
}

// TriggerAlert triggers an alert and sends notifications
func (nm *NotificationManager) TriggerAlert(alertID string, data map[string]interface{}) error {
	nm.mu.Lock()
	alert, exists := nm.alerts[alertID]
	if !exists || !alert.Active {
		nm.mu.Unlock()
		return fmt.Errorf("alert not found or inactive: %s", alertID)
	}

	// Mark as triggered
	now := time.Now()
	alert.Triggered = true
	alert.TriggeredAt = &now

	subscribers := make([]string, len(nm.subscribers[alertID]))
	copy(subscribers, nm.subscribers[alertID])
	nm.mu.Unlock()

	// Create notification
	notification := &Notification{
		ID:        fmt.Sprintf("notif_%d", time.Now().UnixNano()),
		AlertID:   alert.ID,
		Type:      alert.Type,
		Severity:  alert.Severity,
		Title:     alert.Title,
		Message:   alert.Message,
		Data:      data,
		UserID:    alert.UserID,
		Timestamp: now,
		Read:      false,
	}

	// Format message using handler if available
	if handler, exists := nm.alertHandlers[alert.Type]; exists {
		notification.Message = handler.FormatMessage(alert, data)
	}

	// Send notification to subscribers
	nm.sendNotification(notification, subscribers)

	nm.logger.WithFields(logrus.Fields{
		"alert_id":        alertID,
		"notification_id": notification.ID,
		"subscribers":     len(subscribers),
	}).Info("Triggered alert")

	return nil
}

// sendNotification sends a notification to subscribers
func (nm *NotificationManager) sendNotification(notification *Notification, subscribers []string) {

	if notification.UserID != "" {
		// User-specific notification (price/volume/risk/custom alerts are always
		// created with an owning userID) - deliver only to that user's connections.
		if err := nm.wsManager.SendNotificationToUser(notification.UserID, "notification", notification); err != nil {
			nm.logger.WithError(err).Error("Failed to send notification to user")
		}
	} else {
		// Genuinely system-wide alert with no owning user - broadcast is correct here.
		nm.wsManager.BroadcastNotification("notification", notification)
	}

	// Log notification
	nm.logger.WithFields(logrus.Fields{
		"notification_id": notification.ID,
		"alert_id":        notification.AlertID,
		"type":            notification.Type,
		"severity":        notification.Severity,
		"subscribers":     len(subscribers),
	}).Info("Sent notification")
}

// GetAlerts returns all alerts
func (nm *NotificationManager) GetAlerts() map[string]*Alert {
	nm.mu.RLock()
	defer nm.mu.RUnlock()

	result := make(map[string]*Alert)
	for id, alert := range nm.alerts {
		result[id] = alert
	}

	return result
}

// GetActiveAlerts returns only active alerts
func (nm *NotificationManager) GetActiveAlerts() map[string]*Alert {
	nm.mu.RLock()
	defer nm.mu.RUnlock()

	result := make(map[string]*Alert)
	for id, alert := range nm.alerts {
		if alert.Active {
			result[id] = alert
		}
	}

	return result
}

// monitorPriceAlerts monitors price-based alerts
func (nm *NotificationManager) monitorPriceAlerts() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-nm.ctx.Done():
			return
		case <-ticker.C:
			nm.checkPriceAlerts()
		}
	}
}

// checkPriceAlerts checks all price threshold alerts
func (nm *NotificationManager) checkPriceAlerts() {
	nm.mu.RLock()
	priceAlerts := make([]*Alert, 0)
	for _, alert := range nm.alerts {
		if alert.Type == AlertTypePriceThreshold && alert.Active && !alert.Triggered {
			priceAlerts = append(priceAlerts, alert)
		}
	}
	nm.mu.RUnlock()

	for _, alert := range priceAlerts {
		// Get current price
		ticker, err := nm.gctClient.GetTickerBySymbol(nm.ctx, alert.Exchange, alert.Symbol, "spot")
		if err != nil {
			nm.logger.WithError(err).WithFields(logrus.Fields{
				"alert_id": alert.ID,
				"exchange": alert.Exchange,
				"symbol":   alert.Symbol,
			}).Error("Failed to get ticker for price alert")
			continue
		}

		// Check conditions
		data := map[string]interface{}{
			"current_price": ticker.Last,
			"ticker":        ticker,
		}

		if handler, exists := nm.alertHandlers[AlertTypePriceThreshold]; exists {
			if handler.CheckCondition(alert, data) {
				nm.TriggerAlert(alert.ID, data)
			}
		}
	}
}

// NOTE (INFRA-03): the previous monitorOrderAlerts/checkOrderAlerts pair that
// lived here polled GetOrders() globally and broadcast every filled/canceled
// order to ALL WebSocket connections. GCT's Order type carries no per-user
// attribution, so there was no safe way to route those events to only their
// owning user - it was a real PnL/trade-data leak to every connected client.
// It has been removed. Per-user order alerts (with real subscription-based
// user attribution) are already handled correctly by
// GCTAlertForwarder.checkOrderAlerts in gct_alert_forwarder.go, started via
// nm.gctAlertForwarder.Start() in NotificationManager.Start().

// monitorSystemAlerts monitors system-level alerts
func (nm *NotificationManager) monitorSystemAlerts() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-nm.ctx.Done():
			return
		case <-ticker.C:
			nm.checkSystemHealth()
		}
	}
}

// checkSystemHealth checks system health and creates alerts if needed
func (nm *NotificationManager) checkSystemHealth() {
	// Check GoCryptoTrader connection
	_, err := nm.gctClient.GetEnabledExchanges(nm.ctx)
	if err != nil {
		notification := &Notification{
			ID:       fmt.Sprintf("system_notif_%d", time.Now().Unix()),
			AlertID:  "system_health",
			Type:     AlertTypeSystem,
			Severity: SeverityError,
			Title:    "GoCryptoTrader Connection Error",
			Message:  fmt.Sprintf("Failed to connect to GoCryptoTrader: %v", err),
			Data: map[string]interface{}{
				"error": err.Error(),
			},
			Timestamp: time.Now(),
			Read:      false,
		}

		nm.wsManager.BroadcastNotification("notification", notification)
	}
}

// cleanupExpiredAlerts removes expired alerts
func (nm *NotificationManager) cleanupExpiredAlerts() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-nm.ctx.Done():
			return
		case <-ticker.C:
			nm.performCleanup()
		}
	}
}

// performCleanup removes expired alerts
func (nm *NotificationManager) performCleanup() {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	now := time.Now()
	expiredCount := 0

	for id, alert := range nm.alerts {
		if alert.ExpiresAt != nil && now.After(*alert.ExpiresAt) {
			delete(nm.alerts, id)
			delete(nm.subscribers, id)
			expiredCount++
		}
	}

	if expiredCount > 0 {
		nm.logger.WithField("expired_count", expiredCount).Info("Cleaned up expired alerts")
	}
}

// GetStats returns notification manager statistics
func (nm *NotificationManager) GetStats() map[string]interface{} {
	nm.mu.RLock()
	defer nm.mu.RUnlock()

	stats := map[string]interface{}{
		"total_alerts":     len(nm.alerts),
		"active_alerts":    0,
		"triggered_alerts": 0,
		"alert_types":      make(map[string]int),
		"subscribers":      len(nm.subscribers),
	}

	for _, alert := range nm.alerts {
		if alert.Active {
			stats["active_alerts"] = stats["active_alerts"].(int) + 1
		}
		if alert.Triggered {
			stats["triggered_alerts"] = stats["triggered_alerts"].(int) + 1
		}

		types := stats["alert_types"].(map[string]int)
		types[string(alert.Type)]++
	}

	return stats
}

// GetPushService returns the push service
func (nm *NotificationManager) GetPushService() *PushService {
	return nm.pushService
}

// GetMCPAlertGenerator returns the MCP alert generator
func (nm *NotificationManager) GetMCPAlertGenerator() *MCPAlertGenerator {
	return nm.mcpAlertGen
}

// GetGCTAlertForwarder returns the GCT alert forwarder
func (nm *NotificationManager) GetGCTAlertForwarder() *GCTAlertForwarder {
	return nm.gctAlertForwarder
}

// ConfigurePushService configures the push service with new settings
func (nm *NotificationManager) ConfigurePushService(config *PushConfig) {
	nm.pushService = NewPushService(nm.logger, config)
	nm.logger.Info("Push service reconfigured")
}

// ProcessMCPToolResult processes an MCP tool result for alert generation
func (nm *NotificationManager) ProcessMCPToolResult(toolName string, result map[string]interface{}, userID string) error {
	return nm.mcpAlertGen.ProcessMCPResult(toolName, result, userID)
}

// SubscribeToGCTAlerts subscribes a user to GoCryptoTrader alerts
func (nm *NotificationManager) SubscribeToGCTAlerts(userID, alertType, exchange, symbol string) error {
	return nm.gctAlertForwarder.SubscribeToAlerts(userID, alertType, exchange, symbol)
}

// UnsubscribeFromGCTAlerts unsubscribes a user from GoCryptoTrader alerts
func (nm *NotificationManager) UnsubscribeFromGCTAlerts(userID, alertType, exchange, symbol string) error {
	return nm.gctAlertForwarder.UnsubscribeFromAlerts(userID, alertType, exchange, symbol)
}

// SendPushNotification sends a push notification
func (nm *NotificationManager) SendPushNotification(ctx context.Context, deviceTokens []string, notification *PushNotification) error {
	return nm.pushService.SendPushNotification(ctx, deviceTokens, notification)
}

// CreateMCPAlertRule creates a new MCP alert rule
func (nm *NotificationManager) CreateMCPAlertRule(rule *AlertRule) error {
	return nm.mcpAlertGen.AddAlertRule(rule)
}

// GetNotificationStats returns comprehensive notification statistics
func (nm *NotificationManager) GetNotificationStats() map[string]interface{} {
	nm.mu.RLock()
	defer nm.mu.RUnlock()

	stats := map[string]interface{}{
		"total_alerts":         len(nm.alerts),
		"active_alerts":        0,
		"total_subscribers":    len(nm.subscribers),
		"mcp_alert_rules":      len(nm.mcpAlertGen.GetAlertRules("")),
		"gct_subscriptions":    nm.gctAlertForwarder.GetSubscriptionStats(),
		"push_service_enabled": nm.pushService.config.Enabled,
	}

	// Count active alerts
	for _, alert := range nm.alerts {
		if alert.Active {
			stats["active_alerts"] = stats["active_alerts"].(int) + 1
		}
	}

	return stats
}

// TriggerCustomNotification is defined in handlers.go

// TriggerTradeNotification sends a trading-specific notification
func (nm *NotificationManager) TriggerTradeNotification(
	tradeType string, // "opened", "closed", "sl", "tp"
	pair string,
	side string,
	size float64,
	price float64,
	pnl float64,
	reason string,
) error {
	var title string
	var severity AlertSeverity
	var emoji string

	switch tradeType {
	case "opened":
		title = "Trade Opened"
		severity = SeverityInfo
		if side == "buy" || side == "long" {
			emoji = "🟢"
		} else {
			emoji = "🔴"
		}
	case "closed":
		title = "Trade Closed"
		if pnl >= 0 {
			severity = SeverityInfo
			emoji = "✅"
		} else {
			severity = SeverityWarning
			emoji = "❌"
		}
	case "sl":
		title = "Stop Loss Hit"
		severity = SeverityWarning
		emoji = "🛑"
	case "tp":
		title = "Take Profit Hit"
		severity = SeverityInfo
		emoji = "🎯"
	default:
		title = "Trade Update"
		severity = SeverityInfo
		emoji = "📊"
	}

	message := fmt.Sprintf("%s %s %s %.4f @ $%.5f", emoji, side, pair, size, price)
	if pnl != 0 {
		pnlSign := "+"
		if pnl < 0 {
			pnlSign = ""
		}
		message += fmt.Sprintf(" | PnL: %s$%.4f", pnlSign, pnl)
	}
	if reason != "" {
		message += fmt.Sprintf(" | %s", reason)
	}

	data := map[string]interface{}{
		"trade_type": tradeType,
		"pair":       pair,
		"side":       side,
		"size":       size,
		"price":      price,
		"pnl":        pnl,
		"reason":     reason,
	}

	nm.TriggerCustomNotification(title, message, severity, data, "system")
	return nil
}
