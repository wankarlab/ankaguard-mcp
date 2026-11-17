package notifications

import (
	"context"
	"fmt"
	"strings"
	"time"

)

// AITradeNotification represents an AI-enhanced trade notification
type AITradeNotification struct {
	*Notification
	LLMExplanation  string   `json:"llm_explanation"`
	MarketAnalysis  string   `json:"market_analysis"`
	KeyFactors      []string `json:"key_factors"`
	RiskAssessment  string   `json:"risk_assessment"`
	ExpectedOutcome string   `json:"expected_outcome"`
	Confidence      float64  `json:"confidence"`
}

// NotifyTradeWithAI sends an AI-enhanced trade notification
// llmCallFunc is a callback to avoid import cycles with llm package
func (nm *NotificationManager) NotifyTradeWithAI(
	ctx context.Context,
	trade *TradeInfo,
	llmCallFunc func(ctx context.Context, prompt string) (string, error),
) error {
	// Generate LLM explanation if callback provided
	var llmExplanation string
	var marketAnalysis string
	var keyFactors []string
	var riskAssessment string

	if llmCallFunc != nil {
		prompt := nm.buildTradeNotificationPrompt(trade)
		response, err := llmCallFunc(ctx, prompt)
		if err != nil {
			nm.logger.WithError(err).Warn("Failed to generate LLM explanation, using basic notification")
		} else {
			// Parse LLM response
			llmExplanation, marketAnalysis, keyFactors, riskAssessment = nm.parseLLMTradeResponse(response)
		}
	}

	// Create enhanced notification
	notification := &AITradeNotification{
		Notification: &Notification{
			ID:       fmt.Sprintf("trade_%s_%d", trade.Symbol, time.Now().UnixNano()),
			AlertID:  fmt.Sprintf("trade_auto_%s", trade.OrderID),
			Type:     AlertTypeOrderFilled,
			Severity: nm.getTradeNotificationSeverity(trade),
			Title:    fmt.Sprintf("%s Trade Executed: %s", trade.Action, trade.Symbol),
			Message:  nm.formatAITradeMessage(trade, llmExplanation),
			Data: map[string]interface{}{
				"symbol":           trade.Symbol,
				"action":           trade.Action,
				"price":            trade.Price,
				"size":             trade.Size,
				"confidence":       trade.Confidence,
				"llm_explanation":  llmExplanation,
				"market_analysis":  marketAnalysis,
				"key_factors":      keyFactors,
				"risk_assessment":  riskAssessment,
				"expected_outcome": trade.ExpectedOutcome,
			},
			UserID:    trade.UserID,
			Timestamp: time.Now(),
			Read:      false,
		},
		LLMExplanation:  llmExplanation,
		MarketAnalysis:  marketAnalysis,
		KeyFactors:      keyFactors,
		RiskAssessment:  riskAssessment,
		ExpectedOutcome: trade.ExpectedOutcome,
		Confidence:      trade.Confidence,
	}

	// Send via WebSocket
	if nm.wsManager != nil {
		if err := nm.wsManager.BroadcastNotification("ai_trade_notification", notification); err != nil {
			nm.logger.WithError(err).Error("Failed to send WebSocket notification")
		}
	}

	// Send via push service if available
	if nm.pushService != nil {
		pushNotif := &PushNotification{
			Title:    notification.Title,
			Body:     notification.Message,
			Data:     notification.Data,
			Priority: "normal",
		}
		// Send to all devices (would need device tokens in real implementation)
		if err := nm.pushService.SendPushNotification(ctx, []string{}, pushNotif); err != nil {
			nm.logger.WithError(err).Error("Failed to send push notification")
		}
	}

	nm.logger.WithFields(map[string]interface{}{
		"symbol":     trade.Symbol,
		"action":     trade.Action,
		"confidence": trade.Confidence,
		"has_llm":    llmExplanation != "",
	}).Info("AI-enhanced trade notification sent")

	return nil
}

