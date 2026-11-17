package master

// MasterPersistence describes the minimal storage surface the MasterAgent
// uses to survive restarts. Implementations live outside this package
// (typically in internal/master/adapters) so that the master package itself
// has no hard storage dependency.
//
// All methods may be called concurrently with normal agent operations and
// must be safe to fail — the agent treats persistence as best-effort and
// only logs save errors.
type MasterPersistence interface {
	// SaveScoringWeights persists the current scanner weights so the
	// learned tilt survives across restarts.
	SaveScoringWeights(w *ScoringWeights) error

	// LoadScoringWeights returns the last persisted weights or (nil, nil)
	// when none have ever been written.
	LoadScoringWeights() (*ScoringWeights, error)

	// SaveOpportunities bulk-persists the opportunities produced by a scan.
	SaveOpportunities(opps []*MarketOpportunity, scanID string) error

	// SaveStateSnapshot persists the latest known runtime state.
	SaveStateSnapshot(state *MasterState) error

	// LoadStateSnapshot returns the last persisted state or (nil, nil) when
	// none has ever been written.
	LoadStateSnapshot() (*MasterState, error)
}
