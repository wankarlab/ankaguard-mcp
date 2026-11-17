package handlers

import (
	"testing"

	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/sirupsen/logrus"
)

// TestGenerateRecommendation_BuySignal tests BUY recommendation generation
func TestGenerateRecommendation_BuySignal(t *testing.T) {
	handler := NewAnalysisHandler(&AnalysisHandlerConfig{
		Logger: logrus.New(),
	})

	response := &models.ComprehensiveAnalysisResponse{
		TechnicalIndicators: &models.IndicatorsResponse{
			RSI: &models.RSIIndicator{
				Value:  30.0, // Oversold - bullish
				Signal: "oversold",
			},
			MACD: &models.MACDIndicator{
				Histogram: 5.0,
				Trend:     "bullish", // Bullish
			},
			Bollinger: &models.BollingerBandsIndicator{
				Position: "lower", // Near lower band - bullish
			},
		},
	}

	recommendation := handler.generateRecommendation(response)

	if recommendation != "BUY" {
		t.Errorf("Expected BUY recommendation, got %s", recommendation)
	}
}

// TestGenerateRecommendation_SellSignal tests SELL recommendation generation
func TestGenerateRecommendation_SellSignal(t *testing.T) {
	handler := NewAnalysisHandler(&AnalysisHandlerConfig{
		Logger: logrus.New(),
	})

	response := &models.ComprehensiveAnalysisResponse{
		TechnicalIndicators: &models.IndicatorsResponse{
			RSI: &models.RSIIndicator{
				Value:  75.0, // Overbought - bearish
				Signal: "overbought",
			},
			MACD: &models.MACDIndicator{
				Histogram: -3.0,
				Trend:     "bearish", // Bearish
			},
			Bollinger: &models.BollingerBandsIndicator{
				Position: "outside_upper", // Above upper band - bearish
			},
		},
	}

	recommendation := handler.generateRecommendation(response)

	if recommendation != "SELL" {
		t.Errorf("Expected SELL recommendation, got %s", recommendation)
	}
}

// TestGenerateRecommendation_WaitSignal tests WAIT recommendation generation
func TestGenerateRecommendation_WaitSignal(t *testing.T) {
	handler := NewAnalysisHandler(&AnalysisHandlerConfig{
		Logger: logrus.New(),
	})

	response := &models.ComprehensiveAnalysisResponse{
		TechnicalIndicators: &models.IndicatorsResponse{
			RSI: &models.RSIIndicator{
				Value:  50.0, // Neutral
				Signal: "neutral",
			},
			MACD: &models.MACDIndicator{
				Histogram: 0.5,
				Trend:     "neutral", // Neutral
			},
			Bollinger: &models.BollingerBandsIndicator{
				Position: "middle", // Middle - neutral
			},
		},
	}

	recommendation := handler.generateRecommendation(response)

	if recommendation != "WAIT" {
		t.Errorf("Expected WAIT recommendation, got %s", recommendation)
	}
}

// TestGenerateRecommendation_MixedSignals tests mixed signals result in WAIT
func TestGenerateRecommendation_MixedSignals(t *testing.T) {
	handler := NewAnalysisHandler(&AnalysisHandlerConfig{
		Logger: logrus.New(),
	})

	response := &models.ComprehensiveAnalysisResponse{
		TechnicalIndicators: &models.IndicatorsResponse{
			RSI: &models.RSIIndicator{
				Value:  30.0, // Oversold - bullish
				Signal: "oversold",
			},
			MACD: &models.MACDIndicator{
				Histogram: -3.0,
				Trend:     "bearish", // Bearish - conflicting
			},
		},
	}

	recommendation := handler.generateRecommendation(response)

	// With conflicting signals (50/50), should not reach 60% consensus
	if recommendation != "WAIT" {
		t.Errorf("Expected WAIT for mixed signals, got %s", recommendation)
	}
}

// TestGenerateRecommendation_LLMOverride tests that LLM analysis overrides technical indicators
func TestGenerateRecommendation_LLMOverride(t *testing.T) {
	handler := NewAnalysisHandler(&AnalysisHandlerConfig{
		Logger: logrus.New(),
	})

	response := &models.ComprehensiveAnalysisResponse{
		TechnicalIndicators: &models.IndicatorsResponse{
			RSI: &models.RSIIndicator{
				Value:  75.0, // Overbought - would suggest SELL
				Signal: "overbought",
			},
		},
		LLMAnalysis: &models.TradingSignalResponse{
			Action: "BUY", // LLM says BUY
		},
	}

	recommendation := handler.generateRecommendation(response)

	// LLM analysis should override technical indicators
	if recommendation != "BUY" {
		t.Errorf("Expected BUY from LLM override, got %s", recommendation)
	}
}

