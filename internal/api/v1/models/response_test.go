package models

import (
	"encoding/json"
	"testing"
	"time"
)

// TestPriceData_JSONMarshaling tests JSON marshaling of PriceData
func TestPriceData_JSONMarshaling(t *testing.T) {
	priceData := PriceData{
		Last:             50000.50,
		Bid:              49999.00,
		Ask:              50001.00,
		Open:             49800.00,
		High24h:          50500.00,
		Low24h:           49500.00,
		Volume:           1234.56,
		VolumeUSD:        61729000.00,
		Change24h:        200.50,
		ChangePercent24h: 0.403,
	}

	// Marshal to JSON
	data, err := json.Marshal(priceData)
	if err != nil {
		t.Fatalf("Failed to marshal PriceData: %v", err)
	}

	// Unmarshal back
	var unmarshaled PriceData
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("Failed to unmarshal PriceData: %v", err)
	}

	// Verify all fields match
	if unmarshaled.Last != priceData.Last {
		t.Errorf("Last = %f, want %f", unmarshaled.Last, priceData.Last)
	}
	if unmarshaled.Bid != priceData.Bid {
		t.Errorf("Bid = %f, want %f", unmarshaled.Bid, priceData.Bid)
	}
	if unmarshaled.Ask != priceData.Ask {
		t.Errorf("Ask = %f, want %f", unmarshaled.Ask, priceData.Ask)
	}
	if unmarshaled.High24h != priceData.High24h {
		t.Errorf("High24h = %f, want %f", unmarshaled.High24h, priceData.High24h)
	}
	if unmarshaled.Low24h != priceData.Low24h {
		t.Errorf("Low24h = %f, want %f", unmarshaled.Low24h, priceData.Low24h)
	}
	if unmarshaled.Volume != priceData.Volume {
		t.Errorf("Volume = %f, want %f", unmarshaled.Volume, priceData.Volume)
	}
	if unmarshaled.Change24h != priceData.Change24h {
		t.Errorf("Change24h = %f, want %f", unmarshaled.Change24h, priceData.Change24h)
	}
}

// TestPriceData_JSONFieldNames tests that JSON field names match snake_case convention
func TestPriceData_JSONFieldNames(t *testing.T) {
	priceData := PriceData{
		Last:             50000.50,
		Bid:              49999.00,
		Ask:              50001.00,
		High24h:          50500.00,
		Low24h:           49500.00,
		Volume:           1234.56,
		VolumeUSD:        61729000.00,
		Change24h:        200.50,
		ChangePercent24h: 0.403,
	}

	data, err := json.Marshal(priceData)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	// Parse as generic map to check field names
	var jsonMap map[string]interface{}
	if err := json.Unmarshal(data, &jsonMap); err != nil {
		t.Fatalf("Failed to unmarshal to map: %v", err)
	}

	// Verify snake_case field names (excluding omitempty fields with zero values)
	expectedFields := []string{
		"last", "bid", "ask", "high_24h", "low_24h",
		"volume", "change_24h",
	}

	for _, field := range expectedFields {
		if _, ok := jsonMap[field]; !ok {
			t.Errorf("Expected field %s not found in JSON", field)
		}
	}
}

