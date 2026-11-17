package core

import (
	"errors"
	"fmt"
	"time"
)

// Position modes. Phase 1 never produces "real".
const (
	ModeSimulated = "simulated"
	ModeShadow    = "shadow"
)

// EntryChannel says how an entry was decided (design D6).
type EntryChannel string

const (
	ChannelAuto           EntryChannel = "auto"
	ChannelManual         EntryChannel = "manual"
	ChannelCounterfactual EntryChannel = "counterfactual"
)

// Position states.
const (
	PosStateOpen    = "OPEN"
	PosStateClosed  = "CLOSED"
	PosStateDropped = "DROPPED" // simulated dropped entry: fee paid, nothing executed
)

// Exit reasons.
const (
	ExitTakeProfit      = "TAKE_PROFIT"
	ExitStopLoss        = "STOP_LOSS"
	ExitTrailing        = "TRAILING_STOP"
	ExitMaxHold         = "MAX_HOLD"
	ExitEmergencyLiq    = "EMERGENCY_EXIT:liquidity_drop"
	ExitEmergencyLPOut  = "EMERGENCY_EXIT:liquidity_removed"
	ExitEmergencyFreeze = "EMERGENCY_EXIT:freeze"
	ExitNoRoute         = "NO_EXIT_ROUTE"
)

// Entry block reasons.
const (
	BlockNoStrategy     = "NO_STRATEGY"
	BlockNoExitPlan     = "NO_EXIT_PLAN"
	BlockKillSwitch     = "KILL_SWITCH"
	BlockDisabled       = "LAUNCH_DISABLED"
	BlockPauseEntries   = "PAUSE_ENTRIES"
	BlockMaxPositions   = "MAX_OPEN_POSITIONS"
	BlockMaxExposure    = "MAX_EXPOSURE"
	BlockMaxPerToken    = "MAX_PER_TOKEN"
	BlockStrategyFilter = "STRATEGY_FILTER"
	BlockNotAvailable   = "NOT_AVAILABLE_IN_PHASE"
)

// TakeProfitLevel sells SellPct of the initial size when the executable value
// reaches +GainPct over the cost basis.
type TakeProfitLevel struct {
	GainPct float64 `json:"gain_pct"`
	SellPct float64 `json:"sell_pct"`
}

// StrategyParams are the immutable parameters of a strategy version.
type StrategyParams struct {
	SourceKinds          []SourceKind      `json:"source_kinds"` // accepted source kinds
	RequireGraduated     bool              `json:"require_graduated"`
	MinAgeSeconds        int               `json:"min_age_s"` // since graduation if RequireGraduated, else since creation
	MaxAgeSeconds        int               `json:"max_age_s"`
	MinLiquidityUSD      float64           `json:"min_liquidity_usd"`
	CapitalPerTradeUSD   float64           `json:"capital_per_trade_usd"`
	TakeProfits          []TakeProfitLevel `json:"take_profits"`
	StopLossPct          float64           `json:"stop_loss_pct"` // positive number, e.g. 35 = -35 %
	TrailingPct          float64           `json:"trailing_pct"`  // 0 = disabled
	TrailingAfterFirstTP bool              `json:"trailing_after_first_tp"`
	MaxHoldSeconds       int               `json:"max_hold_s"`
	MaxSlippagePct       float64           `json:"max_slippage_pct"`
	MaxPoolImpactPct     float64           `json:"max_pool_impact_pct"`
}

