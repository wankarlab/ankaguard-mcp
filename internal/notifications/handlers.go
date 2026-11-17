package notifications

import (
	"fmt"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"
)

// registerDefaultHandlers registers the default alert handlers
func (nm *NotificationManager) registerDefaultHandlers() {
	nm.alertHandlers[AlertTypePriceThreshold] = &PriceThresholdHandler{}
	nm.alertHandlers[AlertTypeVolumeSpike] = &VolumeSpikeHandler{}
	nm.alertHandlers[AlertTypeRiskLimit] = &RiskLimitHandler{}
	nm.alertHandlers[AlertTypeCustom] = &CustomHandler{}
}

// PriceThresholdHandler handles price threshold alerts
type PriceThresholdHandler struct{}

func (h *PriceThresholdHandler) CheckCondition(alert *Alert, data map[string]interface{}) bool {
	currentPrice, ok := data["current_price"].(float64)
	if !ok {
		return false
	}

	// Check threshold conditions
	if threshold, exists := alert.Conditions["threshold"]; exists {
		thresholdPrice, ok := threshold.(float64)
		if !ok {
			// Try to parse as string
			if thresholdStr, ok := threshold.(string); ok {
				if parsed, err := strconv.ParseFloat(thresholdStr, 64); err == nil {
					thresholdPrice = parsed
				} else {
					return false
				}
			} else {
				return false
			}
		}

		direction, exists := alert.Conditions["direction"].(string)
		if !exists {
			direction = "above" // default
		}

		switch direction {
		case "above":
			return currentPrice >= thresholdPrice
		case "below":
			return currentPrice <= thresholdPrice
		case "crosses_above":
			// Would need previous price to implement properly
			return currentPrice >= thresholdPrice
		case "crosses_below":
			// Would need previous price to implement properly
			return currentPrice <= thresholdPrice
		}
	}

	return false
}

func (h *PriceThresholdHandler) FormatMessage(alert *Alert, data map[string]interface{}) string {
	currentPrice, _ := data["current_price"].(float64)
	threshold, _ := alert.Conditions["threshold"].(float64)
	direction, _ := alert.Conditions["direction"].(string)

	switch direction {
	case "above":
		return fmt.Sprintf("%s price (%.2f) is above threshold (%.2f)", alert.Symbol, currentPrice, threshold)
	case "below":
		return fmt.Sprintf("%s price (%.2f) is below threshold (%.2f)", alert.Symbol, currentPrice, threshold)
	default:
		return fmt.Sprintf("%s price alert triggered: %.2f", alert.Symbol, currentPrice)
	}
}

// VolumeSpikeHandler handles volume spike alerts
type VolumeSpikeHandler struct{}

func (h *VolumeSpikeHandler) CheckCondition(alert *Alert, data map[string]interface{}) bool {
	currentVolume, ok := data["current_volume"].(float64)
	if !ok {
		return false
	}

	averageVolume, ok := data["average_volume"].(float64)
	if !ok {
		return false
	}

	multiplier := 2.0 // default multiplier
	if mult, exists := alert.Conditions["multiplier"]; exists {
		if multFloat, ok := mult.(float64); ok {
			multiplier = multFloat
		}
	}

	return currentVolume >= (averageVolume * multiplier)
}

func (h *VolumeSpikeHandler) FormatMessage(alert *Alert, data map[string]interface{}) string {
	currentVolume, _ := data["current_volume"].(float64)
	averageVolume, _ := data["average_volume"].(float64)
	multiplier, _ := alert.Conditions["multiplier"].(float64)

	if multiplier == 0 {
		multiplier = 2.0
	}

	return fmt.Sprintf("%s volume spike detected: %.2f (%.1fx average of %.2f)",
		alert.Symbol, currentVolume, currentVolume/averageVolume, averageVolume)
}

// RiskLimitHandler handles risk limit alerts
type RiskLimitHandler struct{}

func (h *RiskLimitHandler) CheckCondition(alert *Alert, data map[string]interface{}) bool {
	currentRisk, ok := data["current_risk"].(float64)
	if !ok {
		return false
	}

	riskLimit, ok := alert.Conditions["risk_limit"].(float64)
	if !ok {
		return false
	}

	return currentRisk >= riskLimit
}

func (h *RiskLimitHandler) FormatMessage(alert *Alert, data map[string]interface{}) string {
	currentRisk, _ := data["current_risk"].(float64)
	riskLimit, _ := alert.Conditions["risk_limit"].(float64)

	return fmt.Sprintf("Risk limit exceeded: %.2f%% (limit: %.2f%%)", currentRisk*100, riskLimit*100)
}

// CustomHandler handles custom alerts with user-defined conditions
type CustomHandler struct{}

func (h *CustomHandler) CheckCondition(alert *Alert, data map[string]interface{}) bool {
	// For custom alerts, we'll use a simple expression evaluator
	// This is a simplified implementation - in production you might want a more robust solution

	condition, exists := alert.Conditions["expression"].(string)
	if !exists {
		return false
	}

	// Simple condition evaluation (extend as needed)
	switch condition {
	case "always":
		return true
	case "never":
		return false
	default:
		// For now, return false for unknown expressions
		// In a real implementation, you'd parse and evaluate the expression
		return false
	}
}

func (h *CustomHandler) FormatMessage(alert *Alert, data map[string]interface{}) string {
	if customMessage, exists := alert.Conditions["message"]; exists {
		if msg, ok := customMessage.(string); ok {
			return msg
		}
	}

	return fmt.Sprintf("Custom alert triggered: %s", alert.Title)
}

