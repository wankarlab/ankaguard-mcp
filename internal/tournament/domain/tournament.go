// Package domain defines the core tournament entities for AnkaAI Arena.
package domain

import "time"

// TournamentStatus represents the lifecycle state of a tournament.
type TournamentStatus string

const (
	TournamentPending TournamentStatus = "pending"
	TournamentLive    TournamentStatus = "live"
	TournamentEnded   TournamentStatus = "ended"
	TournamentPaused  TournamentStatus = "paused"
)

// CapitalState controls whether the tournament runs on paper, shadow, micro, or real capital.
// Escalation paper → shadow → micro → real is gated by SEC-5/6/10 (not yet implemented).
type CapitalState string

const (
	CapitalPaper   CapitalState = "paper"
	CapitalShadow  CapitalState = "shadow"
	CapitalMicro   CapitalState = "micro"
	CapitalReal    CapitalState = "real"
)

// Tournament is the top-level aggregate for an AnkaAI Arena competition.
type Tournament struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	Format       string           `json:"format"`        // round_robin, elimination, etc.
	Status       TournamentStatus `json:"status"`
	CapitalState CapitalState     `json:"capital_state"`
	Config       TournamentConfig `json:"config"`
	Competitors  []Competitor     `json:"competitors,omitempty"`
	Rounds       []Round          `json:"rounds,omitempty"`
	CreatedAt    time.Time        `json:"created_at"`
	StartedAt    *time.Time       `json:"started_at,omitempty"`
	EndedAt      *time.Time       `json:"ended_at,omitempty"`
}

// TournamentConfig holds runtime parameters for a tournament.
type TournamentConfig struct {
	RoundDurationSec  int     `json:"round_duration_sec"`
	VotingDurationSec int     `json:"voting_duration_sec"`
	MaxCompetitors    int     `json:"max_competitors"`
	CapUSD            float64 `json:"cap_usd"`
}
