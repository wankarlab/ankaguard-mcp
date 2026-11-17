package scalping

import (
	"context"
	"fmt"
	"time"
)

// BotStatusTool implementa la tool para obtener el status de un bot
type BotStatusTool struct {
	adapter *Adapter
}

// NewBotStatusTool crea una nueva instancia de la tool
func NewBotStatusTool(adapter *Adapter) *BotStatusTool {
	return &BotStatusTool{
		adapter: adapter,
	}
}

// Name retorna el nombre de la tool
func (t *BotStatusTool) Name() string {
	return "get_scalping_bot_status"
}

// Description retorna la descripción de la tool
func (t *BotStatusTool) Description() string {
	return "Gets the current status and performance metrics of a scalping bot. " +
		"Returns detailed information including trades executed, PnL, win rate, and recent signals."
}

// InputSchema retorna el schema JSON de los parámetros de entrada
func (t *BotStatusTool) InputSchema() map[string]interface{} {
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
		},
		"required": []string{"exchange", "pair"},
	}
}

// Execute ejecuta la tool
func (t *BotStatusTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	// Parsear parámetros
	exchange := getStringParam(params, "exchange")
	pair := getStringParam(params, "pair")
	botID := getStringParam(params, "bot_id")

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

	t.adapter.GetLogger().Infof("Getting bot status for %s", botID)

	// Obtener status del bot
	bot, err := t.adapter.GetBotManager().GetBotStatus(botID)
	if err != nil {
		// Si el bot no existe, devolver estado "not running"
		t.adapter.GetLogger().Warnf("Bot %s not found: %v", botID, err)
		return &GetBotStatusResponse{
			BotID:           botID,
			Status:          "not_running",
			Exchange:        exchange,
			Pair:            pair,
			Running:         false,
			Uptime:          "0s",
			TradesToday:     0,
			PnLToday:        0.0,
			WinRate:         0.0,
			ActivePositions: 0,
			Message:         "No bot running for this pair",
		}, nil
	}

	// Calcular uptime
	uptime := time.Since(bot.StartTime)

	// Calcular win rate
	winRate := 0.0
	if bot.TotalTrades > 0 {
		winRate = float64(bot.WinningTrades) / float64(bot.TotalTrades)
	}

	// Crear respuesta
	response := &GetBotStatusResponse{
		BotID:           bot.ID,
		Status:          bot.Status,
		Exchange:        exchange,
		Pair:            pair,
		Running:         bot.Status == "running",
		Config:          bot.Config,
		StartTime:       bot.StartTime.Format(time.RFC3339),
		Uptime:          uptime.String(),
		TradesToday:     bot.TradesToday,
		PnLToday:        bot.PnLToday,
		WinRate:         winRate,
		ActivePositions: bot.ActivePositions,
		LastSignals:     bot.LastSignals,
		TotalTrades:     bot.TotalTrades,
		WinningTrades:   bot.WinningTrades,
		LosingTrades:    bot.TotalTrades - bot.WinningTrades,
		AvgProfit:       bot.AvgProfit,
		AvgLoss:         bot.AvgLoss,
		Message:         "Bot status retrieved successfully",
	}

	return response, nil
}
