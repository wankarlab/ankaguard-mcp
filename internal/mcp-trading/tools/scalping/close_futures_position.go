package scalping

import (
	"context"
	"fmt"
	"strings"
)

// CloseFuturesPositionTool implementa la tool para cerrar posiciones de futuros
type CloseFuturesPositionTool struct {
	adapter *Adapter
}

// NewCloseFuturesPositionTool crea una nueva instancia de la tool
func NewCloseFuturesPositionTool(adapter *Adapter) *CloseFuturesPositionTool {
	return &CloseFuturesPositionTool{
		adapter: adapter,
	}
}

// Name retorna el nombre de la tool
func (t *CloseFuturesPositionTool) Name() string {
	return "close_futures_position"
}

// Description retorna la descripción de la tool
func (t *CloseFuturesPositionTool) Description() string {
	return "Closes an open futures position by placing an opposite market order with reduce-only flag. " +
		"Automatically determines the correct side and size to fully close the position. " +
		"Returns confirmation with final PnL and execution details."
}

// InputSchema retorna el schema JSON de los parámetros de entrada
func (t *CloseFuturesPositionTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name (e.g., 'kucoin')",
			},
			"symbol": map[string]interface{}{
				"type":        "string",
				"description": "Futures symbol to close (e.g., 'DOGEUSDTM', 'BTCUSDTM')",
			},
		},
		"required": []string{"exchange", "symbol"},
	}
}

// Execute ejecuta la tool
func (t *CloseFuturesPositionTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	if t == nil || t.adapter == nil {
		return nil, fmt.Errorf("tool or adapter is nil")
	}

	// Parsear parámetros
	exchange := getStringParam(params, "exchange")
	symbol := getStringParam(params, "symbol")

	if exchange == "" {
		return nil, fmt.Errorf("exchange is required")
	}
	if symbol == "" {
		return nil, fmt.Errorf("symbol is required")
	}

	t.adapter.GetLogger().Infof("Closing futures position for %s on %s", symbol, exchange)

	// Normalizar símbolo para el formato correcto de futuros
	normalizedSymbol := normalizeSymbolForFutures(symbol)
	if normalizedSymbol != symbol {
		t.adapter.GetLogger().Infof("Symbol normalized: %s -> %s", symbol, normalizedSymbol)
	}

	// Obtener posición actual para verificar que existe
	cl, err := t.adapter.ClientFor(ctx)
	if err != nil {
		return nil, err
	}
	position, err := cl.GetFuturesPosition(ctx, exchange, normalizedSymbol)
	if err != nil {
		return nil, fmt.Errorf("failed to get position: %w", err)
	}

	if position == nil {
		return nil, fmt.Errorf("no open position found for %s", symbol)
	}

	// Guardar PnL antes de cerrar
	unrealizedPnL := position.UnrealizedPnL
	realizedPnL := position.RealizedPnL
	pnlPercent := position.CalculatePnLPercent()

	t.adapter.GetLogger().Infof("Closing %s position: size=%.8f, side=%s, unrealized_pnl=%.2f USDT (%.2f%%)",
		symbol, position.Size, position.Side, unrealizedPnL, pnlPercent)

	// Cerrar posición usando el símbolo normalizado
	err = cl.CloseFuturesPosition(ctx, exchange, normalizedSymbol)
	if err != nil {
		return nil, fmt.Errorf("failed to close position: %w", err)
	}

	t.adapter.GetLogger().Infof("✓ Position closed successfully for %s: PnL=%.2f USDT (%.2f%%)",
		symbol, unrealizedPnL, pnlPercent)

	return map[string]interface{}{
		"exchange":       exchange,
		"symbol":         symbol,
		"status":         "closed",
		"side":           position.Side,
		"size":           position.Size,
		"entry_price":    position.EntryPrice,
		"exit_price":     position.CurrentPrice,
		"unrealized_pnl": unrealizedPnL,
		"realized_pnl":   realizedPnL,
		"total_pnl":      unrealizedPnL + realizedPnL,
		"pnl_percent":    pnlPercent,
		"leverage":       position.Leverage,
		"margin_mode":    position.MarginMode,
	}, nil
}

// normalizeSymbolForFutures convierte símbolos al formato correcto para futuros de KuCoin
// DOGE-USDT -> DOGEUSDTM
// BTC-USDT -> BTCUSDTM
// DOGEUSDTM -> DOGEUSDTM (sin cambios si ya está correcto)
func normalizeSymbolForFutures(symbol string) string {
	if symbol == "" {
		return ""
	}

	// Si ya termina en M, probablemente ya está en formato correcto
	if strings.HasSuffix(symbol, "M") {
		return symbol
	}

	// Si contiene guión, convertir de formato spot a futures
	if strings.Contains(symbol, "-") {
		// DOGE-USDT -> DOGEUSDTM
		normalized := strings.ReplaceAll(symbol, "-", "") + "M"
		return normalized
	}

	// Si no tiene guión ni M, agregar M (asumir que es formato sin guión)
	// DOGEUSDT -> DOGEUSDTM
	return symbol + "M"
}
