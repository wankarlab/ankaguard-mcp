package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// newLaunchTestRouter mounts the launch routes the way router.go does, with a
// stub middleware that injects user_level / user_id (as KongJWTMiddleware
// does). A nil pool exercises the in-handler checks that run before any query.
func newLaunchTestRouter(h *LaunchHandler, level int) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_level", level)
		c.Set("user_id", "tester")
		c.Next()
	})
	g := r.Group("/launch")
	g.GET("/status", h.GetStatus)
	g.GET("/gate", h.GetGate)
	g.POST("/opportunities/:id/decision", h.Decide)
	g.POST("/strategies/:id/versions", h.CreateStrategyVersion)
	g.POST("/kill-switch", h.KillSwitch)
	g.POST("/resume", h.Resume)
	g.POST("/real-mode/enable", h.EnableRealMode)
	return r
}

type launchEnvelope struct {
	Success bool `json:"success"`
	Error   *struct {
		Code string `json:"code"`
	} `json:"error"`
	Data json.RawMessage `json:"data"`
}

func launchDo(t *testing.T, r *gin.Engine, method, path, body string) (int, launchEnvelope) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var env launchEnvelope
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	return w.Code, env
}

func errCode(env launchEnvelope) string {
	if env.Error == nil {
		return ""
	}
	return env.Error.Code
}

const validDecisionBody = `{"action":"approve","category":"community"}`
const validVersionBody = `{"reason":"tune","params":{"source_kinds":["amm"],"capital_per_trade_usd":5,"take_profits":[{"gain_pct":40,"sell_pct":50}],"stop_loss_pct":35,"max_hold_s":3600,"max_slippage_pct":3,"max_pool_impact_pct":1}}`

func TestLaunchDecisionRequiresOperate(t *testing.T) {
	h := NewLaunchHandler(nil, nil, logrus.New())
	code, env := launchDo(t, newLaunchTestRouter(h, 1), http.MethodPost, "/launch/opportunities/1/decision", validDecisionBody)
	if code != http.StatusForbidden || errCode(env) != "FEATURE_ACCESS_DENIED" {
		t.Fatalf("level 1: got %d %s", code, errCode(env))
	}
	// Level 2 has launch_analyze but not launch_operate.
	code, env = launchDo(t, newLaunchTestRouter(h, 2), http.MethodPost, "/launch/opportunities/1/decision", validDecisionBody)
	if code != http.StatusForbidden {
		t.Fatalf("level 2: got %d %s", code, errCode(env))
	}
	// Level 3 passes the check and reaches the (absent) database.
	code, env = launchDo(t, newLaunchTestRouter(h, 3), http.MethodPost, "/launch/opportunities/1/decision", validDecisionBody)
	if code != http.StatusServiceUnavailable || errCode(env) != "LAUNCH_UNAVAILABLE" {
		t.Fatalf("level 3: got %d %s", code, errCode(env))
	}
}

func TestLaunchDecisionValidation(t *testing.T) {
	r := newLaunchTestRouter(NewLaunchHandler(nil, nil, logrus.New()), 3)
	code, env := launchDo(t, r, http.MethodPost, "/launch/opportunities/1/decision", `{"action":"approve","category":"astrology"}`)
	if code != http.StatusBadRequest || errCode(env) != "INVALID_CATEGORY" {
		t.Fatalf("category: got %d %s", code, errCode(env))
	}
	code, env = launchDo(t, r, http.MethodPost, "/launch/opportunities/1/decision", `{"action":"yolo","category":"team"}`)
	if code != http.StatusBadRequest || errCode(env) != "INVALID_ACTION" {
		t.Fatalf("action: got %d %s", code, errCode(env))
	}
}

func TestLaunchKillSwitchAndResumeLevels(t *testing.T) {
	h := NewLaunchHandler(nil, nil, logrus.New())
	for _, path := range []string{"/launch/kill-switch", "/launch/resume"} {
		code, env := launchDo(t, newLaunchTestRouter(h, 2), http.MethodPost, path, `{"reason":"x"}`)
		if code != http.StatusForbidden || errCode(env) != "FEATURE_ACCESS_DENIED" {
			t.Fatalf("%s level 2: got %d %s", path, code, errCode(env))
		}
		// Level 3 is allowed: it reaches the reason check, then the database.
		code, env = launchDo(t, newLaunchTestRouter(h, 3), http.MethodPost, path, `{"reason":"  "}`)
		if code != http.StatusBadRequest || errCode(env) != "REASON_REQUIRED" {
			t.Fatalf("%s empty reason: got %d %s", path, code, errCode(env))
		}
		code, env = launchDo(t, newLaunchTestRouter(h, 3), http.MethodPost, path, `{"reason":"manual stop"}`)
		if code != http.StatusServiceUnavailable || errCode(env) != "LAUNCH_UNAVAILABLE" {
			t.Fatalf("%s level 3: got %d %s", path, code, errCode(env))
		}
	}
}

func TestLaunchStrategyVersionLevels(t *testing.T) {
	h := NewLaunchHandler(nil, nil, logrus.New())
	code, env := launchDo(t, newLaunchTestRouter(h, 1), http.MethodPost, "/launch/strategies/1/versions", validVersionBody)
	if code != http.StatusForbidden || errCode(env) != "FEATURE_ACCESS_DENIED" {
		t.Fatalf("level 1: got %d %s", code, errCode(env))
	}
	// Level 2 (launch_analyze) is allowed: reaches the database check.
	code, env = launchDo(t, newLaunchTestRouter(h, 2), http.MethodPost, "/launch/strategies/1/versions", validVersionBody)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("level 2: got %d %s", code, errCode(env))
	}
	r := newLaunchTestRouter(h, 2)
	code, env = launchDo(t, r, http.MethodPost, "/launch/strategies/1/versions", `{"reason":"","params":{}}`)
	if code != http.StatusBadRequest || errCode(env) != "REASON_REQUIRED" {
		t.Fatalf("reason: got %d %s", code, errCode(env))
	}
	code, env = launchDo(t, r, http.MethodPost, "/launch/strategies/1/versions", `{"reason":"x","params":{"stop_loss_pct":0}}`)
	if code != http.StatusBadRequest || errCode(env) != "INVALID_PARAMS" {
		t.Fatalf("params: got %d %s", code, errCode(env))
	}
}

func TestLaunchRealModeAlwaysRefused(t *testing.T) {
	for _, level := range []int{0, 1, 4} {
		code, env := launchDo(t, newLaunchTestRouter(NewLaunchHandler(nil, nil, logrus.New()), level), http.MethodPost, "/launch/real-mode/enable", `{}`)
		if code != http.StatusConflict || errCode(env) != "NOT_AVAILABLE_IN_PHASE" {
			t.Fatalf("level %d: got %d %s", level, code, errCode(env))
		}
	}
}

func TestLaunchNilPoolReturns503(t *testing.T) {
	r := newLaunchTestRouter(NewLaunchHandler(nil, nil, logrus.New()), 1)
	for _, path := range []string{"/launch/status", "/launch/gate"} {
		code, env := launchDo(t, r, http.MethodGet, path, "")
		if code != http.StatusServiceUnavailable || errCode(env) != "LAUNCH_UNAVAILABLE" {
			t.Fatalf("%s: got %d %s", path, code, errCode(env))
		}
	}
}
