package handlers

import (
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"
	"github.com/sirupsen/logrus"

	tradingMiddleware "github.com/rantipay/trading-mcp/internal/trading/middleware"
)

const equityTestSecret = "test-secret-for-equity-history"

// newEquityTestRouter mounts the handler behind the strict JWT middleware.
func newEquityTestRouter(t *testing.T) (*gin.Engine, *sql.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE portfolio_snapshots (snapshot_time TIMESTAMP, total_value_usdt REAL)`); err != nil {
		t.Fatal(err)
	}
	h := NewAccountEquityHandler(db, logrus.New())
	r := gin.New()
	g := r.Group("/api/v1")
	g.Use(tradingMiddleware.KongJWTMiddleware(equityTestSecret, "", logrus.New()))
	g.GET("/account/equity-history", h.GetEquityHistory)
	return r, db
}

func seedEquity(t *testing.T, db *sql.DB, ts time.Time, v float64) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO portfolio_snapshots VALUES (?, ?)`, ts.UTC(), v); err != nil {
		t.Fatal(err)
	}
}

type equityBody struct {
	Data struct {
		Range       string `json:"range"`
		TotalPoints int    `json:"total_points"`
		Reduced     bool   `json:"reduced"`
		Points      []struct {
			TotalEquityUSD float64 `json:"total_equity_usd"`
		} `json:"points"`
		Metrics *struct {
			Current            float64  `json:"current"`
			Peak               float64  `json:"peak"`
			MaxDrawdownPct     float64  `json:"max_drawdown_pct"`
			CurrentDrawdownPct float64  `json:"current_drawdown_pct"`
			ChangeAbs          float64  `json:"change_abs"`
			ChangePct          *float64 `json:"change_pct"`
		} `json:"metrics"`
	} `json:"data"`
}

func getEquity(t *testing.T, r *gin.Engine, query, auth string) (int, equityBody) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/account/equity-history"+query, nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var b equityBody
	_ = json.Unmarshal(w.Body.Bytes(), &b)
	return w.Code, b
}

func TestEquityHistoryAnonymous401(t *testing.T) {
	r, _ := newEquityTestRouter(t)
	if code, _ := getEquity(t, r, "", ""); code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", code)
	}
}

func TestEquityHistoryInvalidToken401(t *testing.T) {
	r, _ := newEquityTestRouter(t)
	if code, _ := getEquity(t, r, "", "Bearer not-a-jwt"); code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", code)
	}
	bad := mintStatusJWT(t, "another-secret", time.Hour)
	if code, _ := getEquity(t, r, "", "Bearer "+bad); code != http.StatusUnauthorized {
		t.Fatalf("want 401 for wrong-secret token, got %d", code)
	}
}

func TestEquityHistoryInvalidRange400(t *testing.T) {
	r, _ := newEquityTestRouter(t)
	tok := mintStatusJWT(t, equityTestSecret, time.Hour)
	if code, _ := getEquity(t, r, "?range=5y", "Bearer "+tok); code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", code)
	}
}

func TestEquityHistoryEmptyHasNullMetrics(t *testing.T) {
	r, _ := newEquityTestRouter(t)
	tok := mintStatusJWT(t, equityTestSecret, time.Hour)
	code, b := getEquity(t, r, "", "Bearer "+tok)
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d", code)
	}
	if b.Data.Points == nil || len(b.Data.Points) != 0 {
		t.Fatalf("want empty (non-null) points, got %v", b.Data.Points)
	}
	if b.Data.Metrics != nil {
		t.Fatalf("want null metrics, got %+v", b.Data.Metrics)
	}
	if b.Data.Range != "7d" {
		t.Fatalf("want default range 7d, got %q", b.Data.Range)
	}
}

func TestEquityHistoryDrawdownSeeded(t *testing.T) {
	r, db := newEquityTestRouter(t)
	tok := mintStatusJWT(t, equityTestSecret, time.Hour)
	now := time.Now()
	// 100 -> 120 (peak) -> 90 (trough, -25%) -> 110 (current)
	for i, v := range []float64{100, 120, 90, 110} {
		seedEquity(t, db, now.Add(time.Duration(i-5)*time.Hour), v)
	}
	// Outside the 24h window: must be ignored.
	seedEquity(t, db, now.Add(-72*time.Hour), 1000)

	code, b := getEquity(t, r, "?range=24h", "Bearer "+tok)
	if code != http.StatusOK || b.Data.Metrics == nil {
		t.Fatalf("code=%d metrics=%v", code, b.Data.Metrics)
	}
	m := b.Data.Metrics
	near := func(got, want float64) bool { return math.Abs(got-want) < 1e-6 }
	if len(b.Data.Points) != 4 || b.Data.Reduced {
		t.Fatalf("want 4 unreduced points, got %d", len(b.Data.Points))
	}
	if !near(m.Peak, 120) || !near(m.MaxDrawdownPct, 25) || !near(m.Current, 110) ||
		!near(m.CurrentDrawdownPct, 100.0/12) || !near(m.ChangeAbs, 10) {
		t.Fatalf("unexpected metrics: %+v", m)
	}
	if m.ChangePct == nil || !near(*m.ChangePct, 10) {
		t.Fatalf("unexpected change_pct: %v", m.ChangePct)
	}
}

func TestEquityHistoryReducesKeepingExtremes(t *testing.T) {
	r, db := newEquityTestRouter(t)
	tok := mintStatusJWT(t, equityTestSecret, time.Hour)
	start := time.Now().Add(-3000 * time.Hour)
	tx, _ := db.Begin()
	for i := 0; i < 2000; i++ {
		v := 100 + float64(i%7)
		switch i {
		case 900:
			v = 500 // peak
		case 1300:
			v = 10 // valley
		}
		if _, err := tx.Exec(`INSERT INTO portfolio_snapshots VALUES (?, ?)`,
			start.Add(time.Duration(i)*time.Hour).UTC(), v); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	code, b := getEquity(t, r, "?range=all", "Bearer "+tok)
	if code != http.StatusOK {
		t.Fatalf("code %d", code)
	}
	if n := len(b.Data.Points); n > 500 || n == 0 || !b.Data.Reduced || b.Data.TotalPoints != 2000 {
		t.Fatalf("points=%d reduced=%v total=%d", n, b.Data.Reduced, b.Data.TotalPoints)
	}
	var hasPeak, hasValley bool
	for _, p := range b.Data.Points {
		hasPeak = hasPeak || p.TotalEquityUSD == 500
		hasValley = hasValley || p.TotalEquityUSD == 10
	}
	if !hasPeak || !hasValley {
		t.Fatalf("reduction lost extremes: peak=%v valley=%v", hasPeak, hasValley)
	}
	if b.Data.Metrics == nil || b.Data.Metrics.Peak != 500 || b.Data.Metrics.MaxDrawdownPct < 97 {
		t.Fatalf("metrics must come from the full set: %+v", b.Data.Metrics)
	}
}
