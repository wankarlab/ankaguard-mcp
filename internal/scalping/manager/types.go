package manager

import (
	"context"
	"time"
)

// LifecycleEventType identifies the type of bot lifecycle event.
type LifecycleEventType string

const (
	EventBotStarted LifecycleEventType = "BOT_STARTED"
	EventBotStopped LifecycleEventType = "BOT_STOPPED"
	EventBotFailed  LifecycleEventType = "BOT_FAILED"
	EventMaxBotsHit LifecycleEventType = "MAX_BOTS_HIT"
	EventBotKilled  LifecycleEventType = "BOT_KILLED"
)

// LifecycleEvent describes a bot manager lifecycle event.
type LifecycleEvent struct {
	Type      LifecycleEventType
	BotID     string
	Pair      string
	Timestamp time.Time
	Reason    string
	Metadata  map[string]interface{}
}

// LifecycleHook is a callback invoked on bot manager lifecycle events.
type LifecycleHook func(event LifecycleEvent)

// BotManagerConfig provides optional construction-time configuration.
type BotManagerConfig struct {
	MaxBots int
}

// BotConfig holds the configuration for a scalping bot instance.
type BotConfig struct {
	Exchange            string   `json:"exchange"`
	Pairs               []string `json:"pairs"`
	MarketType          string   `json:"market_type,omitempty"`
	Strategies          []string `json:"strategies"`
	MaxPositionSize     float64  `json:"max_position_size_usd"`
	MaxDailyTrades      int      `json:"max_daily_trades"`
	StopLoss            float64  `json:"stop_loss_percent"`
	TakeProfit          float64  `json:"take_profit_percent"`
	ConfidenceThreshold float64  `json:"confidence_threshold,omitempty"`
	Leverage            *int     `json:"leverage,omitempty"`
	// PaperMode prevents the bot from sending real orders to the exchange.
	// The bot still runs its full analysis cycle and records trades as if
	// executed, allowing the feedback loop to learn without risking capital.
	PaperMode bool `json:"paper_mode,omitempty" yaml:"paper_mode"`
}

// Signal represents a trading signal.
type Signal struct {
	Pair       string  `json:"pair"`
	Direction  string  `json:"direction"`
	Confidence float64 `json:"confidence"`
	Strategy   string  `json:"strategy"`
	Price      float64 `json:"price"`
	Reason     string  `json:"reason"`
	Timestamp  string  `json:"timestamp"`
}

// BotTrade represents a trade executed by a bot.
type BotTrade struct {
	ID              string  `json:"id"`
	Timestamp       string  `json:"timestamp"`
	Symbol          string  `json:"symbol"`
	Direction       string  `json:"direction"`
	EntryPrice      float64 `json:"entry_price"`
	ExitPrice       float64 `json:"exit_price,omitempty"`
	Quantity        float64 `json:"quantity"`
	PnL             float64 `json:"pnl"`
	PnLPercent      float64 `json:"pnl_percent"`
	DurationSeconds int     `json:"duration_seconds"`
	Status          string  `json:"status"`
	Reason          string  `json:"reason,omitempty"`
	Strategy        string  `json:"strategy,omitempty"`
	EntryOrderID    string  `json:"entry_order_id,omitempty"`
	ExitOrderID     string  `json:"exit_order_id,omitempty"`
}

// DPMPositionInfo is a read-only snapshot of an open paper position's DPM state.
type DPMPositionInfo struct {
	Pair              string    `json:"pair"`
	Direction         string    `json:"direction"`
	EntryPrice        float64   `json:"entry_price"`
	CurrentQty        float64   `json:"current_qty"`
	OriginalQty       float64   `json:"original_qty"`
	OpenedAt          time.Time `json:"opened_at"`
	TakeProfit        float64   `json:"take_profit"`
	StopLoss          float64   `json:"stop_loss"`
	OriginalTP        float64   `json:"original_tp"`
	OriginalSL        float64   `json:"original_sl"`
	MaxFavorablePrice float64   `json:"max_favorable_price"`
	BreakevenMoved    bool      `json:"breakeven_moved"`
	Partial1Done      bool      `json:"partial1_done"`
	Partial2Done      bool      `json:"partial2_done"`
	PartialPnL        float64   `json:"partial_pnl"`
}

// PaperPosition tracks a simulated open position for paper-trading bots.
type PaperPosition struct {
	Pair       string
	Direction  string
	EntryPrice float64
	Quantity   float64
	OpenedAt   time.Time
	TakeProfit float64
	StopLoss   float64

	MaxFavorablePrice float64
	OriginalTP        float64
	OriginalSL        float64
	BreakevenMoved    bool
	Partial1Done      bool
	Partial2Done      bool
	PartialPnL        float64
	PartialQtyClosed  float64
	LastAICheck       time.Time
	EntryRSI          float64
	EntryMACDHist     float64
}

// BotInstance represents a running bot instance.
type BotInstance struct {
	ID              string
	Config          BotConfig
	Status          string
	StartTime       time.Time
	StopChan        chan struct{}
	cancelCtx       context.CancelFunc
	TradesToday     int
	PnLToday        float64
	TotalTrades     int
	WinningTrades   int
	LosingTrades    int
	ActivePositions int
	LastSignals     []Signal
	AvgProfit       float64
	AvgLoss         float64
	paperPositions      map[string]*PaperPosition
	livePositionMirrors map[string]*PaperPosition
	// pairCooldowns tracks when each pair is allowed to re-enter after a stop-loss.
	pairCooldowns map[string]time.Time
}

// IsPaperMode returns true if the bot is configured to simulate execution.
func (b *BotInstance) IsPaperMode() bool {
	return b.Config.PaperMode
}

// BotInfo is a lightweight summary of an active bot, suitable for listing.
type BotInfo struct {
	BotID           string           `json:"bot_id"`
	Pair            string           `json:"pair"`
	Pairs           []string         `json:"pairs"`
	Exchange        string           `json:"exchange"`
	StartedAt       time.Time        `json:"started_at"`
	Status          string           `json:"status"`
	PaperMode       bool             `json:"paper_mode"`
	TradesToday     int              `json:"trades_today"`
	PnLToday        float64          `json:"pnl_today"`
	TotalTrades     int              `json:"total_trades"`
	WinningTrades   int              `json:"winning_trades"`
	ActivePositions int              `json:"active_positions"`
	DPMPositions    []DPMPositionInfo `json:"dpm_positions,omitempty"`
	LastSignals     []Signal         `json:"last_signals,omitempty"`
}
