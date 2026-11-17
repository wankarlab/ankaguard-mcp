// Package handlers contains HTTP handlers for the trading-mcp REST API.
// Register in main.go: handler := handlers.NewConfigHandler(cfgStore); handler.Register(router.Group("/api/v1/config"))
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	dynamic "github.com/rantipay/trading-mcp/internal/config/dynamic"
	"github.com/rantipay/trading-mcp/internal/config/dynamic/domain"
	"github.com/rantipay/trading-mcp/internal/config/dynamic/repository"
)

// autoPresetAppliedTotal counts the number of times an auto-generated preset
// (_auto_* prefix) is force-applied. Alert if this counter exceeds 0 in production.
var autoPresetAppliedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "auto_preset_applied_total",
	Help: "Total number of auto-generated presets applied via X-Force-Auto-Apply.",
}, []string{"name"})

// ConfigHandler exposes read-only REST endpoints for the dynamic configuration system.
type ConfigHandler struct {
	store *dynamic.ConfigStore
}

// NewConfigHandler creates a ConfigHandler backed by the given ConfigStore.
func NewConfigHandler(store *dynamic.ConfigStore) *ConfigHandler {
	return &ConfigHandler{store: store}
}

// Register mounts all config endpoints on the supplied RouterGroup.
//
//	GET  /groups
//	GET  /params
//	GET  /params/:key
//	GET  /params/:key/effective
//	PUT  /params/:key
//	DELETE /params/:key
//	POST /params/bulk
//	GET  /history
//	POST /rollback/:history_id
//	GET  /presets
//	GET  /presets/:name
//	POST /presets/:name/apply
//	POST /presets/:name/rollback
//	GET  /diff
//	POST /snapshot
func (h *ConfigHandler) Register(rg *gin.RouterGroup) {
	h.RegisterReadOnly(rg)
	h.RegisterWrite(rg)
}

// RegisterReadOnly mounts only the read endpoints. Safe to expose to any
// authenticated user — these values are platform policy/config, not secrets.
// Used by the JWT web-console mount (writes are gated separately by level).
func (h *ConfigHandler) RegisterReadOnly(rg *gin.RouterGroup) {
	rg.GET("/groups", h.listGroups)
	rg.GET("/params", h.listParams)
	rg.GET("/params/:key", h.getParam)
	rg.GET("/params/:key/effective", h.effectiveValue)
	rg.GET("/history", h.listHistory)
	rg.GET("/presets", h.listPresets)
	// NOTE: Gin's router does not allow a static child ("/presets/active") to
	// coexist with the wildcard ("/presets/:name") on the same path segment, so
	// the active-mode lookup is dispatched inside getPreset when name == "active".
	rg.GET("/presets/:name", h.getPreset)
	rg.GET("/diff", h.diffConfig)
}

// RegisterWrite mounts the mutating endpoints. Gate this group behind an
// elevated permission level when mounting on a user-facing (JWT) plane.
func (h *ConfigHandler) RegisterWrite(rg *gin.RouterGroup) {
	rg.PUT("/params/:key", h.updateParam)
	rg.DELETE("/params/:key", h.deleteParam)
	rg.POST("/params/bulk", h.bulkParams)
	rg.POST("/rollback/:history_id", h.rollbackChange)
	rg.POST("/presets/:name/apply", h.applyPreset)
	rg.POST("/presets/:name/rollback", h.rollbackPreset)
	rg.POST("/snapshot", h.createSnapshot)
	// Preset/mode CRUD (control panel). Mounted on the write group so they inherit
	// the same elevated permission gate as the other mutating config endpoints.
	rg.POST("/presets", h.createPreset)
	rg.PUT("/presets/:name", h.updatePreset)
	rg.DELETE("/presets/:name", h.deletePreset)
}

// ---- handlers ----------------------------------------------------------------

// listGroups returns a sorted, deduplicated list of all group names.
//
// GET /api/v1/config/groups
func (h *ConfigHandler) listGroups(c *gin.Context) {
	regs, err := h.store.Repo().ListRegistry(c.Request.Context(), "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to list registry: " + err.Error()})
		return
	}

	seen := make(map[string]struct{}, len(regs))
	for _, r := range regs {
		seen[r.GroupName] = struct{}{}
	}
	groups := make([]string, 0, len(seen))
	for g := range seen {
		groups = append(groups, g)
	}
	sort.Strings(groups)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"groups": groups, "total": len(groups)},
	})
}

