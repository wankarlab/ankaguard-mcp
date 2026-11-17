package services

import (
	"context"
	"fmt"
	"time"

	"github.com/rantipay/trading-mcp/internal/ai"
	"github.com/sirupsen/logrus"
)

// AIAnalysisService provides AI-powered market analysis
type AIAnalysisService struct {
	contextBuilder *ai.ContextBuilder
	decisionEngine *ai.DecisionEngine
	log            *logrus.Logger
}

// AnalysisRequest defines parameters for analysis
type AnalysisRequest struct {
	Symbol     string        `json:"symbol"`
	Timeframes []string      `json:"timeframes"`
	Position   *PositionInfo `json:"position,omitempty"`
	Depth      string        `json:"depth"`
}

// PositionInfo contains position details
type PositionInfo struct {
	ID         string  `json:"id"`
	Side       string  `json:"side"`
	EntryPrice float64 `json:"entry_price"`
	Size       float64 `json:"size"`
	Leverage   int     `json:"leverage"`
}

// AnalysisResponse contains the complete analysis
type AnalysisResponse struct {
	Symbol           string                  `json:"symbol"`
	Timestamp        time.Time               `json:"timestamp"`
	CurrentPrice     float64                 `json:"current_price"`
	MarketCondition  string                  `json:"market_condition"`
	Trend            string                  `json:"trend"`
	Strength         float64                 `json:"strength"`
	Indicators       map[string]interface{}  `json:"indicators"`
	Recommendation   string                  `json:"recommendation"`
	Confidence       float64                 `json:"confidence"`
	Reasoning        []string                `json:"reasoning"`
	PositionAnalysis *PositionAnalysisResult `json:"position_analysis,omitempty"`
	Scenarios        []Scenario              `json:"scenarios"`
	SuggestedActions []Action                `json:"suggested_actions"`
}

// PositionAnalysisResult contains position-specific analysis
type PositionAnalysisResult struct {
	CurrentPnL       float64 `json:"current_pnl"`
	CurrentPnLPct    float64 `json:"current_pnl_pct"`
	MaxPotentialGain float64 `json:"max_potential_gain"`
	MaxPotentialLoss float64 `json:"max_potential_loss"`
	RiskReward       float64 `json:"risk_reward"`
	TimeInPosition   string  `json:"time_in_position"`
	Status           string  `json:"status"`
}

// Scenario represents a possible market scenario
type Scenario struct {
	Name        string  `json:"name"`
	Probability float64 `json:"probability"`
	TargetPrice float64 `json:"target_price"`
	Timeframe   string  `json:"timeframe"`
	Impact      string  `json:"impact"`
}

// Action represents a suggested action
type Action struct {
	Type        string  `json:"type"`
	Priority    string  `json:"priority"`
	Description string  `json:"description"`
	Price       float64 `json:"price,omitempty"`
	Percentage  float64 `json:"percentage,omitempty"`
}

// NewAIAnalysisService creates a new AI analysis service
func NewAIAnalysisService(
	contextBuilder *ai.ContextBuilder,
	decisionEngine *ai.DecisionEngine,
	log *logrus.Logger,
) *AIAnalysisService {
	return &AIAnalysisService{
		contextBuilder: contextBuilder,
		decisionEngine: decisionEngine,
		log:            log,
	}
}

// AnalyzePosition performs comprehensive AI analysis
func (s *AIAnalysisService) AnalyzePosition(ctx context.Context, req *AnalysisRequest) (*AnalysisResponse, error) {
	s.log.WithFields(logrus.Fields{
		"symbol":     req.Symbol,
		"timeframes": req.Timeframes,
	}).Info("Starting AI analysis")

	// Build market context
	marketCtx, err := s.contextBuilder.BuildContext(ctx, req.Symbol)
	if err != nil {
		return nil, fmt.Errorf("failed to build market context: %w", err)
	}

	// Get AI decision
	decision, err := s.decisionEngine.EvaluateOpportunity(ctx, marketCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate opportunity: %w", err)
	}

	// Build response
	response := &AnalysisResponse{
		Symbol:           req.Symbol,
		Timestamp:        time.Now(),
		CurrentPrice:     marketCtx.CurrentPrice,
		Indicators:       s.extractIndicators(marketCtx),
		Reasoning:        []string{decision.Reason},
		Scenarios:        s.generateScenarios(marketCtx.CurrentPrice, decision),
		SuggestedActions: make([]Action, 0),
	}

	// Analyze market condition
	response.MarketCondition, response.Trend, response.Strength = s.analyzeMarketCondition(marketCtx)
	response.Recommendation = decision.Action
	response.Confidence = decision.Confidence

	// Analyze position if provided
	if req.Position != nil {
		response.PositionAnalysis = s.analyzePositionDetails(req.Position, marketCtx.CurrentPrice)
		response.SuggestedActions = s.generateActions(req, response)
	}

	return response, nil
}

