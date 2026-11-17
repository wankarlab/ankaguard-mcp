package dynamic

import (
	"errors"
	"testing"

	"github.com/rantipay/trading-mcp/internal/config/dynamic/domain"
	"github.com/rantipay/trading-mcp/internal/edition"
)

// C1: in the product edition the per-order cap cannot be disabled.
func TestProductInvariantMaxOrderSize(t *testing.T) {
	for _, v := range []string{"0", "0.0", "-1", " 0 "} {
		if err := checkProductInvariant("risk.max_order_size_usd", v); !errors.Is(err, ErrEditionInvariant) {
			t.Fatalf("value %q accepted: %v", v, err)
		}
	}
	if err := checkProductInvariant("risk.max_order_size_usd", "2.5"); err != nil {
		t.Fatal(err)
	}
	if err := checkProductInvariant("indicators.rsi.period", "0"); err != nil {
		t.Fatal(err)
	}
}

// ValidateValue applies the invariant only in the product edition; the
// standard build keeps 0 = cap disabled, as before.
func TestValidateValueFollowsEdition(t *testing.T) {
	min, max := "0.0", "100000.0"
	reg := &domain.ParameterRegistry{Key: "risk.max_order_size_usd", Type: domain.ParamTypeFloat, MinValue: &min, MaxValue: &max}
	err := ValidateValue(reg, "0")
	if edition.IsProduct() != (err != nil) {
		t.Fatalf("product=%v err=%v", edition.IsProduct(), err)
	}
}
