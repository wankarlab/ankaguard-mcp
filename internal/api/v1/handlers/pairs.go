// Package handlers contains HTTP handlers for REST API v1
package handlers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/storage"
	"github.com/sirupsen/logrus"
)

// RankedPairEntry is a single entry returned by GET /api/v1/pairs/ranked.
type RankedPairEntry struct {
	Pair     string  `json:"pair"`
	Exchange string  `json:"exchange,omitempty"`
	Score    float64 `json:"score"`
}

// RankedPairProvider is the minimal interface the PairsHandler needs to serve
// the /ranked endpoint. *masterPairProvider (main.go) satisfies this.
type RankedPairProvider interface {
	// TopRankedPairs returns pairs sorted by score descending, limited to n entries.
	// n <= 0 means "return all available".
	TopRankedPairs(n int) []RankedPairEntry
}

// PairsHandler exposes user pair selection endpoints (DM-2.3).
type PairsHandler struct {
	gctClient    *gctclient.Client
	repo         *storage.UserPairSelectionRepository
	assetType    string // e.g. "futures"
	logger       *logrus.Logger
	rankProvider RankedPairProvider // optional; nil → empty ranked list
}

// NewPairsHandler creates a new PairsHandler.
func NewPairsHandler(
	gctClient *gctclient.Client,
	repo *storage.UserPairSelectionRepository,
	assetType string,
	logger *logrus.Logger,
) *PairsHandler {
	if assetType == "" {
		assetType = "futures"
	}
	return &PairsHandler{
		gctClient: gctClient,
		repo:      repo,
		assetType: assetType,
		logger:    logger,
	}
}

// SetRankedPairProvider wires the provider used by GetRankedPairs. Safe to call
// with nil to revert to the empty-stub behaviour. Thread-safe after construction
// (call before the router starts serving requests).
func (h *PairsHandler) SetRankedPairProvider(p RankedPairProvider) {
	h.rankProvider = p
}

// validModes is the set of accepted mode strings.
var validModes = map[string]bool{
	"auto":   true,
	"manual": true,
	"hybrid": true,
}

// GetAvailablePairs handles GET /api/v1/pairs/available
// Returns the live tradable pairs from the exchange.
func (h *PairsHandler) GetAvailablePairs(c *gin.Context) {
	exchange := c.DefaultQuery("exchange", "kucoin")
	assetType := c.DefaultQuery("asset_type", h.assetType)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	pairs, err := h.gctClient.GetTradablePairs(ctx, exchange, assetType)
	if err != nil {
		h.logger.WithError(err).Error("Failed to get tradable pairs")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponseWithDetails(
			"PAIRS_ERROR", "Failed to retrieve tradable pairs", err.Error(),
		))
		return
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"exchange": exchange,
		"pairs":    pairs,
		"count":    len(pairs),
	}))
}

// GetSelection handles GET /api/v1/pairs/selection
// Returns the persisted user pair selection.
func (h *PairsHandler) GetSelection(c *gin.Context) {
	userID := userIDFromContext(c)
	exchange := c.DefaultQuery("exchange", "kucoin")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	sel, err := h.repo.Get(ctx, userID, exchange)
	if err != nil {
		h.logger.WithError(err).Error("Failed to get pair selection")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponseWithDetails(
			"SELECTION_ERROR", "Failed to retrieve pair selection", err.Error(),
		))
		return
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"exchange":  sel.Exchange,
		"mode":      sel.Mode,
		"pairs":     sel.Pairs,
		"overrides": sel.Overrides,
	}))
}

// putSelectionRequest is the body for PUT /api/v1/pairs/selection.
type putSelectionRequest struct {
	Mode     string   `json:"mode"`
	Pairs    []string `json:"pairs"`
	Exchange string   `json:"exchange"`
}

// PutSelection handles PUT /api/v1/pairs/selection
// Validates and persists the user pair selection.
func (h *PairsHandler) PutSelection(c *gin.Context) {
	var req putSelectionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_BODY", err.Error()))
		return
	}

	if req.Exchange == "" {
		req.Exchange = "kucoin"
	}

	if !validModes[req.Mode] {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse(
			"INVALID_MODE", "mode must be one of: auto, manual, hybrid",
		))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	// For manual/hybrid validate each requested pair against live tradable pairs.
	if req.Mode == "manual" || req.Mode == "hybrid" {
		tradable, err := h.gctClient.GetTradablePairs(ctx, req.Exchange, h.assetType)
		if err != nil {
			h.logger.WithError(err).Warn("Could not fetch tradable pairs for validation — skipping validation")
		} else {
			tradableSet := make(map[string]struct{}, len(tradable))
			for _, p := range tradable {
				tradableSet[p] = struct{}{}
			}
			var rejected []string
			for _, p := range req.Pairs {
				if _, ok := tradableSet[p]; !ok {
					rejected = append(rejected, p)
				}
			}
			if len(rejected) > 0 {
				c.JSON(http.StatusBadRequest, gin.H{
					"success":  false,
					"error":    "UNKNOWN_PAIRS",
					"message":  "one or more pairs are not tradable on this exchange",
					"rejected": rejected,
				})
				return
			}
		}
	}

	userID := userIDFromContext(c)

	sel := &storage.UserPairSelection{
		UserID:    userID,
		Exchange:  req.Exchange,
		Mode:      req.Mode,
		Pairs:     req.Pairs,
		Overrides: make(map[string]any),
	}
	if sel.Pairs == nil {
		sel.Pairs = []string{}
	}

	if err := h.repo.Upsert(ctx, sel); err != nil {
		h.logger.WithError(err).Error("Failed to upsert pair selection")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponseWithDetails(
			"UPSERT_ERROR", "Failed to save pair selection", err.Error(),
		))
		return
	}

	h.logger.WithFields(logrus.Fields{
		"exchange": req.Exchange,
		"mode":     req.Mode,
		"pairs":    len(req.Pairs),
	}).Info("Pair selection updated")

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"exchange": sel.Exchange,
		"mode":     sel.Mode,
		"pairs":    sel.Pairs,
	}))
}

// GetRankedPairs handles GET /api/v1/pairs/ranked
// Returns the master-agent scored ranking sorted by score descending.
// Optional query param: ?limit=N (default 20, max 100).
func (h *PairsHandler) GetRankedPairs(c *gin.Context) {
	limit := 20
	if v := c.Query("limit"); v != "" {
		if n, err := parsePositiveInt(v); err == nil && n > 0 {
			if n > 100 {
				n = 100
			}
			limit = n
		}
	}

	if h.rankProvider == nil {
		c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
			"pairs": []RankedPairEntry{},
			"count": 0,
			"note":  "master agent not available",
		}))
		return
	}

	pairs := h.rankProvider.TopRankedPairs(limit)
	if pairs == nil {
		pairs = []RankedPairEntry{}
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"pairs": pairs,
		"count": len(pairs),
	}))
}

// parsePositiveInt parses s as a positive integer.
func parsePositiveInt(s string) (int, error) {
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("not a number")
		}
		n = n*10 + int(ch-'0')
	}
	return n, nil
}
