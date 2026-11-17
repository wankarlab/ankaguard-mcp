package strategy

import "fmt"

// Status values for the StrategySpec lifecycle state machine.
const (
	StatusDraft      = "draft"
	StatusValidating = "validating"
	StatusValidated  = "validated"
	StatusRejected   = "rejected"
	StatusLive       = "live"
	StatusDemoted    = "demoted"
)

// transitions defines the allowed (from → to) pairs for the lifecycle.
// Only the listed transitions are valid; everything else is an error.
// Promotion policy (auto vs human_gate) is enforced at the API layer (API-3),
// not here — this enforces structural legality only.
var transitions = map[string][]string{
	StatusDraft:      {StatusValidating},
	StatusValidating: {StatusValidated, StatusRejected},
	StatusValidated:  {StatusLive, StatusDraft},   // →draft means a new revision is being prepared
	StatusRejected:   {StatusDraft},               // author can create a new draft
	StatusLive:       {StatusDemoted, StatusValidating}, // demoted by TTL/loss/ranking; re-validates
	StatusDemoted:    {StatusValidating, StatusDraft},
}

// ValidateTransition returns nil if transitioning from → to is allowed.
func ValidateTransition(from, to string) error {
	allowed, ok := transitions[from]
	if !ok {
		return fmt.Errorf("unknown status %q", from)
	}
	for _, a := range allowed {
		if a == to {
			return nil
		}
	}
	return fmt.Errorf("transition %q → %q is not allowed", from, to)
}

// ValidatePromotion enforces the operator≠author conflict-of-interest rule.
// The author of a Spec may never approve their own promotion (RBAC-2).
// approverID is the JWT sub of the user performing the promotion action.
// authorID is from SpecMetadata.Author (bound at create time).
func ValidatePromotion(authorID, approverID string) error {
	if authorID == "" || approverID == "" {
		return fmt.Errorf("both author and approver identity are required for promotion")
	}
	if authorID == approverID {
		return fmt.Errorf("conflict of interest: author %q cannot approve their own Spec promotion", authorID)
	}
	return nil
}

// DemotionReason classifies why a live Spec was demoted.
type DemotionReason string

const (
	DemotionTTL          DemotionReason = "ttl_expired"            // 12h certification TTL elapsed
	DemotionRanking      DemotionReason = "outranked"               // a higher-ranked Spec replaced this one
	DemotionLoss         DemotionReason = "live_pf_below_threshold" // live PF dropped below retention threshold
	DemotionDrift        DemotionReason = "drift_detected"          // live_pf dropped >30% vs shadow
)

// String returns the string value of a DemotionReason.
func (d DemotionReason) String() string { return string(d) }
