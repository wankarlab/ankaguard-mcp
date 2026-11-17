package gctclient

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/hft"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/config"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/rantipay/trading-mcp/internal/metrics"
	"github.com/sirupsen/logrus"
)

// Client wraps the GCT client with HFT optimizations and reconnection logic
type Client struct {
	gctClient     gct.GCTClient
	paperMu       sync.RWMutex
	paperClient   gct.GCTClient         // paper broker; nil until SetPaperClient
	hftSystem     *hft.HFTTradingSystem // HFT system for high-performance trading
	config        *config.GCTConfig
	log           *logrus.Logger
	connected     bool
	reconnecting  bool
	stopReconnect chan struct{}
}

// NewClient creates a new GCT client with HFT optimizations and reconnection support
func NewClient(cfg *config.GCTConfig, log *logrus.Logger) *Client {
	gctCfg := &gct.Config{
		Address:     cfg.GRPCAddress,
		Username:    cfg.Username,
		Password:    cfg.Password,
		Timeout:     cfg.Timeout,
		MaxRetries:  cfg.MaxRetries,
		UseTLS:      false, // GCT no tiene TLS habilitado
		TLSInsecure: false,
		Logger:      log, // Add logger for futures manager
	}

	return NewClientWithBase(cfg, log, gct.NewClient(gctCfg))
}

// NewClientWithBase builds the wrapper (HFT system, reconnection) around an
// already constructed base client. NewClient uses it with the real gRPC client;
// tests use it to inject a fake real client.
func NewClientWithBase(cfg *config.GCTConfig, log *logrus.Logger, baseClient gct.GCTClient) *Client {
	// Create HFT system with optimizations
	hftConfig := hft.DefaultHFTConfig()
	hftConfig.MetricsNamespace = "mcp"
	// Force circuit breaker on for the production gRPC client regardless of the
	// package default — this is the real order-submission path, not a test harness.
	hftConfig.EnableCircuitBreaker = true

	// Create adapter to make gct.GCTClient compatible with HFT system
	adapter := newGCTClientAdapter(baseClient)

	// QUANT-05: wire a real order-status check function into the HFT
	// reconciliation service so a network/timeout error on submission can
	// actually be verified against the exchange (previously always nil,
	// meaning any uncertain order was never reconciled and would have
	// panicked if it ever had been). baseClient's concrete type exposes
	// GetOrderFillInfo (see internal/gct.client.GetOrderFillInfo) via the
	// same narrow-interface pattern used for CancelFuturesOrder.
	var checkFunc hft.OrderCheckFunc
	if fg, ok := baseClient.(orderFillInfoGetter); ok {
		checkFunc = func(ctx context.Context, exchange, orderID string) (*hft.OrderStatus, error) {
			size, filled, err := fg.GetOrderFillInfo(ctx, exchange, orderID)
			if err != nil {
				return nil, err
			}
			state := "active"
			if size > 0 && filled >= size {
				state = "filled"
			}
			return &hft.OrderStatus{
				OrderID:    orderID,
				State:      state,
				FilledSize: filled,
			}, nil
		}
	}

	hftSystem, err := hft.NewHFTTradingSystem(adapter, log, hftConfig, checkFunc)
	if err != nil {
		log.WithError(err).Error("Failed to create HFT system, using standard client")
		hftSystem = nil
	}

	return &Client{
		gctClient:     baseClient,
		hftSystem:     hftSystem,
		config:        cfg,
		log:           log,
		stopReconnect: make(chan struct{}),
	}
}

// Connect establishes connection to GoCryptoTrader with retry logic
func (c *Client) Connect(ctx context.Context) error {
	c.log.WithFields(logrus.Fields{
		"address": c.config.GRPCAddress,
	}).Info("Connecting to GoCryptoTrader...")

	// Try initial connection with retries
	var lastErr error
	for attempt := 0; attempt < c.config.MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := c.calculateBackoff(attempt)
			c.log.WithFields(logrus.Fields{
				"attempt": attempt + 1,
				"backoff": backoff,
			}).Warn("Retrying connection to GoCryptoTrader...")

			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		err := c.gctClient.Connect(ctx, c.config.GRPCAddress)
		if err == nil {
			c.connected = true
			c.log.Info("Successfully connected to GoCryptoTrader")
			metrics.GetRecorder().SetGCTConnectionStatus("grpc", true)

			// Start HFT system if available
			if c.hftSystem != nil {
				if err := c.hftSystem.Start(); err != nil {
					c.log.WithError(err).Warn("Failed to start HFT system, continuing with standard client")
				} else {
					c.log.Info("HFT optimizations enabled")
				}
			}

			// Start health check goroutine
			go c.healthCheckLoop()

			return nil
		}

		lastErr = err
		c.log.WithError(err).WithField("attempt", attempt+1).Warn("Failed to connect to GoCryptoTrader")
	}

	return fmt.Errorf("failed to connect after %d attempts: %w", c.config.MaxRetries, lastErr)
}

// Disconnect closes the connection
func (c *Client) Disconnect() error {
	c.log.Info("Disconnecting from GoCryptoTrader...")

	// Stop HFT system if running
	if c.hftSystem != nil && c.hftSystem.IsRunning() {
		if err := c.hftSystem.Stop(); err != nil {
			c.log.WithError(err).Warn("Error stopping HFT system")
		}
	}

	// Stop reconnection attempts
	close(c.stopReconnect)

	c.connected = false
	return c.gctClient.Disconnect()
}

// IsConnected returns true if connected
func (c *Client) IsConnected() bool {
	return c.connected && c.gctClient.IsConnected()
}

// SetKuCoinCredentials configures KuCoin API credentials for futures trading
func (c *Client) SetKuCoinCredentials(apiKey, apiSecret, apiPassword string) {
	c.gctClient.SetKuCoinCredentials(apiKey, apiSecret, apiPassword)
}

// GetBaseClient returns the underlying REAL gct.GCTClient.
//
// It ignores the paper profile: it is meant for the internal bots and REST
// handlers, which always operate on the real account. Anything reachable from
// an MCP tool call must use ClientFor(ctx) instead, or a paper token would
// reach the exchange through it (see docs/product/order-paths.md).
// Callers that need the full gct.GCTClient interface (e.g. ScalpingEngine,
// AITradingBot) can use this to share the same gRPC connection instead of
// opening a second TCP dial to GoCryptoTrader.
func (c *Client) GetBaseClient() gct.GCTClient {
	return c.gctClient
}

// healthCheckLoop periodically checks connection health and reconnects if needed
func (c *Client) healthCheckLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if !c.IsConnected() {
				c.log.Warn("Connection to GoCryptoTrader lost, attempting to reconnect...")
				metrics.GetRecorder().SetGCTConnectionStatus("grpc", false)
				c.reconnect()
			} else {
				// Perform health check
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				err := c.gctClient.HealthCheck(ctx)
				cancel()

				if err != nil {
					c.log.WithError(err).Warn("Health check failed, attempting to reconnect...")
					metrics.GetRecorder().SetGCTConnectionStatus("grpc", false)
					c.reconnect()
				} else {
					metrics.GetRecorder().SetGCTConnectionStatus("grpc", true)
				}
			}
		case <-c.stopReconnect:
			c.log.Info("Stopping health check loop")
			metrics.GetRecorder().SetGCTConnectionStatus("grpc", false)
			return
		}
	}
}

