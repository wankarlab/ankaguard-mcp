package models

import (
	"time"

	"github.com/thrasher-corp/gocryptotrader/exchanges/order"
)

// ScalpingSignal extiende las señales de trading existentes con información específica de scalping
type ScalpingSignal struct {
	ID            string                 `json:"id"`
	Pair          string                 `json:"pair"`
	Exchange      string                 `json:"exchange"`
	Direction     string                 `json:"direction"` // "buy", "sell", "hold"
	Confidence    float64                `json:"confidence"`
	AIConfidence  float64                `json:"ai_confidence"`
	AIDirection   string                 `json:"ai_direction"`
	Strategies    []string               `json:"strategies"` // Estrategias que generaron la señal
	CombinedScore float64                `json:"combined_score"`
	Price         float64                `json:"price"`
	Volume        float64                `json:"volume"`
	Timestamp     time.Time              `json:"timestamp"`
	Metadata      map[string]interface{} `json:"metadata"`
}

// ScalpingTrade representa un trade ejecutado por el sistema de scalping
type ScalpingTrade struct {
	ID         string        `json:"id"`
	Pair       string        `json:"pair"`
	Exchange   string        `json:"exchange"`
	Side       order.Side    `json:"side"`
	EntryPrice float64       `json:"entry_price"`
	ExitPrice  float64       `json:"exit_price"`
	Amount     float64       `json:"amount"`
	PnL        float64       `json:"pnl"`
	PnLPercent float64       `json:"pnl_percent"`
	Fee        float64       `json:"fee"`
	Duration   time.Duration `json:"duration"`
	Strategies []string      `json:"strategies"`
	AIUsed     bool          `json:"ai_used"`
	Latency    time.Duration `json:"latency"`
	Slippage   float64       `json:"slippage"`
	OpenedAt   time.Time     `json:"opened_at"`
	ClosedAt   time.Time     `json:"closed_at"`
	Status     string        `json:"status"`
	OrderID    string        `json:"order_id"`
	SignalID   string        `json:"signal_id"`
}

// EngineMetrics contiene métricas del motor de scalping
type EngineMetrics struct {
	TotalTrades      int           `json:"total_trades"`
	WinningTrades    int           `json:"winning_trades"`
	LosingTrades     int           `json:"losing_trades"`
	WinRate          float64       `json:"win_rate"`
	TotalPnL         float64       `json:"total_pnl"`
	TotalPnLPercent  float64       `json:"total_pnl_percent"`
	TotalProfit      float64       `json:"total_profit"`
	TotalLoss        float64       `json:"total_loss"`
	AvgTrade         float64       `json:"avg_trade"`
	AvgLatency       time.Duration `json:"avg_latency"`
	MaxLatency       time.Duration `json:"max_latency"`
	MinLatency       time.Duration `json:"min_latency"`
	ActivePairs      int           `json:"active_pairs"`
	ActiveStrategies int           `json:"active_strategies"`
	AIAccuracy       float64       `json:"ai_accuracy"`
	SharpeRatio      float64       `json:"sharpe_ratio"`
	MaxDrawdown      float64       `json:"max_drawdown"`
	ProfitFactor     float64       `json:"profit_factor"`
	AvgTradeDuration time.Duration `json:"avg_trade_duration"`
	LastUpdated      time.Time     `json:"last_updated"`
	LastTradeAt      time.Time     `json:"last_trade_at"`
	ErrorCount       int           `json:"error_count"`
	LastErrorAt      time.Time     `json:"last_error_at"`
	LastError        string        `json:"last_error"`
}

