// Package launchapi holds the read/write queries behind the AnkaLaunch phase-1
// console: the REST handlers (internal/api/v1/handlers/launch_*.go) and the
// read-only MCP tools (internal/mcp-trading/tools/launch) share these
// functions so both surfaces return identical objects. The response shapes
// are the contract in docs/bot/launch-api-phase1.md.
package launchapi

import (
	"encoding/json"
	"time"

	"github.com/rantipay/trading-mcp/internal/launch/core"
)

// HeartbeatStaleAfter is how old the launch-bot heartbeat may be before the
// service is reported as down.
const HeartbeatStaleAfter = 90 * time.Second

// Default gate requirements, used when the dynamic config is not available.
const (
	DefaultMinTrades      = 30
	DefaultMinShadowWeeks = 4
	DefaultMaxFraudBuys   = 0
)

// GateRequirements are the thresholds the gate verdict is judged against.
type GateRequirements struct {
	MinTrades      int `json:"min_trades"`
	MinShadowWeeks int `json:"min_shadow_weeks"`
	MaxFraudBuys   int `json:"max_fraud_buys"`
}

// DefaultRequirements returns the manifest defaults.
func DefaultRequirements() GateRequirements {
	return GateRequirements{MinTrades: DefaultMinTrades, MinShadowWeeks: DefaultMinShadowWeeks, MaxFraudBuys: DefaultMaxFraudBuys}
}

// RequirementsFunc resolves the current requirements (live config).
type RequirementsFunc func() GateRequirements

// GateEvaluation is one row of launch.gate_evaluations.
type GateEvaluation struct {
	ID                int64            `json:"id"`
	StrategyVersionID *int64           `json:"strategy_version_id"`
	Mode              string           `json:"mode"`
	Verdict           string           `json:"verdict"`
	Trades            int              `json:"trades"`
	ShadowWeeks       float64          `json:"shadow_weeks"`
	NetPnLUSD         float64          `json:"net_pnl_usd"`
	MeanPnLUSD        *float64         `json:"mean_pnl_usd"`
	CILow             *float64         `json:"ci_low"`
	CIHigh            *float64         `json:"ci_high"`
	CILevel           float64          `json:"ci_level"`
	ProfitFactor      *float64         `json:"profit_factor"`
	MaxDrawdownUSD    *float64         `json:"max_drawdown_usd"`
	FraudBuys         int              `json:"fraud_buys"`
	Requirements      GateRequirements `json:"requirements"`
	Detail            string           `json:"detail"`
	EvaluatedAt       time.Time        `json:"evaluated_at"`
}

// Breaker is the global circuit breaker state.
type Breaker struct {
	State  string     `json:"state"` // running | killed
	Since  *time.Time `json:"since"`
	Actor  string     `json:"actor"`
	Reason string     `json:"reason"`
}

// SourceStatus is the health of one discovery source.
type SourceStatus struct {
	Name         string     `json:"name"`
	Role         string     `json:"role"`
	Connected    bool       `json:"connected"`
	Active       bool       `json:"active"`
	LastEventAt  *time.Time `json:"last_event_at"`
	EventsPerMin float64    `json:"events_per_min"`
	Reconnects   int        `json:"reconnects"`
	RateLimited  int        `json:"rate_limited"`
	QuotaUsedPct float64    `json:"quota_used_pct"`
}

// ShadowSummary aggregates shadow positions (counterfactual excluded).
type ShadowSummary struct {
	OpenPositions   int     `json:"open_positions"`
	ExposureUSD     float64 `json:"exposure_usd"`
	ClosedPositions int     `json:"closed_positions"`
	NetPnLUSD       float64 `json:"net_pnl_usd"`
	GrossPnLUSD     float64 `json:"gross_pnl_usd"`
}

// Counts24h are discovery counters of the last 24 hours.
type Counts24h struct {
	Events        int            `json:"events"`
	Opportunities int            `json:"opportunities"`
	Rejected      int            `json:"rejected"`
	ByReason      map[string]int `json:"by_reason"`
	ByBand        map[string]int `json:"by_band"`
}

// Status is the GET /status payload.
type Status struct {
	Service       string            `json:"service"` // up | down | never_started
	LastHeartbeat *time.Time        `json:"last_heartbeat"`
	Instance      *string           `json:"instance"`
	EntriesPaused bool              `json:"entries_paused"`
	PauseReason   *string           `json:"pause_reason"`
	Breaker       Breaker           `json:"breaker"`
	Sources       []SourceStatus    `json:"sources"`
	NetworkState  map[string]string `json:"network_state"`
	Gate          *GateEvaluation   `json:"gate"`
	Shadow        ShadowSummary     `json:"shadow"`
	Counts24h     Counts24h         `json:"counts_24h"`
	RealMode      bool              `json:"real_mode"`
}

// DecisionView is the human decision attached to an opportunity.
type DecisionView struct {
	Action    string    `json:"action"`
	Category  *string   `json:"category"`
	Comment   *string   `json:"comment"`
	Actor     string    `json:"actor"`
	CreatedAt time.Time `json:"created_at"`
}

// Thesis is the for/against reasoning attached to an opportunity.
type Thesis struct {
	For     []string `json:"for"`
	Against []string `json:"against"`
}