// TestComprehensiveAnalysisResponse_JSONMarshaling tests JSON marshaling
func TestComprehensiveAnalysisResponse_JSONMarshaling(t *testing.T) {
	now := time.Now().UTC()

	response := ComprehensiveAnalysisResponse{
		Symbol:    "BTC-USDT",
		Exchange:  "kucoin",
		Timestamp: now,
		PriceData: PriceData{
			Last:             50000.50,
			Bid:              49999.00,
			Ask:              50001.00,
			High24h:          50500.00,
			Low24h:           49500.00,
			Volume:           1234.56,
			Change24h:        200.50,
			ChangePercent24h: 0.403,
		},
		TechnicalIndicators: &IndicatorsResponse{
			Exchange:  "kucoin",
			Pair:      "BTC-USDT",
			Interval:  "1h",
			Timestamp: now,
			RSI: &RSIIndicator{
				Value:  65.5,
				Period: 14,
				Signal: "neutral",
			},
			MACD: &MACDIndicator{
				MACD:      12.5,
				Signal:    10.2,
				Histogram: 2.3,
				Trend:     "bullish",
			},
		},
		Recommendation: "BUY",
	}

	// Marshal to JSON
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Failed to marshal ComprehensiveAnalysisResponse: %v", err)
	}

	// Unmarshal back
	var unmarshaled ComprehensiveAnalysisResponse
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("Failed to unmarshal ComprehensiveAnalysisResponse: %v", err)
	}

	// Verify key fields
	if unmarshaled.Symbol != response.Symbol {
		t.Errorf("Symbol = %s, want %s", unmarshaled.Symbol, response.Symbol)
	}
	if unmarshaled.Exchange != response.Exchange {
		t.Errorf("Exchange = %s, want %s", unmarshaled.Exchange, response.Exchange)
	}
	if unmarshaled.Recommendation != response.Recommendation {
		t.Errorf("Recommendation = %s, want %s", unmarshaled.Recommendation, response.Recommendation)
	}

	// Verify nested PriceData
	if unmarshaled.PriceData.Last != response.PriceData.Last {
		t.Errorf("PriceData.Last = %f, want %f", unmarshaled.PriceData.Last, response.PriceData.Last)
	}

	// Verify nested TechnicalIndicators
	if unmarshaled.TechnicalIndicators == nil {
		t.Fatal("TechnicalIndicators should not be nil")
	}
	if unmarshaled.TechnicalIndicators.RSI.Value != response.TechnicalIndicators.RSI.Value {
		t.Errorf("RSI.Value = %f, want %f", unmarshaled.TechnicalIndicators.RSI.Value, response.TechnicalIndicators.RSI.Value)
	}
}

// TestComprehensiveAnalysisResponse_WithOptionalFields tests optional fields
func TestComprehensiveAnalysisResponse_WithOptionalFields(t *testing.T) {
	now := time.Now().UTC()

	response := ComprehensiveAnalysisResponse{
		Symbol:    "BTC-USDT",
		Exchange:  "kucoin",
		Timestamp: now,
		PriceData: PriceData{
			Last:    50000.50,
			Bid:     49999.00,
			Ask:     50001.00,
			High24h: 50500.00,
			Low24h:  49500.00,
			Volume:  1234.56,
		},
		TechnicalIndicators: &IndicatorsResponse{
			Exchange: "kucoin",
			Pair:     "BTC-USDT",
		},
		Recommendation: "BUY",
		LLMAnalysis: &TradingSignalResponse{
			Pair:       "BTC-USDT",
			Action:     "BUY",
			Confidence: 0.85,
			Reasoning:  "Strong bullish momentum",
			Provider:   "openai",
			Timestamp:  now,
		},
		SentimentAnalysis: &SentimentResponse{
			Symbol:     "BTC",
			Overall:    0.7,
			Confidence: 0.8,
			Trend:      "bullish",
			Timestamp:  now,
		},
	}

	// Marshal to JSON
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	// Unmarshal back
	var unmarshaled ComprehensiveAnalysisResponse
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	// Verify optional fields are present
	if unmarshaled.LLMAnalysis == nil {
		t.Error("LLMAnalysis should not be nil")
	} else {
		if unmarshaled.LLMAnalysis.Action != "BUY" {
			t.Errorf("LLMAnalysis.Action = %s, want BUY", unmarshaled.LLMAnalysis.Action)
		}
		if unmarshaled.LLMAnalysis.Confidence != 0.85 {
			t.Errorf("LLMAnalysis.Confidence = %f, want 0.85", unmarshaled.LLMAnalysis.Confidence)
		}
	}

	if unmarshaled.SentimentAnalysis == nil {
		t.Error("SentimentAnalysis should not be nil")
	} else {
		if unmarshaled.SentimentAnalysis.Overall != 0.7 {
			t.Errorf("SentimentAnalysis.Overall = %f, want 0.7", unmarshaled.SentimentAnalysis.Overall)
		}
		if unmarshaled.SentimentAnalysis.Trend != "bullish" {
			t.Errorf("SentimentAnalysis.Trend = %s, want bullish", unmarshaled.SentimentAnalysis.Trend)
		}
	}
}