// reconnect attempts to reconnect with exponential backoff
func (c *Client) reconnect() {
	if c.reconnecting {
		return
	}

	c.reconnecting = true
	defer func() { c.reconnecting = false }()

	c.connected = false
	metrics.GetRecorder().IncrementGCTReconnects()

	for attempt := 0; attempt < c.config.MaxRetries; attempt++ {
		backoff := c.calculateBackoff(attempt)

		c.log.WithFields(logrus.Fields{
			"attempt": attempt + 1,
			"backoff": backoff,
		}).Info("Attempting to reconnect to GoCryptoTrader...")

		select {
		case <-time.After(backoff):
		case <-c.stopReconnect:
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), c.config.Timeout)
		err := c.gctClient.Connect(ctx, c.config.GRPCAddress)
		cancel()

		if err == nil {
			c.connected = true
			c.log.Info("Successfully reconnected to GoCryptoTrader")
			metrics.GetRecorder().SetGCTConnectionStatus("grpc", true)
			return
		}

		c.log.WithError(err).WithField("attempt", attempt+1).Warn("Reconnection attempt failed")
	}

	c.log.Error("Failed to reconnect to GoCryptoTrader after all attempts")
}

// calculateBackoff calculates exponential backoff duration
func (c *Client) calculateBackoff(attempt int) time.Duration {
	// Exponential backoff: base * 2^attempt, capped at 60 seconds
	backoff := float64(c.config.RetryBackoff) * math.Pow(2, float64(attempt))
	maxBackoff := 60 * time.Second

	if time.Duration(backoff) > maxBackoff {
		return maxBackoff
	}

	return time.Duration(backoff)
}

// GetTicker retrieves ticker data
func (c *Client) GetTicker(ctx context.Context, exchange, pair string) (*models.Ticker, error) {
	start := time.Now()

	if !c.IsConnected() {
		metrics.RecordGCTCall("GetTicker", "error", time.Since(start))
		metrics.GetRecorder().RecordGCTError("GetTicker", "not_connected")
		return nil, fmt.Errorf("not connected to GoCryptoTrader")
	}

	// Use direct client for market data (not latency-critical)
	ticker, err := c.gctClient.GetTicker(ctx, exchange, pair)
	if err != nil {
		metrics.RecordGCTCall("GetTicker", "error", time.Since(start))
		metrics.GetRecorder().RecordGCTError("GetTicker", "grpc_error")
		return nil, err
	}

	metrics.RecordGCTCall("GetTicker", "success", time.Since(start))
	metrics.RecordTickerUpdate(exchange, pair)

	return &models.Ticker{
		Exchange:  ticker.Exchange,
		Pair:      ticker.Pair,
		Last:      ticker.Last,
		Bid:       ticker.Bid,
		Ask:       ticker.Ask,
		Volume:    ticker.Volume,
		High24h:   ticker.High,
		Low24h:    ticker.Low,
		Change24h: 0, // TODO: Calculate from previous close
		Timestamp: ticker.Timestamp,
	}, nil
}

// GetOrderbook retrieves orderbook data
func (c *Client) GetOrderbook(ctx context.Context, exchange, pair string, depth int) (*models.Orderbook, error) {
	start := time.Now()

	if !c.IsConnected() {
		metrics.RecordGCTCall("GetOrderbook", "error", time.Since(start))
		metrics.GetRecorder().RecordGCTError("GetOrderbook", "not_connected")
		return nil, fmt.Errorf("not connected to GoCryptoTrader")
	}

	// Use direct client for market data (not latency-critical)
	orderbook, err := c.gctClient.GetOrderbook(ctx, exchange, pair)
	if err != nil {
		metrics.RecordGCTCall("GetOrderbook", "error", time.Since(start))
		metrics.GetRecorder().RecordGCTError("GetOrderbook", "grpc_error")
		return nil, err
	}

	metrics.RecordGCTCall("GetOrderbook", "success", time.Since(start))
	metrics.RecordOrderbookUpdate(exchange, pair)

	// Convert to models.Orderbook
	result := &models.Orderbook{
		Exchange:  orderbook.Exchange,
		Pair:      orderbook.Pair,
		Bids:      make([]models.OrderbookItem, 0),
		Asks:      make([]models.OrderbookItem, 0),
		Timestamp: orderbook.Timestamp,
	}

	// Limit depth if specified
	maxBids := len(orderbook.Bids)
	maxAsks := len(orderbook.Asks)
	if depth > 0 {
		if depth < maxBids {
			maxBids = depth
		}
		if depth < maxAsks {
			maxAsks = depth
		}
	}

	for i := 0; i < maxBids; i++ {
		result.Bids = append(result.Bids, models.OrderbookItem{
			Price:  orderbook.Bids[i].Price,
			Amount: orderbook.Bids[i].Amount,
		})
	}

	for i := 0; i < maxAsks; i++ {
		result.Asks = append(result.Asks, models.OrderbookItem{
			Price:  orderbook.Asks[i].Price,
			Amount: orderbook.Asks[i].Amount,
		})
	}

	return result, nil
}

// GetCandles retrieves candle data
func (c *Client) GetCandles(ctx context.Context, exchange, pair, interval string, limit int) ([]models.Candle, error) {
	if !c.IsConnected() {
		return nil, fmt.Errorf("not connected to GoCryptoTrader")
	}

	// Use direct client for market data (not latency-critical)
	candles, err := c.gctClient.GetCandles(ctx, exchange, pair, interval, limit)
	if err != nil {
		return nil, err
	}

	result := make([]models.Candle, len(candles))
	for i, candle := range candles {
		result[i] = models.Candle{
			Timestamp: candle.Timestamp,
			Open:      candle.Open,
			High:      candle.High,
			Low:       candle.Low,
			Close:     candle.Close,
			Volume:    candle.Volume,
		}
	}

	return result, nil
}

