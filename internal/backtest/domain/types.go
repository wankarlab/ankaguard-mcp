// Package domain defines the core data types used by the backtesting subsystem.
package domain

import "time"

// CostModel holds the fee and slippage assumptions used in a backtest simulation.
// All values are in percentage points (e.g. 0.06 = 0.06%).
// Defaults via DefaultCostModel() model KuCoin futures conservatively.
type CostModel struct {
	// ID is the stable identifier stored in the cost_models table.
	ID string `json:"id,omitempty"`
	// Entry is always a market (taker) order.
	EntryTakerFeePct float64 `json:"entry_taker_fee_pct"`
	// Exit on take-profit may be a limit (maker) order.
	ExitMakerFeePct float64 `json:"exit_maker_fee_pct"`
	// Exit on stop-loss is always a market (taker) order.
	ExitTakerFeePct float64 `json:"exit_taker_fee_pct"`
	// SlippagePct models adverse fill on SL exits (price moves against you before fill).
	// Not applied to TP exits (limit orders fill at the target price).
	SlippagePct float64 `json:"slippage_pct"`
}

// DefaultCostModel returns conservative KuCoin futures fee assumptions.
// Taker 0.06%, maker 0.02%, SL slippage 0.05% (half a tick in normal liquidity).
func DefaultCostModel() CostModel {
	return CostModel{
		EntryTakerFeePct: 0.06,
		ExitMakerFeePct:  0.02,
		ExitTakerFeePct:  0.06,
		SlippagePct:      0.05,
	}
}

// BacktestRequest is the input specification for a single backtest run.
type BacktestRequest struct {
	// Overrides maps parameter keys to the string values to test.
	Overrides map[string]string `json:"overrides"`
	// BaselinePreset identifies the config baseline: "" = current live snapshot,
	// "preset:<name>" = a named preset, "current" is also accepted as an alias for "".
	BaselinePreset string    `json:"baseline_preset"`
	Pair           string    `json:"pair"`
	Exchange       string    `json:"exchange"`
	From           time.Time `json:"from"`
	To             time.Time `json:"to"`
	TriggeredBy    string    `json:"triggered_by"`
	// Costs models exchange fees and slippage. Zero value → DefaultCostModel() is applied.
	Costs CostModel `json:"costs"`
}

// BacktestResult is the computed output of a backtest run.
type BacktestResult struct {
	Trades  []BacktestTrade `json:"trades"`
	Metrics Metrics         `json:"metrics"`
}

// BacktestRun is the persisted record of a backtest job in the database.
type BacktestRun struct {
	ID              int64             `json:"id"`
	ConfigOverrides map[string]string `json:"config_overrides"`
	Baseline        string            `json:"baseline"`
	Pair            string            `json:"pair"`
	Exchange        string            `json:"exchange"`
	HorizonFrom     time.Time         `json:"horizon_from"`
	HorizonTo       time.Time         `json:"horizon_to"`
	CandleCount     int               `json:"candle_count"`
	Status          string            `json:"status"` // pending|running|done|failed
	StartedAt       *time.Time        `json:"started_at,omitempty"`
	FinishedAt      *time.Time        `json:"finished_at,omitempty"`
	Metrics         *Metrics          `json:"metrics,omitempty"`
	TriggeredBy     string            `json:"triggered_by"`
	Error           *string           `json:"error,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
}

// BacktestTrade represents a single simulated trade during a backtest replay.
type BacktestTrade struct {
	RunID         int64              `json:"run_id"`
	CandleTime    time.Time          `json:"candle_time"`
	Side          string             `json:"side"` // buy|sell
	EntryPrice    float64            `json:"entry_price"`
	ExitPrice     float64            `json:"exit_price,omitempty"`
	ExitTime      *time.Time         `json:"exit_time,omitempty"`
	ExitReason    string             `json:"exit_reason,omitempty"`    // stop_loss|take_profit|timeout|opposite_signal
	GrossPnLPct   float64            `json:"gross_pnl_pct,omitempty"`  // price delta only, no costs
	PnLPct        float64            `json:"pnl_pct,omitempty"`        // net of fees + slippage
	TotalCostPct  float64            `json:"total_cost_pct,omitempty"` // fees + slippage deducted
	Confidence    float64            `json:"confidence,omitempty"`
	SignalFactors map[string]float64 `json:"signal_factors,omitempty"`
}

// Metrics holds the summary statistics computed from a completed backtest.
type Metrics struct {
	NTrades            int     `json:"n_trades"`
	WinRate            float64 `json:"win_rate"`
	Sharpe             float64 `json:"sharpe"`
	MaxDrawdown        float64 `json:"max_drawdown"`
	ProfitFactor       float64 `json:"profit_factor"`
	AvgWinPct          float64 `json:"avg_win_pct"`
	AvgLossPct         float64 `json:"avg_loss_pct"`
	LargestWin         float64 `json:"largest_win"`
	LargestLoss        float64 `json:"largest_loss"`
	NConsecutiveWins   int     `json:"n_consecutive_wins"`
	NConsecutiveLosses int     `json:"n_consecutive_losses"`
	ExpectedValue      float64 `json:"expected_value"`
}

// VirtualPosition tracks a simulated open position during walk-forward replay.
type VirtualPosition struct {
	Side       string
	EntryPrice float64
	EntryTime  time.Time
	StopLoss   float64
	TakeProfit float64
	Confidence float64
}
