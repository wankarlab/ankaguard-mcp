package technical

import (
	"context"
	"fmt"
	"strings"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
)

// CalculateRSIFuturesTool calcula el RSI específicamente para Futures
// Usa directamente el símbolo de Futures sin conversión
type CalculateRSIFuturesTool struct {
	BaseTool
}

func NewCalculateRSIFuturesTool(client *gctclient.Client) *CalculateRSIFuturesTool {
	return &CalculateRSIFuturesTool{BaseTool: BaseTool{client: client}}
}

func (t *CalculateRSIFuturesTool) Name() string {
	return "calculate_rsi_futures"
}

func (t *CalculateRSIFuturesTool) Description() string {
	return "Calculate RSI for Futures contracts - optimized for futures trading with direct symbol usage (e.g., 'DOGEUSDTM', 'BTCUSDTM')"
}

func (t *CalculateRSIFuturesTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name (e.g., 'kucoin')",
			},
			"symbol": map[string]interface{}{
				"type":        "string",
				"description": "Futures contract symbol (e.g., 'DOGEUSDTM', 'BTCUSDTM')",
			},
			"period": map[string]interface{}{
				"type":        "integer",
				"description": "RSI period (default: 14)",
				"default":     14,
			},
		},
		"required": []string{"exchange", "symbol"},
	}
}

func (t *CalculateRSIFuturesTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, ok := params["exchange"].(string)
	if !ok || exchange == "" {
		return nil, fmt.Errorf("exchange is required")
	}

	symbol, ok := params["symbol"].(string)
	if !ok || symbol == "" {
		return nil, fmt.Errorf("symbol is required")
	}

	period := getIntParam(params, "period", 14)

	// Validate that this is a futures symbol
	if !isFuturesSymbol(symbol) {
		return nil, fmt.Errorf("symbol %s does not appear to be a futures contract (should end with 'M' or similar)", symbol)
	}

	return executeWithContext(ctx, func(ctx context.Context) (interface{}, error) {
		// Check if client is available
		if t.client == nil {
			return nil, fmt.Errorf("GCT client not available")
		}

		// Try to calculate RSI for futures symbol
		rsi, err := t.client.CalculateRSI(ctx, exchange, symbol, period)
		if err != nil {
			// If direct calculation fails, try with converted symbol
			convertedSymbol := convertToFuturesSymbol(symbol)
			if convertedSymbol != symbol {
				rsi, err = t.client.CalculateRSI(ctx, exchange, convertedSymbol, period)
				if err != nil {
					return nil, fmt.Errorf("failed to calculate RSI for futures symbol %s (also tried %s): %w", symbol, convertedSymbol, err)
				}
			} else {
				return nil, fmt.Errorf("failed to calculate RSI for futures symbol %s: %w", symbol, err)
			}
		}

		return map[string]interface{}{
			"rsi":      rsi,
			"period":   period,
			"exchange": exchange,
			"symbol":   symbol,
			"note":     "RSI calculated for futures contract",
		}, nil
	})
}

// isFuturesSymbol checks if a symbol appears to be a futures contract
func isFuturesSymbol(symbol string) bool {
	// Common futures symbol patterns
	return len(symbol) > 6 && (symbol[len(symbol)-1:] == "M" ||
		symbol[len(symbol)-2:] == "TM" ||
		symbol[len(symbol)-4:] == "USDT" && len(symbol) > 8)
}

// convertToFuturesSymbol attempts to convert a symbol to futures format
func convertToFuturesSymbol(symbol string) string {
	// If it already looks like futures, return as-is
	if isFuturesSymbol(symbol) {
		return symbol
	}

	// Try common conversions
	if symbol == "BTC-USDT" {
		return "BTCUSDTM"
	}
	if symbol == "DOGE-USDT" {
		return "DOGEUSDTM"
	}
	if symbol == "ETH-USDT" {
		return "ETHUSDTM"
	}

	// Generic conversion: remove dashes and add M
	converted := strings.ReplaceAll(symbol, "-", "") + "M"
	return converted
}

// CalculateMACDFuturesTool calcula el MACD específicamente para Futures
type CalculateMACDFuturesTool struct {
	BaseTool
}

func NewCalculateMACDFuturesTool(client *gctclient.Client) *CalculateMACDFuturesTool {
	return &CalculateMACDFuturesTool{BaseTool: BaseTool{client: client}}
}

func (t *CalculateMACDFuturesTool) Name() string {
	return "calculate_macd_futures"
}

func (t *CalculateMACDFuturesTool) Description() string {
	return "Calculate MACD for Futures contracts - optimized for futures trading with direct symbol usage"
}

func (t *CalculateMACDFuturesTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name (e.g., 'kucoin')",
			},
			"symbol": map[string]interface{}{
				"type":        "string",
				"description": "Futures contract symbol (e.g., 'DOGEUSDTM', 'BTCUSDTM')",
			},
			"fast_period": map[string]interface{}{
				"type":        "integer",
				"description": "Fast EMA period (default: 12)",
				"default":     12,
			},
			"slow_period": map[string]interface{}{
				"type":        "integer",
				"description": "Slow EMA period (default: 26)",
				"default":     26,
			},
			"signal_period": map[string]interface{}{
				"type":        "integer",
				"description": "Signal line period (default: 9)",
				"default":     9,
			},
		},
		"required": []string{"exchange", "symbol"},
	}
}

func (t *CalculateMACDFuturesTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, ok := params["exchange"].(string)
	if !ok || exchange == "" {
		return nil, fmt.Errorf("exchange is required")
	}

	symbol, ok := params["symbol"].(string)
	if !ok || symbol == "" {
		return nil, fmt.Errorf("symbol is required")
	}

	fastPeriod := getIntParam(params, "fast_period", 12)
	slowPeriod := getIntParam(params, "slow_period", 26)
	signalPeriod := getIntParam(params, "signal_period", 9)

	// Validate that this is a futures symbol
	if !isFuturesSymbol(symbol) {
		return nil, fmt.Errorf("symbol %s does not appear to be a futures contract (should end with 'M' or similar)", symbol)
	}

	return executeWithContext(ctx, func(ctx context.Context) (interface{}, error) {
		// Check if client is available
		if t.client == nil {
			return nil, fmt.Errorf("GCT client not available")
		}

		// Try to calculate MACD for futures symbol
		macdResult, err := t.client.CalculateMACD(ctx, exchange, symbol, fastPeriod, slowPeriod, signalPeriod)
		if err != nil {
			// If direct calculation fails, try with converted symbol
			convertedSymbol := convertToFuturesSymbol(symbol)
			if convertedSymbol != symbol {
				macdResult, err = t.client.CalculateMACD(ctx, exchange, convertedSymbol, fastPeriod, slowPeriod, signalPeriod)
				if err != nil {
					return nil, fmt.Errorf("failed to calculate MACD for futures symbol %s (also tried %s): %w", symbol, convertedSymbol, err)
				}
			} else {
				return nil, fmt.Errorf("failed to calculate MACD for futures symbol %s: %w", symbol, err)
			}
		}

		return map[string]interface{}{
			"macd":          macdResult.MACD,
			"signal":        macdResult.Signal,
			"histogram":     macdResult.Histogram,
			"fast_period":   fastPeriod,
			"slow_period":   slowPeriod,
			"signal_period": signalPeriod,
			"exchange":      exchange,
			"symbol":        symbol,
			"note":          "MACD calculated for futures contract",
		}, nil
	})
}
