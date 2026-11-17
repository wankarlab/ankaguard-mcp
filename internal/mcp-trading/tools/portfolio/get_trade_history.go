package portfolio

import (
	"context"
	"time"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
)

// GetTradeHistoryToolV2 obtiene historial de trades ejecutados con filtros de tiempo
// Esta version mejorada soporta filtros de start_time y end_time
type GetTradeHistoryToolV2 struct {
	client *gctclient.Client
}

// NewGetTradeHistoryToolV2 crea una nueva instancia del tool
func NewGetTradeHistoryToolV2(client *gctclient.Client) *GetTradeHistoryToolV2 {
	return &GetTradeHistoryToolV2{client: client}
}

// Name retorna el nombre del tool
func (t *GetTradeHistoryToolV2) Name() string {
	return "get_trade_history_v2"
}

// Description retorna la descripcion del tool
func (t *GetTradeHistoryToolV2) Description() string {
	return "Get executed trade history with time range filters, including trade ID, pair, side, price, amount, fee, timestamp, and calculated P&L"
}

// InputSchema retorna el esquema de entrada del tool
func (t *GetTradeHistoryToolV2) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name (e.g., 'kucoin', 'binance')",
			},
			"pair": map[string]interface{}{
				"type":        "string",
				"description": "Trading pair filter (optional, e.g., 'BTC-USDT'). If not specified, returns trades for all pairs",
			},
			"start_time": map[string]interface{}{
				"type":        "string",
				"description": "Start time in RFC3339 format (e.g., '2024-01-01T00:00:00Z'). Default: 30 days ago",
			},
			"end_time": map[string]interface{}{
				"type":        "string",
				"description": "End time in RFC3339 format (e.g., '2024-01-31T23:59:59Z'). Default: now",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum number of trades to return (default: 100, max: 500)",
				"default":     100,
			},
		},
		"required": []string{"exchange"},
	}
}

// TradeHistoryEntry representa un trade individual con PnL calculado
type TradeHistoryEntry struct {
	ID        string    `json:"id"`
	Pair      string    `json:"pair"`
	Side      string    `json:"side"`
	Price     float64   `json:"price"`
	Amount    float64   `json:"amount"`
	Fee       float64   `json:"fee"`
	FeeCcy    string    `json:"fee_currency"`
	Timestamp time.Time `json:"timestamp"`
	PnL       float64   `json:"pnl"`
	PnLPct    float64   `json:"pnl_percent"`
	Value     float64   `json:"value"`
	OrderID   string    `json:"order_id,omitempty"`
	TradeType string    `json:"trade_type"`
}

// Execute ejecuta la obtencion del historial de trades
func (t *GetTradeHistoryToolV2) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	// Validar exchange
	exchange, ok := params["exchange"].(string)
	if !ok || exchange == "" {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "exchange is required",
		}
	}

	// Obtener pair (opcional)
	pair := ""
	if p, ok := params["pair"].(string); ok {
		pair = p
	}

	// Parsear start_time (default: 30 dias atras)
	endTime := time.Now().UTC()
	startTime := endTime.AddDate(0, 0, -30)

	if st, ok := params["start_time"].(string); ok && st != "" {
		parsed, err := time.Parse(time.RFC3339, st)
		if err != nil {
			return nil, &models.MCPError{
				Code:    -32602,
				Message: "Invalid params",
				Data:    "start_time must be in RFC3339 format (e.g., '2024-01-01T00:00:00Z')",
			}
		}
		startTime = parsed.UTC()
	}

	// Parsear end_time (default: ahora)
	if et, ok := params["end_time"].(string); ok && et != "" {
		parsed, err := time.Parse(time.RFC3339, et)
		if err != nil {
			return nil, &models.MCPError{
				Code:    -32602,
				Message: "Invalid params",
				Data:    "end_time must be in RFC3339 format (e.g., '2024-01-31T23:59:59Z')",
			}
		}
		endTime = parsed.UTC()
	}

	// Validar que start_time sea anterior a end_time
	if startTime.After(endTime) {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "start_time must be before end_time",
		}
	}

	// Obtener limit (default: 100, max: 500)
	limit := 100
	if l, ok := params["limit"]; ok {
		switch v := l.(type) {
		case int:
			limit = v
		case float64:
			limit = int(v)
		}
	}

	// Limitar a maximo 500
	if limit > 500 {
		limit = 500
	}
	if limit < 1 {
		limit = 100
	}

	// Obtener historial de trades con filtro de tiempo
	trades, err := t.client.GetTradeHistoryWithTimeFilter(ctx, exchange, pair, limit, startTime, endTime)
	if err != nil {
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Failed to get trade history",
			Data:    err.Error(),
		}
	}

	// Procesar trades para agregar PnL y formatear respuesta
	tradeEntries := make([]TradeHistoryEntry, 0, len(trades))
	var totalPnL float64
	var totalVolume float64
	var totalFees float64
	buyCount := 0
	sellCount := 0

	for _, trade := range trades {
		entry := t.parseTradeEntry(trade)
		tradeEntries = append(tradeEntries, entry)

		totalPnL += entry.PnL
		totalVolume += entry.Value
		totalFees += entry.Fee

		if entry.Side == "buy" {
			buyCount++
		} else {
			sellCount++
		}
	}

	// Calcular metricas adicionales
	avgTradeSize := 0.0
	if len(trades) > 0 {
		avgTradeSize = totalVolume / float64(len(trades))
	}

	return map[string]interface{}{
		"exchange":       exchange,
		"pair":           pair,
		"start_time":     startTime.Format(time.RFC3339),
		"end_time":       endTime.Format(time.RFC3339),
		"trades":         tradeEntries,
		"count":          len(tradeEntries),
		"limit":          limit,
		"total_pnl":      totalPnL,
		"total_volume":   totalVolume,
		"total_fees":     totalFees,
		"buy_count":      buyCount,
		"sell_count":     sellCount,
		"avg_trade_size": avgTradeSize,
		"period_days":    int(endTime.Sub(startTime).Hours() / 24),
	}, nil
}

