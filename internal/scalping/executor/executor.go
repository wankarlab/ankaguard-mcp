package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/scalping/models"
)

// Error definitions for futures trading
var (
	ErrInvalidLeverage    = errors.New("invalid leverage value")
	ErrInvalidSymbol      = errors.New("invalid futures symbol")
	ErrStopLossCreation   = errors.New("stop loss creation failed")
	ErrTakeProfitCreation = errors.New("take profit creation failed")
	// ErrOrderSubmittedUnconfirmed signals that the order reached the exchange
	// (has a valid order ID) but confirmation polling timed out. The caller must
	// NOT retry — doing so would submit a duplicate order. Treat the trade as
	// submitted-but-unconfirmed and let the position sync pick it up.
	ErrOrderSubmittedUnconfirmed = errors.New("order submitted but confirmation timed out — do not retry")
)

// HighFrequencyExecutor ejecuta órdenes de alta frecuencia con baja latencia
type HighFrequencyExecutor struct {
	mu        sync.RWMutex
	logger    *logrus.Logger
	gctClient gct.GCTClient

	// Métricas de latencia
	latencyStats *LatencyStats
	orderHistory []*OrderExecution
	maxHistory   int

	// Configuración
	maxRetries   int
	retryDelay   time.Duration
	timeout      time.Duration
	useWebSocket bool

	// B2: graceful mode fallback — when enabled, a futures order that fails
	// with a market/config error (margin-mode mismatch, leverage rejected,
	// KuCoin 404 on marginMode, contract-not-found) is retried as a spot
	// order instead of being dropped entirely.
	// Safe default: false (live). Set to true for paper / non-live sessions.
	EnableModeFallback bool

	// Rate limiting
	rateLimiter *RateLimiter
}

// LatencyStats contiene estadísticas de latencia
type LatencyStats struct {
	mu               sync.RWMutex
	TotalOrders      int           `json:"total_orders"`
	SuccessfulOrders int           `json:"successful_orders"`
	FailedOrders     int           `json:"failed_orders"`
	AvgLatency       time.Duration `json:"avg_latency"`
	MinLatency       time.Duration `json:"min_latency"`
	MaxLatency       time.Duration `json:"max_latency"`
	P50Latency       time.Duration `json:"p50_latency"`
	P95Latency       time.Duration `json:"p95_latency"`
	P99Latency       time.Duration `json:"p99_latency"`
	LastUpdated      time.Time     `json:"last_updated"`
	latencies        []time.Duration
}

// OrderExecution representa una ejecución de orden
type OrderExecution struct {
	OrderID           string
	StopLossOrderID   string // ID de la orden de stop loss
	TakeProfitOrderID string // ID de la orden de take profit
	Signal            *models.ScalpingSignal
	Exchange          string
	Pair              string
	Side              string
	Amount            float64
	Price             float64
	StopLossPrice     float64 // Precio del stop loss
	TakeProfitPrice   float64 // Precio del take profit
	Leverage          float64 // Leverage usado (1 para spot)
	OrderType         string
	Status            string
	Latency           time.Duration
	Slippage          float64
	Fee               float64
	ExecutedAt        time.Time
	ConfirmedAt       time.Time
	Error             error
	RetryCount        int
}

// RateLimiter controla la tasa de órdenes
type RateLimiter struct {
	mu            sync.Mutex
	maxPerSecond  int
	maxPerMinute  int
	secondCounter int
	minuteCounter int
	lastSecond    time.Time
	lastMinute    time.Time
}

// NewHighFrequencyExecutor crea un nuevo ejecutor de alta frecuencia
func NewHighFrequencyExecutor(logger *logrus.Logger, gctClient gct.GCTClient) *HighFrequencyExecutor {
	return &HighFrequencyExecutor{
		logger:    logger,
		gctClient: gctClient,
		latencyStats: &LatencyStats{
			MinLatency: time.Hour, // Inicializar con valor alto
			latencies:  make([]time.Duration, 0, 1000),
		},
		orderHistory: make([]*OrderExecution, 0, 1000),
		maxHistory:   1000,
		maxRetries:   3,
		retryDelay:   100 * time.Millisecond,
		timeout:      5 * time.Second,
		useWebSocket: true,
		rateLimiter: &RateLimiter{
			maxPerSecond: 10,
			maxPerMinute: 100,
			lastSecond:   time.Now(),
			lastMinute:   time.Now(),
		},
	}
}

