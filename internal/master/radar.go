package master

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// radarNotifier is the minimal interface satisfied by *notifications.PushService.
type radarNotifier interface {
	SendTradingAlert(ctx context.Context, deviceTokens []string, alertType, symbol, message string, data map[string]interface{}) error
}

// Radar evaluates market opportunities against the user's current pair selection
// and pushes notifications for high-score pairs that are not already selected.
type Radar struct {
	enabled  bool
	minScore float64
	cooldown time.Duration

	mu          sync.Mutex
	lastNotified map[string]time.Time // pair → last notification time
}

// NewRadar creates a Radar. When enabled is false or minScore <= 0, Candidates
// always returns nil.
func NewRadar(enabled bool, minScore float64, cooldown time.Duration) *Radar {
	return &Radar{
		enabled:      enabled,
		minScore:     minScore,
		cooldown:     cooldown,
		lastNotified: make(map[string]time.Time),
	}
}

// Candidates returns opportunities that meet all three criteria:
//  1. OpportunityScore >= minScore
//  2. Pair is NOT in selected
//  3. Pair has not been notified within the cooldown window
//
// For returned candidates the last-notified timestamp is updated atomically.
// Returns nil if the Radar is disabled or minScore <= 0.
// The caller is responsible for skipping this call in AUTO mode.
func (r *Radar) Candidates(opps []*MarketOpportunity, selected []string, now time.Time) []*MarketOpportunity {
	if !r.enabled || r.minScore <= 0 {
		return nil
	}

	// Build a fast lookup set for selected pairs.
	selectedSet := make(map[string]struct{}, len(selected))
	for _, p := range selected {
		selectedSet[p] = struct{}{}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	var candidates []*MarketOpportunity
	for _, opp := range opps {
		if opp == nil {
			continue
		}
		if opp.OpportunityScore < r.minScore {
			continue
		}
		if _, inSelected := selectedSet[opp.Pair]; inSelected {
			continue
		}
		if last, seen := r.lastNotified[opp.Pair]; seen && now.Sub(last) < r.cooldown {
			continue
		}
		// Passes all filters — record timestamp and collect.
		r.lastNotified[opp.Pair] = now
		candidates = append(candidates, opp)
	}
	return candidates
}

// Notify delivers a push notification for a single opportunity. Errors are
// swallowed (best-effort delivery). Safe to call with a nil notifier or empty
// device tokens — both are no-ops.
func (r *Radar) Notify(ctx context.Context, notifier radarNotifier, deviceTokens []string, opp *MarketOpportunity) {
	if notifier == nil || len(deviceTokens) == 0 || opp == nil {
		return
	}
	message := fmt.Sprintf("New opportunity: %s score %.2f", opp.Pair, opp.OpportunityScore)
	data := map[string]interface{}{
		"pair":     opp.Pair,
		"exchange": opp.Exchange,
		"score":    opp.OpportunityScore,
		"action":   "PUT /api/v1/pairs/selection",
	}
	_ = notifier.SendTradingAlert(ctx, deviceTokens, "opportunity", opp.Pair, message, data)
}
