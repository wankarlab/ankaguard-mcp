package scalping

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/scalping/executor"
	"github.com/rantipay/trading-mcp/internal/scalping/models"
)

// ExecuteTradeTool implementa la tool para ejecutar trades de scalping
type ExecuteTradeTool struct {
	adapter *Adapter
}

// NewExecuteTradeTool crea una nueva instancia de la tool
func NewExecuteTradeTool(adapter *Adapter) *ExecuteTradeTool {
	return &ExecuteTradeTool{
		adapter: adapter,
	}
}

// Name retorna el nombre de la tool
func (t *ExecuteTradeTool) Name() string {
	return "execute_scalping_trade"
}

// Description retorna la descripción de la tool
func (t *ExecuteTradeTool) Description() string {
	return "Executes a scalping trade with automatic risk management and futures support. " +
		"Features: " +
		"- Automatic order type detection (spot for leverage=1, futures for leverage>1) " +
		"- Automatic symbol conversion for futures (BTC-USDT → BTCUSDTM) " +
		"- Automatic Stop Loss and Take Profit order creation for futures positions " +
		"- Risk limit validation and position size calculation " +
		"- Market order execution with low latency " +
		"Returns complete order details including main order ID, SL/TP order IDs, execution price, latency, and fees."
}

// InputSchema retorna el schema JSON de los parámetros de entrada
func (t *ExecuteTradeTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name (e.g., 'kucoin', 'binance')",
			},
			"pair": map[string]interface{}{
				"type":        "string",
				"description": "Trading pair (e.g., 'BTC-USDT')",
			},
			"side": map[string]interface{}{
				"type":        "string",
				"description": "Order side: 'buy' or 'sell'",
				"enum":        []string{"buy", "sell"},
			},
			"amount_usd": map[string]interface{}{
				"type":        "number",
				"description": "Amount in USD to trade",
				"minimum":     1.0,
			},
			"strategy": map[string]interface{}{
				"type":        "string",
				"description": "Strategy name (optional, for tracking)",
			},
			"stop_loss_percent": map[string]interface{}{
				"type":        "number",
				"description": "Stop loss percentage (optional, default: 0.5%)",
				"minimum":     0.1,
				"maximum":     5.0,
			},
			"take_profit_percent": map[string]interface{}{
				"type":        "number",
				"description": "Take profit percentage (optional, default: 1.0%)",
				"minimum":     0.1,
				"maximum":     10.0,
			},
			"leverage": map[string]interface{}{
				"type":        "number",
				"description": "Leverage for futures trading (optional, 1-100x, default: 1)",
				"minimum":     1.0,
				"maximum":     100.0,
			},
		},
		"required": []string{"exchange", "pair", "side", "amount_usd"},
	}
}

