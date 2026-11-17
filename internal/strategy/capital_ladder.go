// Package strategy — RBAC-4: capital-ladder promotion gates.
//
// The capital ladder is an orthogonal dimension to the Spec lifecycle status
// (draft/validated/live).  A Spec can be "live" and simultaneously on any
// capital track.  The track controls how much real money the Spec may trade:
//
//	PAPER   → SHADOW   → VIRTUAL  → REAL_MICRO → REAL_SCALED
//
// Transitions are system-triggered (not user-requested) unless noted.
// AI (Bucle B) may NEVER advance or demote a track; only the automatic risk
// path or an operator/admin (level≥3) may do so.
package strategy

import (
	"fmt"
	"time"
)

// CapitalTrack enumerates the 5 positions on the capital ladder.
type CapitalTrack string

const (
	// TrackPaper: simulated trades only, zero real capital exposure.
	TrackPaper CapitalTrack = "paper"

	// TrackShadow: Spec runs in shadow mode alongside live trades; its
	// decisions are logged but DO NOT execute real orders.  The gap between
	// shadow_pf and live_pf is measured here.
	TrackShadow CapitalTrack = "shadow"

	// TrackVirtual: Spec executes with a hard micro-cap of $1 USD equivalent
	// per trade.  Demonstrates real-exchange connectivity; minimal loss risk.
	// Promotion from SHADOW requires operator≠author signature.
	TrackVirtual CapitalTrack = "virtual"

	// TrackRealMicro: Spec operates at normal risk limits for a single pair
	// with capped notional (max_order_size_usd from config).  Requires
	// ≥K days at TrackVirtual with no drift alert.
	TrackRealMicro CapitalTrack = "real_micro"

	// TrackRealScaled: Full production risk limits; fan-out to N pairs allowed.
	// Requires sustained tracking-error < threshold over ≥ MinScaledDays.
	TrackRealScaled CapitalTrack = "real_scaled"
)

// TrackState records the current ladder position and promotion history.
type TrackState struct {
	Track       CapitalTrack `json:"track"`
	PromotedAt  time.Time    `json:"promoted_at"`
	PromotedBy  string       `json:"promoted_by,omitempty"` // operator/system
	DemotedAt   *time.Time   `json:"demoted_at,omitempty"`
	DemotedBy   string       `json:"demoted_by,omitempty"` // operator/system/auto-risk
	DemotionRsn string       `json:"demotion_reason,omitempty"`
}

// TrackGateConditions holds the numeric thresholds that must hold for a track
// to advance.  Values are read from the current RobustnessPolicy / config.
type TrackGateConditions struct {
	// Paper → Shadow
	MinTradesForShadow    int     // ≥ N paper trades required
	MinOosPFForShadow     float64 // walk-forward OOS PF ≥ this

	// Shadow → Virtual (requires operator signature)
	MinShadowGreenDays    int     // ≥ K consecutive days shadow_pf ≥ 1.0
	MaxDriftForVirtual    float64 // shadow_pf vs live_pf divergence < this

	// Virtual → RealMicro
	MinVirtualDays        int     // ≥ J days at virtual track
	MaxDriftForMicro      float64 // drift threshold for micro promotion

	// RealMicro → RealScaled
	MinMicroDays          int     // ≥ M days at micro
	MaxTrackingError      float64 // tracking-error < threshold for scaled
}

// DefaultTrackGateConditions returns conservative defaults.
func DefaultTrackGateConditions() TrackGateConditions {
	return TrackGateConditions{
		MinTradesForShadow: 30,
		MinOosPFForShadow:  1.30,
		MinShadowGreenDays: 14,
		MaxDriftForVirtual: 0.10,
		MinVirtualDays:     7,
		MaxDriftForMicro:   0.08,
		MinMicroDays:       14,
		MaxTrackingError:   0.05,
	}
}

// allowedTrackTransitions lists the valid (from → to) pairs.
var allowedTrackTransitions = map[CapitalTrack][]CapitalTrack{
	TrackPaper:      {TrackShadow},
	TrackShadow:     {TrackVirtual, TrackPaper},     // demote back to paper on bad drift
	TrackVirtual:    {TrackRealMicro, TrackShadow},  // micro fails → back to shadow
	TrackRealMicro:  {TrackRealScaled, TrackShadow}, // scaled fails → back to shadow (not micro)
	TrackRealScaled: {TrackShadow},                  // scaled demoted → shadow (full reset)
}

