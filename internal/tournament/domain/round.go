// Package domain defines the core tournament entities for AnkaAI Arena.
package domain

import "time"

// RoundPhase models the lifecycle state machine for a single tournament round.
type RoundPhase string

const (
	PhaseLobby  RoundPhase = "LOBBY"   // waiting for start time
	PhaseActive RoundPhase = "ACTIVE"  // bots trading live
	PhaseVoting RoundPhase = "VOTING"  // audience votes open
	PhaseSettle RoundPhase = "SETTLE"  // votes tallied, winner determined
	PhaseRecap  RoundPhase = "RECAP"   // results visible, next round prep
)

// Round represents one competition period inside a tournament.
type Round struct {
	ID                 string     `json:"id"`
	TournamentID       string     `json:"tournament_id"`
	RoundNumber        int        `json:"round_number"`
	Phase              RoundPhase `json:"phase"`
	Symbol             string     `json:"symbol,omitempty"`            // trading pair for this round
	StartedAt          *time.Time `json:"started_at,omitempty"`
	EndedAt            *time.Time `json:"ended_at,omitempty"`
	WinnerCompetitorID string     `json:"winner_competitor_id,omitempty"`
}

// Vote records a single audience vote for a competitor in a round.
// UNIQUE(round_id, voter_id) is enforced at DB level — one vote per round per user.
type Vote struct {
	ID           string    `json:"id"`
	RoundID      string    `json:"round_id"`
	VoterID      string    `json:"voter_id"`
	CompetitorID string    `json:"competitor_id"`
	Side         string    `json:"side"`     // support, against
	Platform     string    `json:"platform"` // web, tiktok (tiktok gated)
	CreatedAt    time.Time `json:"created_at"`
}

// VoteTally aggregates vote counts for a round, keyed by competitor_id.
type VoteTally struct {
	RoundID string           `json:"round_id"`
	Counts  map[string]int64 `json:"counts"` // competitor_id → vote count
	Total   int64            `json:"total"`
}