// Execute ejecuta la tool
func (t *ExecuteTradeTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	// Parsear parámetros
	req := ExecuteTradeRequest{
		Exchange:          getStringParam(params, "exchange"),
		Pair:              getStringParam(params, "pair"),
		Side:              getStringParam(params, "side"),
		AmountUSD:         getFloat64Param(params, "amount_usd"),
		Strategy:          getStringParam(params, "strategy"),
		StopLossPercent:   getFloat64Param(params, "stop_loss_percent"),
		TakeProfitPercent: getFloat64Param(params, "take_profit_percent"),
		Leverage:          getFloat64Param(params, "leverage"),
	}

	// Validar parámetros
	if err := t.validateRequest(&req); err != nil {
		return nil, err
	}

	// Aplicar defaults
	if req.StopLossPercent == 0 {
		req.StopLossPercent = 0.5
	}
	if req.TakeProfitPercent == 0 {
		req.TakeProfitPercent = 1.0
	}
	if req.Leverage == 0 {
		req.Leverage = 1
	}

	if req.Leverage > 1 {
		t.adapter.GetLogger().Infof("Executing %s trade for %s: %.2f USD with %dx leverage (SL: %.2f%%, TP: %.2f%%)",
			req.Side, req.Pair, req.AmountUSD, int(req.Leverage), req.StopLossPercent, req.TakeProfitPercent)
	} else {
		t.adapter.GetLogger().Infof("Executing %s trade for %s: %.2f USD (SL: %.2f%%, TP: %.2f%%)",
			req.Side, req.Pair, req.AmountUSD, req.StopLossPercent, req.TakeProfitPercent)
	}

	// Verificar límites de riesgo
	canTrade, reason := t.adapter.GetRiskManager().CheckRiskLimits()
	if !canTrade {
		return nil, fmt.Errorf("%w: %s", ErrExceedsRiskLimits, reason)
	}

	// Obtener precio actual con cliente resiliente (reintentos + circuit breaker)
	var ticker *gct.Ticker
	err := t.adapter.GetResilientClient().ExecuteWithResilience(ctx, "GetTicker", func(ctx context.Context) error {
		var err error
		ticker, err = t.adapter.GetGCTClient().GetTicker(ctx, req.Exchange, req.Pair)
		return err
	})

	if err != nil {
		// Translate error to MCP-friendly format
		translatedErr := translateKuCoinError(err)
		return nil, fmt.Errorf("failed to get ticker: %w", translatedErr)
	}

	// Validar precio - usar el precio más apropiado según el lado
	var executionPrice float64
	if req.Side == "buy" {
		// Para comprar, usar el Ask (precio de venta del vendedor)
		executionPrice = ticker.Ask
		if executionPrice <= 0 {
			executionPrice = ticker.Last
		}
	} else {
		// Para vender, usar el Bid (precio de compra del comprador)
		executionPrice = ticker.Bid
		if executionPrice <= 0 {
			executionPrice = ticker.Last
		}
	}

	// Validación final del precio
	if executionPrice <= 0 {
		return nil, fmt.Errorf("invalid ticker price: Last=%.8f, Bid=%.8f, Ask=%.8f for %s on %s",
			ticker.Last, ticker.Bid, ticker.Ask, req.Pair, req.Exchange)
	}

	t.adapter.GetLogger().Infof("Ticker data: Last=%.8f, Bid=%.8f, Ask=%.8f, Using=%.8f",
		ticker.Last, ticker.Bid, ticker.Ask, executionPrice)

	// Crear señal para el executor
	signal := &models.ScalpingSignal{
		ID:         fmt.Sprintf("manual_%d", time.Now().UnixNano()),
		Pair:       req.Pair,
		Exchange:   req.Exchange,
		Direction:  req.Side,
		Confidence: 1.0, // Manual trade tiene confianza máxima
		Strategies: []string{req.Strategy},
		Price:      executionPrice,
		Volume:     ticker.Volume,
		Timestamp:  time.Now(),
		Metadata:   make(map[string]interface{}),
	}

	// Pasar parámetros de SL/TP al metadata para que el executor los use
	signal.Metadata["stop_loss_percent"] = req.StopLossPercent
	signal.Metadata["take_profit_percent"] = req.TakeProfitPercent

	// Calcular cantidad en base currency
	// Para futures: Size = USD / price (el leverage se aplica automáticamente por KuCoin)
	// El margen requerido será: (Size * Price) / Leverage
	// Ejemplo: $9 USD, precio $0.16, leverage 5x
	// Size = 9 / 0.16 = 56.25 DOGE
	// Margen = (56.25 * 0.16) / 5 = $1.8 USD
	amount := req.AmountUSD / executionPrice

	t.adapter.GetLogger().Infof("Amount calculation: USD=%.2f, Leverage=%.0fx, Price=%.8f, Amount=%.8f",
		req.AmountUSD, req.Leverage, executionPrice, amount)

	// Validar que el amount no sea infinito o NaN
	if amount <= 0 || amount != amount { // NaN check
		return nil, fmt.Errorf("invalid calculated amount: %.8f (price: %.8f, usd: %.2f, leverage: %.0fx)",
			amount, executionPrice, req.AmountUSD, req.Leverage)
	}

	// Validar que no sea infinito
	if amount > 1e15 { // Número arbitrariamente grande
		return nil, fmt.Errorf("calculated amount is too large (possibly infinite): %.8f (price: %.8f, usd: %.2f, leverage: %.0fx)",
			amount, executionPrice, req.AmountUSD, req.Leverage)
	}

	// Ejecutar orden con leverage usando cliente resiliente
	var execution *executor.OrderExecution
	err = t.adapter.GetResilientClient().ExecuteWithResilience(ctx, "ExecuteOrder", func(ctx context.Context) error {
		var err error
		execution, err = t.adapter.GetExecutor().ExecuteOrderWithLeverage(ctx, signal, amount, req.Leverage)
		return err
	})

	if err != nil {
		// Translate error to MCP-friendly format
		translatedErr := translateKuCoinError(err)
		t.adapter.GetLogger().Errorf("Order execution failed: %v", translatedErr)
		return nil, translatedErr
	}

	// Crear respuesta con todos los detalles incluyendo SL/TP order IDs
	response := &ExecuteTradeResponse{
		OrderID:           execution.OrderID,
		StopLossOrderID:   execution.StopLossOrderID,   // Incluir ID de orden SL
		TakeProfitOrderID: execution.TakeProfitOrderID, // Incluir ID de orden TP
		Exchange:          req.Exchange,
		Pair:              execution.Pair, // Usar el par del execution (puede estar convertido a futures)
		Side:              req.Side,
		Amount:            execution.Amount,
		Price:             execution.Price,
		StopLoss:          execution.StopLossPrice,   // Precio de SL (calculado por executor)
		TakeProfit:        execution.TakeProfitPrice, // Precio de TP (calculado por executor)
		Leverage:          req.Leverage,
		Status:            execution.Status,
		ExecutedAt:        execution.ExecutedAt.Format(time.RFC3339),
		Latency:           execution.Latency.String(),
		EstimatedFee:      execution.Fee,
	}

	t.adapter.GetLogger().Infof("Trade executed successfully: %s %s %.8f @ %.8f (order: %s, latency: %v)",
		req.Side, req.Pair, execution.Amount, execution.Price, execution.OrderID, execution.Latency)

	return response, nil
}

