package scalping

import (
	"context"
	"fmt"
	"time"
)

// StartBotTool implementa la tool para iniciar un bot de scalping
type StartBotTool struct {
	adapter *Adapter
}

// NewStartBotTool crea una nueva instancia de la tool
func NewStartBotTool(adapter *Adapter) *StartBotTool {
	return &StartBotTool{
		adapter: adapter,
	}
}

// Name retorna el nombre de la tool
func (t *StartBotTool) Name() string {
	return "start_scalping_bot"
}

// Description retorna la descripción de la tool
func (t *StartBotTool) Description() string {
	return "Starts an autonomous scalping bot that monitors specified pairs and executes trades automatically. " +
		"The bot uses configured strategies and respects risk limits. " +
		"Returns a bot_id that can be used to monitor or stop the bot."
}

// InputSchema retorna el schema JSON de los parámetros de entrada
func (t *StartBotTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name (e.g., 'kucoin', 'binance')",
			},
			"pairs": map[string]interface{}{
				"type":        "array",
				"description": "List of trading pairs to monitor (max 10)",
				"items": map[string]interface{}{
					"type": "string",
				},
				"minItems": 1,
				"maxItems": 10,
			},
			"strategies": map[string]interface{}{
				"type":        "array",
				"description": "Strategies to use. Options: 'rsi', 'macd', 'bollinger', 'volume'",
				"items": map[string]interface{}{
					"type": "string",
					"enum": []string{"rsi", "macd", "bollinger", "volume"},
				},
				"minItems": 1,
			},
			"max_position_size_usd": map[string]interface{}{
				"type":        "number",
				"description": "Maximum position size in USD per trade",
				"minimum":     10.0,
				"maximum":     1000.0,
			},
			"max_daily_trades": map[string]interface{}{
				"type":        "number",
				"description": "Maximum number of trades per day",
				"minimum":     1,
				"maximum":     100,
			},
			"stop_loss_percent": map[string]interface{}{
				"type":        "number",
				"description": "Stop loss percentage",
				"minimum":     0.1,
				"maximum":     5.0,
			},
			"take_profit_percent": map[string]interface{}{
				"type":        "number",
				"description": "Take profit percentage",
				"minimum":     0.1,
				"maximum":     10.0,
			},
		},
		"required": []string{
			"exchange",
			"pairs",
			"strategies",
			"max_position_size_usd",
			"max_daily_trades",
			"stop_loss_percent",
			"take_profit_percent",
		},
	}
}

// Execute ejecuta la tool
func (t *StartBotTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	// Parsear parámetros
	req := StartBotRequest{
		Exchange:        getStringParam(params, "exchange"),
		Pairs:           getStringSliceParam(params, "pairs"),
		Strategies:      getStringSliceParam(params, "strategies"),
		MaxPositionSize: getFloat64Param(params, "max_position_size_usd"),
		MaxDailyTrades:  getIntParam(params, "max_daily_trades"),
		StopLoss:        getFloat64Param(params, "stop_loss_percent"),
		TakeProfit:      getFloat64Param(params, "take_profit_percent"),
	}

	t.adapter.GetLogger().Infof("Starting bot for %s with %d pairs and %d strategies",
		req.Exchange, len(req.Pairs), len(req.Strategies))

	// Crear configuración del bot
	config := BotConfig{
		Exchange:        req.Exchange,
		Pairs:           req.Pairs,
		Strategies:      req.Strategies,
		MaxPositionSize: req.MaxPositionSize,
		MaxDailyTrades:  req.MaxDailyTrades,
		StopLoss:        req.StopLoss,
		TakeProfit:      req.TakeProfit,
	}

	// Iniciar bot con contexto de fondo (no se cancela con la request)
	botCtx := context.Background()
	botID, err := t.adapter.GetBotManager().StartBot(botCtx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to start bot: %w", err)
	}

	// Crear respuesta
	response := &StartBotResponse{
		BotID:     botID,
		Status:    "running",
		Config:    config,
		StartedAt: time.Now().Format(time.RFC3339),
	}

	t.adapter.GetLogger().Infof("Bot started successfully: %s", botID)

	return response, nil
}
