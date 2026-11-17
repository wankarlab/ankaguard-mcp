package orders

import (
	"context"
	"fmt"
	"strings"

	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
)

// The tools in this file talk to KuCoin through the DIRECT REST client
// (internal/gct → KuCoinRESTClient) instead of the GoCryptoTrader gRPC path
// used by the rest of the order tools. GCT only services the small set of
// futures contracts enabled in its config, so any contract outside that list
// (or a position opened manually / outside the bot) is unreachable via gRPC and
// fails with "currency not enabled". The live scalping engine already places
// SL/TP through this same direct client, so these tools simply expose that
// capability for manual management of ANY futures contract.
//
// The direct client is obtained from the shared wrapper via GetBaseClient(),
// reusing the exact authenticated instance the engine uses — no extra dial, no
// duplicate credential loading.

// toFuturesSymbol normalises a pair/symbol to KuCoin's futures format
// (e.g. "TIA-USDT" -> "TIAUSDTM", "TIAUSDTM" -> "TIAUSDTM"). Mirrors the
// unexported convertPairToFuturesSymbol in the gct package.
func toFuturesSymbol(s string) string {
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	if s == "" {
		return ""
	}
	if strings.HasSuffix(s, "M") {
		return s
	}
	return s + "M"
}

// ClosePositionDirectTool closes an existing futures position at market via the
// direct KuCoin REST client. It reads the position to use its EXACT contract
// size and the correct opposite side, so it works for any contract regardless
// of GCT's enabled list.
type ClosePositionDirectTool struct {
	BaseTool
}

func NewClosePositionDirectTool(client *gctclient.Client) *ClosePositionDirectTool {
	return &ClosePositionDirectTool{BaseTool: BaseTool{client: client}}
}

func (t *ClosePositionDirectTool) Name() string { return "close_position_direct" }

func (t *ClosePositionDirectTool) Description() string {
	return "Close an existing futures position at market via the DIRECT KuCoin REST client " +
		"(bypasses GoCryptoTrader's enabled-currency restriction). Works for any contract, " +
		"including positions opened manually or outside the bot. Uses the position's exact size."
}

func (t *ClosePositionDirectTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{"type": "string", "description": "Exchange name (default 'kucoin')"},
			"symbol":   map[string]interface{}{"type": "string", "description": "Futures symbol or pair (e.g. 'TIAUSDTM' or 'TIA-USDT')"},
		},
		"required": []string{"symbol"},
	}
}

func (t *ClosePositionDirectTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange := getStringParam(params, "exchange", "kucoin")
	rawSymbol := getStringParam(params, "symbol", "")
	if rawSymbol == "" {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: "symbol is required"}
	}
	symbol := toFuturesSymbol(rawSymbol)

	// ClientFor routes a paper profile to the paper broker; GetBaseClient would
	// always be the real exchange client.
	base, routeErr := t.client.ClientFor(ctx)
	if routeErr != nil {
		return nil, routeErr
	}
	if base == nil {
		return nil, fmt.Errorf("direct exchange client unavailable")
	}

	// Read the position first so the result reports what was closed (and to fail
	// clearly if there is nothing open).
	pos, err := base.GetFuturesPosition(ctx, exchange, symbol)
	if err != nil {
		return nil, fmt.Errorf("failed to read position for %s: %w", symbol, err)
	}
	if pos == nil || pos.Size == 0 {
		return map[string]interface{}{
			"success": false,
			"symbol":  symbol,
			"message": "no open position to close",
		}, nil
	}

	if err := base.CloseFuturesPosition(ctx, exchange, symbol); err != nil {
		return nil, fmt.Errorf("failed to close position %s: %w", symbol, err)
	}

	return map[string]interface{}{
		"success":        true,
		"symbol":         symbol,
		"closed_side":    pos.Side,
		"closed_size":    pos.Size,
		"entry_price":    pos.EntryPrice,
		"last_price":     pos.CurrentPrice,
		"unrealized_pnl": pos.UnrealizedPnL,
		"message":        fmt.Sprintf("closed %s %s (%.0f contracts) at market via direct REST", pos.Side, symbol, pos.Size),
		"client_via":     "direct-rest",
	}, nil
}