// NotifyPositionClosedWithAI sends an AI-enhanced position closed notification
func (nm *NotificationManager) NotifyPositionClosedWithAI(
	ctx context.Context,
	position *PositionInfo,
	llmCallFunc func(ctx context.Context, prompt string) (string, error),
) error {
	// Generate LLM analysis of what went right/wrong
	var llmAnalysis string
	var learningInsights string

	if llmCallFunc != nil && position.PnL != 0 {
		prompt := nm.buildPositionClosedPrompt(position)
		response, err := llmCallFunc(ctx, prompt)
		if err != nil {
			nm.logger.WithError(err).Warn("Failed to generate LLM analysis")
		} else {
			llmAnalysis, learningInsights = nm.parseLLMPositionResponse(response)
		}
	}

	// Determine severity based on P&L
	severity := SeverityInfo
	if position.PnL < -position.Size*0.03 { // Loss > 3%
		severity = SeverityWarning
	} else if position.PnL > position.Size*0.05 { // Profit > 5%
		severity = SeverityInfo
	}

	notification := &Notification{
		ID:       fmt.Sprintf("position_closed_%s_%d", position.Symbol, time.Now().UnixNano()),
		AlertID:  fmt.Sprintf("position_%s", position.PositionID),
		Type:     AlertTypePositionChange,
		Severity: severity,
		Title:    fmt.Sprintf("Position Closed: %s", position.Symbol),
		Message:  nm.formatPositionClosedMessage(position, llmAnalysis),
		Data: map[string]interface{}{
			"symbol":            position.Symbol,
			"entry_price":       position.EntryPrice,
			"exit_price":        position.ExitPrice,
			"pnl":               position.PnL,
			"pnl_percent":       position.PnLPercent,
			"duration":          position.Duration,
			"close_reason":      position.CloseReason,
			"llm_analysis":      llmAnalysis,
			"learning_insights": learningInsights,
		},
		UserID:    position.UserID,
		Timestamp: time.Now(),
		Read:      false,
	}

	// Send notification
	if nm.wsManager != nil {
		nm.wsManager.BroadcastNotification("position_closed_notification", notification)
	}

	return nil
}

// buildTradeNotificationPrompt builds a prompt for LLM trade explanation
func (nm *NotificationManager) buildTradeNotificationPrompt(trade *TradeInfo) string {
	return fmt.Sprintf(`Generate a concise explanation for this trade execution:

Symbol: %s
Action: %s
Price: $%.4f
Size: %.4f
Confidence: %.2f%%

Market Context:
- Trend: %s
- RSI: %.2f
- Volume: %s

Provide a brief explanation (2-3 sentences) covering:
1. Why this trade was executed
2. Key market factors
3. Risk assessment
4. Expected outcome

Format as plain text, concise and actionable.`,
		trade.Symbol,
		trade.Action,
		trade.Price,
		trade.Size,
		trade.Confidence*100,
		trade.Trend,
		trade.RSI,
		trade.VolumeStatus,
	)
}

// buildPositionClosedPrompt builds a prompt for position analysis
func (nm *NotificationManager) buildPositionClosedPrompt(position *PositionInfo) string {
	outcome := "profit"
	if position.PnL < 0 {
		outcome = "loss"
	}

	return fmt.Sprintf(`Analyze this closed position:

Symbol: %s
Entry: $%.4f
Exit: $%.4f
P&L: $%.2f (%.2f%%)
Duration: %s
Close Reason: %s

Provide brief analysis (2-3 sentences):
1. What went right/wrong
2. Key learning points
3. Suggestions for improvement

This was a %s. Be constructive and actionable.`,
		position.Symbol,
		position.EntryPrice,
		position.ExitPrice,
		position.PnL,
		position.PnLPercent,
		position.Duration,
		position.CloseReason,
		outcome,
	)
}

// parseLLMTradeResponse parses LLM response for trade notification
func (nm *NotificationManager) parseLLMTradeResponse(response string) (explanation, analysis string, factors []string, risk string) {
	// Simplified parser - extract key information
	lines := strings.Split(response, "\n")

	explanation = response // Full response as explanation

	// Extract key factors (look for bullet points or numbered lists)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "-") || strings.HasPrefix(line, "•") || strings.HasPrefix(line, "*") {
			factor := strings.TrimLeft(line, "-•* ")
			if factor != "" {
				factors = append(factors, factor)
			}
		}
	}

	// Extract risk assessment (look for risk-related keywords)
	responseLower := strings.ToLower(response)
	if strings.Contains(responseLower, "high risk") {
		risk = "High"
	} else if strings.Contains(responseLower, "low risk") {
		risk = "Low"
	} else {
		risk = "Medium"
	}

	// Extract market analysis (first paragraph usually)
	if len(lines) > 0 {
		analysis = lines[0]
	}

	return
}

