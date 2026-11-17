package technical

import (
	"context"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
)

// CalculateRSITool calcula el RSI
type CalculateRSITool struct {
	BaseTool
}

func NewCalculateRSITool(client *gctclient.Client) *CalculateRSITool {
	return &CalculateRSITool{BaseTool: BaseTool{client: client}}
}

func (t *CalculateRSITool) Name() string {
	return "calculate_rsi"
}

func (t *CalculateRSITool) Description() string {
	return "Calculate Relative Strength Index (RSI) - momentum oscillator measuring speed and magnitude of price changes"
}

func (t *CalculateRSITool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange":    map[string]interface{}{"type": "string", "description": "Exchange name"},
			"pair":        map[string]interface{}{"type": "string", "description": "Trading pair (e.g., 'BTC-USDT' for spot, 'BTCUSDTM' for futures, or 'BTC-USDT' with market_type)"},
			"period":      map[string]interface{}{"type": "integer", "description": "RSI period (default: 14)", "default": 14},
			"market_type": map[string]interface{}{"type": "string", "description": "Market type: 'spot', 'futures', 'margin', or 'options' (optional, enables symbol conversion)", "enum": []string{"spot", "futures", "margin", "options"}},
		},
		"required": []string{"exchange", "pair"},
	}
}

func (t *CalculateRSITool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, pair, err := t.validateExchangeAndPair(params)
	if err != nil {
		return nil, err
	}
	period := getIntParam(params, "period", 14)

	// Handle optional market_type parameter for symbol conversion
	if marketType, ok := params["market_type"].(string); ok && marketType != "" {
		// Validate market_type
		if err := t.validateMarketType(marketType); err != nil {
			return nil, err
		}

		// Convert symbol using market_type
		convertedPair, err := t.convertSymbol(pair, marketType, exchange)
		if err != nil {
			return nil, err
		}
		pair = convertedPair
	}

	return executeWithContext(ctx, func(ctx context.Context) (interface{}, error) {
		rsi, err := t.client.CalculateRSI(ctx, exchange, pair, period)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"rsi":      rsi,
			"period":   period,
			"exchange": exchange,
			"pair":     pair,
		}, nil
	})
}

// CalculateMACDTool calcula el MACD
type CalculateMACDTool struct {
	BaseTool
}

func NewCalculateMACDTool(client *gctclient.Client) *CalculateMACDTool {
	return &CalculateMACDTool{BaseTool: BaseTool{client: client}}
}

func (t *CalculateMACDTool) Name() string {
	return "calculate_macd"
}

func (t *CalculateMACDTool) Description() string {
	return "Calculate MACD (Moving Average Convergence Divergence) - trend-following momentum indicator"
}

func (t *CalculateMACDTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange":      map[string]interface{}{"type": "string", "description": "Exchange name"},
			"pair":          map[string]interface{}{"type": "string", "description": "Trading pair (e.g., 'BTC-USDT' for spot, 'BTCUSDTM' for futures, or 'BTC-USDT' with market_type)"},
			"fast_period":   map[string]interface{}{"type": "integer", "description": "Fast EMA period (default: 12)", "default": 12},
			"slow_period":   map[string]interface{}{"type": "integer", "description": "Slow EMA period (default: 26)", "default": 26},
			"signal_period": map[string]interface{}{"type": "integer", "description": "Signal line period (default: 9)", "default": 9},
			"market_type":   map[string]interface{}{"type": "string", "description": "Market type: 'spot', 'futures', 'margin', or 'options' (optional, enables symbol conversion)", "enum": []string{"spot", "futures", "margin", "options"}},
		},
		"required": []string{"exchange", "pair"},
	}
}

func (t *CalculateMACDTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, pair, err := t.validateExchangeAndPair(params)
	if err != nil {
		return nil, err
	}
	fastPeriod := getIntParam(params, "fast_period", 12)
	slowPeriod := getIntParam(params, "slow_period", 26)
	signalPeriod := getIntParam(params, "signal_period", 9)

	// Handle optional market_type parameter for symbol conversion
	if marketType, ok := params["market_type"].(string); ok && marketType != "" {
		// Validate market_type
		if err := t.validateMarketType(marketType); err != nil {
			return nil, err
		}

		// Convert symbol using market_type
		convertedPair, err := t.convertSymbol(pair, marketType, exchange)
		if err != nil {
			return nil, err
		}
		pair = convertedPair
	}

	return executeWithContext(ctx, func(ctx context.Context) (interface{}, error) {
		return t.client.CalculateMACD(ctx, exchange, pair, fastPeriod, slowPeriod, signalPeriod)
	})
}