// listParams returns parameters, optionally filtered by group, scope_type, scope_id, or
// modified=true (only params that have at least one explicit value row).
//
// GET /api/v1/config/params?group=&scope_type=&scope_id=&modified=true
func (h *ConfigHandler) listParams(c *gin.Context) {
	ctx := c.Request.Context()

	group := c.Query("group")
	scopeTypeFilter := domain.ScopeType(c.Query("scope_type"))
	scopeIDFilter := c.Query("scope_id")
	modifiedOnly := c.Query("modified") == "true"

	regs, err := h.store.Repo().ListRegistry(ctx, group)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to list registry: " + err.Error()})
		return
	}

	// For modified/scope filtering we need all values once.
	var allVals []*domain.ParameterValue
	if modifiedOnly || scopeTypeFilter != "" || scopeIDFilter != "" {
		allVals, err = h.store.Repo().ListAllValues(ctx)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to list values: " + err.Error()})
			return
		}
	}

	// Build an index: key → []ParameterValue for quick lookup.
	valIndex := make(map[string][]*domain.ParameterValue, len(allVals))
	for _, v := range allVals {
		valIndex[v.Key] = append(valIndex[v.Key], v)
	}

	// Build global-scope snapshot for current_effective_value.
	globalChain := dynamic.BotScopeChain("", "", "")
	snap := h.store.SnapshotForScope(globalChain)

	type paramItem struct {
		Key                   string `json:"key"`
		Type                  string `json:"type"`
		Group                 string `json:"group"`
		Description           string `json:"description"`
		Default               string `json:"default"`
		CurrentEffectiveValue string `json:"current_effective_value"`
		HasOverride           bool   `json:"has_override"`
	}

	result := make([]paramItem, 0, len(regs))
	for _, reg := range regs {
		vals := valIndex[reg.Key]

		// Apply scope_type / scope_id filter.
		if scopeTypeFilter != "" || scopeIDFilter != "" {
			match := false
			for _, v := range vals {
				if scopeTypeFilter != "" && v.ScopeType != scopeTypeFilter {
					continue
				}
				if scopeIDFilter != "" && v.ScopeID != scopeIDFilter {
					continue
				}
				match = true
				break
			}
			if !match {
				continue
			}
		}

		// Apply modified filter.
		if modifiedOnly && len(vals) == 0 {
			continue
		}

		result = append(result, paramItem{
			Key:                   reg.Key,
			Type:                  string(reg.Type),
			Group:                 reg.GroupName,
			Description:           reg.Description,
			Default:               reg.DefaultValue,
			CurrentEffectiveValue: snap.String(reg.Key),
			HasOverride:           len(vals) > 0,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"params": result, "total": len(result)},
	})
}

// getParam returns full metadata, all scope values, and the last 20 history entries
// for a single parameter.
//
// GET /api/v1/config/params/:key
func (h *ConfigHandler) getParam(c *gin.Context) {
	ctx := c.Request.Context()
	key := c.Param("key")

	reg, err := h.store.Repo().GetRegistry(ctx, key)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "parameter not found: " + key})
		return
	}

	vals, err := h.store.Repo().ListValues(ctx, key)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to list values: " + err.Error()})
		return
	}

	history, err := h.store.Repo().ListHistory(ctx, repository.HistoryFilter{Key: key}, 20)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to list history: " + err.Error()})
		return
	}

	// Resolve effective value using global scope chain.
	snap := h.store.SnapshotForScope(dynamic.BotScopeChain("", "", ""))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"registry":        registryJSON(reg),
			"values":          valueListJSON(vals),
			"history":         historyListJSON(history),
			"effective_value": snap.String(key),
			"has_override":    len(vals) > 0,
		},
	})
}

// effectiveValue resolves the effective value for a key given an optional bot scope.
//
// GET /api/v1/config/params/:key/effective?bot_id=&pair=&exchange=
func (h *ConfigHandler) effectiveValue(c *gin.Context) {
	key := c.Param("key")
	botID := c.Query("bot_id")
	pair := c.Query("pair")
	exchange := c.Query("exchange")

	reg, err := h.store.Repo().GetRegistry(c.Request.Context(), key)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "parameter not found: " + key})
		return
	}

	chain := dynamic.BotScopeChain(botID, pair, exchange)
	snap := h.store.SnapshotForScope(chain)
	value := snap.String(key)

	// Describe the scope chain used for transparency.
	scopeChain := make([]gin.H, 0, len(chain))
	for _, ref := range chain {
		scopeChain = append(scopeChain, gin.H{
			"scope_type": string(ref.Type),
			"scope_id":   ref.ID,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"key":         key,
			"value":       value,
			"type":        string(reg.Type),
			"scope_chain": scopeChain,
		},
	})
}

// listHistory returns history entries filtered by optional query parameters.
//
// GET /api/v1/config/history?key=&by=&since=&until=&limit=100
func (h *ConfigHandler) listHistory(c *gin.Context) {
	ctx := c.Request.Context()

	filter := repository.HistoryFilter{
		Key:       c.Query("key"),
		ChangedBy: c.Query("by"),
	}

	if s := c.Query("since"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "invalid since format, expected RFC3339"})
			return
		}
		filter.Since = t
	}

	if u := c.Query("until"); u != "" {
		t, err := time.Parse(time.RFC3339, u)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "invalid until format, expected RFC3339"})
			return
		}
		filter.Until = t
	}

	limit := 100
	if l := c.Query("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}

	history, err := h.store.Repo().ListHistory(ctx, filter, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to list history: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"history": historyListJSON(history), "total": len(history)},
	})
}

// ---- write handlers ----------------------------------------------------------

// riskSensitiveConfigKeyPrefixes mirrors the list in
// internal/trading/middleware/kong_jwt_middleware.go (isRiskSensitiveConfigPath).
// Kept here too as defense in depth: the middleware gate covers the single-key
// PUT/DELETE path (key is in the URL), but POST /params/bulk carries the keys
// in the JSON body where the middleware cannot see them, so bulkParams must
// enforce the same floor itself (HTTP-01).
//
// trading.live_enabled is the real-money master switch of the live MCP
// profile, so it needs the operator level as well.
var riskSensitiveConfigKeyPrefixes = []string{
	"risk.",
	"bot.max_daily_loss_percent",
	"trading.live_enabled",
}

