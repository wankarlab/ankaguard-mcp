package handlers

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"github.com/sirupsen/logrus"

	_ "github.com/mattn/go-sqlite3"
)

type fakeNotifier struct {
	mu    sync.Mutex
	calls []struct{ title, body string }
}

func (f *fakeNotifier) SendToUser(_ context.Context, _, title, body string, _ map[string]interface{}) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, struct{ title, body string }{title, body})
	return nil
}

func (f *fakeNotifier) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func newTestMonitor(t *testing.T) (*XSecAlertMonitor, *sql.DB, *fakeNotifier) {
	t.Helper()
	db, err := sql.Open("sqlite3", t.TempDir()+"/alerts.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	schema := []string{
		`CREATE TABLE xsec_live_book (pair TEXT PRIMARY KEY, dir INTEGER, status TEXT,
			opened_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP, entry_price REAL DEFAULT 0, notional_usd REAL DEFAULT 0)`,
		`CREATE TABLE xsec_jobs (id INTEGER PRIMARY KEY AUTOINCREMENT, mode TEXT, status TEXT,
			finished_at TIMESTAMP)`,
	}
	for _, s := range schema {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	h := &XSecHandler{db: db, log: log} // nil gctClient → enrich is a no-op
	fake := &fakeNotifier{}
	m := NewXSecAlertMonitor(h, db, fake, "owner-uuid", log)
	if err := ensureAlertStateSchema(db); err != nil {
		t.Fatalf("ensure state: %v", err)
	}
	return m, db, fake
}

func seedLeg(t *testing.T, db *sql.DB, pair string, dir int, notional float64) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO xsec_live_book (pair, dir, status, notional_usd) VALUES (?, ?, 'open', ?)`,
		pair, dir, notional); err != nil {
		t.Fatalf("seed leg: %v", err)
	}
}

func TestMonitor_Imbalance_FiresOnceThenRecovers(t *testing.T) {
	m, db, fake := newTestMonitor(t)
	ctx := context.Background()

	// Net long-heavy: long $20 vs short $5 → imbalance = 15/25 = 60% > 15%.
	seedLeg(t, db, "AAA-USDT", 1, 20)
	seedLeg(t, db, "BBB-USDT", -1, 5)

	m.tick(ctx)
	if fake.count() != 1 {
		t.Fatalf("expected 1 alert on first firing, got %d", fake.count())
	}
	if !m.stateActive("imbalance") {
		t.Fatal("imbalance state should be active after firing")
	}

	// Second tick, still firing → no re-fire (dedup).
	m.tick(ctx)
	if fake.count() != 1 {
		t.Fatalf("dedup failed: expected still 1 alert, got %d", fake.count())
	}

	// Rebalance to neutral: long $10 vs short $10 → imbalance 0 → recovered.
	if _, err := db.Exec(`UPDATE xsec_live_book SET notional_usd = 10`); err != nil {
		t.Fatal(err)
	}
	m.tick(ctx)
	if fake.count() != 2 {
		t.Fatalf("expected recovered alert, got %d total", fake.count())
	}
	if m.stateActive("imbalance") {
		t.Fatal("imbalance state should clear after recovery")
	}
}

func TestMonitor_Staleness_FiresWhenPastCadence(t *testing.T) {
	m, db, fake := newTestMonitor(t)
	ctx := context.Background()

	// Balanced book so imbalance never interferes; only staleness can fire.
	seedLeg(t, db, "AAA-USDT", 1, 10)
	seedLeg(t, db, "BBB-USDT", -1, 10)

	// Last rebalance-live 20 days ago; default cadence 10 → firing.
	if _, err := db.Exec(`INSERT INTO xsec_jobs (mode, status, finished_at)
		VALUES ('rebalance-live', 'done', datetime('now','-20 days'))`); err != nil {
		t.Fatal(err)
	}

	m.tick(ctx)
	if !m.stateActive("staleness") {
		t.Fatal("staleness should be active after 20d")
	}
	if fake.count() != 1 {
		t.Fatalf("expected 1 staleness alert, got %d", fake.count())
	}
}

func TestMonitor_JobFailure_EdgeTriggersPerNewErrorID(t *testing.T) {
	m, db, fake := newTestMonitor(t)
	ctx := context.Background()

	// First run baselines to the pre-existing error backlog WITHOUT alerting.
	if _, err := db.Exec(`INSERT INTO xsec_jobs (id, mode, status) VALUES (5, 'rebalance-live', 'error')`); err != nil {
		t.Fatal(err)
	}
	m.processJobFailure(ctx)
	if fake.count() != 0 {
		t.Fatalf("first run should baseline (no alert for backlog), got %d", fake.count())
	}

	// A NEW error after the baseline alerts once.
	if _, err := db.Exec(`INSERT INTO xsec_jobs (id, mode, status) VALUES (8, 'rebalance-live', 'error')`); err != nil {
		t.Fatal(err)
	}
	m.processJobFailure(ctx)
	if fake.count() != 1 {
		t.Fatalf("expected 1 alert on new error id, got %d", fake.count())
	}

	// Same error id → no re-fire.
	m.processJobFailure(ctx)
	if fake.count() != 1 {
		t.Fatalf("dedup failed: expected still 1, got %d", fake.count())
	}
}

func TestMonitor_Disabled_NoAlerts(t *testing.T) {
	m, db, fake := newTestMonitor(t)
	ctx := context.Background()
	seedLeg(t, db, "AAA-USDT", 1, 20)
	seedLeg(t, db, "BBB-USDT", -1, 5)
	// xsec_params already exists (LoadParams created it in the constructor).
	if _, err := db.Exec(`INSERT INTO xsec_params (key, value) VALUES ('xsec.alert.enabled','false')`); err != nil {
		t.Fatal(err)
	}
	m.tick(ctx)
	if fake.count() != 0 {
		t.Fatalf("disabled monitor should not alert, got %d", fake.count())
	}
}
