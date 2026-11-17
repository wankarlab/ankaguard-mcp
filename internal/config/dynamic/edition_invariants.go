package dynamic

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/rantipay/trading-mcp/internal/edition"
)

// ErrEditionInvariant is returned when a value breaks a safety invariant of
// the compiled edition (for example disabling the per-order cap in the
// product build).
var ErrEditionInvariant = errors.New("value not allowed in this edition")

// CheckEditionInvariant rejects values that the compiled edition forbids,
// whatever the registry bounds say. In the standard build it never rejects
// anything, so the deployment that runs the real-money bot is unchanged.
//
// Product build:
//   - risk.max_order_size_usd must be > 0: the per-order cap cannot be
//     disabled (spec order-notional-cap / secure-defaults).
func CheckEditionInvariant(key, value string) error {
	if !edition.IsProduct() {
		return nil
	}
	return checkProductInvariant(key, value)
}

// checkProductInvariant holds the product rules; split out so tests can run
// them under any build tag.
func checkProductInvariant(key, value string) error {
	switch key {
	case "risk.max_order_size_usd":
		v, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return fmt.Errorf("%w: %s must be a number", ErrEditionInvariant, key)
		}
		if v <= 0 {
			return fmt.Errorf("%w: %s must be greater than 0 (the per-order cap cannot be disabled)", ErrEditionInvariant, key)
		}
	}
	return nil
}
