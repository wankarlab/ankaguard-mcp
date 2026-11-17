package services

import "testing"

// TestPositionOwnership covers the cross-instance ownership registry that stops
// the multi-pair orchestrator's bot instances from each adopting one physical
// position into separate managed copies.
func TestPositionOwnership(t *testing.T) {
	// Use a unique symbol so parallel tests can't collide on the global map.
	sym := "OWNTEST-USDT"
	defer releasePositionOwnership(sym, "sell", "opener")

	// Opener force-claims.
	if !claimPositionOwnership(sym, "sell", "opener", true) {
		t.Fatal("opener force-claim must succeed")
	}
	// A second instance trying to adopt the same physical position is refused.
	if claimPositionOwnership(sym, "sell", "adopter-A", false) {
		t.Fatal("a different instance must NOT claim a slot owned by the opener")
	}
	// Exchange-format symbol must map to the same slot (DOTUSDTM == DOT-USDT).
	if claimPositionOwnership("OWNTESTUSDTM", "short", "adopter-B", false) {
		t.Fatal("exchange-format symbol must resolve to the same owned slot")
	}
	// The opposite side is a different physical position — claimable.
	if !claimPositionOwnership(sym, "buy", "long-owner", false) {
		t.Fatal("opposite side is a separate slot and must be claimable")
	}
	releasePositionOwnership(sym, "buy", "long-owner")

	// After the owner releases, another instance can claim.
	releasePositionOwnership(sym, "sell", "opener")
	if !claimPositionOwnership(sym, "sell", "adopter-C", false) {
		t.Fatal("after release the slot must be claimable again")
	}
	releasePositionOwnership(sym, "sell", "adopter-C")

	// Release by a non-owner must NOT free the real owner's slot.
	claimPositionOwnership(sym, "sell", "real-owner", true)
	releasePositionOwnership(sym, "sell", "impostor") // no-op
	if claimPositionOwnership(sym, "sell", "someone-else", false) {
		t.Fatal("non-owner release must not free the slot")
	}
	releasePositionOwnership(sym, "sell", "real-owner")
}
