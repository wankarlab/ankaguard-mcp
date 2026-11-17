package orders

import (
	"context"
	"time"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/sirupsen/logrus"
)

// SubmitOrderTool envía una orden genérica
type SubmitOrderTool struct {
	BaseTool
}

func NewSubmitOrderTool(client *gctclient.Client) *SubmitOrderTool {
	return &SubmitOrderTool{BaseTool: BaseTool{client: client}}
}

func (t *SubmitOrderTool) Name() string {
	return "submit_order"
}

func (t *SubmitOrderTool) Description() string {
	return "Submit a trading order (market, limit, or stop) to an exchange"
}

func (t *SubmitOrderTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name",
			},
			"pair": map[string]interface{}{
				"type":        "string",
				"description": "Trading pair (e.g., 'BTC-USDT' for spot, 'BTCUSDTM' for futures, or 'BTC-USDT' with market_type)",
			},
			"side": map[string]interface{}{
				"type":        "string",
				"description": "Order side: 'buy' or 'sell'",
				"enum":        []string{"buy", "sell"},
			},
			"type": map[string]interface{}{
				"type":        "string",
				"description": "Order type: 'market', 'limit', or 'stop'",
				"enum":        []string{"market", "limit", "stop"},
			},
			"amount": map[string]interface{}{
				"type":        "number",
				"description": "Order amount/quantity",
			},
			"price": map[string]interface{}{
				"type":        "number",
				"description": "Limit price (required for limit and stop orders)",
			},
			"stop_price": map[string]interface{}{
				"type":        "number",
				"description": "Stop price (required for stop orders)",
			},
			"market_type": map[string]interface{}{
				"type":        "string",
				"description": "Market type: 'spot', 'futures', 'margin', or 'options' (optional, enables symbol conversion and validation)",
				"enum":        []string{"spot", "futures", "margin", "options"},
			},
			"leverage": map[string]interface{}{
				"type":        "number",
				"description": "Leverage for futures/margin trading (1-100, not allowed for spot)",
			},
			"reduce_only": map[string]interface{}{
				"type":        "boolean",
				"description": "Only reduce existing position, don't open new one (futures only)",
			},
			"close_order": map[string]interface{}{
				"type":        "boolean",
				"description": "Close position in Hedge Mode (KuCoin futures only, required for Hedge Mode)",
			},
			"client_id": map[string]interface{}{
				"type":        "string",
				"description": "Optional client order ID for idempotency. If omitted, a deterministic ID is derived from the order content so a retried duplicate is deduplicated by the exchange.",
			},
		},
		"required": []string{"exchange", "pair", "side", "type", "amount"},
	}
}