// SubmitOrder submits a new order using HFT optimizations
func (c *Client) SubmitOrder(ctx context.Context, req *models.OrderRequest) (*models.OrderResponse, error) {
	start := time.Now()

	if !c.IsConnected() {
		metrics.RecordGCTCall("SubmitOrder", "error", time.Since(start))
		metrics.GetRecorder().RecordGCTError("SubmitOrder", "not_connected")
		return nil, fmt.Errorf("not connected to GoCryptoTrader")
	}

	gctReq := &gct.SubmitOrderRequest{
		Exchange:   req.Exchange,
		Pair:       req.Pair,
		Side:       req.Side,
		Type:       req.Type,
		Amount:     req.Amount,
		Price:      req.Price,
		ClientID:   req.ClientID,
		StopPrice:  req.StopPrice,
		Leverage:   req.Leverage,
		ReduceOnly: req.ReduceOnly,
		CloseOrder: req.CloseOrder,
	}

	// A paper profile is served by the paper broker and never touches the HFT
	// system (whose retries would otherwise forward the order to the real client).
	target, isPaper, routeErr := c.route(ctx)
	if routeErr != nil {
		return nil, routeErr
	}

	// Use HFT system for optimized order submission with retry logic and circuit breaker
	var order interface{}
	var err error

	if isPaper {
		order, err = target.SubmitOrder(ctx, gctReq)
	} else if c.hftSystem != nil && c.hftSystem.IsRunning() {
		order, err = c.hftSystem.SubmitOrder(ctx, gctReq)
	} else {
		// Fallback to direct client if HFT system not available
		order, err = target.SubmitOrder(ctx, gctReq)
	}

	if err != nil {
		metrics.RecordGCTCall("SubmitOrder", "error", time.Since(start))
		metrics.GetRecorder().RecordGCTError("SubmitOrder", "grpc_error")
		return nil, err
	}

	// Type assert the result
	gctOrder, ok := order.(*gct.Order)
	if !ok {
		metrics.RecordGCTCall("SubmitOrder", "error", time.Since(start))
		metrics.GetRecorder().RecordGCTError("SubmitOrder", "type_assertion")
		return nil, fmt.Errorf("unexpected order type returned")
	}

	metrics.RecordGCTCall("SubmitOrder", "success", time.Since(start))
	if !isPaper {
		metrics.RecordOrder(req.Exchange, req.Pair, req.Side, req.Type, "submitted", time.Since(start))
	}

	return &models.OrderResponse{
		OrderID:   gctOrder.ID,
		Exchange:  gctOrder.Exchange,
		Pair:      gctOrder.Pair,
		Side:      gctOrder.Side,
		Type:      gctOrder.Type,
		Amount:    gctOrder.Amount,
		Price:     gctOrder.Price,
		Status:    gctOrder.Status,
		Timestamp: gctOrder.CreatedAt,
	}, nil
}

// SubmitFuturesOrder submits a futures order using HFT optimizations
func (c *Client) SubmitFuturesOrder(ctx context.Context, req *gct.SubmitOrderRequest) (*gct.Order, error) {
	if !c.IsConnected() {
		return nil, fmt.Errorf("not connected to GoCryptoTrader")
	}

	target, isPaper, routeErr := c.route(ctx)
	if routeErr != nil {
		return nil, routeErr
	}

	// Use HFT system for optimized futures order submission (never for paper)
	var order interface{}
	var err error

	if isPaper {
		order, err = target.SubmitFuturesOrder(ctx, req)
	} else if c.hftSystem != nil && c.hftSystem.IsRunning() {
		order, err = c.hftSystem.SubmitFuturesOrder(ctx, req)
	} else {
		// Fallback to direct client if HFT system not available
		order, err = target.SubmitFuturesOrder(ctx, req)
	}

	if err != nil {
		return nil, err
	}

	// Type assert the result
	gctOrder, ok := order.(*gct.Order)
	if !ok {
		return nil, fmt.Errorf("unexpected order type returned")
	}

	return gctOrder, nil
}

// CancelOrder cancels an order
func (c *Client) CancelOrder(ctx context.Context, exchange, orderID string) error {
	if !c.IsConnected() {
		return fmt.Errorf("not connected to GoCryptoTrader")
	}

	target, _, err := c.route(ctx)
	if err != nil {
		return err
	}
	// Use direct client for cancellation (not as latency-critical as submission)
	return target.CancelOrder(ctx, exchange, orderID)
}

// futuresOrderCanceller is the narrow capability the concrete internal/gct
// client exposes for cancelling direct-REST futures orders. Not part of
// gct.GCTClient so test mocks stay small.
type futuresOrderCanceller interface {
	CancelFuturesOrder(ctx context.Context, exchange, orderID string) error
}

// CancelFuturesOrder cancels a futures order through the SAME direct KuCoin
// REST path that placed it. Futures orders submitted here bypass GCT's order
// manager, so the plain gRPC CancelOrder cannot find them and fails with
// "currency pair unset" (observed live 2026-07-09: two unfilled maker limits
// left resting because the fallback cancel could not reach them). Falls back
// to the gRPC cancel when the direct capability is unavailable.
func (c *Client) CancelFuturesOrder(ctx context.Context, exchange, orderID string) error {
	if !c.IsConnected() {
		return fmt.Errorf("not connected to GoCryptoTrader")
	}
	target, _, err := c.route(ctx)
	if err != nil {
		return err
	}
	if fc, ok := target.(futuresOrderCanceller); ok {
		return fc.CancelFuturesOrder(ctx, exchange, orderID)
	}
	return target.CancelOrder(ctx, exchange, orderID)
}

// GetOrderStatus retrieves order status
func (c *Client) GetOrderStatus(ctx context.Context, exchange, orderID string) (*models.OrderResponse, error) {
	if !c.IsConnected() {
		return nil, fmt.Errorf("not connected to GoCryptoTrader")
	}

	target, _, err := c.route(ctx)
	if err != nil {
		return nil, err
	}
	// Use direct client for status queries (not latency-critical)
	order, err := target.GetOrderInfo(ctx, exchange, orderID)
	if err != nil {
		return nil, err
	}

	return &models.OrderResponse{
		OrderID:   order.ID,
		Exchange:  order.Exchange,
		Pair:      order.Pair,
		Side:      order.Side,
		Type:      order.Type,
		Amount:    order.Amount,
		Price:     order.Price,
		Status:    order.Status,
		Timestamp: order.CreatedAt,
	}, nil
}

// orderFillInfoGetter is the narrow capability the concrete internal/gct
// client exposes for reading a real fill status directly from the exchange
// (see CancelFuturesOrder above for the same pattern). Not part of
// gct.GCTClient so test mocks stay small.
type orderFillInfoGetter interface {
	GetOrderFillInfo(ctx context.Context, exchange, orderID string) (size, filled float64, err error)
}

// GetOrderFillInfo returns the total requested size and the quantity already
// filled for orderID (TOOL-05: modify_order needs this to resubmit only the
// unfilled remainder after cancelling, instead of the original full amount,
// which would double a user's exposure on a partially-filled order).
func (c *Client) GetOrderFillInfo(ctx context.Context, exchange, orderID string) (size, filled float64, err error) {
	if !c.IsConnected() {
		return 0, 0, fmt.Errorf("not connected to GoCryptoTrader")
	}
	target, _, routeErr := c.route(ctx)
	if routeErr != nil {
		return 0, 0, routeErr
	}
	fg, ok := target.(orderFillInfoGetter)
	if !ok {
		return 0, 0, fmt.Errorf("GetOrderFillInfo: not supported by underlying client")
	}
	return fg.GetOrderFillInfo(ctx, exchange, orderID)
}

