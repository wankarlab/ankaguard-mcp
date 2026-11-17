package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/rantipay/trading-mcp/internal/xsec"
)

// shadowEvalCost is the round-trip cost fraction charged on rotated names when
// evaluating a scorer's forward paper equity — the same maker default the
// backtest and live config use, so the shadow A/B is net-of-cost and directly
// comparable to the /xsec backtests.
const shadowEvalCost = 0.0012

// GetShadowComparison handles GET /api/v1/xsec/shadow. For every scorer recorded
// in xsec_shadow_baskets (the live "momentum" config, the "ltr" ML shadow, and
// the "cand_lb14k12" candidate), it returns the realized forward, net-of-cost,
// market-neutral paper equity curve — the A/B evaluation the shadow table was
// built to feed. Read-only; degrades to an empty list if the table is absent.
func (h *XSecHandler) GetShadowComparison(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("XSEC_UNAVAILABLE", "xsec db not configured"))
		return
	}
	scorers, err := xsec.ShadowScorers(h.db)
	if err != nil {
		// Table missing / unreadable → empty comparison, not an error.
		c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{"scorers": []xsec.ShadowEquity{}, "cost_frac": shadowEvalCost}))
		return
	}
	out := make([]xsec.ShadowEquity, 0, len(scorers))
	for _, s := range scorers {
		eq, err := xsec.EvalShadowEquity(h.db, s, "1d", shadowEvalCost)
		if err != nil {
			h.log.WithError(err).WithField("scorer", s).Debug("xsec shadow eval failed")
			continue
		}
		out = append(out, eq)
	}
	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"scorers":   out,
		"cost_frac": shadowEvalCost,
	}))
}
