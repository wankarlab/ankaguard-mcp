package gct

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/sirupsen/logrus"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"

	dynamic "github.com/rantipay/trading-mcp/internal/config/dynamic"
	"github.com/rantipay/trading-mcp/internal/edition"
	ta "github.com/rantipay/trading-mcp/internal/indicators"
	"github.com/rantipay/trading-mcp/internal/risk"
)

// withRetry executes fn with exponential backoff for transient errors.
// Only use for idempotent (GET) operations to avoid duplicate mutations.
func withRetry(ctx context.Context, maxAttempts int, fn func() error) error {
	var lastErr error
	backoff := 100 * time.Millisecond
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := fn(); err != nil {
			if isTransient(err) {
				lastErr = err
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(backoff):
					backoff *= 2
					continue
				}
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("max retries exceeded: %w", lastErr)
}

// isTransient returns true for errors that are safe to retry.
// Covers gRPC transient codes (Unavailable, DeadlineExceeded, ResourceExhausted)
// as well as network-level timeouts and connection-refused errors.
func isTransient(err error) bool {
	if err == nil {
		return false
	}

	// gRPC status codes that are safe to retry
	if s, ok := status.FromError(err); ok {
		switch s.Code() {
		case codes.Unavailable, // 503-equivalent: server temporarily unavailable
			codes.DeadlineExceeded,  // timeout at the gRPC layer
			codes.ResourceExhausted: // rate-limited / quota exceeded
			return true
		}
	}

	// Network-level errors: timeout, temporary failures, connection refused
	var netErr net.Error
	if ok := (func() bool {
		// Use errors.As equivalent via type assertion for net.Error
		e := err
		for e != nil {
			if ne, ok := e.(net.Error); ok {
				netErr = ne
				return true
			}
			type unwrapper interface{ Unwrap() error }
			if u, ok := e.(unwrapper); ok {
				e = u.Unwrap()
			} else {
				break
			}
		}
		return false
	}()); ok && netErr != nil {
		if netErr.Timeout() || netErr.Temporary() { //nolint:staticcheck
			return true
		}
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "unavailable")
}

// GCTClient is the interface for interacting with GoCryptoTrader via gRPC
type GCTClient interface {
	// Connection management
	Connect(ctx context.Context, address string) error
	Disconnect() error
	IsConnected() bool
	HealthCheck(ctx context.Context) error

	// Market data
	GetTicker(ctx context.Context, exchange, pair string) (*Ticker, error)
	GetOrderbook(ctx context.Context, exchange, pair string) (*Orderbook, error)
	// GetCandles returns candles in ASCENDING time order (oldest first,
	// candles[len-1] is the most recent). This contract holds for every backend
	// path (gRPC, KuCoin spot REST, KuCoin futures REST). Consumers compute
	// indicators / read the latest candle assuming this order — do not return
	// newest-first.
	GetCandles(ctx context.Context, exchange, pair, interval string, limit int) ([]*Candle, error)

	// Account operations
	GetAccountInfo(ctx context.Context, exchange string) (*AccountInfo, error)

	// Order management
	SubmitOrder(ctx context.Context, req *SubmitOrderRequest) (*Order, error)
	GetOrderInfo(ctx context.Context, exchange, orderID string) (*Order, error)
	CancelOrder(ctx context.Context, exchange, orderID string) error

	// Portfolio
	GetPortfolio(ctx context.Context) (*Portfolio, error)

	// Additional methods
	GetEnabledExchanges(ctx context.Context) ([]string, error)
	GetOrders(ctx context.Context, exchange, pair, orderType, assetType string, startTime, endTime *time.Time) ([]*Order, error)
	GetTickerBySymbol(ctx context.Context, exchange, symbol, assetType string) (*Ticker, error)
	GetOrderBookBySymbol(ctx context.Context, exchange, symbol, assetType string) (*Orderbook, error)
	GetTrades(ctx context.Context, exchange, symbol string, limit int) ([]map[string]interface{}, error)
	GetOHLCV(ctx context.Context, exchange, symbol, interval string, limit int) ([]map[string]interface{}, error)
	CalculateRSI(ctx context.Context, exchange, symbol string, period int) (float64, error)
	CalculateMACD(ctx context.Context, exchange, symbol string, fastPeriod, slowPeriod, signalPeriod int) (map[string]float64, error)
	CalculateBollingerBands(ctx context.Context, exchange, symbol string, period int, stdDev float64) (map[string]float64, error)
	GetExchangeInfo(ctx context.Context, exchange string) (map[string]interface{}, error)
	GetAllTickers(ctx context.Context, exchange string) ([]map[string]interface{}, error)
	SimulateOrder(ctx context.Context, req *SubmitOrderRequest) (map[string]interface{}, error)
	GetBalance(ctx context.Context, exchange, currency string) (map[string]interface{}, error)
	GetPositions(ctx context.Context, exchange string) ([]map[string]interface{}, error)
	PlaceOrder(ctx context.Context, req *SubmitOrderRequest) (*Order, error)
	GetGRPCConn() *grpc.ClientConn
	SetKuCoinCredentials(apiKey, apiSecret, apiPassword string)

	// Futures trading methods
	SubmitFuturesOrder(ctx context.Context, req *SubmitOrderRequest) (*Order, error)
	GetFuturesPositions(ctx context.Context, exchange string) ([]*Position, error)
	GetFuturesPosition(ctx context.Context, exchange, symbol string) (*Position, error)
	CloseFuturesPosition(ctx context.Context, exchange, symbol string) error
	SetLeverage(ctx context.Context, exchange, symbol string, leverage int) error

	// Market type support (NEW)
	ConvertSymbol(symbol string, marketType MarketType, exchange string) (string, error)

	// Portfolio overview
	GetPortfolioOverview(ctx context.Context, exchange string) (*PortfolioOverview, error)
}

// restCircuitBreaker tracks REST API health to avoid repeated failing calls.
type restCircuitBreaker struct {
	mu               sync.Mutex
	failureCount     int
	lastFailure      time.Time
	state            string // "closed", "open", "half_open"
	failureThreshold int
	resetTimeout     time.Duration
}

func newRestCircuitBreaker(threshold int, resetTimeout time.Duration) *restCircuitBreaker {
	return &restCircuitBreaker{
		state:            "closed",
		failureThreshold: threshold,
		resetTimeout:     resetTimeout,
	}
}

// isOpen returns true if the circuit is open (REST calls should be skipped).
func (cb *restCircuitBreaker) isOpen() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if cb.state == "open" {
		if time.Since(cb.lastFailure) > cb.resetTimeout {
			cb.state = "half_open"
			return false
		}
		return true
	}
	return false
}

// recordFailure records a REST failure and may open the circuit.
func (cb *restCircuitBreaker) recordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failureCount++
	cb.lastFailure = time.Now()
	if cb.failureCount >= cb.failureThreshold {
		cb.state = "open"
	}
}

// recordSuccess resets the circuit breaker to closed state.
func (cb *restCircuitBreaker) recordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failureCount = 0
	cb.state = "closed"
}

// client implements GCTClient
type client struct {
	conn            *grpc.ClientConn
	rpcClient       gctrpc.GoCryptoTraderServiceClient
	address         string
	username        string
	password        string
	timeout         time.Duration
	kucoinClient    *KuCoinRESTClient
	futuresManager  *FuturesManager
	marginManager   *MarginManager   // Wave 4-A: margin scaffold (lazy-init)
	marginEnabled   bool             // Wave 4-A: mirrors bot.margin_enabled config gate
	symbolConverter *SymbolConverter // NEW: Symbol converter for market types
	logger          interface{}      // Will be set to *logrus.Logger when available
	restOnlyPairs   map[string]bool
	mu              sync.RWMutex
	restCB          *restCircuitBreaker

	// riskSentinel is the optional real-money risk gate consulted by
	// SubmitOrder/SubmitFuturesOrder (TOOL-01/HTTP-06 fix). nil by default so
	// non-production callers (tests, cmd/ai-bot-llm, cmd/ai-trading-bot) are
	// unaffected; the production server wires it via SetRiskSentinel right
	// after constructing the real RiskSentinel instance.
	riskSentinel risk.RiskSentinel

	// notional caches reference prices and contract multipliers used to compute
	// the USD notional of an order (order_notional.go).
	notional notionalState

	// liveOrderGate is the optional real-money gate of the product edition
	// (SetLiveOrderGate): consulted for every order that can open or increase
	// exposure. nil (standard build) = no gate.
	liveOrderGate func() error
}

// SetLiveOrderGate wires a gate consulted by every real order path
// (SubmitOrder spot route, SubmitFuturesOrder and everything that reaches
// them: MCP tools, REST handlers, the HFT wrapper) before an order that opens
// or increases exposure. A non-nil error rejects the order. Orders that only
// reduce or close a futures position are never gated, so exposure can always
// be unwound. The product edition sets it from main.go (license + Pro +
// trading.live_enabled); the standard build never calls it.
func (c *client) SetLiveOrderGate(gate func() error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.liveOrderGate = gate
}

// orderReducesExposure reports whether an order only reduces or closes
// exposure, which exempts it from the per-order cap and the live gate.
//
// Only the futures route sends reduceOnly/closeOrder to the exchange. The spot
// route (SubmitOrder -> gRPC SubmitOrderRequest) has no such field, so the
// flags are not enforced there: a spot "reduce_only" buy is an ordinary buy
// that increases exposure. On the spot route the flags therefore count only
// for a sell of a pair that really is spot (a spot sell can only dispose of
// holdings); a pair with a futures suffix sent down the spot route keeps the
// cap, because there a sell could open a short.
func orderReducesExposure(req *SubmitOrderRequest, futures bool) bool {
	if req == nil || !(req.ReduceOnly || req.CloseOrder) {
		return false
	}
	if futures {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(req.Side), "sell") && detectAssetType(req.Pair) == "spot"
}

// SetRiskSentinel wires the real-money risk gate into this client. Every
// order-submission path (spot via SubmitOrder, futures via SubmitFuturesOrder
// — including the "direct REST" MCP tools and HTTP handlers that call
// SubmitFuturesOrder directly via GetBaseClient()) consults it before hitting
// the exchange. Passing nil (the zero value) restores the previous
// unconditional-submission behaviour.
func (c *client) SetRiskSentinel(sentinel risk.RiskSentinel) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.riskSentinel = sentinel
}

