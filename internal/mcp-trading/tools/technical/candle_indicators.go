package technical

import (
	"context"
	"fmt"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/tools/common"
)

// candleSource is the only data source of the candle indicators.
// *gctclient.Client implements it; tests inject a fake.
type candleSource interface {
	GetCandles(ctx context.Context, exchange, pair, interval string, limit int) ([]models.Candle, error)
}

type intParam struct {
	Name        string
	Default     int
	Description string
}

// indicatorSpec describes one indicator computed over real exchange candles.
type indicatorSpec struct {
	Name        string
	Description string
	Params      []intParam
	// MinCandles is the number of candles the computation needs.
	MinCandles func(p map[string]int) int
	Compute    func(c []models.Candle, p map[string]int) (map[string]interface{}, error)
}

// candleIndicatorTool is the shared implementation of the candle based indicators.
type candleIndicatorTool struct {
	BaseTool
	spec   indicatorSpec
	source candleSource
}

func newCandleIndicator(spec indicatorSpec, client *gctclient.Client) *candleIndicatorTool {
	return &candleIndicatorTool{BaseTool: BaseTool{client: client}, spec: spec, source: client}
}

func (t *candleIndicatorTool) Name() string        { return t.spec.Name }
func (t *candleIndicatorTool) Description() string { return t.spec.Description }

func (t *candleIndicatorTool) InputSchema() map[string]interface{} {
	props := map[string]interface{}{
		"exchange":    map[string]interface{}{"type": "string", "description": "Exchange name"},
		"pair":        map[string]interface{}{"type": "string", "description": "Trading pair (e.g., 'BTC-USDT' for spot, 'BTCUSDTM' for futures, or 'BTC-USDT' with market_type)"},
		"interval":    map[string]interface{}{"type": "string", "default": "1h", "description": "Candle interval (e.g., '15m', '1h', '1d')"},
		"limit":       map[string]interface{}{"type": "integer", "description": "Candles to fetch (default: enough for the indicator, at least 200)"},
		"market_type": map[string]interface{}{"type": "string", "description": "Market type (optional, enables symbol conversion)", "enum": []string{"spot", "futures", "margin", "options"}},
	}
	for _, p := range t.spec.Params {
		props[p.Name] = map[string]interface{}{"type": "integer", "default": p.Default, "description": p.Description}
	}
	return map[string]interface{}{"type": "object", "properties": props, "required": []string{"exchange", "pair"}}
}

func (t *candleIndicatorTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, pair, err := t.validateExchangeAndPair(params)
	if err != nil {
		return nil, err
	}
	p := map[string]int{}
	for _, sp := range t.spec.Params {
		v := getIntParam(params, sp.Name, sp.Default)
		if v < 2 {
			return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: fmt.Sprintf("%s must be at least 2", sp.Name)}
		}
		p[sp.Name] = v
	}
	if marketType, ok := params["market_type"].(string); ok && marketType != "" {
		if err := t.validateMarketType(marketType); err != nil {
			return nil, err
		}
		if pair, err = t.convertSymbol(pair, marketType, exchange); err != nil {
			return nil, err
		}
	}
	interval := common.GetStringParam(params, "interval", "1h")
	need := t.spec.MinCandles(p)
	limit := getIntParam(params, "limit", 0)
	if limit == 0 {
		limit = need * 2
		if limit < 200 {
			limit = 200
		}
	}
	if limit < need {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: fmt.Sprintf("limit must be at least %d for these parameters", need)}
	}

	raw, err := t.source.GetCandles(ctx, exchange, pair, interval, limit)
	if err != nil {
		return nil, common.DataUnavailable("could not fetch candles from the exchange: "+err.Error(), map[string]interface{}{
			"exchange": exchange, "pair": pair, "interval": interval,
		})
	}
	candles := ascending(raw)
	if len(candles) < need {
		return nil, common.DataUnavailable(fmt.Sprintf("not enough candles for %s: have %d, need %d", t.spec.Name, len(candles), need), map[string]interface{}{
			"candles_available": len(candles), "candles_required": need, "candles_missing": need - len(candles),
			"exchange": exchange, "pair": pair, "interval": interval,
		})
	}
	out, err := t.spec.Compute(candles, p)
	if err != nil {
		return nil, common.DataUnavailable(err.Error(), map[string]interface{}{"exchange": exchange, "pair": pair})
	}
	out["exchange"], out["pair"], out["interval"] = exchange, pair, interval
	out["candles_used"] = len(candles)
	out["as_of"] = candles[len(candles)-1].Timestamp
	for k, v := range p {
		out[k] = v
	}
	return out, nil
}

func fixed(n int) func(map[string]int) int { return func(map[string]int) int { return n } }

func periodPlus(extra int) func(map[string]int) int {
	return func(p map[string]int) int { return p["period"] + extra }
}

