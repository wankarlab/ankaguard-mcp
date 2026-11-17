package universe

import (
	"context"
	"testing"
)

// mutableStore is a SelectionStore whose contents can be swapped between calls,
// proving the CompositeUniverseProvider re-reads the store on every Pairs() call.
type mutableStore struct {
	mode  string
	pairs []string
}

func (m *mutableStore) GetSelection(_ context.Context, _, _ string) (string, []string, error) {
	return m.mode, m.pairs, nil
}

func TestComposite_HotReload_ReflectsSelectionChange(t *testing.T) {
	store := &mutableStore{mode: "manual", pairs: []string{"BTC-USDT"}}
	auto := &fakeAuto{pairs: []string{"SOL-USDT"}}
	c := NewCompositeUniverseProvider(store, auto, "testuser", ModeAuto)

	ctx := context.Background()

	// First call: manual mode → returns user's pairs.
	pairs, err := c.Pairs(ctx, "kucoin")
	if err != nil {
		t.Fatalf("first call unexpected error: %v", err)
	}
	if len(pairs) != 1 || pairs[0] != "BTC-USDT" {
		t.Fatalf("first call: expected [BTC-USDT], got %v", pairs)
	}

	// Mutate the store WITHOUT reconstructing the provider.
	store.mode = "auto"
	store.pairs = nil

	// Second call: should now reflect the new auto mode.
	pairs, err = c.Pairs(ctx, "kucoin")
	if err != nil {
		t.Fatalf("second call unexpected error: %v", err)
	}
	if len(pairs) != 1 || pairs[0] != "SOL-USDT" {
		t.Fatalf("second call: expected [SOL-USDT] from auto, got %v", pairs)
	}
}

func TestComposite_HotReload_ManualPairsChange(t *testing.T) {
	store := &mutableStore{mode: "manual", pairs: []string{"BTC-USDT", "ETH-USDT"}}
	auto := &fakeAuto{pairs: []string{"DOGE-USDT"}}
	c := NewCompositeUniverseProvider(store, auto, "testuser", ModeAuto)

	ctx := context.Background()

	// First call.
	pairs, err := c.Pairs(ctx, "kucoin")
	if err != nil {
		t.Fatalf("first call unexpected error: %v", err)
	}
	if len(pairs) != 2 {
		t.Fatalf("first call: expected 2 pairs, got %v", pairs)
	}

	// User adds a third pair and removes one — no provider reconstruction.
	store.pairs = []string{"SOL-USDT", "XRP-USDT", "ADA-USDT"}

	// Second call must see the updated list.
	pairs, err = c.Pairs(ctx, "kucoin")
	if err != nil {
		t.Fatalf("second call unexpected error: %v", err)
	}
	expected := []string{"SOL-USDT", "XRP-USDT", "ADA-USDT"}
	if len(pairs) != len(expected) {
		t.Fatalf("second call: expected %v, got %v", expected, pairs)
	}
	for i, p := range expected {
		if pairs[i] != p {
			t.Fatalf("second call index %d: expected %s, got %s", i, p, pairs[i])
		}
	}
}
