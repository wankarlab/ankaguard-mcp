package scalping

import (
	"context"
	"fmt"
	"time"
)

// StopBotTool implementa la tool para detener un bot de scalping
type StopBotTool struct {
	adapter *Adapter
}

// NewStopBotTool crea una nueva instancia de la tool
func NewStopBotTool(adapter *Adapter) *StopBotTool {
	return &StopBotTool{
		adapter: adapter,
	}
}

// Name retorna el nombre de la tool
func (t *StopBotTool) Name() string {
	return "stop_scalping_bot"
}

// Description retorna la descripción de la tool
func (t *StopBotTool) Description() string {
	return "Stops a running scalping bot. Optionally closes all open positions. " +
		"Returns final statistics including total trades, PnL, and win rate."
}

// InputSchema retorna el schema JSON de los parámetros de entrada
func (t *StopBotTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"bot_id": map[string]interface{}{
				"type":        "string",
				"description": "Bot ID to stop",
			},
			"close_positions": map[string]interface{}{
				"type":        "boolean",
				"description": "Whether to close all open positions (default: false)",
				"default":     false,
			},
		},
		"required": []string{"bot_id"},
	}
}

// Execute ejecuta la tool
func (t *StopBotTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	// Parsear parámetros
	req := StopBotRequest{
		BotID:          getStringParam(params, "bot_id"),
		ClosePositions: getBoolParam(params, "close_positions"),
	}

	if req.BotID == "" {
		return nil, fmt.Errorf("bot_id is required")
	}

	t.adapter.GetLogger().Infof("Stopping bot %s (close_positions: %v)", req.BotID, req.ClosePositions)

	// Obtener status antes de detener
	bot, err := t.adapter.GetBotManager().GetBotStatus(req.BotID)
	if err != nil {
		return nil, err
	}

	// Detener bot
	if err := t.adapter.GetBotManager().StopBot(req.BotID, req.ClosePositions); err != nil {
		return nil, fmt.Errorf("failed to stop bot: %w", err)
	}

	// Calcular win rate
	winRate := 0.0
	if bot.TotalTrades > 0 {
		winRate = float64(bot.WinningTrades) / float64(bot.TotalTrades)
	}

	// Crear respuesta
	response := &StopBotResponse{
		BotID:       req.BotID,
		Status:      "stopped",
		StoppedAt:   time.Now().Format(time.RFC3339),
		TotalTrades: bot.TotalTrades,
		TotalPnL:    bot.PnLToday,
		WinRate:     winRate,
	}

	if req.ClosePositions {
		response.ClosedPositions = bot.ActivePositions
	}

	t.adapter.GetLogger().Infof("Bot %s stopped successfully (trades: %d, PnL: %.2f, win rate: %.2f%%)",
		req.BotID, bot.TotalTrades, bot.PnLToday, winRate*100)

	return response, nil
}
