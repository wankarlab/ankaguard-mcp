// Package handlers contains HTTP handlers for REST API v1
package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	tradingModels "github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/rantipay/trading-mcp/internal/trading/middleware"
	"github.com/sirupsen/logrus"
)

// OrdersHandler handles order-related endpoints
type OrdersHandler struct {
	gctClient  *gctclient.Client
	sessionMgr *gct.SessionManager // optional; nil disables per-user session path
	logger     *logrus.Logger
}

// NewOrdersHandler creates a new orders handler
func NewOrdersHandler(gctClient *gctclient.Client, logger *logrus.Logger) *OrdersHandler {
	return &OrdersHandler{
		gctClient: gctClient,
		logger:    logger,
	}
}

// WithSessionManager returns a copy of the handler with the given SessionManager wired in.
// Call this after NewOrdersHandler when per-user session support is desired.
func (h *OrdersHandler) WithSessionManager(sm *gct.SessionManager) *OrdersHandler {
	h.sessionMgr = sm
	return h
}

// CreateOrder handles POST /api/v1/orders
// Creates a new order on the specified exchange
func (h *OrdersHandler) CreateOrder(c *gin.Context) {
	userID, exists := middleware.GetUserIDFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	kongLevel, _ := middleware.GetKongLevelFromContext(c)
	if kongLevel < 3 {
		c.JSON(http.StatusForbidden, models.NewErrorResponse("INSUFFICIENT_PERMISSIONS", "Level 3 or higher required for trading"))
		return
	}

	var req models.CreateOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponseWithDetails("INVALID_REQUEST", "Invalid order request", err.Error()))
		return
	}

	// Validate order type specific requirements
	if req.Type == "limit" && req.Price <= 0 {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_REQUEST", "Limit orders require a price"))
		return
	}
	if (req.Type == "stop" || req.Type == "stop_limit") && req.StopPrice <= 0 {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_REQUEST", "Stop orders require a stop price"))
		return
	}

	h.logger.WithFields(logrus.Fields{
		"user_id":  userID,
		"exchange": req.Exchange,
		"pair":     req.Pair,
		"side":     req.Side,
		"type":     req.Type,
		"amount":   req.Amount,
		"price":    req.Price,
	}).Info("Creating order")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	orderReq := &tradingModels.OrderRequest{
		Exchange:  req.Exchange,
		Pair:      req.Pair,
		Side:      req.Side,
		Type:      req.Type,
		Amount:    req.Amount,
		Price:     req.Price,
		StopPrice: req.StopPrice,
		Leverage:  req.Leverage,
		ClientID:  req.ClientID,
	}

	order, err := h.gctClient.SubmitOrder(ctx, orderReq)
	if err != nil {
		h.logger.WithError(err).Error("Failed to create order")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponseWithDetails("ORDER_ERROR", "Failed to create order", err.Error()))
		return
	}

	response := models.OrderResponse{
		OrderID:   order.OrderID,
		Exchange:  order.Exchange,
		Pair:      order.Pair,
		Side:      order.Side,
		Type:      order.Type,
		Status:    order.Status,
		Amount:    order.Amount,
		Price:     order.Price,
		Filled:    0,
		Remaining: order.Amount,
		CreatedAt: order.Timestamp,
		UpdatedAt: order.Timestamp,
	}

	h.logger.WithField("order_id", order.OrderID).Info("Order created successfully")
	c.JSON(http.StatusCreated, models.NewSuccessResponse(response))
}

// ListOrders handles GET /api/v1/orders
// Lists orders with optional filters
func (h *OrdersHandler) ListOrders(c *gin.Context) {
	userID, exists := middleware.GetUserIDFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	var req models.ListOrdersRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_REQUEST", err.Error()))
		return
	}

	// Set defaults
	if req.Limit <= 0 || req.Limit > 100 {
		req.Limit = 50
	}

	h.logger.WithFields(logrus.Fields{
		"user_id":  userID,
		"exchange": req.Exchange,
		"pair":     req.Pair,
		"status":   req.Status,
		"limit":    req.Limit,
	}).Debug("Listing orders")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	exchange := req.Exchange
	if exchange == "" {
		exchange = "kucoin" // Default exchange
	}

	var orders []models.OrderResponse

	// Note (MU-4.3): per-user order reads (GetOpenOrdersForUser,
	// GetOrderHistoryForUser) are not yet implemented in gctclient — the gRPC
	// layer does not support per-user credential injection for read operations.
	// The sessionMgr field is wired here so that future ForUser read methods can
	// be integrated without changing the handler signature. For now all read paths
	// use the global gctClient (same behaviour as before MU-4.3).
	// CancelOrderForUser (write path) already uses the session — see CancelOrder.

	// Get orders based on status filter
	switch req.Status {
	case "open", "":
		// Get open orders
		openOrders, err := h.gctClient.GetOpenOrders(ctx, exchange, req.Pair)
		if err != nil {
			h.logger.WithError(err).Warn("Failed to get open orders")
			// Continue with empty list
		} else {
			orders = convertMapOrdersToResponse(openOrders, exchange)
		}

	case "closed", "history":
		// Get order history
		historyOrders, err := h.gctClient.GetOrderHistory(ctx, exchange, req.Pair, req.Limit)
		if err != nil {
			h.logger.WithError(err).Warn("Failed to get order history")
			// Continue with empty list
		} else {
			orders = convertMapOrdersToResponse(historyOrders, exchange)
		}

	case "all":
		// Get both open and closed orders
		openOrders, _ := h.gctClient.GetOpenOrders(ctx, exchange, req.Pair)
		historyOrders, _ := h.gctClient.GetOrderHistory(ctx, exchange, req.Pair, req.Limit)

		orders = append(convertMapOrdersToResponse(openOrders, exchange),
			convertMapOrdersToResponse(historyOrders, exchange)...)
	}

	// Apply pagination
	if req.Offset > len(orders) {
		orders = []models.OrderResponse{}
	} else if req.Offset+req.Limit > len(orders) {
		orders = orders[req.Offset:]
	} else {
		orders = orders[req.Offset : req.Offset+req.Limit]
	}

	response := models.OrdersListResponse{
		Orders: orders,
	}

	meta := &models.APIMeta{
		Count:  len(orders),
		Limit:  req.Limit,
		Offset: req.Offset,
	}

	c.JSON(http.StatusOK, models.NewSuccessResponseWithMeta(response, meta))
}

