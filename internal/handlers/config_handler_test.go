// Package handlers contains HTTP handlers for the trading-mcp REST API.
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dynamic "github.com/rantipay/trading-mcp/internal/config/dynamic"
	"github.com/rantipay/trading-mcp/internal/config/dynamic/domain"
	"github.com/rantipay/trading-mcp/internal/config/dynamic/repository"
)

// ─── mock ConfigRepository ────────────────────────────────────────────────────

// mockConfigRepository is an in-memory implementation of repository.ConfigRepository
// for use in handler unit tests.
type mockConfigRepository struct {
	registry map[string]*domain.ParameterRegistry
	values   map[string]*domain.ParameterValue // composite key: "key|scope_type|scope_id"
	history  map[int64]*domain.ParameterHistory
	presets  map[string]*domain.ParameterPreset

	nextHistoryID int64

	// Inject errors per operation.
	errListRegistry  error
	errGetRegistry   error
	errListValues    error
	errListAllValues error
	errGetValue      error
	errSetValue      error
	errDeleteValue   error
	errBulkSet       error
	errListHistory   error
	errGetHistory    error
	errListPresets   error
	errGetPreset     error
	errCreatePreset  error
	errApplyPreset   error
}

func newMockConfigRepo() *mockConfigRepository {
	return &mockConfigRepository{
		registry:      make(map[string]*domain.ParameterRegistry),
		values:        make(map[string]*domain.ParameterValue),
		history:       make(map[int64]*domain.ParameterHistory),
		presets:       make(map[string]*domain.ParameterPreset),
		nextHistoryID: 1,
	}
}

func (r *mockConfigRepository) valueKey(key string, scope domain.ScopeType, scopeID string) string {
	return fmt.Sprintf("%s|%s|%s", key, string(scope), scopeID)
}

func (r *mockConfigRepository) ListRegistry(_ context.Context, group string) ([]*domain.ParameterRegistry, error) {
	if r.errListRegistry != nil {
		return nil, r.errListRegistry
	}
	out := make([]*domain.ParameterRegistry, 0, len(r.registry))
	for _, reg := range r.registry {
		if group == "" || reg.GroupName == group {
			out = append(out, reg)
		}
	}
	return out, nil
}

