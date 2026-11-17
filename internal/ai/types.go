package ai

import (
	"sync"
	"time"
)

// Candle represents a candlestick
type Candle struct {
	Timestamp time.Time
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64
}

// TimeframeData holds data for a specific timeframe
type TimeframeData struct {
	Timeframe string
	Candles   []Candle
	UpdatedAt time.Time
}

// MultiTimeframeData holds data from multiple timeframes
// REFACTORED: Now uses dynamic map instead of fixed fields
type MultiTimeframeData struct {
	Pair       string
	Timeframes map[string]*TimeframeData // Dynamic map: "1h" -> TimeframeData
	Style      *Strategy                 // Reference to trading style
	Timestamp  time.Time
}

// GetTimeframe returns timeframe data by key (e.g., "1h", "15m", "5m")
func (m *MultiTimeframeData) GetTimeframe(tf string) *TimeframeData {
	if m.Timeframes == nil {
		return nil
	}
	return m.Timeframes[tf]
}

// GetPrimaryTimeframe returns data for the primary timeframe defined in Style
func (m *MultiTimeframeData) GetPrimaryTimeframe() *TimeframeData {
	if m.Style == nil || len(m.Style.Timeframes) == 0 {
		return nil
	}
	// Primary is the first timeframe in the list
	primaryTF := m.Style.Timeframes[0].Interval
	return m.GetTimeframe(primaryTF)
}

// GetMacroTimeframe returns data for the longest (macro) timeframe
func (m *MultiTimeframeData) GetMacroTimeframe() *TimeframeData {
	if m.Style == nil || len(m.Style.Timeframes) == 0 {
		return nil
	}
	// Macro is the last timeframe in the list (assumed to be longest)
	macroTF := m.Style.Timeframes[len(m.Style.Timeframes)-1].Interval
	return m.GetTimeframe(macroTF)
}

// HasTimeframe checks if a timeframe exists in the data
func (m *MultiTimeframeData) HasTimeframe(tf string) bool {
	return m.GetTimeframe(tf) != nil
}

// GetAllTimeframes returns all available timeframe keys
func (m *MultiTimeframeData) GetAllTimeframes() []string {
	if m.Timeframes == nil {
		return nil
	}
	keys := make([]string, 0, len(m.Timeframes))
	for k := range m.Timeframes {
		keys = append(keys, k)
	}
	return keys
}

// Indicators holds technical indicators
type Indicators struct {
	RSI       float64
	MACD      *MACD
	Bollinger *Bollinger
	EMA20     float64
	Volume    float64
	ATR       float64 // Average True Range for dynamic stop loss
}

// MACD represents MACD indicator
type MACD struct {
	Value     float64
	Signal    float64
	Histogram float64
	Trend     string // "bullish", "bearish", "neutral"
}

// Bollinger represents Bollinger Bands
type Bollinger struct {
	Upper    float64
	Middle   float64
	Lower    float64
	Width    float64
	Position string // "upper", "middle", "lower", "outside_upper", "outside_lower"
}

// TimeframeContext holds analysis for a timeframe
type TimeframeContext struct {
	Timeframe  string
	Trend      string  // "bullish", "bearish", "sideways"
	Strength   float64 // 0-1
	Indicators *Indicators
	Candles    []Candle
}

// MarketContext holds complete market analysis
// REFACTORED: Now uses dynamic map instead of fixed fields
type MarketContext struct {
	Pair         string
	Timestamp    time.Time
	Timeframes   map[string]*TimeframeContext // Dynamic map: "1h" -> TimeframeContext
	Style        *Strategy                    // Reference to trading style
	CurrentPrice float64
	KeyLevels    *KeyLevels

	// Per-pair directional control (futures bidirectional trading). Set by the
	// bot's runAnalysis from the pair's own 1h EMA50 trend ONLY when the
	// directional feature is enabled — empty/"off" preserves legacy behavior.
	// These are per-call fields (never bot-level state), so one pair's direction
	// can never cross into another's.
	//   MacroTrend:      "bullish" | "bearish" | "neutral" — the pair's macro trend.
	//   DirectionalMode: "off" | "suppress_countertrend" | "trend_follow".
	MacroTrend      string
	DirectionalMode string
}

// GetTimeframe returns timeframe context by key (e.g., "1h", "15m", "5m")
func (m *MarketContext) GetTimeframe(tf string) *TimeframeContext {
	if m.Timeframes == nil {
		return nil
	}
	return m.Timeframes[tf]
}

// GetPrimaryContext returns context for the primary timeframe defined in Style
func (m *MarketContext) GetPrimaryContext() *TimeframeContext {
	if m.Style == nil || len(m.Style.Timeframes) == 0 {
		return nil
	}
	// Primary is the first timeframe in the list
	primaryTF := m.Style.Timeframes[0].Interval
	return m.GetTimeframe(primaryTF)
}