// extractIndicators extracts indicators from market context
func (s *AIAnalysisService) extractIndicators(ctx *ai.MarketContext) map[string]interface{} {
	indicators := make(map[string]interface{})

	// Extract RSI from all available timeframes
	rsiData := make(map[string]float64)
	for tf, tfCtx := range ctx.Timeframes {
		if tfCtx != nil && tfCtx.Indicators != nil {
			rsiData[tf] = tfCtx.Indicators.RSI
		}
	}
	indicators["rsi"] = rsiData

	// Extract MACD from primary timeframe
	primaryCtx := ctx.GetPrimaryContext()
	if primaryCtx != nil && primaryCtx.Indicators != nil && primaryCtx.Indicators.MACD != nil {
		indicators["macd"] = map[string]interface{}{
			"value":     primaryCtx.Indicators.MACD.Value,
			"histogram": primaryCtx.Indicators.MACD.Histogram,
			"trend":     primaryCtx.Indicators.MACD.Trend,
		}
	}

	return indicators
}

// analyzeMarketCondition determines market condition
func (s *AIAnalysisService) analyzeMarketCondition(ctx *ai.MarketContext) (string, string, float64) {
	// Use RSI from primary timeframe
	primaryCtx := ctx.GetPrimaryContext()
	if primaryCtx == nil || primaryCtx.Indicators == nil {
		return "neutral", "sideways", 0.5
	}

	rsi := primaryCtx.Indicators.RSI

	if rsi < 30 {
		return "oversold", "bullish_reversal", (30 - rsi) / 30
	} else if rsi > 70 {
		return "overbought", "bearish_reversal", (rsi - 70) / 30
	} else if rsi < 45 {
		return "bearish", "bearish", 0.5
	} else if rsi > 55 {
		return "bullish", "bullish", 0.5
	}

	return "neutral", "sideways", 0.3
}

// analyzePositionDetails analyzes position-specific metrics
func (s *AIAnalysisService) analyzePositionDetails(pos *PositionInfo, currentPrice float64) *PositionAnalysisResult {
	var pnl, pnlPct float64

	if pos.Side == "buy" {
		pnl = (currentPrice - pos.EntryPrice) * pos.Size * float64(pos.Leverage)
		pnlPct = ((currentPrice - pos.EntryPrice) / pos.EntryPrice) * 100
	} else {
		pnl = (pos.EntryPrice - currentPrice) * pos.Size * float64(pos.Leverage)
		pnlPct = ((pos.EntryPrice - currentPrice) / pos.EntryPrice) * 100
	}

	status := "breakeven"
	if pnl > 0 {
		status = "winning"
	} else if pnl < 0 {
		status = "losing"
	}

	return &PositionAnalysisResult{
		CurrentPnL:       pnl,
		CurrentPnLPct:    pnlPct,
		MaxPotentialGain: pnl * 2,
		MaxPotentialLoss: pnl * 0.5,
		RiskReward:       2.0,
		TimeInPosition:   "calculating...",
		Status:           status,
	}
}

// generateScenarios generates possible market scenarios
func (s *AIAnalysisService) generateScenarios(currentPrice float64, decision *ai.Decision) []Scenario {
	scenarios := make([]Scenario, 0)

	if decision.Action == "BUY" || decision.Action == "HOLD" {
		scenarios = append(scenarios, Scenario{
			Name:        "Rebote Alcista",
			Probability: 0.70,
			TargetPrice: currentPrice * 1.02,
			Timeframe:   "1-2 horas",
			Impact:      "positive",
		})
	}

	scenarios = append(scenarios, Scenario{
		Name:        "Consolidación",
		Probability: 0.20,
		TargetPrice: currentPrice,
		Timeframe:   "2-4 horas",
		Impact:      "neutral",
	})

	scenarios = append(scenarios, Scenario{
		Name:        "Corrección",
		Probability: 0.10,
		TargetPrice: currentPrice * 0.98,
		Timeframe:   "1-2 horas",
		Impact:      "negative",
	})

	return scenarios
}

// generateActions generates suggested actions
func (s *AIAnalysisService) generateActions(req *AnalysisRequest, response *AnalysisResponse) []Action {
	actions := make([]Action, 0)

	if req.Position != nil && response.PositionAnalysis != nil {
		if response.PositionAnalysis.CurrentPnLPct > 1.5 {
			actions = append(actions, Action{
				Type:        "take_profit",
				Priority:    "high",
				Description: "Tomar ganancias parciales (50%)",
				Percentage:  50,
			})
		}

		if response.Recommendation == "HOLD" {
			actions = append(actions, Action{
				Type:        "hold",
				Priority:    "medium",
				Description: "Mantener posición - Condiciones favorables",
			})
		}

		stopLossPrice := req.Position.EntryPrice * 0.99
		if req.Position.Side == "sell" {
			stopLossPrice = req.Position.EntryPrice * 1.01
		}
		actions = append(actions, Action{
			Type:        "stop_loss",
			Priority:    "high",
			Description: "Proteger con stop loss",
			Price:       stopLossPrice,
		})
	}

	return actions
}
