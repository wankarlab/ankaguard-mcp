package core

import "time"

// ReasonCode is a rejection or block reason (FRD FR-SEC-01, FR-DATA-*).
type ReasonCode string

const (
	ReasonKnownRugger       ReasonCode = "KNOWN_RUGGER"
	ReasonBlacklisted       ReasonCode = "BLACKLISTED"
	ReasonHoneypot          ReasonCode = "HONEYPOT"
	ReasonNoExitRoute       ReasonCode = "NO_EXIT_ROUTE"
	ReasonAuthorityActive   ReasonCode = "AUTHORITY_ACTIVE"
	ReasonToken22Extension  ReasonCode = "TOKEN22_EXTENSION"
	ReasonTaxTooHigh        ReasonCode = "TAX_TOO_HIGH"
	ReasonLPUnlocked        ReasonCode = "LP_UNLOCKED"
	ReasonRange             ReasonCode = "RANGE"
	ReasonConcentration     ReasonCode = "CONCENTRATION"
	ReasonBlockedStale      ReasonCode = "BLOCKED_STALE"
	ReasonDataConflict      ReasonCode = "DATA_CONFLICT"
	ReasonInvalidPrice      ReasonCode = "INVALID_PRICE"
	ReasonRPCBudgetExceeded ReasonCode = "RPC_BUDGET_EXCEEDED"
)

// Band is the action band of a score (FRD FR-SCORE-03).
type Band string

const (
	BandIgnore         Band = "IGNORE"
	BandObserve        Band = "OBSERVE"
	BandAlert          Band = "ALERT"
	BandManualApproval Band = "MANUAL_APPROVAL"
	BandAutoCandidate  Band = "AUTO_CANDIDATE"
)

// Opportunity statuses.
const (
	OppStatusRejected       = "REJECTED"
	OppStatusBlockedStale   = "BLOCKED_STALE"
	OppStatusDataConflict   = "DATA_CONFLICT"
	OppStatusScored         = "SCORED"           // IGNORE / OBSERVE / ALERT bands
	OppStatusPendingManual  = "PENDING_APPROVAL" // MANUAL_APPROVAL band, waiting for the operator
	OppStatusApproved       = "APPROVED"
	OppStatusRejectedManual = "REJECTED_MANUAL"
	OppStatusExpired        = "EXPIRED"
	OppStatusEntered        = "ENTERED" // a shadow position was opened
	OppStatusEntryBlocked   = "ENTRY_BLOCKED"
)

// ScoreFactor is one line of a score breakdown.
type ScoreFactor struct {
	Factor     string  `json:"factor"`
	Raw        float64 `json:"raw"`
	Normalized float64 `json:"normalized"` // 0..1
	Weight     float64 `json:"weight"`
	Confidence float64 `json:"confidence"` // 0 when the factor had no data
	Points     float64 `json:"points"`     // contribution on the 0..100 scale
}

// Thesis summarises the automatic case for and against an opportunity, shown
// in the manual-approval queue.
type Thesis struct {
	For     []string `json:"for"`
	Against []string `json:"against"`
}

// Opportunity is the result of screening a candidate.
type Opportunity struct {
	ID           int64         `json:"id"`
	Candidate    Candidate     `json:"candidate"`
	ScreenResult string        `json:"screen_result"` // "PASSED" or "REJECTED"
	RejectReason ReasonCode    `json:"reject_reason,omitempty"`
	Score        float64       `json:"score"`
	Band         Band          `json:"band,omitempty"`
	Status       string        `json:"status"`
	Breakdown    []ScoreFactor `json:"breakdown,omitempty"`
	Snapshot     Snapshot      `json:"snapshot"`
	Thesis       Thesis        `json:"thesis"`
	ExpiresAt    time.Time     `json:"expires_at"`
	CreatedAt    time.Time     `json:"created_at"`
}