// evaluateOrderRisk is the single chokepoint consulted by both SubmitOrder's
// spot path and SubmitFuturesOrder before submitting to the exchange. It is a
// no-op (nil error) when no RiskSentinel has been wired, preserving existing
// behaviour for callers that never call SetRiskSentinel.
//
// futures says which route the order takes, because the unit of req.Amount
// depends on it (USD on the futures route, base units on spot; see
// order_notional.go).
func (c *client) evaluateOrderRisk(ctx context.Context, req *SubmitOrderRequest, futures bool) error {
	c.mu.RLock()
	sentinel := c.riskSentinel
	gate := c.liveOrderGate
	c.mu.RUnlock()
	if req == nil {
		return nil
	}
	reduces := orderReducesExposure(req, futures)

	// Product edition: real-money orders that add exposure need the live gate
	// (Pro license, trading.live_enabled). Reductions and closes always pass.
	if gate != nil && !reduces {
		if err := gate(); err != nil {
			return err
		}
	}
	if sentinel == nil {
		return nil
	}

	actionType := risk.ActionOpenPosition
	if reduces {
		actionType = risk.ActionClosePosition
	}

	action := &risk.TradingAction{
		Type:     actionType,
		Symbol:   req.Pair,
		Side:     req.Side,
		Size:     req.Amount,
		Leverage: req.Leverage,
		Price:    req.Price,
		OwnPrice: req.Price > 0,
	}

	// The notional is the sentinel's per-order cap input. Orders that only
	// reduce or close exposure are never blocked by it, so no lookup is made
	// for them (a close must not depend on a price or contract-spec fetch).
	if actionType != risk.ActionClosePosition {
		maxAge := risk.DefaultReferencePriceMaxAge
		if p, ok := sentinel.(interface{ ReferencePriceMaxAge() time.Duration }); ok {
			maxAge = p.ReferencePriceMaxAge()
		}
		lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		n := c.computeOrderNotional(lookupCtx, req, futures, maxAge)
		cancel()
		action.NotionalUSD, action.NotionalUnknown, action.NotionalReason = n.USD, n.Unknown, n.Reason
		fields := logrus.Fields{"pair": req.Pair, "side": req.Side, "type": req.Type, "futures": futures}
		if n.Unknown {
			fields["reason"] = n.Reason
			logrus.WithFields(fields).Debug("order notional unknown")
		} else {
			fields["notional_usd"] = fmt.Sprintf("%.4f", n.USD)
			logrus.WithFields(fields).Debug("order notional computed")
		}
	}

	decision, err := sentinel.EvaluateAction(ctx, action)
	if err != nil {
		return fmt.Errorf("risk evaluation failed: %w", err)
	}
	if decision != nil && !decision.Allowed {
		return risk.NewRejectedError(decision)
	}
	return nil
}

// Config holds the configuration for the GCT client
type Config struct {
	Address     string
	Username    string
	Password    string
	Timeout     time.Duration
	MaxRetries  int
	UseTLS      bool
	TLSInsecure bool        // Skip certificate verification
	TLSCertFile string      // Path to TLS certificate file
	Logger      interface{} // Optional logger (*logrus.Logger)
}

// NewClient creates a new GoCryptoTrader gRPC client
func NewClient(cfg *Config) GCTClient {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}

	kucoinClient := NewKuCoinRESTClient()

	return &client{
		address:         cfg.Address,
		username:        cfg.Username,
		password:        cfg.Password,
		timeout:         cfg.Timeout,
		kucoinClient:    kucoinClient,
		symbolConverter: NewSymbolConverter(), // NEW: Initialize symbol converter
		logger:          cfg.Logger,
		restOnlyPairs:   make(map[string]bool),
		restCB:          newRestCircuitBreaker(20, 60*time.Second), // raised from 5: a scan of 50 pairs must not trip the CB
	}
}

// InitializeFuturesManager initializes the futures manager with a logger
// This should be called after the client is created and logger is available
func (c *client) InitializeFuturesManager(logger interface{}) error {
	if c == nil {
		return fmt.Errorf("client is nil")
	}
	if c.kucoinClient == nil {
		return fmt.Errorf("kucoin client is nil")
	}

	// Try to cast logger to *logrus.Logger
	var log interface{}
	if logger != nil {
		log = logger
	}

	// Create a minimal logger interface if needed
	// For now, we'll use a simple approach
	c.logger = log

	// We'll initialize futures manager lazily when needed
	return nil
}

// isRestOnlyPair checks if a pair is marked to skip gRPC and only use REST
func (c *client) isRestOnlyPair(pair string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.restOnlyPairs == nil {
		return false
	}
	return c.restOnlyPairs[pair]
}

// markAsRestOnly marks a pair to skip gRPC and only use REST
func (c *client) markAsRestOnly(pair string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.restOnlyPairs == nil {
		c.restOnlyPairs = make(map[string]bool)
	}
	c.restOnlyPairs[pair] = true
}

// SetKuCoinCredentials configura las credenciales de KuCoin para el cliente REST
func (c *client) SetKuCoinCredentials(apiKey, apiSecret, apiPassword string) {
	if c.kucoinClient != nil {
		c.kucoinClient.SetCredentials(apiKey, apiSecret, apiPassword)
	}
}

// Connect establishes a connection to GoCryptoTrader gRPC server
func (c *client) Connect(ctx context.Context, address string) error {
	if address != "" {
		c.address = address
	}

	if c.address == "" {
		return fmt.Errorf("gRPC address is required")
	}

	// Create gRPC connection with TLS
	// GoCryptoTrader uses TLS by default
	var opts []grpc.DialOption

	// Configure TLS
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true, // Skip certificate verification for self-signed certs
	}
	creds := credentials.NewTLS(tlsConfig)
	opts = append(opts, grpc.WithTransportCredentials(creds))

	// Add block option to wait for connection
	opts = append(opts, grpc.WithBlock())

	conn, err := grpc.DialContext(ctx, c.address, opts...)
	if err != nil {
		return fmt.Errorf("failed to connect to GoCryptoTrader at %s: %w", c.address, err)
	}

	c.conn = conn
	c.rpcClient = gctrpc.NewGoCryptoTraderServiceClient(conn)

	// Validate KuCoin futures access (optional - don't block startup)
	// This helps detect configuration issues early
	if c.kucoinClient != nil && c.kucoinClient.HasCredentials() {
		if err := c.ValidateKuCoinFuturesAccess(ctx); err != nil {
			// Log warning but don't fail connection
			if c.logger != nil {
				if log, ok := c.logger.(*logrus.Logger); ok {
					log.Warnf("KuCoin futures validation failed: %v", err)
				}
			}
		}
	}

	return nil
}

// Disconnect closes the connection to GoCryptoTrader
func (c *client) Disconnect() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// IsConnected returns true if the client is connected
func (c *client) IsConnected() bool {
	return c.conn != nil && c.conn.GetState().String() == "READY"
}

// HealthCheck performs a health check on the GoCryptoTrader server
func (c *client) HealthCheck(ctx context.Context) error {
	if !c.IsConnected() {
		return fmt.Errorf("not connected to GoCryptoTrader")
	}

	ctx = c.withAuth(ctx)

	// Use GetInfo as a health check
	_, err := c.rpcClient.GetInfo(ctx, &gctrpc.GetInfoRequest{})
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}

	return nil
}

// withAuth adds authentication metadata to the context
func (c *client) withAuth(ctx context.Context) context.Context {
	if c.username != "" && c.password != "" {
		// GoCryptoTrader expects Basic authentication with base64 encoding
		auth := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", c.username, c.password)))
		md := metadata.New(map[string]string{
			"authorization": fmt.Sprintf("Basic %s", auth),
		})
		ctx = metadata.NewOutgoingContext(ctx, md)
	} else {
		// Log warning if credentials are missing
		fmt.Printf("WARNING: GCT credentials not set - username: '%s', password: '%s'\n", c.username, c.password)
	}
	return ctx
}