// parseLLMPositionResponse parses LLM response for position closed
func (nm *NotificationManager) parseLLMPositionResponse(response string) (analysis, insights string) {
	lines := strings.Split(response, "\n")

	// First half is analysis, second half is insights
	midpoint := len(lines) / 2
	if midpoint > 0 {
		analysis = strings.Join(lines[:midpoint], " ")
		insights = strings.Join(lines[midpoint:], " ")
	} else {
		analysis = response
		insights = "Continue monitoring similar setups"
	}

	return
}

// formatAITradeMessage formats the trade notification message
func (nm *NotificationManager) formatAITradeMessage(trade *TradeInfo, llmExplanation string) string {
	pnlSign := ""
	if trade.ExpectedPnL > 0 {
		pnlSign = "+"
	}

	msg := fmt.Sprintf(`🤖 AI Trade Executed

Symbol: %s
Action: %s %s
Price: $%.4f
Size: %.4f
Confidence: %.1f%%

Expected P&L: %s$%.2f

`,
		trade.Symbol,
		getActionEmoji(trade.Action),
		trade.Action,
		trade.Price,
		trade.Size,
		trade.Confidence*100,
		pnlSign,
		trade.ExpectedPnL,
	)

	if llmExplanation != "" {
		msg += fmt.Sprintf("💡 AI Analysis:\n%s\n", llmExplanation)
	}

	return msg
}

// formatPositionClosedMessage formats the position closed message
func (nm *NotificationManager) formatPositionClosedMessage(position *PositionInfo, llmAnalysis string) string {
	pnlSign := "+"
	emoji := "✅"
	if position.PnL < 0 {
		pnlSign = ""
		emoji = "❌"
	}

	msg := fmt.Sprintf(`%s Position Closed

Symbol: %s
Entry: $%.4f
Exit: $%.4f
P&L: %s$%.2f (%.2f%%)
Duration: %s
Reason: %s

`,
		emoji,
		position.Symbol,
		position.EntryPrice,
		position.ExitPrice,
		pnlSign,
		position.PnL,
		position.PnLPercent,
		position.Duration,
		position.CloseReason,
	)

	if llmAnalysis != "" {
		msg += fmt.Sprintf("🧠 AI Analysis:\n%s\n", llmAnalysis)
	}

	return msg
}

// getTradeNotificationSeverity determines notification severity
func (nm *NotificationManager) getTradeNotificationSeverity(trade *TradeInfo) AlertSeverity {
	if trade.Confidence < 0.5 {
		return SeverityWarning
	}
	return SeverityInfo
}

// getActionEmoji returns emoji for trade action
func getActionEmoji(action string) string {
	switch strings.ToUpper(action) {
	case "BUY":
		return "📈"
	case "SELL":
		return "📉"
	default:
		return "📊"
	}
}

// TradeInfo represents trade information for notifications
type TradeInfo struct {
	Symbol          string
	Action          string
	Price           float64
	Size            float64
	Confidence      float64
	ExpectedPnL     float64
	ExpectedOutcome string
	OrderID         string
	UserID          string
	Trend           string
	RSI             float64
	VolumeStatus    string
}

// PositionInfo represents position information for notifications
type PositionInfo struct {
	PositionID  string
	Symbol      string
	EntryPrice  float64
	ExitPrice   float64
	PnL         float64
	PnLPercent  float64
	Size        float64
	Duration    time.Duration
	CloseReason string
	UserID      string
}

// DailySummaryInfo represents daily trading summary information
type DailySummaryInfo struct {
	Date            time.Time
	TotalTrades     int
	WinningTrades   int
	LosingTrades    int
	TotalPnL        float64
	WinRate         float64
	BestTrade       *TradeInfo
	WorstTrade      *TradeInfo
	TopSymbols      []string
	MarketCondition string
	UserID          string
}