// Opportunity is the console view of launch.opportunities.
type Opportunity struct {
	ID           int64              `json:"id"`
	Mint         string             `json:"mint"`
	Symbol       string             `json:"symbol"`
	Pool         string             `json:"pool"`
	Dex          string             `json:"dex"`
	SourceKind   string             `json:"source_kind"`
	Graduated    bool               `json:"graduated"`
	ScreenResult string             `json:"screen_result"`
	RejectReason *string            `json:"reject_reason"`
	Score        *float64           `json:"score"`
	Band         *string            `json:"band"`
	Status       string             `json:"status"`
	ExpiresAt    *time.Time         `json:"expires_at"`
	CreatedAt    time.Time          `json:"created_at"`
	Breakdown    []core.ScoreFactor `json:"breakdown"`
	Snapshot     json.RawMessage    `json:"snapshot"`
	Thesis       Thesis             `json:"thesis"`
	Decision     *DecisionView      `json:"decision"`
	PositionID   *int64             `json:"position_id"`
}

// OpportunityFilter filters ListOpportunities.
type OpportunityFilter struct {
	Band     string
	Status   string
	Limit    int
	BeforeID int64
}

// OpportunityPage is a page of opportunities.
type OpportunityPage struct {
	Items        []Opportunity `json:"items"`
	NextBeforeID *int64        `json:"next_before_id"`
}

// Position is the console view of launch.positions.
type Position struct {
	ID                int64      `json:"id"`
	OpportunityID     *int64     `json:"opportunity_id"`
	Mint              string     `json:"mint"`
	Symbol            string     `json:"symbol"`
	Mode              string     `json:"mode"`
	EntryChannel      string     `json:"entry_channel"`
	State             string     `json:"state"`
	InvestedUSD       float64    `json:"invested_usd"`
	EntryPriceUSD     float64    `json:"entry_price_usd"`
	RemainingTokens   float64    `json:"remaining_tokens"`
	CurrentValueUSD   *float64   `json:"current_value_usd"`
	GrossPnLUSD       *float64   `json:"gross_pnl_usd"`
	NetPnLUSD         *float64   `json:"net_pnl_usd"`
	Costs             core.Costs `json:"costs"`
	ExitReason        *string    `json:"exit_reason"`
	SizeReason        *string    `json:"size_reason"`
	FraudFlag         bool       `json:"fraud_flag"`
	StrategyVersionID *int64     `json:"strategy_version_id"`
	OpenedAt          time.Time  `json:"opened_at"`
	ClosedAt          *time.Time `json:"closed_at"`
}

// OrderView is one order of a position.
type OrderView struct {
	ID               int64     `json:"id"`
	Side             string    `json:"side"`
	Status           string    `json:"status"`
	TxSignature      *string   `json:"tx_signature"`
	QuotePrice       *float64  `json:"quote_price"`
	FillPrice        *float64  `json:"fill_price"`
	ExpectedSlippage *float64  `json:"expected_slippage"`
	ActualSlippage   *float64  `json:"actual_slippage"`
	CreatedAt        time.Time `json:"created_at"`
}

// PositionDetail is Position plus its orders.
type PositionDetail struct {
	Position
	Orders []OrderView `json:"orders"`
}

// PositionFilter filters ListPositions.
type PositionFilter struct {
	Channel  string
	State    string
	Limit    int
	BeforeID int64
}

// PositionPage is a page of positions.
type PositionPage struct {
	Items        []Position `json:"items"`
	NextBeforeID *int64     `json:"next_before_id"`
}

// StrategyVersion is one immutable parameter set.
type StrategyVersion struct {
	ID        int64           `json:"id"`
	Version   int             `json:"version"`
	Params    json.RawMessage `json:"params"`
	Author    string          `json:"author"`
	Reason    string          `json:"reason"`
	CreatedAt time.Time       `json:"created_at"`
}

// Strategy is a strategy with its versions (newest first).
type Strategy struct {
	ID       int64             `json:"id"`
	Name     string            `json:"name"`
	Status   string            `json:"status"`
	Versions []StrategyVersion `json:"versions"`
}

// GateOverview is the GET /gate payload.
type GateOverview struct {
	Latest  *GateEvaluation  `json:"latest"`
	History []GateEvaluation `json:"history"`
}

// ChannelStats aggregates closed shadow positions of one entry channel.
type ChannelStats struct {
	Trades       int      `json:"trades"`
	NetPnLUSD    float64  `json:"net_pnl_usd"`
	WinRate      float64  `json:"win_rate"`
	ProfitFactor *float64 `json:"profit_factor"`
}

// CategoryLift compares manual and counterfactual outcomes for one reason category.
type CategoryLift struct {
	Category       string       `json:"category"`
	Manual         ChannelStats `json:"manual"`
	Counterfactual ChannelStats `json:"counterfactual"`
}

// HumanLiftChannels groups ChannelStats by entry channel.
type HumanLiftChannels struct {
	Manual         ChannelStats `json:"manual"`
	Counterfactual ChannelStats `json:"counterfactual"`
	Auto           ChannelStats `json:"auto"`
}

// HumanLiftReport is the GET /reports/human-lift payload.
type HumanLiftReport struct {
	Status                          string            `json:"status"` // INSUFFICIENT_DATA | OK
	MinTrades                       int               `json:"min_trades"`
	Channels                        HumanLiftChannels `json:"channels"`
	ByCategory                      []CategoryLift    `json:"by_category"`
	ManualMinusCounterfactualNetUSD *float64          `json:"manual_minus_counterfactual_net_usd"`
}
