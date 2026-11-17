package scalping

import (
	"context"
	"fmt"

	"github.com/rantipay/trading-mcp/internal/gct"
)

// Position is an alias for gct.Position
type Position = gct.Position

// GetFuturesPositionsTool implementa la tool para obtener posiciones de futuros
type GetFuturesPositionsTool struct {
	adapter *Adapter
}

// NewGetFuturesPositionsTool crea una nueva instancia de la tool
func NewGetFuturesPositionsTool(adapter *Adapter) *GetFuturesPositionsTool {
	return &GetFuturesPositionsTool{
		adapter: adapter,
	}
}

// Name retorna el nombre de la tool
func (t *GetFuturesPositionsTool) Name() string {
	return "get_futures_positions"
}

// Description retorna la descripción de la tool
func (t *GetFuturesPositionsTool) Description() string {
	return "Retrieves all open futures positions with real-time PnL, leverage, and liquidation information. " +
		"Returns detailed position data including entry price, current price, unrealized PnL, margin mode, and risk metrics."
}

// InputSchema retorna el schema JSON de los parámetros de entrada
func (t *GetFuturesPositionsTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name (e.g., 'kucoin')",
			},
			"market_type": map[string]interface{}{
				"type":        "string",
				"description": "Market type filter: 'spot', 'futures', 'margin', or 'options' (optional)",
				"enum":        []string{"spot", "futures", "margin", "options"},
			},
		},
		"required": []string{"exchange"},
	}
}

// Execute ejecuta la tool
func (t *GetFuturesPositionsTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	if t == nil || t.adapter == nil {
		return nil, fmt.Errorf("tool or adapter is nil")
	}

	// Parsear parámetros
	exchange := getStringParam(params, "exchange")
	if exchange == "" {
		return nil, fmt.Errorf("exchange is required")
	}

	// Parsear market_type opcional
	marketType := getStringParam(params, "market_type")
	if marketType != "" {
		// Validar market_type
		validTypes := map[string]bool{
			"spot":    true,
			"futures": true,
			"margin":  true,
			"options": true,
		}
		if !validTypes[marketType] {
			return nil, fmt.Errorf("invalid market_type: %s (must be one of: spot, futures, margin, options)", marketType)
		}
		t.adapter.GetLogger().Infof("Fetching %s positions for %s", marketType, exchange)
	} else {
		t.adapter.GetLogger().Infof("Fetching futures positions for %s", exchange)
	}

	// Obtener posiciones del GCT client
	cl, err := t.adapter.ClientFor(ctx)
	if err != nil {
		return nil, err
	}
	positions, err := cl.GetFuturesPositions(ctx, exchange)
	if err != nil {
		return nil, fmt.Errorf("failed to get futures positions: %w", err)
	}

	if positions == nil {
		positions = make([]*Position, 0)
	}

	// Filtrar por market_type si se especificó
	if marketType != "" {
		filteredPositions := make([]*Position, 0)
		for _, pos := range positions {
			if pos != nil && string(pos.MarketType) == marketType {
				filteredPositions = append(filteredPositions, pos)
			}
		}
		positions = filteredPositions
		t.adapter.GetLogger().Infof("Filtered to %d %s positions", len(positions), marketType)
	}

	// Convertir a formato de respuesta
	positionsResponse := make([]map[string]interface{}, 0, len(positions))
	totalUnrealizedPnL := 0.0

	for _, pos := range positions {
		if pos == nil {
			continue
		}

		pnlPercent := pos.CalculatePnLPercent()
		totalUnrealizedPnL += pos.UnrealizedPnL

		posMap := map[string]interface{}{
			"symbol":            pos.Symbol,
			"side":              pos.Side,
			"size":              pos.Size,
			"entry_price":       pos.EntryPrice,
			"current_price":     pos.CurrentPrice,
			"unrealized_pnl":    pos.UnrealizedPnL,
			"realized_pnl":      pos.RealizedPnL,
			"pnl_percent":       pnlPercent,
			"leverage":          pos.Leverage,
			"margin_mode":       pos.MarginMode,
			"liquidation_price": pos.LiquidationPrice,
			"margin":            pos.Margin,
			"updated_at":        pos.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
		}

		positionsResponse = append(positionsResponse, posMap)
	}

	t.adapter.GetLogger().Infof("Found %d open futures positions (total unrealized PnL: %.2f USDT)",
		len(positions), totalUnrealizedPnL)

	return map[string]interface{}{
		"exchange":             exchange,
		"positions":            positionsResponse,
		"count":                len(positions),
		"total_unrealized_pnl": totalUnrealizedPnL,
	}, nil
}
