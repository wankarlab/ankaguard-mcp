package universe

import "context"

// SelectionStore reads the persisted user pair selection.
// A thin adapter over storage.UserPairSelectionRepository is wired at startup;
// defined here to keep the universe package free of storage imports.
type SelectionStore interface {
	GetSelection(ctx context.Context, userID, exchange string) (mode string, pairs []string, err error)
}

// UserCuratedUniverse implements PairUniverseProvider using a user-persisted
// pair list (ModeManual).
type UserCuratedUniverse struct {
	store  SelectionStore
	userID string
}

// NewUserCuratedUniverse constructs a UserCuratedUniverse.
// If userID is empty it defaults to "default".
func NewUserCuratedUniverse(store SelectionStore, userID string) *UserCuratedUniverse {
	if userID == "" {
		userID = "default"
	}
	return &UserCuratedUniverse{store: store, userID: userID}
}

// Mode returns ModeManual.
func (u *UserCuratedUniverse) Mode() UniverseMode {
	return ModeManual
}

// Pairs returns the user-persisted pairs for the given exchange.
// The slice may be empty if the user has not yet selected any pairs.
func (u *UserCuratedUniverse) Pairs(ctx context.Context, exchange string) ([]string, error) {
	_, pairs, err := u.store.GetSelection(ctx, u.userID, exchange)
	if err != nil {
		return nil, err
	}
	return pairs, nil
}