func isRiskSensitiveConfigKey(key string) bool {
	for _, prefix := range riskSensitiveConfigKeyPrefixes {
		if key == prefix || strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// requireLevelForKey aborts the request with 403 and returns false when key is
// risk-sensitive and the authenticated caller's level (set by KongJWTMiddleware
// as "user_level" in the gin context) is below minLevel. Callers on the
// admin-token plane (no JWT, no "user_level" in context) are trusted as-is —
// that plane already requires AdminAuthRequired at the router level.
func requireLevelForKey(c *gin.Context, key string, minLevel int) bool {
	if !isRiskSensitiveConfigKey(key) {
		return true
	}
	levelVal, exists := c.Get("user_level")
	if !exists {
		// No JWT context: admin-token plane, already gated upstream.
		return true
	}
	level, ok := levelVal.(int)
	if !ok || level < minLevel {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "error": fmt.Sprintf("key %q requires level %d", key, minLevel)})
		c.Abort()
		return false
	}
	return true
}

// requireLevelForKeys applies requireLevelForKey to every key, in a stable
// order, before anything is written. Preset create/update/apply and both
// rollbacks write keys carried in the body or in stored data where the
// path-based middleware gate cannot see them, so they must enforce the same
// floor as PUT /params/:key themselves.
func requireLevelForKeys(c *gin.Context, keys []string, minLevel int) bool {
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	for _, k := range sorted {
		if !requireLevelForKey(c, k, minLevel) {
			return false
		}
	}
	return true
}

// paramKeys returns the keys of a preset parameter map.
func paramKeys(params map[string]string) []string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	return keys
}

// rejectEditionInvariants answers 400 and returns false when a value breaks a
// safety invariant of the compiled edition (no-op in the standard build).
func rejectEditionInvariants(c *gin.Context, params map[string]string) bool {
	for _, k := range paramKeys(params) {
		if err := dynamic.CheckEditionInvariant(k, params[k]); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "validation failed: " + err.Error()})
			c.Abort()
			return false
		}
	}
	return true
}

// verifiedActor returns the authenticated user_id from the JWT context set by
// KongJWTMiddleware, if present. HTTP-01: audit trail identity (UpdatedBy) must
// come from the verified token, never from the client-controlled X-By-User
// header or request body — those remain usable only on the admin-token plane
// where no JWT context exists.
func verifiedActor(c *gin.Context) (string, bool) {
	v, exists := c.Get("user_id")
	if !exists {
		return "", false
	}
	s, ok := v.(string)
	return s, ok && s != ""
}

// updateParam validates and persists a single parameter value change.
//
// PUT /api/v1/config/params/:key
// Body: {"value":"...","scope_type":"global","scope_id":"*","by":"user","reason":"..."}
func (h *ConfigHandler) updateParam(c *gin.Context) {
	ctx := c.Request.Context()
	key := c.Param("key")

	if !requireLevelForKey(c, key, 4) {
		return
	}

	var body struct {
		Value     string `json:"value"`
		ScopeType string `json:"scope_type"`
		ScopeID   string `json:"scope_id"`
		By        string `json:"by"`
		Reason    string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "invalid request body: " + err.Error()})
		return
	}

	// HTTP-01: prefer the verified JWT identity over any client-supplied value
	// for the audit trail. Only fall back to the header/body (trusted on the
	// admin-token plane, where there is no JWT context) when no JWT identity
	// is present.
	if actor, ok := verifiedActor(c); ok {
		body.By = actor
	} else {
		if body.By == "" {
			body.By = c.GetHeader("X-By-User")
		}
		if body.By == "" {
			body.By = "api"
		}
	}

	// Default scope to global/*.
	scopeType := domain.ScopeType(body.ScopeType)
	if scopeType == "" {
		scopeType = domain.ScopeGlobal
	}
	scopeID := body.ScopeID
	if scopeID == "" {
		scopeID = "*"
	}

	// Verify key exists in registry.
	reg, err := h.store.Repo().GetRegistry(ctx, key)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "parameter not found: " + key})
		return
	}

	// Validate value.
	if err := dynamic.ValidateValue(reg, body.Value); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "validation failed: " + err.Error()})
		return
	}

	// Capture old value before write.
	oldValue := ""
	if existing, err := h.store.Repo().GetValue(ctx, key, scopeType, scopeID); err == nil {
		oldValue = existing.Value
	}

	val := &domain.ParameterValue{
		Key:       key,
		ScopeType: scopeType,
		ScopeID:   scopeID,
		Value:     body.Value,
		UpdatedBy: body.By,
		Source:    domain.SourceManual,
	}

	if err := h.store.Set(ctx, val, body.Reason); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to set value: " + err.Error()})
		return
	}

	// Fetch history id of the change we just wrote (most recent for this key).
	historyEntries, _ := h.store.Repo().ListHistory(ctx, repository.HistoryFilter{Key: key}, 1)
	var historyID int64
	if len(historyEntries) > 0 {
		historyID = historyEntries[0].ID
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"key":        key,
			"scope_type": string(scopeType),
			"scope_id":   scopeID,
			"old_value":  oldValue,
			"new_value":  body.Value,
			"history_id": historyID,
		},
	})
}

