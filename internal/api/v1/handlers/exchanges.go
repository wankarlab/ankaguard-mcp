// Package handlers contains HTTP handlers for REST API v1
package handlers

import (
	"context"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	exch "github.com/rantipay/trading-mcp/internal/exchange"
	"github.com/sirupsen/logrus"
)

// ExchangesHandler exposes multi-exchange spread and pricing endpoints (DM-5.4).
type ExchangesHandler struct {
	router exch.ExchangeRouter
	logger *logrus.Logger
}

// NewExchangesHandler creates a new ExchangesHandler.
func NewExchangesHandler(router exch.ExchangeRouter, logger *logrus.Logger) *ExchangesHandler {
	return &ExchangesHandler{
		router: router,
		logger: logger,
	}
}

// GetPriceMatrix handles GET /api/v1/exchanges/price-matrix/:pair
// Returns the live price quotes from all active exchanges for a given pair.
func (h *ExchangesHandler) GetPriceMatrix(c *gin.Context) {
	pair := c.Param("pair")
	if pair == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("MISSING_PAIR", "pair parameter required"))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	matrix, err := h.router.PriceMatrix(ctx, pair)
	if err != nil {
		h.logger.WithError(err).WithField("pair", pair).Error("Failed to get price matrix")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponseWithDetails(
			"PRICE_MATRIX_ERROR", "Failed to retrieve price matrix", err.Error(),
		))
		return
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"pair":   pair,
		"venues": matrix,
		"count":  len(matrix),
	}))
}

// spreadResult holds the spread info for a single pair.
type spreadResult struct {
	Pair         string  `json:"pair"`
	SpreadPct    float64 `json:"spread_pct"`
	VenuesCount  int     `json:"venues_count"`
	BestBid      float64 `json:"best_bid"`
	BestAsk      float64 `json:"best_ask"`
}

// GetSpreads handles GET /api/v1/exchanges/spreads
// Returns the bid-ask spread across venues for requested pairs (comma-separated).
// Query param: ?pairs=BTC-USDT,ETH-USDT
func (h *ExchangesHandler) GetSpreads(c *gin.Context) {
	pairsParam := c.DefaultQuery("pairs", "")
	if pairsParam == "" {
		c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
			"spreads": []interface{}{},
			"note":    "provide ?pairs=BTC-USDT,ETH-USDT",
		}))
		return
	}

	pairs := strings.Split(pairsParam, ",")
	spreads := make([]spreadResult, 0, len(pairs))

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}

		matrix, err := h.router.PriceMatrix(ctx, pair)
		if err != nil {
			// Be resilient: log error and skip this pair
			h.logger.WithError(err).WithField("pair", pair).Debug("Failed to get matrix for pair, skipping")
			continue
		}

		if len(matrix) == 0 {
			// No venues have a quote for this pair
			continue
		}

		// Find min Last and max Last across all venues
		minLast := math.MaxFloat64
		maxLast := 0.0
		bestBid := 0.0
		bestAsk := math.MaxFloat64

		for _, quote := range matrix {
			if quote.Last < minLast {
				minLast = quote.Last
			}
			if quote.Last > maxLast {
				maxLast = quote.Last
			}
			if quote.Bid > bestBid {
				bestBid = quote.Bid
			}
			if quote.Ask < bestAsk {
				bestAsk = quote.Ask
			}
		}

		// Compute spread as (max - min) / min * 100
		var spreadPct float64
		if minLast > 0 {
			spreadPct = ((maxLast - minLast) / minLast) * 100
		}

		spreads = append(spreads, spreadResult{
			Pair:        pair,
			SpreadPct:   spreadPct,
			VenuesCount: len(matrix),
			BestBid:     bestBid,
			BestAsk:     bestAsk,
		})
	}

	// Sort by spread descending
	// Simple bubble sort since we expect only a few pairs
	for i := 0; i < len(spreads); i++ {
		for j := i + 1; j < len(spreads); j++ {
			if spreads[j].SpreadPct > spreads[i].SpreadPct {
				spreads[i], spreads[j] = spreads[j], spreads[i]
			}
		}
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"spreads": spreads,
	}))
}

// GetExchanges handles GET /api/v1/exchanges.
// Returns all active exchanges with credential validation status (EX-3).
func (h *ExchangesHandler) GetExchanges(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()

	active, err := h.router.ActiveExchanges(ctx)
	if err != nil {
		h.logger.WithError(err).Error("Failed to list active exchanges")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("EXCHANGE_LIST_ERROR", err.Error()))
		return
	}

	type exchangeInfo struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	out := make([]exchangeInfo, 0, len(active))
	for _, ex := range active {
		out = append(out, exchangeInfo{Name: ex, Status: "active"})
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"exchanges": out,
		"count":     len(out),
	}))
}

// GetExchangeStatus handles GET /api/v1/exchanges/:exchange/status (EX-3).
// Returns connectivity + credential status for a named exchange.
func (h *ExchangesHandler) GetExchangeStatus(c *gin.Context) {
	exchange := strings.ToLower(c.Param("exchange"))
	if exchange == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("VALIDATION_FAILED", "exchange is required"))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()

	active, err := h.router.ActiveExchanges(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("EXCHANGE_STATUS_ERROR", err.Error()))
		return
	}

	status := "inactive"
	for _, ex := range active {
		if strings.EqualFold(ex, exchange) {
			status = "active"
			break
		}
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"exchange": exchange,
		"status":   status,
	}))
}