// convertToFuturesSymbol converts a spot trading pair to futures symbol format
// Examples: BTC-USDT → BTCUSDTM, ETH-USDT → ETHUSDTM, SHIB-USDT → SHIBUSDTM
func (e *HighFrequencyExecutor) convertToFuturesSymbol(spotPair string) (string, error) {
	if spotPair == "" {
		return "", fmt.Errorf("%w: empty symbol", ErrInvalidSymbol)
	}

	// Check if already in futures format (ends with M)
	if strings.HasSuffix(spotPair, "M") {
		e.logger.Debugf("Symbol already in futures format: %s", spotPair)
		return spotPair, nil
	}

	// Remove hyphen and add 'M' suffix
	// BTC-USDT → BTCUSDTM
	futuresSymbol := strings.ReplaceAll(spotPair, "-", "") + "M"

	// Validate the conversion
	if len(futuresSymbol) < 4 { // Minimum: XYM (3 chars + M)
		return "", fmt.Errorf("%w: invalid symbol format after conversion: %s", ErrInvalidSymbol, futuresSymbol)
	}

	e.logger.Debugf("Converted symbol: %s → %s", spotPair, futuresSymbol)
	return futuresSymbol, nil
}

// ExecuteOrder ejecuta una orden con baja latencia (sin leverage)
func (e *HighFrequencyExecutor) ExecuteOrder(ctx context.Context, signal *models.ScalpingSignal, amount float64) (*OrderExecution, error) {
	return e.ExecuteOrderWithLeverage(ctx, signal, amount, 1)
}

