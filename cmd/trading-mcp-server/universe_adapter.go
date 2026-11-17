package main

import (
	"context"

	"github.com/rantipay/trading-mcp/internal/storage"
)

// selectionStoreAdapter bridges storage.UserPairSelectionRepository to the
// universe.SelectionStore interface without creating an import cycle
// (universe must not import storage; storage must not import universe).
// Placed in package main which imports both freely.
type selectionStoreAdapter struct {
	repo *storage.UserPairSelectionRepository
}

func (a selectionStoreAdapter) GetSelection(ctx context.Context, userID, exchange string) (string, []string, error) {
	sel, err := a.repo.Get(ctx, userID, exchange)
	if err != nil {
		return "", nil, err
	}
	return sel.Mode, sel.Pairs, nil
}
