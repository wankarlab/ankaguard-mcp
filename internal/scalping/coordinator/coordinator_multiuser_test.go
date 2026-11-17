package coordinator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPairCoordinator_MultiUser_SamePairDifferentUsers asserts that the same pair
// can be claimed by different owners across different users (multi-account parallelism).
func TestPairCoordinator_MultiUser_SamePairDifferentUsers(t *testing.T) {
	pc := NewPairCoordinator()

	// User A claims BTCUSDT with owner "ai"
	claimedA := pc.TryClaimForUser("A", "BTCUSDT", "ai")
	require.True(t, claimedA, "User A should successfully claim BTCUSDT with owner ai")

	// User B claims the same pair BTCUSDT with owner "ai" (different user context)
	claimedB := pc.TryClaimForUser("B", "BTCUSDT", "ai")
	require.True(t, claimedB, "User B should successfully claim BTCUSDT with owner ai (independent user context)")

	// Verify both claims exist
	ownerA := pc.OwnedByForUser("A", "BTCUSDT")
	assert.Equal(t, "ai", ownerA, "User A's BTCUSDT should be owned by ai")

	ownerB := pc.OwnedByForUser("B", "BTCUSDT")
	assert.Equal(t, "ai", ownerB, "User B's BTCUSDT should be owned by ai")
}

// TestPairCoordinator_MultiUser_SamePairSameUserDifferentOwners asserts that
// the same pair for the same user cannot be claimed by two different owners.
func TestPairCoordinator_MultiUser_SamePairSameUserDifferentOwners(t *testing.T) {
	pc := NewPairCoordinator()

	// User A claims BTCUSDT with owner "ai"
	claimed1 := pc.TryClaimForUser("A", "BTCUSDT", "ai")
	require.True(t, claimed1, "First claim should succeed")

	// User A tries to claim the same pair with a different owner "scalp"
	claimed2 := pc.TryClaimForUser("A", "BTCUSDT", "scalp")
	assert.False(t, claimed2, "Second claim with different owner should fail")

	// Verify ownership is still "ai"
	owner := pc.OwnedByForUser("A", "BTCUSDT")
	assert.Equal(t, "ai", owner, "Ownership should remain with the first owner (ai)")
}

// TestPairCoordinator_MultiUser_ReleaseScopedToUser asserts that releasing a claim
// for one user does not affect another user's claim on the same pair.
func TestPairCoordinator_MultiUser_ReleaseScopedToUser(t *testing.T) {
	pc := NewPairCoordinator()

	// Both users claim BTCUSDT with owner "ai"
	pc.TryClaimForUser("A", "BTCUSDT", "ai")
	pc.TryClaimForUser("B", "BTCUSDT", "ai")

	// Release User A's claim
	pc.ReleaseForUser("A", "BTCUSDT", "ai")

	// User A should have no owner
	ownerA := pc.OwnedByForUser("A", "BTCUSDT")
	assert.Equal(t, "", ownerA, "User A's claim should be released")

	// User B should still have the claim
	ownerB := pc.OwnedByForUser("B", "BTCUSDT")
	assert.Equal(t, "ai", ownerB, "User B's claim should remain unaffected")
}

// TestPairCoordinator_Legacy_DefaultUserDelegate asserts that legacy TryClaim/Release
// methods delegate to the "default" user context and do not collide with other users.
func TestPairCoordinator_Legacy_DefaultUserDelegate(t *testing.T) {
	pc := NewPairCoordinator()

	// Legacy claim on BTCUSDT with owner "ai"
	claimed1 := pc.TryClaim("BTCUSDT", "ai")
	require.True(t, claimed1, "Legacy TryClaim should succeed")

	// Non-default user A claims the same pair
	claimedA := pc.TryClaimForUser("A", "BTCUSDT", "scalp")
	require.True(t, claimedA, "User A should successfully claim BTCUSDT with different owner")

	// Verify legacy (default) still owns "ai"
	ownerDefault := pc.OwnedBy("BTCUSDT")
	assert.Equal(t, "ai", ownerDefault, "Default user should still own BTCUSDT with ai")

	// Verify user A owns "scalp"
	ownerA := pc.OwnedByForUser("A", "BTCUSDT")
	assert.Equal(t, "scalp", ownerA, "User A should own BTCUSDT with scalp")
}