// deleteParam removes a scoped parameter value override so the key falls back to the
// next scope in the resolution chain.
//
// DELETE /api/v1/config/params/:key?scope_type=&scope_id=&by=
func (h *ConfigHandler) deleteParam(c *gin.Context) {
	ctx := c.Request.Context()
	key := c.Param("key")

	if !requireLevelForKey(c, key, 4) {
		return
	}

	scopeType := domain.ScopeType(c.Query("scope_type"))
	if scopeType == "" {
		scopeType = domain.ScopeGlobal
	}
	scopeID := c.Query("scope_id")
	if scopeID == "" {
		scopeID = "*"
	}
	var by string
	if actor, ok := verifiedActor(c); ok {
		by = actor
	} else {
		by = c.Query("by")
		if by == "" {
			by = c.GetHeader("X-By-User")
		}
		if by == "" {
			by = "api"
		}
	}

	if err := h.store.Repo().DeleteValue(ctx, key, scopeType, scopeID, by); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to delete value: " + err.Error()})
		return
	}

	// Rebuild snapshot after deletion (best-effort; non-fatal).
	_ = h.store.Load(ctx)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"deleted":    true,
			"key":        key,
			"scope_type": string(scopeType),
			"scope_id":   scopeID,
		},
	})
}

// bulkParamsRequest is the expected request body for POST /params/bulk.
type bulkParamsRequest struct {
	By      string `json:"by"`
	Reason  string `json:"reason"`
	Changes []struct {
		Key       string `json:"key"`
		Value     string `json:"value"`
		ScopeType string `json:"scope_type"`
		ScopeID   string `json:"scope_id"`
	} `json:"changes"`
}

// bulkParams atomically validates and applies a set of parameter changes.
//
// POST /api/v1/config/params/bulk
func (h *ConfigHandler) bulkParams(c *gin.Context) {
	ctx := c.Request.Context()

	var body bulkParamsRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "invalid request body: " + err.Error()})
		return
	}

	// HTTP-01: bulk carries keys in the body (path-based middleware gate can't
	// see them), so reject the whole batch up front if it touches any
	// risk-sensitive key and the caller is below the required level.
	for _, ch := range body.Changes {
		if !requireLevelForKey(c, ch.Key, 4) {
			return
		}
	}

	// HTTP-01: same verified-identity preference as updateParam/deleteParam.
	if actor, ok := verifiedActor(c); ok {
		body.By = actor
	}

	if body.By == "" {
		body.By = c.GetHeader("X-By-User")
	}
	if body.By == "" {
		body.By = "api"
	}

	if len(body.Changes) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "changes array must not be empty"})
		return
	}

	snapBefore := h.store.Snapshot()
	hashBefore := snapBefore.Hash

	// Build the slice of ParameterValue and collect old values.
	vals := make([]*domain.ParameterValue, 0, len(body.Changes))
	type changeResult struct {
		Key       string `json:"key"`
		Old       string `json:"old"`
		New       string `json:"new"`
		HistoryID int64  `json:"history_id"`
	}

	for _, ch := range body.Changes {
		scopeType := domain.ScopeType(ch.ScopeType)
		if scopeType == "" {
			scopeType = domain.ScopeGlobal
		}
		scopeID := ch.ScopeID
		if scopeID == "" {
			scopeID = "*"
		}
		vals = append(vals, &domain.ParameterValue{
			Key:       ch.Key,
			ScopeType: scopeType,
			ScopeID:   scopeID,
			Value:     ch.Value,
			UpdatedBy: body.By,
			Source:    domain.SourceManual,
		})
	}

	// Capture old values from the snapshot before applying.
	oldValues := make(map[string]string, len(vals))
	for _, v := range vals {
		existing, err := h.store.Repo().GetValue(ctx, v.Key, v.ScopeType, v.ScopeID)
		if err == nil {
			oldValues[v.Key] = existing.Value
		}
	}

	// Execute bulk set (includes per-value validation + cross-param validation).
	if err := h.store.BulkSet(ctx, vals, body.By, body.Reason); err != nil {
		errMsg := err.Error()
		// Detect cross-param failure.
		if strings.Contains(errMsg, "cross-param") {
			c.JSON(http.StatusBadRequest, gin.H{
				"success":          false,
				"error":            "cross_param_validation_failed",
				"validator":        "cross_param_failed",
				"message":          errMsg,
				"rejected_changes": body.Changes,
			})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": errMsg})
		return
	}

	// Build changes array with history IDs.
	results := make([]changeResult, 0, len(vals))
	var lastHistoryID int64
	for _, v := range vals {
		histEntries, _ := h.store.Repo().ListHistory(ctx, repository.HistoryFilter{Key: v.Key}, 1)
		var hid int64
		if len(histEntries) > 0 {
			hid = histEntries[0].ID
			if hid > lastHistoryID {
				lastHistoryID = hid
			}
		}
		results = append(results, changeResult{
			Key:       v.Key,
			Old:       oldValues[v.Key],
			New:       v.Value,
			HistoryID: hid,
		})
	}

	snapAfter := h.store.Snapshot()
	hashAfter := snapAfter.Hash
	nextCycleETA := time.Now().Add(30 * time.Second).UTC().Format(time.RFC3339)
	rollbackToken := fmt.Sprintf("snap_%d", lastHistoryID)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"applied":              len(results),
			"failed":               0,
			"snapshot_hash_before": hashBefore,
			"snapshot_hash_after":  hashAfter,
			"next_cycle_eta":       nextCycleETA,
			"rollback_token":       rollbackToken,
			"changes":              results,
		},
	})
}

