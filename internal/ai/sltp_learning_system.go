package ai

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/domain"
	"github.com/rantipay/trading-mcp/internal/gct"
)

// SLTPLearningSystem analyzes execution history to optimize SL/TP parameters
type SLTPLearningSystem struct {
	executionHistRepo domain.ExecutionHistoryRepository
	aiDecisionRepo    domain.AIDecisionRepository
	gctClient         gct.GCTClient
	logger            *logrus.Logger
	config            *LearningConfig
}

// LearningConfig holds configuration for the learning system
type LearningConfig struct {
	// Analysis periods
	AnalysisWindow     time.Duration `yaml:"analysis_window"`       // e.g., 30 days
	MinExecutionsForML int           `yaml:"min_executions_for_ml"` // Minimum executions needed for ML

	// Pattern detection
	PrematureStopThreshold  float64       `yaml:"premature_stop_threshold"`   // % price reversal to consider premature
	PrematureStopTimeWindow time.Duration `yaml:"premature_stop_time_window"` // Time window to check for reversal

	// Optimization parameters
	OptimalSLRangeMin float64 `yaml:"optimal_sl_range_min"` // Min SL distance %
	OptimalSLRangeMax float64 `yaml:"optimal_sl_range_max"` // Max SL distance %
	OptimalSLStepSize float64 `yaml:"optimal_sl_step_size"` // Step size for optimization

	// Learning thresholds
	MinConfidenceForSuggestion float64 `yaml:"min_confidence_for_suggestion"` // Min confidence to suggest changes
	LearningRate               float64 `yaml:"learning_rate"`                 // How aggressively to adjust
}

// ExecutionAnalysis represents analysis of a single execution
type ExecutionAnalysis struct {
	ExecutionID       string        `json:"execution_id"`
	Symbol            string        `json:"symbol"`
	Type              string        `json:"type"` // "stop_loss" or "take_profit"
	WasPremature      bool          `json:"was_premature"`
	PriceReversal     float64       `json:"price_reversal_pct"`
	TimeToReversal    time.Duration `json:"time_to_reversal"`
	OptimalSLDistance float64       `json:"optimal_sl_distance_pct"`
	ActualSLDistance  float64       `json:"actual_sl_distance_pct"`
	PnL               float64       `json:"pnl"`
	Timestamp         time.Time     `json:"timestamp"`
}

// PatternAnalysis represents patterns found in execution data
type PatternAnalysis struct {
	Symbol                string    `json:"symbol"`
	TotalExecutions       int       `json:"total_executions"`
	PrematureStops        int       `json:"premature_stops"`
	PrematureStopRate     float64   `json:"premature_stop_rate"`
	AverageOptimalSL      float64   `json:"average_optimal_sl_pct"`
	CurrentAverageSL      float64   `json:"current_average_sl_pct"`
	SuggestedSLAdjustment float64   `json:"suggested_sl_adjustment_pct"`
	Confidence            float64   `json:"confidence"`
	LastAnalysis          time.Time `json:"last_analysis"`
}

// OptimizationResult represents the result of SL/TP optimization
type OptimizationResult struct {
	Symbol            string                       `json:"symbol"`
	AnalysisPeriod    time.Duration                `json:"analysis_period"`
	TotalExecutions   int                          `json:"total_executions"`
	Patterns          []PatternAnalysis            `json:"patterns"`
	Recommendations   []OptimizationRecommendation `json:"recommendations"`
	OverallConfidence float64                      `json:"overall_confidence"`
	GeneratedAt       time.Time                    `json:"generated_at"`
}

// OptimizationRecommendation represents a specific optimization recommendation
type OptimizationRecommendation struct {
	Type                string                 `json:"type"` // "stop_loss_adjustment", "take_profit_adjustment"
	Symbol              string                 `json:"symbol"`
	CurrentValue        float64                `json:"current_value"`
	RecommendedValue    float64                `json:"recommended_value"`
	Adjustment          float64                `json:"adjustment_pct"`
	Reason              string                 `json:"reason"`
	Confidence          float64                `json:"confidence"`
	ExpectedImprovement string                 `json:"expected_improvement"`
	Metadata            map[string]interface{} `json:"metadata,omitempty"`
}

// NewSLTPLearningSystem creates a new learning system
func NewSLTPLearningSystem(
	executionHistRepo domain.ExecutionHistoryRepository,
	aiDecisionRepo domain.AIDecisionRepository,
	gctClient gct.GCTClient,
	config *LearningConfig,
	logger *logrus.Logger,
) *SLTPLearningSystem {
	if logger == nil {
		logger = logrus.New()
	}

	if config == nil {
		config = GetDefaultLearningConfig()
	}

	return &SLTPLearningSystem{
		executionHistRepo: executionHistRepo,
		aiDecisionRepo:    aiDecisionRepo,
		gctClient:         gctClient,
		logger:            logger,
		config:            config,
	}
}

