package scalping

import (
	"context"
	"fmt"
)

// GetBotTradesTool implementa la tool para obtener el historial de trades del bot
type GetBotTradesTool struct {
	adapter *Adapter
}

// NewGetBotTradesTool crea una nueva instancia de la tool
func NewGetBotTradesTool(adapter *Adapter) *GetBotTradesTool {
	return &GetBotTradesTool{
		adapter: adapter,
	}
}

// Name retorna el nombre de la tool
func (t *GetBotTradesTool) Name() string {
	return "get_scalping_bot_trades"
}

// Description retorna la descripción de la tool
func (t *GetBotTradesTool) Description() string {
	return "Gets the trade history of a scalping bot. " +
		"Returns a list of executed trades with entry/exit prices, PnL, and duration. " +
		"Supports pagination with limit and offset parameters."
}

// InputSchema retorna el schema JSON de los parámetros de entrada
func (t *GetBotTradesTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name (e.g., 'kucoin', 'binance')",
			},
			"pair": map[string]interface{}{
				"type":        "string",
				"description": "Trading pair (e.g., 'BTC-USDT', 'DOGE-USDT')",
			},
			"bot_id": map[string]interface{}{
				"type":        "string",
				"description": "Bot ID (optional, will be inferred from exchange+pair if not provided)",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum number of trades to return (default: 50, max: 200)",
				"default":     50,
				"minimum":     1,
				"maximum":     200,
			},
			"offset": map[string]interface{}{
				"type":        "integer",
				"description": "Number of trades to skip for pagination (default: 0)",
				"default":     0,
				"minimum":     0,
			},
			"status": map[string]interface{}{
				"type":        "string",
				"description": "Filter by trade status (optional): 'completed', 'active', 'cancelled'",
				"enum":        []string{"completed", "active", "cancelled"},
			},
		},
		"required": []string{"exchange", "pair"},
	}
}

// Execute ejecuta la tool
func (t *GetBotTradesTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	// Parsear parámetros
	exchange := getStringParam(params, "exchange")
	pair := getStringParam(params, "pair")
	botID := getStringParam(params, "bot_id")
	status := getStringParam(params, "status")

	// Parsear limit y offset
	limit := 50
	if l, ok := params["limit"].(float64); ok {
		limit = int(l)
		if limit > 200 {
			limit = 200
		}
	}

	offset := 0
	if o, ok := params["offset"].(float64); ok {
		offset = int(o)
	}

	// Validar parámetros requeridos
	if exchange == "" {
		return nil, ErrInvalidExchange
	}
	if pair == "" {
		return nil, ErrInvalidPair
	}

	// Si no se proporciona bot_id, generarlo a partir de exchange+pair
	if botID == "" {
		botID = fmt.Sprintf("bot_%s_%s", exchange, pair)
	}

	t.adapter.GetLogger().Infof("Getting trade history for bot %s (limit=%d, offset=%d, status=%s)",
		botID, limit, offset, status)

	// Obtener trades del bot manager
	trades, totalCount, err := t.adapter.GetBotManager().GetBotTrades(botID, limit, offset, status)
	if err != nil {
		t.adapter.GetLogger().Warnf("Failed to get trades for bot %s: %v", botID, err)
		// Devolver respuesta vacía en lugar de error
		return &GetBotTradesResponse{
			Trades:     []BotTrade{},
			TotalCount: 0,
			Limit:      limit,
			Offset:     offset,
			HasMore:    false,
			Message:    "No trades found for this bot",
		}, nil
	}

	// Calcular si hay más trades
	hasMore := (offset + len(trades)) < totalCount

	response := &GetBotTradesResponse{
		Trades:     trades,
		TotalCount: totalCount,
		Limit:      limit,
		Offset:     offset,
		HasMore:    hasMore,
	}

	t.adapter.GetLogger().Infof("Retrieved %d trades (total: %d, hasMore: %v)",
		len(trades), totalCount, hasMore)

	return response, nil
}