// GetPortfolio retrieves portfolio information
func (c *Client) GetPortfolio(ctx context.Context) (*models.Portfolio, error) {
	if !c.IsConnected() {
		return nil, fmt.Errorf("not connected to GoCryptoTrader")
	}

	target, _, routeErr := c.route(ctx)
	if routeErr != nil {
		return nil, routeErr
	}
	portfolio, err := target.GetPortfolio(ctx)
	if err != nil {
		return nil, err
	}

	result := &models.Portfolio{
		TotalValueUSD: portfolio.TotalValue,
		Holdings:      make(map[string]*models.Holding),
		PnL:           portfolio.PnL,
		PnLPercent:    portfolio.PnLPercentage,
		Timestamp:     portfolio.UpdatedAt,
	}

	for currency, holding := range portfolio.Holdings {
		result.Holdings[currency] = &models.Holding{
			Currency: holding.Currency,
			Amount:   holding.Total,
			ValueUSD: holding.Value,
			AvgPrice: 0, // TODO: Calculate from trades
		}
	}

	return result, nil
}

// GetPortfolioForExchange retrieves portfolio information for a specific exchange
func (c *Client) GetPortfolioForExchange(ctx context.Context, exchange string) (*models.Portfolio, error) {
	// For now, return the general portfolio as GCT doesn't separate by exchange
	// In the future, this could be enhanced to filter by exchange
	return c.GetPortfolio(ctx)
}

// GetBalance retrieves balance for a currency
func (c *Client) GetBalance(ctx context.Context, exchange, currency string) (*models.Balance, error) {
	start := time.Now()

	if !c.IsConnected() {
		metrics.RecordGCTCall("GetBalance", "error", time.Since(start))
		metrics.GetRecorder().RecordGCTError("GetBalance", "not_connected")
		return nil, fmt.Errorf("not connected to GoCryptoTrader")
	}

	target, _, routeErr := c.route(ctx)
	if routeErr != nil {
		return nil, routeErr
	}
	balanceData, err := target.GetBalance(ctx, exchange, currency)
	if err != nil {
		metrics.RecordGCTCall("GetBalance", "error", time.Since(start))
		metrics.GetRecorder().RecordGCTError("GetBalance", "grpc_error")
		return nil, err
	}

	metrics.RecordGCTCall("GetBalance", "success", time.Since(start))

	available, _ := balanceData["available"].(float64)
	locked, _ := balanceData["in_order"].(float64)
	total, _ := balanceData["total"].(float64)

	return &models.Balance{
		Exchange:  exchange,
		Currency:  currency,
		Available: available,
		Locked:    locked,
		Total:     total,
	}, nil
}

// CalculateRSI calculates RSI indicator
func (c *Client) CalculateRSI(ctx context.Context, exchange, pair string, period int) (float64, error) {
	if !c.IsConnected() {
		return 0, fmt.Errorf("not connected to GoCryptoTrader")
	}

	return c.gctClient.CalculateRSI(ctx, exchange, pair, period)
}

// CalculateMACD calculates MACD indicator
func (c *Client) CalculateMACD(ctx context.Context, exchange, pair string, fastPeriod, slowPeriod, signalPeriod int) (*models.MACD, error) {
	if !c.IsConnected() {
		return nil, fmt.Errorf("not connected to GoCryptoTrader")
	}

	macdData, err := c.gctClient.CalculateMACD(ctx, exchange, pair, fastPeriod, slowPeriod, signalPeriod)
	if err != nil {
		return nil, err
	}

	return &models.MACD{
		MACD:      macdData["macd"],
		Signal:    macdData["signal"],
		Histogram: macdData["histogram"],
	}, nil
}

// CalculateBollingerBands calculates Bollinger Bands
func (c *Client) CalculateBollingerBands(ctx context.Context, exchange, pair string, period int, stdDev float64) (*models.BollingerBands, error) {
	if !c.IsConnected() {
		return nil, fmt.Errorf("not connected to GoCryptoTrader")
	}

	bbData, err := c.gctClient.CalculateBollingerBands(ctx, exchange, pair, period, stdDev)
	if err != nil {
		return nil, err
	}

	return &models.BollingerBands{
		Upper:  bbData["upper"],
		Middle: bbData["middle"],
		Lower:  bbData["lower"],
	}, nil
}

// GetHFTMetrics returns HFT performance metrics
func (c *Client) GetHFTMetrics() hft.MetricsSnapshot {
	if c.hftSystem != nil {
		return c.hftSystem.GetMetrics()
	}
	return hft.MetricsSnapshot{}
}

// GetCircuitBreakerState returns the current circuit breaker state
func (c *Client) GetCircuitBreakerState() string {
	if c.hftSystem != nil {
		return c.hftSystem.GetCircuitBreakerState()
	}
	return "disabled"
}

// ResetCircuitBreaker manually resets the circuit breaker
func (c *Client) ResetCircuitBreaker() {
	if c.hftSystem != nil {
		c.hftSystem.ResetCircuitBreaker()
	}
}

// StartHFT starts the HFT system
func (c *Client) StartHFT() error {
	if c.hftSystem == nil {
		return fmt.Errorf("HFT system not initialized")
	}
	return c.hftSystem.Start()
}

// StopHFT stops the HFT system
func (c *Client) StopHFT() error {
	if c.hftSystem == nil {
		return fmt.Errorf("HFT system not initialized")
	}
	return c.hftSystem.Stop()
}

// SetExchangeCredentials sets exchange API credentials for the client
func (c *Client) SetExchangeCredentials(exchange, apiKey, apiSecret, passphrase string) {
	if c.log == nil {
		c.log = logrus.New()
	}
	c.log.WithFields(logrus.Fields{
		"exchange":       exchange,
		"api_key_length": len(apiKey),
		"has_secret":     apiSecret != "",
		"has_passphrase": passphrase != "",
	}).Info("Setting exchange credentials")

	// Set credentials in the underlying GCT client
	if c.gctClient != nil {
		if exchange == "kucoin" {
			c.gctClient.SetKuCoinCredentials(apiKey, apiSecret, passphrase)
			c.log.Info("✅ KuCoin credentials set successfully")
		}
	}
}

// ConvertSymbol converts a symbol between standard and exchange-specific formats
// This method exposes the GCT client's symbol converter to MCP tools
func (c *Client) ConvertSymbol(symbol, marketType, exchange string) (string, error) {
	if c.gctClient == nil {
		return "", fmt.Errorf("GCT client not initialized")
	}

	// Convert string marketType to gct.MarketType
	var mt gct.MarketType
	switch marketType {
	case "spot":
		mt = gct.MarketTypeSpot
	case "futures":
		mt = gct.MarketTypeFutures
	case "margin":
		mt = gct.MarketTypeMargin
	case "options":
		mt = gct.MarketTypeOptions
	default:
		return "", fmt.Errorf("invalid market type: %s", marketType)
	}

	return c.gctClient.ConvertSymbol(symbol, mt, exchange)
}