// AnalyzeExecutions analyzes execution history to identify patterns
func (ls *SLTPLearningSystem) AnalyzeExecutions(ctx context.Context, symbol string) (*OptimizationResult, error) {
	// Get execution history for the analysis window
	from := time.Now().Add(-ls.config.AnalysisWindow)
	to := time.Now()

	executions, err := ls.executionHistRepo.GetByDateRange(ctx, from, to)
	if err != nil {
		return nil, fmt.Errorf("failed to get execution history: %w", err)
	}

	// Filter executions for the specific symbol if provided
	var filteredExecutions []*domain.ExecutionHistory
	for _, exec := range executions {
		if symbol == "" || exec.Symbol == symbol {
			filteredExecutions = append(filteredExecutions, exec)
		}
	}

	if len(filteredExecutions) < ls.config.MinExecutionsForML {
		return nil, fmt.Errorf("insufficient execution data: need at least %d executions, got %d",
			ls.config.MinExecutionsForML, len(filteredExecutions))
	}

	ls.logger.WithFields(logrus.Fields{
		"symbol":     symbol,
		"executions": len(filteredExecutions),
		"period":     ls.config.AnalysisWindow,
	}).Info("Starting execution analysis")

	// Analyze each execution
	analyses := make([]*ExecutionAnalysis, 0, len(filteredExecutions))
	for _, exec := range filteredExecutions {
		analysis, err := ls.analyzeExecution(ctx, exec)
		if err != nil {
			ls.logger.WithError(err).WithField("execution_id", exec.ID).Warn("Failed to analyze execution")
			continue
		}
		analyses = append(analyses, analysis)
	}

	// Group analyses by symbol and detect patterns
	patterns := ls.detectPatterns(analyses)

	// Generate recommendations
	recommendations := ls.generateRecommendations(patterns)

	// Calculate overall confidence
	overallConfidence := ls.calculateOverallConfidence(patterns)

	result := &OptimizationResult{
		Symbol:            symbol,
		AnalysisPeriod:    ls.config.AnalysisWindow,
		TotalExecutions:   len(analyses),
		Patterns:          patterns,
		Recommendations:   recommendations,
		OverallConfidence: overallConfidence,
		GeneratedAt:       time.Now(),
	}

	// Store optimization results as AI decisions
	if err := ls.storeOptimizationResults(ctx, result); err != nil {
		ls.logger.WithError(err).Warn("Failed to store optimization results")
	}

	return result, nil
}

// analyzeExecution analyzes a single execution to determine if it was premature
func (ls *SLTPLearningSystem) analyzeExecution(ctx context.Context, exec *domain.ExecutionHistory) (*ExecutionAnalysis, error) {
	analysis := &ExecutionAnalysis{
		ExecutionID: exec.ID,
		Symbol:      exec.Symbol,
		Type:        exec.Type,
		PnL:         exec.PnL.InexactFloat64(),
		Timestamp:   exec.ExecutedAt,
	}

	// Calculate actual SL distance
	triggerPrice := exec.TriggerPrice.InexactFloat64()
	executionPrice := exec.ExecutionPrice.InexactFloat64()

	if triggerPrice > 0 && executionPrice > 0 {
		analysis.ActualSLDistance = math.Abs(triggerPrice-executionPrice) / executionPrice * 100
	}

	// Check if this was a premature stop loss
	if exec.Type == "stop_loss" {
		wasPremature, reversal, timeToReversal := ls.checkPrematureStop(ctx, exec)
		analysis.WasPremature = wasPremature
		analysis.PriceReversal = reversal
		analysis.TimeToReversal = timeToReversal

		if wasPremature {
			// Calculate what would have been the optimal SL distance
			analysis.OptimalSLDistance = ls.calculateOptimalSLDistance(ctx, exec, reversal)
		}
	}

	return analysis, nil
}

// checkPrematureStop checks if a stop loss was premature by looking at price action after execution
func (ls *SLTPLearningSystem) checkPrematureStop(ctx context.Context, exec *domain.ExecutionHistory) (bool, float64, time.Duration) {
	// Get price data after the execution
	_ = exec.ExecutedAt.Add(ls.config.PrematureStopTimeWindow) // endTime for future use

	// Get candle data to check price movement
	// Note: This is a simplified implementation - in production you'd want more granular data
	candles, err := ls.gctClient.GetCandles(ctx, exec.Exchange, exec.Symbol, "1h", 24) // Get 24 hours of data
	if err != nil {
		ls.logger.WithError(err).Warn("Failed to get candle data for premature stop analysis")
		return false, 0, 0
	}

	if len(candles) == 0 {
		return false, 0, 0
	}

	executionPrice := exec.ExecutionPrice.InexactFloat64()
	maxReversal := 0.0
	var timeToMaxReversal time.Duration

	// Check if price reversed significantly after the stop loss
	for _, candle := range candles {
		var reversal float64

		// For long positions (stop loss below entry), check if price went back up
		// For short positions (stop loss above entry), check if price went back down
		if exec.Reason == "Stop loss triggered" {
			// Determine position side from price relationship (simplified)
			if exec.TriggerPrice.InexactFloat64() < executionPrice {
				// Likely a long position stop loss
				reversal = (candle.High - executionPrice) / executionPrice * 100
			} else {
				// Likely a short position stop loss
				reversal = (executionPrice - candle.Low) / executionPrice * 100
			}
		}

		if reversal > maxReversal {
			maxReversal = reversal
			timeToMaxReversal = time.Hour // Simplified - use 1 hour as placeholder
		}
	}

	// Consider it premature if price reversed more than the threshold
	isPremature := maxReversal >= ls.config.PrematureStopThreshold

	return isPremature, maxReversal, timeToMaxReversal
}

