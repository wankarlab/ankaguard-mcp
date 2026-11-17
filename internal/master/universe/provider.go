package universe

import "context"

// UniverseMode identifies how the tradable-pair universe is sourced.
type UniverseMode string

const (
	ModeAuto   UniverseMode = "auto"
	ModeManual UniverseMode = "manual"
	ModeHybrid UniverseMode = "hybrid"
)

// PairUniverseProvider returns the set of tradable pairs the master scanner
// should consider for a given exchange, according to the active mode.
type PairUniverseProvider interface {
	// Pairs returns the current universe of pairs for the exchange.
	Pairs(ctx context.Context, exchange string) ([]string, error)
	// Mode reports the provider's selection mode.
	Mode() UniverseMode
}