// GetFuturesPositions retrieves all open futures positions
func (c *Client) GetFuturesPositions(ctx context.Context, exchange string) ([]*gct.Position, error) {
	if !c.IsConnected() {
		return nil, fmt.Errorf("not connected to GoCryptoTrader")
	}

	target, _, err := c.route(ctx)
	if err != nil {
		return nil, err
	}
	return target.GetFuturesPositions(ctx, exchange)
}

// GetOpenOrders retrieves all open orders for an exchange, optionally filtered by pair
func (c *Client) GetOpenOrders(ctx context.Context, exchange, pair string) ([]map[string]interface{}, error) {
	start := time.Now()

	if !c.IsConnected() {
		metrics.RecordGCTCall("GetOpenOrders", "error", time.Since(start))
		metrics.GetRecorder().RecordGCTError("GetOpenOrders", "not_connected")
		return nil, fmt.Errorf("not connected to GoCryptoTrader")
	}

	target, _, routeErr := c.route(ctx)
	if routeErr != nil {
		return nil, routeErr
	}
	orders, err := target.GetOrders(ctx, exchange, pair, "", "spot", nil, nil)
	if err != nil {
		metrics.RecordGCTCall("GetOpenOrders", "error", time.Since(start))
		metrics.GetRecorder().RecordGCTError("GetOpenOrders", "grpc_error")
		return nil, fmt.Errorf("failed to get open orders: %w", err)
	}

	metrics.RecordGCTCall("GetOpenOrders", "success", time.Since(start))

	result := make([]map[string]interface{}, 0, len(orders))
	for _, order := range orders {
		// Only include open/active orders
		if order.Status != "filled" && order.Status != "cancelled" && order.Status != "rejected" {
			result = append(result, map[string]interface{}{
				"order_id":   order.ID,
				"exchange":   order.Exchange,
				"pair":       order.Pair,
				"side":       order.Side,
				"type":       order.Type,
				"price":      order.Price,
				"amount":     order.Amount,
				"remaining":  order.Remaining,
				"status":     order.Status,
				"created_at": order.CreatedAt,
			})
		}
	}

	return result, nil
}

// GetOrderHistory retrieves order history for an exchange, optionally filtered by pair
// Deprecated: Use GetOrderHistoryWithTimeFilter for explicit time range control
func (c *Client) GetOrderHistory(ctx context.Context, exchange, pair string, limit int) ([]map[string]interface{}, error) {
	// Default to last 30 days for backward compatibility
	endTime := time.Now()
	startTime := endTime.AddDate(0, 0, -30)
	return c.GetOrderHistoryWithTimeFilter(ctx, exchange, pair, limit, startTime, endTime)
}

// GetOrderHistoryWithTimeFilter retrieves order history for an exchange with explicit time range filtering
func (c *Client) GetOrderHistoryWithTimeFilter(ctx context.Context, exchange, pair string, limit int, startTime, endTime time.Time) ([]map[string]interface{}, error) {
	if !c.IsConnected() {
		return nil, fmt.Errorf("not connected to GoCryptoTrader")
	}

	target, _, routeErr := c.route(ctx)
	if routeErr != nil {
		return nil, routeErr
	}
	orders, err := target.GetOrders(ctx, exchange, pair, "", "spot", &startTime, &endTime)
	if err != nil {
		return nil, fmt.Errorf("failed to get order history: %w", err)
	}

	result := make([]map[string]interface{}, 0, len(orders))
	for i, order := range orders {
		if limit > 0 && i >= limit {
			break
		}
		result = append(result, map[string]interface{}{
			"order_id":   order.ID,
			"exchange":   order.Exchange,
			"pair":       order.Pair,
			"side":       order.Side,
			"type":       order.Type,
			"price":      order.Price,
			"amount":     order.Amount,
			"filled":     order.Amount - order.Remaining,
			"remaining":  order.Remaining,
			"status":     order.Status,
			"created_at": order.CreatedAt,
			"updated_at": order.UpdatedAt,
		})
	}

	return result, nil
}

// GetTradeHistory retrieves trade history for an exchange, optionally filtered by pair
// Deprecated: Use GetTradeHistoryWithTimeFilter for explicit time range control
func (c *Client) GetTradeHistory(ctx context.Context, exchange, pair string, limit int) ([]map[string]interface{}, error) {
	// Default to last 30 days for backward compatibility
	endTime := time.Now()
	startTime := endTime.AddDate(0, 0, -30)
	return c.GetTradeHistoryWithTimeFilter(ctx, exchange, pair, limit, startTime, endTime)
}

// GetTradeHistoryWithTimeFilter retrieves trade history for an exchange with explicit time range filtering
// Returns executed trades with id, pair, side, price, amount, fee, timestamp, and calculated pnl
func (c *Client) GetTradeHistoryWithTimeFilter(ctx context.Context, exchange, pair string, limit int, startTime, endTime time.Time) ([]map[string]interface{}, error) {
	if !c.IsConnected() {
		return nil, fmt.Errorf("not connected to GoCryptoTrader")
	}

	// Use the underlying GCT client to get trades
	trades, err := c.gctClient.GetTrades(ctx, exchange, pair, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get trade history: %w", err)
	}

	// Filter trades by time range and enrich with additional data
	filteredTrades := make([]map[string]interface{}, 0, len(trades))
	for _, trade := range trades {
		// Extract timestamp from trade
		var tradeTime time.Time
		if ts, ok := trade["timestamp"].(time.Time); ok {
			tradeTime = ts
		} else if ts, ok := trade["timestamp"].(string); ok {
			if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
				tradeTime = parsed
			}
		} else if ts, ok := trade["timestamp"].(int64); ok {
			tradeTime = time.Unix(ts, 0)
		} else if ts, ok := trade["timestamp"].(float64); ok {
			tradeTime = time.Unix(int64(ts), 0)
		}

		// Filter by time range if timestamp is available
		if !tradeTime.IsZero() {
			if tradeTime.Before(startTime) || tradeTime.After(endTime) {
				continue
			}
		}

		// Calculate value and add to result
		price, _ := trade["price"].(float64)
		amount, _ := trade["amount"].(float64)
		value := price * amount

		// Add calculated fields
		enrichedTrade := make(map[string]interface{})
		for k, v := range trade {
			enrichedTrade[k] = v
		}
		enrichedTrade["value"] = value

		// Add pair if not present
		if _, ok := enrichedTrade["pair"]; !ok {
			if pair != "" {
				enrichedTrade["pair"] = pair
			}
		}

		filteredTrades = append(filteredTrades, enrichedTrade)

		// Apply limit
		if limit > 0 && len(filteredTrades) >= limit {
			break
		}
	}

	return filteredTrades, nil
}

// GetRecentTrades retrieves recent market trades for a trading pair
func (c *Client) GetRecentTrades(ctx context.Context, exchange, pair string, limit int) ([]map[string]interface{}, error) {
	if !c.IsConnected() {
		return nil, fmt.Errorf("not connected to GoCryptoTrader")
	}

	trades, err := c.gctClient.GetTrades(ctx, exchange, pair, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get recent trades: %w", err)
	}

	return trades, nil
}

