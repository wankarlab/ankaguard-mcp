package productapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

type fakeResetter struct {
	holders []string
	err     error
}

func (f *fakeResetter) ResetAccount(_ context.Context, holder string) error {
	f.holders = append(f.holders, holder)
	return f.err
}

// engine simulates the JWT middleware: it copies the test's identity into the
// gin context the way KongJWTMiddleware does.
func engine(cfg Config, ident map[string]interface{}) *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	v1 := e.Group("/api/v1")
	v1.Use(func(c *gin.Context) {
		for k, v := range ident {
			c.Set(k, v)
		}
		c.Next()
	})
	if cfg.Log == nil {
		l := logrus.New()
		l.SetLevel(logrus.PanicLevel)
		cfg.Log = l
	}
	New(cfg).RegisterRoutes(v1)
	return e
}

func do(e *gin.Engine, method, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

func code(w *httptest.ResponseRecorder) string {
	var b struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &b)
	return b.Code
}

// Task 5.9: POST /api/v1/paper/accounts/:holder/reset, operator session only.
func TestPaperResetIsOperatorOnly(t *testing.T) {
	operator := map[string]interface{}{"user_id": "operator", "user_level": 4, "token_use": "operator_session"}
	agent := map[string]interface{}{"user_id": "agent:jti-1", "user_level": 1, "token_use": "agent", "mcp_profile": "paper"}
	// An agent token must stay out even if someone mints it with level 4.
	privilegedAgent := map[string]interface{}{"user_id": "agent:jti-2", "user_level": 4, "token_use": "agent"}

	t.Run("operator session resets the account", func(t *testing.T) {
		r := &fakeResetter{}
		e := engine(Config{Standalone: true, Paper: r, InitialBalance: func() float64 { return 1000 }}, operator)
		w := do(e, "POST", "/api/v1/paper/accounts/agent:jti-1/reset")
		if w.Code != 200 {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		if len(r.holders) != 1 || r.holders[0] != "agent:jti-1" {
			t.Fatalf("reset holders = %v", r.holders)
		}
		var b map[string]interface{}
		_ = json.Unmarshal(w.Body.Bytes(), &b)
		if b["holder"] != "agent:jti-1" || b["balance_usd"] != 1000.0 {
			t.Fatalf("body = %v", b)
		}
	})

	t.Run("agent token is refused with OPERATOR_REQUIRED", func(t *testing.T) {
		r := &fakeResetter{}
		e := engine(Config{Standalone: true, Paper: r}, agent)
		w := do(e, "POST", "/api/v1/paper/accounts/agent:jti-1/reset")
		if w.Code != 403 || code(w) != "OPERATOR_REQUIRED" {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		if len(r.holders) != 0 {
			t.Fatal("the account was reset by an agent token")
		}
	})

	t.Run("standalone: level 4 without an operator session is refused", func(t *testing.T) {
		r := &fakeResetter{}
		w := do(engine(Config{Standalone: true, Paper: r}, privilegedAgent), "POST", "/api/v1/paper/accounts/x/reset")
		if w.Code != 403 || len(r.holders) != 0 {
			t.Fatalf("%d %v", w.Code, r.holders)
		}
	})

	t.Run("external mode: level 4 is the operator", func(t *testing.T) {
		r := &fakeResetter{}
		w := do(engine(Config{Standalone: false, Paper: r}, map[string]interface{}{"user_id": "u", "user_level": 4}), "POST", "/api/v1/paper/accounts/x/reset")
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	})

	t.Run("no identity: 401", func(t *testing.T) {
		w := do(engine(Config{Paper: &fakeResetter{}}, nil), "POST", "/api/v1/paper/accounts/x/reset")
		if w.Code != 401 || code(w) != "UNAUTHORIZED" {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	})

	t.Run("no broker: 503 PAPER_UNAVAILABLE", func(t *testing.T) {
		w := do(engine(Config{Standalone: true}, operator), "POST", "/api/v1/paper/accounts/x/reset")
		if w.Code != 503 || code(w) != "PAPER_UNAVAILABLE" {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	})
}

type fakeLicense struct{}

func (fakeLicense) Status(context.Context) LicenseStatus {
	return LicenseStatus{Edition: "pro", State: "valid"}
}

func TestProductStatusShapeAndLicenseHook(t *testing.T) {
	ident := map[string]interface{}{"user_id": "u", "user_level": 1}

	t.Run("provisional values", func(t *testing.T) {
		e := engine(Config{AuthMode: "external", Build: "standard", LiveEnabled: func() bool { return true }}, ident)
		w := do(e, "GET", "/api/v1/product/status")
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		var b map[string]interface{}
		_ = json.Unmarshal(w.Body.Bytes(), &b)
		lic := b["license"].(map[string]interface{})
		if b["edition"] != "community" || b["build"] != "standard" || b["auth_mode"] != "external" ||
			b["live_enabled"] != true || lic["state"] != "invalid" || lic["edition"] != "community" {
			t.Fatalf("status = %v", b)
		}
		for _, k := range []string{"holder", "license_id", "expires_at", "days_remaining", "grace_days_remaining", "warning"} {
			if _, ok := lic[k]; !ok {
				t.Errorf("license.%s missing from the contract shape", k)
			}
		}
	})

	t.Run("the licence hook replaces the provisional source", func(t *testing.T) {
		e := engine(Config{License: fakeLicense{}, LiveEnabled: func() bool { return false }}, ident)
		var b map[string]interface{}
		_ = json.Unmarshal(do(e, "GET", "/api/v1/product/status").Body.Bytes(), &b)
		if b["edition"] != "pro" || b["live_enabled"] != false {
			t.Fatalf("status = %v", b)
		}
	})

	t.Run("requires an identity", func(t *testing.T) {
		if w := do(engine(Config{}, nil), "GET", "/api/v1/product/status"); w.Code != http.StatusUnauthorized {
			t.Fatalf("%d", w.Code)
		}
	})
}