// ExecuteOrderWithLeverage ejecuta una orden con baja latencia y leverage especificado
func (e *HighFrequencyExecutor) ExecuteOrderWithLeverage(ctx context.Context, signal *models.ScalpingSignal, amount float64, leverage float64) (*OrderExecution, error) {
	start := time.Now()

	// Validate leverage range (1x to 100x)
	if leverage < 1 || leverage > 100 {
		return nil, fmt.Errorf("%w: leverage must be between 1 and 100, got %.2f", ErrInvalidLeverage, leverage)
	}

	// Verificar rate limiting
	if !e.rateLimiter.Allow() {
		return nil, fmt.Errorf("rate limit exceeded")
	}

	// Crear contexto con timeout
	execCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	// Preparar orden con leverage
	orderReq := e.prepareOrderWithLeverage(signal, amount, leverage)

	// Ejecutar con retry
	var execution *OrderExecution
	var err error

	for attempt := 0; attempt <= e.maxRetries; attempt++ {
		execution, err = e.executeWithRetry(execCtx, orderReq, signal, attempt)
		if err == nil {
			break
		}

		// Order reached the exchange but confirmation timed out — do not retry
		// to avoid placing a duplicate order on the exchange.
		if errors.Is(err, ErrOrderSubmittedUnconfirmed) {
			e.logger.WithField("attempt", attempt+1).Warn("Order submitted but unconfirmed — aborting retry loop to prevent double-entry")
			break
		}

		// Hard exchange rejection (e.g. insufficient margin) — retrying will
		// never succeed until an external condition changes. Stop immediately.
		if isNonRetryableError(err) {
			e.logger.WithField("attempt", attempt+1).Warn("Non-retryable exchange error — aborting retry loop")
			break
		}

		if attempt < e.maxRetries {
			e.logger.Warnf("Order execution failed (attempt %d/%d): %v", attempt+1, e.maxRetries, err)
			time.Sleep(e.retryDelay)
		}
	}

	if errors.Is(err, ErrOrderSubmittedUnconfirmed) {
		// Order was placed on the exchange but we couldn't confirm it in time.
		// Return a best-effort execution record so callers can track it; clear
		// the error so the position sync loop (not a retry) handles reconciliation.
		execution = &OrderExecution{
			Signal:     signal,
			Exchange:   signal.Exchange,
			Pair:       signal.Pair,
			Status:     "submitted_unconfirmed",
			RetryCount: 0,
			ExecutedAt: time.Now(),
		}
		return execution, nil
	}

	if err != nil {
		e.logger.Errorf("Order execution failed after %d attempts: %v", e.maxRetries+1, err)
		execution = &OrderExecution{
			Signal:     signal,
			Exchange:   signal.Exchange,
			Pair:       signal.Pair,
			Status:     "failed",
			Error:      err,
			RetryCount: e.maxRetries + 1,
			ExecutedAt: time.Now(),
		}
		return execution, err
	}

	// Create Stop Loss and Take Profit orders for futures positions
	var slLatency, tpLatency time.Duration
	if leverage > 1 {
		// Extract SL/TP parameters from signal metadata
		stopLossPercent, hasSL := signal.Metadata["stop_loss_percent"].(float64)
		takeProfitPercent, hasTP := signal.Metadata["take_profit_percent"].(float64)

		// Create Stop Loss order
		if hasSL && stopLossPercent > 0 {
			slStart := time.Now()
			slOrderID, slErr := e.createStopLossOrder(execCtx, execution, stopLossPercent)
			// SCALP-06 fix (c): a failed SL placement used to be logged and
			// otherwise ignored, leaving a leveraged position open with no
			// exchange-side protection. Retry with a short backoff before
			// giving up, and if it still fails, escalate by closing the
			// now-unprotected position immediately — leaving it open with
			// no stop-loss is a worse outcome than closing on a placement
			// failure.
			for attempt := 0; slErr != nil && attempt < 2; attempt++ {
				time.Sleep(e.retryDelay)
				slOrderID, slErr = e.createStopLossOrder(execCtx, execution, stopLossPercent)
			}
			slLatency = time.Since(slStart)

			if slErr != nil {
				e.logger.WithFields(logrus.Fields{
					"error":      slErr.Error(),
					"sl_latency": slLatency,
					"pair":       execution.Pair,
					"exchange":   execution.Exchange,
				}).Error("🚨 Stop loss order creation failed after retries — closing now-unprotected position")
				if closeErr := e.gctClient.CloseFuturesPosition(execCtx, execution.Exchange, execution.Pair); closeErr != nil {
					e.logger.WithError(closeErr).Error("🚨🚨 Failed to close unprotected position after SL placement failure — MANUAL INTERVENTION REQUIRED")
				} else {
					execution.Status = "closed_unprotected_sl_failure"
				}
			} else if slOrderID != "" {
				execution.StopLossOrderID = slOrderID
				// Calculate and store SL price
				if execution.Side == "buy" {
					execution.StopLossPrice = execution.Price * (1 - stopLossPercent/100)
				} else {
					execution.StopLossPrice = execution.Price * (1 + stopLossPercent/100)
				}
				e.logger.WithFields(logrus.Fields{
					"sl_order_id": slOrderID,
					"sl_price":    execution.StopLossPrice,
					"sl_latency":  slLatency,
				}).Info("✓ Stop loss order created")
			}
		}

		// Create Take Profit order
		if hasTP && takeProfitPercent > 0 {
			tpStart := time.Now()
			tpOrderID, tpErr := e.createTakeProfitOrder(execCtx, execution, takeProfitPercent)
			tpLatency = time.Since(tpStart)

			if tpErr != nil {
				// Log warning but don't fail the main order
				e.logger.WithFields(logrus.Fields{
					"error":      tpErr.Error(),
					"tp_latency": tpLatency,
				}).Warn("✗ Failed to create take profit order (non-fatal)")
			} else if tpOrderID != "" {
				execution.TakeProfitOrderID = tpOrderID
				// Calculate and store TP price
				if execution.Side == "buy" {
					execution.TakeProfitPrice = execution.Price * (1 + takeProfitPercent/100)
				} else {
					execution.TakeProfitPrice = execution.Price * (1 - takeProfitPercent/100)
				}
				e.logger.WithFields(logrus.Fields{
					"tp_order_id": tpOrderID,
					"tp_price":    execution.TakeProfitPrice,
					"tp_latency":  tpLatency,
				}).Info("✓ Take profit order created")
			}
		}
	}

	// Calcular latencia total
	execution.Latency = time.Since(start)

	// Actualizar estadísticas
	e.updateStats(execution)

	// Guardar en historial
	e.addToHistory(execution)

	// Log resultado detallado con latencias completas
	if leverage > 1 {
		e.logger.WithFields(logrus.Fields{
			"side":          execution.Side,
			"pair":          execution.Pair,
			"amount":        execution.Amount,
			"price":         execution.Price,
			"leverage":      int(leverage),
			"sl_order_id":   execution.StopLossOrderID,
			"tp_order_id":   execution.TakeProfitOrderID,
			"total_latency": execution.Latency,
			"sl_latency":    slLatency,
			"tp_latency":    tpLatency,
		}).Info("✓ Futures order executed successfully")
	} else {
		e.logger.WithFields(logrus.Fields{
			"side":          execution.Side,
			"pair":          execution.Pair,
			"amount":        execution.Amount,
			"price":         execution.Price,
			"total_latency": execution.Latency,
		}).Info("✓ Spot order executed successfully")
	}

	return execution, nil
}