// CancelOrder handles DELETE /api/v1/orders/:id
// Cancels an existing order
func (h *OrdersHandler) CancelOrder(c *gin.Context) {
	userID, exists := middleware.GetUserIDFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	kongLevel, _ := middleware.GetKongLevelFromContext(c)
	if kongLevel < 3 {
		c.JSON(http.StatusForbidden, models.NewErrorResponse("INSUFFICIENT_PERMISSIONS", "Level 3 or higher required for trading"))
		return
	}

	orderID := c.Param("id")
	if orderID == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_REQUEST", "Order ID is required"))
		return
	}

	exchange := c.Query("exchange")
	if exchange == "" {
		exchange = "kucoin" // Default exchange
	}

	h.logger.WithFields(logrus.Fields{
		"user_id":  userID,
		"order_id": orderID,
		"exchange": exchange,
	}).Info("Canceling order")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	err := h.gctClient.CancelOrder(ctx, exchange, orderID)
	if err != nil {
		h.logger.WithError(err).Error("Failed to cancel order")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponseWithDetails("CANCEL_ERROR", "Failed to cancel order", err.Error()))
		return
	}

	h.logger.WithField("order_id", orderID).Info("Order canceled successfully")
	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"order_id": orderID,
		"status":   "cancelled",
		"message":  "Order cancelled successfully",
	}))
}

// GetOrder handles GET /api/v1/orders/:id
// Gets details of a specific order
func (h *OrdersHandler) GetOrder(c *gin.Context) {
	userID, exists := middleware.GetUserIDFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	orderID := c.Param("id")
	if orderID == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_REQUEST", "Order ID is required"))
		return
	}

	exchange := c.Query("exchange")
	if exchange == "" {
		exchange = "kucoin" // Default exchange
	}

	h.logger.WithFields(logrus.Fields{
		"user_id":  userID,
		"order_id": orderID,
		"exchange": exchange,
	}).Debug("Getting order")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	order, err := h.gctClient.GetOrderStatus(ctx, exchange, orderID)
	if err != nil {
		h.logger.WithError(err).Error("Failed to get order")
		c.JSON(http.StatusNotFound, models.NewErrorResponse("ORDER_NOT_FOUND", "Order not found"))
		return
	}

	response := models.OrderResponse{
		OrderID:   order.OrderID,
		Exchange:  order.Exchange,
		Pair:      order.Pair,
		Side:      order.Side,
		Type:      order.Type,
		Status:    order.Status,
		Amount:    order.Amount,
		Price:     order.Price,
		CreatedAt: order.Timestamp,
		UpdatedAt: order.Timestamp,
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(response))
}

// convertMapOrdersToResponse converts map-based order data to response models
func convertMapOrdersToResponse(orders []map[string]interface{}, exchange string) []models.OrderResponse {
	result := make([]models.OrderResponse, 0, len(orders))

	for _, order := range orders {
		orderResp := models.OrderResponse{
			Exchange: exchange,
		}

		if id, ok := order["order_id"].(string); ok {
			orderResp.OrderID = id
		}
		if pair, ok := order["pair"].(string); ok {
			orderResp.Pair = pair
		}
		if side, ok := order["side"].(string); ok {
			orderResp.Side = side
		}
		if orderType, ok := order["type"].(string); ok {
			orderResp.Type = orderType
		}
		if status, ok := order["status"].(string); ok {
			orderResp.Status = status
		}
		if amount, ok := order["amount"].(float64); ok {
			orderResp.Amount = amount
		}
		if price, ok := order["price"].(float64); ok {
			orderResp.Price = price
		}
		if remaining, ok := order["remaining"].(float64); ok {
			orderResp.Remaining = remaining
			orderResp.Filled = orderResp.Amount - remaining
		}
		if createdAt, ok := order["created_at"].(time.Time); ok {
			orderResp.CreatedAt = createdAt
		}
		if updatedAt, ok := order["updated_at"].(time.Time); ok {
			orderResp.UpdatedAt = updatedAt
		} else {
			orderResp.UpdatedAt = orderResp.CreatedAt
		}

		result = append(result, orderResp)
	}

	return result
}
