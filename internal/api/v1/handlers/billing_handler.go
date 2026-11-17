// Package handlers — CP-2: performance fee billing endpoints.
//
// GET  /api/v1/billing/fees/:user_id         — list fee periods (pending/collected)
// GET  /api/v1/billing/fees/:user_id/:period_id — detail for one period
//
// Collection is gated OFF by default (no outbound emitter).  When enabled,
// collection routes via odoo.ProcessPayment only (ADR-0002 §3, never Fineract).
//
// Authorization: RequireLevel 1 (own) or level 3+ (any user).
package handlers

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/sirupsen/logrus"
)

// FeeLedger is the minimal interface the billing handler needs.
// Satisfied by *billing.FeeLedger (internal/billing).
// Returns expose the accrued fee per period so the handler can apply the
// dynamic-config fee economy (rate + author/treasury split) on top.
type FeeLedger interface {
	// FeePeriodsForUser returns pending fee periods as plain economy rows.
	FeePeriodsForUser(ctx context.Context, userID string) ([]FeePeriodEconomy, error)
	// FeePeriodByID returns a single fee period by ID, or nil when not found.
	FeePeriodByID(ctx context.Context, id int64) (*FeePeriodEconomy, error)
}

// FeePeriodEconomy is the provider-agnostic projection of one accrued fee period.
// Money is kept in float USD here to match the existing ledger storage; the
// split economy (rate + author/treasury) is layered on by the handler from the
// dynamic config table — NEVER hardcoded in Go or the client.
type FeePeriodEconomy struct {
	ID              int64     `json:"id"`
	UserID          string    `json:"user_id"`
	PeriodStart     time.Time `json:"period_start"`
	PeriodEnd       time.Time `json:"period_end"`
	HighWaterMark   float64   `json:"high_water_mark"`
	PeriodNetPnLSum float64   `json:"period_net_pnl_sum"`
	AccruedFeeUSD   float64   `json:"accrued_fee_usd"`
	Rate            float64   `json:"rate"` // rate stored at accrual time (audit)
	Status          string    `json:"status"`
}

// FeeEconomyProvider supplies the dynamic fee economy (rate + splits) read from
// the config table. All optional: a nil func falls back to the canonical
// manifest defaults so the response is always well-formed.
type FeeEconomyProvider struct {
	FeeRate       func() float64 // billing.fee_rate    (default 0.20)
	AuthorSplit   func() float64 // billing.author_split (default 0.70)
	TreasurySplit func() float64 // billing.treasury_split (default 0.30)
}

// BillingHandler exposes performance fee ledger data over REST.
type BillingHandler struct {
	ledger  FeeLedger
	economy FeeEconomyProvider
	log     *logrus.Logger
}

// NewBillingHandler constructs a BillingHandler.
// ledger may be nil (returns 503).
func NewBillingHandler(ledger FeeLedger, log *logrus.Logger) *BillingHandler {
	if log == nil {
		log = logrus.New()
	}
	return &BillingHandler{ledger: ledger, log: log}
}

// WithEconomy wires the dynamic fee economy readers (rate + splits) from the
// config table. Safe to call after construction; nil funcs use manifest defaults.
func (h *BillingHandler) WithEconomy(p FeeEconomyProvider) *BillingHandler {
	h.economy = p
	return h
}

// resolveEconomy reads the live fee economy from config, applying manifest
// defaults when a reader is nil or returns a non-positive value.
func (h *BillingHandler) resolveEconomy() (feeRate, authorSplit, treasurySplit float64) {
	feeRate, authorSplit, treasurySplit = 0.20, 0.70, 0.30
	if h.economy.FeeRate != nil {
		if v := h.economy.FeeRate(); v > 0 {
			feeRate = v
		}
	}
	if h.economy.AuthorSplit != nil {
		if v := h.economy.AuthorSplit(); v > 0 {
			authorSplit = v
		}
	}
	if h.economy.TreasurySplit != nil {
		if v := h.economy.TreasurySplit(); v > 0 {
			treasurySplit = v
		}
	}
	return
}

// enrichPeriod returns the period plus the backend-computed split amounts.
func enrichPeriod(p FeePeriodEconomy, authorSplit, treasurySplit float64) gin.H {
	return gin.H{
		"id":                 p.ID,
		"user_id":            p.UserID,
		"period_start":       p.PeriodStart,
		"period_end":         p.PeriodEnd,
		"high_water_mark":    p.HighWaterMark,
		"period_net_pnl_sum": p.PeriodNetPnLSum,
		"fee_usd":            p.AccruedFeeUSD,
		"rate":               p.Rate,
		"status":             p.Status,
		"author_amount":      p.AccruedFeeUSD * authorSplit,
		"treasury_amount":    p.AccruedFeeUSD * treasurySplit,
	}
}