// rollbackChange reverts a previously recorded parameter change by re-applying the old value.
//
// POST /api/v1/config/rollback/:history_id
func (h *ConfigHandler) rollbackChange(c *gin.Context) {
	ctx := c.Request.Context()

	idStr := c.Param("history_id")
	historyID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "invalid history_id: must be integer"})
		return
	}

	entry, err := h.store.Repo().GetHistory(ctx, historyID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": fmt.Sprintf("history entry %d not found", historyID)})
		return
	}

	// The rollback writes entry.Key: same level floor as PUT /params/:key.
	if !requireLevelForKey(c, entry.Key, 4) {
		return
	}

	if entry.OldValue == nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "cannot rollback: history entry has no prior value (was an insert); use DELETE to remove the override instead",
		})
		return
	}

	by := c.GetHeader("X-By-User")
	if by == "" {
		by = "api"
	}

	reason := fmt.Sprintf("rollback of history_id=%d", historyID)

	val := &domain.ParameterValue{
		Key:       entry.Key,
		ScopeType: entry.ScopeType,
		ScopeID:   entry.ScopeID,
		Value:     *entry.OldValue,
		UpdatedBy: by,
		Source:    domain.SourceManual,
	}

	if err := h.store.Set(ctx, val, reason); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to apply rollback: " + err.Error()})
		return
	}

	// Fetch the new history row just written.
	newEntries, _ := h.store.Repo().ListHistory(ctx, repository.HistoryFilter{Key: entry.Key}, 1)
	var newHistoryID int64
	if len(newEntries) > 0 {
		newHistoryID = newEntries[0].ID
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"rolled_back": true,
			"history_id":  newHistoryID,
			"key":         entry.Key,
			"old_value":   entry.NewValue,  // what was active before rollback
			"new_value":   *entry.OldValue, // what is now active after rollback
		},
	})
}

// ---- preset handlers (TC-6.2 / TC-6.3 / TC-6.4) -----------------------------

// listPresets returns all defined presets, optionally filtered by category.
//
// GET /api/v1/config/presets?category=mode
func (h *ConfigHandler) listPresets(c *gin.Context) {
	ctx := c.Request.Context()

	category := c.Query("category")
	var (
		presets []*domain.ParameterPreset
		err     error
	)
	if category != "" {
		presets, err = h.store.Repo().ListPresetsByCategory(ctx, category)
	} else {
		presets, err = h.store.Repo().ListPresets(ctx)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to list presets: " + err.Error()})
		return
	}
	out := make([]gin.H, 0, len(presets))
	for _, p := range presets {
		out = append(out, presetJSON(p))
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"presets": out, "total": len(out)}})
}

// getPreset returns a single preset by name. The reserved name "active" returns
// the currently-active mode (is_active=true AND category='mode') because Gin's
// router cannot host a static "/presets/active" sibling next to "/presets/:name".
//
// GET /api/v1/config/presets/:name
// GET /api/v1/config/presets/active
func (h *ConfigHandler) getPreset(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	if name == "active" {
		p, err := h.store.Repo().GetActiveMode(ctx)
		if err != nil {
			// No mode activated yet is a normal state, not an error: return 200 with
			// a null payload so the web console renders "no active mode" cleanly
			// instead of logging a 404 on every poll. A genuine DB failure is still
			// surfaced as 500 so it is not silently masked as "none".
			if errors.Is(err, pgx.ErrNoRows) {
				c.JSON(http.StatusOK, gin.H{"success": true, "data": nil})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to read active mode: " + err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": presetJSON(p)})
		return
	}

	p, err := h.store.Repo().GetPreset(ctx, name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "preset not found: " + name})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": presetJSON(p)})
}

// presetUpsertRequest is the body for creating or updating a preset/mode.
type presetUpsertRequest struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	RiskLabel   string            `json:"risk_label"`
	Category    string            `json:"category"`
	Editable    *bool             `json:"editable"`
	Params      map[string]string `json:"params"`
}

// validatePresetParams checks every key/value in params against the registry
// (existence, type, min/max, enum). Returns a descriptive error on the first
// invalid entry so the client can correct it.
func (h *ConfigHandler) validatePresetParams(ctx context.Context, params map[string]string) error {
	for key, value := range params {
		reg, err := h.store.Repo().GetRegistry(ctx, key)
		if err != nil {
			return fmt.Errorf("unknown parameter %q", key)
		}
		if vErr := dynamic.ValidateValue(reg, value); vErr != nil {
			return fmt.Errorf("invalid value for %q: %w", key, vErr)
		}
	}
	return nil
}

// createPreset creates a new preset or mode from an explicit set of params.
// Every param is validated against the registry before persistence.
//
// POST /api/v1/config/presets
// Body: {"name","description","risk_label","category","editable","params":{key:value}}
func (h *ConfigHandler) createPreset(c *gin.Context) {
	ctx := c.Request.Context()

	var body presetUpsertRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "invalid request body: " + err.Error()})
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "name is required"})
		return
	}
	if body.Name == "active" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "'active' is a reserved preset name"})
		return
	}
	if len(body.Params) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "params must not be empty"})
		return
	}
	if !requireLevelForKeys(c, paramKeys(body.Params), 4) {
		return
	}

	if err := h.validatePresetParams(ctx, body.Params); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "validation failed: " + err.Error()})
		return
	}

	by := c.GetHeader("X-By-User")
	if by == "" {
		by = "api"
	}

	category := strings.TrimSpace(body.Category)
	if category == "" {
		category = "preset"
	}
	editable := true
	if body.Editable != nil {
		editable = *body.Editable
	}

	rawParams, err := json.Marshal(body.Params)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to encode params: " + err.Error()})
		return
	}

	preset := &domain.ParameterPreset{
		Name:        body.Name,
		Description: body.Description,
		ScopeType:   domain.ScopeGlobal,
		ScopeID:     "*",
		ParamsJSON:  json.RawMessage(rawParams),
		CreatedBy:   by,
		IsActive:    false,
		Category:    category,
		RiskLabel:   body.RiskLabel,
		Editable:    editable,
	}

	if err := h.store.Repo().CreatePreset(ctx, preset); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to create preset: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"name":     body.Name,
			"category": category,
			"params":   len(body.Params),
		},
	})
}

