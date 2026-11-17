package toolpolicy

import "sort"

// Entry is one row of the classification table.
type Entry struct {
	Name  string
	Class Class
	// Note explains non-obvious decisions (shown in docs/product/tool-classification.md).
	Note string
	// Origin is only used by originTable (see origin.go); empty in the class table.
	Origin Origin
}

func e(name string, c Class, note string) Entry { return Entry{Name: name, Class: c, Note: note} }

// table is the complete classification. Keep it sorted by name. The test
// suite fails when it drifts from the registered tools (see
// internal/mcp-trading/toolinventory).
var table = []Entry{
	e("activate_kill_switch", ClassSafety, "Activates the emergency stop."),
	e("analyze_scalping_opportunity", ClassRead, ""),
	e("backtest_strategy", ClassRead, "Replays a strategy over real candles in memory; no exchange, account or config effect."),
	e("batch_convert_trading_pairs", ClassRead, ""),
	e("calculate_adx", ClassRead, ""),
	e("calculate_atr", ClassRead, ""),
	e("calculate_bollinger", ClassRead, ""),
	e("calculate_cci", ClassRead, ""),
	e("calculate_drawdown", ClassRead, ""),
	e("calculate_ema", ClassRead, ""),
	e("calculate_fibonacci", ClassRead, ""),
	e("calculate_funding_costs", ClassRead, ""),
	e("calculate_ichimoku", ClassRead, ""),
	e("calculate_liquidation_price", ClassRead, ""),
	e("calculate_macd", ClassRead, ""),
	e("calculate_macd_futures", ClassRead, ""),
	e("calculate_mfi", ClassRead, ""),
	e("calculate_obv", ClassRead, ""),
	e("calculate_position_size", ClassRead, ""),
	e("calculate_rsi", ClassRead, ""),
	e("calculate_rsi_futures", ClassRead, ""),
	e("calculate_sma", ClassRead, ""),
	e("calculate_stochastic", ClassRead, ""),
	e("calculate_stop_loss", ClassRead, ""),
	e("calculate_take_profit", ClassRead, ""),
	e("calculate_volatility", ClassRead, ""),
	e("calculate_vwap", ClassRead, ""),
	e("calculate_williams_r", ClassRead, ""),
	e("cancel_all_orders", ClassTradeReduce, "Cancels open orders."),
	e("cancel_order", ClassTradeReduce, "Cancels an open order."),
	e("check_risk_limits", ClassRead, ""),
	e("close_futures_position", ClassTradeReduce, "Reduce-only market order that closes a position."),
	e("close_position_direct", ClassTradeReduce, "Closes a futures position through the direct KuCoin REST client."),
	e("configure_notifications", ClassControl, "Changes push notification settings."),
	e("convert_trading_pair", ClassRead, ""),
	e("create_funding_rate_alert", ClassControl, "Creates an alert (configuration)."),
	e("create_trailing_stop", ClassTradeReduce, "Places a reduce-only protective stop (ReduceOnly is forced to true in the tool)."),
	e("deactivate_kill_switch", ClassControl, "Lifts a safety stop: must not be available to read or paper profiles."),
	e("execute_scalping_trade", ClassTradeIncrease, "Opens a scalping trade."),
	e("get_24h_stats", ClassRead, ""),
	e("get_account_info", ClassRead, ""),
	e("get_active_subscriptions", ClassRead, ""),
	e("get_advanced_portfolio_metrics", ClassRead, ""),
	e("get_balance", ClassRead, ""),
	e("get_candles", ClassRead, ""),
	e("get_correlation", ClassRead, ""),
	e("get_exchange_info", ClassRead, ""),
	e("get_exposure", ClassRead, ""),
	e("get_funding_rate", ClassRead, ""),
	e("get_futures_positions", ClassRead, ""),
	e("get_index_price", ClassRead, ""),
	e("get_mark_price", ClassRead, ""),
	e("get_market_types", ClassRead, ""),
	e("get_markets", ClassRead, ""),
	e("get_notification_preferences", ClassRead, ""),
	e("get_open_orders", ClassRead, ""),
	e("get_order_history", ClassRead, ""),
	e("get_order_status", ClassRead, ""),
	e("get_orderbook", ClassRead, ""),
	e("get_pair_selection", ClassRead, ""),
	e("get_pnl", ClassRead, ""),
	e("get_portfolio", ClassRead, ""),
	e("get_portfolio_history", ClassRead, ""),
	e("get_portfolio_overview", ClassRead, ""),
	e("get_portfolio_risk_metrics", ClassRead, ""),
	e("get_positions", ClassRead, ""),
	e("get_rebalance_recommendations", ClassRead, "Recommendations only; it does not place orders."),
	e("get_risk_state", ClassRead, ""),
	e("get_scalping_bot_performance", ClassRead, ""),
	e("get_scalping_bot_status", ClassRead, ""),
	e("get_scalping_bot_trades", ClassRead, ""),
	e("get_scalping_signals", ClassRead, ""),
	e("get_supported_pair_formats", ClassRead, ""),
	e("get_ticker", ClassRead, ""),
	e("get_tracked_positions", ClassRead, ""),
	e("get_trade_history", ClassRead, ""),
	e("get_trade_history_v2", ClassRead, ""),
	e("get_trades", ClassRead, ""),
	e("get_trading_mode", ClassRead, ""),
	e("get_websocket_status", ClassRead, ""),
	e("launch_gate_verdict", ClassRead, ""),
	e("launch_get_position", ClassRead, ""),
	e("launch_list_opportunities", ClassRead, ""),
	e("launch_status", ClassRead, ""),
	e("list_tradable_pairs", ClassRead, ""),
	e("llm_analyze_market", ClassRead, "Spends LLM credits but has no trading or configuration effect."),
	e("llm_get_provider_status", ClassRead, ""),
	e("manage_user_subscriptions", ClassControl, "Reads and writes per-user subscription preferences."),
	e("modify_order", ClassTradeIncrease, "Changing price or amount can enlarge exposure; already blocked by the global stop."),
	e("register_fcm_token", ClassControl, "Registers a device for push notifications."),
	e("set_notification_preferences", ClassControl, ""),
	e("set_pair_selection", ClassControl, "Changes the persisted trading universe."),
	e("set_trading_mode", ClassControl, "Changes market type / mode selection."),
	e("simulate_order", ClassRead, "Hypothetical execution; stores nothing and never reaches the exchange."),
	e("start_scalping_bot", ClassTradeIncrease, "Starts an autonomous trading loop."),
	e("stop_scalping_bot", ClassTradeReduce, "Stops a bot and can close its positions. Doubtful: could also be safety."),
	e("submit_futures_order_direct", ClassTradeIncrease, "Class depends on arguments: reduce_only or close_order true makes the call trade_reduce."),
	e("submit_limit_order", ClassTradeIncrease, ""),
	e("submit_market_order", ClassTradeIncrease, ""),
	e("submit_order", ClassTradeIncrease, "Its reduce_only flag is NOT trusted for classification (kept as today: blocked by the global stop)."),
	e("submit_stop_order", ClassTradeIncrease, ""),
	e("subscribe_market_data", ClassControl, "Subscribe/unsubscribe changes server-side WebSocket state."),
	e("test_notification", ClassControl, "Sends a notification."),
	e("update_sltp", ClassControl, "Edits the SL/TP tracked by the SL/TP executor (local position and DB stop, no exchange order is modified), but a looser stop raises risk. Doubtful."),
	e("validate_trade", ClassRead, ""),
}

