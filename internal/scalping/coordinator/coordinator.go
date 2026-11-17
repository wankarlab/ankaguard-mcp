package coordinator

import (
	"fmt"
	"strings"
	"sync"
)

// PairCoordinator prevents two independent trading actors (e.g. AITradingBot and
// a BotManager scalping bot) from simultaneously operating on the same pair.
// Claims are scoped per user and keyed by composite string "userID|pair" (canonical upper-case pair).
// All methods are safe for concurrent use.
type PairCoordinator struct {
	mu     sync.RWMutex
	claims map[string]string // "userID|pair" → owner
}

// NewPairCoordinator creates an empty coordinator ready for use.
func NewPairCoordinator() *PairCoordinator {
	return &PairCoordinator{claims: make(map[string]string)}
}

// TryClaimForUser registers owner as the exclusive holder of pair for a specific user.
// Returns false (without claiming) if pair is already held by a different owner in that user's context.
// Returns true if the claim is new or the owner already holds it.
func (pc *PairCoordinator) TryClaimForUser(userID, pair, owner string) bool {
	key := userID + "|" + strings.ToUpper(pair)
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if existing, ok := pc.claims[key]; ok && existing != owner {
		return false
	}
	pc.claims[key] = owner
	return true
}

// TryClaim registers owner as the exclusive holder of pair in the default user context.
// Delegates to TryClaimForUser with userID = "default".
// Returns false (without claiming) if pair is already held by a different owner.
// Returns true if the claim is new or the owner already holds it.
func (pc *PairCoordinator) TryClaim(pair, owner string) bool {
	return pc.TryClaimForUser("default", pair, owner)
}

// ReleaseForUser removes the claim on pair held by owner in a specific user's context.
// No-op if the pair is not claimed or is claimed by a different owner.
func (pc *PairCoordinator) ReleaseForUser(userID, pair, owner string) {
	key := userID + "|" + strings.ToUpper(pair)
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if pc.claims[key] == owner {
		delete(pc.claims, key)
	}
}

// Release removes the claim on pair held by owner in the default user context.
// Delegates to ReleaseForUser with userID = "default".
// No-op if the pair is not claimed or is claimed by a different owner.
func (pc *PairCoordinator) Release(pair, owner string) {
	pc.ReleaseForUser("default", pair, owner)
}

// OwnedByForUser returns the name of the owner that holds pair in a specific user's context,
// or empty string if unclaimed.
func (pc *PairCoordinator) OwnedByForUser(userID, pair string) string {
	key := userID + "|" + strings.ToUpper(pair)
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.claims[key]
}

// OwnedBy returns the name of the owner that holds pair in the default user context,
// or empty string if unclaimed. Delegates to OwnedByForUser with userID = "default".
func (pc *PairCoordinator) OwnedBy(pair string) string {
	return pc.OwnedByForUser("default", pair)
}

// ActivePairsForUser returns a snapshot of all currently claimed pairs in a specific user's context and their owners.
// Keys in the returned map are pair strings (without userID prefix).
func (pc *PairCoordinator) ActivePairsForUser(userID string) map[string]string {
	prefix := userID + "|"
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	out := make(map[string]string)
	for k, v := range pc.claims {
		if strings.HasPrefix(k, prefix) {
			pair := strings.TrimPrefix(k, prefix)
			out[pair] = v
		}
	}
	return out
}

// ActivePairs returns a snapshot of all currently claimed pairs in the default user context and their owners.
// Delegates to ActivePairsForUser with userID = "default".
// Keys in the returned map are pair strings (without userID prefix).
func (pc *PairCoordinator) ActivePairs() map[string]string {
	return pc.ActivePairsForUser("default")
}

// ErrPairConflict is returned when a StartBot call is rejected because the pair
// is already claimed by another actor.
type ErrPairConflict struct {
	Pair  string
	Owner string
}

func (e *ErrPairConflict) Error() string {
	return fmt.Sprintf("pair %s is already reserved by %s", e.Pair, e.Owner)
}
