package handlers

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/volatility"
	"github.com/sirupsen/logrus"
)

// VolatilityHandler exposes the dynamic per-pair volatility classification table.
//
//   GET  /api/v1/volatility               → list all classifications
//   GET  /api/v1/volatility/:pair         → single pair's classification
//   POST /api/v1/volatility/:pair/override → manually pin a pair to a tier
type VolatilityHandler struct {
	classifier *volatility.Classifier
	log        *logrus.Logger
}

// NewVolatilityHandler returns a new handler. classifier may be nil; in that
// case all endpoints respond with 503 service-unavailable.
func NewVolatilityHandler(classifier *volatility.Classifier, log *logrus.Logger) *VolatilityHandler {
	return &VolatilityHandler{classifier: classifier, log: log}
}

// volatilityRow is the JSON shape exposed by the API.
type volatilityRow struct {
	Pair             string    `json:"pair"`
	Tier             string    `json:"tier"`
	CurrentATRPct    float64   `json:"current_atr_pct"`
	AvgATRPct24h     float64   `json:"avg_atr_pct_24h"`
	MaxATRPct24h     float64   `json:"max_atr_pct_24h"`
	LastSpikeATRPct  float64   `json:"last_spike_atr_pct"`
	LastSpikeAt      time.Time `json:"last_spike_at,omitempty"`
	SamplesCount     int       `json:"samples_count"`
	TierChangedAt    time.Time `json:"tier_changed_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	ClassifiedBy     string    `json:"classified_by"`
}

func toRow(c *volatility.Classification) volatilityRow {
	return volatilityRow{
		Pair:            c.Pair,
		Tier:            string(c.Tier),
		CurrentATRPct:   c.CurrentATRPct,
		AvgATRPct24h:    c.AvgATRPct24h,
		MaxATRPct24h:    c.MaxATRPct24h,
		LastSpikeATRPct: c.LastSpikeATRPct,
		LastSpikeAt:     c.LastSpikeAt,
		SamplesCount:    c.SamplesCount,
		TierChangedAt:   c.TierChangedAt,
		UpdatedAt:       c.UpdatedAt,
		ClassifiedBy:    c.ClassifiedBy,
	}
}

// ListClassifications returns every tracked pair with its current tier.
// Optional ?tier= filter narrows to a single tier (low|normal|volatile|extreme).
func (h *VolatilityHandler) ListClassifications(c *gin.Context) {
	if h.classifier == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "volatility classifier not configured"})
		return
	}
	tierFilter := volatility.Tier(c.Query("tier"))
	all := h.classifier.GetAll()

	rows := make([]volatilityRow, 0, len(all))
	tiers := map[string]int{"low": 0, "normal": 0, "volatile": 0, "extreme": 0}
	for _, cl := range all {
		tiers[string(cl.Tier)]++
		if tierFilter != "" && cl.Tier != tierFilter {
			continue
		}
		rows = append(rows, toRow(cl))
	}
	sort.Slice(rows, func(i, j int) bool {
		// Sort by tier severity (extreme first) then by ATR%
		order := map[string]int{"extreme": 0, "volatile": 1, "normal": 2, "low": 3}
		oi, oj := order[rows[i].Tier], order[rows[j].Tier]
		if oi != oj {
			return oi < oj
		}
		return rows[i].CurrentATRPct > rows[j].CurrentATRPct
	})
	c.JSON(http.StatusOK, gin.H{
		"count":         len(rows),
		"by_tier_count": tiers,
		"pairs":         rows,
	})
}

// GetClassification returns a single pair's volatility record.
func (h *VolatilityHandler) GetClassification(c *gin.Context) {
	if h.classifier == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "volatility classifier not configured"})
		return
	}
	pair := c.Param("pair")
	if pair == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing pair"})
		return
	}
	cl, ok := h.classifier.Get(pair)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "pair not classified yet", "pair": pair})
		return
	}
	c.JSON(http.StatusOK, toRow(cl))
}

// SetOverride pins a pair to a manual tier (overrides automatic classification).
// Body: {"tier": "low" | "normal" | "volatile" | "extreme"}
func (h *VolatilityHandler) SetOverride(c *gin.Context) {
	if h.classifier == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "volatility classifier not configured"})
		return
	}
	pair := c.Param("pair")
	if pair == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing pair"})
		return
	}
	var body struct {
		Tier string `json:"tier"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body: " + err.Error()})
		return
	}
	tier := volatility.Tier(body.Tier)
	if !tier.IsValid() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tier; must be one of low|normal|volatile|extreme"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	if err := h.classifier.SetManualOverride(ctx, pair, tier); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to set override: " + err.Error()})
		return
	}
	h.log.WithFields(logrus.Fields{"pair": pair, "tier": tier}).Info("volatility: manual override set")
	c.JSON(http.StatusOK, gin.H{"pair": pair, "tier": tier, "classified_by": "manual_override"})
}