// GetExchangeInfo retrieves exchange information including supported assets and pairs
func (c *Client) GetExchangeInfo(ctx context.Context, exchange string) (map[string]interface{}, error) {
	if !c.IsConnected() {
		return nil, fmt.Errorf("not connected to GoCryptoTrader")
	}

	info, err := c.gctClient.GetExchangeInfo(ctx, exchange)
	if err != nil {
		return nil, fmt.Errorf("failed to get exchange info: %w", err)
	}

	return info, nil
}

// SetLeverage sets the leverage for a futures trading pair
func (c *Client) SetLeverage(ctx context.Context, exchange, symbol string, leverage int) error {
	if !c.IsConnected() {
		return fmt.Errorf("not connected to GoCryptoTrader")
	}

	target, _, err := c.route(ctx)
	if err != nil {
		return err
	}
	return target.SetLeverage(ctx, exchange, symbol, leverage)
}

// CloseFuturesPosition closes a specific futures position
func (c *Client) CloseFuturesPosition(ctx context.Context, exchange, symbol string) error {
	if !c.IsConnected() {
		return fmt.Errorf("not connected to GoCryptoTrader")
	}

	target, _, err := c.route(ctx)
	if err != nil {
		return err
	}
	return target.CloseFuturesPosition(ctx, exchange, symbol)
}

// GetAccountInfo retrieves account information for an exchange
func (c *Client) GetAccountInfo(ctx context.Context, exchange string) (*gct.AccountInfo, error) {
	if !c.IsConnected() {
		return nil, fmt.Errorf("not connected to GoCryptoTrader")
	}

	target, _, err := c.route(ctx)
	if err != nil {
		return nil, err
	}
	return target.GetAccountInfo(ctx, exchange)
}

// GetTradeHistoryForExchange retrieves trade history for a specific exchange with period in days
func (c *Client) GetTradeHistoryForExchange(ctx context.Context, exchange string, periodDays int) ([]models.Trade, error) {
	endTime := time.Now()
	startTime := endTime.AddDate(0, 0, -periodDays)

	trades, err := c.GetTradeHistoryWithTimeFilter(ctx, exchange, "", 0, startTime, endTime)
	if err != nil {
		return nil, err
	}

	result := make([]models.Trade, 0, len(trades))
	for _, trade := range trades {
		// Convert map to Trade struct
		tradeObj := models.Trade{
			Exchange: exchange,
		}

		if id, ok := trade["id"].(string); ok {
			tradeObj.ID = id
		}
		if pair, ok := trade["pair"].(string); ok {
			tradeObj.Pair = pair
		}
		if side, ok := trade["side"].(string); ok {
			tradeObj.Side = side
		}
		if amount, ok := trade["amount"].(float64); ok {
			tradeObj.Amount = amount
		}
		if price, ok := trade["price"].(float64); ok {
			tradeObj.EntryPrice = price
		}
		if pnl, ok := trade["pnl"].(float64); ok {
			tradeObj.PnL = pnl
		}
		if status, ok := trade["status"].(string); ok {
			tradeObj.Status = status
		}
		if timestamp, ok := trade["timestamp"].(time.Time); ok {
			tradeObj.OpenedAt = timestamp
		}

		result = append(result, tradeObj)
	}

	return result, nil
}

// ConfigureNotifications configures push notification settings (stub implementation)
func (c *Client) ConfigureNotifications(ctx context.Context, config *models.NotificationConfig) error {
	c.log.WithFields(logrus.Fields{
		"enabled":         config.Enabled,
		"fcm_configured":  config.FCMServerKey != "",
		"apns_configured": config.APNSKeyID != "",
	}).Info("Notification configuration received")

	// TODO: Implement actual notification configuration
	// For now, just log the configuration
	return nil
}

// SetNotificationPreferences sets user notification preferences (stub implementation)
func (c *Client) SetNotificationPreferences(ctx context.Context, preferences *models.NotificationPreferences) error {
	c.log.WithFields(logrus.Fields{
		"user_id":          preferences.UserID,
		"device_tokens":    len(preferences.DeviceTokens),
		"enabled_channels": len(preferences.EnabledChannels),
	}).Info("Notification preferences set")

	// TODO: Implement actual preference storage
	return nil
}

// GetNotificationPreferences gets user notification preferences (stub implementation)
func (c *Client) GetNotificationPreferences(ctx context.Context, userID string) (*models.NotificationPreferences, error) {
	// TODO: Implement actual preference retrieval
	// For now, return default preferences
	return &models.NotificationPreferences{
		UserID:          userID,
		DeviceTokens:    []string{},
		EnabledChannels: []string{"push"},
		TradingAlerts:   true,
		PriceAlerts:     true,
		RiskAlerts:      true,
		SystemAlerts:    true,
	}, nil
}

// SendTestNotification sends a test notification (stub implementation)
func (c *Client) SendTestNotification(ctx context.Context, deviceTokens []string, title, body, channel string) (interface{}, error) {
	c.log.WithFields(logrus.Fields{
		"device_tokens": len(deviceTokens),
		"title":         title,
		"body":          body,
		"channel":       channel,
	}).Info("Test notification sent")

	// TODO: Implement actual notification sending
	return map[string]interface{}{
		"sent":       true,
		"recipients": len(deviceTokens),
		"channel":    channel,
	}, nil
}

// CreateFundingRateAlert creates a funding rate alert (stub implementation)
func (c *Client) CreateFundingRateAlert(ctx context.Context, alert *models.FundingRateAlert) (*models.FundingRateAlert, error) {
	// Generate a simple ID
	alert.ID = fmt.Sprintf("alert_%d", time.Now().Unix())
	alert.CreatedAt = time.Now()

	c.log.WithFields(logrus.Fields{
		"alert_id":  alert.ID,
		"exchange":  alert.Exchange,
		"pair":      alert.Pair,
		"threshold": alert.Threshold,
		"direction": alert.Direction,
	}).Info("Funding rate alert created")

	// TODO: Implement actual alert storage and monitoring
	return alert, nil
}

// WebSocket subscription methods (stub implementations)
func (c *Client) SubscribeWebSocket(ctx context.Context, subscription *models.WebSocketSubscription) (interface{}, error) {
	c.log.WithFields(logrus.Fields{
		"channel":  subscription.Channel,
		"symbol":   subscription.Symbol,
		"exchange": subscription.Exchange,
	}).Info("WebSocket subscription created")

	// TODO: Implement actual WebSocket subscription
	return map[string]interface{}{
		"subscribed": true,
		"channel":    subscription.Channel,
		"symbol":     subscription.Symbol,
	}, nil
}

