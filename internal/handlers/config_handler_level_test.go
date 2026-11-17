package handlers

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	dynamic "github.com/rantipay/trading-mcp/internal/config/dynamic"
	"github.com/rantipay/trading-mcp/internal/config/dynamic/domain"
)

// seedRiskParams registers the risk-sensitive keys used by the C1 tests.
func seedRiskParams(repo *mockConfigRepository) {
	min, max := "0.0", "100000.0"
	repo.registry["risk.max_order_size_usd"] = &domain.ParameterRegistry{
		Key: "risk.max_order_size_usd", Type: domain.ParamTypeFloat, GroupName: "risk",
		DefaultValue: "2.0", MinValue: &min, MaxValue: &max,
		ScopeLevels: []domain.ScopeType{domain.ScopeGlobal},
	}
	repo.registry["risk.enforce_cap_on_market_orders"] = &domain.ParameterRegistry{
		Key: "risk.enforce_cap_on_market_orders", Type: domain.ParamTypeBool, GroupName: "risk",
		DefaultValue: "true", ScopeLevels: []domain.ScopeType{domain.ScopeGlobal},
	}
	repo.registry["trading.live_enabled"] = &domain.ParameterRegistry{
		Key: "trading.live_enabled", Type: domain.ParamTypeBool, GroupName: "trading",
		DefaultValue: "false", ScopeLevels: []domain.ScopeType{domain.ScopeGlobal},
	}
}

// buildLevelRouter mounts the handler behind a fake JWT layer that sets the
// caller level the way KongJWTMiddleware does.
func buildLevelRouter(store *dynamic.ConfigStore, level int) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_level", level)
		c.Set("user_id", "tester")
		c.Next()
	})
	NewConfigHandler(store).Register(r.Group("/config"))
	return r
}

// C1: every write path that is not PUT /params/:key applies the same level-4
// floor to the risk-sensitive keys it writes. Before the fix a level-1 token
// disabled the per-order cap through a preset or a rollback.
func TestRiskKeysNeedOperatorLevelOnEveryWritePath(t *testing.T) {
	riskParams := map[string]interface{}{
		"name":   "nocap",
		"params": map[string]string{"risk.max_order_size_usd": "50", "risk.enforce_cap_on_market_orders": "false"},
	}
	newRepo := func() *mockConfigRepository {
		repo := newMockConfigRepo()
		seedRSIParam(repo)
		seedRiskParams(repo)
		repo.presets["nocap"] = &domain.ParameterPreset{
			Name: "nocap", ScopeType: domain.ScopeGlobal, ScopeID: "*", Editable: true,
			ParamsJSON: json.RawMessage(`{"risk.enforce_cap_on_market_orders":"false"}`), CreatedAt: time.Now(),
		}
		repo.presets["_auto_snap_before_nocap_20260101T000000Z"] = &domain.ParameterPreset{
			Name: "_auto_snap_before_nocap_20260101T000000Z", ScopeType: domain.ScopeGlobal, ScopeID: "*",
			ParamsJSON: json.RawMessage(`{"trading.live_enabled":"true"}`), CreatedAt: time.Now(),
		}
		old := "false"
		repo.history[7] = &domain.ParameterHistory{
			ID: 7, Key: "risk.enforce_cap_on_market_orders", ScopeType: domain.ScopeGlobal, ScopeID: "*",
			OldValue: &old, NewValue: "true", ChangedAt: time.Now(),
		}
		return repo
	}
	cases := []struct {
		name, method, path string
		body               interface{}
	}{
		{"create preset", http.MethodPost, "/config/presets", riskParams},
		{"update preset", http.MethodPut, "/config/presets/nocap", riskParams},
		{"apply preset", http.MethodPost, "/config/presets/nocap/apply", map[string]string{}},
		{"rollback change", http.MethodPost, "/config/rollback/7", nil},
		{"rollback preset", http.MethodPost, "/config/presets/nocap/rollback", nil},
		{"put live_enabled", http.MethodPut, "/config/params/trading.live_enabled", map[string]string{"value": "true"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newRepo()
			w := doRequest(buildLevelRouter(newTestStore(repo), 1), tc.method, tc.path, tc.body)
			assert.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
			// Nothing was written.
			for _, k := range []string{"risk.max_order_size_usd", "risk.enforce_cap_on_market_orders", "trading.live_enabled"} {
				_, wrote := repo.values[repo.valueKey(k, domain.ScopeGlobal, "*")]
				assert.False(t, wrote, "%s written by a level-1 caller", k)
			}

			// The operator level still can.
			repo = newRepo()
			w = doRequest(buildLevelRouter(newTestStore(repo), 4), tc.method, tc.path, tc.body)
			assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
		})
	}
}

// Non-sensitive presets keep working at level 1 (unchanged behaviour).
func TestNonRiskPresetStillLevelOne(t *testing.T) {
	repo := newMockConfigRepo()
	seedRSIParam(repo)
	repo.presets["fast"] = &domain.ParameterPreset{
		Name: "fast", ScopeType: domain.ScopeGlobal, ScopeID: "*",
		ParamsJSON: json.RawMessage(`{"indicators.rsi.period":"7"}`), CreatedAt: time.Now(),
	}
	w := doRequest(buildLevelRouter(newTestStore(repo), 1), http.MethodPost, "/config/presets/fast/apply", map[string]string{})
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

// trading.live_enabled is a risk-sensitive key (real-money master switch).
func TestLiveEnabledIsRiskSensitive(t *testing.T) {
	assert.True(t, isRiskSensitiveConfigKey("trading.live_enabled"))
	assert.True(t, isRiskSensitiveConfigKey("risk.max_order_size_usd"))
	assert.False(t, isRiskSensitiveConfigKey("indicators.rsi.period"))
}