// parseTradeEntry convierte un trade del cliente a TradeHistoryEntry
func (t *GetTradeHistoryToolV2) parseTradeEntry(trade map[string]interface{}) TradeHistoryEntry {
	entry := TradeHistoryEntry{}

	// ID
	if id, ok := trade["trade_id"].(string); ok {
		entry.ID = id
	} else if id, ok := trade["id"].(string); ok {
		entry.ID = id
	}

	// Pair
	if pair, ok := trade["pair"].(string); ok {
		entry.Pair = pair
	} else if symbol, ok := trade["symbol"].(string); ok {
		entry.Pair = symbol
	}

	// Side
	if side, ok := trade["side"].(string); ok {
		entry.Side = side
	}

	// Price
	if price, ok := trade["price"].(float64); ok {
		entry.Price = price
	}

	// Amount
	if amount, ok := trade["amount"].(float64); ok {
		entry.Amount = amount
	} else if qty, ok := trade["quantity"].(float64); ok {
		entry.Amount = qty
	}

	// Fee
	if fee, ok := trade["fee"].(float64); ok {
		entry.Fee = fee
	}

	// Fee Currency
	if feeCcy, ok := trade["fee_currency"].(string); ok {
		entry.FeeCcy = feeCcy
	}

	// Timestamp
	if ts, ok := trade["timestamp"].(time.Time); ok {
		entry.Timestamp = ts
	} else if tsStr, ok := trade["timestamp"].(string); ok {
		if parsed, err := time.Parse(time.RFC3339, tsStr); err == nil {
			entry.Timestamp = parsed
		}
	} else if tsUnix, ok := trade["timestamp"].(int64); ok {
		entry.Timestamp = time.Unix(tsUnix, 0)
	} else if tsFloat, ok := trade["timestamp"].(float64); ok {
		entry.Timestamp = time.Unix(int64(tsFloat), 0)
	}

	// Order ID (si esta disponible)
	if orderID, ok := trade["order_id"].(string); ok {
		entry.OrderID = orderID
	}

	// Trade Type
	if tradeType, ok := trade["type"].(string); ok {
		entry.TradeType = tradeType
	} else if entry.Side != "" {
		entry.TradeType = "spot" // Default
	}

	// Calcular valor del trade
	entry.Value = entry.Price * entry.Amount

	// PnL (si esta disponible en el trade)
	if pnl, ok := trade["pnl"].(float64); ok {
		entry.PnL = pnl
	} else if pnl, ok := trade["realized_pnl"].(float64); ok {
		entry.PnL = pnl
	}

	// PnL Percent
	if pnlPct, ok := trade["pnl_percent"].(float64); ok {
		entry.PnLPct = pnlPct
	} else if entry.Value > 0 && entry.PnL != 0 {
		// Calcular porcentaje si tenemos valor y pnl
		entry.PnLPct = (entry.PnL / entry.Value) * 100
	}

	return entry
}
