package knowledge

import (
	"time"
)

// AnalysisType represents the type of market analysis being performed
type AnalysisType string

const (
	AnalysisTypeMarket         AnalysisType = "market_analysis"
	AnalysisTypeSetup          AnalysisType = "setup_identification"
	AnalysisTypeRisk           AnalysisType = "risk_assessment"
	AnalysisTypeValidation     AnalysisType = "trade_validation"
	AnalysisTypePositionSizing AnalysisType = "position_sizing"
	AnalysisTypeOrderFlow      AnalysisType = "order_flow"
	AnalysisTypeDerivatives    AnalysisType = "derivatives"
	AnalysisTypeOnChain        AnalysisType = "on_chain"
)

// KnowledgeSection represents a section of trading knowledge
type KnowledgeSection struct {
	ID          string
	Title       string
	Content     string
	Category    string
	Tags        []string
	LastUpdated time.Time
}

// SteeringFile represents a Kiro steering file configuration
type SteeringFile struct {
	Path       string
	Inclusion  string // "always", "manual", "fileMatch"
	Pattern    string // For fileMatch inclusion
	Content    string
	References []string // File references using #[[file:path]]
}

// TrainingDocument represents a training documentation file
type TrainingDocument struct {
	Path        string
	Title       string
	Sections    map[string]*KnowledgeSection
	LastUpdated time.Time
}

// ContextRequest represents a request for knowledge context
type ContextRequest struct {
	AnalysisType AnalysisType
	Pair         string
	Timeframe    string
	TradingStyle string // "scalping", "day_trading", "swing_trading"
	Additional   map[string]interface{}
}

// ContextResponse contains the built context for AI analysis
type ContextResponse struct {
	Context     string
	Sections    []string
	Sources     []string
	GeneratedAt time.Time
}