// calculateOptimalSLDistance calculates what would have been the optimal SL distance
func (ls *SLTPLearningSystem) calculateOptimalSLDistance(ctx context.Context, exec *domain.ExecutionHistory, priceReversal float64) float64 {
	// The optimal SL would have been just beyond the maximum adverse movement
	// Add a small buffer to account for volatility
	buffer := priceReversal * 0.2 // 20% buffer
	optimalDistance := priceReversal + buffer

	// Ensure it's within reasonable bounds
	if optimalDistance < ls.config.OptimalSLRangeMin {
		optimalDistance = ls.config.OptimalSLRangeMin
	} else if optimalDistance > ls.config.OptimalSLRangeMax {
		optimalDistance = ls.config.OptimalSLRangeMax
	}

	return optimalDistance
}

// detectPatterns analyzes execution data to detect patterns
func (ls *SLTPLearningSystem) detectPatterns(analyses []*ExecutionAnalysis) []PatternAnalysis {
	// Group analyses by symbol
	symbolGroups := make(map[string][]*ExecutionAnalysis)
	for _, analysis := range analyses {
		symbolGroups[analysis.Symbol] = append(symbolGroups[analysis.Symbol], analysis)
	}

	patterns := make([]PatternAnalysis, 0, len(symbolGroups))

	for symbol, symbolAnalyses := range symbolGroups {
		pattern := ls.analyzeSymbolPattern(symbol, symbolAnalyses)
		patterns = append(patterns, pattern)
	}

	return patterns
}

// analyzeSymbolPattern analyzes patterns for a specific symbol
func (ls *SLTPLearningSystem) analyzeSymbolPattern(symbol string, analyses []*ExecutionAnalysis) PatternAnalysis {
	totalExecutions := len(analyses)
	prematureStops := 0
	totalOptimalSL := 0.0
	totalActualSL := 0.0
	validSLCount := 0

	for _, analysis := range analyses {
		if analysis.Type == "stop_loss" {
			if analysis.WasPremature {
				prematureStops++
			}

			if analysis.OptimalSLDistance > 0 {
				totalOptimalSL += analysis.OptimalSLDistance
				validSLCount++
			}

			if analysis.ActualSLDistance > 0 {
				totalActualSL += analysis.ActualSLDistance
			}
		}
	}

	prematureStopRate := 0.0
	if totalExecutions > 0 {
		prematureStopRate = float64(prematureStops) / float64(totalExecutions) * 100
	}

	averageOptimalSL := 0.0
	if validSLCount > 0 {
		averageOptimalSL = totalOptimalSL / float64(validSLCount)
	}

	currentAverageSL := 0.0
	if totalExecutions > 0 {
		currentAverageSL = totalActualSL / float64(totalExecutions)
	}

	// Calculate suggested adjustment
	suggestedAdjustment := 0.0
	confidence := 0.0

	if averageOptimalSL > 0 && currentAverageSL > 0 {
		suggestedAdjustment = averageOptimalSL - currentAverageSL

		// Calculate confidence based on data quality and consistency
		confidence = ls.calculatePatternConfidence(prematureStopRate, validSLCount, totalExecutions)
	}

	return PatternAnalysis{
		Symbol:                symbol,
		TotalExecutions:       totalExecutions,
		PrematureStops:        prematureStops,
		PrematureStopRate:     prematureStopRate,
		AverageOptimalSL:      averageOptimalSL,
		CurrentAverageSL:      currentAverageSL,
		SuggestedSLAdjustment: suggestedAdjustment,
		Confidence:            confidence,
		LastAnalysis:          time.Now(),
	}
}

// calculatePatternConfidence calculates confidence in the pattern analysis
func (ls *SLTPLearningSystem) calculatePatternConfidence(prematureStopRate float64, validSLCount, totalExecutions int) float64 {
	confidence := 0.0

	// Base confidence on data volume
	if totalExecutions >= 50 {
		confidence += 0.4
	} else if totalExecutions >= 20 {
		confidence += 0.2
	} else if totalExecutions >= 10 {
		confidence += 0.1
	}

	// Increase confidence if we have good SL data
	if validSLCount >= totalExecutions/2 {
		confidence += 0.3
	} else if validSLCount >= totalExecutions/4 {
		confidence += 0.2
	}

	// Increase confidence if premature stop rate is significant
	if prematureStopRate >= 30 {
		confidence += 0.3
	} else if prematureStopRate >= 20 {
		confidence += 0.2
	} else if prematureStopRate >= 10 {
		confidence += 0.1
	}

	// Ensure confidence is within bounds
	if confidence > 1.0 {
		confidence = 1.0
	}

	return confidence
}

