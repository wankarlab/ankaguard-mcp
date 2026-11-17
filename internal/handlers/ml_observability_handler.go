// Package handlers — ML observability (Phase E of the self-learning engine).
//
// Read-only endpoints to inspect the learning state:
//   GET /admin/ml/registry → model_registry (every version, status, metrics)
//   GET /admin/ml/weights  → signal_source_weights (per source × regime)
//
// Register in main.go:
//
//	h := handlers.NewMLObservabilityHandler(db)
//	h.Register(router.Group("/admin/ml", middleware.AdminAuthRequired(token, log)))
package handlers

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
)

// MLObservabilityHandler serves read-only views of the ML learning state.
type MLObservabilityHandler struct {
	db *sql.DB
}

// NewMLObservabilityHandler builds the handler over the SQLite trading DB.
func NewMLObservabilityHandler(db *sql.DB) *MLObservabilityHandler {
	return &MLObservabilityHandler{db: db}
}

// Register mounts the routes on the given group.
func (h *MLObservabilityHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/registry", h.listRegistry)
	rg.GET("/weights", h.listWeights)
}

// listRegistry returns every model version with its OOS metrics and lifecycle.
// GET /admin/ml/registry
func (h *MLObservabilityHandler) listRegistry(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "db unavailable"})
		return
	}
	rows, err := h.db.QueryContext(c.Request.Context(), `
		SELECT id, model_path, model_version, horizon_min,
		       oos_pf, oos_auc, oos_brier, oos_trades, train_rows,
		       status, shadow_until, created_at, promoted_at, retired_at
		FROM model_registry
		ORDER BY created_at DESC
		LIMIT 100`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}
	defer rows.Close()

	out := make([]gin.H, 0, 32)
	for rows.Next() {
		var (
			id, horizon, oosTrades, trainRows                       int64
			path, version, status                                   string
			oosPF, oosAUC, oosBrier                                 float64
			shadowUntil, createdAt, promotedAt, retiredAt           sql.NullInt64
		)
		if err := rows.Scan(&id, &path, &version, &horizon, &oosPF, &oosAUC, &oosBrier,
			&oosTrades, &trainRows, &status, &shadowUntil, &createdAt, &promotedAt, &retiredAt); err != nil {
			continue
		}
		out = append(out, gin.H{
			"id": id, "model_version": version, "horizon_min": horizon, "status": status,
			"oos_pf": oosPF, "oos_auc": oosAUC, "oos_brier": oosBrier,
			"oos_trades": oosTrades, "train_rows": trainRows,
			"shadow_until": nullInt(shadowUntil), "created_at": nullInt(createdAt),
			"promoted_at": nullInt(promotedAt), "retired_at": nullInt(retiredAt),
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "count": len(out), "data": out})
}

// listWeights returns the Bayesian per-(source, regime) weights.
// GET /admin/ml/weights
func (h *MLObservabilityHandler) listWeights(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "db unavailable"})
		return
	}
	rows, err := h.db.QueryContext(c.Request.Context(), `
		SELECT source, regime, weight, hit_rate, sample_n, updated_at
		FROM signal_source_weights
		ORDER BY regime, source`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}
	defer rows.Close()

	out := make([]gin.H, 0, 24)
	for rows.Next() {
		var (
			source, regime    string
			weight, hitRate   float64
			sampleN, updatedAt sql.NullInt64
		)
		if err := rows.Scan(&source, &regime, &weight, &hitRate, &sampleN, &updatedAt); err != nil {
			continue
		}
		out = append(out, gin.H{
			"source": source, "regime": regime, "weight": weight,
			"hit_rate": hitRate, "sample_n": nullInt(sampleN), "updated_at": nullInt(updatedAt),
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "count": len(out), "data": out})
}

func nullInt(n sql.NullInt64) interface{} {
	if !n.Valid {
		return nil
	}
	return n.Int64
}