// CalculateBollingerTool calcula Bollinger Bands
type CalculateBollingerTool struct {
	BaseTool
}

func NewCalculateBollingerTool(client *gctclient.Client) *CalculateBollingerTool {
	return &CalculateBollingerTool{BaseTool: BaseTool{client: client}}
}

func (t *CalculateBollingerTool) Name() string {
	return "calculate_bollinger"
}

func (t *CalculateBollingerTool) Description() string {
	return "Calculate Bollinger Bands - volatility indicator with upper, middle, and lower bands"
}

func (t *CalculateBollingerTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{"type": "string", "description": "Exchange name"},
			"pair":     map[string]interface{}{"type": "string", "description": "Trading pair"},
			"period":   map[string]interface{}{"type": "integer", "description": "Period (default: 20)", "default": 20},
			"std_dev":  map[string]interface{}{"type": "number", "description": "Standard deviations (default: 2)", "default": 2.0},
		},
		"required": []string{"exchange", "pair"},
	}
}

func (t *CalculateBollingerTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, pair, err := t.validateExchangeAndPair(params)
	if err != nil {
		return nil, err
	}
	period := getIntParam(params, "period", 20)
	stdDev := getFloatParam(params, "std_dev", 2.0)

	return executeWithContext(ctx, func(ctx context.Context) (interface{}, error) {
		return t.client.CalculateBollingerBands(ctx, exchange, pair, period, stdDev)
	})
}

// CalculateEMATool calcula EMA
type CalculateEMATool struct {
	BaseTool
}

func NewCalculateEMATool(client *gctclient.Client) *CalculateEMATool {
	return &CalculateEMATool{BaseTool: BaseTool{client: client}}
}

func (t *CalculateEMATool) Name() string {
	return "calculate_ema"
}

func (t *CalculateEMATool) Description() string {
	return "Calculate Exponential Moving Average (EMA) - weighted moving average giving more weight to recent prices"
}

func (t *CalculateEMATool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange":    map[string]interface{}{"type": "string", "description": "Exchange name"},
			"pair":        map[string]interface{}{"type": "string", "description": "Trading pair (e.g., 'BTC-USDT' for spot, 'BTCUSDTM' for futures, or 'BTC-USDT' with market_type)"},
			"period":      map[string]interface{}{"type": "integer", "description": "EMA period (default: 20)", "default": 20},
			"market_type": map[string]interface{}{"type": "string", "description": "Market type: 'spot', 'futures', 'margin', or 'options' (optional, enables symbol conversion)", "enum": []string{"spot", "futures", "margin", "options"}},
		},
		"required": []string{"exchange", "pair"},
	}
}

func (t *CalculateEMATool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, pair, err := t.validateExchangeAndPair(params)
	if err != nil {
		return nil, err
	}
	period := getIntParam(params, "period", 20)

	// Handle optional market_type parameter for symbol conversion
	if marketType, ok := params["market_type"].(string); ok && marketType != "" {
		// Validate market_type
		if err := t.validateMarketType(marketType); err != nil {
			return nil, err
		}

		// Convert symbol using market_type
		convertedPair, err := t.convertSymbol(pair, marketType, exchange)
		if err != nil {
			return nil, err
		}
		pair = convertedPair
	}

	return map[string]interface{}{
		"exchange": exchange,
		"pair":     pair,
		"period":   period,
		"ema":      0.0,
		"note":     "EMA calculation not yet fully implemented",
	}, nil
}

// CalculateSMATool calcula SMA
type CalculateSMATool struct {
	BaseTool
}

func NewCalculateSMATool(client *gctclient.Client) *CalculateSMATool {
	return &CalculateSMATool{BaseTool: BaseTool{client: client}}
}

func (t *CalculateSMATool) Name() string {
	return "calculate_sma"
}

func (t *CalculateSMATool) Description() string {
	return "Calculate Simple Moving Average (SMA) - arithmetic mean of prices over a period"
}

func (t *CalculateSMATool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{"type": "string", "description": "Exchange name"},
			"pair":     map[string]interface{}{"type": "string", "description": "Trading pair"},
			"period":   map[string]interface{}{"type": "integer", "description": "SMA period (default: 20)", "default": 20},
		},
		"required": []string{"exchange", "pair"},
	}
}

func (t *CalculateSMATool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, pair, err := t.validateExchangeAndPair(params)
	if err != nil {
		return nil, err
	}
	period := getIntParam(params, "period", 20)

	return map[string]interface{}{
		"exchange": exchange,
		"pair":     pair,
		"period":   period,
		"sma":      0.0,
		"note":     "SMA calculation not yet fully implemented",
	}, nil
}
