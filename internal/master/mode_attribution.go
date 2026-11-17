package master

// ModeStats holds aggregated performance statistics for a single trading mode.
type ModeStats struct {
	Mode         string
	Trades       int
	Wins         int
	WinRate      float64
	ProfitFactor float64
	NetPnL       float64
}

// deriveMode returns the trading mode for a trade record using the following priority:
//  1. If MarketType is set (non-empty), use it directly.
//  2. If Leverage > 1, classify as "futures" (consistent with engine heuristic).
//  3. Default to "spot".
func deriveMode(t TradeRecord) string {
	if t.MarketType != "" {
		return t.MarketType
	}
	if t.Leverage > 1 {
		return "futures"
	}
	return "spot"
}

// AttributeByMode buckets a slice of trades by derived mode and computes performance stats.
// Returns a map keyed by mode string (e.g. "spot", "futures", "margin").
func AttributeByMode(trades []TradeRecord) map[string]ModeStats {
	type bucket struct {
		wins    int
		grossW  float64 // sum of positive PnL (wins)
		grossL  float64 // sum of absolute value of negative PnL (losses)
		netPnL  float64
		total   int
	}

	buckets := make(map[string]*bucket)

	for _, t := range trades {
		mode := deriveMode(t)
		b, ok := buckets[mode]
		if !ok {
			b = &bucket{}
			buckets[mode] = b
		}
		b.total++
		b.netPnL += t.PnLPercent
		if t.Won {
			b.wins++
			b.grossW += t.PnLPercent
		} else {
			b.grossL += -t.PnLPercent // store as positive magnitude
		}
	}

	out := make(map[string]ModeStats, len(buckets))
	for mode, b := range buckets {
		var winRate, pf float64
		if b.total > 0 {
			winRate = float64(b.wins) / float64(b.total)
		}
		// Profit factor = gross profit / gross loss; infinity (or max float) if no losses.
		if b.grossL > 0 {
			pf = b.grossW / b.grossL
		} else if b.grossW > 0 {
			pf = b.grossW / 0.0001 // effectively infinite — cap via caller if needed
		}
		out[mode] = ModeStats{
			Mode:         mode,
			Trades:       b.total,
			Wins:         b.wins,
			WinRate:      winRate,
			ProfitFactor: pf,
			NetPnL:       b.netPnL,
		}
	}
	return out
}

// AttributeByModeAndRegime buckets trades by both derived mode and regime.
// Returns a nested map: mode -> regime -> ModeStats.
// Trades with an empty Regime field are bucketed under the key "unknown".
func AttributeByModeAndRegime(trades []TradeRecord) map[string]map[string]ModeStats {
	type key struct{ mode, regime string }
	type bucket struct {
		wins   int
		grossW float64
		grossL float64
		netPnL float64
		total  int
	}

	buckets := make(map[key]*bucket)

	for _, t := range trades {
		mode := deriveMode(t)
		reg := t.Regime
		if reg == "" {
			reg = "unknown"
		}
		k := key{mode, reg}
		b, ok := buckets[k]
		if !ok {
			b = &bucket{}
			buckets[k] = b
		}
		b.total++
		b.netPnL += t.PnLPercent
		if t.Won {
			b.wins++
			b.grossW += t.PnLPercent
		} else {
			b.grossL += -t.PnLPercent
		}
	}

	// Build nested output map.
	out := make(map[string]map[string]ModeStats)
	for k, b := range buckets {
		if out[k.mode] == nil {
			out[k.mode] = make(map[string]ModeStats)
		}
		var winRate, pf float64
		if b.total > 0 {
			winRate = float64(b.wins) / float64(b.total)
		}
		if b.grossL > 0 {
			pf = b.grossW / b.grossL
		} else if b.grossW > 0 {
			pf = b.grossW / 0.0001
		}
		out[k.mode][k.regime] = ModeStats{
			Mode:         k.mode,
			Trades:       b.total,
			Wins:         b.wins,
			WinRate:      winRate,
			ProfitFactor: pf,
			NetPnL:       b.netPnL,
		}
	}
	return out
}
