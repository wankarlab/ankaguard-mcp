package universe

import "context"

// CompositeUniverseProvider dispatches to the correct universe source based on
// the mode persisted in the store for the given user.  The stored mode takes
// priority; if it is absent or unrecognised, defaultMode is used.
type CompositeUniverseProvider struct {
	store       SelectionStore
	auto        PairUniverseProvider
	userID      string
	defaultMode UniverseMode
}

// NewCompositeUniverseProvider constructs a CompositeUniverseProvider.
func NewCompositeUniverseProvider(
	store SelectionStore,
	auto PairUniverseProvider,
	userID string,
	defaultMode UniverseMode,
) *CompositeUniverseProvider {
	if userID == "" {
		userID = "default"
	}
	return &CompositeUniverseProvider{
		store:       store,
		auto:        auto,
		userID:      userID,
		defaultMode: defaultMode,
	}
}

// parseMode converts a stored mode string to a UniverseMode.
// Returns ("", false) when the string is not a known mode.
func parseMode(s string) (UniverseMode, bool) {
	switch UniverseMode(s) {
	case ModeAuto, ModeManual, ModeHybrid:
		return UniverseMode(s), true
	}
	return "", false
}

// effectiveMode reads the store and returns the resolved mode + user pairs.
// On store error the caller falls back to defaultMode with empty userPairs.
func (c *CompositeUniverseProvider) effectiveMode(ctx context.Context, exchange string) (UniverseMode, []string) {
	storedStr, userPairs, err := c.store.GetSelection(ctx, c.userID, exchange)
	if err != nil {
		return c.defaultMode, nil
	}
	mode, ok := parseMode(storedStr)
	if !ok {
		return c.defaultMode, userPairs
	}
	return mode, userPairs
}

// Pairs returns the union of pairs according to the effective mode.
//
//   - ModeAuto   — delegate entirely to the dynamic auto provider.
//   - ModeManual — return the user-curated list as-is (may be empty).
//   - ModeHybrid — user pairs first (preserving order, deduped), then auto
//     pairs appended (deduped, skipping already-present ones).
//     On auto error, at least the user pairs are returned.
func (c *CompositeUniverseProvider) Pairs(ctx context.Context, exchange string) ([]string, error) {
	mode, userPairs := c.effectiveMode(ctx, exchange)

	switch mode {
	case ModeManual:
		return userPairs, nil

	case ModeAuto:
		return c.auto.Pairs(ctx, exchange)

	default: // ModeHybrid
		seen := make(map[string]struct{}, len(userPairs))
		result := make([]string, 0, len(userPairs))
		for _, p := range userPairs {
			if _, dup := seen[p]; !dup {
				seen[p] = struct{}{}
				result = append(result, p)
			}
		}

		autoPairs, err := c.auto.Pairs(ctx, exchange)
		if err != nil {
			// Degrade gracefully: return at least the user pairs.
			return result, nil
		}
		for _, p := range autoPairs {
			if _, dup := seen[p]; !dup {
				seen[p] = struct{}{}
				result = append(result, p)
			}
		}
		return result, nil
	}
}

// Mode returns the current effective mode for the provider's user.
// It reads the store best-effort; on error, defaultMode is returned.
func (c *CompositeUniverseProvider) Mode() UniverseMode {
	mode, _ := c.effectiveMode(context.Background(), "")
	return mode
}