func (c *Client) UnsubscribeWebSocket(ctx context.Context, subscription *models.WebSocketSubscription) (interface{}, error) {
	c.log.WithFields(logrus.Fields{
		"channel":  subscription.Channel,
		"symbol":   subscription.Symbol,
		"exchange": subscription.Exchange,
	}).Info("WebSocket subscription removed")

	// TODO: Implement actual WebSocket unsubscription
	return map[string]interface{}{
		"unsubscribed": true,
		"channel":      subscription.Channel,
		"symbol":       subscription.Symbol,
	}, nil
}

func (c *Client) GetActiveSubscriptions(ctx context.Context, exchange, channel string) ([]models.WebSocketSubscription, error) {
	// TODO: Implement actual subscription retrieval
	// For now, return empty list
	return []models.WebSocketSubscription{}, nil
}

func (c *Client) GetUserSubscriptions(ctx context.Context, userID string) (*models.MarketDataSubscription, error) {
	// TODO: Implement actual user subscription retrieval
	return &models.MarketDataSubscription{
		UserID:   userID,
		Symbols:  []string{},
		Channels: []string{},
		Active:   false,
	}, nil
}

func (c *Client) SetUserSubscriptions(ctx context.Context, subscription *models.MarketDataSubscription) (interface{}, error) {
	c.log.WithFields(logrus.Fields{
		"user_id":  subscription.UserID,
		"symbols":  len(subscription.Symbols),
		"channels": len(subscription.Channels),
	}).Info("User subscriptions set")

	// TODO: Implement actual subscription storage
	return subscription, nil
}

func (c *Client) AddUserSubscriptions(ctx context.Context, subscription *models.MarketDataSubscription) (interface{}, error) {
	c.log.WithFields(logrus.Fields{
		"user_id":  subscription.UserID,
		"symbols":  len(subscription.Symbols),
		"channels": len(subscription.Channels),
	}).Info("User subscriptions added")

	// TODO: Implement actual subscription addition
	return subscription, nil
}

func (c *Client) RemoveUserSubscriptions(ctx context.Context, subscription *models.MarketDataSubscription) (interface{}, error) {
	c.log.WithFields(logrus.Fields{
		"user_id":  subscription.UserID,
		"symbols":  len(subscription.Symbols),
		"channels": len(subscription.Channels),
	}).Info("User subscriptions removed")

	// TODO: Implement actual subscription removal
	return subscription, nil
}

func (c *Client) GetWebSocketStatus(ctx context.Context, exchange string) (interface{}, error) {
	// TODO: Implement actual WebSocket status retrieval
	return map[string]interface{}{
		"connected":     c.IsConnected(),
		"exchange":      exchange,
		"subscriptions": 0,
		"uptime":        "0s",
	}, nil
}

// GetPortfolioOverview returns a comprehensive portfolio snapshot: spot wallet, futures
// wallet, open positions with risk metrics, account health, and bot decision context.
func (c *Client) GetPortfolioOverview(ctx context.Context, exchange string) (*gct.PortfolioOverview, error) {
	target, _, routeErr := c.route(ctx)
	if routeErr != nil {
		return nil, routeErr
	}
	overview, err := target.GetPortfolioOverview(ctx, exchange)
	if err != nil {
		return nil, err
	}
	return overview, nil
}

// ---------------------------------------------------------------------------
// Per-user session variants (MU-0 isolation layer)
//
// Each method below routes account-touching operations through an isolated
// ExchangeSession so that concurrent users sign requests with their own keys.
// Market-data methods (GetTicker, GetCandles, GetTradablePairs, allTickers)
// remain global — they do not require authentication.
// ---------------------------------------------------------------------------

// SubmitOrderForUser submits an order using the credentials embedded in the
// provided ExchangeSession, bypassing the shared global credential state.
//
// CORE-04: this used to unconditionally call
// session.FuturesManager().PlaceFuturesOrder(...) regardless of the actual
// requested market, so a plain spot buy (leverage=0) was silently placed as
// a futures order — exposing the user to margin/liquidation they never
// requested. It now branches on the same signal the platform's global order
// path already uses (internal/gct/client.go SubmitOrder: leverage>0 means
// futures): leverage<=0 routes through the isolated session's KuCoin SPOT
// REST client (session.KuCoinClient().SubmitSpotOrder), leverage>0 keeps
// using FuturesManager as before.
func (c *Client) SubmitOrderForUser(ctx context.Context, session *gct.ExchangeSession, req *models.OrderRequest) (*models.OrderResponse, error) {
	if err := paperUnsupportedForUser(ctx); err != nil {
		return nil, err
	}
	if session == nil {
		return nil, fmt.Errorf("exchange session is required")
	}
	if req == nil {
		return nil, fmt.Errorf("order request is required")
	}

	isFutures := req.Leverage > 0

	if !isFutures {
		// Spot path: real submission via the session's own KuCoin credentials.
		if req.StopPrice > 0 {
			return nil, fmt.Errorf("SubmitOrderForUser: stop orders are not supported on the spot path yet (stop_price=%.8f); submit a futures order (leverage>0) or a plain market/limit spot order", req.StopPrice)
		}

		spotReq := &gct.SpotOrderRequest{
			Symbol:    req.Pair,
			Side:      req.Side,
			Type:      req.Type,
			Size:      req.Amount,
			Price:     req.Price,
			ClientOID: req.ClientID,
		}

		orderID, err := session.KuCoinClient().SubmitSpotOrder(ctx, spotReq)
		if err != nil {
			return nil, fmt.Errorf("SubmitOrderForUser: spot order failed: %w", err)
		}

		session.Touch()
		return &models.OrderResponse{
			OrderID:   orderID,
			Exchange:  req.Exchange,
			Pair:      req.Pair,
			Side:      req.Side,
			Type:      req.Type,
			Amount:    req.Amount,
			Price:     req.Price,
			Status:    "submitted",
			Timestamp: time.Now(),
		}, nil
	}

	// Futures path: unchanged behaviour (leverage>0 explicitly requested).
	gctReq := &gct.SubmitOrderRequest{
		Exchange:   req.Exchange,
		Pair:       req.Pair,
		Side:       req.Side,
		Type:       req.Type,
		Amount:     req.Amount,
		Price:      req.Price,
		ClientID:   req.ClientID,
		StopPrice:  req.StopPrice,
		Leverage:   req.Leverage,
		ReduceOnly: req.ReduceOnly,
		CloseOrder: req.CloseOrder,
	}

	order, err := session.FuturesManager().PlaceFuturesOrder(ctx, gctReq)
	if err != nil {
		return nil, fmt.Errorf("SubmitOrderForUser: %w", err)
	}

	session.Touch()
	return &models.OrderResponse{
		OrderID:   order.ID,
		Exchange:  order.Exchange,
		Pair:      order.Pair,
		Side:      order.Side,
		Type:      order.Type,
		Amount:    order.Amount,
		Price:     order.Price,
		Status:    order.Status,
		Timestamp: order.CreatedAt,
	}, nil
}