// GetFees handles GET /api/v1/billing/fees/:user_id.
// Returns all pending fee periods for the given user.
// Enforces: caller can only view own fees unless level >= 3.
func (h *BillingHandler) GetFees(c *gin.Context) {
	if h.ledger == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("FEE_LEDGER_UNAVAILABLE", "fee ledger not configured"))
		return
	}

	targetUserID := c.Param("user_id")
	if targetUserID == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("VALIDATION_FAILED", "user_id is required"))
		return
	}

	callerID := userIDFromContext(c)
	if callerID != targetUserID {
		level, _ := c.Get("kong_level")
		callerLevel, _ := level.(int)
		if callerLevel < 3 {
			c.JSON(http.StatusForbidden, models.NewErrorResponse("FORBIDDEN", "can only view your own fee periods"))
			return
		}
	}

	periods, err := h.ledger.FeePeriodsForUser(c.Request.Context(), targetUserID)
	if err != nil {
		h.log.WithError(err).WithField("user_id", targetUserID).Error("FeePeriodsForUser failed")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", err.Error()))
		return
	}

	feeRate, authorSplit, treasurySplit := h.resolveEconomy()

	enriched := make([]gin.H, 0, len(periods))
	var totalFee, totalAuthor, totalTreasury float64
	for _, p := range periods {
		enriched = append(enriched, enrichPeriod(p, authorSplit, treasurySplit))
		totalFee += p.AccruedFeeUSD
		totalAuthor += p.AccruedFeeUSD * authorSplit
		totalTreasury += p.AccruedFeeUSD * treasurySplit
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"user_id": targetUserID,
		"periods": enriched,
		// Economy is sourced from the dynamic config table — the client only formats.
		"economy": gin.H{
			"fee_rate":       feeRate,
			"author_split":   authorSplit,
			"treasury_split": treasurySplit,
		},
		"aggregate": gin.H{
			"total_fee_usd":         totalFee,
			"total_author_amount":   totalAuthor,
			"total_treasury_amount": totalTreasury,
		},
		"note": "collection_off: fees accrue but are not collected until operator enables billing",
	}))
}

// GetFeePeriod handles GET /api/v1/billing/fees/:user_id/:period_id.
// Returns detail for a single fee period.
func (h *BillingHandler) GetFeePeriod(c *gin.Context) {
	if h.ledger == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("FEE_LEDGER_UNAVAILABLE", "fee ledger not configured"))
		return
	}

	targetUserID := c.Param("user_id")
	periodIDStr := c.Param("period_id")
	if targetUserID == "" || periodIDStr == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("VALIDATION_FAILED", "user_id and period_id are required"))
		return
	}

	periodID, err := strconv.ParseInt(periodIDStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("VALIDATION_FAILED", "period_id must be an integer"))
		return
	}

	callerID := userIDFromContext(c)
	if callerID != targetUserID {
		level, _ := c.Get("kong_level")
		callerLevel, _ := level.(int)
		if callerLevel < 3 {
			c.JSON(http.StatusForbidden, models.NewErrorResponse("FORBIDDEN", "can only view your own fee periods"))
			return
		}
	}

	period, err := h.ledger.FeePeriodByID(c.Request.Context(), periodID)
	if err != nil {
		h.log.WithError(err).WithField("period_id", periodID).Error("FeePeriodByID failed")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", err.Error()))
		return
	}
	if period == nil {
		c.JSON(http.StatusNotFound, models.NewErrorResponse("NOT_FOUND", "fee period not found"))
		return
	}
	// HTTP-02: FeePeriodByID looks up by period_id alone, so a caller who owns
	// targetUserID (or has level>=3) but supplies someone ELSE's period_id would
	// otherwise get that other user's fee/PnL data back. Enforce ownership here.
	if period.UserID != targetUserID {
		c.JSON(http.StatusNotFound, models.NewErrorResponse("NOT_FOUND", "fee period not found"))
		return
	}

	feeRate, authorSplit, treasurySplit := h.resolveEconomy()
	enriched := enrichPeriod(*period, authorSplit, treasurySplit)
	enriched["economy"] = gin.H{
		"fee_rate":       feeRate,
		"author_split":   authorSplit,
		"treasury_split": treasurySplit,
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(enriched))
}
