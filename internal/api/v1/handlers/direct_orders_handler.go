// Package handlers contains HTTP handlers for REST API v1.
//
// direct_orders_handler.go exposes the two DIRECT-order MCP tools
// (close_position_direct / submit_futures_order_direct) over REST. These talk
// to KuCoin through the direct REST client obtained via gctClient.GetBaseClient()
// instead of the GoCryptoTrader gRPC path, so they work for ANY futures contract
// — including positions opened manually or on contracts GCT has not enabled
// (which otherwise fail with "currency not enabled"). The live scalping engine
// already places SL/TP through this same direct client.
package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/killswitch"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/sirupsen/logrus"
)

// DirectOrdersHandler handles the direct-REST order endpoints that bypass
// GoCryptoTrader's enabled-currency restriction.
type DirectOrdersHandler struct {
	gctClient *gctclient.Client
	logger    *logrus.Logger
}

// NewDirectOrdersHandler creates a new direct-orders handler.
func NewDirectOrdersHandler(client *gctclient.Client, logger *logrus.Logger) *DirectOrdersHandler {
	if logger == nil {
		logger = logrus.New()
	}
	return &DirectOrdersHandler{gctClient: client, logger: logger}
}

// toFuturesSymbol normalises a pair/symbol to KuCoin's futures format
// (e.g. "TIA-USDT" -> "TIAUSDTM", "TIAUSDTM" -> "TIAUSDTM"). Replicated from the
// orders tools package on purpose: importing that package here would create an
// import cycle.
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

// deterministicClientOrderID mirrors the orders package helper so REST-submitted
// orders carry an idempotent client order ID (audit P0a). Replicated locally to
// avoid the import cycle with the orders tools package.
//
// The hash body and "mcp-" prefix are IDENTICAL to the MCP tool helper on
// purpose: an identical order placed via the MCP tool and via this REST endpoint
// within the same minute bucket produces the SAME clientOid, so the exchange
// dedups across channels instead of opening two positions.
func deterministicClientOrderID(exchange, pair, side, orderType string, amount, price float64) string {
	minute := time.Now().UTC().Truncate(time.Minute).Unix()
	raw := fmt.Sprintf("%s|%s|%s|%s|%.8f|%.8f|%d", exchange, pair, side, orderType, amount, price, minute)
	sum := sha256.Sum256([]byte(raw))
	return "mcp-" + hex.EncodeToString(sum[:])[:16]
}

// closePositionDirectRequest is the JSON body for ClosePositionDirect.
type closePositionDirectRequest struct {
	Exchange string `json:"exchange"`
	Symbol   string `json:"symbol" binding:"required"`
}

// ClosePositionDirect handles POST /api/v1/orders/direct/close.
// It reads the existing futures position to learn its exact size and side, then
// closes it at market via the direct REST client.
func (h *DirectOrdersHandler) ClosePositionDirect(c *gin.Context) {
	var req closePositionDirectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponseWithDetails(
			"INVALID_REQUEST", "Invalid close request", err.Error()))
		return
	}

	exchange := req.Exchange
	if exchange == "" {
		exchange = "kucoin"
	}
	symbol := toFuturesSymbol(req.Symbol)
	if symbol == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_REQUEST", "symbol is required"))
		return
	}

	base := h.gctClient.GetBaseClient()
	if base == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse(
			"SERVICE_UNAVAILABLE", "direct exchange client unavailable"))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	pos, err := base.GetFuturesPosition(ctx, exchange, symbol)
	if err != nil {
		c.JSON(http.StatusBadGateway, models.NewErrorResponseWithDetails(
			"POSITION_READ_FAILED",
			fmt.Sprintf("failed to read position for %s", symbol),
			fmt.Errorf("read position %s: %w", symbol, err).Error()))
		return
	}
	if pos == nil || pos.Size == 0 {
		c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
			"success": false,
			"symbol":  symbol,
			"message": "no open position to close",
		}))
		return
	}

	if err := base.CloseFuturesPosition(ctx, exchange, symbol); err != nil {
		c.JSON(http.StatusBadGateway, models.NewErrorResponseWithDetails(
			"CLOSE_FAILED",
			fmt.Sprintf("failed to close position %s", symbol),
			fmt.Errorf("close position %s: %w", symbol, err).Error()))
		return
	}

	h.logger.WithFields(logrus.Fields{
		"exchange": exchange,
		"symbol":   symbol,
		"side":     pos.Side,
		"size":     pos.Size,
	}).Info("Closed position via direct REST")

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"success":        true,
		"symbol":         symbol,
		"closed_side":    pos.Side,
		"closed_size":    pos.Size,
		"entry_price":    pos.EntryPrice,
		"last_price":     pos.CurrentPrice,
		"unrealized_pnl": pos.UnrealizedPnL,
		"message": fmt.Sprintf("closed %s %s (%.0f contracts) at market via direct REST",
			pos.Side, symbol, pos.Size),
		"client_via": "direct-rest",
	}))
}