// EngineStatus representa el estado actual del motor de scalping
type EngineStatus struct {
	Running          bool          `json:"running"`
	StartedAt        time.Time     `json:"started_at"`
	Uptime           time.Duration `json:"uptime"`
	ActivePairs      []string      `json:"active_pairs"`
	ActiveStrategies []string      `json:"active_strategies"`
	OpenPositions    int           `json:"open_positions"`
	TotalCapital     float64       `json:"total_capital"`
	AvailableCapital float64       `json:"available_capital"`
	Exposure         float64       `json:"exposure"`
	ExposurePercent  float64       `json:"exposure_percent"`
	LastSignal       time.Time     `json:"last_signal"`
	LastTrade        time.Time     `json:"last_trade"`
	HealthStatus     string        `json:"health_status"`
	HealthIssues     []string      `json:"health_issues"`
	Errors           []string      `json:"errors"`
}

// AIModelInfo contiene información sobre el modelo de IA
type AIModelInfo struct {
	ModelType      string    `json:"model_type"`
	Version        string    `json:"version"`
	TrainedAt      time.Time `json:"trained_at"`
	Accuracy       float64   `json:"accuracy"`
	Precision      float64   `json:"precision"`
	Recall         float64   `json:"recall"`
	F1Score        float64   `json:"f1_score"`
	Samples        int       `json:"samples"`
	Features       int       `json:"features"`
	Epochs         int       `json:"epochs"`
	Loss           float64   `json:"loss"`
	ValidationLoss float64   `json:"validation_loss"`
	ModelPath      string    `json:"model_path"`
	Active         bool      `json:"active"`
}

// Prediction representa una predicción del modelo de IA
type Prediction struct {
	Direction    string             `json:"direction"`     // "up", "down", "neutral"
	Confidence   float64            `json:"confidence"`    // 0-1
	ExpectedMove float64            `json:"expected_move"` // % esperado
	TimeHorizon  int                `json:"time_horizon"`  // minutos
	Timestamp    time.Time          `json:"timestamp"`
	ModelVersion string             `json:"model_version"`
	Features     map[string]float64 `json:"features"`
}

// Features contiene las características para el modelo de IA
type Features struct {
	Prices      []float64 `json:"prices"`
	Volumes     []float64 `json:"volumes"`
	RSI         float64   `json:"rsi"`
	MACD        float64   `json:"macd"`
	MACDSignal  float64   `json:"macd_signal"`
	MACDHist    float64   `json:"macd_hist"`
	BBUpper     float64   `json:"bb_upper"`
	BBMiddle    float64   `json:"bb_middle"`
	BBLower     float64   `json:"bb_lower"`
	ATR         float64   `json:"atr"`
	Spread      float64   `json:"spread"`
	Volatility  float64   `json:"volatility"`
	TimeOfDay   int       `json:"time_of_day"`
	DayOfWeek   int       `json:"day_of_week"`
	VolumeRatio float64   `json:"volume_ratio"`
	PriceChange float64   `json:"price_change"`
	Momentum    float64   `json:"momentum"`
}

// RiskParameters contiene parámetros de gestión de riesgo
type RiskParameters struct {
	MaxPositionSizeUSD   float64 `json:"max_position_size_usd"`
	MaxTotalExposureUSD  float64 `json:"max_total_exposure_usd"`
	StopLossPercent      float64 `json:"stop_loss_percent"`
	TakeProfitPercent    float64 `json:"take_profit_percent"`
	MaxDailyLossUSD      float64 `json:"max_daily_loss_usd"`
	MaxConsecutiveLosses int     `json:"max_consecutive_losses"`
	VolatilityMultiplier float64 `json:"volatility_multiplier"`
	KellyFraction        float64 `json:"kelly_fraction"`
	RiskFreeRate         float64 `json:"risk_free_rate"`
	ConfidenceThreshold  float64 `json:"confidence_threshold"`
}

// StrategyConfig contiene configuración de una estrategia
type StrategyConfig struct {
	Name          string                 `json:"name"`
	Enabled       bool                   `json:"enabled"`
	Weight        float64                `json:"weight"`
	Params        map[string]interface{} `json:"params"`
	MinConfidence float64                `json:"min_confidence"`
}