// SubmitFuturesOrderDirectTool submits a futures order (market, limit, or stop)
// via the direct KuCoin REST client. Use it to place protective stops or entries
// on contracts that GCT does not have enabled. For a protective stop on an
// existing position pass type='stop', stop_price, reduce_only=true and the side
// OPPOSITE to the position (buy to protect a short, sell to protect a long).
//
// amount is the USD notional target; the futures manager converts it to the
// right number of contracts using the contract multiplier. reduce_only caps the
// fill to the open position so it can only reduce, never flip.
type SubmitFuturesOrderDirectTool struct {
	BaseTool
}

func NewSubmitFuturesOrderDirectTool(client *gctclient.Client) *SubmitFuturesOrderDirectTool {
	return &SubmitFuturesOrderDirectTool{BaseTool: BaseTool{client: client}}
}

func (t *SubmitFuturesOrderDirectTool) Name() string { return "submit_futures_order_direct" }

func (t *SubmitFuturesOrderDirectTool) Description() string {
	return "Submit a futures order (market/limit/stop) via the DIRECT KuCoin REST client, " +
		"bypassing GoCryptoTrader's enabled-currency restriction. Supports protective stops " +
		"(type='stop' + stop_price + reduce_only) and take-profit/entry orders on ANY contract."
}

func (t *SubmitFuturesOrderDirectTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange":        map[string]interface{}{"type": "string", "description": "Exchange name (default 'kucoin')"},
			"pair":            map[string]interface{}{"type": "string", "description": "Pair or futures symbol (e.g. 'TIA-USDT' or 'TIAUSDTM')"},
			"side":            map[string]interface{}{"type": "string", "description": "'buy' or 'sell'", "enum": []string{"buy", "sell"}},
			"type":            map[string]interface{}{"type": "string", "description": "'market', 'limit', or 'stop' (default 'market')", "enum": []string{"market", "limit", "stop"}},
			"amount":          map[string]interface{}{"type": "number", "description": "USD notional target (converted to contracts)"},
			"price":           map[string]interface{}{"type": "number", "description": "Limit price (required for limit orders)"},
			"stop_price":      map[string]interface{}{"type": "number", "description": "Stop trigger price (required for stop orders)"},
			"stop_price_type": map[string]interface{}{"type": "string", "description": "'TP' (trade price, default) or 'MP' (mark price)", "enum": []string{"TP", "MP"}},
			"reduce_only":     map[string]interface{}{"type": "boolean", "description": "Only reduce/close the existing position, never increase it"},
			"close_order":     map[string]interface{}{"type": "boolean", "description": "Close position in Hedge Mode"},
			"leverage":        map[string]interface{}{"type": "number", "description": "Leverage (1-100, default 1)"},
		},
		"required": []string{"pair", "side", "amount"},
	}
}

func (t *SubmitFuturesOrderDirectTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange := getStringParam(params, "exchange", "kucoin")
	pair := getStringParam(params, "pair", "")
	side := getStringParam(params, "side", "")
	if pair == "" {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: "pair is required"}
	}
	if side != "buy" && side != "sell" {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: "side must be 'buy' or 'sell'"}
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
	stopPriceType := getStringParam(params, "stop_price_type", "")

	if orderType == "limit" && price <= 0 {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: "price is required for limit orders"}
	}
	if orderType == "stop" && stopPrice <= 0 {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: "stop_price is required for stop orders"}
	}

	// ClientFor routes a paper profile to the paper broker; GetBaseClient would
	// always be the real exchange client.
	base, routeErr := t.client.ClientFor(ctx)
	if routeErr != nil {
		return nil, routeErr
	}
	if base == nil {
		return nil, fmt.Errorf("direct exchange client unavailable")
	}

	req := &gct.SubmitOrderRequest{
		Exchange:      exchange,
		Pair:          pair,
		Side:          side,
		Type:          orderType,
		Amount:        amount,
		Price:         price,
		StopPrice:     stopPrice,
		StopPriceType: stopPriceType,
		ReduceOnly:    reduceOnly,
		CloseOrder:    closeOrder,
		Leverage:      leverage,
		ClientID:      resolveClientOrderID("", exchange, pair, side, orderType, amount, price),
		MarketType:    gct.MarketTypeFutures,
	}

	order, err := base.SubmitFuturesOrder(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to submit %s futures order for %s: %w", orderType, pair, err)
	}

	return map[string]interface{}{
		"success":    true,
		"order":      order,
		"client_via": "direct-rest",
	}, nil
}
