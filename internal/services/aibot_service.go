package services

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rantipay/trading-mcp/internal/ai"
	"github.com/sirupsen/logrus"
)

// AIBotService manages the AI trading bot lifecycle
type AIBotService struct {
	positionManager *ai.PositionManager
	log             *logrus.Logger
	mu              sync.RWMutex
	running         bool
	startedAt       time.Time
	stats           *BotStats
	currentConfig   *SimpleBotConfig
}

// BotStats holds bot statistics
type BotStats struct {
	TotalCycles      int       `json:"total_cycles"`
	BuySignals       int       `json:"buy_signals"`
	SellSignals      int       `json:"sell_signals"`
	WaitSignals      int       `json:"wait_signals"`
	AvgConfidence    float64   `json:"avg_confidence"`
	PositionsOpened  int       `json:"positions_opened"`
	PositionsClosed  int       `json:"positions_closed"`
	LastSignal       string    `json:"last_signal"`
	LastSignalTime   time.Time `json:"last_signal_time"`
	LastSignalPrice  float64   `json:"last_signal_price"`
	LastSignalReason string    `json:"last_signal_reason"`
}

// SimpleBotConfig holds the lightweight configuration used by AIBotService.
// This is intentionally minimal (7 fields). For the full production bot
// configuration (30+ fields) see TradingBotConfig in ai_trading_bot.go.
type SimpleBotConfig struct {
	Strategy    string  `json:"strategy"`
	Pair        string  `json:"pair"`
	Exchange    string  `json:"exchange"`
	MarketType  string  `json:"market_type,omitempty"` // spot, futures, margin, options
	AutoExecute bool    `json:"auto_execute"`
	Confidence  float64 `json:"confidence_threshold"`
	Leverage    *int    `json:"leverage,omitempty"` // only for futures/margin
}

// NewAIBotService creates a new AI bot service
func NewAIBotService(log *logrus.Logger) *AIBotService {
	return &AIBotService{
		log:   log,
		stats: &BotStats{},
	}
}

// Start starts the AI bot with given configuration
func (s *AIBotService) Start(ctx context.Context, config *SimpleBotConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("bot is already running")
	}

	s.log.WithFields(logrus.Fields{
		"strategy":     config.Strategy,
		"pair":         config.Pair,
		"auto_execute": config.AutoExecute,
	}).Info("Starting AI bot")

	// TODO: Initialize bot with config
	// This will be connected to the actual bot implementation

	s.running = true
	s.startedAt = time.Now()
	s.stats = &BotStats{}

	return nil
}

// Stop stops the AI bot
func (s *AIBotService) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return fmt.Errorf("bot is not running")
	}

	s.log.Info("Stopping AI bot")

	// TODO: Stop bot gracefully

	s.running = false

	return nil
}

// GetStatus returns current bot status
func (s *AIBotService) GetStatus() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	status := map[string]interface{}{
		"running":    s.running,
		"started_at": nil,
		"uptime":     0,
		"stats":      s.stats,
	}

	if s.running {
		status["started_at"] = s.startedAt
		status["uptime"] = int(time.Since(s.startedAt).Seconds())
	}

	return status
}

// GetPositions returns all open positions
func (s *AIBotService) GetPositions() []*ai.ManagedPosition {
	if s.positionManager == nil {
		return []*ai.ManagedPosition{}
	}
	return s.positionManager.GetAllPositions()
}

// GetPosition returns a specific position
func (s *AIBotService) GetPosition(positionID string) (*ai.ManagedPosition, error) {
	if s.positionManager == nil {
		return nil, fmt.Errorf("position manager not initialized")
	}

	pos, exists := s.positionManager.GetPosition(positionID)
	if !exists {
		return nil, fmt.Errorf("position not found: %s", positionID)
	}

	return pos, nil
}

// ClosePosition closes a specific position
func (s *AIBotService) ClosePosition(ctx context.Context, positionID string, reason string) error {
	if s.positionManager == nil {
		return fmt.Errorf("position manager not initialized")
	}

	if _, exists := s.positionManager.GetPosition(positionID); !exists {
		return fmt.Errorf("position not found: %s", positionID)
	}

	s.log.WithFields(logrus.Fields{
		"position_id": positionID,
		"reason":      reason,
	}).Info("Closing position manually")

	// TODO: Execute close order
	s.positionManager.RemovePosition(positionID)

	return nil
}

// UpdateStats updates bot statistics
func (s *AIBotService) UpdateStats(signal string, confidence float64, price float64, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.stats.TotalCycles++
	s.stats.LastSignal = signal
	s.stats.LastSignalTime = time.Now()
	s.stats.LastSignalPrice = price
	s.stats.LastSignalReason = reason

	switch signal {
	case "BUY":
		s.stats.BuySignals++
	case "SELL":
		s.stats.SellSignals++
	case "WAIT":
		s.stats.WaitSignals++
	}

	// Update average confidence
	s.stats.AvgConfidence = (s.stats.AvgConfidence*float64(s.stats.TotalCycles-1) + confidence) / float64(s.stats.TotalCycles)

}

// OnPositionOpened called when a position is opened
func (s *AIBotService) OnPositionOpened(pos *ai.ManagedPosition) {
	s.mu.Lock()
	s.stats.PositionsOpened++
	s.mu.Unlock()

}

// OnPositionClosed called when a position is closed
func (s *AIBotService) OnPositionClosed(pos *ai.ManagedPosition, reason string) {
	s.mu.Lock()
	s.stats.PositionsClosed++
	s.mu.Unlock()

}

// IsRunning returns whether the bot is running
func (s *AIBotService) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}
