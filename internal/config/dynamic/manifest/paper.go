package manifest

import (
	"github.com/rantipay/trading-mcp/internal/config/dynamic/domain"
	"github.com/rantipay/trading-mcp/internal/edition"
)

// editionDefault returns the registry default for the compiled edition: std
// for the standard build (this deployment, unchanged) and prod for the product
// builds, which ship safe by default (design D6/D4).
func editionDefault(std, prod string) string {
	if edition.IsProduct() {
		return prod
	}
	return std
}

// paperEntries registers the paper trading broker parameters (OpenSpec change
// guarded-mcp-product, group 5). They are read on every paper operation, so a
// change applies without a restart (an initial balance change only affects
// accounts created or reset afterwards).
func paperEntries() []Entry {
	return []Entry{
		{Key: "paper.initial_balance_usd", Type: domain.ParamTypeFloat, Group: "paper",
			Description: "Starting balance in USD of a new or reset paper account",
			Default:     "1000.0", Min: "1.0", Max: "100000000.0", ScopeLevels: globalOnly},
		{Key: "paper.slippage_pct", Type: domain.ParamTypeFloat, Group: "paper",
			Description: "Adverse slippage in % applied to market and triggered-stop paper fills",
			Default:     "0.05", Min: "0.0", Max: "5.0", ScopeLevels: globalOnly},
		{Key: "paper.taker_fee_pct", Type: domain.ParamTypeFloat, Group: "paper",
			Description: "Commission in % of the notional charged on every paper fill",
			Default:     "0.06", Min: "0.0", Max: "5.0", ScopeLevels: globalOnly},
	}
}

// tradingEntries registers the product-level trading switches.
func tradingEntries() []Entry {
	return []Entry{
		{Key: "trading.live_enabled", Type: domain.ParamTypeBool, Group: "trading",
			Description: "Master switch for LIVE (real money) tool calls on /mcp. false = tools of class trade_increase and control called with the live profile are rejected with LIVE_TRADING_DISABLED; paper and read profiles, the AI bot and XSEC are not affected. Registry default true so this deployment behaves as before.",
			Default:     editionDefault("true", "false"), ScopeLevels: globalOnly},
	}
}