// isNonRetryableError reports whether err is a hard exchange rejection that
// retrying will never resolve — e.g. insufficient margin, position-limit hit,
// invalid symbol. These should abort all retry loops immediately.
func isNonRetryableError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	// KuCoin 330008 = "insufficient available margin" — retrying won't help until
	// existing positions close and free up margin.
	for _, keyword := range []string{"code=330008", "insufficient available margin", "insufficient margin"} {
		if strings.Contains(msg, keyword) {
			return true
		}
	}
	return false
}

// isFuturesFallbackableError reports whether err represents a market-side or
// exchange-configuration error that a spot order could avoid.  It returns true
// only for well-known retryable conditions so that genuine hard failures
// (insufficient balance, risk-limit rejection, auth errors) still propagate.
//
// Matched patterns (case-insensitive substrings):
//   - "404" / "not found"           → KuCoin marginMode endpoint missing
//   - "margin"                      → margin-mode mismatch / not enabled
//   - "leverage"                    → leverage value rejected by exchange
//   - "not supported"               → futures market unavailable on this pair
//   - "contract"                    → contract-not-found (perpetual not listed)
func isFuturesFallbackableError(err error) bool {
	if err == nil {
		return false
	}
	// Insufficient margin is a hard limit — spot fallback won't help.
	if isNonRetryableError(err) {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, keyword := range []string{"404", "not found", "margin", "leverage", "not supported", "contract"} {
		if strings.Contains(msg, keyword) {
			return true
		}
	}
	return false
}

// executeWithRetry ejecuta la orden con un intento específico
func (e *HighFrequencyExecutor) executeWithRetry(ctx context.Context, orderReq *gct.SubmitOrderRequest, signal *models.ScalpingSignal, attempt int) (*OrderExecution, error) {
	execStart := time.Now()

	// Log inicio con contexto completo
	e.logger.WithFields(logrus.Fields{
		"pair":     orderReq.Pair,
		"side":     orderReq.Side,
		"amount":   orderReq.Amount,
		"leverage": orderReq.Leverage,
		"attempt":  attempt,
	}).Info("→ Starting order execution")

	var gctOrder *gct.Order
	var err error

	// Medir latencia de detección y routing
	routingStart := time.Now()

	// Detect order type based on leverage and route accordingly.
	// B2: if futures submission fails with a market/config error and
	// EnableModeFallback is true, retry as a spot order.
	if orderReq.Leverage > 1 {
		// Futures order
		e.logger.Infof("→ Routing to FUTURES (leverage: %dx)", int(orderReq.Leverage))
		gctOrder, err = e.gctClient.SubmitFuturesOrder(ctx, orderReq)
		if err != nil {
			routingLatency := time.Since(routingStart)
			e.logger.WithFields(logrus.Fields{
				"error":           err.Error(),
				"routing_latency": routingLatency,
				"total_latency":   time.Since(execStart),
			}).Error("✗ Futures order submission failed")

			// B2: graceful mode fallback — only on market/config errors
			if e.EnableModeFallback && isFuturesFallbackableError(err) {
				e.logger.WithFields(logrus.Fields{
					"original_error":  err.Error(),
					"pair":            orderReq.Pair,
					"original_market": "futures",
					"fallback_market": "spot",
				}).Warn("[B2] Futures order rejected with market/config error — falling back to spot")

				// Build a spot-compatible copy of the request
				spotReq := &gct.SubmitOrderRequest{
					Exchange:   orderReq.Exchange,
					Pair:       signal.Pair, // use original spot pair, not converted futures symbol
					Side:       orderReq.Side,
					Type:       orderReq.Type,
					Amount:     orderReq.Amount,
					Price:      orderReq.Price,
					ClientID:   orderReq.ClientID + "_fb",
					Leverage:   1,
					MarketType: gct.MarketTypeSpot,
				}

				gctOrder, err = e.gctClient.SubmitOrder(ctx, spotReq)
				if err != nil {
					e.logger.WithFields(logrus.Fields{
						"error":           err.Error(),
						"routing_latency": time.Since(routingStart),
					}).Error("[B2] Spot fallback order also failed")
					return nil, fmt.Errorf("futures order failed and spot fallback also failed: %w", err)
				}

				// Reflect the downgrade in the request so the OrderExecution
				// struct accurately records leverage=1 and the spot pair.
				orderReq = spotReq

				e.logger.WithFields(logrus.Fields{
					"order_id":        gctOrder.ID,
					"pair":            spotReq.Pair,
					"routing_latency": time.Since(routingStart),
				}).Info("[B2] Spot fallback order submitted successfully")
			} else {
				return nil, fmt.Errorf("failed to submit futures order: %w", err)
			}
		}
	} else {
		// Orden SPOT
		e.logger.Infof("→ Routing to SPOT")
		gctOrder, err = e.gctClient.SubmitOrder(ctx, orderReq)
		if err != nil {
			routingLatency := time.Since(routingStart)
			e.logger.WithFields(logrus.Fields{
				"error":           err.Error(),
				"routing_latency": routingLatency,
				"total_latency":   time.Since(execStart),
			}).Error("✗ Spot order submission failed")
			return nil, fmt.Errorf("failed to submit spot order: %w", err)
		}
	}

	routingLatency := time.Since(routingStart)

	// Log latencia de orden
	e.logger.WithFields(logrus.Fields{
		"order_id":        gctOrder.ID,
		"routing_latency": routingLatency,
	}).Info("✓ Order submitted to exchange")

	// Medir latencia de confirmación
	confirmStart := time.Now()
	confirmedOrder, err := e.waitForConfirmation(ctx, signal.Exchange, gctOrder.ID, 2*time.Second)
	// SCALP-06 fix (b): a confirmation timeout used to skip SL/TP placement
	// entirely (the caller returned before the leverage>1 SL/TP block ever
	// ran), leaving a real leveraged position with no exchange-side
	// protection. Retry confirmation a couple more times with a short
	// backoff — the order already landed on the exchange (we have its ID),
	// so this is just waiting longer for it to settle, not re-submitting.
	for retry := 0; err != nil && retry < 2; retry++ {
		time.Sleep(1 * time.Second)
		confirmedOrder, err = e.waitForConfirmation(ctx, signal.Exchange, gctOrder.ID, 2*time.Second)
	}
	confirmLatency := time.Since(confirmStart)

	if err != nil {
		e.logger.WithFields(logrus.Fields{
			"order_id":        gctOrder.ID,
			"confirm_latency": confirmLatency,
			"total_latency":   time.Since(execStart),
		}).Warn("✗ Order confirmation timed out — order was submitted, recording as unconfirmed to prevent double-entry")
		// Wrap with ErrOrderSubmittedUnconfirmed so the retry loop knows the
		// order already landed on the exchange and must not be re-submitted.
		return nil, fmt.Errorf("%w (order_id=%s): %v", ErrOrderSubmittedUnconfirmed, gctOrder.ID, err)
	}

	// Calcular slippage
	slippage := e.calculateSlippage(signal.Price, confirmedOrder.Price)

	// Log latencias finales
	totalLatency := time.Since(execStart)
	e.logger.WithFields(logrus.Fields{
		"order_id":        gctOrder.ID,
		"routing_latency": routingLatency,
		"confirm_latency": confirmLatency,
		"total_latency":   totalLatency,
		"price":           confirmedOrder.Price,
		"slippage":        slippage,
	}).Info("✓ Order execution completed")

	execution := &OrderExecution{
		OrderID:     gctOrder.ID,
		Signal:      signal,
		Exchange:    signal.Exchange,
		Pair:        orderReq.Pair, // Usar el par del request (ya convertido si es futures)
		Side:        orderReq.Side,
		Amount:      confirmedOrder.Amount,
		Price:       confirmedOrder.Price,
		Leverage:    orderReq.Leverage,
		OrderType:   orderReq.Type,
		Status:      "executed",
		Slippage:    slippage,
		Fee:         0.0, // TODO: Calcular fee desde la orden
		ExecutedAt:  execStart,
		ConfirmedAt: time.Now(),
		RetryCount:  attempt,
	}

	return execution, nil
}

// prepareOrder prepara una orden para ejecución (sin leverage)
func (e *HighFrequencyExecutor) prepareOrder(signal *models.ScalpingSignal, amount float64) *gct.SubmitOrderRequest {
	return e.prepareOrderWithLeverage(signal, amount, 1)
}

// prepareOrderWithLeverage prepara una orden para ejecución con leverage
func (e *HighFrequencyExecutor) prepareOrderWithLeverage(signal *models.ScalpingSignal, amount float64, leverage float64) *gct.SubmitOrderRequest {
	// Pre-flight validations
	if signal == nil {
		e.logger.Error("Signal is nil, cannot prepare order")
		return nil
	}

	if signal.Exchange == "" {
		e.logger.Error("Exchange is empty, cannot prepare order")
		return nil
	}

	if signal.Pair == "" {
		e.logger.Error("Trading pair is empty, cannot prepare order")
		return nil
	}

	if amount <= 0 {
		e.logger.Errorf("Invalid amount: %.8f, must be positive", amount)
		return nil
	}

	var side string
	if signal.Direction == "buy" {
		side = "buy"
	} else if signal.Direction == "sell" {
		side = "sell"
	} else {
		e.logger.Errorf("Invalid direction: %s, must be 'buy' or 'sell'", signal.Direction)
		return nil
	}

	// Determinar el par correcto basado en leverage
	pair := signal.Pair
	if leverage > 1 {
		// Convertir a símbolo de futuros
		futuresSymbol, err := e.convertToFuturesSymbol(signal.Pair)
		if err != nil {
			e.logger.Errorf("Failed to convert symbol to futures format: %v", err)
			// Fallback: usar el símbolo original
		} else {
			pair = futuresSymbol
			e.logger.Infof("Converted symbol for futures: %s → %s", signal.Pair, pair)
		}
	}

	// Generar ClientID único
	clientID := fmt.Sprintf("scalp_%d", time.Now().UnixNano())

	// Stamp MarketType to match the routing decision already made by the leverage check above.
	// This is a minimal non-behavior-changing stamp so the order request is self-describing.
	// Wave 1: if leverage > 1 we already converted to futures symbol — mark it explicitly.
	// Full mode.Resolver wiring is deferred to Wave 2 (executor wiring is delicate).
	var marketType gct.MarketType
	if leverage > 1 {
		marketType = gct.MarketTypeFutures
	}

	return &gct.SubmitOrderRequest{
		Exchange:   signal.Exchange,
		Pair:       pair,
		Side:       side,
		Type:       "market", // Market order for fast execution
		Amount:     amount,
		Price:      signal.Price, // Reference price
		ClientID:   clientID,
		Leverage:   leverage,
		MarketType: marketType,
	}
}

// waitForConfirmation espera la confirmación de la orden
func (e *HighFrequencyExecutor) waitForConfirmation(ctx context.Context, exchange, orderID string, timeout time.Duration) (*gct.Order, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("confirmation timeout")
		case <-ticker.C:
			order, err := e.gctClient.GetOrderInfo(ctx, exchange, orderID)
			if err != nil {
				continue
			}

			// Verificar si la orden está completa
			if order.Status == "filled" || order.Status == "closed" {
				return order, nil
			}
		}
	}
}

