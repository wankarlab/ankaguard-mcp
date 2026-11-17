package storage

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openTestDB opens an in-memory SQLite DB and applies the user_pair_selection
// table schema (from migration 008) manually, without running the full migration
// runner, to keep the test hermetic and fast.
func openTestDBForUPS(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)

	t.Cleanup(func() { db.Close() })

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS user_pair_selection (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id  TEXT NOT NULL DEFAULT 'default',
			exchange TEXT NOT NULL DEFAULT 'kucoin',
			mode     TEXT NOT NULL DEFAULT 'auto',
			pairs    TEXT NOT NULL DEFAULT '[]',
			overrides TEXT NOT NULL DEFAULT '{}',
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(user_id, exchange)
		)
	`)
	require.NoError(t, err, "creating user_pair_selection table")

	return db
}

func TestUserPairSelectionRepository_GetMissing_ReturnsDefault(t *testing.T) {
	db := openTestDBForUPS(t)
	repo := NewUserPairSelectionRepository(db)

	ctx := context.Background()
	sel, err := repo.Get(ctx, "user-404", "kucoin")
	require.NoError(t, err, "Get on missing row should not error")

	assert.Equal(t, "auto", sel.Mode, "default mode should be 'auto'")
	assert.Equal(t, []string{}, sel.Pairs, "default pairs should be empty slice")
	assert.NotNil(t, sel.Overrides, "default overrides should be non-nil map")
	assert.Equal(t, "user-404", sel.UserID)
	assert.Equal(t, "kucoin", sel.Exchange)
}

func TestUserPairSelectionRepository_UpsertAndGet_RoundTrip(t *testing.T) {
	db := openTestDBForUPS(t)
	repo := NewUserPairSelectionRepository(db)

	ctx := context.Background()
	want := &UserPairSelection{
		UserID:    "user-1",
		Exchange:  "kucoin",
		Mode:      "manual",
		Pairs:     []string{"BTC-USDT", "ETH-USDT", "SOL-USDT"},
		Overrides: map[string]any{"max_pairs": float64(5), "risk_pct": float64(1.5)},
	}

	err := repo.Upsert(ctx, want)
	require.NoError(t, err)

	got, err := repo.Get(ctx, "user-1", "kucoin")
	require.NoError(t, err)

	assert.Equal(t, want.Mode, got.Mode)
	assert.Equal(t, want.Pairs, got.Pairs)
	assert.Equal(t, want.Overrides["max_pairs"], got.Overrides["max_pairs"])
	assert.Equal(t, want.Overrides["risk_pct"], got.Overrides["risk_pct"])
	assert.Equal(t, want.UserID, got.UserID)
	assert.Equal(t, want.Exchange, got.Exchange)
}

func TestUserPairSelectionRepository_Upsert_ConflictUpdates(t *testing.T) {
	db := openTestDBForUPS(t)
	repo := NewUserPairSelectionRepository(db)

	ctx := context.Background()

	// First insert.
	first := &UserPairSelection{
		UserID:    "user-2",
		Exchange:  "binance",
		Mode:      "auto",
		Pairs:     []string{},
		Overrides: map[string]any{},
	}
	err := repo.Upsert(ctx, first)
	require.NoError(t, err, "first upsert")

	// Second upsert with same (user_id, exchange) — must update, not duplicate.
	second := &UserPairSelection{
		UserID:    "user-2",
		Exchange:  "binance",
		Mode:      "manual",
		Pairs:     []string{"BTC-USDT"},
		Overrides: map[string]any{"note": "updated"},
	}
	err = repo.Upsert(ctx, second)
	require.NoError(t, err, "second upsert (conflict path)")

	got, err := repo.Get(ctx, "user-2", "binance")
	require.NoError(t, err)

	// Must reflect the second upsert, not the first.
	assert.Equal(t, "manual", got.Mode)
	assert.Equal(t, []string{"BTC-USDT"}, got.Pairs)
	assert.Equal(t, "updated", got.Overrides["note"])

	// Verify only one row exists.
	var count int
	err = db.QueryRow(`SELECT COUNT(*) FROM user_pair_selection WHERE user_id='user-2' AND exchange='binance'`).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "UNIQUE conflict should update, not insert duplicate")
}

func TestUserPairSelectionRepository_MultipleUsers_Isolated(t *testing.T) {
	db := openTestDBForUPS(t)
	repo := NewUserPairSelectionRepository(db)

	ctx := context.Background()

	users := []struct {
		id   string
		mode string
	}{
		{"alice", "manual"},
		{"bob", "auto"},
		{"carol", "hybrid"},
	}
	for _, u := range users {
		err := repo.Upsert(ctx, &UserPairSelection{
			UserID:    u.id,
			Exchange:  "kucoin",
			Mode:      u.mode,
			Pairs:     []string{},
			Overrides: map[string]any{},
		})
		require.NoError(t, err)
	}

	for _, u := range users {
		got, err := repo.Get(ctx, u.id, "kucoin")
		require.NoError(t, err)
		assert.Equal(t, u.mode, got.Mode, "user %s should retain its own mode", u.id)
	}
}

func TestUserPairSelectionRepository_NilUpsert_ReturnsError(t *testing.T) {
	db := openTestDBForUPS(t)
	repo := NewUserPairSelectionRepository(db)

	err := repo.Upsert(context.Background(), nil)
	assert.Error(t, err, "upserting nil should return an error")
}

func TestUserPairSelectionRepository_HybridMode_RoundTrip(t *testing.T) {
	db := openTestDBForUPS(t)
	repo := NewUserPairSelectionRepository(db)

	ctx := context.Background()
	want := &UserPairSelection{
		UserID:    "user-hybrid",
		Exchange:  "kucoin",
		Mode:      "hybrid",
		Pairs:     []string{"BTC-USDT", "ETH-USDT"},
		Overrides: map[string]any{},
	}
	require.NoError(t, repo.Upsert(ctx, want))

	got, err := repo.Get(ctx, "user-hybrid", "kucoin")
	require.NoError(t, err)
	assert.Equal(t, "hybrid", got.Mode)
	assert.Equal(t, want.Pairs, got.Pairs)
}
