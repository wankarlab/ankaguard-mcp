// Package handlers contains HTTP handlers for REST API v1
package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/trading/middleware"
	"github.com/sirupsen/logrus"
)

// MarketHandler handles market data endpoints
type MarketHandler struct {
	gctClient *gctclient.Client
	logger    *logrus.Logger
}

// NewMarketHandler creates a new market handler
func NewMarketHandler(gctClient *gctclient.Client, logger *logrus.Logger) *MarketHandler {
	return &MarketHandler{
		gctClient: gctClient,
		logger:    logger,
	}
}

// GetTicker handles GET /api/v1/market/ticker/:pair
// Returns ticker data for a trading pair
func (h *MarketHandler) GetTicker(c *gin.Context) {
	userID, exists := middleware.GetUserIDFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	pair := c.Param("pair")
	if pair == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_REQUEST", "Trading pair is required"))
		return
	}

	exchange := c.Query("exchange")
	if exchange == "" {
		exchange = "kucoin" // Default exchange
	}

	// Normalize pair format (e.g., BTC-USDT or BTCUSDT)
	pair = normalizePair(pair)

	h.logger.WithFields(logrus.Fields{
		"user_id":  userID,
		"exchange": exchange,
		"pair":     pair,
	}).Debug("Getting ticker")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	ticker, err := h.gctClient.GetTicker(ctx, exchange, pair)
	if err != nil {
		h.logger.WithError(err).Error("Failed to get ticker")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponseWithDetails("TICKER_ERROR", "Failed to retrieve ticker", err.Error()))
		return
	}

	// Calculate change percentage if we have open price
	changePercent := 0.0
	if ticker.High24h > 0 && ticker.Last > 0 {
		// Estimate from high/low if we don't have open
		midPoint := (ticker.High24h + ticker.Low24h) / 2
		if midPoint > 0 {
			changePercent = ((ticker.Last - midPoint) / midPoint) * 100
		}
	}

	response := models.TickerResponse{
		Exchange:         ticker.Exchange,
		Pair:             ticker.Pair,
		Last:             ticker.Last,
		Bid:              ticker.Bid,
		Ask:              ticker.Ask,
		High24h:          ticker.High24h,
		Low24h:           ticker.Low24h,
		Volume:           ticker.Volume,
		Change24h:        ticker.Change24h,
		ChangePercent24h: changePercent,
		MarkPrice:        ticker.MarkPrice,
		IndexPrice:       ticker.IndexPrice,
		Timestamp:        ticker.Timestamp,
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(response))
}

// GetOrderbook handles GET /api/v1/market/orderbook/:pair
// Returns orderbook data for a trading pair
func (h *MarketHandler) GetOrderbook(c *gin.Context) {
	userID, exists := middleware.GetUserIDFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	pair := c.Param("pair")
	if pair == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_REQUEST", "Trading pair is required"))
		return
	}

	exchange := c.Query("exchange")
	if exchange == "" {
		exchange = "kucoin" // Default exchange
	}

	depth := 20 // Default depth
	if d := c.Query("depth"); d != "" {
		// Parse depth from query
		var parsed int
		if _, err := c.GetQuery("depth"); err {
			if n, parseErr := parseInt(d); parseErr == nil && n > 0 && n <= 100 {
				depth = n
			}
		}
		_ = parsed // Avoid unused variable error
	}

	// Normalize pair format
	pair = normalizePair(pair)

	h.logger.WithFields(logrus.Fields{
		"user_id":  userID,
		"exchange": exchange,
		"pair":     pair,
		"depth":    depth,
	}).Debug("Getting orderbook")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	orderbook, err := h.gctClient.GetOrderbook(ctx, exchange, pair, depth)
	if err != nil {
		h.logger.WithError(err).Error("Failed to get orderbook")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponseWithDetails("ORDERBOOK_ERROR", "Failed to retrieve orderbook", err.Error()))
		return
	}

	// Convert to response format
	bids := make([]models.OrderbookItem, 0, len(orderbook.Bids))
	asks := make([]models.OrderbookItem, 0, len(orderbook.Asks))
	cumBidTotal := 0.0
	cumAskTotal := 0.0

	for _, bid := range orderbook.Bids {
		cumBidTotal += bid.Amount * bid.Price
		bids = append(bids, models.OrderbookItem{
			Price:  bid.Price,
			Amount: bid.Amount,
			Total:  cumBidTotal,
		})
	}

	for _, ask := range orderbook.Asks {
		cumAskTotal += ask.Amount * ask.Price
		asks = append(asks, models.OrderbookItem{
			Price:  ask.Price,
			Amount: ask.Amount,
			Total:  cumAskTotal,
		})
	}

	// Calculate spread
	spread := 0.0
	spreadPercent := 0.0
	if len(bids) > 0 && len(asks) > 0 {
		bestBid := bids[0].Price
		bestAsk := asks[0].Price
		spread = bestAsk - bestBid
		if bestBid > 0 {
			spreadPercent = (spread / bestBid) * 100
		}
	}

	response := models.OrderbookResponse{
		Exchange:      orderbook.Exchange,
		Pair:          orderbook.Pair,
		Bids:          bids,
		Asks:          asks,
		Spread:        spread,
		SpreadPercent: spreadPercent,
		Timestamp:     orderbook.Timestamp,
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(response))
}