// CancelOrder cancela una orden
func (e *HighFrequencyExecutor) CancelOrder(ctx context.Context, exchange, orderID string) error {
	start := time.Now()

	err := e.gctClient.CancelOrder(ctx, exchange, orderID)
	if err != nil {
		return fmt.Errorf("failed to cancel order: %w", err)
	}

	latency := time.Since(start)
	e.logger.Infof("Order cancelled: %s (latency: %v)", orderID, latency)

	return nil
}

// createStopLossOrder creates a stop loss order for a futures position
// Returns the stop loss order ID or an error
func (e *HighFrequencyExecutor) createStopLossOrder(ctx context.Context, execution *OrderExecution, stopLossPercent float64) (string, error) {
	if stopLossPercent <= 0 {
		e.logger.Debug("Stop loss percent is 0 or negative, skipping SL creation")
		return "", nil
	}

	// Validate stop loss percentage range (0.1% to 5%)
	if stopLossPercent < 0.1 || stopLossPercent > 5.0 {
		return "", fmt.Errorf("%w: stop loss percent must be between 0.1%% and 5%%, got %.2f%%", ErrInvalidLeverage, stopLossPercent)
	}

	// Calculate stop loss price based on entry price and side
	var stopPrice float64
	if execution.Side == "buy" {
		// For long positions, SL is below entry price
		stopPrice = execution.Price * (1 - stopLossPercent/100)
	} else {
		// For short positions, SL is above entry price
		stopPrice = execution.Price * (1 + stopLossPercent/100)
	}

	// Determine closing side (opposite of entry)
	closeSide := "sell"
	if execution.Side == "sell" {
		closeSide = "buy"
	}

	// Generate unique client ID for stop loss order
	clientID := fmt.Sprintf("sl_%s_%d", execution.OrderID, time.Now().UnixNano())

	// Create stop loss order request
	slReq := &gct.SubmitOrderRequest{
		Exchange:      execution.Exchange,
		Pair:          execution.Pair,
		Side:          closeSide,
		Type:          "stop",
		Amount:        execution.Amount,
		Price:         stopPrice,
		StopPrice:     stopPrice,
		StopPriceType: "MP", // Mark Price
		ReduceOnly:    true,
		ClientID:      clientID,
		Leverage:      execution.Leverage,
	}

	e.logger.Infof("Creating stop loss order: %s %s %.8f @ %.8f (-%.2f%%)",
		closeSide, execution.Pair, execution.Amount, stopPrice, stopLossPercent)

	// Submit stop loss order as futures order
	slOrder, err := e.gctClient.SubmitFuturesOrder(ctx, slReq)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrStopLossCreation, err)
	}

	e.logger.Infof("Stop loss order created successfully: %s at %.8f", slOrder.ID, stopPrice)
	return slOrder.ID, nil
}

