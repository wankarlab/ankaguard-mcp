package risk

// GuardAction represents the recommended action based on liquidation proximity.
type GuardAction int

const (
	// GuardOK — position has sufficient buffer from the liquidation price.
	GuardOK GuardAction = iota

	// GuardReduceLeverage — buffer is thin; reduce leverage to increase distance.
	// Triggered when distance < minBufferPct.
	GuardReduceLeverage

	// GuardConvertToSpot — position is dangerously close to liquidation;
	// convert to a deleveraged / spot equivalent to avoid forced closure.
	// Triggered when distance < minBufferPct/2.
	GuardConvertToSpot

	// GuardClose — position is critically close to liquidation and should be
	// closed immediately to prevent full loss.
	// Triggered when distance < minBufferPct/4.
	GuardClose
)

// String returns a human-readable label for the guard action.
func (a GuardAction) String() string {
	switch a {
	case GuardOK:
		return "OK"
	case GuardReduceLeverage:
		return "ReduceLeverage"
	case GuardConvertToSpot:
		return "ConvertToSpot"
	case GuardClose:
		return "Close"
	default:
		return "Unknown"
	}
}

// EvaluateLiquidationRisk computes how close a futures position is to its
// liquidation price and returns the recommended GuardAction.
//
// Parameters:
//   - entryPrice      — the price at which the position was opened (informational,
//                       not used in distance calculation; available for callers
//                       that log it).
//   - markPrice       — current mark price of the instrument.
//   - liquidationPrice — the exchange-computed liquidation price for the position.
//   - minBufferPct    — minimum acceptable percentage distance from mark to
//                       liquidation (e.g. 5.0 means 5 %).  Must be > 0.
//   - side            — "long" or "short" (case-insensitive).
//
// Return values:
//   - GuardOK            when bufferPct >= minBufferPct
//   - GuardReduceLeverage when minBufferPct/2 <= bufferPct < minBufferPct
//   - GuardConvertToSpot  when minBufferPct/4 <= bufferPct < minBufferPct/2
//   - GuardClose          when bufferPct < minBufferPct/4
//
// The function is PURE (no side effects, no I/O) so it can be called from any
// layer without risk of unintended state mutation.  The position_manager at
// internal/trading/position_manager.go should call this on every mark-price
// update and act on the returned action.
func EvaluateLiquidationRisk(entryPrice, markPrice, liquidationPrice, minBufferPct float64, side string) GuardAction {
	_ = entryPrice // reserved for future use (e.g. unrealised-PnL display)

	if minBufferPct <= 0 {
		// Invalid configuration — treat as no constraint.
		return GuardOK
	}

	// Distance from current mark price to liquidation, expressed as a
	// percentage of the mark price.  Direction depends on position side:
	//   long  → liquidation is below mark; distance = (mark - liq) / mark * 100
	//   short → liquidation is above mark; distance = (liq - mark) / mark * 100
	if markPrice <= 0 || liquidationPrice <= 0 {
		// Cannot evaluate with zero/negative prices; fail safe.
		return GuardClose
	}

	var bufferPct float64
	switch normaliseSide(side) {
	case "long":
		// For longs the liquidation price is below the mark price.
		// If liquidation >= mark the position is already underwater.
		if liquidationPrice >= markPrice {
			return GuardClose
		}
		bufferPct = (markPrice - liquidationPrice) / markPrice * 100.0
	case "short":
		// For shorts the liquidation price is above the mark price.
		if liquidationPrice <= markPrice {
			return GuardClose
		}
		bufferPct = (liquidationPrice - markPrice) / markPrice * 100.0
	default:
		// Unknown side — conservative default.
		return GuardClose
	}

	switch {
	case bufferPct >= minBufferPct:
		return GuardOK
	case bufferPct >= minBufferPct/2:
		return GuardReduceLeverage
	case bufferPct >= minBufferPct/4:
		return GuardConvertToSpot
	default:
		return GuardClose
	}
}

// normaliseSide converts "buy"/"long"/"BUY"/"LONG" → "long",
// and "sell"/"short"/"SELL"/"SHORT" → "short".
func normaliseSide(side string) string {
	switch side {
	case "long", "Long", "LONG", "buy", "Buy", "BUY":
		return "long"
	case "short", "Short", "SHORT", "sell", "Sell", "SELL":
		return "short"
	default:
		return side
	}
}
