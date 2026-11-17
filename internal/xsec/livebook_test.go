package xsec

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// newLiveBookTestDB opens a fresh in-memory SQLite DB for the live-book tests.
// The schema is created lazily by the package functions under test, so no
// setup beyond opening the connection is needed here.
func newLiveBookTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestUpsertLiveBookEntry_CreatesAndUpdates(t *testing.T) {
	db := newLiveBookTestDB(t)

	if err := UpsertLiveBookEntry(db, "BTC-USDT", +1, 62000.50, 5.0); err != nil {
		t.Fatalf("upsert (create): %v", err)
	}
	pairs, err := ActiveLiveBookPairs(db)
	if err != nil {
		t.Fatalf("active pairs: %v", err)
	}
	if dir, ok := pairs["BTC-USDT"]; !ok || dir != +1 {
		t.Fatalf("expected BTC-USDT dir +1, got %v (ok=%v)", dir, ok)
	}

	// Update: same pair, flipped direction (e.g. a flip's re-entry).
	if err := UpsertLiveBookEntry(db, "BTC-USDT", -1, 61500.00, 5.0); err != nil {
		t.Fatalf("upsert (update): %v", err)
	}
	pairs, err = ActiveLiveBookPairs(db)
	if err != nil {
		t.Fatalf("active pairs after update: %v", err)
	}
	if dir, ok := pairs["BTC-USDT"]; !ok || dir != -1 {
		t.Fatalf("expected BTC-USDT dir -1 after update, got %v (ok=%v)", dir, ok)
	}
	if len(pairs) != 1 {
		t.Fatalf("expected exactly 1 active pair, got %d", len(pairs))
	}
}

func TestCloseLiveBookEntry_MarksClosed(t *testing.T) {
	db := newLiveBookTestDB(t)

	if err := UpsertLiveBookEntry(db, "ETH-USDT", +1, 3200.00, 4.0); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := CloseLiveBookEntry(db, "ETH-USDT"); err != nil {
		t.Fatalf("close: %v", err)
	}
	pairs, err := ActiveLiveBookPairs(db)
	if err != nil {
		t.Fatalf("active pairs: %v", err)
	}
	if _, ok := pairs["ETH-USDT"]; ok {
		t.Fatalf("expected ETH-USDT to be absent from active pairs after close, got dir=%v", pairs["ETH-USDT"])
	}
}

func TestActiveLiveBookPairs_OnlyReturnsOpenRows(t *testing.T) {
	db := newLiveBookTestDB(t)

	if err := UpsertLiveBookEntry(db, "SOL-USDT", +1, 150.25, 3.0); err != nil {
		t.Fatalf("upsert SOL: %v", err)
	}
	if err := UpsertLiveBookEntry(db, "AVAX-USDT", -1, 28.10, 3.0); err != nil {
		t.Fatalf("upsert AVAX: %v", err)
	}
	if err := CloseLiveBookEntry(db, "AVAX-USDT"); err != nil {
		t.Fatalf("close AVAX: %v", err)
	}

	pairs, err := ActiveLiveBookPairs(db)
	if err != nil {
		t.Fatalf("active pairs: %v", err)
	}
	if len(pairs) != 1 {
		t.Fatalf("expected exactly 1 open pair, got %d: %v", len(pairs), pairs)
	}
	if dir, ok := pairs["SOL-USDT"]; !ok || dir != +1 {
		t.Fatalf("expected SOL-USDT dir +1, got %v (ok=%v)", dir, ok)
	}
	if _, ok := pairs["AVAX-USDT"]; ok {
		t.Fatalf("AVAX-USDT should not be in the active set (closed)")
	}
}

// TestUpsertLiveBookEntry_PersistsEntryPriceAndNotional guards against the
// original bug (found 2026-07-23): entry_price/notional_usd previously did
// not exist in the schema at all, so GetStatus always rendered $0.00 for
// every leg regardless of the real position. This reads the raw columns
// back (ActiveLiveBookPairs doesn't expose them) to confirm both a fresh
// insert and an update (resize) persist the real numbers.
func TestUpsertLiveBookEntry_PersistsEntryPriceAndNotional(t *testing.T) {
	db := newLiveBookTestDB(t)

	if err := UpsertLiveBookEntry(db, "BTC-USDT", +1, 62000.50, 5.0); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	var entry, notional float64
	if err := db.QueryRow(`SELECT entry_price, notional_usd FROM xsec_live_book WHERE pair = ?`, "BTC-USDT").
		Scan(&entry, &notional); err != nil {
		t.Fatalf("read back entry_price/notional_usd: %v", err)
	}
	if entry != 62000.50 || notional != 5.0 {
		t.Fatalf("expected entry=62000.50 notional=5.0, got entry=%v notional=%v", entry, notional)
	}

	// A resize overwrites both — the row must reflect the CURRENT size, not
	// the original one.
	if err := UpsertLiveBookEntry(db, "BTC-USDT", +1, 63100.00, 8.0); err != nil {
		t.Fatalf("upsert (resize): %v", err)
	}
	if err := db.QueryRow(`SELECT entry_price, notional_usd FROM xsec_live_book WHERE pair = ?`, "BTC-USDT").
		Scan(&entry, &notional); err != nil {
		t.Fatalf("read back after resize: %v", err)
	}
	if entry != 63100.00 || notional != 8.0 {
		t.Fatalf("expected entry=63100.00 notional=8.0 after resize, got entry=%v notional=%v", entry, notional)
	}
}

func TestCloseLiveBookEntry_MissingPairIsNoop(t *testing.T) {
	db := newLiveBookTestDB(t)

	if err := CloseLiveBookEntry(db, "DOES-NOTEXIST"); err != nil {
		t.Fatalf("closing a missing pair should be a no-op, got error: %v", err)
	}
	pairs, err := ActiveLiveBookPairs(db)
	if err != nil {
		t.Fatalf("active pairs: %v", err)
	}
	if len(pairs) != 0 {
		t.Fatalf("expected empty registry, got %v", pairs)
	}
}
