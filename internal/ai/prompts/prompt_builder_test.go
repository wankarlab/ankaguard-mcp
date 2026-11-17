package prompts

import (
	"strings"
	"testing"

	"github.com/rantipay/trading-mcp/internal/ai"
)

func TestPromptBuilder_BuildAnalysisPrompt(t *testing.T) {
	// Create a template manager with mock templates
	tm := NewTemplateManager("")
	tm.templates["scalping-analysis"] = "Analysis for {{PAIR}} at {{CURRENT_PRICE}}\nRSI: {{INDICATORS}}\nSupport: {{SUPPORT}}, Resistance: {{RESISTANCE}}"
	tm.templates["day-trading-analysis"] = "Day trading for {{PAIR}}"

	builder := NewPromptBuilder(tm)

	tests := []struct {
		name                string
		pair                string
		ctx                 *ai.MarketContext
		styleType           string
		primaryTimeframe    string
		secondaryTimeframes []string
		knowledgeContext    string
		wantErr             bool
		wantContains        []string
	}{
		{
			name:             "valid scalping prompt",
			pair:             "BTC-USDT",
			styleType:        "scalping",
			primaryTimeframe: "1m",
			secondaryTimeframes: []string{"5m", "15m"},
			knowledgeContext: "Test knowledge",
			ctx: &ai.MarketContext{
				CurrentPrice: 45000.0,
				Timeframes: map[string]*ai.TimeframeContext{
					"1m": {
						Trend:    "bullish",
						Strength: 0.75,
						Indicators: &ai.Indicators{
							RSI: 65.0,
							ATR: 100.0,
						},
					},
				},
				KeyLevels: &ai.KeyLevels{
					Support:    44000.0,
					Resistance: 46000.0,
				},
			},
			wantErr:      false,
			wantContains: []string{"BTC-USDT", "45000", "44000", "46000"},
		},
		{
			name:             "missing style type",
			pair:             "BTC-USDT",
			styleType:        "",
			primaryTimeframe: "1m",
			ctx:              &ai.MarketContext{CurrentPrice: 45000.0},
			wantErr:          true,
		},
		{
			name:             "unknown style type",
			pair:             "BTC-USDT",
			styleType:        "invalid_style",
			primaryTimeframe: "1m",
			ctx:              &ai.MarketContext{CurrentPrice: 45000.0},
			wantErr:          true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt, err := builder.BuildAnalysisPrompt(
				tt.pair,
				tt.ctx,
				tt.styleType,
				tt.primaryTimeframe,
				tt.secondaryTimeframes,
				tt.knowledgeContext,
			)

			if (err != nil) != tt.wantErr {
				t.Errorf("BuildAnalysisPrompt() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				for _, contains := range tt.wantContains {
					if !strings.Contains(prompt, contains) {
						t.Errorf("BuildAnalysisPrompt() result missing '%s', got: %s", contains, prompt)
					}
				}
			}
		})
	}
}

func TestPromptBuilder_buildVariables(t *testing.T) {
	tm := NewTemplateManager("")
	builder := NewPromptBuilder(tm)

	ctx := &ai.MarketContext{
		CurrentPrice: 45000.0,
		Timeframes: map[string]*ai.TimeframeContext{
			"1m": {
				Trend:    "bullish",
				Strength: 0.75,
				Indicators: &ai.Indicators{
					RSI: 65.0,
					ATR: 100.0,
					MACD: &ai.MACD{
						Value:     0.0012,
						Signal:    0.0008,
						Histogram: 0.0004,
					},
				},
				Candles: []ai.Candle{
					{Close: 44900, Volume: 1000},
					{Close: 45000, Volume: 1200},
				},
			},
		},
		KeyLevels: &ai.KeyLevels{
			Support:    44000.0,
			Resistance: 46000.0,
		},
	}

	variables := builder.buildVariables(
		"BTC-USDT",
		ctx,
		"1m",
		[]string{"5m", "15m"},
		"Test knowledge",
	)

	// Check required variables
	requiredVars := []string{
		"PAIR", "CURRENT_PRICE", "TIMESTAMP", "PRIMARY_TIMEFRAME",
		"SECONDARY_TIMEFRAMES", "TIMEFRAME_ANALYSIS", "INDICATORS",
		"SUPPORT", "RESISTANCE", "VOLATILITY", "VOLUME_STATUS",
		"TREND_ALIGNMENT", "TRADING_SESSION", "MARKET_PHASE",
		"KNOWLEDGE_CONTEXT",
	}

	for _, varName := range requiredVars {
		if _, ok := variables[varName]; !ok {
			t.Errorf("buildVariables() missing required variable: %s", varName)
		}
	}

	// Check specific values
	if variables["PAIR"] != "BTC-USDT" {
		t.Errorf("buildVariables() PAIR = %v, want BTC-USDT", variables["PAIR"])
	}

	if variables["CURRENT_PRICE"] != "45000.00" {
		t.Errorf("buildVariables() CURRENT_PRICE = %v, want 45000.00", variables["CURRENT_PRICE"])
	}

	if variables["SUPPORT"] != "44000.00" {
		t.Errorf("buildVariables() SUPPORT = %v, want 44000.00", variables["SUPPORT"])
	}
}

