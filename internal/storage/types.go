package storage

import (
	"time"

	"github.com/rantipay/trading-mcp/internal/ai"
)

// TradingStyleRecord represents a trading style stored in the database
type TradingStyleRecord struct {
	ID                  int64
	Name                string
	Type                string
	ConfigYAML          string
	PrimaryTimeframe    string
	SecondaryTimeframes string // JSON array
	RequiredTimeframes  string // JSON array
	CreatedAt           time.Time
	UpdatedAt           time.Time
	Active              bool
}

// CandleRecord represents a cached candle in the database
type CandleRecord struct {
	ID        int64
	Pair      string
	Timeframe string
	Timestamp time.Time
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64
	CachedAt  time.Time
	ExpiresAt time.Time
}

// TradeRecord represents a trade in the database
type TradeRecord struct {
	ID                 int64
	TradeID            string
	Pair               string
	Exchange           string
	Side               string
	TradingStyle       string
	TradingStyleType   string
	PrimaryTimeframe   string
	TimeframesUsed     string // JSON array
	EntryPrice         float64
	EntryTime          time.Time
	Size               float64
	Leverage           int
	ExitPrice          *float64
	ExitTime           *time.Time
	ExitReason         *string
	StopLoss           *float64
	TakeProfit         *float64
	PnL                *float64
	PnLPct             *float64
	Fees               float64
	NetPnL             *float64
	Confidence         float64
	DecisionReason     *string
	IndicatorsSnapshot *string // JSON
	Status             string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// PerformanceMetricsRecord represents aggregated metrics in the database
type PerformanceMetricsRecord struct {
	ID               int64
	TradingStyle     string
	TradingStyleType string
	Timeframe        *string // NULL for aggregated
	Pair             *string // NULL for aggregated
	PeriodStart      time.Time
	PeriodEnd        time.Time
	TotalTrades      int
	WinningTrades    int
	LosingTrades     int
	WinRate          float64
	ProfitFactor     float64
	AvgWin           float64
	AvgLoss          float64
	LargestWin       float64
	LargestLoss      float64
	MaxDrawdown      float64
	MaxDrawdownPct   float64
	SharpeRatio      float64
	TotalPnL         float64
	TotalPnLPct      float64
	TotalFees        float64
	NetPnL           float64
	AvgConfidence    float64
	MinConfidence    float64
	MaxConfidence    float64
	CalculatedAt     time.Time
}

// Trade represents a trade for application use
type Trade struct {
	ID                 string
	Pair               string
	Exchange           string
	Side               string
	TradingStyle       string
	TradingStyleType   string
	PrimaryTimeframe   string
	TimeframesUsed     []string
	EntryPrice         float64
	EntryTime          time.Time
	Size               float64
	Leverage           int
	ExitPrice          float64
	ExitTime           time.Time
	ExitReason         string
	StopLoss           float64
	TakeProfit         float64
	PnL                float64
	PnLPct             float64
	Fees               float64
	NetPnL             float64
	Confidence         float64
	DecisionReason     string
	IndicatorsSnapshot map[string]interface{}
	Status             string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// PerformanceMetrics represents aggregated performance metrics
type PerformanceMetrics struct {
	TradingStyle     string
	TradingStyleType string
	Timeframe        string
	Pair             string
	PeriodStart      time.Time
	PeriodEnd        time.Time
	TotalTrades      int
	WinningTrades    int
	LosingTrades     int
	WinRate          float64
	ProfitFactor     float64
	AvgWin           float64
	AvgLoss          float64
	LargestWin       float64
	LargestLoss      float64
	MaxDrawdown      float64
	MaxDrawdownPct   float64
	SharpeRatio      float64
	TotalPnL         float64
	TotalPnLPct      float64
	TotalFees        float64
	NetPnL           float64
	AvgConfidence    float64
	MinConfidence    float64
	MaxConfidence    float64
	CalculatedAt     time.Time
}

// Candle represents a market candle
type Candle struct {
	Timestamp time.Time
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64
}

// ToAICandle converts storage Candle to ai.Candle
func (c *Candle) ToAICandle() ai.Candle {
	return ai.Candle{
		Timestamp: c.Timestamp,
		Open:      c.Open,
		High:      c.High,
		Low:       c.Low,
		Close:     c.Close,
		Volume:    c.Volume,
	}
}
