// Package domain defines the core tournament entities for AnkaAI Arena.
package domain

import "time"

// CompetitorKind distinguishes AI-driven bots from human traders.
type CompetitorKind string

const (
	CompetitorAI    CompetitorKind = "AI"
	CompetitorHuman CompetitorKind = "HUMAN"
)

// Competitor represents a participant (AI bot or human) inside a tournament.
type Competitor struct {
	ID           string         `json:"id"`
	TournamentID string         `json:"tournament_id"`
	Kind         CompetitorKind `json:"kind"`
	Provider     string         `json:"provider"`           // gemini, claude, openrouter, human
	SpecID       string         `json:"spec_id,omitempty"`  // strategy spec reference
	Subaccount   string         `json:"subaccount,omitempty"`
	Rank         int            `json:"rank"`
	PnlUSD       float64        `json:"pnl_usd"`
	PnlPct       float64        `json:"pnl_pct"`
	TradesTotal  int            `json:"trades_total"`
	Wins         int            `json:"wins"`
	Status       string         `json:"status"` // active, eliminated, withdrawn
	CreatedAt    time.Time      `json:"created_at"`
}

// WinRate returns the percentage of winning trades (0–100).
// Returns 0 when no trades have been recorded.
func (c *Competitor) WinRate() float64 {
	if c.TradesTotal == 0 {
		return 0
	}
	return float64(c.Wins) / float64(c.TradesTotal) * 100
}