// candleIndicatorSpecs are the indicators added to the registry by registerCandleIndicators.
var candleIndicatorSpecs = []indicatorSpec{
	{
		Name: "calculate_atr", Description: "Calculate Average True Range (ATR, Wilder) - volatility indicator",
		Params: []intParam{{"period", 14, "ATR period"}}, MinCandles: periodPlus(1),
		Compute: func(c []models.Candle, p map[string]int) (map[string]interface{}, error) {
			return map[string]interface{}{"atr": atrValue(c, p["period"])}, nil
		},
	},
	{
		Name: "calculate_mfi", Description: "Calculate Money Flow Index (MFI) - volume-weighted RSI",
		Params: []intParam{{"period", 14, "MFI period"}}, MinCandles: periodPlus(1),
		Compute: func(c []models.Candle, p map[string]int) (map[string]interface{}, error) {
			return map[string]interface{}{"mfi": mfiValue(c, p["period"])}, nil
		},
	},
	{
		Name: "calculate_stochastic", Description: "Calculate Stochastic Oscillator (%K and %D) - compares the close with the high-low range",
		Params:     []intParam{{"k_period", 14, "%K lookback"}, {"d_period", 3, "%D smoothing"}},
		MinCandles: func(p map[string]int) int { return p["k_period"] + p["d_period"] - 1 },
		Compute: func(c []models.Candle, p map[string]int) (map[string]interface{}, error) {
			k, d := stochasticValue(c, p["k_period"], p["d_period"])
			return map[string]interface{}{"k": k, "d": d}, nil
		},
	},
	{
		Name: "calculate_adx", Description: "Calculate Average Directional Index (ADX, Wilder) with +DI and -DI - trend strength",
		Params: []intParam{{"period", 14, "ADX period"}}, MinCandles: func(p map[string]int) int { return 2*p["period"] + 1 },
		Compute: func(c []models.Candle, p map[string]int) (map[string]interface{}, error) {
			adx, pdi, mdi := adxValue(c, p["period"])
			return map[string]interface{}{"adx": adx, "plus_di": pdi, "minus_di": mdi}, nil
		},
	},
	{
		Name: "calculate_cci", Description: "Calculate Commodity Channel Index (CCI) - momentum oscillator",
		Params: []intParam{{"period", 20, "CCI period"}}, MinCandles: periodPlus(0),
		Compute: func(c []models.Candle, p map[string]int) (map[string]interface{}, error) {
			return map[string]interface{}{"cci": cciValue(c, p["period"])}, nil
		},
	},
	{
		Name: "calculate_williams_r", Description: "Calculate Williams %R (-100..0) - overbought/oversold oscillator",
		Params: []intParam{{"period", 14, "Lookback"}}, MinCandles: periodPlus(0),
		Compute: func(c []models.Candle, p map[string]int) (map[string]interface{}, error) {
			return map[string]interface{}{"williams_r": williamsRValue(c, p["period"])}, nil
		},
	},
	{
		Name: "calculate_obv", Description: "Calculate On Balance Volume (OBV) over the fetched candle window; its level depends on the window start, read the trend",
		MinCandles: fixed(2),
		Compute: func(c []models.Candle, _ map[string]int) (map[string]interface{}, error) {
			return map[string]interface{}{"obv": obvValue(c)}, nil
		},
	},
	{
		Name: "calculate_vwap", Description: "Calculate Volume Weighted Average Price (VWAP) of the typical price over the fetched candle window",
		MinCandles: fixed(2),
		Compute: func(c []models.Candle, _ map[string]int) (map[string]interface{}, error) {
			v, err := vwapValue(c)
			if err != nil {
				return nil, err
			}
			return map[string]interface{}{"vwap": v}, nil
		},
	},
	{
		Name: "calculate_ichimoku", Description: "Calculate Ichimoku lines (9/26/52): tenkan-sen, kijun-sen and the cloud spans projected 26 periods ahead",
		MinCandles: fixed(52),
		Compute: func(c []models.Candle, _ map[string]int) (map[string]interface{}, error) {
			out := map[string]interface{}{}
			for k, v := range ichimokuValues(c) {
				out[k] = v
			}
			return out, nil
		},
	},
	{
		Name: "calculate_volatility", Description: "Calculate price volatility: sample standard deviation of the simple returns of the last `period` candles (per candle, not annualized)",
		Params: []intParam{{"period", 20, "Number of returns"}}, MinCandles: periodPlus(1),
		Compute: func(c []models.Candle, p map[string]int) (map[string]interface{}, error) {
			v := volatilityValue(c, p["period"])
			return map[string]interface{}{"volatility": v, "volatility_pct": v * 100}, nil
		},
	},
}

// newCandleIndicatorTools builds the candle indicator tools.
func newCandleIndicatorTools(client *gctclient.Client) []*candleIndicatorTool {
	out := make([]*candleIndicatorTool, 0, len(candleIndicatorSpecs))
	for _, s := range candleIndicatorSpecs {
		out = append(out, newCandleIndicator(s, client))
	}
	return out
}
