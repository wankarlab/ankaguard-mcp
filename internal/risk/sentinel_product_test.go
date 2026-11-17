//go:build product

package risk

import (
	"context"
	"os"
	"testing"

	"github.com/sirupsen/logrus"
)

// TestMain switches the store fallback off so the existing tests keep testing
// the store-less sentinel logic; the test below switches it back on.
func TestMain(m *testing.M) {
	storeFallbackEnabled = func() bool { return false }
	os.Exit(m.Run())
}

// Product edition without a config store: the per-order cap and its
// enforcement on market orders fall back to the registry defaults instead of
// disappearing.
func TestProductSentinelWithoutStoreUsesDefaults(t *testing.T) {
	storeFallbackEnabled = func() bool { return true }
	defer func() { storeFallbackEnabled = func() bool { return false } }()
	s := NewRiskSentinel(&RiskConfig{MaxDrawdownDaily: 5, MaxExposurePerSymbol: 50, MaxExposureTotal: 80}, 1000.0, logrus.New()).(*sentinel)
	if got := s.EffectiveMaxOrderSizeUSD(context.Background()); got <= 0 {
		t.Fatalf("per-order cap disappeared without the store: %v", got)
	}
	if !s.EnforceCapOnMarketOrders() {
		t.Fatal("market-order cap enforcement must default to on in the product edition")
	}
}