// generateRecommendations generates optimization recommendations based on patterns
func (ls *SLTPLearningSystem) generateRecommendations(patterns []PatternAnalysis) []OptimizationRecommendation {
	recommendations := make([]OptimizationRecommendation, 0)

	for _, pattern := range patterns {
		if pattern.Confidence < ls.config.MinConfidenceForSuggestion {
			continue
		}

		// Generate stop loss adjustment recommendation
		if math.Abs(pattern.SuggestedSLAdjustment) >= 1.0 { // At least 1% adjustment
			adjustment := pattern.SuggestedSLAdjustment * ls.config.LearningRate

			recommendation := OptimizationRecommendation{
				Type:             "stop_loss_adjustment",
				Symbol:           pattern.Symbol,
				CurrentValue:     pattern.CurrentAverageSL,
				RecommendedValue: pattern.CurrentAverageSL + adjustment,
				Adjustment:       adjustment,
				Confidence:       pattern.Confidence,
			}

			if adjustment > 0 {
				recommendation.Reason = fmt.Sprintf("High premature stop rate (%.1f%%) suggests stop loss is too tight", pattern.PrematureStopRate)
				recommendation.ExpectedImprovement = "Reduce premature stops and improve position survival rate"
			} else {
				recommendation.Reason = "Analysis suggests stop loss can be tightened without increasing premature stops"
				recommendation.ExpectedImprovement = "Better risk management with tighter stops"
			}

			recommendations = append(recommendations, recommendation)
		}
	}

	return recommendations
}

// calculateOverallConfidence calculates overall confidence in the optimization results
func (ls *SLTPLearningSystem) calculateOverallConfidence(patterns []PatternAnalysis) float64 {
	if len(patterns) == 0 {
		return 0.0
	}

	totalConfidence := 0.0
	for _, pattern := range patterns {
		totalConfidence += pattern.Confidence
	}

	return totalConfidence / float64(len(patterns))
}

// storeOptimizationResults stores optimization results as AI decisions for tracking
func (ls *SLTPLearningSystem) storeOptimizationResults(ctx context.Context, result *OptimizationResult) error {
	for _, recommendation := range result.Recommendations {
		decision := &domain.AIDecision{
			ID:           fmt.Sprintf("learning_%d", time.Now().UnixNano()),
			PositionID:   "", // Not tied to a specific position
			DecisionType: "learning_optimization",
			BeforeValue:  decimal.NewFromFloat(recommendation.CurrentValue),
			AfterValue:   decimal.NewFromFloat(recommendation.RecommendedValue),
			MarketCondition: fmt.Sprintf(`{"symbol": "%s", "analysis_period": "%s", "total_executions": %d}`,
				recommendation.Symbol, result.AnalysisPeriod, result.TotalExecutions),
			Indicators: fmt.Sprintf(`{"premature_stop_rate": "%.2f", "confidence": "%.2f"}`,
				0.0, recommendation.Confidence), // Would need to get from pattern
			Confidence: decimal.NewFromFloat(recommendation.Confidence),
			Outcome:    "pending",
			CreatedAt:  time.Now(),
		}

		if err := ls.aiDecisionRepo.Create(ctx, decision); err != nil {
			return fmt.Errorf("failed to store optimization decision: %w", err)
		}
	}

	return nil
}

// RunWeeklyOptimization runs the weekly optimization process
func (ls *SLTPLearningSystem) RunWeeklyOptimization(ctx context.Context) error {
	ls.logger.Info("Starting weekly SL/TP optimization")

	// Get all symbols that have executions in the analysis window
	from := time.Now().Add(-ls.config.AnalysisWindow)
	executions, err := ls.executionHistRepo.GetByDateRange(ctx, from, time.Now())
	if err != nil {
		return fmt.Errorf("failed to get executions for optimization: %w", err)
	}

	// Get unique symbols
	symbolSet := make(map[string]bool)
	for _, exec := range executions {
		symbolSet[exec.Symbol] = true
	}

	symbols := make([]string, 0, len(symbolSet))
	for symbol := range symbolSet {
		symbols = append(symbols, symbol)
	}

	ls.logger.WithField("symbols", len(symbols)).Info("Running optimization for symbols")

	// Analyze each symbol
	totalRecommendations := 0
	for _, symbol := range symbols {
		result, err := ls.AnalyzeExecutions(ctx, symbol)
		if err != nil {
			ls.logger.WithError(err).WithField("symbol", symbol).Warn("Failed to analyze symbol")
			continue
		}

		totalRecommendations += len(result.Recommendations)

		ls.logger.WithFields(logrus.Fields{
			"symbol":          symbol,
			"executions":      result.TotalExecutions,
			"recommendations": len(result.Recommendations),
			"confidence":      result.OverallConfidence,
		}).Info("Symbol optimization completed")
	}

	ls.logger.WithFields(logrus.Fields{
		"symbols_analyzed":      len(symbols),
		"total_recommendations": totalRecommendations,
	}).Info("Weekly optimization completed")

	return nil
}

