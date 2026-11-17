package toolpolicy

import "sort"

// paperUnsupported lists tools that cannot be served by the paper broker with a
// paper profile, with the reason. They either drive the real scalping engine
// or read state of the REAL account that the broker does not model (bot
// state, tracked positions, equity history). For a paper token they fail with
// PAPER_UNSUPPORTED before running, so they can neither reach the exchange nor
// show real account data (paper-trading-broker: "Cuenta real invisible").
var paperUnsupported = map[string]string{
	"execute_scalping_trade":         "the scalping engine trades the real account and has no paper mode",
	"start_scalping_bot":             "scalping bots trade the real account and have no paper mode",
	"stop_scalping_bot":              "scalping bots belong to the real account",
	"get_scalping_bot_status":        "scalping bots belong to the real account",
	"get_scalping_bot_performance":   "scalping bots belong to the real account",
	"get_scalping_bot_trades":        "scalping bots belong to the real account",
	"get_tracked_positions":          "tracked positions belong to the real account",
	"check_risk_limits":              "risk state is derived from the real account",
	"get_risk_state":                 "risk state is derived from the real account",
	"get_portfolio_history":          "equity history belongs to the real account",
	"get_advanced_portfolio_metrics": "equity history belongs to the real account",
	"get_portfolio_risk_metrics":     "equity history belongs to the real account",
}

// PaperUnsupportedReason reports whether tool must be rejected with
// PAPER_UNSUPPORTED for a paper profile, and why.
func PaperUnsupportedReason(tool string) (string, bool) {
	r, ok := paperUnsupported[tool]
	return r, ok
}

// PaperUnsupportedTools returns the sorted names of the tools in that set.
func PaperUnsupportedTools() []string {
	out := make([]string, 0, len(paperUnsupported))
	for n := range paperUnsupported {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// paperServedReads are read tools whose data comes from the order and account
// methods of the exchange client, which the selector serves from the paper
// broker for a paper profile. Their responses are marked as paper.
var paperServedReads = map[string]struct{}{
	"get_balance":            {},
	"get_portfolio":          {},
	"get_positions":          {},
	"get_pnl":                {},
	"get_exposure":           {},
	"get_account_info":       {},
	"get_portfolio_overview": {},
	"get_open_orders":        {},
	"get_order_history":      {},
	"get_order_status":       {},
	"get_futures_positions":  {},
	"calculate_drawdown":     {},
}

// PaperServedReadTools returns the sorted names of the read tools in that set.
func PaperServedReadTools() []string {
	out := make([]string, 0, len(paperServedReads))
	for n := range paperServedReads {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ServedByPaper reports whether, for a paper profile, the response of tool
// (with the given class) is produced from the paper broker and so must carry
// the paper marker and the execution assumptions.
func ServedByPaper(tool string, class Class) bool {
	if class == ClassTradeIncrease || class == ClassTradeReduce {
		return true
	}
	_, ok := paperServedReads[tool]
	return ok
}