// updatePreset edits an existing preset's params, description and risk_label.
// Refuses to edit a non-editable preset.
//
// PUT /api/v1/config/presets/:name
// Body: {"description","risk_label","params":{key:value}}
func (h *ConfigHandler) updatePreset(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	existing, err := h.store.Repo().GetPreset(ctx, name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "preset not found: " + name})
		return
	}
	if !existing.Editable {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "error": "preset is not editable: " + name})
		return
	}

	var body presetUpsertRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "invalid request body: " + err.Error()})
		return
	}
	if len(body.Params) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "params must not be empty"})
		return
	}
	if !requireLevelForKeys(c, paramKeys(body.Params), 4) {
		return
	}

	if err := h.validatePresetParams(ctx, body.Params); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "validation failed: " + err.Error()})
		return
	}

	rawParams, err := json.Marshal(body.Params)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to encode params: " + err.Error()})
		return
	}

	updated := &domain.ParameterPreset{
		Name:        name,
		Description: body.Description,
		RiskLabel:   body.RiskLabel,
		ParamsJSON:  json.RawMessage(rawParams),
	}

	if err := h.store.Repo().UpdatePreset(ctx, name, updated); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to update preset: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"name": name, "params": len(body.Params)},
	})
}

// deletePreset removes a preset. Refuses to delete a non-editable preset.
//
// DELETE /api/v1/config/presets/:name
func (h *ConfigHandler) deletePreset(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	existing, err := h.store.Repo().GetPreset(ctx, name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "preset not found: " + name})
		return
	}
	if !existing.Editable {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "error": "preset is not editable: " + name})
		return
	}

	if err := h.store.Repo().DeletePreset(ctx, name); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to delete preset: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"deleted": true, "name": name}})
}

// applyPreset atomically applies a preset, capturing a pre-apply auto-snapshot first.
//
// POST /api/v1/config/presets/:name/apply
// Body: {"by":"...","reason":"..."}
func (h *ConfigHandler) applyPreset(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	// TC-8.8: Safety guard — auto-generated presets require an explicit opt-in header
	// to prevent accidental application of optimizer-generated configurations.
	if strings.HasPrefix(name, "_auto_") {
		if c.GetHeader("X-Force-Auto-Apply") != "true" {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"error":   "auto_preset_blocked",
				"message": "auto-generated presets require explicit X-Force-Auto-Apply header",
			})
			return
		}
		// Header present — log WARN and increment metric before proceeding.
		autoPresetAppliedTotal.WithLabelValues(name).Inc()
	}

	var body struct {
		By     string `json:"by"`
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "invalid request body: " + err.Error()})
		return
	}
	if body.By == "" {
		body.By = c.GetHeader("X-By-User")
	}
	if body.By == "" {
		body.By = "api"
	}

	// 1. Load the preset.
	preset, err := h.store.Repo().GetPreset(ctx, name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "preset not found: " + name})
		return
	}

	// 2. Decode preset params so we know which keys will change.
	var presetParams map[string]string
	if len(preset.ParamsJSON) > 0 {
		if err := json.Unmarshal(preset.ParamsJSON, &presetParams); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to parse preset params: " + err.Error()})
			return
		}
	}

	// 2b. Applying writes every key of the preset: same level floor as
	// PUT /params/:key, and the edition invariants (the repository applies
	// the preset directly, without the store's validation).
	if !requireLevelForKeys(c, paramKeys(presetParams), 4) {
		return
	}
	if !rejectEditionInvariants(c, presetParams) {
		return
	}

	// 3. Capture current values for the keys in this preset → auto-snapshot.
	snapName := fmt.Sprintf("_auto_snap_before_%s_%s", name, time.Now().UTC().Format("20060102T150405Z"))
	if len(presetParams) > 0 {
		snapParams := make(map[string]string, len(presetParams))
		snap := h.store.Snapshot()
		globalChain := dynamic.BotScopeChain("", "", "")
		scoped := h.store.SnapshotForScope(globalChain)
		for key := range presetParams {
			_ = snap // ensure snap is used (it anchors consistent reads)
			snapParams[key] = scoped.String(key)
		}
		rawSnap, _ := json.Marshal(snapParams)
		autoPreset := &domain.ParameterPreset{
			Name:        snapName,
			Description: fmt.Sprintf("Auto-snapshot captured before applying preset '%s'", name),
			ScopeType:   domain.ScopeGlobal,
			ScopeID:     "*",
			ParamsJSON:  json.RawMessage(rawSnap),
			CreatedBy:   body.By,
			IsActive:    false,
		}
		if snapErr := h.store.Repo().CreatePreset(ctx, autoPreset); snapErr != nil {
			// Best-effort: log but do not abort.
			_ = snapErr
		}
	}

	// 4. Apply the preset via the repository (handles history + is_active flag).
	reason := body.Reason
	if reason == "" {
		reason = fmt.Sprintf("apply preset %s", name)
	}
	_, err = h.store.Repo().ApplyPreset(ctx, name, body.By, reason)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "failed to apply preset: " + err.Error()})
		return
	}

	// 5. Reload store snapshot.
	_ = h.store.Load(ctx)

	snapAfter := h.store.Snapshot()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"applied":             true,
			"preset":              name,
			"changes_count":       len(presetParams),
			"rollback_token":      snapName,
			"snapshot_hash_after": snapAfter.Hash,
		},
	})
}