// GetDefaultLearningConfig returns default learning configuration
func GetDefaultLearningConfig() *LearningConfig {
	return &LearningConfig{
		AnalysisWindow:             30 * 24 * time.Hour, // 30 days
		MinExecutionsForML:         20,                  // Need at least 20 executions
		PrematureStopThreshold:     5.0,                 // 5% price reversal
		PrematureStopTimeWindow:    24 * time.Hour,      // Check 24 hours after execution
		OptimalSLRangeMin:          1.0,                 // Min 1% SL distance
		OptimalSLRangeMax:          15.0,                // Max 15% SL distance
		OptimalSLStepSize:          0.5,                 // 0.5% step size
		MinConfidenceForSuggestion: 0.6,                 // 60% confidence threshold
		LearningRate:               0.5,                 // 50% of suggested adjustment
	}
}

// LLMEnhancedAnalysis represents trade analysis enhanced with LLM insights
type LLMEnhancedAnalysis struct {
	ExecutionAnalysis *ExecutionAnalysis `json:"execution_analysis"`
	LLMInsights       *LLMTradeInsights  `json:"llm_insights"`
	CombinedScore     float64            `json:"combined_score"`
	Timestamp         time.Time          `json:"timestamp"`
}

// LLMTradeInsights represents insights generated by LLM
type LLMTradeInsights struct {
	FailureReason     string   `json:"failure_reason"`
	MarketConditions  string   `json:"market_conditions"`
	KeyFactors        []string `json:"key_factors"`
	Recommendations   []string `json:"recommendations"`
	ShouldAdjustSL    bool     `json:"should_adjust_sl"`
	ShouldAdjustTP    bool     `json:"should_adjust_tp"`
	SuggestedSLChange float64  `json:"suggested_sl_change"`
	SuggestedTPChange float64  `json:"suggested_tp_change"`
	ConfidenceScore   float64  `json:"confidence_score"`
	LearningPriority  string   `json:"learning_priority"`
}

// AnalyzeExecutionWithLLM analyzes a trade execution using LLM for deeper insights
// This method requires an LLM analyzer to be passed in to avoid import cycles
func (ls *SLTPLearningSystem) AnalyzeExecutionWithLLM(
	ctx context.Context,
	exec *domain.ExecutionHistory,
	llmCall func(ctx context.Context, prompt string) (string, error),
) (*LLMEnhancedAnalysis, error) {
	// First do traditional analysis
	tradAnalysis, err := ls.analyzeExecution(ctx, exec)
	if err != nil {
		return nil, fmt.Errorf("traditional analysis failed: %w", err)
	}

	// Build prompt for LLM
	prompt := ls.buildTradeAnalysisPrompt(exec, tradAnalysis)

	// Call LLM
	llmResponse, err := llmCall(ctx, prompt)
	if err != nil {
		ls.logger.WithError(err).Warn("LLM analysis failed, using traditional analysis only")
		return &LLMEnhancedAnalysis{
			ExecutionAnalysis: tradAnalysis,
			LLMInsights:       nil,
			CombinedScore:     tradAnalysis.PnL,
			Timestamp:         time.Now(),
		}, nil
	}

	// Parse LLM response
	insights := ls.parseLLMTradeInsights(llmResponse)

	// Combine traditional and LLM analysis
	combinedScore := ls.calculateCombinedScore(tradAnalysis, insights)

	enhanced := &LLMEnhancedAnalysis{
		ExecutionAnalysis: tradAnalysis,
		LLMInsights:       insights,
		CombinedScore:     combinedScore,
		Timestamp:         time.Now(),
	}

	ls.logger.WithFields(logrus.Fields{
		"execution_id":      exec.ID,
		"was_premature":     tradAnalysis.WasPremature,
		"llm_confidence":    insights.ConfidenceScore,
		"learning_priority": insights.LearningPriority,
	}).Info("LLM-enhanced trade analysis completed")

	return enhanced, nil
}

// buildTradeAnalysisPrompt builds a prompt for LLM trade analysis
func (ls *SLTPLearningSystem) buildTradeAnalysisPrompt(
	exec *domain.ExecutionHistory,
	analysis *ExecutionAnalysis,
) string {
	prompt := fmt.Sprintf(`Analyze this cryptocurrency trade execution and provide insights:

**Trade Details:**
- Symbol: %s
- Type: %s
- Trigger Price: $%.4f
- Execution Price: $%.4f
- Size: %.4f
- P&L: $%.2f
- Executed At: %s

**Analysis Results:**
- Was Premature: %v
- Actual SL Distance: %.2f%%
- Optimal SL Distance: %.2f%%

Provide a JSON response with:
{
  "failure_reason": "Brief explanation",
  "market_conditions": "Description",
  "key_factors": ["factor1", "factor2"],
  "recommendations": ["rec1", "rec2"],
  "should_adjust_sl": true/false,
  "should_adjust_tp": true/false,
  "suggested_sl_change": 0.5,
  "suggested_tp_change": 0.0,
  "confidence_score": 0.85,
  "learning_priority": "high"
}`,
		exec.Symbol,
		exec.Type,
		exec.TriggerPrice.InexactFloat64(),
		exec.ExecutionPrice.InexactFloat64(),
		exec.Size.InexactFloat64(),
		exec.PnL.InexactFloat64(),
		exec.ExecutedAt.Format("2006-01-02 15:04:05"),
		analysis.WasPremature,
		analysis.ActualSLDistance,
		analysis.OptimalSLDistance,
	)

	if analysis.WasPremature {
		prompt += fmt.Sprintf("\n- Price Reversal: %.2f%%\n- Time to Reversal: %s",
			analysis.PriceReversal, analysis.TimeToReversal)
	}

	return prompt
}

