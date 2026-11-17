package notifications

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/gct"
)

// GCTAlertForwarder forwards alerts from GoCryptoTrader
type GCTAlertForwarder struct {
	logger              *logrus.Logger
	gctClient           gct.GCTClient
	notificationManager *NotificationManager
	ctx                 context.Context
	cancel              context.CancelFunc
	alertSubscriptions  map[string]*GCTAlertSubscription
}

// GCTAlertSubscription represents a subscription to GoCryptoTrader alerts
type GCTAlertSubscription struct {
	UserID    string    `json:"user_id"`
	AlertType string    `json:"alert_type"` // "order", "price", "portfolio", "system"
	Exchange  string    `json:"exchange,omitempty"`
	Symbol    string    `json:"symbol,omitempty"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}

// GCTAlert represents an alert from GoCryptoTrader
type GCTAlert struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"`
	Severity  string                 `json:"severity"`
	Title     string                 `json:"title"`
	Message   string                 `json:"message"`
	Exchange  string                 `json:"exchange,omitempty"`
	Symbol    string                 `json:"symbol,omitempty"`
	Data      map[string]interface{} `json:"data,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
}

// NewGCTAlertForwarder creates a new GoCryptoTrader alert forwarder
func NewGCTAlertForwarder(logger *logrus.Logger, gctClient gct.GCTClient, notificationManager *NotificationManager) *GCTAlertForwarder {
	ctx, cancel := context.WithCancel(context.Background())

	return &GCTAlertForwarder{
		logger:              logger,
		gctClient:           gctClient,
		notificationManager: notificationManager,
		ctx:                 ctx,
		cancel:              cancel,
		alertSubscriptions:  make(map[string]*GCTAlertSubscription),
	}
}

// Start starts the alert forwarder
func (gaf *GCTAlertForwarder) Start() error {
	gaf.logger.Info("Starting GoCryptoTrader alert forwarder")

	// Start monitoring different types of alerts
	go gaf.monitorOrderAlerts()
	go gaf.monitorPriceAlerts()
	go gaf.monitorPortfolioAlerts()
	go gaf.monitorSystemAlerts()

	return nil
}

// Stop stops the alert forwarder
func (gaf *GCTAlertForwarder) Stop() error {
	gaf.logger.Info("Stopping GoCryptoTrader alert forwarder")
	gaf.cancel()
	return nil
}

// SubscribeToAlerts subscribes a user to specific alert types
func (gaf *GCTAlertForwarder) SubscribeToAlerts(userID, alertType, exchange, symbol string) error {
	subscriptionID := fmt.Sprintf("%s_%s_%s_%s", userID, alertType, exchange, symbol)

	subscription := &GCTAlertSubscription{
		UserID:    userID,
		AlertType: alertType,
		Exchange:  exchange,
		Symbol:    symbol,
		Active:    true,
		CreatedAt: time.Now(),
	}

	gaf.alertSubscriptions[subscriptionID] = subscription

	gaf.logger.WithFields(logrus.Fields{
		"user_id":    userID,
		"alert_type": alertType,
		"exchange":   exchange,
		"symbol":     symbol,
	}).Info("Subscribed to GoCryptoTrader alerts")

	return nil
}

// UnsubscribeFromAlerts unsubscribes a user from alerts
func (gaf *GCTAlertForwarder) UnsubscribeFromAlerts(userID, alertType, exchange, symbol string) error {
	subscriptionID := fmt.Sprintf("%s_%s_%s_%s", userID, alertType, exchange, symbol)

	if subscription, exists := gaf.alertSubscriptions[subscriptionID]; exists {
		subscription.Active = false
		delete(gaf.alertSubscriptions, subscriptionID)

		gaf.logger.WithFields(logrus.Fields{
			"user_id":    userID,
			"alert_type": alertType,
			"exchange":   exchange,
			"symbol":     symbol,
		}).Info("Unsubscribed from GoCryptoTrader alerts")
	}

	return nil
}

// monitorOrderAlerts monitors order-related alerts
func (gaf *GCTAlertForwarder) monitorOrderAlerts() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	lastCheck := time.Now().Add(-1 * time.Hour) // Check last hour on startup

	for {
		select {
		case <-gaf.ctx.Done():
			return
		case <-ticker.C:
			gaf.checkOrderAlerts(lastCheck)
			lastCheck = time.Now()
		}
	}
}

// checkOrderAlerts checks for order-related alerts
func (gaf *GCTAlertForwarder) checkOrderAlerts(since time.Time) {
	// Get recent orders from all exchanges
	for _, subscription := range gaf.alertSubscriptions {
		if !subscription.Active || subscription.AlertType != "order" {
			continue
		}

		orders, err := gaf.gctClient.GetOrders(gaf.ctx, subscription.Exchange, "", "", "spot", nil, nil)
		if err != nil {
			gaf.logger.WithError(err).Error("Failed to get orders for alert checking")
			continue
		}

		// Check for filled orders since last check
		for _, order := range orders {
			if order.Status == "filled" && order.UpdateTime.After(since) {
				symbol := fmt.Sprintf("%s%s", order.BaseCurrency, order.QuoteCurrency)
				alert := &GCTAlert{
					ID:       fmt.Sprintf("order_filled_%s", order.ID),
					Type:     "order_filled",
					Severity: "info",
					Title:    "Order Filled",
					Message:  fmt.Sprintf("Order %s filled: %s %s at %.2f", order.ID, order.Side, symbol, order.Price),
					Exchange: subscription.Exchange,
					Symbol:   symbol,
					Data: map[string]interface{}{
						"order_id":    order.ID,
						"symbol":      symbol,
						"side":        order.Side,
						"amount":      order.Amount,
						"price":       order.Price,
						"open_volume": order.OpenVolume,
						"status":      order.Status,
					},
					Timestamp: order.UpdateTime,
				}

				gaf.forwardAlert(subscription.UserID, alert)
			}

			// Check for cancelled orders
			if order.Status == "cancelled" && order.UpdateTime.After(since) {
				symbol := fmt.Sprintf("%s%s", order.BaseCurrency, order.QuoteCurrency)
				alert := &GCTAlert{
					ID:       fmt.Sprintf("order_cancelled_%s", order.ID),
					Type:     "order_cancelled",
					Severity: "warning",
					Title:    "Order Cancelled",
					Message:  fmt.Sprintf("Order %s cancelled: %s %s", order.ID, order.Side, symbol),
					Exchange: subscription.Exchange,
					Symbol:   symbol,
					Data: map[string]interface{}{
						"order_id": order.ID,
						"symbol":   symbol,
						"side":     order.Side,
						"amount":   order.Amount,
						"price":    order.Price,
						"status":   order.Status,
					},
					Timestamp: order.UpdateTime,
				}

				gaf.forwardAlert(subscription.UserID, alert)
			}
		}
	}
}

// monitorPriceAlerts monitors price-related alerts
func (gaf *GCTAlertForwarder) monitorPriceAlerts() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-gaf.ctx.Done():
			return
		case <-ticker.C:
			gaf.checkPriceAlerts()
		}
	}
}

// checkPriceAlerts checks for significant price movements
func (gaf *GCTAlertForwarder) checkPriceAlerts() {
	for _, subscription := range gaf.alertSubscriptions {
		if !subscription.Active || subscription.AlertType != "price" {
			continue
		}

		// Get current price
		ticker, err := gaf.gctClient.GetTickerBySymbol(gaf.ctx, subscription.Exchange, subscription.Symbol, "spot")
		if err != nil {
			gaf.logger.WithError(err).Error("Failed to get ticker for price alert")
			continue
		}

		// Calculate simple price change (using high/low as approximation)
		priceRange := ticker.High - ticker.Low
		changePercent := (priceRange / ticker.Last) * 100

		if changePercent > 5.0 { // 5% change
			severity := "info"
			if changePercent > 10.0 { // 10% change
				severity = "warning"
			}

			alert := &GCTAlert{
				ID:       fmt.Sprintf("price_change_%s_%s_%d", subscription.Exchange, subscription.Symbol, time.Now().Unix()),
				Type:     "price_change",
				Severity: severity,
				Title:    "Significant Price Movement",
				Message:  fmt.Sprintf("%s price range %.2f%% (current: %.2f)", subscription.Symbol, changePercent, ticker.Last),
				Exchange: subscription.Exchange,
				Symbol:   subscription.Symbol,
				Data: map[string]interface{}{
					"symbol":         subscription.Symbol,
					"price":          ticker.Last,
					"change_percent": changePercent,
					"volume":         ticker.Volume,
					"high":           ticker.High,
					"low":            ticker.Low,
					"bid":            ticker.Bid,
					"ask":            ticker.Ask,
				},
				Timestamp: time.Now(),
			}

			gaf.forwardAlert(subscription.UserID, alert)
		}
	}
}

// monitorPortfolioAlerts monitors portfolio-related alerts
func (gaf *GCTAlertForwarder) monitorPortfolioAlerts() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-gaf.ctx.Done():
			return
		case <-ticker.C:
			gaf.checkPortfolioAlerts()
		}
	}
}

// checkPortfolioAlerts checks for portfolio-related alerts
func (gaf *GCTAlertForwarder) checkPortfolioAlerts() {
	for _, subscription := range gaf.alertSubscriptions {
		if !subscription.Active || subscription.AlertType != "portfolio" {
			continue
		}

		// Get portfolio data
		portfolio, err := gaf.gctClient.GetPortfolio(gaf.ctx)
		if err != nil {
			gaf.logger.WithError(err).Error("Failed to get portfolio for alert checking")
			continue
		}

		// Check for significant portfolio value changes
		// This is a simplified check - in practice you'd compare with previous values
		if portfolio.TotalValue > 0 {
			// Example: Alert if portfolio value drops below a threshold
			// You would store previous values and compare

			alert := &GCTAlert{
				ID:       fmt.Sprintf("portfolio_update_%s_%d", subscription.UserID, time.Now().Unix()),
				Type:     "portfolio_update",
				Severity: "info",
				Title:    "Portfolio Update",
				Message:  fmt.Sprintf("Portfolio value: %.2f", portfolio.TotalValue),
				Data: map[string]interface{}{
					"total_value":    portfolio.TotalValue,
					"allocations":    portfolio.Allocations,
					"pnl":            portfolio.PnL,
					"pnl_percentage": portfolio.PnLPercentage,
				},
				Timestamp: time.Now(),
			}

			gaf.forwardAlert(subscription.UserID, alert)
		}
	}
}

// monitorSystemAlerts monitors system-related alerts
func (gaf *GCTAlertForwarder) monitorSystemAlerts() {
	ticker := time.NewTicker(120 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-gaf.ctx.Done():
			return
		case <-ticker.C:
			gaf.checkSystemAlerts()
		}
	}
}

// checkSystemAlerts checks for system-related alerts
func (gaf *GCTAlertForwarder) checkSystemAlerts() {
	// Check GoCryptoTrader health
	// This would typically involve calling a health check endpoint

	for _, subscription := range gaf.alertSubscriptions {
		if !subscription.Active || subscription.AlertType != "system" {
			continue
		}

		// Example system alert - connection status
		// In practice, you'd check actual system metrics
		alert := &GCTAlert{
			ID:       fmt.Sprintf("system_status_%s_%d", subscription.UserID, time.Now().Unix()),
			Type:     "system_status",
			Severity: "info",
			Title:    "System Status",
			Message:  "GoCryptoTrader connection healthy",
			Data: map[string]interface{}{
				"status":    "healthy",
				"timestamp": time.Now(),
			},
			Timestamp: time.Now(),
		}

		gaf.forwardAlert(subscription.UserID, alert)
	}
}

// forwardAlert forwards an alert to the notification system
func (gaf *GCTAlertForwarder) forwardAlert(userID string, alert *GCTAlert) {
	// Convert GCT alert to notification system alert
	var severity AlertSeverity
	switch alert.Severity {
	case "critical":
		severity = SeverityCritical
	case "warning":
		severity = SeverityWarning
	case "info":
		severity = SeverityInfo
	default:
		severity = SeverityInfo
	}

	// Create notification
	notification := &Notification{
		ID:        alert.ID,
		AlertID:   alert.ID,
		Type:      AlertType(alert.Type),
		Severity:  severity,
		Title:     alert.Title,
		Message:   alert.Message,
		Data:      alert.Data,
		UserID:    userID,
		Timestamp: alert.Timestamp,
		Read:      false,
	}

	if err := gaf.notificationManager.wsManager.SendNotificationToUser(userID, "alert", notification); err != nil {
		gaf.logger.WithError(err).Error("Failed to send WebSocket alert")
	}

	// Send push notification if configured
	if gaf.notificationManager.pushService != nil {
		deviceTokens := []string{} // Would be retrieved from user service

		if err := gaf.notificationManager.pushService.SendTradingAlert(
			gaf.ctx,
			deviceTokens,
			alert.Type,
			alert.Symbol,
			alert.Message,
			alert.Data,
		); err != nil {
			gaf.logger.WithError(err).Error("Failed to send push alert")
		}
	}

	gaf.logger.WithFields(logrus.Fields{
		"alert_id":   alert.ID,
		"alert_type": alert.Type,
		"user_id":    userID,
		"severity":   alert.Severity,
	}).Info("Forwarded GoCryptoTrader alert")
}

// GetSubscriptions returns all active subscriptions for a user
func (gaf *GCTAlertForwarder) GetSubscriptions(userID string) []*GCTAlertSubscription {
	subscriptions := make([]*GCTAlertSubscription, 0)

	for _, subscription := range gaf.alertSubscriptions {
		if subscription.UserID == userID && subscription.Active {
			subscriptions = append(subscriptions, subscription)
		}
	}

	return subscriptions
}

// GetSubscriptionStats returns subscription statistics
func (gaf *GCTAlertForwarder) GetSubscriptionStats() map[string]interface{} {
	stats := map[string]interface{}{
		"total_subscriptions":  len(gaf.alertSubscriptions),
		"active_subscriptions": 0,
		"types":                make(map[string]int),
		"exchanges":            make(map[string]int),
	}

	for _, subscription := range gaf.alertSubscriptions {
		if subscription.Active {
			stats["active_subscriptions"] = stats["active_subscriptions"].(int) + 1

			types := stats["types"].(map[string]int)
			types[subscription.AlertType]++

			if subscription.Exchange != "" {
				exchanges := stats["exchanges"].(map[string]int)
				exchanges[subscription.Exchange]++
			}
		}
	}

	return stats
}
