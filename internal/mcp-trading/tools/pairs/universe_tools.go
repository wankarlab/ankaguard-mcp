package pairs

// universe_tools.go — MCP tools for user pair selection (DM-2.5).
// list_tradable_pairs, get_pair_selection, set_pair_selection.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/server"
	"github.com/rantipay/trading-mcp/internal/storage"
	"github.com/sirupsen/logrus"
)

// validModesSet is the set of accepted mode strings.
var validModesSet = map[string]bool{
	"auto":   true,
	"manual": true,
	"hybrid": true,
}

// ──────────────────────────────────────────────
// list_tradable_pairs
// ──────────────────────────────────────────────

// ListTradablePairsTool fetches live tradable pairs from an exchange via GCT.
type ListTradablePairsTool struct {
	gctClient *gctclient.Client
	logger    *logrus.Logger
}

// NewListTradablePairsTool creates the tool.
func NewListTradablePairsTool(gctClient *gctclient.Client, logger *logrus.Logger) *ListTradablePairsTool {
	return &ListTradablePairsTool{gctClient: gctClient, logger: logger}
}

func (t *ListTradablePairsTool) Name() string        { return "list_tradable_pairs" }
func (t *ListTradablePairsTool) Description() string {
	return "List live tradable pairs for an exchange. Useful to discover valid values before calling set_pair_selection."
}
func (t *ListTradablePairsTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name (default: kucoin)",
				"default":     "kucoin",
			},
			"asset_type": map[string]interface{}{
				"type":        "string",
				"description": "Asset type: spot, futures (default: futures)",
				"default":     "futures",
			},
		},
		"required": []string{},
	}
}

func (t *ListTradablePairsTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	exchange, _ := args["exchange"].(string)
	if exchange == "" {
		exchange = "kucoin"
	}
	assetType, _ := args["asset_type"].(string)
	if assetType == "" {
		assetType = "futures"
	}

	pairsSlice, err := t.gctClient.GetTradablePairs(ctx, exchange, assetType)
	if err != nil {
		return nil, fmt.Errorf("failed to get tradable pairs: %w", err)
	}

	return map[string]interface{}{
		"exchange":   exchange,
		"asset_type": assetType,
		"pairs":      pairsSlice,
		"count":      len(pairsSlice),
	}, nil
}

// ──────────────────────────────────────────────
// get_pair_selection
// ──────────────────────────────────────────────

// GetPairSelectionTool reads the persisted user pair selection from the DB.
type GetPairSelectionTool struct {
	repo   *storage.UserPairSelectionRepository
	logger *logrus.Logger
}

// NewGetPairSelectionTool creates the tool.
func NewGetPairSelectionTool(repo *storage.UserPairSelectionRepository, logger *logrus.Logger) *GetPairSelectionTool {
	return &GetPairSelectionTool{repo: repo, logger: logger}
}

func (t *GetPairSelectionTool) Name() string        { return "get_pair_selection" }
func (t *GetPairSelectionTool) Description() string {
	return "Get the persisted user pair selection (mode + pairs) for an exchange."
}
func (t *GetPairSelectionTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name (default: kucoin)",
				"default":     "kucoin",
			},
		},
		"required": []string{},
	}
}

func (t *GetPairSelectionTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	exchange, _ := args["exchange"].(string)
	if exchange == "" {
		exchange = "kucoin"
	}

	sel, err := t.repo.Get(ctx, "default", exchange)
	if err != nil {
		return nil, fmt.Errorf("failed to get pair selection: %w", err)
	}

	return map[string]interface{}{
		"exchange":  sel.Exchange,
		"mode":      sel.Mode,
		"pairs":     sel.Pairs,
		"overrides": sel.Overrides,
	}, nil
}

// ──────────────────────────────────────────────
// set_pair_selection
// ──────────────────────────────────────────────

// SetPairSelectionTool persists a user pair selection; validates pairs against
// live tradable pairs for manual/hybrid modes.
type SetPairSelectionTool struct {
	gctClient *gctclient.Client
	repo      *storage.UserPairSelectionRepository
	assetType string
	logger    *logrus.Logger
}

// NewSetPairSelectionTool creates the tool.
func NewSetPairSelectionTool(
	gctClient *gctclient.Client,
	repo *storage.UserPairSelectionRepository,
	assetType string,
	logger *logrus.Logger,
) *SetPairSelectionTool {
	if assetType == "" {
		assetType = "futures"
	}
	return &SetPairSelectionTool{gctClient: gctClient, repo: repo, assetType: assetType, logger: logger}
}