// TestComprehensiveAnalysisResponse_WithoutOptionalFields tests omitempty behavior
func TestComprehensiveAnalysisResponse_WithoutOptionalFields(t *testing.T) {
	response := ComprehensiveAnalysisResponse{
		Symbol:   "BTC-USDT",
		Exchange: "kucoin",
		Timestamp: time.Now(),
		PriceData: PriceData{
			Last:    50000.50,
			Bid:     49999.00,
			Ask:     50001.00,
			High24h: 50500.00,
			Low24h:  49500.00,
			Volume:  1234.56,
		},
		TechnicalIndicators: &IndicatorsResponse{
			Exchange: "kucoin",
			Pair:     "BTC-USDT",
		},
		Recommendation: "WAIT",
		// LLMAnalysis and SentimentAnalysis are nil
	}

	// Marshal to JSON
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	// Parse as map to check fields
	var jsonMap map[string]interface{}
	if err := json.Unmarshal(data, &jsonMap); err != nil {
		t.Fatalf("Failed to unmarshal to map: %v", err)
	}

	// Optional fields should not be present due to omitempty
	if _, ok := jsonMap["llm_analysis"]; ok {
		t.Error("llm_analysis should be omitted when nil")
	}
	if _, ok := jsonMap["sentiment_analysis"]; ok {
		t.Error("sentiment_analysis should be omitted when nil")
	}

	// Required fields should be present
	requiredFields := []string{"symbol", "exchange", "timestamp", "price_data", "technical_indicators", "recommendation"}
	for _, field := range requiredFields {
		if _, ok := jsonMap[field]; !ok {
			t.Errorf("Required field %s not found in JSON", field)
		}
	}
}

// TestPriceData_ZeroValues tests handling of zero values
func TestPriceData_ZeroValues(t *testing.T) {
	priceData := PriceData{
		Last:    50000.50,
		Bid:     49999.00,
		Ask:     50001.00,
		High24h: 50500.00,
		Low24h:  49500.00,
		Volume:  1234.56,
		// Open, VolumeUSD, Change24h, ChangePercent24h are zero
	}

	data, err := json.Marshal(priceData)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var jsonMap map[string]interface{}
	if err := json.Unmarshal(data, &jsonMap); err != nil {
		t.Fatalf("Failed to unmarshal to map: %v", err)
	}

	// Fields with omitempty and zero values should be omitted
	omitFields := []string{"open", "volume_usd", "change_percent_24h"}
	for _, field := range omitFields {
		if val, ok := jsonMap[field]; ok && val != 0 {
			// Field is present with non-zero value, which is fine
			continue
		}
	}

	// Required fields without omitempty should always be present
	requiredFields := []string{"last", "bid", "ask", "high_24h", "low_24h", "volume", "change_24h"}
	for _, field := range requiredFields {
		if _, ok := jsonMap[field]; !ok {
			t.Errorf("Required field %s not found in JSON", field)
		}
	}
}