// SubmitFuturesOrderForUser submits a futures order using the isolated session.
func (c *Client) SubmitFuturesOrderForUser(ctx context.Context, session *gct.ExchangeSession, req *gct.SubmitOrderRequest) (*gct.Order, error) {
	if err := paperUnsupportedForUser(ctx); err != nil {
		return nil, err
	}
	if session == nil {
		return nil, fmt.Errorf("exchange session is required")
	}
	if req == nil {
		return nil, fmt.Errorf("order request is required")
	}

	order, err := session.FuturesManager().PlaceFuturesOrder(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("SubmitFuturesOrderForUser: %w", err)
	}

	session.Touch()
	return order, nil
}

// GetFuturesPositionsForUser retrieves open futures positions for the session user.
func (c *Client) GetFuturesPositionsForUser(ctx context.Context, session *gct.ExchangeSession) ([]*gct.Position, error) {
	if err := paperUnsupportedForUser(ctx); err != nil {
		return nil, err
	}
	if session == nil {
		return nil, fmt.Errorf("exchange session is required")
	}

	positions, err := session.FuturesManager().GetPositions(ctx)
	if err != nil {
		return nil, fmt.Errorf("GetFuturesPositionsForUser: %w", err)
	}

	session.Touch()
	return positions, nil
}

// GetBalanceForUser retrieves the balance for a currency using the session's credentials.
func (c *Client) GetBalanceForUser(ctx context.Context, session *gct.ExchangeSession, currency string) (*models.Balance, error) {
	if err := paperUnsupportedForUser(ctx); err != nil {
		return nil, err
	}
	if session == nil {
		return nil, fmt.Errorf("exchange session is required")
	}

	kc := session.KuCoinClient()
	balanceData, err := kc.GetFuturesBalance(ctx, currency)
	if err != nil {
		// Fall back to spot balance if futures balance fails.
		spotData, spotErr := kc.GetSpotBalance(ctx, currency)
		if spotErr != nil {
			return nil, fmt.Errorf("GetBalanceForUser: futures: %v; spot: %w", err, spotErr)
		}
		balanceData = spotData
	}

	available, _ := balanceData["available"].(float64)
	inOrder, _ := balanceData["in_order"].(float64)
	total, _ := balanceData["total"].(float64)

	session.Touch()
	return &models.Balance{
		Exchange:  session.Exchange(),
		Currency:  currency,
		Available: available,
		Locked:    inOrder,
		Total:     total,
	}, nil
}

// SetMarginModeForUser sets the margin mode for a futures symbol using the session.
func (c *Client) SetMarginModeForUser(ctx context.Context, session *gct.ExchangeSession, symbol, mode string) error {
	if err := paperUnsupportedForUser(ctx); err != nil {
		return err
	}
	if session == nil {
		return fmt.Errorf("exchange session is required")
	}

	err := session.KuCoinClient().SetMarginMode(ctx, symbol, mode)
	if err != nil {
		return fmt.Errorf("SetMarginModeForUser: %w", err)
	}

	session.Touch()
	return nil
}

// SetLeverageForUser sets leverage for a futures symbol using the session.
func (c *Client) SetLeverageForUser(ctx context.Context, session *gct.ExchangeSession, symbol string, leverage int) error {
	if err := paperUnsupportedForUser(ctx); err != nil {
		return err
	}
	if session == nil {
		return fmt.Errorf("exchange session is required")
	}

	err := session.KuCoinClient().SetLeverage(ctx, symbol, leverage)
	if err != nil {
		return fmt.Errorf("SetLeverageForUser: %w", err)
	}

	session.Touch()
	return nil
}

// CancelOrderForUser cancels a spot order using the session's credentials.
// KuCoin spot cancel is routed via the REST client's authenticated endpoint.
func (c *Client) CancelOrderForUser(ctx context.Context, session *gct.ExchangeSession, exchange, orderID string) error {
	if err := paperUnsupportedForUser(ctx); err != nil {
		return err
	}
	if session == nil {
		return fmt.Errorf("exchange session is required")
	}
	if orderID == "" {
		return fmt.Errorf("orderID is required")
	}

	// Delegate to the global GCT client but note: this still uses the gRPC path.
	// The session isolates REST-signed operations; gRPC cancel does not carry
	// per-user keys, so fall back to the shared gRPC channel here.
	// TODO(MU-1): wire a per-user gRPC credential interceptor.
	if !c.IsConnected() {
		return fmt.Errorf("not connected to GoCryptoTrader")
	}
	err := c.gctClient.CancelOrder(ctx, exchange, orderID)
	if err != nil {
		return fmt.Errorf("CancelOrderForUser: %w", err)
	}

	session.Touch()
	return nil
}

// CancelFuturesOrderForUser cancels a futures order using the session.
// Mirrors CancelOrderForUser; kept as a separate method for futures context.
func (c *Client) CancelFuturesOrderForUser(ctx context.Context, session *gct.ExchangeSession, exchange, orderID string) error {
	if err := paperUnsupportedForUser(ctx); err != nil {
		return err
	}
	return c.CancelOrderForUser(ctx, session, exchange, orderID)
}

// CloseFuturesPositionForUser closes a futures position using the session.
func (c *Client) CloseFuturesPositionForUser(ctx context.Context, session *gct.ExchangeSession, symbol string) error {
	if err := paperUnsupportedForUser(ctx); err != nil {
		return err
	}
	if session == nil {
		return fmt.Errorf("exchange session is required")
	}

	err := session.FuturesManager().ClosePosition(ctx, symbol)
	if err != nil {
		return fmt.Errorf("CloseFuturesPositionForUser: %w", err)
	}

	session.Touch()
	return nil
}

// SubmitStopLossForUser places a stop-loss futures order using the session.
func (c *Client) SubmitStopLossForUser(ctx context.Context, session *gct.ExchangeSession, req *gct.SubmitOrderRequest) (*gct.Order, error) {
	if err := paperUnsupportedForUser(ctx); err != nil {
		return nil, err
	}
	if session == nil {
		return nil, fmt.Errorf("exchange session is required")
	}
	if req == nil {
		return nil, fmt.Errorf("order request is required")
	}

	req.ReduceOnly = true
	order, err := session.FuturesManager().PlaceFuturesOrder(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("SubmitStopLossForUser: %w", err)
	}

	session.Touch()
	return order, nil
}

// SubmitTakeProfitForUser places a take-profit futures order using the session.
func (c *Client) SubmitTakeProfitForUser(ctx context.Context, session *gct.ExchangeSession, req *gct.SubmitOrderRequest) (*gct.Order, error) {
	if err := paperUnsupportedForUser(ctx); err != nil {
		return nil, err
	}
	if session == nil {
		return nil, fmt.Errorf("exchange session is required")
	}
	if req == nil {
		return nil, fmt.Errorf("order request is required")
	}

	req.ReduceOnly = true
	order, err := session.FuturesManager().PlaceFuturesOrder(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("SubmitTakeProfitForUser: %w", err)
	}

	session.Touch()
	return order, nil
}