// GenerateDailySummaryWithAI generates and sends a daily trading summary with AI insights
func (nm *NotificationManager) GenerateDailySummaryWithAI(
	ctx context.Context,
	summary *DailySummaryInfo,
	llmCallFunc func(ctx context.Context, prompt string) (string, error),
) error {
	// Generate AI insights and recommendations
	var aiInsights string
	var recommendations []string
	var marketOutlook string

	if llmCallFunc != nil {
		prompt := nm.buildDailySummaryPrompt(summary)
		response, err := llmCallFunc(ctx, prompt)
		if err != nil {
			nm.logger.WithError(err).Warn("Failed to generate AI daily summary")
		} else {
			aiInsights, recommendations, marketOutlook = nm.parseAIDailySummaryResponse(response)
		}
	}

	// Determine summary severity based on performance
	severity := SeverityInfo
	if summary.TotalPnL < -100 { // Significant daily loss
		severity = SeverityWarning
	} else if summary.WinRate < 0.3 { // Low win rate
		severity = SeverityWarning
	}

	notification := &Notification{
		ID:       fmt.Sprintf("daily_summary_%s", summary.Date.Format("2006-01-02")),
		AlertID:  fmt.Sprintf("daily_summary_%s", summary.Date.Format("20060102")),
		Type:     AlertTypeCustom,
		Severity: severity,
		Title:    fmt.Sprintf("Daily Trading Summary - %s", summary.Date.Format("Jan 2, 2006")),
		Message:  nm.formatDailySummaryMessage(summary, aiInsights),
		Data: map[string]interface{}{
			"date":             summary.Date.Format("2006-01-02"),
			"total_trades":     summary.TotalTrades,
			"winning_trades":   summary.WinningTrades,
			"losing_trades":    summary.LosingTrades,
			"total_pnl":        summary.TotalPnL,
			"win_rate":         summary.WinRate,
			"top_symbols":      summary.TopSymbols,
			"market_condition": summary.MarketCondition,
			"ai_insights":      aiInsights,
			"recommendations":  recommendations,
			"market_outlook":   marketOutlook,
		},
		UserID:    summary.UserID,
		Timestamp: time.Now(),
		Read:      false,
	}

	// Send notification
	if nm.wsManager != nil {
		if err := nm.wsManager.BroadcastNotification("daily_summary_notification", notification); err != nil {
			nm.logger.WithError(err).Error("Failed to send daily summary WebSocket notification")
		}
	}

	// Send via push service if available
	if nm.pushService != nil {
		pushNotif := &PushNotification{
			Title:    notification.Title,
			Body:     fmt.Sprintf("P&L: $%.2f | Win Rate: %.1f%% | %d trades", summary.TotalPnL, summary.WinRate*100, summary.TotalTrades),
			Data:     notification.Data,
			Priority: "normal",
		}
		if err := nm.pushService.SendPushNotification(ctx, []string{}, pushNotif); err != nil {
			nm.logger.WithError(err).Error("Failed to send daily summary push notification")
		}
	}

	nm.logger.WithFields(map[string]interface{}{
		"date":         summary.Date.Format("2006-01-02"),
		"total_trades": summary.TotalTrades,
		"total_pnl":    summary.TotalPnL,
		"win_rate":     summary.WinRate,
		"has_ai":       aiInsights != "",
	}).Info("Daily summary notification sent")

	return nil
}

// buildDailySummaryPrompt builds a prompt for AI daily summary analysis
func (nm *NotificationManager) buildDailySummaryPrompt(summary *DailySummaryInfo) string {
	return fmt.Sprintf(`Analyze today's trading performance and provide insights:

DATE: %s
PERFORMANCE:
- Total Trades: %d
- Winning Trades: %d
- Losing Trades: %d
- Total P&L: $%.2f
- Win Rate: %.1f%%
- Top Symbols: %s
- Market Condition: %s

Provide analysis in 3 sections:
1. PERFORMANCE INSIGHTS (2-3 sentences about what went well/poorly)
2. RECOMMENDATIONS (3-4 actionable items for tomorrow)
3. MARKET OUTLOOK (brief outlook for next trading session)

Be concise, actionable, and constructive. Focus on learning and improvement.`,
		summary.Date.Format("January 2, 2006"),
		summary.TotalTrades,
		summary.WinningTrades,
		summary.LosingTrades,
		summary.TotalPnL,
		summary.WinRate*100,
		strings.Join(summary.TopSymbols, ", "),
		summary.MarketCondition,
	)
}