func (r *mockConfigRepository) GetRegistry(_ context.Context, key string) (*domain.ParameterRegistry, error) {
	if r.errGetRegistry != nil {
		return nil, r.errGetRegistry
	}
	reg, ok := r.registry[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return reg, nil
}

func (r *mockConfigRepository) UpsertRegistry(_ context.Context, reg *domain.ParameterRegistry) error {
	r.registry[reg.Key] = reg
	return nil
}

func (r *mockConfigRepository) GetValue(_ context.Context, key string, scope domain.ScopeType, scopeID string) (*domain.ParameterValue, error) {
	if r.errGetValue != nil {
		return nil, r.errGetValue
	}
	v, ok := r.values[r.valueKey(key, scope, scopeID)]
	if !ok {
		return nil, errors.New("not found")
	}
	return v, nil
}

func (r *mockConfigRepository) ListValues(_ context.Context, key string) ([]*domain.ParameterValue, error) {
	if r.errListValues != nil {
		return nil, r.errListValues
	}
	out := make([]*domain.ParameterValue, 0)
	for _, v := range r.values {
		if v.Key == key {
			out = append(out, v)
		}
	}
	return out, nil
}

func (r *mockConfigRepository) ListAllValues(_ context.Context) ([]*domain.ParameterValue, error) {
	if r.errListAllValues != nil {
		return nil, r.errListAllValues
	}
	out := make([]*domain.ParameterValue, 0, len(r.values))
	for _, v := range r.values {
		out = append(out, v)
	}
	return out, nil
}

func (r *mockConfigRepository) SetValue(_ context.Context, val *domain.ParameterValue, reason string) error {
	if r.errSetValue != nil {
		return r.errSetValue
	}
	val.UpdatedAt = time.Now()
	r.values[r.valueKey(val.Key, val.ScopeType, val.ScopeID)] = val

	// Write history row.
	id := r.nextHistoryID
	r.nextHistoryID++
	var oldVal *string
	if existing, ok := r.values[r.valueKey(val.Key, val.ScopeType, val.ScopeID)]; ok {
		s := existing.Value
		oldVal = &s
	}
	r.history[id] = &domain.ParameterHistory{
		ID:           id,
		Key:          val.Key,
		ScopeType:    val.ScopeType,
		ScopeID:      val.ScopeID,
		OldValue:     oldVal,
		NewValue:     val.Value,
		ChangedAt:    time.Now(),
		ChangedBy:    val.UpdatedBy,
		ChangeReason: reason,
	}
	return nil
}

func (r *mockConfigRepository) DeleteValue(_ context.Context, key string, scope domain.ScopeType, scopeID string, _ string) error {
	delete(r.values, r.valueKey(key, scope, scopeID))
	return nil
}

func (r *mockConfigRepository) BulkSetValues(_ context.Context, vals []*domain.ParameterValue, by, reason string) error {
	if r.errBulkSet != nil {
		return r.errBulkSet
	}
	for _, v := range vals {
		v.UpdatedAt = time.Now()
		r.values[r.valueKey(v.Key, v.ScopeType, v.ScopeID)] = v

		id := r.nextHistoryID
		r.nextHistoryID++
		r.history[id] = &domain.ParameterHistory{
			ID:           id,
			Key:          v.Key,
			ScopeType:    v.ScopeType,
			ScopeID:      v.ScopeID,
			NewValue:     v.Value,
			ChangedAt:    time.Now(),
			ChangedBy:    by,
			ChangeReason: reason,
		}
	}
	return nil
}

func (r *mockConfigRepository) ListHistory(_ context.Context, filter repository.HistoryFilter, limit int) ([]*domain.ParameterHistory, error) {
	if r.errListHistory != nil {
		return nil, r.errListHistory
	}
	out := make([]*domain.ParameterHistory, 0, len(r.history))
	for _, h := range r.history {
		if filter.Key != "" && h.Key != filter.Key {
			continue
		}
		if filter.ChangedBy != "" && h.ChangedBy != filter.ChangedBy {
			continue
		}
		out = append(out, h)
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *mockConfigRepository) GetHistory(_ context.Context, id int64) (*domain.ParameterHistory, error) {
	if r.errGetHistory != nil {
		return nil, r.errGetHistory
	}
	h, ok := r.history[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return h, nil
}

func (r *mockConfigRepository) ListPresets(_ context.Context) ([]*domain.ParameterPreset, error) {
	if r.errListPresets != nil {
		return nil, r.errListPresets
	}
	out := make([]*domain.ParameterPreset, 0, len(r.presets))
	for _, p := range r.presets {
		out = append(out, p)
	}
	return out, nil
}

func (r *mockConfigRepository) GetPreset(_ context.Context, name string) (*domain.ParameterPreset, error) {
	if r.errGetPreset != nil {
		return nil, r.errGetPreset
	}
	p, ok := r.presets[name]
	if !ok {
		return nil, errors.New("not found")
	}
	return p, nil
}

func (r *mockConfigRepository) CreatePreset(_ context.Context, preset *domain.ParameterPreset) error {
	if r.errCreatePreset != nil {
		return r.errCreatePreset
	}
	preset.CreatedAt = time.Now()
	r.presets[preset.Name] = preset
	return nil
}

func (r *mockConfigRepository) ApplyPreset(_ context.Context, name, by, reason string) (string, error) {
	if r.errApplyPreset != nil {
		return "", r.errApplyPreset
	}
	p, ok := r.presets[name]
	if !ok {
		return "", errors.New("preset not found: " + name)
	}
	// Apply the preset values.
	var params map[string]string
	if err := json.Unmarshal(p.ParamsJSON, &params); err != nil {
		return "", err
	}
	for k, v := range params {
		r.values[r.valueKey(k, domain.ScopeGlobal, "*")] = &domain.ParameterValue{
			Key:       k,
			ScopeType: domain.ScopeGlobal,
			ScopeID:   "*",
			Value:     v,
			UpdatedBy: by,
			UpdatedAt: time.Now(),
			Source:    domain.SourcePreset,
		}
		id := r.nextHistoryID
		r.nextHistoryID++
		r.history[id] = &domain.ParameterHistory{
			ID:           id,
			Key:          k,
			ScopeType:    domain.ScopeGlobal,
			ScopeID:      "*",
			NewValue:     v,
			ChangedAt:    time.Now(),
			ChangedBy:    by,
			ChangeReason: reason,
		}
	}
	now := time.Now()
	p.LastAppliedAt = &now
	p.IsActive = true
	return fmt.Sprintf("snap_%d", r.nextHistoryID), nil
}

func (r *mockConfigRepository) UpdatePresetPerformance(_ context.Context, name string, perf *domain.PresetPerformance) error {
	if p, ok := r.presets[name]; ok {
		p.PerformanceScore = perf
	}
	return nil
}

func (r *mockConfigRepository) ListPresetsByCategory(_ context.Context, category string) ([]*domain.ParameterPreset, error) {
	if r.errListPresets != nil {
		return nil, r.errListPresets
	}
	out := make([]*domain.ParameterPreset, 0, len(r.presets))
	for _, p := range r.presets {
		if p.Category == category {
			out = append(out, p)
		}
	}
	return out, nil
}

func (r *mockConfigRepository) GetActiveMode(_ context.Context) (*domain.ParameterPreset, error) {
	for _, p := range r.presets {
		if p.IsActive && p.Category == "mode" {
			return p, nil
		}
	}
	return nil, errors.New("no active mode")
}

func (r *mockConfigRepository) UpdatePreset(_ context.Context, name string, preset *domain.ParameterPreset) error {
	if _, ok := r.presets[name]; !ok {
		return errors.New("not found")
	}
	r.presets[name] = preset
	return nil
}

func (r *mockConfigRepository) DeletePreset(_ context.Context, name string) error {
	p, ok := r.presets[name]
	if !ok || !p.Editable {
		return errors.New("not found or not editable")
	}
	delete(r.presets, name)
	return nil
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func ptr(s string) *string { return &s }

func strptr(s string) *string { return &s }

// newTestStore builds a real ConfigStore backed by the mock repo.
func newTestStore(repo *mockConfigRepository) *dynamic.ConfigStore {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel) // silence output in tests

	store, err := dynamic.NewConfigStore(repo, log)
	if err != nil {
		panic("newTestStore: " + err.Error())
	}
	return store
}

// buildRouter wires ConfigHandler onto a fresh gin.Engine and returns it.
func buildRouter(store *dynamic.ConfigStore) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewConfigHandler(store)
	h.Register(r.Group("/config"))
	return r
}

// doRequest performs an HTTP request against the gin router and returns the recorder.
func doRequest(router *gin.Engine, method, path string, body interface{}) *httptest.ResponseRecorder {
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			panic(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(bodyBytes))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// parseBody decodes JSON from a recorder into a map.
func parseBody(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))
	return m
}

// seedRSIParam registers the RSI period parameter in the repo registry.
func seedRSIParam(repo *mockConfigRepository) {
	min := "2"
	max := "100"
	repo.registry["indicators.rsi.period"] = &domain.ParameterRegistry{
		Key:          "indicators.rsi.period",
		Type:         domain.ParamTypeInt,
		GroupName:    "indicators",
		Description:  "RSI lookback period",
		DefaultValue: "14",
		MinValue:     &min,
		MaxValue:     &max,
		ScopeLevels:  []domain.ScopeType{domain.ScopeGlobal},
	}
}

// seedMACDParams registers MACD fast/slow period parameters.
func seedMACDParams(repo *mockConfigRepository) {
	min1, max1 := "1", "50"
	repo.registry["indicators.macd.fast_period"] = &domain.ParameterRegistry{
		Key:          "indicators.macd.fast_period",
		Type:         domain.ParamTypeInt,
		GroupName:    "indicators",
		Description:  "MACD fast period",
		DefaultValue: "12",
		MinValue:     &min1,
		MaxValue:     &max1,
		ScopeLevels:  []domain.ScopeType{domain.ScopeGlobal},
	}
	min2, max2 := "2", "200"
	repo.registry["indicators.macd.slow_period"] = &domain.ParameterRegistry{
		Key:          "indicators.macd.slow_period",
		Type:         domain.ParamTypeInt,
		GroupName:    "indicators",
		Description:  "MACD slow period",
		DefaultValue: "26",
		MinValue:     &min2,
		MaxValue:     &max2,
		ScopeLevels:  []domain.ScopeType{domain.ScopeGlobal},
	}
}

// ─── TC-9.2 handler tests ─────────────────────────────────────────────────────

// TestListGroups verifies GET /groups returns 200 with a sorted, deduplicated group list.
func TestListGroups(t *testing.T) {
	repo := newMockConfigRepo()
	repo.registry["indicators.rsi.period"] = &domain.ParameterRegistry{Key: "indicators.rsi.period", GroupName: "indicators", Type: domain.ParamTypeInt, DefaultValue: "14"}
	repo.registry["indicators.macd.fast"] = &domain.ParameterRegistry{Key: "indicators.macd.fast", GroupName: "indicators", Type: domain.ParamTypeInt, DefaultValue: "12"}
	repo.registry["scalping.max_bots"] = &domain.ParameterRegistry{Key: "scalping.max_bots", GroupName: "scalping", Type: domain.ParamTypeInt, DefaultValue: "5"}
	repo.registry["risk.max_loss"] = &domain.ParameterRegistry{Key: "risk.max_loss", GroupName: "risk", Type: domain.ParamTypeFloat, DefaultValue: "5.0"}

	router := buildRouter(newTestStore(repo))
	w := doRequest(router, http.MethodGet, "/config/groups", nil)

	assert.Equal(t, http.StatusOK, w.Code)
	body := parseBody(t, w)
	assert.True(t, body["success"].(bool))

	data := body["data"].(map[string]interface{})
	groups := data["groups"].([]interface{})

	// Must be sorted and deduplicated.
	assert.Equal(t, float64(3), data["total"])
	groupStrs := make([]string, len(groups))
	for i, g := range groups {
		groupStrs[i] = g.(string)
	}
	assert.Equal(t, []string{"indicators", "risk", "scalping"}, groupStrs)
}

// TestGetParam_NotFound verifies GET /params/:key returns 404 for an unknown key.
func TestGetParam_NotFound(t *testing.T) {
	repo := newMockConfigRepo()
	router := buildRouter(newTestStore(repo))

	w := doRequest(router, http.MethodGet, "/config/params/unknown_key", nil)

	assert.Equal(t, http.StatusNotFound, w.Code)
	body := parseBody(t, w)
	assert.False(t, body["success"].(bool))
	assert.Contains(t, body["error"].(string), "unknown_key")
}

// TestGetParam_OK verifies GET /params/:key returns 200 with registry + values for a known key.
func TestGetParam_OK(t *testing.T) {
	repo := newMockConfigRepo()
	seedRSIParam(repo)

	router := buildRouter(newTestStore(repo))

	w := doRequest(router, http.MethodGet, "/config/params/indicators.rsi.period", nil)

	assert.Equal(t, http.StatusOK, w.Code)
	body := parseBody(t, w)
	assert.True(t, body["success"].(bool))

	data := body["data"].(map[string]interface{})
	reg := data["registry"].(map[string]interface{})
	assert.Equal(t, "indicators.rsi.period", reg["key"])
	assert.Equal(t, "int", reg["type"])

	// values and history must be present (can be empty slices).
	assert.NotNil(t, data["values"])
	assert.NotNil(t, data["history"])
	// effective_value should fall back to default when no override exists.
	assert.Equal(t, "14", data["effective_value"])
}

// TestPutParam_OK verifies PUT /params/:key with a valid value returns 200 and persists the change.
func TestPutParam_OK(t *testing.T) {
	repo := newMockConfigRepo()
	seedRSIParam(repo)

	store := newTestStore(repo)
	router := buildRouter(store)

	body := map[string]interface{}{
		"value":      "21",
		"scope_type": "global",
		"scope_id":   "*",
		"by":         "test-user",
		"reason":     "unit test",
	}
	w := doRequest(router, http.MethodPut, "/config/params/indicators.rsi.period", body)

	assert.Equal(t, http.StatusOK, w.Code)
	resp := parseBody(t, w)
	assert.True(t, resp["success"].(bool))

	data := resp["data"].(map[string]interface{})
	assert.Equal(t, "indicators.rsi.period", data["key"])
	assert.Equal(t, "21", data["new_value"])

	// Confirm the value was actually stored in the repo.
	stored, err := repo.GetValue(context.Background(), "indicators.rsi.period", domain.ScopeGlobal, "*")
	require.NoError(t, err)
	assert.Equal(t, "21", stored.Value)
}

// TestPutParam_ValidationFail verifies PUT /params/:key with a value out of range returns 400.
func TestPutParam_ValidationFail(t *testing.T) {
	repo := newMockConfigRepo()
	seedRSIParam(repo) // min=2, max=100

	router := buildRouter(newTestStore(repo))

	// Send value 200, which exceeds max=100.
	body := map[string]interface{}{
		"value": "200",
	}
	w := doRequest(router, http.MethodPut, "/config/params/indicators.rsi.period", body)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	resp := parseBody(t, w)
	assert.False(t, resp["success"].(bool))
	assert.Contains(t, resp["error"].(string), "validation")
}

// TestBulkParams_Success verifies POST /params/bulk with valid changes returns 200 with applied count.
func TestBulkParams_Success(t *testing.T) {
	repo := newMockConfigRepo()
	seedRSIParam(repo)

	// Also seed a float param for variety.
	minV, maxV := "0.0", "100.0"
	repo.registry["risk.stop_loss_pct"] = &domain.ParameterRegistry{
		Key:          "risk.stop_loss_pct",
		Type:         domain.ParamTypeFloat,
		GroupName:    "risk",
		Description:  "Stop loss percentage",
		DefaultValue: "1.5",
		MinValue:     &minV,
		MaxValue:     &maxV,
		ScopeLevels:  []domain.ScopeType{domain.ScopeGlobal},
	}

	router := buildRouter(newTestStore(repo))

	payload := map[string]interface{}{
		"by":     "bulk-test",
		"reason": "unit test bulk",
		"changes": []map[string]interface{}{
			{"key": "indicators.rsi.period", "value": "20"},
			{"key": "risk.stop_loss_pct", "value": "2.0"},
		},
	}
	w := doRequest(router, http.MethodPost, "/config/params/bulk", payload)

	assert.Equal(t, http.StatusOK, w.Code)
	resp := parseBody(t, w)
	assert.True(t, resp["success"].(bool))

	data := resp["data"].(map[string]interface{})
	assert.Equal(t, float64(2), data["applied"])
	assert.Equal(t, float64(0), data["failed"])
}

// TestBulkParams_CrossValidationFail verifies POST /params/bulk where MACD fast >= slow returns 400.
func TestBulkParams_CrossValidationFail(t *testing.T) {
	repo := newMockConfigRepo()
	seedMACDParams(repo)

	// Pre-seed valid defaults so cross-validator sees both keys.
	repo.values[repo.valueKey("indicators.macd.fast_period", domain.ScopeGlobal, "*")] = &domain.ParameterValue{
		Key: "indicators.macd.fast_period", ScopeType: domain.ScopeGlobal, ScopeID: "*", Value: "12",
	}
	repo.values[repo.valueKey("indicators.macd.slow_period", domain.ScopeGlobal, "*")] = &domain.ParameterValue{
		Key: "indicators.macd.slow_period", ScopeType: domain.ScopeGlobal, ScopeID: "*", Value: "26",
	}

	router := buildRouter(newTestStore(repo))

	// Send fast_period=30 > slow_period=26 — should trigger cross-validation failure.
	payload := map[string]interface{}{
		"by":     "cross-val-test",
		"reason": "unit test cross-param",
		"changes": []map[string]interface{}{
			{"key": "indicators.macd.fast_period", "value": "30"},
			{"key": "indicators.macd.slow_period", "value": "26"},
		},
	}
	w := doRequest(router, http.MethodPost, "/config/params/bulk", payload)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	resp := parseBody(t, w)
	assert.False(t, resp["success"].(bool))
	assert.Equal(t, "cross_param_validation_failed", resp["error"])
}

// TestRollback_OK verifies POST /rollback/:history_id with a valid entry returns 200.
func TestRollback_OK(t *testing.T) {
	repo := newMockConfigRepo()
	seedRSIParam(repo)

	// Insert a history entry that has a non-nil OldValue.
	oldVal := "14"
	repo.history[10] = &domain.ParameterHistory{
		ID:        10,
		Key:       "indicators.rsi.period",
		ScopeType: domain.ScopeGlobal,
		ScopeID:   "*",
		OldValue:  &oldVal,
		NewValue:  "21",
		ChangedBy: "test-user",
		ChangedAt: time.Now(),
	}
	// Also store a current value that would be "rolled back".
	repo.values[repo.valueKey("indicators.rsi.period", domain.ScopeGlobal, "*")] = &domain.ParameterValue{
		Key: "indicators.rsi.period", ScopeType: domain.ScopeGlobal, ScopeID: "*", Value: "21",
	}

	router := buildRouter(newTestStore(repo))

	w := doRequest(router, http.MethodPost, "/config/rollback/10", nil)

	assert.Equal(t, http.StatusOK, w.Code)
	resp := parseBody(t, w)
	assert.True(t, resp["success"].(bool))

	data := resp["data"].(map[string]interface{})
	assert.True(t, data["rolled_back"].(bool))
	assert.Equal(t, "indicators.rsi.period", data["key"])
	// After rollback the new_value should be the old value (14).
	assert.Equal(t, "14", data["new_value"])
}

// TestRollback_NoOldValue verifies POST /rollback/:history_id returns 400 when OldValue is nil.
func TestRollback_NoOldValue(t *testing.T) {
	repo := newMockConfigRepo()
	seedRSIParam(repo)

	// Insert a history entry with nil OldValue (first-ever insert).
	repo.history[5] = &domain.ParameterHistory{
		ID:        5,
		Key:       "indicators.rsi.period",
		ScopeType: domain.ScopeGlobal,
		ScopeID:   "*",
		OldValue:  nil, // nil = was an insert, no prior value
		NewValue:  "21",
		ChangedBy: "test-user",
		ChangedAt: time.Now(),
	}

	router := buildRouter(newTestStore(repo))

	w := doRequest(router, http.MethodPost, "/config/rollback/5", nil)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	resp := parseBody(t, w)
	assert.False(t, resp["success"].(bool))
	assert.Contains(t, resp["error"].(string), "no prior value")
}

// TestApplyPreset_OK verifies POST /presets/:name/apply returns 200 with applied:true.
func TestApplyPreset_OK(t *testing.T) {
	repo := newMockConfigRepo()
	seedRSIParam(repo)

	// Register the aggressive preset.
	repo.presets["aggressive"] = &domain.ParameterPreset{
		ID:         1,
		Name:       "aggressive",
		ScopeType:  domain.ScopeGlobal,
		ScopeID:    "*",
		ParamsJSON: json.RawMessage(`{"indicators.rsi.period":"7"}`),
		CreatedBy:  "test",
		CreatedAt:  time.Now(),
	}

	router := buildRouter(newTestStore(repo))

	w := doRequest(router, http.MethodPost, "/config/presets/aggressive/apply", map[string]interface{}{
		"by":     "unit-test",
		"reason": "test apply preset",
	})

	assert.Equal(t, http.StatusOK, w.Code)
	resp := parseBody(t, w)
	assert.True(t, resp["success"].(bool))

	data := resp["data"].(map[string]interface{})
	assert.True(t, data["applied"].(bool))
	assert.Equal(t, "aggressive", data["preset"])
}

// TestApplyPreset_AutoBlocked verifies POST /presets/_auto_X/apply without force header returns 403.
func TestApplyPreset_AutoBlocked(t *testing.T) {
	repo := newMockConfigRepo()
	seedRSIParam(repo)

	repo.presets["_auto_optimizer_20260101"] = &domain.ParameterPreset{
		ID:         2,
		Name:       "_auto_optimizer_20260101",
		ScopeType:  domain.ScopeGlobal,
		ScopeID:    "*",
		ParamsJSON: json.RawMessage(`{"indicators.rsi.period":"9"}`),
		CreatedBy:  "optimizer",
		CreatedAt:  time.Now(),
	}

	router := buildRouter(newTestStore(repo))

	// Request WITHOUT the X-Force-Auto-Apply header.
	w := doRequest(router, http.MethodPost, "/config/presets/_auto_optimizer_20260101/apply", map[string]interface{}{
		"by": "unit-test",
	})

	assert.Equal(t, http.StatusForbidden, w.Code)
	resp := parseBody(t, w)
	assert.False(t, resp["success"].(bool))
	assert.Equal(t, "auto_preset_blocked", resp["error"])
}

// TestApplyPreset_AutoWithForce verifies POST /presets/_auto_X/apply with X-Force-Auto-Apply:true returns 200.
func TestApplyPreset_AutoWithForce(t *testing.T) {
	repo := newMockConfigRepo()
	seedRSIParam(repo)

	autoName := "_auto_optimizer_20260102"
	repo.presets[autoName] = &domain.ParameterPreset{
		ID:         3,
		Name:       autoName,
		ScopeType:  domain.ScopeGlobal,
		ScopeID:    "*",
		ParamsJSON: json.RawMessage(`{"indicators.rsi.period":"9"}`),
		CreatedBy:  "optimizer",
		CreatedAt:  time.Now(),
	}

	router := buildRouter(newTestStore(repo))

	// Build request with the force header.
	bodyBytes, _ := json.Marshal(map[string]interface{}{"by": "unit-test"})
	req := httptest.NewRequest(http.MethodPost, "/config/presets/"+autoName+"/apply", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Force-Auto-Apply", "true")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	resp := parseBody(t, w)
	assert.True(t, resp["success"].(bool))

	data := resp["data"].(map[string]interface{})
	assert.True(t, data["applied"].(bool))
	assert.Equal(t, autoName, data["preset"])
}

// ensure strptr is used (suppress unused warning)
var _ = strptr
var _ = ptr