// GetTicker retrieves ticker data for a specific exchange and pair
func (c *client) GetTicker(ctx context.Context, exchange, pair string) (*Ticker, error) {
	if !c.IsConnected() {
		// The product build runs without GoCryptoTrader (public market data and
		// the paper broker): KuCoin tickers come straight from the public REST
		// API. The standard build keeps failing, exactly as before.
		if edition.IsProduct() && strings.EqualFold(exchange, "kucoin") && c.kucoinClient != nil {
			return c.getKuCoinTickerREST(ctx, exchange, pair)
		}
		return nil, fmt.Errorf("not connected to GoCryptoTrader")
	}

	// Si es KuCoin, intentar usar el cliente REST directamente como fallback
	if exchange == "kucoin" || exchange == "Kucoin" {
		// Intentar primero con gRPC
		ctx = c.withAuth(ctx)
		base, quote, delimiter := parseCurrencyPair(pair)
		assetType := detectAssetType(pair)

		var resp *gctrpc.TickerResponse
		var err error

		if !c.isRestOnlyPair(pair) {
			resp, err = c.rpcClient.GetTicker(ctx, &gctrpc.GetTickerRequest{
				Exchange: exchange,
				Pair: &gctrpc.CurrencyPair{
					Delimiter: delimiter,
					Base:      base,
					Quote:     quote,
				},
				AssetType: assetType,
			})
		} else {
			// Skip gRPC directly
			err = fmt.Errorf("rpc error: code = Unknown desc = %s currency not enabled (cached)", pair)
		}

		// Si gRPC falla, usar REST API directamente
		if err != nil {
			// Marcar como REST-only si es el error conocido de moneda no habilitada
			if strings.Contains(err.Error(), "currency not enabled") {
				c.markAsRestOnly(pair)
			}

			// Convertir formato de par si es necesario
			symbol := pair
			if delimiter == "" {
				// Si no hay delimitador, agregar uno para spot
				if assetType != "futures" {
					symbol = base + "-" + quote
				}
			}

			// Para futures, convertir formato (DOGE-USDTM -> DOGEUSDTM)
			if assetType == "futures" {
				symbol = strings.ReplaceAll(pair, "-", "")
			}

			if c.restCB.isOpen() {
				if c.logger != nil {
					if log, ok := c.logger.(*logrus.Logger); ok {
						log.WithField("pair", pair).Warn("Circuit breaker OPEN for REST - waiting 60s before retry")
					}
				}
				return nil, fmt.Errorf("REST circuit breaker open: %w", err)
			}
			restTicker, restErr := c.kucoinClient.GetTicker(ctx, symbol)
			if restErr != nil {
				c.restCB.recordFailure()
				return nil, fmt.Errorf("failed to get ticker via gRPC (%w) and REST (%w)", err, restErr)
			}
			c.restCB.recordSuccess()

			return &Ticker{
				Exchange:  exchange,
				Pair:      pair,
				Last:      restTicker.Last,
				Bid:       restTicker.Bid,
				Ask:       restTicker.Ask,
				Volume:    0, // REST API no retorna volumen en este endpoint
				High:      0,
				Low:       0,
				Timestamp: restTicker.Timestamp,
			}, nil
		}

		return &Ticker{
			Exchange:  exchange,
			Pair:      pair,
			Last:      resp.Last,
			Bid:       resp.Bid,
			Ask:       resp.Ask,
			Volume:    resp.Volume,
			High:      resp.High,
			Low:       resp.Low,
			Timestamp: time.Now(),
		}, nil
	}

	// Para otros exchanges, usar solo gRPC
	ctx = c.withAuth(ctx)
	base, quote, delimiter := parseCurrencyPair(pair)
	assetType := detectAssetType(pair)

	resp, err := c.rpcClient.GetTicker(ctx, &gctrpc.GetTickerRequest{
		Exchange: exchange,
		Pair: &gctrpc.CurrencyPair{
			Delimiter: delimiter,
			Base:      base,
			Quote:     quote,
		},
		AssetType: assetType,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get ticker: %w", err)
	}

	return &Ticker{
		Exchange:  exchange,
		Pair:      pair,
		Last:      resp.Last,
		Bid:       resp.Bid,
		Ask:       resp.Ask,
		Volume:    resp.Volume,
		High:      resp.High,
		Low:       resp.Low,
		Timestamp: time.Now(),
	}, nil
}

// GetOrderbook retrieves orderbook data for a specific exchange and pair.
// Retries up to 3 times on transient gRPC errors (idempotent read operation).
func (c *client) GetOrderbook(ctx context.Context, exchange, pair string) (*Orderbook, error) {
	if !c.IsConnected() {
		return nil, fmt.Errorf("not connected to GoCryptoTrader")
	}

	ctx = c.withAuth(ctx)
	base, quote, delimiter := parseCurrencyPair(pair)
	assetType := detectAssetType(pair)

	var resp *gctrpc.OrderbookResponse
	err := withRetry(ctx, 3, func() error {
		var callErr error
		resp, callErr = c.rpcClient.GetOrderbook(ctx, &gctrpc.GetOrderbookRequest{
			Exchange: exchange,
			Pair: &gctrpc.CurrencyPair{
				Delimiter: delimiter,
				Base:      base,
				Quote:     quote,
			},
			AssetType: assetType,
		})
		return callErr
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get orderbook: %w", err)
	}

	orderbook := &Orderbook{
		Exchange:  exchange,
		Pair:      pair,
		Bids:      make([]OrderbookEntry, 0),
		Asks:      make([]OrderbookEntry, 0),
		Timestamp: time.Now(),
	}

	for _, bid := range resp.Bids {
		orderbook.Bids = append(orderbook.Bids, OrderbookEntry{
			Price:  bid.Price,
			Amount: bid.Amount,
		})
	}

	for _, ask := range resp.Asks {
		orderbook.Asks = append(orderbook.Asks, OrderbookEntry{
			Price:  ask.Price,
			Amount: ask.Amount,
		})
	}

	return orderbook, nil
}

// GetCandles retrieves candle/OHLCV data
func (c *client) GetCandles(ctx context.Context, exchange, pair, interval string, limit int) ([]*Candle, error) {
	if !c.IsConnected() {
		return nil, fmt.Errorf("not connected to GoCryptoTrader")
	}

	// Si es KuCoin, intentar usar el cliente REST directamente como fallback
	if exchange == "kucoin" || exchange == "Kucoin" {
		// Intentar primero con gRPC
		ctx = c.withAuth(ctx)

		// Convert interval string to seconds
		var intervalSeconds int64
		switch interval {
		case "60", "1m":
			intervalSeconds = 60
		case "300", "5m":
			intervalSeconds = 300
		case "900", "15m":
			intervalSeconds = 900
		case "3600", "1h":
			intervalSeconds = 3600
		default:
			intervalSeconds = 60
		}

		endTime := time.Now()
		startTime := endTime.Add(-time.Duration(limit) * time.Duration(intervalSeconds) * time.Second)

		base, quote, delimiter := parseCurrencyPair(pair)
		assetType := detectAssetType(pair)
		timeFormat := "2006-01-02 15:04:05 MST"

		var resp *gctrpc.GetHistoricCandlesResponse
		var err error

		if !c.isRestOnlyPair(pair) {
			resp, err = c.rpcClient.GetHistoricCandles(ctx, &gctrpc.GetHistoricCandlesRequest{
				Exchange: exchange,
				Pair: &gctrpc.CurrencyPair{
					Delimiter: delimiter,
					Base:      base,
					Quote:     quote,
				},
				AssetType:    assetType,
				Start:        startTime.Format(timeFormat),
				End:          endTime.Format(timeFormat),
				TimeInterval: intervalSeconds,
			})
		} else {
			// Skip gRPC directly
			err = fmt.Errorf("rpc error: code = Unknown desc = %s currency not enabled (cached)", pair)
		}

		// Si gRPC falla, usar REST API directamente
		if err != nil {
			// Marcar como REST-only si es el error conocido de moneda no habilitada
			if strings.Contains(err.Error(), "currency not enabled") {
				c.markAsRestOnly(pair)
			}

			if c.logger != nil {
				logger := c.logger.(*logrus.Logger)
				logger.WithFields(logrus.Fields{
					"exchange": exchange,
					"pair":     pair,
					"interval": interval,
					"limit":    limit,
					"error":    err.Error(),
				}).Warn("Failed to get candles via gRPC, trying REST fallback")
			}

			symbol := pair
			if delimiter == "" {
				symbol = base + "-" + quote
			}

			if c.restCB.isOpen() {
				if c.logger != nil {
					if log, ok := c.logger.(*logrus.Logger); ok {
						log.WithField("pair", pair).Warn("Circuit breaker OPEN for REST - waiting 60s before retry")
					}
				}
				return nil, fmt.Errorf("REST circuit breaker open: %w", err)
			}
			restCandles, restErr := c.kucoinClient.GetCandles(ctx, symbol, interval, limit)
			if restErr != nil {
				c.restCB.recordFailure()
				if c.logger != nil {
					logger := c.logger.(*logrus.Logger)
					logger.WithFields(logrus.Fields{
						"exchange":   exchange,
						"pair":       pair,
						"grpc_error": err.Error(),
						"rest_error": restErr.Error(),
					}).Error("Failed to get candles via both gRPC and REST")
				}
				return nil, fmt.Errorf("failed to get candles via gRPC (%w) and REST (%w)", err, restErr)
			}
			c.restCB.recordSuccess()

			if c.logger != nil {
				logger := c.logger.(*logrus.Logger)
				logger.WithFields(logrus.Fields{
					"exchange": exchange,
					"pair":     pair,
					"candles":  len(restCandles),
				}).Info("Successfully retrieved candles via REST fallback")
			}

			return restCandles, nil
		}

		candles := make([]*Candle, 0, len(resp.Candle))
		for _, c := range resp.Candle {
			timestamp, _ := time.Parse(time.RFC3339, c.Time)
			candles = append(candles, &Candle{
				Timestamp: timestamp,
				Open:      c.Open,
				High:      c.High,
				Low:       c.Low,
				Close:     c.Close,
				Volume:    c.Volume,
			})
		}

		return candles, nil
	}

	// Para otros exchanges, usar solo gRPC
	ctx = c.withAuth(ctx)

	var intervalSeconds int64
	switch interval {
	case "60", "1m":
		intervalSeconds = 60
	case "300", "5m":
		intervalSeconds = 300
	case "900", "15m":
		intervalSeconds = 900
	case "3600", "1h":
		intervalSeconds = 3600
	default:
		intervalSeconds = 60
	}

	endTime := time.Now()
	startTime := endTime.Add(-time.Duration(limit) * time.Duration(intervalSeconds) * time.Second)

	base, quote, delimiter := parseCurrencyPair(pair)
	assetType := detectAssetType(pair)
	timeFormat := "2006-01-02 15:04:05 MST"

	resp, err := c.rpcClient.GetHistoricCandles(ctx, &gctrpc.GetHistoricCandlesRequest{
		Exchange: exchange,
		Pair: &gctrpc.CurrencyPair{
			Delimiter: delimiter,
			Base:      base,
			Quote:     quote,
		},
		AssetType:    assetType,
		Start:        startTime.Format(timeFormat),
		End:          endTime.Format(timeFormat),
		TimeInterval: intervalSeconds,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get candles: %w", err)
	}

	candles := make([]*Candle, 0, len(resp.Candle))
	for _, c := range resp.Candle {
		timestamp, _ := time.Parse(time.RFC3339, c.Time)
		candles = append(candles, &Candle{
			Timestamp: timestamp,
			Open:      c.Open,
			High:      c.High,
			Low:       c.Low,
			Close:     c.Close,
			Volume:    c.Volume,
		})
	}

	return candles, nil
}

// GetAccountInfo retrieves account information
func (c *client) GetAccountInfo(ctx context.Context, exchange string) (*AccountInfo, error) {
	if !c.IsConnected() {
		return nil, fmt.Errorf("not connected to GoCryptoTrader")
	}

	ctx = c.withAuth(ctx)

	// Use UpdateAccountBalances to get fresh data from exchange
	resp, err := c.rpcClient.UpdateAccountBalances(ctx, &gctrpc.GetAccountBalancesRequest{
		Exchange:  exchange,
		AssetType: "spot", // Start with spot
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get account balances: %w", err)
	}

	accountInfo := &AccountInfo{
		Exchange: exchange,
		Accounts: make([]Account, 0),
	}

	// Process accounts
	for _, acc := range resp.Accounts {
		account := Account{
			ID:       acc.Id,
			Balances: make(map[string]Balance),
		}

		// Process balances
		for _, bal := range acc.Currencies {
			balance := Balance{
				Currency:  bal.Currency,
				Total:     bal.TotalValue,
				Available: bal.TotalValue - bal.Hold,
				InOrder:   bal.Hold,
			}
			account.Balances[bal.Currency] = balance
		}

		accountInfo.Accounts = append(accountInfo.Accounts, account)
	}

	return accountInfo, nil
}

// SubmitOrder submits a new order
func (c *client) SubmitOrder(ctx context.Context, req *SubmitOrderRequest) (*Order, error) {
	// Wave 4-A: margin gate fires BEFORE the connectivity check so that a
	// disabled-gate error is returned even when GCT is not running.  This lets
	// unit tests (and operators) confirm gate behaviour without a live gRPC
	// connection.  The gate does not bypass any spot/futures routing.
	if req != nil && req.MarketType == MarketTypeMargin {
		c.mu.RLock()
		enabled := c.marginEnabled
		c.mu.RUnlock()
		if !enabled {
			return nil, ErrMarginDisabled
		}
		// Gate is open: initialise the margin manager, then delegate.  The
		// manager currently returns ErrMarginNotImplemented until
		// SubmitMarginOrder is wired in KuCoinRESTClient.
		if err := c.ensureMarginManager(); err != nil {
			return nil, fmt.Errorf("margin manager init failed: %w", err)
		}
		return c.marginManager.PlaceMarginOrder(ctx, req)
	}

	if !c.IsConnected() {
		return nil, fmt.Errorf("not connected to GoCryptoTrader")
	}

	// Convert symbol if MarketType is provided
	pair := req.Pair
	if req.MarketType != "" {
		convertedPair, err := c.ConvertSymbol(pair, req.MarketType, req.Exchange)
		if err != nil {
			return nil, fmt.Errorf("failed to convert symbol: %w", err)
		}
		pair = convertedPair
		req.Pair = pair // Update request with converted pair
	}

	// Validate leverage based on market type
	if req.MarketType == MarketTypeSpot && req.Leverage > 0 && req.Leverage != 1 {
		return nil, fmt.Errorf("leverage not allowed for spot trading")
	}
	if req.MarketType == MarketTypeFutures && req.Leverage > 0 && (req.Leverage < 1 || req.Leverage > 100) {
		return nil, fmt.Errorf("leverage must be between 1 and 100 for futures")
	}

	// Explicit spot override: force spot path even when leverage > 0 (e.g. leverage was set
	// inadvertently). The validation block above already guards leverage != 1 for spot.
	if req.MarketType == MarketTypeSpot {
		// Fall through to the spot path below (skip the leverage>0 futures gate).
		goto spotPath
	}

	// Route to futures when leverage is set OR when market type is explicitly futures.
	if req.Leverage > 0 || req.MarketType == MarketTypeFutures {
		if req.MarketType == MarketTypeFutures && req.Leverage == 0 {
			req.Leverage = 1 // default leverage for explicit futures with no leverage set
		}
		return c.SubmitFuturesOrder(ctx, req)
	}

spotPath:

	// TOOL-01 fix: consult the real-money risk gate (kill switch, drawdown,
	// consecutive losses, exposure, max_order_size_usd) before submitting. A
	// no-op when no RiskSentinel has been wired via SetRiskSentinel.
	if err := c.evaluateOrderRisk(ctx, req, false); err != nil {
		return nil, err
	}

	ctx = c.withAuth(ctx)
	base, quote, delimiter := parseCurrencyPair(req.Pair)
	assetType := detectAssetType(req.Pair)

	// For futures without explicit leverage, still configure if detected
	marginType := ""
	if assetType == "futures" {
		marginType = "" // Let KuCoin use account default margin mode

		// NOTA: NO configurar margin mode aquí para evitar conflictos con Position Mode
		// KuCoin usará la configuración por defecto de la cuenta
		// Si es KuCoin futures, configurar margin mode y leverage
		// if req.Exchange == "kucoin" || req.Exchange == "Kucoin" {
		// 	// Convertir el par al formato de símbolo de KuCoin (ej: DOGE-USDTM -> DOGEUSDTM)
		// 	symbol := strings.ReplaceAll(req.Pair, "-", "")
		//
		// 	// Configurar modo de margen primero
		// 	fmt.Printf("⚙️  Configurando margin mode CROSSED para %s...\n", symbol)
		// 	if err := c.kucoinClient.SetMarginMode(ctx, symbol, "CROSSED"); err != nil {
		// 		fmt.Printf("⚠️  Warning: Failed to set margin mode: %v\n", err)
		// 	}
		//
		// 	// Luego configurar leverage si se especificó
		// 	if req.Leverage > 0 {
		// 		fmt.Printf("⚙️  Configurando leverage %dx para %s...\n", int(req.Leverage), symbol)
		// 		if err := c.kucoinClient.SetLeverage(ctx, symbol, int(req.Leverage)); err != nil {
		// 			fmt.Printf("⚠️  Warning: Failed to set leverage: %v\n", err)
		// 		}
		// 	}
		// }
	}

	resp, err := c.rpcClient.SubmitOrder(ctx, &gctrpc.SubmitOrderRequest{
		Exchange: req.Exchange,
		Pair: &gctrpc.CurrencyPair{
			Delimiter: delimiter,
			Base:      base,
			Quote:     quote,
		},
		Side:       req.Side,
		OrderType:  req.Type,
		Amount:     req.Amount,
		Price:      req.Price,
		AssetType:  assetType,
		ClientId:   req.ClientID,
		MarginType: marginType,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to submit order: %w", err)
	}

	// Parse base and quote currencies from pair
	baseCurrency := ""
	quoteCurrency := ""
	if len(req.Pair) >= 7 {
		baseCurrency = req.Pair[:3]
		quoteCurrency = req.Pair[4:]
	}

	now := time.Now()
	return &Order{
		ID:            resp.OrderId,
		Exchange:      req.Exchange,
		Pair:          req.Pair,
		BaseCurrency:  baseCurrency,
		QuoteCurrency: quoteCurrency,
		Side:          req.Side,
		Type:          req.Type,
		Price:         req.Price,
		Amount:        req.Amount,
		Remaining:     req.Amount,
		OpenVolume:    req.Amount,
		Status:        "submitted",
		CreatedAt:     now,
		UpdatedAt:     now,
		UpdateTime:    now,
	}, nil
}

// GetOrderInfo retrieves information about a specific order
func (c *client) GetOrderInfo(ctx context.Context, exchange, orderID string) (*Order, error) {
	if !c.IsConnected() {
		return nil, fmt.Errorf("not connected to GoCryptoTrader")
	}

	ctx = c.withAuth(ctx)

	// TODO: Find correct method name in gctrpc
	// resp, err := c.rpcClient.GetOrderInfo(ctx, &gctrpc.GetOrderInfoRequest{
	// 	Exchange: exchange,
	// 	OrderId:  orderID,
	// })

	return &Order{
		ID:        orderID,
		Exchange:  exchange,
		Status:    "unknown",
		CreatedAt: time.Now(),
	}, fmt.Errorf("GetOrderInfo not yet implemented - need to find correct gRPC method")
}

// CancelOrder cancels an existing order
func (c *client) CancelOrder(ctx context.Context, exchange, orderID string) error {
	if !c.IsConnected() {
		return fmt.Errorf("not connected to GoCryptoTrader")
	}

	ctx = c.withAuth(ctx)

	_, err := c.rpcClient.CancelOrder(ctx, &gctrpc.CancelOrderRequest{
		Exchange: exchange,
		OrderId:  orderID,
	})
	if err != nil {
		return fmt.Errorf("failed to cancel order: %w", err)
	}

	return nil
}

// CancelFuturesOrder cancels a futures order via the direct KuCoin REST path —
// the same path SubmitFuturesOrder places them on. GCT's gRPC CancelOrder only
// knows orders inside its own order manager, so direct-REST orders fail there
// with "currency pair unset". Non-KuCoin exchanges (or missing credentials)
// fall back to the gRPC cancel. Deliberately NOT part of the GCTClient
// interface so existing test mocks keep compiling; callers reach it via a
// narrow type assertion (see gctclient.Client.CancelFuturesOrder).
func (c *client) CancelFuturesOrder(ctx context.Context, exchange, orderID string) error {
	if strings.EqualFold(exchange, "kucoin") && c.kucoinClient != nil && c.kucoinClient.HasCredentials() {
		return c.kucoinClient.CancelFuturesOrder(ctx, orderID)
	}
	return c.CancelOrder(ctx, exchange, orderID)
}

// GetOrderFillInfo returns the total size and filled quantity for orderID,
// queried directly against KuCoin's REST order-status endpoints (futures
// then spot). GetOrderInfo/GCT's gRPC order manager doesn't see orders
// placed through the direct-REST path (same root cause as
// CancelFuturesOrder above), so this exists specifically to let modify_order
// (TOOL-05) discover partial fills before deciding how much to resubmit.
// Deliberately NOT part of the GCTClient interface so existing test mocks
// keep compiling; callers reach it via a narrow type assertion (see
// gctclient.Client.GetOrderFillInfo).
func (c *client) GetOrderFillInfo(ctx context.Context, exchange, orderID string) (size, filled float64, err error) {
	if !strings.EqualFold(exchange, "kucoin") || c.kucoinClient == nil || !c.kucoinClient.HasCredentials() {
		return 0, 0, fmt.Errorf("GetOrderFillInfo: not supported for exchange %q", exchange)
	}
	size, filled, _, err = c.kucoinClient.GetOrderFill(ctx, orderID)
	return size, filled, err
}

// GetPortfolio retrieves portfolio information
func (c *client) GetPortfolio(ctx context.Context) (*Portfolio, error) {
	if !c.IsConnected() {
		return nil, fmt.Errorf("not connected to GoCryptoTrader")
	}

	// Try gRPC first
	portfolio, err := c.getPortfolioViaGRPC(ctx)
	if err == nil {
		return portfolio, nil
	}

	// Log gRPC failure
	if c.logger != nil {
		logger := c.logger.(*logrus.Logger)
		logger.WithError(err).Warn("Failed to get portfolio via gRPC, trying REST fallback")
	}

	// Fallback to REST for KuCoin
	if c.kucoinClient != nil {
		if c.logger != nil {
			logger := c.logger.(*logrus.Logger)
			logger.Info("Attempting to get portfolio via REST API")
		}

		restPortfolio, restErr := c.kucoinClient.GetPortfolioViaREST(ctx)
		if restErr == nil {
			if c.logger != nil {
				logger := c.logger.(*logrus.Logger)
				logger.WithFields(logrus.Fields{
					"total_value": restPortfolio.TotalValue,
					"holdings":    len(restPortfolio.Holdings),
				}).Info("Successfully retrieved portfolio via REST fallback")
			}
			return restPortfolio, nil
		}

		// Log REST failure with details
		if c.logger != nil {
			logger := c.logger.(*logrus.Logger)
			logger.WithFields(logrus.Fields{
				"error": restErr.Error(),
			}).Error("Failed to get portfolio via REST")
		}

		return nil, fmt.Errorf("failed to get portfolio via gRPC (%w) and REST (%w)", err, restErr)
	}

	return nil, fmt.Errorf("failed to get portfolio via gRPC (%w) and REST client not available", err)
}

// getPortfolioViaGRPC retrieves portfolio via gRPC
func (c *client) getPortfolioViaGRPC(ctx context.Context) (*Portfolio, error) {
	ctx = c.withAuth(ctx)

	// Use UpdateAccountBalances to force GoCryptoTrader to fetch fresh data from exchange
	resp, err := c.rpcClient.UpdateAccountBalances(ctx, &gctrpc.GetAccountBalancesRequest{
		Exchange:  "kucoin", // TODO: Make this configurable or get all exchanges
		AssetType: "spot",   // Start with spot, can add margin/futures later
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get account balances: %w", err)
	}

	portfolio := &Portfolio{
		TotalValue:    0,
		TotalPnL:      0,
		PnL:           0,
		PnLPercentage: 0,
		Holdings:      make(map[string]*Holding),
		Allocations:   make(map[string]float64),
		UpdatedAt:     time.Now(),
	}

	// Process account balances
	for _, account := range resp.Accounts {
		for _, balance := range account.Currencies {
			holding := &Holding{
				Currency:  balance.Currency,
				Total:     balance.TotalValue,
				InOrder:   balance.Hold,
				Available: balance.TotalValue - balance.Hold,
				Value:     balance.TotalValue, // TODO: Convert to USD
			}

			// Use currency as key, or combine with account ID if needed
			key := balance.Currency
			if existing, ok := portfolio.Holdings[key]; ok {
				// Aggregate if same currency exists in multiple accounts
				existing.Total += holding.Total
				existing.InOrder += holding.InOrder
				existing.Available += holding.Available
				existing.Value += holding.Value
			} else {
				portfolio.Holdings[key] = holding
			}

			portfolio.TotalValue += holding.Value
		}
	}

	// Calculate allocations
	if portfolio.TotalValue > 0 {
		for currency, holding := range portfolio.Holdings {
			portfolio.Allocations[currency] = (holding.Value / portfolio.TotalValue) * 100
		}
	}

	// Set PnL aliases
	portfolio.PnL = portfolio.TotalPnL
	if portfolio.TotalValue > 0 {
		portfolio.PnLPercentage = (portfolio.TotalPnL / portfolio.TotalValue) * 100
	}

	return portfolio, nil
}

// GetEnabledExchanges returns a list of enabled exchanges.
// Retries up to 3 times on transient gRPC errors (idempotent read operation).
func (c *client) GetEnabledExchanges(ctx context.Context) ([]string, error) {
	if !c.IsConnected() {
		return nil, fmt.Errorf("not connected to GoCryptoTrader")
	}

	ctx = c.withAuth(ctx)

	var resp *gctrpc.GetExchangesResponse
	err := withRetry(ctx, 3, func() error {
		var callErr error
		resp, callErr = c.rpcClient.GetExchanges(ctx, &gctrpc.GetExchangesRequest{
			Enabled: true,
		})
		return callErr
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get enabled exchanges: %w", err)
	}

	// Parse exchanges from response
	exchanges := []string{}
	if resp.Exchanges != "" {
		// Split by comma or return as single exchange
		exchanges = append(exchanges, resp.Exchanges)
	}
	return exchanges, nil
}

// GetOrders retrieves orders from an exchange
func (c *client) GetOrders(ctx context.Context, exchange, pair, orderType, assetType string, startTime, endTime *time.Time) ([]*Order, error) {
	if !c.IsConnected() {
		return nil, fmt.Errorf("not connected to GoCryptoTrader")
	}

	ctx = c.withAuth(ctx)

	req := &gctrpc.GetOrdersRequest{
		Exchange: exchange,
	}

	// Parse currency pair if provided
	if pair != "" {
		base, quote, delimiter := parseCurrencyPair(pair)
		if base != "" && quote != "" {
			req.Pair = &gctrpc.CurrencyPair{
				Delimiter: delimiter,
				Base:      base,
				Quote:     quote,
			}
		}
	}

	// Set asset type - default to spot if not specified
	if assetType != "" {
		req.AssetType = assetType
	} else if pair != "" {
		// Auto-detect from pair
		req.AssetType = detectAssetType(pair)
	} else {
		req.AssetType = "spot"
	}

	// Log request details for debugging
	if c.logger != nil {
		if log, ok := c.logger.(*logrus.Logger); ok {
			log.WithFields(logrus.Fields{
				"exchange":   exchange,
				"pair":       pair,
				"asset_type": req.AssetType,
			}).Debug("Fetching orders via gRPC")
		}
	}

	resp, err := c.rpcClient.GetOrders(ctx, req)
	if err != nil {
		// For KuCoin, try REST API fallback
		if (exchange == "kucoin" || exchange == "Kucoin") && c.kucoinClient != nil {
			if c.logger != nil {
				if log, ok := c.logger.(*logrus.Logger); ok {
					log.WithFields(logrus.Fields{
						"exchange": exchange,
						"error":    err.Error(),
					}).Warn("Failed to get orders via gRPC, trying REST fallback")
				}
			}

			// For futures, callers resolving an EXTERNAL CLOSE need the
			// closing FILL — a completed order that never appears in the
			// open-orders endpoint. Use recent fills (last 24h) there; keep
			// open orders for spot (legacy behavior).
			var restOrders []*Order
			var restErr error
			if req.AssetType == "futures" {
				restOrders, restErr = c.kucoinClient.GetFuturesRecentFills(ctx, pair)
			} else {
				restOrders, restErr = c.kucoinClient.GetOpenOrders(ctx, pair)
			}
			if restErr != nil {
				return nil, fmt.Errorf("failed to get orders via gRPC (%w) and REST (%w)", err, restErr)
			}

			if c.logger != nil {
				if log, ok := c.logger.(*logrus.Logger); ok {
					log.WithFields(logrus.Fields{
						"exchange":     exchange,
						"orders_count": len(restOrders),
					}).Info("Successfully retrieved orders via REST fallback")
				}
			}

			return restOrders, nil
		}

		return nil, fmt.Errorf("failed to get orders: %w", err)
	}

	var orders []*Order
	for _, o := range resp.Orders {
		// Parse timestamps from response
		var createdAt, updatedAt time.Time
		if o.CreationTime != "" {
			if t, err := time.Parse(time.RFC3339, o.CreationTime); err == nil {
				createdAt = t
			}
		}
		if o.UpdateTime != "" {
			if t, err := time.Parse(time.RFC3339, o.UpdateTime); err == nil {
				updatedAt = t
			}
		}
		if createdAt.IsZero() {
			createdAt = time.Now()
		}
		if updatedAt.IsZero() {
			updatedAt = createdAt
		}

		// Build pair string from base and quote currencies
		orderPair := ""
		if o.BaseCurrency != "" && o.QuoteCurrency != "" {
			orderPair = o.BaseCurrency + "-" + o.QuoteCurrency
		}

		// OpenVolume from response represents remaining amount
		remaining := o.OpenVolume
		if remaining == 0 {
			remaining = o.Amount
		}

		order := &Order{
			ID:            o.Id,
			Exchange:      o.Exchange,
			Pair:          orderPair,
			BaseCurrency:  o.BaseCurrency,
			QuoteCurrency: o.QuoteCurrency,
			Side:          o.OrderSide,
			Type:          o.OrderType,
			Status:        o.Status,
			Price:         o.Price,
			Amount:        o.Amount,
			OpenVolume:    remaining,
			Remaining:     remaining,
			CreatedAt:     createdAt,
			UpdatedAt:     updatedAt,
			UpdateTime:    updatedAt,
		}
		orders = append(orders, order)
	}

	if c.logger != nil {
		if log, ok := c.logger.(*logrus.Logger); ok {
			log.WithFields(logrus.Fields{
				"exchange":     exchange,
				"orders_count": len(orders),
			}).Debug("Successfully retrieved orders via gRPC")
		}
	}

	return orders, nil
}

// getKuCoinTickerREST reads a KuCoin ticker from the public REST API, with the
// same symbol conversion the gRPC fallback in GetTicker applies.
func (c *client) getKuCoinTickerREST(ctx context.Context, exchange, pair string) (*Ticker, error) {
	base, quote, delimiter := parseCurrencyPair(pair)
	symbol := pair
	if detectAssetType(pair) == "futures" {
		symbol = strings.ReplaceAll(pair, "-", "")
	} else if delimiter == "" {
		symbol = base + "-" + quote
	}
	t, err := c.kucoinClient.GetTicker(ctx, symbol)
	if err != nil {
		return nil, fmt.Errorf("GoCryptoTrader is not connected and the public KuCoin ticker failed: %w", err)
	}
	return &Ticker{Exchange: exchange, Pair: pair, Last: t.Last, Bid: t.Bid, Ask: t.Ask, Timestamp: t.Timestamp}, nil
}

// GetTickerBySymbol retrieves ticker data for a specific symbol
func (c *client) GetTickerBySymbol(ctx context.Context, exchange, symbol, assetType string) (*Ticker, error) {
	// This is just an alias for GetTicker with different parameter names
	return c.GetTicker(ctx, exchange, symbol)
}

// GetOrderBookBySymbol retrieves orderbook for a specific symbol
func (c *client) GetOrderBookBySymbol(ctx context.Context, exchange, symbol, assetType string) (*Orderbook, error) {
	// This is just an alias for GetOrderbook
	return c.GetOrderbook(ctx, exchange, symbol)
}

// GetTrades retrieves recent trades from the exchange
func (c *client) GetTrades(ctx context.Context, exchange, symbol string, limit int) ([]map[string]interface{}, error) {
	if !c.IsConnected() {
		return nil, fmt.Errorf("not connected to GoCryptoTrader")
	}

	ctx = c.withAuth(ctx)

	// Parse currency pair
	base, quote, delimiter := parseCurrencyPair(symbol)
	assetType := detectAssetType(symbol)

	// Log the request
	if c.logger != nil {
		if log, ok := c.logger.(*logrus.Logger); ok {
			log.WithFields(logrus.Fields{
				"exchange":  exchange,
				"symbol":    symbol,
				"base":      base,
				"quote":     quote,
				"assetType": assetType,
				"limit":     limit,
			}).Debug("Fetching trades via gRPC")
		}
	}

	// Use GetRecentTrades gRPC call
	resp, err := c.rpcClient.GetRecentTrades(ctx, &gctrpc.GetSavedTradesRequest{
		Exchange: exchange,
		Pair: &gctrpc.CurrencyPair{
			Delimiter: delimiter,
			Base:      base,
			Quote:     quote,
		},
		AssetType: assetType,
	})

	if err != nil {
		// Log the error
		if c.logger != nil {
			if log, ok := c.logger.(*logrus.Logger); ok {
				log.WithFields(logrus.Fields{
					"exchange": exchange,
					"symbol":   symbol,
					"error":    err.Error(),
				}).Warn("Failed to get trades via gRPC")
			}
		}

		// For KuCoin, try REST API fallback
		if (exchange == "kucoin" || exchange == "Kucoin") && c.kucoinClient != nil {
			restSymbol := symbol
			if delimiter == "" {
				restSymbol = base + "-" + quote
			}

			if c.logger != nil {
				if log, ok := c.logger.(*logrus.Logger); ok {
					log.WithFields(logrus.Fields{
						"exchange": exchange,
						"symbol":   restSymbol,
					}).Debug("Trying KuCoin REST API fallback for trades")
				}
			}

			// KuCoin REST client may not have GetTrades, so return the gRPC error
			// TODO: Implement GetTrades in KuCoinRESTClient if needed
		}

		return nil, fmt.Errorf("failed to get trades for %s on %s: %w", symbol, exchange, err)
	}

	// Convert response to map format
	trades := make([]map[string]interface{}, 0, len(resp.Trades))
	for i, t := range resp.Trades {
		if limit > 0 && i >= limit {
			break
		}
		trade := map[string]interface{}{
			"trade_id":  t.TradeId,
			"price":     t.Price,
			"amount":    t.Amount,
			"side":      t.Side,
			"timestamp": t.Timestamp,
		}
		trades = append(trades, trade)
	}

	if c.logger != nil {
		if log, ok := c.logger.(*logrus.Logger); ok {
			log.WithFields(logrus.Fields{
				"exchange":     exchange,
				"symbol":       symbol,
				"trades_count": len(trades),
			}).Debug("Successfully retrieved trades")
		}
	}

	return trades, nil
}

// GetOHLCV retrieves OHLCV candle data
func (c *client) GetOHLCV(ctx context.Context, exchange, symbol, interval string, limit int) ([]map[string]interface{}, error) {
	// Use GetCandles and convert to map format
	candles, err := c.GetCandles(ctx, exchange, symbol, interval, limit)
	if err != nil {
		return nil, err
	}

	result := make([]map[string]interface{}, len(candles))
	for i, candle := range candles {
		result[i] = map[string]interface{}{
			"timestamp": candle.Timestamp.Unix(),
			"open":      candle.Open,
			"high":      candle.High,
			"low":       candle.Low,
			"close":     candle.Close,
			"volume":    candle.Volume,
		}
	}

	return result, nil
}

// CalculateRSI calculates RSI indicator
func (c *client) CalculateRSI(ctx context.Context, exchange, symbol string, period int) (float64, error) {
	// Fetch 3x the period so Wilder smoothing has warm-up history.
	candles, err := c.GetCandles(ctx, exchange, symbol, indicatorInterval(ctx), period*3)
	if err != nil {
		return 0, fmt.Errorf("failed to get candles for RSI: %w", err)
	}

	if len(candles) < period+1 {
		return 0, fmt.Errorf("insufficient data for RSI calculation: need at least %d candles, got %d", period+1, len(candles))
	}

	closes := make([]float64, len(candles))
	for i, candle := range candles {
		closes[i] = candle.Close
	}

	// Wilder-smoothed RSI over the full (ascending) series — the previous
	// inline loop averaged only the OLDEST half of the window, so the value
	// lagged the market by up to a full window of candles.
	return ta.CalculateRSI(closes, period), nil
}

// GetExchangeInfo retrieves exchange information via gRPC
func (c *client) GetExchangeInfo(ctx context.Context, exchange string) (map[string]interface{}, error) {
	if !c.IsConnected() {
		return nil, fmt.Errorf("not connected to GoCryptoTrader")
	}

	ctx = c.withAuth(ctx)

	// Log the request
	if c.logger != nil {
		if log, ok := c.logger.(*logrus.Logger); ok {
			log.WithFields(logrus.Fields{
				"exchange": exchange,
			}).Debug("Fetching exchange info via gRPC")
		}
	}

	// Use GetExchangeInfo gRPC call — retries up to 3 times on transient errors.
	var resp *gctrpc.GetExchangeInfoResponse
	err := withRetry(ctx, 3, func() error {
		var callErr error
		resp, callErr = c.rpcClient.GetExchangeInfo(ctx, &gctrpc.GenericExchangeNameRequest{
			Exchange: exchange,
		})
		return callErr
	})

	if err != nil {
		// Log the error
		if c.logger != nil {
			if log, ok := c.logger.(*logrus.Logger); ok {
				log.WithFields(logrus.Fields{
					"exchange": exchange,
					"error":    err.Error(),
				}).Warn("Failed to get exchange info via gRPC")
			}
		}
		return nil, fmt.Errorf("failed to get exchange info for %s: %w", exchange, err)
	}

	// Build supported assets map
	supportedAssets := make(map[string]interface{})
	for assetType, pairs := range resp.SupportedAssets {
		supportedAssets[assetType] = map[string]interface{}{
			"available_pairs": pairs.AvailablePairs,
			"enabled_pairs":   pairs.EnabledPairs,
		}
	}

	// Build result map with all available info
	result := map[string]interface{}{
		"name":              resp.Name,
		"enabled":           resp.Enabled,
		"verbose":           resp.Verbose,
		"using_sandbox":     resp.UsingSandbox,
		"http_timeout":      resp.HttpTimeout,
		"http_useragent":    resp.HttpUseragent,
		"http_proxy":        resp.HttpProxy,
		"base_currencies":   resp.BaseCurrencies,
		"supported_assets":  supportedAssets,
		"authenticated_api": resp.AuthenticatedApi,
		"status":            "online", // If we got a response, the exchange is online
	}

	if c.logger != nil {
		if log, ok := c.logger.(*logrus.Logger); ok {
			log.WithFields(logrus.Fields{
				"exchange":          exchange,
				"enabled":           resp.Enabled,
				"authenticated_api": resp.AuthenticatedApi,
			}).Debug("Successfully retrieved exchange info")
		}
	}

	return result, nil
}

// GetAllTickers retrieves all tickers for an exchange
func (c *client) GetAllTickers(ctx context.Context, exchange string) ([]map[string]interface{}, error) {
	// TODO: Implement using GoCryptoTrader gRPC
	return []map[string]interface{}{}, nil
}

// SimulateOrder simulates an order without placing it
// It validates the order parameters and calculates expected costs/outcomes
func (c *client) SimulateOrder(ctx context.Context, req *SubmitOrderRequest) (map[string]interface{}, error) {
	if req == nil {
		return nil, fmt.Errorf("order request cannot be nil")
	}

	// Validate required fields
	if req.Exchange == "" {
		return nil, fmt.Errorf("exchange is required for order simulation")
	}
	if req.Pair == "" {
		return nil, fmt.Errorf("pair/symbol is required for order simulation")
	}
	if req.Side == "" {
		return nil, fmt.Errorf("side (buy/sell) is required for order simulation")
	}
	if req.Amount <= 0 {
		return nil, fmt.Errorf("amount must be greater than 0, got: %f", req.Amount)
	}
	if req.Type == "" {
		return nil, fmt.Errorf("order type (limit/market) is required for order simulation")
	}

	// Normalize side
	side := strings.ToLower(req.Side)
	if side != "buy" && side != "sell" {
		return nil, fmt.Errorf("invalid order side: %s, must be 'buy' or 'sell'", req.Side)
	}

	// Normalize order type
	orderType := strings.ToLower(req.Type)
	if orderType != "limit" && orderType != "market" {
		return nil, fmt.Errorf("invalid order type: %s, must be 'limit' or 'market'", req.Type)
	}

	// For limit orders, price is required
	if orderType == "limit" && req.Price <= 0 {
		return nil, fmt.Errorf("price must be greater than 0 for limit orders, got: %f", req.Price)
	}

	// Log the simulation request
	if c.logger != nil {
		if log, ok := c.logger.(*logrus.Logger); ok {
			log.WithFields(logrus.Fields{
				"exchange":   req.Exchange,
				"pair":       req.Pair,
				"side":       req.Side,
				"type":       req.Type,
				"amount":     req.Amount,
				"price":      req.Price,
				"leverage":   req.Leverage,
				"marketType": req.MarketType,
			}).Info("Simulating order")
		}
	}

	// Get current market price if connected
	var currentPrice float64
	var priceSource string
	if c.IsConnected() {
		ticker, err := c.GetTicker(ctx, req.Exchange, req.Pair)
		if err != nil {
			if c.logger != nil {
				if log, ok := c.logger.(*logrus.Logger); ok {
					log.WithFields(logrus.Fields{
						"exchange": req.Exchange,
						"pair":     req.Pair,
						"error":    err.Error(),
					}).Warn("Failed to get current price for simulation, using provided price")
				}
			}
			// Use provided price or return error for market orders
			if orderType == "market" && req.Price <= 0 {
				return nil, fmt.Errorf("failed to get current market price for simulation: %w", err)
			}
			currentPrice = req.Price
			priceSource = "provided"
		} else {
			currentPrice = ticker.Last
			priceSource = "market"
		}
	} else {
		// Not connected, use provided price
		if req.Price <= 0 {
			return nil, fmt.Errorf("not connected to GoCryptoTrader and no price provided for simulation")
		}
		currentPrice = req.Price
		priceSource = "provided"
	}

	// Calculate execution price based on order type
	var executionPrice float64
	if orderType == "market" {
		executionPrice = currentPrice
	} else {
		executionPrice = req.Price
	}

	// Calculate order value
	orderValue := req.Amount * executionPrice

	// Estimate fees (typical exchange fee is 0.1% for spot, 0.05% for futures)
	var feeRate float64
	if req.MarketType == MarketTypeFutures || req.Leverage > 1 {
		feeRate = 0.0005 // 0.05% for futures
	} else {
		feeRate = 0.001 // 0.1% for spot
	}
	estimatedFee := orderValue * feeRate

	// Calculate total cost/proceeds
	var totalCost float64
	var proceeds float64
	if side == "buy" {
		totalCost = orderValue + estimatedFee
		proceeds = 0
	} else {
		totalCost = estimatedFee
		proceeds = orderValue - estimatedFee
	}

	// Calculate margin required for leveraged orders
	var marginRequired float64
	if req.Leverage > 1 {
		marginRequired = orderValue / float64(req.Leverage)
	}

	// Determine market type
	marketType := string(req.MarketType)
	if marketType == "" {
		if req.Leverage > 1 || detectAssetType(req.Pair) == "futures" {
			marketType = string(MarketTypeFutures)
		} else {
			marketType = string(MarketTypeSpot)
		}
	}

	// Build simulation result
	result := map[string]interface{}{
		"success":            true,
		"simulated":          true,
		"exchange":           req.Exchange,
		"pair":               req.Pair,
		"side":               side,
		"order_type":         orderType,
		"amount":             req.Amount,
		"execution_price":    executionPrice,
		"current_price":      currentPrice,
		"price_source":       priceSource,
		"order_value":        orderValue,
		"estimated_fee":      estimatedFee,
		"fee_rate":           feeRate,
		"total_cost":         totalCost,
		"estimated_proceeds": proceeds,
		"market_type":        marketType,
		"leverage":           req.Leverage,
		"margin_required":    marginRequired,
		"warnings":           []string{},
		"timestamp":          time.Now().Format(time.RFC3339),
	}

	// Add warnings based on order analysis
	warnings := []string{}

	// Warning for limit order price significantly different from market
	if orderType == "limit" && priceSource == "market" {
		priceDiff := (req.Price - currentPrice) / currentPrice * 100
		if side == "buy" && priceDiff > 5 {
			warnings = append(warnings, fmt.Sprintf("Buy limit price is %.2f%% above current market price", priceDiff))
		} else if side == "sell" && priceDiff < -5 {
			warnings = append(warnings, fmt.Sprintf("Sell limit price is %.2f%% below current market price", -priceDiff))
		}
	}

	// Warning for high leverage
	if req.Leverage > 20 {
		warnings = append(warnings, fmt.Sprintf("High leverage (%.0fx) increases liquidation risk significantly", req.Leverage))
	}

	// Warning for large order value
	if orderValue > 10000 {
		warnings = append(warnings, fmt.Sprintf("Large order value ($%.2f) - consider splitting into smaller orders", orderValue))
	}

	result["warnings"] = warnings

	if c.logger != nil {
		if log, ok := c.logger.(*logrus.Logger); ok {
			log.WithFields(logrus.Fields{
				"exchange":       req.Exchange,
				"pair":           req.Pair,
				"order_value":    orderValue,
				"estimated_fee":  estimatedFee,
				"warnings_count": len(warnings),
			}).Info("Order simulation completed successfully")
		}
	}

	return result, nil
}

// GetBalance retrieves balance for a specific currency
func (c *client) GetBalance(ctx context.Context, exchange, currency string) (map[string]interface{}, error) {
	// Verificar conexión
	if !c.IsConnected() {
		return nil, fmt.Errorf("not connected to GoCryptoTrader")
	}

	// Para KuCoin, usar el cliente REST directamente como fallback
	if (exchange == "kucoin" || exchange == "Kucoin") && c.kucoinClient != nil {
		if c.logger != nil {
			logger := c.logger.(*logrus.Logger)
			logger.WithFields(logrus.Fields{
				"exchange": exchange,
				"currency": currency,
			}).Debug("Using KuCoin REST client for balance")
		}

		// Obtener balance de cuenta spot
		spotBalance, spotErr := c.kucoinClient.GetSpotBalance(ctx, currency)
		if spotErr == nil && spotBalance != nil {
			if c.logger != nil {
				logger := c.logger.(*logrus.Logger)
				logger.WithFields(logrus.Fields{
					"exchange":  exchange,
					"currency":  currency,
					"total":     spotBalance["total"],
					"available": spotBalance["available"],
				}).Info("Retrieved spot balance via REST")
			}
			return spotBalance, nil
		}

		// Si falla spot, intentar con futures
		futuresBalance, futuresErr := c.kucoinClient.GetFuturesBalance(ctx, currency)
		if futuresErr == nil && futuresBalance != nil {
			if c.logger != nil {
				logger := c.logger.(*logrus.Logger)
				logger.WithFields(logrus.Fields{
					"exchange":  exchange,
					"currency":  currency,
					"total":     futuresBalance["total"],
					"available": futuresBalance["available"],
				}).Info("Retrieved futures balance via REST")
			}
			return futuresBalance, nil
		}

		// Si ambos fallan, log y continuar con gRPC
		if c.logger != nil {
			logger := c.logger.(*logrus.Logger)
			logger.WithFields(logrus.Fields{
				"exchange":   exchange,
				"currency":   currency,
				"spot_error": spotErr,
				"fut_error":  futuresErr,
			}).Warn("Failed to get balance via REST, trying gRPC")
		}
	}

	// Intentar con GetAccountInfo (gRPC)
	accountInfo, err := c.GetAccountInfo(ctx, exchange)
	if err != nil {
		if c.logger != nil {
			logger := c.logger.(*logrus.Logger)
			logger.WithFields(logrus.Fields{
				"exchange": exchange,
				"currency": currency,
				"error":    err.Error(),
			}).Error("Failed to get account info for balance")
		}

		// Si todo falla, retornar 0 en lugar de error
		if c.logger != nil {
			logger := c.logger.(*logrus.Logger)
			logger.WithFields(logrus.Fields{
				"exchange": exchange,
				"currency": currency,
			}).Warn("All methods failed, returning zero balance")
		}

		return map[string]interface{}{
			"currency":  currency,
			"total":     0.0,
			"available": 0.0,
			"in_order":  0.0,
		}, nil
	}

	// Buscar balance en la respuesta de gRPC
	for _, account := range accountInfo.Accounts {
		for _, balance := range account.Balances {
			if balance.Currency == currency {
				if c.logger != nil {
					logger := c.logger.(*logrus.Logger)
					logger.WithFields(logrus.Fields{
						"exchange":  exchange,
						"currency":  currency,
						"total":     balance.Total,
						"available": balance.Available,
					}).Debug("Found balance via gRPC")
				}

				return map[string]interface{}{
					"currency":  balance.Currency,
					"total":     balance.Total,
					"available": balance.Available,
					"in_order":  balance.InOrder,
				}, nil
			}
		}
	}

	// No encontrado, retornar 0
	if c.logger != nil {
		logger := c.logger.(*logrus.Logger)
		logger.WithFields(logrus.Fields{
			"exchange": exchange,
			"currency": currency,
		}).Info("Currency not found in account, returning zero balance")
	}

	return map[string]interface{}{
		"currency":  currency,
		"total":     0.0,
		"available": 0.0,
		"in_order":  0.0,
	}, nil
}

// CalculateMACD calculates MACD indicator
func (c *client) CalculateMACD(ctx context.Context, exchange, symbol string, fastPeriod, slowPeriod, signalPeriod int) (map[string]float64, error) {
	// Get OHLCV data
	candles, err := c.GetCandles(ctx, exchange, symbol, indicatorInterval(ctx), (slowPeriod+signalPeriod)*3)
	if err != nil {
		return nil, fmt.Errorf("failed to get candles for MACD: %w", err)
	}

	if len(candles) < slowPeriod+signalPeriod {
		return nil, fmt.Errorf("insufficient data for MACD calculation: need at least %d candles, got %d", slowPeriod+signalPeriod, len(candles))
	}

	// Extract close prices
	closes := make([]float64, len(candles))
	for i, candle := range candles {
		closes[i] = candle.Close
	}

	// Full-series MACD — the previous inline version fed a single-element
	// slice into the signal EMA, so signal==macd and histogram was always 0.
	res, err := ta.MACD(closes, fastPeriod, slowPeriod, signalPeriod)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate MACD: %w", err)
	}
	if len(res.Histogram) == 0 {
		return nil, fmt.Errorf("MACD produced no values for %s", symbol)
	}

	return map[string]float64{
		"macd":      res.MACD[len(res.MACD)-1],
		"signal":    res.Signal[len(res.Signal)-1],
		"histogram": res.Histogram[len(res.Histogram)-1],
	}, nil
}

// CalculateBollingerBands calculates Bollinger Bands
func (c *client) CalculateBollingerBands(ctx context.Context, exchange, symbol string, period int, stdDev float64) (map[string]float64, error) {
	// Get OHLCV data
	candles, err := c.GetCandles(ctx, exchange, symbol, indicatorInterval(ctx), period*2)
	if err != nil {
		return nil, fmt.Errorf("failed to get candles for Bollinger Bands: %w", err)
	}

	if len(candles) < period {
		return nil, fmt.Errorf("insufficient data for Bollinger Bands calculation: need at least %d candles, got %d", period, len(candles))
	}

	// Extract close prices
	closes := make([]float64, len(candles))
	for i, candle := range candles {
		closes[i] = candle.Close
	}

	res, err := ta.BollingerBandsCalculate(closes, period, stdDev)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate Bollinger Bands: %w", err)
	}
	if len(res.Middle) == 0 {
		return nil, fmt.Errorf("Bollinger Bands produced no values for %s", symbol)
	}

	return map[string]float64{
		"upper":  res.Upper[len(res.Upper)-1],
		"middle": res.Middle[len(res.Middle)-1],
		"lower":  res.Lower[len(res.Lower)-1],
	}, nil
}

// indicatorInterval returns the candle interval used by the exchange-level
// indicator helpers (RSI/MACD/Bollinger for MCP tools). Dynamic key
// indicators.default_interval; falls back to 1h.
func indicatorInterval(ctx context.Context) string {
	if cfg := dynamic.SnapshotFromContext(ctx); cfg != nil {
		if v := cfg.String("indicators.default_interval"); v != "" {
			return v
		}
	}
	return "1h"
}

// GetPositions retrieves positions for an exchange
func (c *client) GetPositions(ctx context.Context, exchange string) ([]map[string]interface{}, error) {
	// TODO: Implement using GoCryptoTrader gRPC
	return []map[string]interface{}{}, nil
}

// PlaceOrder places an order (alias for SubmitOrder)
func (c *client) PlaceOrder(ctx context.Context, req *SubmitOrderRequest) (*Order, error) {
	return c.SubmitOrder(ctx, req)
}

// GetGRPCConn returns the gRPC connection (for advanced usage)
func (c *client) GetGRPCConn() *grpc.ClientConn {
	return c.conn
}

// detectAssetType detects if a pair is futures or spot based on common suffixes
func detectAssetType(pair string) string {
	// Futures indicators
	futuresSuffixes := []string{
		"USDTM",     // KuCoin futures (e.g., 1000SHIB-USDTM)
		"USDT-PERP", // Perpetual futures
		"PERP",      // Generic perpetual
		"SWAP",      // Swap contracts
		"-F",        // Some exchanges use -F suffix
	}

	pairUpper := strings.ToUpper(pair)
	for _, suffix := range futuresSuffixes {
		if strings.HasSuffix(pairUpper, suffix) || strings.Contains(pairUpper, suffix) {
			return "futures"
		}
	}

	return "spot"
}

// parseCurrencyPair parses a currency pair string (e.g., "BTC-USDT", "BTCUSDT")
func parseCurrencyPair(pair string) (base, quote, delimiter string) {
	// Try common delimiters
	for _, delim := range []string{"-", "_", "/"} {
		if idx := findDelimiter(pair, delim); idx != -1 {
			return pair[:idx], pair[idx+1:], delim
		}
	}

	// No delimiter found, try to split common pairs
	// Common quote currencies
	commonQuotes := []string{"USDT", "USD", "BTC", "ETH", "BUSD", "USDC"}
	for _, quote := range commonQuotes {
		if len(pair) > len(quote) && pair[len(pair)-len(quote):] == quote {
			base := pair[:len(pair)-len(quote)]
			return base, quote, ""
		}
	}

	// Fallback: assume first 3 chars are base, rest is quote
	if len(pair) > 3 {
		return pair[:3], pair[3:], ""
	}

	return pair, "", ""
}

// findDelimiter finds the index of a delimiter in a string
func findDelimiter(s, delim string) int {
	for i := 0; i < len(s); i++ {
		if i+len(delim) <= len(s) && s[i:i+len(delim)] == delim {
			return i
		}
	}
	return -1
}

// SubmitFuturesOrder submits a futures order with automatic margin mode setup
func (c *client) SubmitFuturesOrder(ctx context.Context, req *SubmitOrderRequest) (*Order, error) {
	if c == nil {
		return nil, fmt.Errorf("client is nil")
	}
	if req == nil {
		return nil, ErrNilRequest
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// TOOL-01/HTTP-06 fix: consult the real-money risk gate before submitting.
	// This covers every futures entry point — the wrapper path (gctclient.Client
	// -> SubmitOrder redirect), the "direct REST" MCP tool
	// (submit_futures_order_direct), and the HTTP handler
	// (POST /orders/direct/futures) — since all of them ultimately call this
	// same method on the shared client instance. No-op when no RiskSentinel has
	// been wired via SetRiskSentinel.
	if err := c.evaluateOrderRisk(ctx, req, true); err != nil {
		return nil, err
	}

	// Initialize futures manager if needed
	if err := c.ensureFuturesManager(); err != nil {
		return nil, fmt.Errorf("failed to initialize futures manager: %w", err)
	}

	// Delegate to futures manager
	return c.futuresManager.PlaceFuturesOrder(ctx, req)
}

// GetFuturesPositions retrieves all open futures positions
func (c *client) GetFuturesPositions(ctx context.Context, exchange string) ([]*Position, error) {
	if c == nil {
		return nil, fmt.Errorf("client is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Initialize futures manager if needed
	if err := c.ensureFuturesManager(); err != nil {
		return nil, fmt.Errorf("failed to initialize futures manager: %w", err)
	}

	positions, err := c.futuresManager.GetPositions(ctx)
	if err != nil {
		return nil, err
	}

	// Add MarketType to all positions and optionally convert symbols
	for _, pos := range positions {
		pos.MarketType = MarketTypeFutures

		// Convert symbol from exchange format to standard format if needed
		if c.symbolConverter != nil {
			standardSymbol, err := c.symbolConverter.FromExchangeFormat(pos.Symbol, MarketTypeFutures, exchange)
			if err == nil {
				pos.Symbol = standardSymbol
			}
		}
	}

	return positions, nil
}

// GetFuturesPosition retrieves a specific futures position
func (c *client) GetFuturesPosition(ctx context.Context, exchange, symbol string) (*Position, error) {
	if c == nil {
		return nil, fmt.Errorf("client is nil")
	}
	if symbol == "" {
		return nil, fmt.Errorf("symbol is empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Initialize futures manager if needed
	if err := c.ensureFuturesManager(); err != nil {
		return nil, fmt.Errorf("failed to initialize futures manager: %w", err)
	}

	return c.futuresManager.GetPosition(ctx, symbol)
}

// CloseFuturesPosition closes a specific futures position
func (c *client) CloseFuturesPosition(ctx context.Context, exchange, symbol string) error {
	if c == nil {
		return fmt.Errorf("client is nil")
	}
	if symbol == "" {
		return fmt.Errorf("symbol is empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Initialize futures manager if needed
	if err := c.ensureFuturesManager(); err != nil {
		return fmt.Errorf("failed to initialize futures manager: %w", err)
	}

	return c.futuresManager.ClosePosition(ctx, symbol)
}

// ensureFuturesManager initializes the futures manager if not already initialized
func (c *client) ensureFuturesManager() error {
	if c == nil {
		return fmt.Errorf("client is nil")
	}

	// Already initialized
	if c.futuresManager != nil {
		return nil
	}

	if c.kucoinClient == nil {
		return fmt.Errorf("kucoin client is nil")
	}

	// Create a basic logger if not available
	// We'll use a nil logger for now, the futures manager will handle it
	var logger interface{}
	if c.logger != nil {
		logger = c.logger
	}

	// Try to create futures manager
	// We need to handle the case where logger might not be *logrus.Logger
	// For now, we'll create a simple wrapper or use nil-safe operations
	fm, err := createFuturesManagerSafe(c.kucoinClient, logger)
	if err != nil {
		return fmt.Errorf("failed to create futures manager: %w", err)
	}

	c.futuresManager = fm
	return nil
}

// createFuturesManagerSafe creates a futures manager with safe logger handling
func createFuturesManagerSafe(kucoinClient *KuCoinRESTClient, logger interface{}) (*FuturesManager, error) {
	if kucoinClient == nil {
		return nil, fmt.Errorf("kucoin client is nil")
	}

	// Try to cast logger to *logrus.Logger
	if logger == nil {
		return nil, fmt.Errorf("logger is required for futures manager - please set Config.Logger")
	}

	logrusLogger, ok := logger.(*logrus.Logger)
	if !ok {
		return nil, fmt.Errorf("logger must be *logrus.Logger, got %T", logger)
	}

	return NewFuturesManager(kucoinClient, logrusLogger)
}

// ── Wave 4-A: margin manager helpers ─────────────────────────────────────────

// SetMarginEnabled toggles the margin gate at runtime.  This is the bridge
// between the dynamic config store (bot.margin_enabled) and the client struct.
// Call it from the config subscriber whenever bot.margin_enabled changes.
func (c *client) SetMarginEnabled(enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.marginEnabled = enabled
}

// ensureMarginManager lazy-initialises the MarginManager (mirrors ensureFuturesManager).
func (c *client) ensureMarginManager() error {
	if c == nil {
		return fmt.Errorf("client is nil")
	}

	// Already initialised.
	if c.marginManager != nil {
		return nil
	}

	if c.kucoinClient == nil {
		return fmt.Errorf("kucoin client is nil — cannot initialise margin manager")
	}

	var logger interface{}
	if c.logger != nil {
		logger = c.logger
	}

	mm, err := createMarginManagerSafe(c.kucoinClient, logger)
	if err != nil {
		return fmt.Errorf("failed to create margin manager: %w", err)
	}

	c.marginManager = mm
	return nil
}

// createMarginManagerSafe mirrors createFuturesManagerSafe.
func createMarginManagerSafe(kucoinClient *KuCoinRESTClient, logger interface{}) (*MarginManager, error) {
	if kucoinClient == nil {
		return nil, fmt.Errorf("kucoin client is nil")
	}
	if logger == nil {
		return nil, fmt.Errorf("logger is required for margin manager — please set Config.Logger")
	}
	logrusLogger, ok := logger.(*logrus.Logger)
	if !ok {
		return nil, fmt.Errorf("logger must be *logrus.Logger, got %T", logger)
	}
	return NewMarginManager(kucoinClient, logrusLogger)
}

// ValidateKuCoinFuturesAccess verifies that KuCoin credentials are configured
// and that the API key has futures trading permissions enabled.
// This method should be called during initialization to detect configuration issues early.
//
// Returns:
//   - nil if credentials are valid and futures access is available
//   - error with actionable message if validation fails
func (c *client) ValidateKuCoinFuturesAccess(ctx context.Context) error {
	if c == nil {
		return fmt.Errorf("client is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Step 1: Check if KuCoin client is initialized
	if c.kucoinClient == nil {
		return fmt.Errorf("kucoin client not initialized - ensure SetKuCoinCredentials() was called")
	}

	// Step 2: Check if credentials are configured
	if !c.kucoinClient.HasCredentials() {
		return fmt.Errorf("kucoin credentials not configured - set API_KEY, API_SECRET, and API_PASSWORD environment variables")
	}

	// Step 3: Test access to futures API with a simple call
	// We'll try to get futures positions - this requires futures trading permission
	// First, ensure futures manager is initialized
	if err := c.ensureFuturesManager(); err != nil {
		// If we can't initialize futures manager, it's likely a logger issue, not credentials
		if c.logger != nil {
			if log, ok := c.logger.(*logrus.Logger); ok {
				log.Warnf("Could not initialize futures manager for validation: %v", err)
			}
		}
		return nil // Don't block startup
	}

	_, err := c.futuresManager.GetPositions(ctx)
	if err != nil {
		errStr := err.Error()

		// Check for specific error codes
		if strings.Contains(errStr, "400007") {
			// Access denied - futures trading permission not enabled
			if c.logger != nil {
				if log, ok := c.logger.(*logrus.Logger); ok {
					log.Warn("KuCoin futures trading permission not enabled on API key")
				}
			}
			return fmt.Errorf("FUTURES_PERMISSION_REQUIRED: Futures trading permission not enabled. Please enable 'Futures Trading' permission in your KuCoin API key settings at https://www.kucoin.com/account/api")
		}

		if strings.Contains(errStr, "401") || strings.Contains(errStr, "Unauthorized") {
			// Invalid credentials
			return fmt.Errorf("INVALID_CREDENTIALS: KuCoin API credentials are invalid. Please verify your API_KEY, API_SECRET, and API_PASSWORD")
		}

		if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "connection") {
			// Network error - don't fail validation, just warn
			if c.logger != nil {
				if log, ok := c.logger.(*logrus.Logger); ok {
					log.Warnf("Could not validate KuCoin futures access due to network error: %v", err)
				}
			}
			return nil // Don't block startup for network issues
		}

		// Unknown error - log but don't fail
		if c.logger != nil {
			if log, ok := c.logger.(*logrus.Logger); ok {
				log.Warnf("Could not validate KuCoin futures access: %v", err)
			}
		}
		return nil // Don't block startup for unknown errors
	}

	// Step 4: Success - log confirmation
	if c.logger != nil {
		if log, ok := c.logger.(*logrus.Logger); ok {
			log.Info("✓ KuCoin futures access validated successfully")
		}
	}

	return nil
}

// SetLeverage sets the leverage for a futures trading pair
func (c *client) SetLeverage(ctx context.Context, exchange, symbol string, leverage int) error {
	// For KuCoin, use the KuCoin REST client
	if exchange == "kucoin" && c.kucoinClient != nil {
		return c.kucoinClient.SetLeverage(ctx, symbol, leverage)
	}

	// For other exchanges, this would need to be implemented
	return fmt.Errorf("SetLeverage not implemented for exchange: %s", exchange)
}

// ConvertSymbol converts a symbol between standard and exchange-specific formats
// This method is used by MCP tools to convert symbols based on market type
func (c *client) ConvertSymbol(symbol string, marketType MarketType, exchange string) (string, error) {
	if c.symbolConverter == nil {
		return symbol, fmt.Errorf("symbol converter not initialized")
	}
	return c.symbolConverter.ToExchangeFormat(symbol, marketType, exchange)
}

// GetPortfolioOverview returns a comprehensive portfolio snapshot including spot wallet,
// futures wallet, open positions with risk metrics, and bot decision context.
func (c *client) GetPortfolioOverview(ctx context.Context, exchange string) (*PortfolioOverview, error) {
	if c == nil {
		return nil, fmt.Errorf("client is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if c.kucoinClient == nil {
		return nil, fmt.Errorf("kucoin client not available")
	}
	return c.kucoinClient.GetFullPortfolioOverview(ctx)
}
