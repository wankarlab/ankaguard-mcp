package manifest

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/rantipay/trading-mcp/internal/config/dynamic/domain"
	"github.com/rantipay/trading-mcp/internal/edition"
)

func TestSizingEntries_RegisteredAndValid(t *testing.T) {
	all := map[string]Entry{}
	for _, e := range All() {
		all[e.Key] = e
	}
	want := []string{
		"bot.sizing.mode", "bot.sizing.base_pct", "bot.sizing.min_notional_usd", "bot.sizing.max_notional_usd",
		"bot.max_daily_loss_usd", "bot.pair_pause.net_loss_pct", "bot.drawdown.use_account_capital",
		"validation.max_risk_per_trade_pct", "validation.max_daily_loss_pct", "validation.max_drawdown_pct",
		"validation.min_risk_reward", "validation.min_confluence", "risk.max_order_size_pct",
		"risk.enforce_cap_on_market_orders", "risk.reference_price_max_age",
	}
	for _, k := range want {
		e, ok := all[k]
		if !ok {
			t.Errorf("%s: not registered", k)
			continue
		}
		if len(e.Enum) > 0 && !slices.Contains(e.Enum, e.Default) {
			t.Errorf("%s: default %q not in enum %v", k, e.Default, e.Enum)
		}
		if e.Type == domain.ParamTypeInt || e.Type == domain.ParamTypeFloat {
			v := mustFloat(t, k, e.Default)
			if e.Min != "" && v < mustFloat(t, k, e.Min) || e.Max != "" && v > mustFloat(t, k, e.Max) {
				t.Errorf("%s: default %s outside [%s,%s]", k, e.Default, e.Min, e.Max)
			}
		}
	}
	if all["bot.sizing.mode"].Default != "capital_pct" {
		t.Errorf("bot.sizing.mode default must be capital_pct")
	}
	if all["bot.max_daily_loss_percent"].Default != "3.0" {
		t.Errorf("bot.max_daily_loss_percent default must be 3.0, got %s", all["bot.max_daily_loss_percent"].Default)
	}
	// The validator defaults must match aivalidation.DefaultConfig (1/3/10/1.5/3).
	for k, v := range map[string]string{"validation.max_risk_per_trade_pct": "1.0", "validation.max_daily_loss_pct": "3.0",
		"validation.max_drawdown_pct": "10.0", "validation.min_risk_reward": "1.5", "validation.min_confluence": "3"} {
		if all[k].Default != v {
			t.Errorf("%s default %s, want %s", k, all[k].Default, v)
		}
	}
}

func TestDynamicSizingTiers_DefaultCarriesSizePct(t *testing.T) {
	var e Entry
	for _, x := range All() {
		if x.Key == "dynamic_sizing.tiers" {
			e = x
		}
	}
	var tiers []map[string]any
	if err := json.Unmarshal([]byte(e.Default), &tiers); err != nil {
		t.Fatalf("tiers default is not JSON: %v", err)
	}
	if len(tiers) != 4 {
		t.Fatalf("want 4 tiers, got %d", len(tiers))
	}
	for _, tr := range tiers {
		pct, ok := tr["size_pct"].(float64)
		if !ok || pct <= 0 {
			t.Errorf("tier %v: size_pct missing", tr["name"])
		}
		if _, ok := tr["size_usd"]; !ok {
			t.Errorf("tier %v: size_usd must stay for absolute mode", tr["name"])
		}
	}
	if !strings.Contains(e.Description, "size_pct") {
		t.Errorf("description must document size_pct")
	}
}

// Escenarios "Despliegue existente tras actualizar" e "Instalación nueva del
// producto": el parámetro viene desactivado en la edición estándar y activado
// en la edición producto.
func TestEnforceCapOnMarketOrdersDefaultFollowsEdition(t *testing.T) {
	var found *Entry
	for _, e := range All() {
		if e.Key == "risk.enforce_cap_on_market_orders" {
			e := e
			found = &e
		}
	}
	if found == nil {
		t.Fatal("risk.enforce_cap_on_market_orders is not registered")
	}
	want := "false"
	if edition.IsProduct() {
		want = "true"
	}
	if found.Default != want {
		t.Fatalf("default = %q, want %q (product=%v)", found.Default, want, edition.IsProduct())
	}
}