// TestPairCoordinator_ActivePairsForUser asserts that ActivePairsForUser returns
// only the claims for a specific user.
func TestPairCoordinator_ActivePairsForUser(t *testing.T) {
	pc := NewPairCoordinator()

	// User A claims two pairs
	pc.TryClaimForUser("A", "BTCUSDT", "ai")
	pc.TryClaimForUser("A", "ETHUSDT", "scalp")

	// User B claims one pair
	pc.TryClaimForUser("B", "BTCUSDT", "ai")

	// Check User A's active pairs
	activePairsA := pc.ActivePairsForUser("A")
	assert.Len(t, activePairsA, 2, "User A should have 2 active pairs")
	assert.Equal(t, "ai", activePairsA["BTCUSDT"], "User A BTCUSDT should be owned by ai")
	assert.Equal(t, "scalp", activePairsA["ETHUSDT"], "User A ETHUSDT should be owned by scalp")

	// Check User B's active pairs
	activePairsB := pc.ActivePairsForUser("B")
	assert.Len(t, activePairsB, 1, "User B should have 1 active pair")
	assert.Equal(t, "ai", activePairsB["BTCUSDT"], "User B BTCUSDT should be owned by ai")
}

// TestPairCoordinator_MultiUser_ConcurrentClaims asserts that concurrent claims
// from multiple users are handled correctly (thread-safe).
func TestPairCoordinator_MultiUser_ConcurrentClaims(t *testing.T) {
	pc := NewPairCoordinator()

	// Simulate concurrent claims from 3 users on the same pair
	results := make(chan bool, 3)

	// User A
	go func() {
		results <- pc.TryClaimForUser("A", "ETHUSD", "ai")
	}()

	// User B
	go func() {
		results <- pc.TryClaimForUser("B", "ETHUSD", "ai")
	}()

	// User C
	go func() {
		results <- pc.TryClaimForUser("C", "ETHUSD", "ai")
	}()

	// All three should succeed (different user contexts)
	r1 := <-results
	r2 := <-results
	r3 := <-results

	assert.True(t, r1, "User A claim should succeed")
	assert.True(t, r2, "User B claim should succeed")
	assert.True(t, r3, "User C claim should succeed")

	// Verify all three own the pair in their respective contexts
	assert.Equal(t, "ai", pc.OwnedByForUser("A", "ETHUSD"))
	assert.Equal(t, "ai", pc.OwnedByForUser("B", "ETHUSD"))
	assert.Equal(t, "ai", pc.OwnedByForUser("C", "ETHUSD"))
}

// TestPairCoordinator_MultiUser_ReclaimSameOwner asserts that reclaiming a pair
// with the same owner succeeds (idempotent).
func TestPairCoordinator_MultiUser_ReclaimSameOwner(t *testing.T) {
	pc := NewPairCoordinator()

	// User A claims BTCUSDT with owner "ai"
	claimed1 := pc.TryClaimForUser("A", "BTCUSDT", "ai")
	require.True(t, claimed1)

	// User A re-claims the same pair with the same owner "ai" (should be idempotent)
	claimed2 := pc.TryClaimForUser("A", "BTCUSDT", "ai")
	require.True(t, claimed2, "Reclaim with same owner should succeed (idempotent)")

	// Ownership should remain
	owner := pc.OwnedByForUser("A", "BTCUSDT")
	assert.Equal(t, "ai", owner)
}

// TestPairCoordinator_MultiUser_CaseInsensitive asserts that pair names are normalized
// to uppercase in the same way for all users.
func TestPairCoordinator_MultiUser_CaseInsensitive(t *testing.T) {
	pc := NewPairCoordinator()

	// User A claims with lowercase
	claimedA := pc.TryClaimForUser("A", "btcusdt", "ai")
	require.True(t, claimedA)

	// User A tries to claim with uppercase (should fail, already claimed)
	claimedA2 := pc.TryClaimForUser("A", "BTCUSDT", "scalp")
	assert.False(t, claimedA2, "Case-insensitive pair names should map to the same claim")

	// Verify the claim is normalized to uppercase
	owner := pc.OwnedByForUser("A", "BTCUSDT")
	assert.Equal(t, "ai", owner)
}