// CreatePriceAlert creates a price threshold alert
func (nm *NotificationManager) CreatePriceAlert(exchange, symbol string, threshold float64, direction string, userID string) (*Alert, error) {
	alert := &Alert{
		Type:     AlertTypePriceThreshold,
		Severity: SeverityInfo,
		Title:    fmt.Sprintf("Price Alert - %s", symbol),
		Message:  fmt.Sprintf("%s price %s %.2f", symbol, direction, threshold),
		Exchange: exchange,
		Symbol:   symbol,
		UserID:   userID,
		Conditions: map[string]interface{}{
			"threshold": threshold,
			"direction": direction,
		},
	}

	err := nm.CreateAlert(alert)
	return alert, err
}

// CreateVolumeAlert creates a volume spike alert
func (nm *NotificationManager) CreateVolumeAlert(exchange, symbol string, multiplier float64, userID string) (*Alert, error) {
	alert := &Alert{
		Type:     AlertTypeVolumeSpike,
		Severity: SeverityWarning,
		Title:    fmt.Sprintf("Volume Alert - %s", symbol),
		Message:  fmt.Sprintf("%s volume spike (%.1fx normal)", symbol, multiplier),
		Exchange: exchange,
		Symbol:   symbol,
		UserID:   userID,
		Conditions: map[string]interface{}{
			"multiplier": multiplier,
		},
	}

	err := nm.CreateAlert(alert)
	return alert, err
}

// CreateRiskAlert creates a risk limit alert
func (nm *NotificationManager) CreateRiskAlert(riskLimit float64, userID string) (*Alert, error) {
	alert := &Alert{
		Type:     AlertTypeRiskLimit,
		Severity: SeverityCritical,
		Title:    "Risk Limit Alert",
		Message:  fmt.Sprintf("Risk limit exceeded: %.1f%%", riskLimit*100),
		UserID:   userID,
		Conditions: map[string]interface{}{
			"risk_limit": riskLimit,
		},
	}

	err := nm.CreateAlert(alert)
	return alert, err
}

// CreateCustomAlert creates a custom alert with user-defined conditions
func (nm *NotificationManager) CreateCustomAlert(title, message, expression string, severity AlertSeverity, userID string) (*Alert, error) {
	alert := &Alert{
		Type:     AlertTypeCustom,
		Severity: severity,
		Title:    title,
		Message:  message,
		UserID:   userID,
		Conditions: map[string]interface{}{
			"expression": expression,
			"message":    message,
		},
	}

	err := nm.CreateAlert(alert)
	return alert, err
}

// TriggerCustomNotification triggers a custom notification immediately
func (nm *NotificationManager) TriggerCustomNotification(title, message string, severity AlertSeverity, data map[string]interface{}, userID string) {
	notification := &Notification{
		ID:        fmt.Sprintf("custom_notif_%d", time.Now().UnixNano()),
		AlertID:   "custom",
		Type:      AlertTypeCustom,
		Severity:  severity,
		Title:     title,
		Message:   message,
		Data:      data,
		UserID:    userID,
		Timestamp: time.Now(),
		Read:      false,
	}

	// Route to the owning user only when we actually have one; "system"/"" are
	// used by callers (e.g. TriggerTradeNotification) to mean a genuinely
	// system-wide notification with no specific owning user (INFRA-03).
	if userID != "" && userID != "system" {
		if err := nm.wsManager.SendNotificationToUser(userID, "alert", notification); err != nil {
			nm.logger.WithError(err).Error("Failed to send custom notification to user")
		}
	} else {
		nm.wsManager.BroadcastNotification("alert", notification)
	}

	nm.logger.WithFields(logrus.Fields{
		"notification_id": notification.ID,
		"title":           title,
		"severity":        severity,
		"user_id":         userID,
	}).Info("Triggered custom notification")
}

// CheckPortfolioRisk checks portfolio risk and triggers alerts if needed
func (nm *NotificationManager) CheckPortfolioRisk() {
	portfolio, err := nm.gctClient.GetPortfolio(nm.ctx)
	if err != nil {
		nm.logger.WithError(err).Error("Failed to get portfolio for risk check")
		return
	}

	// Calculate portfolio risk (simplified)
	totalValue := 0.0
	totalRisk := 0.0

	// Calculate risk based on portfolio allocations
	for _, allocation := range portfolio.Allocations {
		value := allocation * portfolio.TotalValue
		totalValue += value

		// Simple risk calculation based on volatility (placeholder)
		risk := value * 0.1 // 10% risk assumption
		totalRisk += risk
	}

	if totalValue > 0 {
		riskPercentage := totalRisk / totalValue

		// Check against risk alerts
		nm.mu.RLock()
		riskAlerts := make([]*Alert, 0)
		for _, alert := range nm.alerts {
			if alert.Type == AlertTypeRiskLimit && alert.Active && !alert.Triggered {
				riskAlerts = append(riskAlerts, alert)
			}
		}
		nm.mu.RUnlock()

		// Check each risk alert
		for _, alert := range riskAlerts {
			data := map[string]interface{}{
				"current_risk": riskPercentage,
				"total_value":  totalValue,
				"total_risk":   totalRisk,
				"portfolio":    portfolio,
			}

			if handler, exists := nm.alertHandlers[AlertTypeRiskLimit]; exists {
				if handler.CheckCondition(alert, data) {
					nm.TriggerAlert(alert.ID, data)
				}
			}
		}
	}
}