func (t *SubmitOrderTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	startTime := time.Now()
	logger := getLogger()

	exchange, pair, side, err := t.validateOrderParams(params)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"tool":  t.Name(),
			"error": err.Error(),
		}).Warn("Order validation failed")
		return nil, err
	}

	orderType := getStringParam(params, "type", "market")
	amount, err := getFloatParam(params, "amount", true)
	if err != nil {
		return nil, err
	}

	price, _ := getFloatParam(params, "price", false)
	stopPrice, _ := getFloatParam(params, "stop_price", false)
	leverage, _ := getFloatParam(params, "leverage", false)
	reduceOnly := getBoolParam(params, "reduce_only", false)
	closeOrder := getBoolParam(params, "close_order", false)

	// Log tool execution start
	logger.WithFields(logrus.Fields{
		"tool":       t.Name(),
		"exchange":   exchange,
		"pair":       pair,
		"side":       side,
		"order_type": orderType,
		"amount":     amount,
		"price":      price,
		"leverage":   leverage,
	}).Info("Executing tool")

	// Handle optional market_type parameter
	marketType := getStringParam(params, "market_type", "")
	if marketType != "" {
		// Validate market_type
		if err := t.validateMarketType(marketType); err != nil {
			return nil, err
		}

		// Validate leverage based on market_type
		if err := t.validateLeverageForMarketType(leverage, marketType); err != nil {
			return nil, err
		}

		// Convert symbol using market_type
		convertedPair, err := t.convertSymbol(pair, marketType, exchange)
		if err != nil {
			return nil, err
		}
		pair = convertedPair
	}

	// Respect a caller-supplied client_id; otherwise derive a deterministic one
	// so an identical retry within the dedup window is idempotent (audit P0a).
	suppliedClientID := getStringParam(params, "client_id", "")

	req := &models.OrderRequest{
		Exchange:   exchange,
		Pair:       pair,
		Side:       side,
		Type:       orderType,
		Amount:     amount,
		Price:      price,
		StopPrice:  stopPrice,
		Leverage:   leverage,
		ClientID:   resolveClientOrderID(suppliedClientID, exchange, pair, side, orderType, amount, price),
		ReduceOnly: reduceOnly,
		CloseOrder: closeOrder,
	}

	result, err := executeWithContext(ctx, func(ctx context.Context) (interface{}, error) {
		return t.client.SubmitOrder(ctx, req)
	})

	duration := time.Since(startTime)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"tool":        t.Name(),
			"exchange":    exchange,
			"pair":        pair,
			"duration_ms": duration.Milliseconds(),
			"success":     false,
			"error":       err.Error(),
		}).Error("Tool execution failed")
		return nil, err
	}

	logger.WithFields(logrus.Fields{
		"tool":        t.Name(),
		"exchange":    exchange,
		"pair":        pair,
		"duration_ms": duration.Milliseconds(),
		"success":     true,
		"client_id":   req.ClientID,
	}).Info("Tool completed")

	return result, nil
}

// SubmitMarketOrderTool envía una orden market
type SubmitMarketOrderTool struct {
	BaseTool
}

func NewSubmitMarketOrderTool(client *gctclient.Client) *SubmitMarketOrderTool {
	return &SubmitMarketOrderTool{BaseTool: BaseTool{client: client}}
}

func (t *SubmitMarketOrderTool) Name() string {
	return "submit_market_order"
}

func (t *SubmitMarketOrderTool) Description() string {
	return "Submit a market order that executes immediately at current market price"
}

func (t *SubmitMarketOrderTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{"type": "string", "description": "Exchange name"},
			"pair":     map[string]interface{}{"type": "string", "description": "Trading pair"},
			"side":     map[string]interface{}{"type": "string", "description": "'buy' or 'sell'", "enum": []string{"buy", "sell"}},
			"amount":   map[string]interface{}{"type": "number", "description": "Order amount"},
		},
		"required": []string{"exchange", "pair", "side", "amount"},
	}
}

func (t *SubmitMarketOrderTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, pair, side, err := t.validateOrderParams(params)
	if err != nil {
		return nil, err
	}

	amount, err := getFloatParam(params, "amount", true)
	if err != nil {
		return nil, err
	}

	req := &models.OrderRequest{
		Exchange: exchange,
		Pair:     pair,
		Side:     side,
		Type:     "market",
		Amount:   amount,
		ClientID: deterministicClientOrderID(exchange, pair, side, "market", amount, 0),
	}

	return executeWithContext(ctx, func(ctx context.Context) (interface{}, error) {
		return t.client.SubmitOrder(ctx, req)
	})
}

// SubmitLimitOrderTool envía una orden limit
type SubmitLimitOrderTool struct {
	BaseTool
}

func NewSubmitLimitOrderTool(client *gctclient.Client) *SubmitLimitOrderTool {
	return &SubmitLimitOrderTool{BaseTool: BaseTool{client: client}}
}

func (t *SubmitLimitOrderTool) Name() string {
	return "submit_limit_order"
}

func (t *SubmitLimitOrderTool) Description() string {
	return "Submit a limit order that executes only at specified price or better"
}

func (t *SubmitLimitOrderTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{"type": "string", "description": "Exchange name"},
			"pair":     map[string]interface{}{"type": "string", "description": "Trading pair"},
			"side":     map[string]interface{}{"type": "string", "description": "'buy' or 'sell'", "enum": []string{"buy", "sell"}},
			"amount":   map[string]interface{}{"type": "number", "description": "Order amount"},
			"price":    map[string]interface{}{"type": "number", "description": "Limit price"},
		},
		"required": []string{"exchange", "pair", "side", "amount", "price"},
	}
}

