package manifest

import "github.com/rantipay/trading-mcp/internal/config/dynamic/domain"

// sizingEntries registers the capital-relative sizing and risk parameters of
// the AI trading bot (OpenSpec change capital-relative-sizing). Every money
// value is either a percentage of the real account capital read from the
// exchange or an absolute USD floor/ceiling that bounds it, so the bot can
// trade any account size without code or YAML changes.
func sizingEntries() []Entry {
	return []Entry{
		// ── bot.sizing ──────────────────────────────────────────────────────
		{Key: "bot.sizing.mode", Type: domain.ParamTypeString, Group: "bot",
			Description: "Sizing mode: capital_pct = sizes are % of the real account capital (bounded by min/max notional); absolute = sizes are fixed USD (legacy)",
			Default:     "capital_pct", Enum: []string{"capital_pct", "absolute"}, ScopeLevels: globalOnly},
		{Key: "bot.sizing.base_pct", Type: domain.ParamTypeFloat, Group: "bot",
			Description: "Base trade notional as % of account capital when no confidence tier matches (capital_pct mode)",
			Default:     "10.0", Min: "0.1", Max: "100.0", ScopeLevels: globalOnly},
		{Key: "bot.sizing.min_notional_usd", Type: domain.ParamTypeFloat, Group: "bot",
			Description: "Floor for the computed trade notional in USD (capital_pct mode)",
			Default:     "2.0", Min: "0.5", Max: "1000.0", ScopeLevels: globalOnly},
		{Key: "bot.sizing.max_notional_usd", Type: domain.ParamTypeFloat, Group: "bot",
			Description: "Mandatory ceiling for the computed trade notional in USD (capital_pct mode). 0 or unset falls back to the default — there is no 'no ceiling'",
			Default:     "25.0", Min: "1.0", Max: "100000.0", ScopeLevels: globalOnly},

		// ── bot risk (relative) ─────────────────────────────────────────────
		{Key: "bot.max_daily_loss_usd", Type: domain.ParamTypeFloat, Group: "bot",
			Description: "Optional absolute daily loss cap in USD. The effective cap is the smaller of this and bot.max_daily_loss_percent of the day-start capital. 0 = only the percentage applies",
			Default:     "0.0", Min: "0.0", Max: "100000.0", ScopeLevels: globalOnly},
		{Key: "bot.pair_pause.net_loss_pct", Type: domain.ParamTypeFloat, Group: "bot",
			Description: "Auto-pause a pair when its net session PnL loses this % of the day-start capital",
			Default:     "0.5", Min: "0.01", Max: "20.0", ScopeLevels: globalOnly},
		{Key: "bot.drawdown.use_account_capital", Type: domain.ParamTypeBool, Group: "bot",
			Description: "Use the real day-start account capital for the intraday drawdown control instead of bot.drawdown.initial_capital_proxy",
			Default:     "true", ScopeLevels: globalOnly},

		// ── AI-02 pre-trade validator ───────────────────────────────────────
		{Key: "validation.max_risk_per_trade_pct", Type: domain.ParamTypeFloat, Group: "validation",
			Description: "AI-02: max risk per trade (entry-to-stop loss) as % of account capital",
			Default:     "1.0", Min: "0.1", Max: "10.0", ScopeLevels: globalOnly},
		{Key: "validation.max_daily_loss_pct", Type: domain.ParamTypeFloat, Group: "validation",
			Description: "AI-02 circuit breaker: max daily loss as % of account capital",
			Default:     "3.0", Min: "0.5", Max: "50.0", ScopeLevels: globalOnly},
		{Key: "validation.max_drawdown_pct", Type: domain.ParamTypeFloat, Group: "validation",
			Description: "AI-02 circuit breaker: max drawdown from peak capital in %",
			Default:     "10.0", Min: "1.0", Max: "80.0", ScopeLevels: globalOnly},
		{Key: "validation.max_stop_loss_pct", Type: domain.ParamTypeFloat, Group: "validation",
			Description: "AI-02: max stop-loss distance from entry in % of price. Also caps the stop the volatility auto-configuration may set",
			Default:     "5.0", Min: "0.5", Max: "50.0", ScopeLevels: globalOnly},
		{Key: "validation.min_risk_reward", Type: domain.ParamTypeFloat, Group: "validation",
			Description: "AI-02: minimum reward/risk ratio of the validated SL/TP",
			Default:     "1.5", Min: "1.0", Max: "10.0", ScopeLevels: globalOnly},
		{Key: "validation.min_confluence", Type: domain.ParamTypeInt, Group: "validation",
			Description: "AI-02: minimum number of confluence factors for an automatic entry",
			Default:     "3", Min: "0", Max: "10", ScopeLevels: globalOnly},

		// ── RiskSentinel ────────────────────────────────────────────────────
		{Key: "risk.max_order_size_pct", Type: domain.ParamTypeFloat, Group: "risk",
			Description: "Per-order notional cap as % of account capital, enforced by the RiskSentinel. 0 = use risk.max_order_size_usd",
			Default:     "0.0", Min: "0.0", Max: "100.0", ScopeLevels: globalOnly},
		{Key: "risk.enforce_cap_on_market_orders", Type: domain.ParamTypeBool, Group: "risk",
			Description: "Apply the per-order cap to orders that carry no price of their own (market orders), using their real USD notional from a recent reference price and the contract specification. false (registry default, so existing deployments are unchanged) = such orders are not evaluated against the cap; true = they are, and an order whose notional cannot be determined is rejected (ORDER_NOTIONAL_UNKNOWN). Reduce-only and close orders are never blocked.",
			Default:     editionDefault("false", "true"), ScopeLevels: globalOnly},
		{Key: "risk.reference_price_max_age", Type: domain.ParamTypeDuration, Group: "risk",
			Description: "Maximum age (Go duration, effective range 1s-5m; values outside are clamped) of the real price used as reference to compute the notional of an order without price. An older or missing price makes the notional unknown (rejected when the cap is enforced on market orders)",
			Default:     "10s", ScopeLevels: globalOnly},
	}
}