// submitFuturesOrderDirectRequest is the JSON body for SubmitFuturesOrderDirect.
type submitFuturesOrderDirectRequest struct {
	Exchange      string  `json:"exchange"`
	Pair          string  `json:"pair" binding:"required"`
	Side          string  `json:"side" binding:"required,oneof=buy sell"`
	Type          string  `json:"type" binding:"omitempty,oneof=market limit stop"`
	Amount        float64 `json:"amount" binding:"required,gt=0"`
	Price         float64 `json:"price"`
	StopPrice     float64 `json:"stop_price"`
	StopPriceType string  `json:"stop_price_type" binding:"omitempty,oneof=TP MP"`
	ReduceOnly    bool    `json:"reduce_only"`
	CloseOrder    bool    `json:"close_order"`
	Leverage      float64 `json:"leverage"`
}

// SubmitFuturesOrderDirect handles POST /api/v1/orders/direct/futures.
// It submits a futures order (market/limit/stop) via the direct REST client,
// bypassing GoCryptoTrader's enabled-currency restriction.
func (h *DirectOrdersHandler) SubmitFuturesOrderDirect(c *gin.Context) {
	var req submitFuturesOrderDirectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponseWithDetails(
			"INVALID_REQUEST", "Invalid futures order request", err.Error()))
		return
	}

	// Ring 0: only de-risking orders (reduce-only / close) may pass.
	if killswitch.Ring0Active() && !req.ReduceOnly && !req.CloseOrder {
		c.JSON(http.StatusLocked, models.NewErrorResponse(killswitch.ErrorCode, killswitch.ErrActive.Error()))
		return
	}

	exchange := req.Exchange
	if exchange == "" {
		exchange = "kucoin"
	}
	orderType := req.Type
	if orderType == "" {
		orderType = "market"
	}

	if orderType == "limit" && req.Price <= 0 {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse(
			"INVALID_REQUEST", "price is required for limit orders"))
		return
	}
	if orderType == "stop" && req.StopPrice <= 0 {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse(
			"INVALID_REQUEST", "stop_price is required for stop orders"))
		return
	}

	base := h.gctClient.GetBaseClient()
	if base == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse(
			"SERVICE_UNAVAILABLE", "direct exchange client unavailable"))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	orderReq := &gct.SubmitOrderRequest{
		Exchange:      exchange,
		Pair:          req.Pair,
		Side:          req.Side,
		Type:          orderType,
		Amount:        req.Amount,
		Price:         req.Price,
		StopPrice:     req.StopPrice,
		StopPriceType: req.StopPriceType,
		ReduceOnly:    req.ReduceOnly,
		CloseOrder:    req.CloseOrder,
		Leverage:      req.Leverage,
		ClientID:      deterministicClientOrderID(exchange, req.Pair, req.Side, orderType, req.Amount, req.Price),
		MarketType:    gct.MarketTypeFutures,
	}

	order, err := base.SubmitFuturesOrder(ctx, orderReq)
	if err != nil {
		c.JSON(http.StatusBadGateway, models.NewErrorResponseWithDetails(
			"ORDER_SUBMIT_FAILED",
			fmt.Sprintf("failed to submit %s futures order for %s", orderType, req.Pair),
			fmt.Errorf("submit %s futures order for %s: %w", orderType, req.Pair, err).Error()))
		return
	}

	h.logger.WithFields(logrus.Fields{
		"exchange": exchange,
		"pair":     req.Pair,
		"side":     req.Side,
		"type":     orderType,
		"amount":   req.Amount,
	}).Info("Submitted futures order via direct REST")

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"success":    true,
		"order":      order,
		"client_via": "direct-rest",
	}))
}
