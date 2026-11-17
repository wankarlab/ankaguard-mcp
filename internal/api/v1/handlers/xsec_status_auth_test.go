package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	_ "github.com/mattn/go-sqlite3"
	"github.com/sirupsen/logrus"

	tradingMiddleware "github.com/rantipay/trading-mcp/internal/trading/middleware"
)

const statusTestSecret = "test-secret-for-xsec-status"

func newStatusTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	for _, q := range []string{
		`CREATE TABLE portfolio_snapshots (snapshot_time TIMESTAMP, total_value_usdt REAL)`,
		`INSERT INTO portfolio_snapshots VALUES (CURRENT_TIMESTAMP, 123.45)`,
		`CREATE TABLE xsec_equity (ts TIMESTAMP, equity REAL, n_long INTEGER, n_short INTEGER)`,
		`INSERT INTO xsec_equity VALUES (CURRENT_TIMESTAMP, 1000, 5, 5)`,
		`CREATE TABLE xsec_live_book (pair TEXT, dir INTEGER, status TEXT, opened_at TIMESTAMP, entry_price REAL, notional_usd REAL)`,
		`INSERT INTO xsec_live_book VALUES ('BCH-USDT', 1, 'open', CURRENT_TIMESTAMP, 400, 2.5)`,
		`CREATE TABLE xsec_params (key TEXT, value TEXT, updated_at TIMESTAMP)`,
		`INSERT INTO xsec_params VALUES ('k', '10', CURRENT_TIMESTAMP)`,
		`CREATE TABLE xsec_alert_state (alert_key TEXT, title TEXT, body TEXT, fired_at TIMESTAMP, active INTEGER)`,
		`INSERT INTO xsec_alert_state VALUES ('imbalance', 't', 'b', CURRENT_TIMESTAMP, 1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	h := NewXSecHandler(db, nil, logrus.New())
	r := gin.New()
	r.GET("/api/v1/xsec/status",
		tradingMiddleware.OptionalKongJWTMiddleware(statusTestSecret, "", logrus.New()),
		h.GetStatus)
	return r
}

func mintStatusJWT(t *testing.T, secret string, exp time.Duration) string {
	t.Helper()
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":        "user-1",
		"user_level": 3,
		"exp":        time.Now().Add(exp).Unix(),
	}).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

type statusData struct {
	Data struct {
		EquityHistory     []map[string]any `json:"equity_history"`
		RealEquityHistory []map[string]any `json:"real_equity_history"`
		Positions         []map[string]any `json:"positions"`
		Totals            map[string]any   `json:"totals"`
		Params            []map[string]any `json:"params"`
		ActiveAlerts      []map[string]any `json:"active_alerts"`
	} `json:"data"`
}

func getStatus(t *testing.T, r *gin.Engine, authHeader string) statusData {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/xsec/status", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (optional auth must never reject)", w.Code)
	}
	var out statusData
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func assertNoRealData(t *testing.T, s statusData) {
	t.Helper()
	d := s.Data
	if len(d.RealEquityHistory) != 0 || len(d.Positions) != 0 || len(d.ActiveAlerts) != 0 {
		t.Errorf("anonymous response leaked real data: real_equity=%d positions=%d alerts=%d",
			len(d.RealEquityHistory), len(d.Positions), len(d.ActiveAlerts))
	}
	for k, v := range d.Totals {
		if f, ok := v.(float64); ok && f != 0 {
			t.Errorf("anonymous totals.%s = %v, want 0", k, v)
		}
	}
	// Paper sections stay available.
	if len(d.EquityHistory) != 1 || len(d.Params) != 1 {
		t.Errorf("paper data missing: equity=%d params=%d", len(d.EquityHistory), len(d.Params))
	}
}

func TestXSecStatus_NoAuthHeader_OmitsRealData(t *testing.T) {
	assertNoRealData(t, getStatus(t, newStatusTestRouter(t), ""))
}

func TestXSecStatus_InvalidToken_OmitsRealData(t *testing.T) {
	r := newStatusTestRouter(t)
	assertNoRealData(t, getStatus(t, r, "Bearer not-a-jwt"))
	assertNoRealData(t, getStatus(t, r, "Bearer "+mintStatusJWT(t, "wrong-secret", time.Hour)))
	assertNoRealData(t, getStatus(t, r, "Bearer "+mintStatusJWT(t, statusTestSecret, -time.Hour)))
}

func TestXSecStatus_ValidToken_FullPayload(t *testing.T) {
	r := newStatusTestRouter(t)
	d := getStatus(t, r, "Bearer "+mintStatusJWT(t, statusTestSecret, time.Hour)).Data
	if len(d.RealEquityHistory) != 1 || len(d.Positions) != 1 || len(d.ActiveAlerts) != 1 {
		t.Errorf("authenticated payload incomplete: real_equity=%d positions=%d alerts=%d",
			len(d.RealEquityHistory), len(d.Positions), len(d.ActiveAlerts))
	}
	if len(d.EquityHistory) != 1 || len(d.Params) != 1 {
		t.Errorf("paper data missing for authed caller")
	}
}