// Validate enforces FR-STRAT-03 (partials ≤ 100 %, SL mandatory, positive caps).
func (p StrategyParams) Validate() error {
	var errs []error
	if p.CapitalPerTradeUSD <= 0 {
		errs = append(errs, errors.New("capital_per_trade_usd must be > 0"))
	}
	if p.StopLossPct <= 0 || p.StopLossPct >= 100 {
		errs = append(errs, errors.New("stop_loss_pct is mandatory and must be in (0,100)"))
	}
	var sum float64
	for i, tp := range p.TakeProfits {
		if tp.GainPct <= 0 || tp.SellPct <= 0 {
			errs = append(errs, fmt.Errorf("take_profits[%d]: gain_pct and sell_pct must be > 0", i))
		}
		sum += tp.SellPct
	}
	if sum > 100 {
		errs = append(errs, fmt.Errorf("take_profits sell_pct sum %.2f exceeds 100", sum))
	}
	if p.MaxHoldSeconds <= 0 {
		errs = append(errs, errors.New("max_hold_s must be > 0"))
	}
	if p.MaxSlippagePct <= 0 || p.MaxPoolImpactPct <= 0 {
		errs = append(errs, errors.New("max_slippage_pct and max_pool_impact_pct must be > 0"))
	}
	if p.TrailingPct < 0 || p.TrailingPct >= 100 {
		errs = append(errs, errors.New("trailing_pct must be in [0,100)"))
	}
	return errors.Join(errs...)
}

// DefaultStrategyV1 is the seed "post-graduation-momentum" version (design D11).
func DefaultStrategyV1() StrategyParams {
	return StrategyParams{
		SourceKinds:          []SourceKind{SourceAMM},
		RequireGraduated:     true,
		MinAgeSeconds:        120,
		MaxAgeSeconds:        3600,
		MinLiquidityUSD:      15000,
		CapitalPerTradeUSD:   5,
		TakeProfits:          []TakeProfitLevel{{GainPct: 40, SellPct: 50}, {GainPct: 100, SellPct: 50}},
		StopLossPct:          35,
		TrailingPct:          25,
		TrailingAfterFirstTP: true,
		MaxHoldSeconds:       6 * 3600,
		MaxSlippagePct:       3,
		MaxPoolImpactPct:     1,
	}
}

// Costs is the per-position cost breakdown in USD (FR-PNL-01).
type Costs struct {
	VenueFeesUSD   float64 `json:"venue_fees_usd"`
	NetworkFeesUSD float64 `json:"network_fees_usd"` // base + priority fees, including dropped txs
	RentUSD        float64 `json:"rent_usd"`
	SlippageUSD    float64 `json:"slippage_usd"`
	TokenTaxUSD    float64 `json:"token_tax_usd"`
	DroppedTxs     int     `json:"dropped_txs"`
}

// Total returns the sum of all cost lines.
func (c Costs) Total() float64 {
	return c.VenueFeesUSD + c.NetworkFeesUSD + c.RentUSD + c.SlippageUSD + c.TokenTaxUSD
}

// Position is a SIMULATED/SHADOW position.
type Position struct {
	ID                int64          `json:"id"`
	OpportunityID     int64          `json:"opportunity_id"`
	StrategyVersionID int64          `json:"strategy_version_id"`
	Mint              string         `json:"mint"`
	Mode              string         `json:"mode"`
	Channel           EntryChannel   `json:"entry_channel"`
	State             string         `json:"state"`
	InitialTokens     float64        `json:"initial_tokens"`
	RemainingTokens   float64        `json:"remaining_tokens"`
	InvestedUSD       float64        `json:"invested_usd"`
	EntryPriceUSD     float64        `json:"entry_price_usd"` // executable, slippage included
	PeakValueUSD      float64        `json:"peak_value_usd"`  // peak executable value per initial token basis
	RealizedUSD       float64        `json:"realized_usd"`    // proceeds of partial/final sells
	PartialsDone      []int          `json:"partials_done"`   // indexes of TakeProfits executed
	Costs             Costs          `json:"costs"`
	GrossPnLUSD       float64        `json:"gross_pnl_usd"`
	NetPnLUSD         float64        `json:"net_pnl_usd"`
	ExitReason        string         `json:"exit_reason,omitempty"`
	SizeReason        string         `json:"size_reason,omitempty"`
	FraudFlag         bool           `json:"fraud_flag"`
	Params            StrategyParams `json:"exit_plan"`
	OpenedAt          time.Time      `json:"opened_at"`
	ClosedAt          *time.Time     `json:"closed_at,omitempty"`
}
