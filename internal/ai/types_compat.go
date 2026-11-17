package ai

import (
	"github.com/sirupsen/logrus"
)

// Backward Compatibility Helpers
// These functions provide backward compatibility for code that expects
// the old structure with fixed TF_1h, TF_15m, TF_5m fields

var compatLog = logrus.New()

// GetTF_1h returns 1h timeframe data (backward compatibility)
// DEPRECATED: Use GetTimeframe("1h") instead
func (m *MultiTimeframeData) GetTF_1h() *TimeframeData {
	if m.Timeframes == nil {
		return nil
	}
	tf := m.Timeframes["1h"]
	if tf == nil {
		compatLog.Warn("Accessing TF_1h but 1h timeframe not available")
	}
	return tf
}

// GetTF_15m returns 15m timeframe data (backward compatibility)
// DEPRECATED: Use GetTimeframe("15m") instead
func (m *MultiTimeframeData) GetTF_15m() *TimeframeData {
	if m.Timeframes == nil {
		return nil
	}
	tf := m.Timeframes["15m"]
	if tf == nil {
		compatLog.Warn("Accessing TF_15m but 15m timeframe not available")
	}
	return tf
}

// GetTF_5m returns 5m timeframe data (backward compatibility)
// DEPRECATED: Use GetTimeframe("5m") instead
func (m *MultiTimeframeData) GetTF_5m() *TimeframeData {
	if m.Timeframes == nil {
		return nil
	}
	tf := m.Timeframes["5m"]
	if tf == nil {
		compatLog.Warn("Accessing TF_5m but 5m timeframe not available")
	}
	return tf
}

// GetTF_1h returns 1h timeframe context (backward compatibility)
// DEPRECATED: Use GetTimeframe("1h") instead
func (m *MarketContext) GetTF_1h() *TimeframeContext {
	if m.Timeframes == nil {
		return nil
	}
	tf := m.Timeframes["1h"]
	if tf == nil {
		compatLog.Warn("Accessing TF_1h context but 1h timeframe not available")
	}
	return tf
}

// GetTF_15m returns 15m timeframe context (backward compatibility)
// DEPRECATED: Use GetTimeframe("15m") instead
func (m *MarketContext) GetTF_15m() *TimeframeContext {
	if m.Timeframes == nil {
		return nil
	}
	tf := m.Timeframes["15m"]
	if tf == nil {
		compatLog.Warn("Accessing TF_15m context but 15m timeframe not available")
	}
	return tf
}

// GetTF_5m returns 5m timeframe context (backward compatibility)
// DEPRECATED: Use GetTimeframe("5m") instead
func (m *MarketContext) GetTF_5m() *TimeframeContext {
	if m.Timeframes == nil {
		return nil
	}
	tf := m.Timeframes["5m"]
	if tf == nil {
		compatLog.Warn("Accessing TF_5m context but 5m timeframe not available")
	}
	return tf
}

// NewMultiTimeframeDataFromLegacy creates MultiTimeframeData from legacy fixed fields
// This is useful for migrating old code that creates structures directly
func NewMultiTimeframeDataFromLegacy(pair string, tf1h, tf15m, tf5m *TimeframeData) *MultiTimeframeData {
	mtf := &MultiTimeframeData{
		Pair:       pair,
		Timeframes: make(map[string]*TimeframeData),
	}

	if tf1h != nil {
		mtf.Timeframes["1h"] = tf1h
	}
	if tf15m != nil {
		mtf.Timeframes["15m"] = tf15m
	}
	if tf5m != nil {
		mtf.Timeframes["5m"] = tf5m
	}

	return mtf
}

// NewMarketContextFromLegacy creates MarketContext from legacy fixed fields
func NewMarketContextFromLegacy(pair string, tf1h, tf15m, tf5m *TimeframeContext, currentPrice float64, keyLevels *KeyLevels) *MarketContext {
	mc := &MarketContext{
		Pair:         pair,
		Timeframes:   make(map[string]*TimeframeContext),
		CurrentPrice: currentPrice,
		KeyLevels:    keyLevels,
	}

	if tf1h != nil {
		mc.Timeframes["1h"] = tf1h
	}
	if tf15m != nil {
		mc.Timeframes["15m"] = tf15m
	}
	if tf5m != nil {
		mc.Timeframes["5m"] = tf5m
	}

	return mc
}

// GetDefaultStyle returns a default scalping strategy for backward compatibility
// This is used when no style is specified
func GetDefaultStyle() *Strategy {
	return &Strategy{
		Name: "default-scalping",
		Type: StyleScalping,
		Timeframes: []TimeframeConfig{
			{Interval: "5m", Weight: 0.5},
			{Interval: "15m", Weight: 0.3},
			{Interval: "1h", Weight: 0.2},
		},
		RequiredTimeframes: []string{"5m", "15m"},
		CandleLimits: map[string]int{
			"5m":  50,
			"15m": 50,
			"1h":  30,
		},
		Indicators: IndicatorConfig{
			RSI: RSIConfig{
				Oversold:   30,
				Overbought: 70,
				Period:     14,
				Weight:     1.0,
			},
			MACD: MACDConfig{
				Fast:   12,
				Slow:   26,
				Signal: 9,
				Weight: 1.0,
			},
			Bollinger: BollingerConfig{
				Period: 20,
				StdDev: 2.0,
				Weight: 1.0,
			},
		},
		Filters: FilterConfig{
			EnableMacroTrendFilter:   true,
			EnableReversalFilter:     true,
			EnableSupportFilter:      true,
			EnableVolumeFilter:       true,
			MacroTrendThreshold:      0.6,
			SupportDistanceThreshold: 2.0,
			VolumeHighThreshold:      1.2,
			VolumeLowThreshold:       0.5,
		},
		Alignment: AlignmentConfig{
			Required:      true,
			MinTimeframes: 2,
		},
		Confidence: 0.60,
		Risk: RiskConfig{
			TradeSize:               3.0,
			Leverage:                5,
			StopLossPct:             0.5,
			TakeProfitPct:           1.0,
			ATRStopLossMultiplier:   1.5,
			ATRTakeProfitMultiplier: 2.5,
		},
		Execution: ExecutionConfig{
			CheckInterval:       30,
			ConfidenceThreshold: 0.60,
		},
		PositionManagement: PositionManagementConfig{
			Enabled:            true,
			AutoClose:          true,
			TrailingStop:       true,
			TrailingStopPct:    0.3,
			BreakevenAfterPct:  0.3,
			PartialTakeProfits: false,
			CheckInterval:      10,
			RequireConfirm:     false,
		},
	}
}

// EnsureStyleSet ensures that a Style is set on the data structure
// If not set, it uses the default style
func (m *MultiTimeframeData) EnsureStyleSet() {
	if m.Style == nil {
		m.Style = GetDefaultStyle()
		compatLog.Debug("No style set on MultiTimeframeData, using default scalping style")
	}
}

// EnsureStyleSet ensures that a Style is set on the market context
// If not set, it uses the default style
func (m *MarketContext) EnsureStyleSet() {
	if m.Style == nil {
		m.Style = GetDefaultStyle()
		compatLog.Debug("No style set on MarketContext, using default scalping style")
	}
}

// SetCompatibilityLogger allows setting a custom logger for compatibility warnings
func SetCompatibilityLogger(log *logrus.Logger) {
	compatLog = log
}