// GetMacroContext returns context for the longest (macro) timeframe
func (m *MarketContext) GetMacroContext() *TimeframeContext {
	if m.Style == nil || len(m.Style.Timeframes) == 0 {
		return nil
	}
	// Macro is the last timeframe in the list (assumed to be longest)
	macroTF := m.Style.Timeframes[len(m.Style.Timeframes)-1].Interval
	return m.GetTimeframe(macroTF)
}

// GetSecondaryContexts returns contexts for all secondary timeframes
func (m *MarketContext) GetSecondaryContexts() []*TimeframeContext {
	if m.Style == nil || len(m.Style.Timeframes) <= 1 {
		return nil
	}
	// Secondary are all timeframes except the first (primary)
	contexts := make([]*TimeframeContext, 0, len(m.Style.Timeframes)-1)
	for i := 1; i < len(m.Style.Timeframes); i++ {
		tf := m.Style.Timeframes[i].Interval
		if ctx := m.GetTimeframe(tf); ctx != nil {
			contexts = append(contexts, ctx)
		}
	}
	return contexts
}

// HasTimeframe checks if a timeframe exists in the context
func (m *MarketContext) HasTimeframe(tf string) bool {
	return m.GetTimeframe(tf) != nil
}

// GetAllTimeframes returns all available timeframe keys
func (m *MarketContext) GetAllTimeframes() []string {
	if m.Timeframes == nil {
		return nil
	}
	keys := make([]string, 0, len(m.Timeframes))
	for k := range m.Timeframes {
		keys = append(keys, k)
	}
	return keys
}

// GetTimeframeOrFallback returns preferred timeframe or fallback if not available
func (m *MarketContext) GetTimeframeOrFallback(preferred, fallback string) *TimeframeContext {
	if ctx := m.GetTimeframe(preferred); ctx != nil {
		return ctx
	}
	return m.GetTimeframe(fallback)
}

// KeyLevels holds support and resistance levels
type KeyLevels struct {
	Support    float64
	Resistance float64
	Distance   *LevelDistance
}

// LevelDistance holds distance to levels
type LevelDistance struct {
	ToSupport    float64 // percentage
	ToResistance float64 // percentage
}

// Decision represents a trading decision
type Decision struct {
	Action     string  // "BUY", "SELL", "WAIT"
	Confidence float64 // 0-1
	Reason     string
	Entry      float64
	StopLoss   float64
	TakeProfit float64
	Size       float64
	Leverage   int
	Strategy   string

	// Per-source vote attribution (Active Profit Engine F0). Set by
	// combineLLMAndTechnicalAnalysis so the post-trade labeler can record distinct
	// technical vs llm calibration observations instead of crediting both with the
	// combined side (which made them collinear in signal_calibration). Empty action
	// means the source did not contribute (falls back to the combined side).
	TechnicalAction     string  // technical sub-decision action
	LLMAction           string  // llm sub-decision action
	TechnicalConfidence float64 // technical sub-decision confidence
	LLMConfidence       float64 // llm sub-decision confidence
}

// TrapResult holds the minimal result needed by DecisionEngine from a trap detector.
// It mirrors the relevant fields of manipulation.TrapAnalysis without importing that package.
type TrapResult struct {
	Detected        bool
	TrapType        string
	Confidence      float64
	SuggestedAction string
}

// TrapDetector is the interface that DecisionEngine uses for manipulation detection.
// manipulation.Detector satisfies this interface via a thin adapter in ai_trading_bot.go.
type TrapDetector interface {
	AnalyzeRecentTrap(candles []Candle) *TrapResult
}

// TimeframeCache caches timeframe data
type TimeframeCache struct {
	data map[string]*CachedData
	mu   sync.RWMutex
}

// CachedData holds cached timeframe data
type CachedData struct {
	Data      *TimeframeData
	UpdatedAt time.Time
	TTL       time.Duration
}

// NewTimeframeCache creates a new cache
func NewTimeframeCache() *TimeframeCache {
	return &TimeframeCache{
		data: make(map[string]*CachedData),
	}
}

// Get retrieves cached data
func (c *TimeframeCache) Get(key string) (*TimeframeData, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	cached, exists := c.data[key]
	if !exists {
		return nil, false
	}

	// Check if expired
	if time.Since(cached.UpdatedAt) > cached.TTL {
		return nil, false
	}

	return cached.Data, true
}

// Set stores data in cache
func (c *TimeframeCache) Set(key string, data *TimeframeData, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.data[key] = &CachedData{
		Data:      data,
		UpdatedAt: time.Now(),
		TTL:       ttl,
	}
}

// Clear removes all cached data
func (c *TimeframeCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = make(map[string]*CachedData)
}
