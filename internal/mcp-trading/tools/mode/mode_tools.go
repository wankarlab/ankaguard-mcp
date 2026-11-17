// Package modetools implements MCP tools for the selectable trading-mode control
// surface (Wave 2-A: manual/hybrid). Tools: get_trading_mode, set_trading_mode.
package modetools

import (
	"context"
	"fmt"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/server"
	"github.com/rantipay/trading-mcp/internal/storage"
	"github.com/sirupsen/logrus"
)

// ──────────────────────────────────────────────
// get_trading_mode
// ──────────────────────────────────────────────

// GetTradingModeTool reads the effective trading mode from the DB.
// When a pair is supplied and has a per-pair override that row is returned;
// otherwise the global row is returned.
type GetTradingModeTool struct {
	repo   *storage.TradingModeRepository
	logger *logrus.Logger
}

// NewGetTradingModeTool creates the tool.
func NewGetTradingModeTool(repo *storage.TradingModeRepository, logger *logrus.Logger) *GetTradingModeTool {
	return &GetTradingModeTool{repo: repo, logger: logger}
}

func (t *GetTradingModeTool) Name() string        { return "get_trading_mode" }
func (t *GetTradingModeTool) Description() string {
	return "Get the effective trading mode (market_type, mode_selection, leverage). " +
		"If pair is supplied and a per-pair override exists, that override is returned; " +
		"otherwise the global mode is returned."
}
func (t *GetTradingModeTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"scope": map[string]interface{}{
				"type":        "string",
				"description": "Scope to query: global or pair (default: global)",
				"enum":        []string{"global", "pair"},
				"default":     "global",
			},
			"pair": map[string]interface{}{
				"type":        "string",
				"description": "Trading pair (required when scope=pair, e.g. BTC-USDT)",
			},
		},
		"required": []string{},
	}
}

func (t *GetTradingModeTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	scope, _ := args["scope"].(string)
	pair, _ := args["pair"].(string)
	if scope == "" {
		scope = "global"
	}

	if scope == "pair" {
		if pair == "" {
			return nil, fmt.Errorf("pair is required when scope=pair")
		}
		override, err := t.repo.GetForPair(ctx, pair)
		if err != nil {
			return nil, fmt.Errorf("failed to get per-pair trading mode: %w", err)
		}
		if override != nil {
			return tradingModeToMap(override), nil
		}
		// Fall through to global when no per-pair row exists.
	}

	global, err := t.repo.GetGlobal(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get global trading mode: %w", err)
	}
	result := tradingModeToMap(global)
	if scope == "pair" && pair != "" {
		result["note"] = fmt.Sprintf("no per-pair override for %q; showing global", pair)
	}
	return result, nil
}

// ──────────────────────────────────────────────
// set_trading_mode
// ──────────────────────────────────────────────

// SetTradingModeTool persists a trading mode setting (global or per-pair).
type SetTradingModeTool struct {
	repo   *storage.TradingModeRepository
	logger *logrus.Logger
}

// NewSetTradingModeTool creates the tool.
func NewSetTradingModeTool(repo *storage.TradingModeRepository, logger *logrus.Logger) *SetTradingModeTool {
	return &SetTradingModeTool{repo: repo, logger: logger}
}

func (t *SetTradingModeTool) Name() string        { return "set_trading_mode" }
func (t *SetTradingModeTool) Description() string {
	return "Set the trading mode. " +
		"mode=auto resets to automatic selection (mode_selection=auto). " +
		"mode=spot|futures|margin sets market_type explicitly (mode_selection=manual). " +
		"mode=hedge is rejected (not supported). " +
		"scope=global applies to all pairs; scope=pair targets a specific pair (pair required). " +
		"leverage is optional; spot enforces leverage<=1."
}
func (t *SetTradingModeTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"mode": map[string]interface{}{
				"type":        "string",
				"description": "Trading mode: auto (reset to automatic), spot, futures, margin. hedge is not supported.",
				"enum":        []string{"auto", "spot", "futures", "margin", "hedge"},
			},
			"scope": map[string]interface{}{
				"type":        "string",
				"description": "Scope: global (all pairs) or pair (single pair override)",
				"enum":        []string{"global", "pair"},
				"default":     "global",
			},
			"pair": map[string]interface{}{
				"type":        "string",
				"description": "Trading pair (required when scope=pair, e.g. BTC-USDT)",
			},
			"leverage": map[string]interface{}{
				"type":        "number",
				"description": "Desired leverage multiplier (0 = use default). spot enforces <=1.",
				"default":     0,
			},
		},
		"required": []string{"mode"},
	}
}