// GetCandles handles GET /api/v1/market/candles/:pair
// Returns OHLCV candle data for a trading pair
func (h *MarketHandler) GetCandles(c *gin.Context) {
	userID, exists := middleware.GetUserIDFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	pair := c.Param("pair")
	if pair == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_REQUEST", "Trading pair is required"))
		return
	}

	exchange := c.Query("exchange")
	if exchange == "" {
		exchange = "kucoin"
	}

	interval := c.DefaultQuery("interval", "1h")
	limit := 100
	if l := c.Query("limit"); l != "" {
		if n, err := parseInt(l); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}

	// Normalize pair format
	pair = normalizePair(pair)

	h.logger.WithFields(logrus.Fields{
		"user_id":  userID,
		"exchange": exchange,
		"pair":     pair,
		"interval": interval,
		"limit":    limit,
	}).Debug("Getting candles")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	candles, err := h.gctClient.GetCandles(ctx, exchange, pair, interval, limit)
	if err != nil {
		h.logger.WithError(err).Error("Failed to get candles")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponseWithDetails("CANDLES_ERROR", "Failed to retrieve candles", err.Error()))
		return
	}

	// Convert to response format
	candleResponse := make([]gin.H, 0, len(candles))
	for _, candle := range candles {
		candleResponse = append(candleResponse, gin.H{
			"timestamp": candle.Timestamp,
			"open":      candle.Open,
			"high":      candle.High,
			"low":       candle.Low,
			"close":     candle.Close,
			"volume":    candle.Volume,
		})
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"exchange": exchange,
		"pair":     pair,
		"interval": interval,
		"candles":  candleResponse,
	}))
}

// GetTrades handles GET /api/v1/market/trades/:pair
// Returns recent trades for a trading pair
func (h *MarketHandler) GetTrades(c *gin.Context) {
	userID, exists := middleware.GetUserIDFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	pair := c.Param("pair")
	if pair == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_REQUEST", "Trading pair is required"))
		return
	}

	exchange := c.Query("exchange")
	if exchange == "" {
		exchange = "kucoin"
	}

	limit := 50
	if l := c.Query("limit"); l != "" {
		if n, err := parseInt(l); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	// Normalize pair format
	pair = normalizePair(pair)

	h.logger.WithFields(logrus.Fields{
		"user_id":  userID,
		"exchange": exchange,
		"pair":     pair,
		"limit":    limit,
	}).Debug("Getting trades")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	trades, err := h.gctClient.GetRecentTrades(ctx, exchange, pair, limit)
	if err != nil {
		h.logger.WithError(err).Error("Failed to get trades")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponseWithDetails("TRADES_ERROR", "Failed to retrieve trades", err.Error()))
		return
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"exchange": exchange,
		"pair":     pair,
		"trades":   trades,
	}))
}

// normalizePair normalizes trading pair format
// Handles formats like: BTC-USDT, BTCUSDT, btc/usdt
func normalizePair(pair string) string {
	pair = strings.ToUpper(pair)
	pair = strings.ReplaceAll(pair, "/", "-")
	pair = strings.ReplaceAll(pair, "_", "-")
	return pair
}

// parseInt parses an integer from a string
func parseInt(s string) (int, error) {
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, nil
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}