// requiresOperatorSignature returns true when advancing from → to requires
// an explicit operator≠author approval (not automatic).
func requiresOperatorSignature(from, to CapitalTrack) bool {
	// Virtual requires a human sign-off because it touches real money for the
	// first time.  All subsequent promotions are automatic (system-triggered).
	return from == TrackShadow && to == TrackVirtual
}

// ValidateTrackTransition checks whether from → to is allowed.
// It does NOT check the gate conditions (those are evaluated by the caller).
func ValidateTrackTransition(from, to CapitalTrack) error {
	allowed, ok := allowedTrackTransitions[from]
	if !ok {
		return fmt.Errorf("unknown capital track %q", from)
	}
	for _, a := range allowed {
		if a == to {
			return nil
		}
	}
	return fmt.Errorf("capital track transition %q → %q is not allowed", from, to)
}

// ValidateTrackPromotion validates an operator-signed track promotion.
// operatorID must differ from authorID (RBAC-2 conflict-of-interest).
// Only called when requiresOperatorSignature returns true.
func ValidateTrackPromotion(from, to CapitalTrack, authorID, operatorID string) error {
	if err := ValidateTrackTransition(from, to); err != nil {
		return err
	}
	if requiresOperatorSignature(from, to) {
		if authorID == "" || operatorID == "" {
			return fmt.Errorf("operator signature required for %q → %q promotion", from, to)
		}
		if authorID == operatorID {
			return fmt.Errorf("conflict of interest: spec author %q cannot self-sign capital promotion", authorID)
		}
	}
	return nil
}

// EvaluatePaperToShadow returns nil when the paper → shadow gate conditions
// are met.
func EvaluatePaperToShadow(tradeCount int, oosPF float64, cond TrackGateConditions) error {
	if tradeCount < cond.MinTradesForShadow {
		return fmt.Errorf("paper→shadow: need ≥%d paper trades, have %d", cond.MinTradesForShadow, tradeCount)
	}
	if oosPF < cond.MinOosPFForShadow {
		return fmt.Errorf("paper→shadow: OOS PF %.3f < required %.3f", oosPF, cond.MinOosPFForShadow)
	}
	return nil
}

// EvaluateShadowToVirtual returns nil when the shadow → virtual gate
// conditions are met (operator signature checked separately by caller).
func EvaluateShadowToVirtual(shadowGreenDays int, driftPct float64, cond TrackGateConditions) error {
	if shadowGreenDays < cond.MinShadowGreenDays {
		return fmt.Errorf("shadow→virtual: need ≥%d green shadow days, have %d",
			cond.MinShadowGreenDays, shadowGreenDays)
	}
	if driftPct > cond.MaxDriftForVirtual {
		return fmt.Errorf("shadow→virtual: drift %.3f > max %.3f", driftPct, cond.MaxDriftForVirtual)
	}
	return nil
}

// EvaluateVirtualToMicro returns nil when the virtual → real_micro gate
// conditions are met.
func EvaluateVirtualToMicro(virtualDays int, driftPct float64, cond TrackGateConditions) error {
	if virtualDays < cond.MinVirtualDays {
		return fmt.Errorf("virtual→micro: need ≥%d virtual days, have %d",
			cond.MinVirtualDays, virtualDays)
	}
	if driftPct > cond.MaxDriftForMicro {
		return fmt.Errorf("virtual→micro: drift %.3f > max %.3f", driftPct, cond.MaxDriftForMicro)
	}
	return nil
}

// EvaluateMicroToScaled returns nil when the real_micro → real_scaled gate
// conditions are met.
func EvaluateMicroToScaled(microDays int, trackingError float64, cond TrackGateConditions) error {
	if microDays < cond.MinMicroDays {
		return fmt.Errorf("micro→scaled: need ≥%d micro days, have %d",
			cond.MinMicroDays, microDays)
	}
	if trackingError > cond.MaxTrackingError {
		return fmt.Errorf("micro→scaled: tracking error %.4f > max %.4f",
			trackingError, cond.MaxTrackingError)
	}
	return nil
}

// ShouldAutoDemote returns a non-empty reason when the current track should
// be automatically demoted due to tracking-error divergence.
// This is the only demote trigger for the scaling risk path — a live bot's
// EvaluateAction gating (sentinel.go Ring1) is a separate mechanism.
func ShouldAutoDemote(track CapitalTrack, trackingError float64, cond TrackGateConditions) string {
	switch track {
	case TrackRealMicro, TrackRealScaled:
		if trackingError > cond.MaxTrackingError*1.5 {
			return fmt.Sprintf("tracking-error %.4f exceeds hard demotion threshold %.4f",
				trackingError, cond.MaxTrackingError*1.5)
		}
	}
	return ""
}