func TestPromptBuilder_getVolatilityDescription(t *testing.T) {
	tm := NewTemplateManager("")
	builder := NewPromptBuilder(tm)

	// Create a common strategy for all test cases
	testStyle := &ai.Strategy{
		Timeframes: []ai.TimeframeConfig{
			{Interval: "1m"},
		},
	}

	tests := []struct {
		name  string
		ctx   *ai.MarketContext
		want  string
	}{
		{
			name: "low volatility",
			ctx: &ai.MarketContext{
				CurrentPrice: 45000.0,
				Style:        testStyle,
				Timeframes: map[string]*ai.TimeframeContext{
					"1m": {
						Indicators: &ai.Indicators{ATR: 200.0}, // 0.44% of price
					},
				},
			},
			want: "Low",
		},
		{
			name: "moderate volatility",
			ctx: &ai.MarketContext{
				CurrentPrice: 45000.0,
				Style:        testStyle,
				Timeframes: map[string]*ai.TimeframeContext{
					"1m": {
						Indicators: &ai.Indicators{ATR: 450.0}, // 1.0% of price
					},
				},
			},
			want: "Moderate",
		},
		{
			name: "high volatility",
			ctx: &ai.MarketContext{
				CurrentPrice: 45000.0,
				Style:        testStyle,
				Timeframes: map[string]*ai.TimeframeContext{
					"1m": {
						Indicators: &ai.Indicators{ATR: 1350.0}, // 3.0% of price
					},
				},
			},
			want: "High",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := builder.getVolatilityDescription(tt.ctx)
			if got != tt.want {
				t.Errorf("getVolatilityDescription() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPromptBuilder_getTrendAlignment(t *testing.T) {
	tm := NewTemplateManager("")
	builder := NewPromptBuilder(tm)

	tests := []struct {
		name string
		ctx  *ai.MarketContext
		want string
	}{
		{
			name: "fully aligned bullish",
			ctx: &ai.MarketContext{
				Timeframes: map[string]*ai.TimeframeContext{
					"1m":  {Trend: "bullish"},
					"5m":  {Trend: "bullish"},
					"15m": {Trend: "bullish"},
				},
			},
			want: "Fully Aligned Bullish",
		},
		{
			name: "fully aligned bearish",
			ctx: &ai.MarketContext{
				Timeframes: map[string]*ai.TimeframeContext{
					"1m":  {Trend: "bearish"},
					"5m":  {Trend: "bearish"},
					"15m": {Trend: "bearish"},
				},
			},
			want: "Fully Aligned Bearish",
		},
		{
			name: "mostly bullish",
			ctx: &ai.MarketContext{
				Timeframes: map[string]*ai.TimeframeContext{
					"1m":  {Trend: "bullish"},
					"5m":  {Trend: "bullish"},
					"15m": {Trend: "bearish"},
				},
			},
			want: "Mostly Bullish",
		},
		{
			name: "mixed neutral",
			ctx: &ai.MarketContext{
				Timeframes: map[string]*ai.TimeframeContext{
					"1m":  {Trend: "bullish"},
					"5m":  {Trend: "bearish"},
					"15m": {Trend: "sideways"},
				},
			},
			want: "Mixed/Neutral",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := builder.getTrendAlignment(tt.ctx)
			if got != tt.want {
				t.Errorf("getTrendAlignment() = %v, want %v", got, tt.want)
			}
		})
	}
}