// TestGenerateRecommendation_WithSentiment tests sentiment inclusion in recommendation
func TestGenerateRecommendation_WithSentiment(t *testing.T) {
	handler := NewAnalysisHandler(&AnalysisHandlerConfig{
		Logger: logrus.New(),
	})

	response := &models.ComprehensiveAnalysisResponse{
		TechnicalIndicators: &models.IndicatorsResponse{
			RSI: &models.RSIIndicator{
				Value:  30.0, // Oversold - bullish
				Signal: "oversold",
			},
			MACD: &models.MACDIndicator{
				Trend: "bullish", // Bullish
			},
		},
		SentimentAnalysis: &models.SentimentResponse{
			Overall: 0.7, // Bullish sentiment (>0.3)
		},
	}

	recommendation := handler.generateRecommendation(response)

	// All signals bullish (3/3 = 100%), should be BUY
	if recommendation != "BUY" {
		t.Errorf("Expected BUY with bullish sentiment, got %s", recommendation)
	}
}

// TestGenerateRecommendation_NoIndicators tests fallback when no indicators available
func TestGenerateRecommendation_NoIndicators(t *testing.T) {
	handler := NewAnalysisHandler(&AnalysisHandlerConfig{
		Logger: logrus.New(),
	})

	response := &models.ComprehensiveAnalysisResponse{
		TechnicalIndicators: nil, // No indicators
	}

	recommendation := handler.generateRecommendation(response)

	// Should default to WAIT when no data
	if recommendation != "WAIT" {
		t.Errorf("Expected WAIT when no indicators, got %s", recommendation)
	}
}

// TestGenerateRecommendation_ConsensusThreshold tests 60% consensus requirement
func TestGenerateRecommendation_ConsensusThreshold(t *testing.T) {
	handler := NewAnalysisHandler(&AnalysisHandlerConfig{
		Logger: logrus.New(),
	})

	tests := []struct {
		name           string
		response       *models.ComprehensiveAnalysisResponse
		wantRecommendation string
	}{
		{
			name: "Below 60% bullish - WAIT",
			response: &models.ComprehensiveAnalysisResponse{
				TechnicalIndicators: &models.IndicatorsResponse{
					RSI: &models.RSIIndicator{Signal: "oversold"},   // Bullish
					MACD: &models.MACDIndicator{Trend: "bullish"},   // Bullish
					Bollinger: &models.BollingerBandsIndicator{Position: "middle"}, // Neutral
				},
				SentimentAnalysis: &models.SentimentResponse{Overall: 0.1}, // Neutral (-0.3 to 0.3 is neutral)
			},
			wantRecommendation: "WAIT", // 2/4 bullish = 50% < 60%
		},
		{
			name: "Exactly 60% bearish - SELL",
			response: &models.ComprehensiveAnalysisResponse{
				TechnicalIndicators: &models.IndicatorsResponse{
					RSI: &models.RSIIndicator{Signal: "overbought"}, // Bearish
					MACD: &models.MACDIndicator{Trend: "bearish"},   // Bearish
					Bollinger: &models.BollingerBandsIndicator{Position: "upper"}, // Bearish
				},
			},
			wantRecommendation: "SELL", // 3/3 = 100%
		},
		{
			name: "Below 60% threshold - WAIT",
			response: &models.ComprehensiveAnalysisResponse{
				TechnicalIndicators: &models.IndicatorsResponse{
					RSI: &models.RSIIndicator{Signal: "oversold"},     // Bullish
					MACD: &models.MACDIndicator{Trend: "neutral"},     // Neutral
					Bollinger: &models.BollingerBandsIndicator{Position: "middle"}, // Neutral
				},
			},
			wantRecommendation: "WAIT", // 1/3 bullish = 33% < 60%
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recommendation := handler.generateRecommendation(tt.response)
			if recommendation != tt.wantRecommendation {
				t.Errorf("Expected %s, got %s", tt.wantRecommendation, recommendation)
			}
		})
	}
}