// rollbackPreset reverts to the most-recent auto-snapshot taken before the named preset was applied.
//
// POST /api/v1/config/presets/:name/rollback
func (h *ConfigHandler) rollbackPreset(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	by := c.GetHeader("X-By-User")
	if by == "" {
		by = "api"
	}

	// Find the most-recent auto-snapshot for this preset.
	prefix := "_auto_snap_before_" + name + "_"
	allPresets, err := h.store.Repo().ListPresets(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to list presets: " + err.Error()})
		return
	}

	// Sort descending by name (timestamp suffix makes lexicographic order == chronological order).
	sort.Slice(allPresets, func(i, j int) bool {
		return allPresets[i].Name > allPresets[j].Name
	})

	var snapPreset *domain.ParameterPreset
	for _, p := range allPresets {
		if strings.HasPrefix(p.Name, prefix) {
			snapPreset = p
			break
		}
	}
	if snapPreset == nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "no auto-snapshot found for preset: " + name})
		return
	}

	// Decode snapshot params and apply them via BulkSet.
	var snapParams map[string]string
	if err := json.Unmarshal(snapPreset.ParamsJSON, &snapParams); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to parse snapshot params: " + err.Error()})
		return
	}

	// The rollback restores every key of the snapshot: same level floor as
	// PUT /params/:key.
	if !requireLevelForKeys(c, paramKeys(snapParams), 4) {
		return
	}

	vals := make([]*domain.ParameterValue, 0, len(snapParams))
	for k, v := range snapParams {
		vals = append(vals, &domain.ParameterValue{
			Key:       k,
			ScopeType: domain.ScopeGlobal,
			ScopeID:   "*",
			Value:     v,
			UpdatedBy: by,
			Source:    domain.SourcePreset,
		})
	}

	if len(vals) > 0 {
		if err := h.store.BulkSet(ctx, vals, by, fmt.Sprintf("rollback of preset %s via snapshot %s", name, snapPreset.Name)); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to apply rollback: " + err.Error()})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"rolled_back":      true,
			"preset":           name,
			"applied_snapshot": snapPreset.Name,
			"changes_count":    len(vals),
		},
	})
}

// diffConfig computes the diff between two config states.
//
// GET /api/v1/config/diff?from=preset:NAME&to=preset:NAME
// GET /api/v1/config/diff?from=current&to=preset:NAME
func (h *ConfigHandler) diffConfig(c *gin.Context) {
	ctx := c.Request.Context()
	fromStr := c.Query("from")
	toStr := c.Query("to")

	if fromStr == "" || toStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "both 'from' and 'to' query parameters are required"})
		return
	}

	fromMap, err := resolveConfigSide(ctx, fromStr, h)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "failed to resolve 'from': " + err.Error()})
		return
	}
	toMap, err := resolveConfigSide(ctx, toStr, h)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "failed to resolve 'to': " + err.Error()})
		return
	}

	type addedEntry struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	type changedEntry struct {
		Key  string `json:"key"`
		From string `json:"from"`
		To   string `json:"to"`
	}

	added := make([]addedEntry, 0)
	removed := make([]addedEntry, 0)
	changed := make([]changedEntry, 0)

	// Keys in toMap but not in fromMap → added.
	for k, v := range toMap {
		if _, exists := fromMap[k]; !exists {
			added = append(added, addedEntry{Key: k, Value: v})
		}
	}
	// Keys in fromMap but not in toMap → removed.
	for k, v := range fromMap {
		if _, exists := toMap[k]; !exists {
			removed = append(removed, addedEntry{Key: k, Value: v})
		}
	}
	// Keys in both but different value → changed.
	for k, fromVal := range fromMap {
		if toVal, exists := toMap[k]; exists && toVal != fromVal {
			changed = append(changed, changedEntry{Key: k, From: fromVal, To: toVal})
		}
	}

	// Sort for deterministic output.
	sort.Slice(added, func(i, j int) bool { return added[i].Key < added[j].Key })
	sort.Slice(removed, func(i, j int) bool { return removed[i].Key < removed[j].Key })
	sort.Slice(changed, func(i, j int) bool { return changed[i].Key < changed[j].Key })

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"from":    fromStr,
			"to":      toStr,
			"added":   added,
			"removed": removed,
			"changed": changed,
		},
	})
}