// createTakeProfitOrder creates a take profit order for a futures position
// Returns the take profit order ID or an error
func (e *HighFrequencyExecutor) createTakeProfitOrder(ctx context.Context, execution *OrderExecution, takeProfitPercent float64) (string, error) {
	if takeProfitPercent <= 0 {
		e.logger.Debug("Take profit percent is 0 or negative, skipping TP creation")
		return "", nil
	}

	// Validate take profit percentage range (0.1% to 10%)
	if takeProfitPercent < 0.1 || takeProfitPercent > 10.0 {
		return "", fmt.Errorf("%w: take profit percent must be between 0.1%% and 10%%, got %.2f%%", ErrInvalidLeverage, takeProfitPercent)
	}

	// Calculate take profit price based on entry price and side
	var tpPrice float64
	if execution.Side == "buy" {
		// For long positions, TP is above entry price
		tpPrice = execution.Price * (1 + takeProfitPercent/100)
	} else {
		// For short positions, TP is below entry price
		tpPrice = execution.Price * (1 - takeProfitPercent/100)
	}

	// Determine closing side (opposite of entry)
	closeSide := "sell"
	if execution.Side == "sell" {
		closeSide = "buy"
	}

	// Generate unique client ID for take profit order
	clientID := fmt.Sprintf("tp_%s_%d", execution.OrderID, time.Now().UnixNano())

	// Create take profit order request
	tpReq := &gct.SubmitOrderRequest{
		Exchange:      execution.Exchange,
		Pair:          execution.Pair,
		Side:          closeSide,
		Type:          "stop",
		Amount:        execution.Amount,
		Price:         tpPrice,
		StopPrice:     tpPrice,
		StopPriceType: "MP", // Mark Price
		ReduceOnly:    true,
		ClientID:      clientID,
		Leverage:      execution.Leverage,
	}

	e.logger.Infof("Creating take profit order: %s %s %.8f @ %.8f (+%.2f%%)",
		closeSide, execution.Pair, execution.Amount, tpPrice, takeProfitPercent)

	// Submit take profit order as futures order
	tpOrder, err := e.gctClient.SubmitFuturesOrder(ctx, tpReq)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrTakeProfitCreation, err)
	}

	e.logger.Infof("Take profit order created successfully: %s at %.8f", tpOrder.ID, tpPrice)
	return tpOrder.ID, nil
}

