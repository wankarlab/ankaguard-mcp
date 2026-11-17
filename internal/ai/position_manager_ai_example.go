package ai

import (
	"github.com/sirupsen/logrus"
)

// Example: How to use the AI optimizer integration with Position Manager
//
// This example demonstrates how to set up and use the AI-based stop loss
// optimization feature with the Position Manager.

func ExamplePositionManagerWithAI() {
	// 1. Create logger
	logger := logrus.New()
	logger.SetLevel(logrus.InfoLevel)

	// 2. Configure position manager with AI management enabled
	config := &PositionConfig{
		AutoClose:             true,
		TrailingStop:          true,
		TrailingStopPct:       1.0,
		BreakevenAfterPct:     1.5,
		CheckInterval:         10,   // Check positions every 10 seconds
		AIManagement:          true, // Enable AI management
		AICheckInterval:       60,   // AI analyzes positions every 60 seconds
		AIConfidenceThreshold: 0.7,  // Only auto-adjust if AI confidence >= 0.7
	}

	// 3. Create position manager
	pm := NewPositionManager(config, logger)

	// 4. Create GCT client (for market data)
	// gctClient, _ := gct.NewGCTClient("localhost:9052", logger)

	// 5. Create indicator calculator
	// indicatorCalc := NewIndicatorCalculator(gctClient, "kucoin", logger)

	// 6. Create AI decision repository (for logging)
	// var aiDecisionRepo domain.AIDecisionRepository
	// aiDecisionRepo = repositories.NewAIDecisionRepository(db)

	// 7. Create execution history repository
	// var executionHistRepo domain.ExecutionHistoryRepository
	// executionHistRepo = repositories.NewExecutionHistoryRepository(db)

	// 8. Create AI optimizer
	// aiOptimizer := NewSLTPOptimizer(
	// 	gctClient,
	// 	indicatorCalc,
	// 	nil, // aiDecisionRepo - set this if you have database
	// 	nil, // executionHistRepo - set this if you have database
	// 	logger,
	// )

	// 9. Set AI optimizer on position manager
	// pm.SetAIOptimizer(aiOptimizer)

	// 10. Set AI decision repository (optional, for logging)
	// pm.SetAIDecisionRepo(aiDecisionRepo)

	// 11. Add a position with AI management enabled
	position := &ManagedPosition{
		ID:         "pos-123",
		Symbol:     "DOGE-USDT",
		Side:       "buy",
		EntryPrice: 0.14,
		Size:       1000,
		Leverage:   5,
		StopLoss:   0.13,
		TakeProfit: 0.16,
		Exchange:   "kucoin",
		AIManaged:  true, // Enable AI management for this position
	}

	pm.AddPosition(position)

	// 12. Start position manager (this will also start AI analysis)
	// ctx := context.Background()
	// pm.Start(ctx, priceProvider)

	// The position manager will now:
	// - Check positions every 10 seconds for SL/TP triggers
	// - Analyze positions with AI every 60 seconds
	// - Auto-adjust stop loss if:
	//   * AI confidence >= 0.7
	//   * Position has AIManaged = true
	//   * AI suggests an adjustment
	// - Log all AI decisions to database (if repository is set)

	logger.Info("Position manager with AI optimization is running")
}

// Example: How AI adjusts stop loss based on market conditions
//
// The AI optimizer analyzes market conditions and suggests stop loss adjustments:
//
// 1. High Volatility:
//    - Widens stop loss to avoid premature stops
//    - Example: ATR increases from 0.002 to 0.005
//    - AI suggests moving SL from 0.13 to 0.125 (wider)
//
// 2. Low Volatility:
//    - Tightens stop loss to protect profits
//    - Example: ATR decreases from 0.005 to 0.001
//    - AI suggests moving SL from 0.13 to 0.135 (tighter)
//
// 3. Trend Reversal:
//    - Detects bearish trend while in long position
//    - Suggests tightening stop loss to 1% below current price
//    - Example: MACD crosses below signal, RSI < 50
//
// 4. Confidence-Based Auto-Adjustment:
//    - Only auto-adjusts if confidence >= threshold (default 0.7)
//    - Lower confidence = suggestion only (logged but not applied)
//    - Higher confidence = automatic adjustment

// Example: Monitoring AI decisions
//
// All AI decisions are logged to the ai_decisions table:
//
// - decision_type: "stop_loss_adjust"
// - before_value: 0.13
// - after_value: 0.135
// - market_condition: JSON with trend, volatility, RSI, MACD, ATR
// - indicators: JSON with RSI, ATR, PnL%
// - confidence: 0.85
// - outcome: Set later when position closes ("success", "premature", "saved_loss")
//
// Query example:
// SELECT * FROM ai_decisions WHERE position_id = 'pos-123' ORDER BY created_at DESC;

// Example: Position with AI management disabled
//
// You can have positions with and without AI management:

func ExampleMixedAIManagement() {
	logger := logrus.New()
	config := DefaultPositionConfig()
	config.AIManagement = true // Enable AI globally

	pm := NewPositionManager(config, logger)

	// Position 1: AI managed
	pos1 := &ManagedPosition{
		ID:         "pos-1",
		Symbol:     "DOGE-USDT",
		Side:       "buy",
		EntryPrice: 0.14,
		StopLoss:   0.13,
		AIManaged:  true, // AI will auto-adjust this position
		Exchange:   "kucoin",
	}

	// Position 2: Manual management
	pos2 := &ManagedPosition{
		ID:         "pos-2",
		Symbol:     "BTC-USDT",
		Side:       "buy",
		EntryPrice: 50000,
		StopLoss:   49000,
		AIManaged:  false, // AI will only suggest, not auto-adjust
		Exchange:   "kucoin",
	}

	pm.AddPosition(pos1)
	pm.AddPosition(pos2)

	// AI will:
	// - Auto-adjust pos-1 if confidence is high
	// - Only log suggestions for pos-2 (no auto-adjustment)
}