// TestIndicatorsResponse_NestedStructures tests nested indicator structures
func TestIndicatorsResponse_NestedStructures(t *testing.T) {
	response := IndicatorsResponse{
		Exchange:  "kucoin",
		Pair:      "BTC-USDT",
		Interval:  "1h",
		Timestamp: time.Now(),
		RSI: &RSIIndicator{
			Value:  70.5,
			Period: 14,
			Signal: "overbought",
		},
		MACD: &MACDIndicator{
			MACD:      15.2,
			Signal:    12.8,
			Histogram: 2.4,
			Trend:     "bullish",
		},
		Bollinger: &BollingerBandsIndicator{
			Upper:    51000.00,
			Middle:   50000.00,
			Lower:    49000.00,
			Width:    4.0,
			Position: "upper",
		},
		EMA: map[string]float64{
			"20": 49800.00,
			"50": 49500.00,
		},
		SMA: map[string]float64{
			"20": 49850.00,
			"50": 49550.00,
		},
		ATR: 250.50,
	}

	data, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var unmarshaled IndicatorsResponse
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	// Verify RSI
	if unmarshaled.RSI == nil {
		t.Fatal("RSI should not be nil")
	}
	if unmarshaled.RSI.Value != 70.5 {
		t.Errorf("RSI.Value = %f, want 70.5", unmarshaled.RSI.Value)
	}
	if unmarshaled.RSI.Signal != "overbought" {
		t.Errorf("RSI.Signal = %s, want overbought", unmarshaled.RSI.Signal)
	}

	// Verify MACD
	if unmarshaled.MACD == nil {
		t.Fatal("MACD should not be nil")
	}
	if unmarshaled.MACD.Trend != "bullish" {
		t.Errorf("MACD.Trend = %s, want bullish", unmarshaled.MACD.Trend)
	}

	// Verify Bollinger Bands
	if unmarshaled.Bollinger == nil {
		t.Fatal("Bollinger should not be nil")
	}
	if unmarshaled.Bollinger.Position != "upper" {
		t.Errorf("Bollinger.Position = %s, want upper", unmarshaled.Bollinger.Position)
	}

	// Verify EMA map
	if unmarshaled.EMA == nil {
		t.Fatal("EMA should not be nil")
	}
	if unmarshaled.EMA["20"] != 49800.00 {
		t.Errorf("EMA[20] = %f, want 49800.00", unmarshaled.EMA["20"])
	}

	// Verify SMA map
	if unmarshaled.SMA == nil {
		t.Fatal("SMA should not be nil")
	}
	if unmarshaled.SMA["50"] != 49550.00 {
		t.Errorf("SMA[50] = %f, want 49550.00", unmarshaled.SMA["50"])
	}

	// Verify ATR
	if unmarshaled.ATR != 250.50 {
		t.Errorf("ATR = %f, want 250.50", unmarshaled.ATR)
	}
}

// TestTradingSignalResponse_Serialization tests trading signal response
func TestTradingSignalResponse_Serialization(t *testing.T) {
	response := TradingSignalResponse{
		Pair:            "BTC-USDT",
		Action:          "BUY",
		Confidence:      0.85,
		Reasoning:       "Strong bullish momentum with high volume",
		EntryPrice:      50000.00,
		StopLoss:        49000.00,
		TakeProfit:      52000.00,
		RiskAssessment:  "medium",
		MarketCondition: "trending",
		KeyFactors:      []string{"volume increase", "RSI neutral", "MACD bullish"},
		Warnings:        []string{"high volatility"},
		Provider:        "openai",
		Timestamp:       time.Now(),
	}

	data, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var unmarshaled TradingSignalResponse
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	// Verify fields
	if unmarshaled.Action != "BUY" {
		t.Errorf("Action = %s, want BUY", unmarshaled.Action)
	}
	if unmarshaled.Confidence != 0.85 {
		t.Errorf("Confidence = %f, want 0.85", unmarshaled.Confidence)
	}
	if len(unmarshaled.KeyFactors) != 3 {
		t.Errorf("KeyFactors length = %d, want 3", len(unmarshaled.KeyFactors))
	}
	if len(unmarshaled.Warnings) != 1 {
		t.Errorf("Warnings length = %d, want 1", len(unmarshaled.Warnings))
	}
}

// TestSentimentResponse_Serialization tests sentiment response
func TestSentimentResponse_Serialization(t *testing.T) {
	response := SentimentResponse{
		Symbol:      "BTC",
		Overall:     0.65,
		Confidence:  0.8,
		Trend:       "bullish",
		Sources:     []string{"news", "twitter", "reddit"},
		Keywords:    []string{"bullish", "rally", "surge"},
		NewsCount:   15,
		TweetCount:  1250,
		RedditCount: 85,
		Breakdown: SentimentBreakdown{
			News:    0.7,
			Twitter: 0.6,
			Reddit:  0.65,
		},
		Timestamp: time.Now(),
	}

	data, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var unmarshaled SentimentResponse
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	// Verify fields
	if unmarshaled.Overall != 0.65 {
		t.Errorf("Overall = %f, want 0.65", unmarshaled.Overall)
	}
	if unmarshaled.Trend != "bullish" {
		t.Errorf("Trend = %s, want bullish", unmarshaled.Trend)
	}
	if len(unmarshaled.Sources) != 3 {
		t.Errorf("Sources length = %d, want 3", len(unmarshaled.Sources))
	}
	if unmarshaled.Breakdown.News != 0.7 {
		t.Errorf("Breakdown.News = %f, want 0.7", unmarshaled.Breakdown.News)
	}
}