func (t *SetTradingModeTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	mode, _ := args["mode"].(string)
	scope, _ := args["scope"].(string)
	pair, _ := args["pair"].(string)
	if scope == "" {
		scope = "global"
	}

	// leverage is optional; JSON numbers come as float64.
	var leverage float64
	if lv, ok := args["leverage"].(float64); ok {
		leverage = lv
	}

	if mode == "hedge" {
		return nil, fmt.Errorf("hedge mode is not supported in this version")
	}

	// Derive marketType and modeSelection from the mode parameter.
	marketType, modeSelection, err := parseModeParam(mode)
	if err != nil {
		return nil, err
	}

	if scope == "global" {
		if err := t.repo.SetGlobal(ctx, marketType, modeSelection, leverage); err != nil {
			return nil, fmt.Errorf("failed to set global trading mode: %w", err)
		}
		t.logger.WithFields(logrus.Fields{
			"market_type":    marketType,
			"mode_selection": modeSelection,
			"leverage":       leverage,
		}).Info("Global trading mode updated via MCP tool")
		return map[string]interface{}{
			"scope":          "global",
			"market_type":    marketType,
			"mode_selection": modeSelection,
			"leverage":       leverage,
		}, nil
	}

	// scope == "pair"
	if pair == "" {
		return nil, fmt.Errorf("pair is required when scope=pair")
	}
	if modeSelection == "auto" {
		// "auto" for a per-pair scope means delete the override (revert to global).
		if err := t.repo.DeleteForPair(ctx, pair); err != nil {
			return nil, fmt.Errorf("failed to revert per-pair trading mode: %w", err)
		}
		t.logger.WithField("pair", pair).Info("Per-pair trading mode override reverted to global via MCP tool")
		return map[string]interface{}{
			"scope": "pair",
			"pair":  pair,
			"note":  "per-pair override removed; global settings will apply",
		}, nil
	}
	if err := t.repo.SetForPair(ctx, pair, marketType, leverage); err != nil {
		return nil, fmt.Errorf("failed to set per-pair trading mode: %w", err)
	}
	t.logger.WithFields(logrus.Fields{
		"pair":        pair,
		"market_type": marketType,
		"leverage":    leverage,
	}).Info("Per-pair trading mode updated via MCP tool")
	return map[string]interface{}{
		"scope":          "pair",
		"pair":           pair,
		"market_type":    marketType,
		"mode_selection": "manual",
		"leverage":       leverage,
	}, nil
}

// ──────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────

// parseModeParam converts the user-facing "mode" parameter into (marketType, modeSelection).
// "auto" → ("futures", "auto")  — market_type retains the stored value; auto disables manual.
// "spot" / "futures" / "margin" → (same, "manual")
func parseModeParam(mode string) (marketType, modeSelection string, err error) {
	switch mode {
	case "auto":
		return "futures", "auto", nil
	case "spot", "futures", "margin":
		return mode, "manual", nil
	default:
		return "", "", fmt.Errorf("mode must be one of auto/spot/futures/margin; got %q", mode)
	}
}

// tradingModeToMap converts a UserTradingMode to a generic map for MCP responses.
func tradingModeToMap(m *storage.UserTradingMode) map[string]interface{} {
	return map[string]interface{}{
		"scope":          m.Scope,
		"pair":           m.Pair,
		"market_type":    m.MarketType,
		"mode_selection": m.ModeSelection,
		"leverage":       m.Leverage,
		"updated_at":     m.UpdatedAt,
	}
}

// ──────────────────────────────────────────────
// Registration
// ──────────────────────────────────────────────

// RegisterModeTools registers get_trading_mode and set_trading_mode on the MCP server.
// Mirror of RegisterUniverseTools in the pairs package.
func RegisterModeTools(
	mcpServer *server.MCPServer,
	repo *storage.TradingModeRepository,
	logger *logrus.Logger,
) error {
	tools := []server.Tool{
		NewGetTradingModeTool(repo, logger),
		NewSetTradingModeTool(repo, logger),
	}

	for _, tool := range tools {
		if err := mcpServer.RegisterTool(tool); err != nil {
			return fmt.Errorf("failed to register tool %s: %w", tool.Name(), err)
		}
	}

	logger.WithField("count", len(tools)).Info("Registered trading mode MCP tools")
	return nil
}