// parseLLMTradeInsights parses LLM response into structured insights
func (ls *SLTPLearningSystem) parseLLMTradeInsights(content string) *LLMTradeInsights {
	// Simplified parser - returns basic insights
	insights := &LLMTradeInsights{
		FailureReason:     "Analysis completed",
		MarketConditions:  "Normal",
		KeyFactors:        []string{},
		Recommendations:   []string{},
		ShouldAdjustSL:    false,
		ShouldAdjustTP:    false,
		SuggestedSLChange: 0.0,
		SuggestedTPChange: 0.0,
		ConfidenceScore:   0.7,
		LearningPriority:  "medium",
	}

	// Basic pattern matching
	contentLower := strings.ToLower(content)

	if strings.Contains(contentLower, "premature") {
		insights.FailureReason = "Premature stop loss triggered"
		insights.ShouldAdjustSL = true
		insights.SuggestedSLChange = 0.5
		insights.LearningPriority = "high"
		insights.Recommendations = append(insights.Recommendations, "Consider widening stop loss")
	}

	if strings.Contains(contentLower, "volatile") || strings.Contains(contentLower, "volatility") {
		insights.MarketConditions = "High volatility detected"
		insights.KeyFactors = append(insights.KeyFactors, "Market volatility")
	}

	if strings.Contains(contentLower, "trend") {
		insights.KeyFactors = append(insights.KeyFactors, "Trend analysis")
	}

	return insights
}

// calculateCombinedScore combines traditional and LLM analysis
func (ls *SLTPLearningSystem) calculateCombinedScore(
	tradAnalysis *ExecutionAnalysis,
	llmInsights *LLMTradeInsights,
) float64 {
	score := tradAnalysis.PnL

	if llmInsights != nil {
		confidenceWeight := llmInsights.ConfidenceScore

		if llmInsights.ShouldAdjustSL || llmInsights.ShouldAdjustTP {
			score *= (1.0 - (0.2 * confidenceWeight))
		}

		switch llmInsights.LearningPriority {
		case "high":
			score *= 1.2
		case "low":
			score *= 0.8
		}
	}

	return score
}

// GenerateEnhancedRecommendations generates recommendations using LLM insights
func (ls *SLTPLearningSystem) GenerateEnhancedRecommendations(
	analyses []*LLMEnhancedAnalysis,
) []OptimizationRecommendation {
	if len(analyses) == 0 {
		return nil
	}

	prematureStops := 0
	totalSLAdjustments := 0.0
	highPriorityCount := 0

	for _, analysis := range analyses {
		if analysis.ExecutionAnalysis.WasPremature {
			prematureStops++
		}

		if analysis.LLMInsights != nil {
			if analysis.LLMInsights.ShouldAdjustSL {
				totalSLAdjustments += analysis.LLMInsights.SuggestedSLChange
			}
			if analysis.LLMInsights.LearningPriority == "high" {
				highPriorityCount++
			}
		}
	}

	recommendations := []OptimizationRecommendation{}

	// Generate SL recommendation if needed
	if prematureStops > len(analyses)/3 {
		avgSLAdjustment := totalSLAdjustments / float64(len(analyses))

		rec := OptimizationRecommendation{
			Type:             "stop_loss_adjustment",
			Symbol:           analyses[0].ExecutionAnalysis.Symbol,
			CurrentValue:     analyses[0].ExecutionAnalysis.ActualSLDistance,
			RecommendedValue: analyses[0].ExecutionAnalysis.ActualSLDistance + avgSLAdjustment,
			Adjustment:       avgSLAdjustment,
			Reason: fmt.Sprintf("LLM analysis detected %d premature stops out of %d executions (%.1f%%). "+
				"Widening stop loss by %.2f%% may reduce premature exits.",
				prematureStops, len(analyses), float64(prematureStops)/float64(len(analyses))*100, avgSLAdjustment),
			Confidence:          float64(highPriorityCount) / float64(len(analyses)),
			ExpectedImprovement: fmt.Sprintf("Reduce premature stops by ~%.0f%%", float64(prematureStops)/float64(len(analyses))*50),
		}
		recommendations = append(recommendations, rec)
	}

	return recommendations
}

