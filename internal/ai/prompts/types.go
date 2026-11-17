package prompts

import (
	"time"
)

// Template represents a prompt template
type Template struct {
	Name            string
	Category        string
	Priority        string
	TemplateText    string
	Variables       []string
	ContextSections []string
	Metadata        map[string]interface{}
}

// Prompt represents a generated prompt ready for AI
type Prompt struct {
	ID          string
	Template    string
	Content     string
	Variables   map[string]interface{}
	Context     string
	GeneratedAt time.Time
}

// WorkflowStep represents a single step in a workflow
type WorkflowStep struct {
	ID             string
	PromptTemplate string
	Required       bool
	Condition      string
	DependsOn      []string
	Timeout        time.Duration
}

// Workflow represents a multi-step analysis workflow
type Workflow struct {
	Name        string
	Description string
	Steps       []WorkflowStep
	MaxRetries  int
	RetryDelay  time.Duration
}

// WorkflowResult contains the results of workflow execution
type WorkflowResult struct {
	WorkflowName string
	Steps        []StepResult
	Success      bool
	Error        error
	StartedAt    time.Time
	CompletedAt  time.Time
}

// StepResult contains the result of a single workflow step
type StepResult struct {
	StepID   string
	Success  bool
	Response interface{}
	Error    error
	Duration time.Duration
}

// MarketData contains market information for prompt injection
type MarketData struct {
	Pair      string
	Price     float64
	Timestamp time.Time

	// Order Flow
	CVD1m          float64
	CVD5m          float64
	DeltaImbalance float64

	// Order Book
	BidVolume10   float64
	AskVolume10   float64
	BookImbalance float64

	// Derivatives
	FundingRate    float64
	OpenInterest   float64
	OIChange1h     float64
	Liquidations1h float64

	// Technical Indicators
	RSI5m         float64
	VWAP          float64
	VWAPDeviation float64
	ATR           float64

	// Additional data
	Additional map[string]interface{}
}