// calculateSlippage calcula el slippage entre precio esperado y ejecutado
func (e *HighFrequencyExecutor) calculateSlippage(expectedPrice, executedPrice float64) float64 {
	if expectedPrice == 0 {
		return 0
	}
	return ((executedPrice - expectedPrice) / expectedPrice) * 100
}

// updateStats actualiza las estadísticas de latencia
func (e *HighFrequencyExecutor) updateStats(execution *OrderExecution) {
	e.latencyStats.mu.Lock()
	defer e.latencyStats.mu.Unlock()

	e.latencyStats.TotalOrders++

	if execution.Error == nil {
		e.latencyStats.SuccessfulOrders++
	} else {
		e.latencyStats.FailedOrders++
	}

	// Actualizar latencias
	e.latencyStats.latencies = append(e.latencyStats.latencies, execution.Latency)

	// Mantener solo las últimas 1000 latencias
	if len(e.latencyStats.latencies) > 1000 {
		e.latencyStats.latencies = e.latencyStats.latencies[1:]
	}

	// Calcular estadísticas
	e.calculateLatencyStats()

	e.latencyStats.LastUpdated = time.Now()
}

// calculateLatencyStats calcula estadísticas de latencia
func (e *HighFrequencyExecutor) calculateLatencyStats() {
	if len(e.latencyStats.latencies) == 0 {
		return
	}

	// Calcular promedio
	var total time.Duration
	min := e.latencyStats.latencies[0]
	max := e.latencyStats.latencies[0]

	for _, lat := range e.latencyStats.latencies {
		total += lat
		if lat < min {
			min = lat
		}
		if lat > max {
			max = lat
		}
	}

	e.latencyStats.AvgLatency = total / time.Duration(len(e.latencyStats.latencies))
	e.latencyStats.MinLatency = min
	e.latencyStats.MaxLatency = max

	// Calcular percentiles (simplificado)
	// En producción, usar algoritmo más eficiente
	sorted := make([]time.Duration, len(e.latencyStats.latencies))
	copy(sorted, e.latencyStats.latencies)

	// Bubble sort simple (suficiente para 1000 elementos)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[i] > sorted[j] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	e.latencyStats.P50Latency = sorted[len(sorted)*50/100]
	e.latencyStats.P95Latency = sorted[len(sorted)*95/100]
	e.latencyStats.P99Latency = sorted[len(sorted)*99/100]
}