// GenerateOptimizationRecommendationsWithLLM generates detailed optimization recommendations using LLM
func (ls *SLTPLearningSystem) GenerateOptimizationRecommendationsWithLLM(
	ctx context.Context,
	patterns []PatternAnalysis,
	llmCallFunc func(ctx context.Context, prompt string) (string, error),
) ([]OptimizationRecommendation, error) {
	if llmCallFunc == nil {
		// Fallback to traditional recommendations
		return ls.generateRecommendations(patterns), nil
	}

	// Build comprehensive prompt for LLM analysis
	prompt := ls.buildOptimizationPrompt(patterns)

	// Get LLM analysis
	response, err := llmCallFunc(ctx, prompt)
	if err != nil {
		ls.logger.WithError(err).Warn("Failed to get LLM optimization recommendations, using traditional analysis")
		return ls.generateRecommendations(patterns), nil
	}

	// Parse LLM response and enhance traditional recommendations
	llmRecommendations := ls.parseLLMOptimizationResponse(response)
	traditionalRecommendations := ls.generateRecommendations(patterns)

	// Combine and enhance recommendations
	enhancedRecommendations := ls.combineRecommendations(traditionalRecommendations, llmRecommendations, response)

	ls.logger.WithFields(map[string]interface{}{
		"traditional_count": len(traditionalRecommendations),
		"llm_enhanced":      len(enhancedRecommendations),
		"patterns":          len(patterns),
	}).Info("Generated LLM-enhanced optimization recommendations")

	return enhancedRecommendations, nil
}

// buildOptimizationPrompt builds a comprehensive prompt for LLM optimization analysis
func (ls *SLTPLearningSystem) buildOptimizationPrompt(patterns []PatternAnalysis) string {
	prompt := `Analyze the following trading performance patterns and provide optimization recommendations:

PERFORMANCE ANALYSIS:
`

	for i, pattern := range patterns {
		prompt += fmt.Sprintf(`
Pattern %d - %s:
- Total Executions: %d
- Premature Stop Rate: %.1f%%
- Average SL Distance: %.2f%%
- Average TP Distance: %.2f%%
- Win Rate: %.1f%%
- Average P&L: $%.2f
- Confidence: %.1f%%
`,
			i+1,
			pattern.Symbol,
			pattern.TotalExecutions,
			pattern.PrematureStopRate*100,
			pattern.CurrentAverageSL*100,
			pattern.SuggestedSLAdjustment*100,
			0.0, // Placeholder for win rate
			0.0, // Placeholder for avg PnL
			pattern.Confidence*100,
		)
	}

	prompt += `
ANALYSIS REQUIREMENTS:
1. Identify the main performance issues for each symbol
2. Recommend specific parameter adjustments (SL/TP distances, position sizing)
3. Provide detailed reasoning for each recommendation
4. Estimate expected improvement from each change
5. Prioritize recommendations by impact and confidence

RESPONSE FORMAT:
For each symbol, provide:
- ISSUE: Main problem identified
- RECOMMENDATION: Specific parameter change
- REASONING: Why this change will help
- EXPECTED_IMPACT: Quantified improvement estimate
- CONFIDENCE: High/Medium/Low
- PRIORITY: 1-5 (1=highest)

Be specific with numbers and percentages. Focus on actionable, implementable changes.`

	return prompt
}

// parseLLMOptimizationResponse parses LLM response into structured recommendations
func (ls *SLTPLearningSystem) parseLLMOptimizationResponse(response string) []LLMOptimizationRecommendation {
	recommendations := []LLMOptimizationRecommendation{}

	// Split response into sections (simplified parser)
	sections := strings.Split(response, "\n\n")

	for _, section := range sections {
		if strings.Contains(strings.ToUpper(section), "RECOMMENDATION") {
			rec := ls.parseRecommendationSection(section)
			if rec != nil {
				recommendations = append(recommendations, *rec)
			}
		}
	}

	return recommendations
}

// parseRecommendationSection parses a single recommendation section
func (ls *SLTPLearningSystem) parseRecommendationSection(section string) *LLMOptimizationRecommendation {
	lines := strings.Split(section, "\n")
	rec := &LLMOptimizationRecommendation{}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		lineLower := strings.ToLower(line)

		if strings.Contains(lineLower, "issue:") {
			rec.Issue = strings.TrimSpace(strings.Split(line, ":")[1])
		} else if strings.Contains(lineLower, "recommendation:") {
			rec.Recommendation = strings.TrimSpace(strings.Split(line, ":")[1])
		} else if strings.Contains(lineLower, "reasoning:") {
			rec.Reasoning = strings.TrimSpace(strings.Split(line, ":")[1])
		} else if strings.Contains(lineLower, "expected_impact:") {
			rec.ExpectedImpact = strings.TrimSpace(strings.Split(line, ":")[1])
		} else if strings.Contains(lineLower, "confidence:") {
			confidenceStr := strings.TrimSpace(strings.Split(line, ":")[1])
			switch strings.ToLower(confidenceStr) {
			case "high":
				rec.Confidence = 0.8
			case "medium":
				rec.Confidence = 0.6
			case "low":
				rec.Confidence = 0.4
			default:
				rec.Confidence = 0.5
			}
		} else if strings.Contains(lineLower, "priority:") {
			priorityStr := strings.TrimSpace(strings.Split(line, ":")[1])
			if priority, err := strconv.Atoi(priorityStr); err == nil {
				rec.Priority = priority
			} else {
				rec.Priority = 3 // Default medium priority
			}
		}
	}

	// Only return if we have essential fields
	if rec.Recommendation != "" && rec.Reasoning != "" {
		return rec
	}

	return nil
}