func (t *SubmitLimitOrderTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	startTime := time.Now()
	logger := getLogger()

	exchange, pair, side, err := t.validateOrderParams(params)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"tool":  t.Name(),
			"error": err.Error(),
		}).Warn("Order validation failed")
		return nil, err
	}

	amount, err := getFloatParam(params, "amount", true)
	if err != nil {
		return nil, err
	}

	price, err := getFloatParam(params, "price", true)
	if err != nil {
		return nil, err
	}

	// Derive a deterministic client order ID for idempotent retries (audit P0a).
	clientID := deterministicClientOrderID(exchange, pair, side, "limit", amount, price)

	// Log tool execution start
	logger.WithFields(logrus.Fields{
		"tool":      t.Name(),
		"exchange":  exchange,
		"pair":      pair,
		"side":      side,
		"amount":    amount,
		"price":     price,
		"client_id": clientID,
	}).Info("Executing tool")

	req := &models.OrderRequest{
		Exchange: exchange,
		Pair:     pair,
		Side:     side,
		Type:     "limit",
		Amount:   amount,
		Price:    price,
		ClientID: clientID,
	}

	result, err := executeWithContext(ctx, func(ctx context.Context) (interface{}, error) {
		return t.client.SubmitOrder(ctx, req)
	})

	duration := time.Since(startTime)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"tool":        t.Name(),
			"exchange":    exchange,
			"pair":        pair,
			"duration_ms": duration.Milliseconds(),
			"success":     false,
			"error":       err.Error(),
		}).Error("Tool execution failed")
		return nil, err
	}

	logger.WithFields(logrus.Fields{
		"tool":        t.Name(),
		"exchange":    exchange,
		"pair":        pair,
		"duration_ms": duration.Milliseconds(),
		"success":     true,
		"client_id":   clientID,
	}).Info("Tool completed")

	return result, nil
}

// SubmitStopOrderTool envía una orden stop
type SubmitStopOrderTool struct {
	BaseTool
}

func NewSubmitStopOrderTool(client *gctclient.Client) *SubmitStopOrderTool {
	return &SubmitStopOrderTool{BaseTool: BaseTool{client: client}}
}

func (t *SubmitStopOrderTool) Name() string {
	return "submit_stop_order"
}

func (t *SubmitStopOrderTool) Description() string {
	return "Submit a stop order that triggers when price reaches stop price"
}

func (t *SubmitStopOrderTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange":   map[string]interface{}{"type": "string", "description": "Exchange name"},
			"pair":       map[string]interface{}{"type": "string", "description": "Trading pair"},
			"side":       map[string]interface{}{"type": "string", "description": "'buy' or 'sell'", "enum": []string{"buy", "sell"}},
			"amount":     map[string]interface{}{"type": "number", "description": "Order amount"},
			"stop_price": map[string]interface{}{"type": "number", "description": "Stop trigger price"},
			"price":      map[string]interface{}{"type": "number", "description": "Limit price (optional)"},
		},
		"required": []string{"exchange", "pair", "side", "amount", "stop_price"},
	}
}

func (t *SubmitStopOrderTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, pair, side, err := t.validateOrderParams(params)
	if err != nil {
		return nil, err
	}

	amount, err := getFloatParam(params, "amount", true)
	if err != nil {
		return nil, err
	}

	stopPrice, err := getFloatParam(params, "stop_price", true)
	if err != nil {
		return nil, err
	}

	price, _ := getFloatParam(params, "price", false)

	req := &models.OrderRequest{
		Exchange:  exchange,
		Pair:      pair,
		Side:      side,
		Type:      "stop",
		Amount:    amount,
		Price:     price,
		StopPrice: stopPrice,
		ClientID:  deterministicClientOrderID(exchange, pair, side, "stop", amount, price),
	}

	return executeWithContext(ctx, func(ctx context.Context) (interface{}, error) {
		return t.client.SubmitOrder(ctx, req)
	})
}