// parseAIDailySummaryResponse parses AI response for daily summary
func (nm *NotificationManager) parseAIDailySummaryResponse(response string) (insights string, recommendations []string, outlook string) {
	sections := strings.Split(response, "\n\n")

	for _, section := range sections {
		sectionUpper := strings.ToUpper(section)
		if strings.Contains(sectionUpper, "INSIGHTS") || strings.Contains(sectionUpper, "PERFORMANCE") {
			insights = strings.TrimSpace(section)
		} else if strings.Contains(sectionUpper, "RECOMMENDATIONS") {
			// Extract recommendations as list
			lines := strings.Split(section, "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "-") || strings.HasPrefix(line, "•") || strings.HasPrefix(line, "*") || strings.Contains(line, ".") {
					rec := strings.TrimLeft(line, "-•* ")
					rec = strings.TrimSpace(rec)
					if rec != "" && !strings.Contains(strings.ToUpper(rec), "RECOMMENDATIONS") {
						recommendations = append(recommendations, rec)
					}
				}
			}
		} else if strings.Contains(sectionUpper, "OUTLOOK") || strings.Contains(sectionUpper, "MARKET") {
			outlook = strings.TrimSpace(section)
		}
	}

	// Fallback: use full response as insights if parsing fails
	if insights == "" {
		insights = response
	}

	// Default recommendations if none found
	if len(recommendations) == 0 {
		recommendations = []string{
			"Review today's trades for patterns",
			"Adjust position sizing based on performance",
			"Monitor market conditions for tomorrow",
		}
	}

	return
}

// formatDailySummaryMessage formats the daily summary notification message
func (nm *NotificationManager) formatDailySummaryMessage(summary *DailySummaryInfo, aiInsights string) string {
	pnlEmoji := "📈"
	pnlSign := "+"
	if summary.TotalPnL < 0 {
		pnlEmoji = "📉"
		pnlSign = ""
	}

	winRateEmoji := "✅"
	if summary.WinRate < 0.5 {
		winRateEmoji = "⚠️"
	}

	msg := fmt.Sprintf(`📊 Daily Trading Summary - %s

%s P&L: %s$%.2f
%s Win Rate: %.1f%% (%d/%d trades)
🎯 Top Symbols: %s
🌍 Market: %s

`,
		summary.Date.Format("Jan 2"),
		pnlEmoji,
		pnlSign,
		summary.TotalPnL,
		winRateEmoji,
		summary.WinRate*100,
		summary.WinningTrades,
		summary.TotalTrades,
		strings.Join(summary.TopSymbols, ", "),
		summary.MarketCondition,
	)

	if aiInsights != "" {
		msg += fmt.Sprintf("🤖 AI Insights:\n%s\n", aiInsights)
	}

	return msg
}

// ScheduleDailySummary schedules daily summary generation at specified time (default 20:00 UTC)
func (nm *NotificationManager) ScheduleDailySummary(
	summaryTime time.Time, // Time to send summary (e.g., 20:00 UTC)
	getSummaryFunc func() (*DailySummaryInfo, error),
	llmCallFunc func(ctx context.Context, prompt string) (string, error),
) {
	go func() {
		for {
			select {
			case <-nm.ctx.Done():
				return
			default:
				now := time.Now().UTC()

				// Calculate next summary time
				nextSummary := time.Date(now.Year(), now.Month(), now.Day(),
					summaryTime.Hour(), summaryTime.Minute(), summaryTime.Second(), 0, time.UTC)

				// If we've passed today's summary time, schedule for tomorrow
				if now.After(nextSummary) {
					nextSummary = nextSummary.Add(24 * time.Hour)
				}

				// Wait until summary time
				sleepDuration := nextSummary.Sub(now)
				nm.logger.WithField("next_summary", nextSummary.Format("2006-01-02 15:04:05 UTC")).
					Info("Scheduled next daily summary")

				select {
				case <-nm.ctx.Done():
					return
				case <-time.After(sleepDuration):
					// Generate and send daily summary
					if getSummaryFunc != nil {
						summary, err := getSummaryFunc()
						if err != nil {
							nm.logger.WithError(err).Error("Failed to get daily summary data")
							continue
						}

						ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
						err = nm.GenerateDailySummaryWithAI(ctx, summary, llmCallFunc)
						cancel()

						if err != nil {
							nm.logger.WithError(err).Error("Failed to generate daily summary")
						} else {
							nm.logger.Info("Daily summary sent successfully")
						}
					}
				}
			}
		}
	}()
}
