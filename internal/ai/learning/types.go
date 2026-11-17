package learning

import "time"

// TradeOutcome represents the result of a closed trade fed back into the loop.
type TradeOutcome struct {
	TradeID               string
	Pair                  string
	Side                  string // "BUY" or "SELL"
	EntryPrice            float64
	ExitPrice             float64
	Size                  float64
	EntryTime             time.Time
	ExitTime              time.Time
	PnLAbsolute           float64
	PnLPercent            float64
	Won                   bool
	ExitReason            string // "stop_loss", "take_profit", "manual", "trailing", "wick:buyer_absorption", etc.
	WickPattern           string // set when ExitReason starts with "wick:" — e.g. "buyer_absorption"
	EntryConfidence       float64
	EntrySignal           string
	StopLoss              float64
	TakeProfit            float64
	MaxAdverseExcursion   float64
	MaxFavorableExcursion float64
}

// ParameterAdjustment describes a single incremental change applied to a parameter.
type ParameterAdjustment struct {
	Pair       string
	Parameter  string // "stop_loss_atr_mult", "min_confidence_threshold", etc.
	OldValue   float64
	NewValue   float64
	Reasoning  string
	Confidence float64
	AppliedAt  time.Time
}