func (t *SetPairSelectionTool) Name() string        { return "set_pair_selection" }
func (t *SetPairSelectionTool) Description() string {
	return "Persist a user pair selection (mode + pairs) for an exchange. Mode must be auto, manual, or hybrid. For manual/hybrid, pairs are validated against live tradable pairs."
}
func (t *SetPairSelectionTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name (default: kucoin)",
				"default":     "kucoin",
			},
			"mode": map[string]interface{}{
				"type":        "string",
				"description": "Universe mode: auto, manual, or hybrid",
				"enum":        []string{"auto", "manual", "hybrid"},
			},
			"pairs": map[string]interface{}{
				"type":        "array",
				"description": "List of trading pairs (required for manual/hybrid modes)",
				"items":       map[string]interface{}{"type": "string"},
			},
		},
		"required": []string{"mode"},
	}
}

func (t *SetPairSelectionTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	exchange, _ := args["exchange"].(string)
	if exchange == "" {
		exchange = "kucoin"
	}
	mode, _ := args["mode"].(string)
	if !validModesSet[mode] {
		return nil, fmt.Errorf("mode must be one of: auto, manual, hybrid; got %q", mode)
	}

	// Parse pairs array robustly.
	var pairsSlice []string
	if raw, ok := args["pairs"]; ok && raw != nil {
		// Could be []interface{} from JSON decoding.
		switch v := raw.(type) {
		case []string:
			pairsSlice = v
		case []interface{}:
			for _, item := range v {
				if s, ok := item.(string); ok {
					pairsSlice = append(pairsSlice, s)
				}
			}
		default:
			// Fallback: marshal + unmarshal.
			b, err := json.Marshal(raw)
			if err == nil {
				_ = json.Unmarshal(b, &pairsSlice)
			}
		}
	}
	if pairsSlice == nil {
		pairsSlice = []string{}
	}

	// Validate pairs against live tradable pairs for manual/hybrid.
	if mode == "manual" || mode == "hybrid" {
		tradable, err := t.gctClient.GetTradablePairs(ctx, exchange, t.assetType)
		if err != nil {
			t.logger.WithError(err).Warn("Could not fetch tradable pairs for validation — skipping")
		} else {
			tradableSet := make(map[string]struct{}, len(tradable))
			for _, p := range tradable {
				tradableSet[p] = struct{}{}
			}
			var rejected []string
			for _, p := range pairsSlice {
				if _, ok := tradableSet[p]; !ok {
					rejected = append(rejected, p)
				}
			}
			if len(rejected) > 0 {
				return nil, fmt.Errorf("unknown pairs (not tradable on %s): %v", exchange, rejected)
			}
		}
	}

	sel := &storage.UserPairSelection{
		UserID:    "default",
		Exchange:  exchange,
		Mode:      mode,
		Pairs:     pairsSlice,
		Overrides: make(map[string]any),
	}
	if err := t.repo.Upsert(ctx, sel); err != nil {
		return nil, fmt.Errorf("failed to save pair selection: %w", err)
	}

	t.logger.WithFields(logrus.Fields{
		"exchange": exchange,
		"mode":     mode,
		"pairs":    len(pairsSlice),
	}).Info("Pair selection updated via MCP tool")

	return map[string]interface{}{
		"exchange": exchange,
		"mode":     mode,
		"pairs":    pairsSlice,
	}, nil
}

// ──────────────────────────────────────────────
// Registration
// ──────────────────────────────────────────────

// RegisterUniverseTools registers the 3 universe selection MCP tools.
// It is called from the existing RegisterAll entry point.
func RegisterUniverseTools(
	mcpServer *server.MCPServer,
	gctClient *gctclient.Client,
	repo *storage.UserPairSelectionRepository,
	assetType string,
	logger *logrus.Logger,
) error {
	tools := []server.Tool{
		NewListTradablePairsTool(gctClient, logger),
		NewGetPairSelectionTool(repo, logger),
		NewSetPairSelectionTool(gctClient, repo, assetType, logger),
	}

	for _, tool := range tools {
		if err := mcpServer.RegisterTool(tool); err != nil {
			return fmt.Errorf("failed to register tool %s: %w", tool.Name(), err)
		}
	}

	logger.WithField("count", len(tools)).Info("Registered universe pair selection MCP tools")
	return nil
}