// combineRecommendations combines traditional and LLM recommendations
func (ls *SLTPLearningSystem) combineRecommendations(
	traditional []OptimizationRecommendation,
	llmRecs []LLMOptimizationRecommendation,
	fullLLMResponse string,
) []OptimizationRecommendation {
	enhanced := make([]OptimizationRecommendation, 0)

	// Enhance traditional recommendations with LLM insights
	for _, tradRec := range traditional {
		enhancedRec := tradRec

		// Find matching LLM recommendation
		for _, llmRec := range llmRecs {
			if ls.isMatchingRecommendation(tradRec, llmRec) {
				// Enhance with LLM insights
				enhancedRec.Reason = fmt.Sprintf("%s\n\nAI Analysis: %s", tradRec.Reason, llmRec.Reasoning)
				enhancedRec.ExpectedImprovement = llmRec.ExpectedImpact
				enhancedRec.Confidence = (tradRec.Confidence + llmRec.Confidence) / 2 // Average confidence

				// Add LLM-specific data
				if enhancedRec.Metadata == nil {
					enhancedRec.Metadata = make(map[string]interface{})
				}
				enhancedRec.Metadata["llm_issue"] = llmRec.Issue
				enhancedRec.Metadata["llm_priority"] = llmRec.Priority
				enhancedRec.Metadata["llm_enhanced"] = true
				break
			}
		}

		enhanced = append(enhanced, enhancedRec)
	}

	// Add LLM-only recommendations that don't match traditional ones
	for _, llmRec := range llmRecs {
		if !ls.hasMatchingTraditionalRec(llmRec, traditional) {
			newRec := OptimizationRecommendation{
				Type:                "llm_insight",
				Symbol:              "ALL", // LLM recommendations might be general
				CurrentValue:        0,
				RecommendedValue:    0,
				Adjustment:          0,
				Reason:              fmt.Sprintf("AI Insight: %s\nRecommendation: %s", llmRec.Issue, llmRec.Reasoning),
				Confidence:          llmRec.Confidence,
				ExpectedImprovement: llmRec.ExpectedImpact,
				Metadata: map[string]interface{}{
					"llm_only":           true,
					"llm_priority":       llmRec.Priority,
					"llm_recommendation": llmRec.Recommendation,
				},
			}
			enhanced = append(enhanced, newRec)
		}
	}

	// Sort by priority (LLM priority if available, otherwise by confidence)
	sort.Slice(enhanced, func(i, j int) bool {
		iPriority := 3 // Default priority
		jPriority := 3

		if enhanced[i].Metadata != nil {
			if p, ok := enhanced[i].Metadata["llm_priority"].(int); ok {
				iPriority = p
			}
		}

		if enhanced[j].Metadata != nil {
			if p, ok := enhanced[j].Metadata["llm_priority"].(int); ok {
				jPriority = p
			}
		}

		// Lower priority number = higher priority
		if iPriority != jPriority {
			return iPriority < jPriority
		}

		// If same priority, sort by confidence
		return enhanced[i].Confidence > enhanced[j].Confidence
	})

	return enhanced
}

// isMatchingRecommendation checks if traditional and LLM recommendations are related
func (ls *SLTPLearningSystem) isMatchingRecommendation(
	tradRec OptimizationRecommendation,
	llmRec LLMOptimizationRecommendation,
) bool {
	// Simple matching based on recommendation type and content
	recLower := strings.ToLower(llmRec.Recommendation)

	switch tradRec.Type {
	case "stop_loss_adjustment":
		return strings.Contains(recLower, "stop") || strings.Contains(recLower, "sl") || strings.Contains(recLower, "loss")
	case "take_profit_adjustment":
		return strings.Contains(recLower, "profit") || strings.Contains(recLower, "tp") || strings.Contains(recLower, "target")
	case "position_size_adjustment":
		return strings.Contains(recLower, "size") || strings.Contains(recLower, "position") || strings.Contains(recLower, "risk")
	default:
		return false
	}
}

// hasMatchingTraditionalRec checks if LLM recommendation has a matching traditional one
func (ls *SLTPLearningSystem) hasMatchingTraditionalRec(
	llmRec LLMOptimizationRecommendation,
	traditional []OptimizationRecommendation,
) bool {
	for _, tradRec := range traditional {
		if ls.isMatchingRecommendation(tradRec, llmRec) {
			return true
		}
	}
	return false
}

// LLMOptimizationRecommendation represents a recommendation from LLM analysis
type LLMOptimizationRecommendation struct {
	Issue          string  `json:"issue"`
	Recommendation string  `json:"recommendation"`
	Reasoning      string  `json:"reasoning"`
	ExpectedImpact string  `json:"expected_impact"`
	Confidence     float64 `json:"confidence"`
	Priority       int     `json:"priority"`
}
