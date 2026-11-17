package toolpolicy

import "sort"

// Origin says where the data of a tool response comes from. It is declared
// next to the class so the generated catalog (docs/product/tools.md) can show
// it, and so that no tool returns data without a declared source (change
// guarded-mcp-product, capability mcp-tool-honesty).
type Origin string

const (
	// OriginExchange: values read from the exchange (through GoCryptoTrader or
	// the direct REST client), or an action executed there.
	OriginExchange Origin = "exchange"
	// OriginLocalRegistry: facts recorded locally (snapshots, trades, bots,
	// risk state, configuration, subscriptions).
	OriginLocalRegistry Origin = "local_registry"
	// OriginCalculation: deterministic calculation over real data or over the
	// values the caller passes in (indicators, sizing, format conversion).
	OriginCalculation Origin = "calculation"
	// OriginEstimate: projection, simulation or model output. The response
	// says so (estimate: true) and lists its assumptions.
	OriginEstimate Origin = "estimate"
)

// Origins lists every valid origin in a stable order.
var Origins = []Origin{OriginExchange, OriginLocalRegistry, OriginCalculation, OriginEstimate}

func o(name string, origin Origin) Entry { return Entry{Name: name, Origin: origin} }

// originTable declares the origin of every tool. Keep it sorted by name. The
// test suite fails when it drifts from the registered tools.
var originTable = []Entry{
	o("activate_kill_switch", OriginLocalRegistry),
	o("analyze_scalping_opportunity", OriginCalculation),
	o("backtest_strategy", OriginEstimate),
	o("batch_convert_trading_pairs", OriginCalculation),
	o("calculate_adx", OriginCalculation),
	o("calculate_atr", OriginCalculation),
	o("calculate_bollinger", OriginCalculation),
	o("calculate_cci", OriginCalculation),
	o("calculate_drawdown", OriginCalculation),
	o("calculate_ema", OriginCalculation),
	o("calculate_fibonacci", OriginCalculation),
	o("calculate_funding_costs", OriginEstimate),
	o("calculate_ichimoku", OriginCalculation),
	o("calculate_liquidation_price", OriginEstimate),
	o("calculate_macd", OriginCalculation),
	o("calculate_macd_futures", OriginCalculation),
	o("calculate_mfi", OriginCalculation),
	o("calculate_obv", OriginCalculation),
	o("calculate_position_size", OriginCalculation),
	o("calculate_rsi", OriginCalculation),
	o("calculate_rsi_futures", OriginCalculation),
	o("calculate_sma", OriginCalculation),
	o("calculate_stochastic", OriginCalculation),
	o("calculate_stop_loss", OriginCalculation),
	o("calculate_take_profit", OriginCalculation),
	o("calculate_volatility", OriginCalculation),
	o("calculate_vwap", OriginCalculation),
	o("calculate_williams_r", OriginCalculation),
	o("cancel_all_orders", OriginExchange),
	o("cancel_order", OriginExchange),
	o("check_risk_limits", OriginLocalRegistry),
	o("close_futures_position", OriginExchange),
	o("close_position_direct", OriginExchange),
	o("configure_notifications", OriginLocalRegistry),
	o("convert_trading_pair", OriginCalculation),
	o("create_funding_rate_alert", OriginLocalRegistry),
	o("create_trailing_stop", OriginExchange),
	o("deactivate_kill_switch", OriginLocalRegistry),
	o("execute_scalping_trade", OriginExchange),
	o("get_24h_stats", OriginExchange),
	o("get_account_info", OriginExchange),
	o("get_active_subscriptions", OriginLocalRegistry),
	o("get_advanced_portfolio_metrics", OriginLocalRegistry),
	o("get_balance", OriginExchange),
	o("get_candles", OriginExchange),
	o("get_correlation", OriginCalculation),
	o("get_exchange_info", OriginExchange),
	o("get_exposure", OriginLocalRegistry),
	o("get_funding_rate", OriginExchange),
	o("get_futures_positions", OriginExchange),
	o("get_index_price", OriginExchange),
	o("get_mark_price", OriginExchange),
	o("get_market_types", OriginCalculation),
	o("get_markets", OriginExchange),
	o("get_notification_preferences", OriginLocalRegistry),
	o("get_open_orders", OriginExchange),
	o("get_order_history", OriginExchange),
	o("get_order_status", OriginExchange),
	o("get_orderbook", OriginExchange),
	o("get_pair_selection", OriginLocalRegistry),
	o("get_pnl", OriginExchange),
	o("get_portfolio", OriginExchange),
	o("get_portfolio_history", OriginLocalRegistry),
	o("get_portfolio_overview", OriginExchange),
	o("get_portfolio_risk_metrics", OriginLocalRegistry),
	o("get_positions", OriginExchange),
	o("get_rebalance_recommendations", OriginEstimate),
	o("get_risk_state", OriginLocalRegistry),
	o("get_scalping_bot_performance", OriginLocalRegistry),
	o("get_scalping_bot_status", OriginLocalRegistry),
	o("get_scalping_bot_trades", OriginLocalRegistry),
	o("get_scalping_signals", OriginCalculation),
	o("get_supported_pair_formats", OriginCalculation),
	o("get_ticker", OriginExchange),
	o("get_tracked_positions", OriginLocalRegistry),
	o("get_trade_history", OriginExchange),
	o("get_trade_history_v2", OriginExchange),
	o("get_trades", OriginExchange),
	o("get_trading_mode", OriginLocalRegistry),
	o("get_websocket_status", OriginLocalRegistry),
	o("launch_gate_verdict", OriginLocalRegistry),
	o("launch_get_position", OriginLocalRegistry),
	o("launch_list_opportunities", OriginLocalRegistry),
	o("launch_status", OriginLocalRegistry),
	o("list_tradable_pairs", OriginExchange),
	o("llm_analyze_market", OriginEstimate),
	o("llm_get_provider_status", OriginLocalRegistry),
	o("manage_user_subscriptions", OriginLocalRegistry),
	o("modify_order", OriginExchange),
	o("register_fcm_token", OriginLocalRegistry),
	o("set_notification_preferences", OriginLocalRegistry),
	o("set_pair_selection", OriginLocalRegistry),
	o("set_trading_mode", OriginLocalRegistry),
	o("simulate_order", OriginEstimate),
	o("start_scalping_bot", OriginLocalRegistry),
	o("stop_scalping_bot", OriginLocalRegistry),
	o("submit_futures_order_direct", OriginExchange),
	o("submit_limit_order", OriginExchange),
	o("submit_market_order", OriginExchange),
	o("submit_order", OriginExchange),
	o("submit_stop_order", OriginExchange),
	o("subscribe_market_data", OriginLocalRegistry),
	o("test_notification", OriginLocalRegistry),
	o("update_sltp", OriginLocalRegistry),
	o("validate_trade", OriginCalculation),
}

var originByName = func() map[string]Origin {
	m := make(map[string]Origin, len(originTable))
	for _, en := range originTable {
		if _, dup := m[en.Name]; dup {
			panic("toolpolicy: duplicate origin for " + en.Name)
		}
		m[en.Name] = en.Origin
	}
	return m
}()

// OriginOf returns the declared data origin of a tool; ok is false when the tool has none.
func OriginOf(tool string) (Origin, bool) {
	o, ok := originByName[tool]
	return o, ok
}

// OriginNames returns every tool with a declared origin, sorted.
func OriginNames() []string {
	out := make([]string, 0, len(originTable))
	for _, en := range originTable {
		out = append(out, en.Name)
	}
	sort.Strings(out)
	return out
}
