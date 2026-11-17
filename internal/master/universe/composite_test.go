package universe

import (
	"context"
	"errors"
	"testing"
)

// fakeStore implements SelectionStore for testing.
type fakeStore struct {
	mode  string
	pairs []string
	err   error
}

func (f *fakeStore) GetSelection(_ context.Context, _, _ string) (string, []string, error) {
	return f.mode, f.pairs, f.err
}

// fakeAuto implements PairUniverseProvider with a fixed pair list.
type fakeAuto struct {
	pairs []string
	err   error
}

func (f *fakeAuto) Pairs(_ context.Context, _ string) ([]string, error) {
	return f.pairs, f.err
}

func (f *fakeAuto) Mode() UniverseMode { return ModeAuto }

// helper — build a CompositeUniverseProvider with given store and auto provider.
func newComposite(store SelectionStore, auto PairUniverseProvider, defaultMode UniverseMode) *CompositeUniverseProvider {
	return NewCompositeUniverseProvider(store, auto, "testuser", defaultMode)
}

func TestComposite_Manual(t *testing.T) {
	store := &fakeStore{mode: "manual", pairs: []string{"BTC-USDT", "ETH-USDT"}}
	auto := &fakeAuto{pairs: []string{"XRP-USDT"}}
	c := newComposite(store, auto, ModeAuto)

	pairs, err := c.Pairs(context.Background(), "kucoin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pairs) != 2 || pairs[0] != "BTC-USDT" || pairs[1] != "ETH-USDT" {
		t.Fatalf("expected [BTC-USDT ETH-USDT], got %v", pairs)
	}
	if c.Mode() != ModeManual {
		t.Fatalf("expected ModeManual, got %v", c.Mode())
	}
}

func TestComposite_Auto(t *testing.T) {
	store := &fakeStore{mode: "auto", pairs: nil}
	auto := &fakeAuto{pairs: []string{"SOL-USDT", "DOGE-USDT"}}
	c := newComposite(store, auto, ModeManual)

	pairs, err := c.Pairs(context.Background(), "kucoin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pairs) != 2 || pairs[0] != "SOL-USDT" {
		t.Fatalf("expected [SOL-USDT DOGE-USDT], got %v", pairs)
	}
	if c.Mode() != ModeAuto {
		t.Fatalf("expected ModeAuto, got %v", c.Mode())
	}
}

func TestComposite_Hybrid_DedupeAndOrder(t *testing.T) {
	store := &fakeStore{mode: "hybrid", pairs: []string{"BTC-USDT", "ETH-USDT"}}
	// auto returns BTC-USDT (duplicate) plus new pairs
	auto := &fakeAuto{pairs: []string{"BTC-USDT", "SOL-USDT", "DOGE-USDT"}}
	c := newComposite(store, auto, ModeAuto)

	pairs, err := c.Pairs(context.Background(), "kucoin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expected: BTC-USDT, ETH-USDT (user first), SOL-USDT, DOGE-USDT (auto, deduped)
	expected := []string{"BTC-USDT", "ETH-USDT", "SOL-USDT", "DOGE-USDT"}
	if len(pairs) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, pairs)
	}
	for i, p := range expected {
		if pairs[i] != p {
			t.Fatalf("index %d: expected %s, got %s", i, p, pairs[i])
		}
	}
}

func TestComposite_Hybrid_AutoError_ReturnsUserPairs(t *testing.T) {
	store := &fakeStore{mode: "hybrid", pairs: []string{"BTC-USDT"}}
	auto := &fakeAuto{err: errors.New("exchange unreachable")}
	c := newComposite(store, auto, ModeAuto)

	pairs, err := c.Pairs(context.Background(), "kucoin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pairs) != 1 || pairs[0] != "BTC-USDT" {
		t.Fatalf("expected [BTC-USDT], got %v", pairs)
	}
}

func TestComposite_UnknownMode_FallsBackToDefault(t *testing.T) {
	store := &fakeStore{mode: "whatever", pairs: []string{"ETH-USDT"}}
	auto := &fakeAuto{pairs: []string{"SOL-USDT"}}
	// defaultMode = ModeAuto → should delegate to auto provider
	c := newComposite(store, auto, ModeAuto)

	pairs, err := c.Pairs(context.Background(), "kucoin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pairs) != 1 || pairs[0] != "SOL-USDT" {
		t.Fatalf("expected [SOL-USDT] from auto fallback, got %v", pairs)
	}
	if c.Mode() != ModeAuto {
		t.Fatalf("expected ModeAuto fallback, got %v", c.Mode())
	}
}

func TestComposite_StoreError_FallsBackToDefault(t *testing.T) {
	store := &fakeStore{err: errors.New("db down")}
	auto := &fakeAuto{pairs: []string{"BTC-USDT"}}
	c := newComposite(store, auto, ModeAuto)

	pairs, err := c.Pairs(context.Background(), "kucoin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pairs) != 1 || pairs[0] != "BTC-USDT" {
		t.Fatalf("expected auto fallback [BTC-USDT], got %v", pairs)
	}
}