var byName = func() map[string]Entry {
	m := make(map[string]Entry, len(table))
	for _, en := range table {
		if _, dup := m[en.Name]; dup {
			panic("toolpolicy: duplicate classification for " + en.Name)
		}
		m[en.Name] = en
	}
	return m
}()

// argClassifiers refine the class of a tool from the call arguments. They may
// only be used for tools whose base class is trade_increase.
var argClassifiers = map[string]func(args map[string]interface{}) Class{
	"submit_futures_order_direct": reduceOnlyOrIncrease,
}

// reduceOnlyOrIncrease returns trade_reduce when the call is marked reduce-only
// or close-position, and trade_increase for anything else (missing or
// ambiguous arguments included).
func reduceOnlyOrIncrease(args map[string]interface{}) Class {
	if IsReduceOnlyArgs(args) {
		return ClassTradeReduce
	}
	return ClassTradeIncrease
}

// IsReduceOnlyArgs reports whether order arguments are explicitly de-risking:
// reduce_only or close_order set to the JSON boolean true. Strings or numbers
// are deliberately not accepted.
func IsReduceOnlyArgs(args map[string]interface{}) bool {
	for _, k := range []string{"reduce_only", "close_order"} {
		if v, ok := args[k].(bool); ok && v {
			return true
		}
	}
	return false
}

// ClassOf returns the base class of a tool. ok is false for an unclassified
// tool, which must be neither listed nor executed.
func ClassOf(tool string) (Class, bool) {
	en, ok := byName[tool]
	return en.Class, ok
}

// ClassForCall returns the class of one specific call, taking the arguments
// into account for tools that can either open or reduce exposure.
func ClassForCall(tool string, args map[string]interface{}) (Class, bool) {
	c, ok := ClassOf(tool)
	if !ok {
		return "", false
	}
	if f, has := argClassifiers[tool]; has {
		return f(args), true
	}
	return c, true
}

// Entries returns a copy of the classification table sorted by tool name.
func Entries() []Entry {
	out := make([]Entry, len(table))
	copy(out, table)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Names returns every classified tool name, sorted.
func Names() []string {
	out := make([]string, 0, len(table))
	for _, en := range table {
		out = append(out, en.Name)
	}
	sort.Strings(out)
	return out
}

// ToolsOfClass returns the set of tools whose base class is c.
func ToolsOfClass(c Class) map[string]bool {
	out := make(map[string]bool)
	for _, en := range table {
		if en.Class == c {
			out[en.Name] = true
		}
	}
	return out
}

// HasArgClassifier reports whether the class of the tool depends on arguments.
func HasArgClassifier(tool string) bool {
	_, ok := argClassifiers[tool]
	return ok
}