// createSnapshot captures the current global config values as a named preset.
//
// POST /api/v1/config/snapshot
// Body: {"name":"<optional>"}
func (h *ConfigHandler) createSnapshot(c *gin.Context) {
	ctx := c.Request.Context()

	var body struct {
		Name string `json:"name"`
	}
	_ = c.ShouldBindJSON(&body) // name is optional

	snapName := body.Name
	if snapName == "" {
		snapName = "_snap_" + time.Now().UTC().Format("20060102T150405Z")
	}

	by := c.GetHeader("X-By-User")
	if by == "" {
		by = "api"
	}

	// Capture all current global-scope values from the snapshot.
	snap := h.store.Snapshot()
	globalChain := dynamic.BotScopeChain("", "", "")
	scoped := h.store.SnapshotForScope(globalChain)

	// Enumerate all known keys from the registry to build the snapshot map.
	regs, err := h.store.Repo().ListRegistry(ctx, "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to list registry: " + err.Error()})
		return
	}

	snapParams := make(map[string]string, len(regs))
	for _, reg := range regs {
		_ = snap // anchors consistent read frame
		snapParams[reg.Key] = scoped.String(reg.Key)
	}

	rawSnap, _ := json.Marshal(snapParams)
	preset := &domain.ParameterPreset{
		Name:        snapName,
		Description: fmt.Sprintf("Ad-hoc snapshot created by %s at %s", by, time.Now().UTC().Format(time.RFC3339)),
		ScopeType:   domain.ScopeGlobal,
		ScopeID:     "*",
		ParamsJSON:  json.RawMessage(rawSnap),
		CreatedBy:   by,
		IsActive:    false,
	}

	if err := h.store.Repo().CreatePreset(ctx, preset); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to create snapshot: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"snapshot_name": snapName,
			"values_count":  len(snapParams),
		},
	})
}

// resolveConfigSide converts a "from"/"to" descriptor into a map[string]string.
// Supported descriptors:
//   - "current"       — live effective values from the ConfigStore
//   - "preset:NAME"   — params_json of the named preset
func resolveConfigSide(ctx context.Context, descriptor string, h *ConfigHandler) (map[string]string, error) {
	if descriptor == "current" {
		regs, err := h.store.Repo().ListRegistry(ctx, "")
		if err != nil {
			return nil, err
		}
		chain := dynamic.BotScopeChain("", "", "")
		scoped := h.store.SnapshotForScope(chain)
		m := make(map[string]string, len(regs))
		for _, reg := range regs {
			m[reg.Key] = scoped.String(reg.Key)
		}
		return m, nil
	}

	if strings.HasPrefix(descriptor, "preset:") {
		presetName := strings.TrimPrefix(descriptor, "preset:")
		preset, err := h.store.Repo().GetPreset(ctx, presetName)
		if err != nil {
			return nil, fmt.Errorf("preset '%s' not found", presetName)
		}
		var params map[string]string
		if err := json.Unmarshal(preset.ParamsJSON, &params); err != nil {
			return nil, fmt.Errorf("failed to parse preset params: %w", err)
		}
		return params, nil
	}

	return nil, fmt.Errorf("unsupported descriptor '%s': use 'current' or 'preset:NAME'", descriptor)
}

// ---- JSON shape helpers -------------------------------------------------------

func registryJSON(r *domain.ParameterRegistry) gin.H {
	scopeLevels := make([]string, len(r.ScopeLevels))
	for i, sl := range r.ScopeLevels {
		scopeLevels[i] = string(sl)
	}
	return gin.H{
		"key":              r.Key,
		"type":             string(r.Type),
		"group":            r.GroupName,
		"description":      r.Description,
		"default":          r.DefaultValue,
		"min_value":        r.MinValue,
		"max_value":        r.MaxValue,
		"enum_values":      r.EnumValues,
		"requires_restart": r.RequiresRestart,
		"scope_levels":     scopeLevels,
		"deprecated":       r.Deprecated,
	}
}

func valueListJSON(vals []*domain.ParameterValue) []gin.H {
	out := make([]gin.H, 0, len(vals))
	for _, v := range vals {
		out = append(out, gin.H{
			"id":         v.ID,
			"key":        v.Key,
			"scope_type": string(v.ScopeType),
			"scope_id":   v.ScopeID,
			"value":      v.Value,
			"updated_at": v.UpdatedAt,
			"updated_by": v.UpdatedBy,
			"source":     string(v.Source),
		})
	}
	return out
}

func historyListJSON(entries []*domain.ParameterHistory) []gin.H {
	out := make([]gin.H, 0, len(entries))
	for _, h := range entries {
		out = append(out, gin.H{
			"id":            h.ID,
			"key":           h.Key,
			"scope_type":    string(h.ScopeType),
			"scope_id":      h.ScopeID,
			"old_value":     h.OldValue,
			"new_value":     h.NewValue,
			"changed_at":    h.ChangedAt,
			"changed_by":    h.ChangedBy,
			"change_reason": h.ChangeReason,
			"preset_name":   h.PresetName,
			"rollback_of":   h.RollbackOf,
		})
	}
	return out
}

func presetJSON(p *domain.ParameterPreset) gin.H {
	return gin.H{
		"id":                p.ID,
		"name":              p.Name,
		"description":       p.Description,
		"scope_type":        string(p.ScopeType),
		"scope_id":          p.ScopeID,
		"params_json":       p.ParamsJSON,
		"created_at":        p.CreatedAt,
		"created_by":        p.CreatedBy,
		"is_active":         p.IsActive,
		"last_applied_at":   p.LastAppliedAt,
		"performance_score": p.PerformanceScore,
		"category":          p.Category,
		"risk_label":        p.RiskLabel,
		"editable":          p.Editable,
	}
}