// addToHistory agrega una ejecución al historial
func (e *HighFrequencyExecutor) addToHistory(execution *OrderExecution) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.orderHistory = append(e.orderHistory, execution)

	// Mantener solo las últimas maxHistory ejecuciones
	if len(e.orderHistory) > e.maxHistory {
		e.orderHistory = e.orderHistory[1:]
	}
}

// GetLatencyStats retorna las estadísticas de latencia
func (e *HighFrequencyExecutor) GetLatencyStats() *LatencyStats {
	e.latencyStats.mu.RLock()
	defer e.latencyStats.mu.RUnlock()

	// Retornar copia
	return &LatencyStats{
		TotalOrders:      e.latencyStats.TotalOrders,
		SuccessfulOrders: e.latencyStats.SuccessfulOrders,
		FailedOrders:     e.latencyStats.FailedOrders,
		AvgLatency:       e.latencyStats.AvgLatency,
		MinLatency:       e.latencyStats.MinLatency,
		MaxLatency:       e.latencyStats.MaxLatency,
		P50Latency:       e.latencyStats.P50Latency,
		P95Latency:       e.latencyStats.P95Latency,
		P99Latency:       e.latencyStats.P99Latency,
		LastUpdated:      e.latencyStats.LastUpdated,
	}
}

// GetOrderHistory retorna el historial de órdenes
func (e *HighFrequencyExecutor) GetOrderHistory(limit int) []*OrderExecution {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if limit <= 0 || limit > len(e.orderHistory) {
		limit = len(e.orderHistory)
	}

	// Retornar las últimas 'limit' órdenes
	start := len(e.orderHistory) - limit
	result := make([]*OrderExecution, limit)
	copy(result, e.orderHistory[start:])

	return result
}

// SetMaxRetries establece el número máximo de reintentos
func (e *HighFrequencyExecutor) SetMaxRetries(retries int) {
	e.maxRetries = retries
}

// SetTimeout establece el timeout para ejecución
func (e *HighFrequencyExecutor) SetTimeout(timeout time.Duration) {
	e.timeout = timeout
}

// SetUseWebSocket habilita/deshabilita uso de WebSocket
func (e *HighFrequencyExecutor) SetUseWebSocket(use bool) {
	e.useWebSocket = use
}

// RateLimiter methods

// Allow verifica si se puede ejecutar una orden
func (rl *RateLimiter) Allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	// Reset contador por segundo
	if now.Sub(rl.lastSecond) >= time.Second {
		rl.secondCounter = 0
		rl.lastSecond = now
	}

	// Reset contador por minuto
	if now.Sub(rl.lastMinute) >= time.Minute {
		rl.minuteCounter = 0
		rl.lastMinute = now
	}

	// Verificar límites
	if rl.secondCounter >= rl.maxPerSecond {
		return false
	}

	if rl.minuteCounter >= rl.maxPerMinute {
		return false
	}

	// Incrementar contadores
	rl.secondCounter++
	rl.minuteCounter++

	return true
}

// SetLimits establece los límites de rate limiting
func (rl *RateLimiter) SetLimits(perSecond, perMinute int) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.maxPerSecond = perSecond
	rl.maxPerMinute = perMinute
}