// validateRequest valida la solicitud
func (t *ExecuteTradeTool) validateRequest(req *ExecuteTradeRequest) error {
	if req.Exchange == "" {
		return ErrInvalidExchange
	}
	if req.Pair == "" {
		return ErrInvalidPair
	}
	if req.Side != "buy" && req.Side != "sell" {
		return ErrInvalidSide
	}
	if req.AmountUSD <= 0 {
		return ErrInvalidAmount
	}

	// Verificar límites de riesgo
	riskParams := t.adapter.GetRiskManager().GetRiskParameters()
	if req.AmountUSD > riskParams.MaxPositionSizeUSD {
		return fmt.Errorf("%w: amount %.2f exceeds max position size %.2f",
			ErrExceedsRiskLimits, req.AmountUSD, riskParams.MaxPositionSizeUSD)
	}

	return nil
}

// calculateStopLoss calcula el precio de stop loss
func (t *ExecuteTradeTool) calculateStopLoss(price float64, side string, percent float64) float64 {
	if side == "buy" {
		return price * (1 - percent/100)
	}
	return price * (1 + percent/100)
}

// calculateTakeProfit calcula el precio de take profit
func (t *ExecuteTradeTool) calculateTakeProfit(price float64, side string, percent float64) float64 {
	if side == "buy" {
		return price * (1 + percent/100)
	}
	return price * (1 - percent/100)
}

// translateKuCoinError translates KuCoin errors to MCP-friendly format
// This provides clear, actionable error messages for AI agents
func translateKuCoinError(err error) error {
	if err == nil {
		return nil
	}

	errStr := err.Error()

	// Error 330005: Margin mode mismatch
	if strings.Contains(errStr, "330005") || strings.Contains(errStr, "MARGIN_MODE_MISMATCH") {
		return fmt.Errorf("MARGIN_MODE_MISMATCH: The position margin mode does not match. " +
			"This usually means you have an existing position with a different margin mode (ISOLATED vs CROSSED). " +
			"Please close existing positions or change the margin mode in KuCoin web interface before placing new orders")
	}

	// Error 400007: Access denied / futures permission required
	if strings.Contains(errStr, "400007") || strings.Contains(errStr, "FUTURES_PERMISSION_REQUIRED") {
		return fmt.Errorf("FUTURES_PERMISSION_REQUIRED: Futures trading permission not enabled on your KuCoin API key. " +
			"Please enable 'Futures Trading' permission in your KuCoin API key settings at https://www.kucoin.com/account/api")
	}

	// Error 200004: Insufficient balance
	if strings.Contains(errStr, "200004") || strings.Contains(errStr, "insufficient") {
		return fmt.Errorf("INSUFFICIENT_BALANCE: Not enough balance to place this order. " +
			"Please deposit funds to your futures account or reduce the order size")
	}

	// Error 401: Unauthorized / invalid credentials
	if strings.Contains(errStr, "401") || strings.Contains(errStr, "Unauthorized") || strings.Contains(errStr, "INVALID_CREDENTIALS") {
		return fmt.Errorf("INVALID_CREDENTIALS: Your KuCoin API credentials are invalid or expired. " +
			"Please verify your API_KEY, API_SECRET, and API_PASSWORD environment variables")
	}

	// Leverage errors
	if strings.Contains(errStr, "leverage") && (strings.Contains(errStr, "invalid") || strings.Contains(errStr, "out of")) {
		return fmt.Errorf("INVALID_LEVERAGE: Leverage value is out of allowed range (1-100x). " +
			"Please use a leverage value between 1 and 100")
	}

	// Symbol conversion errors
	if strings.Contains(errStr, "invalid symbol") || strings.Contains(errStr, "symbol format") {
		return fmt.Errorf("INVALID_SYMBOL: The trading symbol format is invalid. " +
			"Please use the correct format (e.g., BTC-USDT for spot, BTCUSDTM for futures)")
	}

	// Network/timeout errors
	if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "connection") {
		return fmt.Errorf("NETWORK_ERROR: Connection to exchange timed out or failed. " +
			"Please check your internet connection and try again")
	}

	// Rate limiting
	if strings.Contains(errStr, "rate limit") || strings.Contains(errStr, "429") {
		return fmt.Errorf("RATE_LIMIT_EXCEEDED: Too many requests to the exchange API. " +
			"Please wait a moment and try again")
	}

	// Return original error if no translation available
	return err
}
