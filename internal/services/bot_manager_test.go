package services

// MU-8.2: BotManager lifecycle and risk-guard tests.
//
// Test strategy:
//   - All tests use dry_run=true (default) to avoid live order paths.
//   - StartBot → StartWithContext requires gctClient.IsConnected()=true;
//     we set this expectation on MockGCTClient (defined in trading_engine_test.go).
//   - The bot's goroutine loop is started but does nothing dangerous because
//     dry_run=true and GCTClient methods are unregistered (will panic if called),
//     so we stop bots promptly after each assertion.
//   - validateUserConfig is tested independently without spinning up a full bot.
//   - circuit breaker is tested via RecordFailure/ResetFailures without starting
//     a real bot goroutine (only the counter logic matters for the gate check).

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ─── Fakes ────────────────────────────────────────────────────────────────────

// fakeConfigStore is an in-memory UserBotConfigStore for tests.
type fakeConfigStore struct {
	cfgs map[string]*UserBotCfg // key: userID + ":" + exchange
}

func newFakeConfigStore() *fakeConfigStore {
	return &fakeConfigStore{cfgs: make(map[string]*UserBotCfg)}
}

func (s *fakeConfigStore) Get(_ context.Context, userID, exchange string) (*UserBotCfg, error) {
	key := userID + ":" + exchange
	if c, ok := s.cfgs[key]; ok {
		return c, nil
	}
	// Return safe default when not found.
	return defaultTestUserCfg(userID, exchange), nil
}

func (s *fakeConfigStore) Upsert(_ context.Context, cfg *UserBotCfg) error {
	key := cfg.UserID + ":" + cfg.Exchange
	s.cfgs[key] = cfg
	return nil
}

// erroringConfigStore always fails Get.
type erroringConfigStore struct{}

func (e *erroringConfigStore) Get(_ context.Context, _, _ string) (*UserBotCfg, error) {
	return nil, fmt.Errorf("db unavailable")
}
func (e *erroringConfigStore) Upsert(_ context.Context, _ *UserBotCfg) error {
	return fmt.Errorf("db unavailable")
}

// defaultTestUserCfg returns a safe, dry-run UserBotCfg that passes validateUserConfig.
func defaultTestUserCfg(userID, exchange string) *UserBotCfg {
	return &UserBotCfg{
		UserID:              userID,
		Exchange:            exchange,
		Enabled:             true,
		TradeSizeUSD:        2.0,
		Leverage:            5,
		MarketType:          "spot",
		ModeSelection:       "AUTO",
		ConfidenceThreshold: 0.70,
		MaxDailyLossUSD:     10.0,
		MaxOpenPositions:    2,
		DryRun:              true,
	}
}

// fakeBotCredentialLoader returns valid credentials for any user so the
// SessionManager can build ExchangeSessions during StartBot.
type fakeBotCredentialLoader struct{}

func (f *fakeBotCredentialLoader) GetCredential(_ context.Context, userID, _ string) (gct.DecryptedExchangeCredential, error) {
	return gct.DecryptedExchangeCredential{
		APIKey:     userID + "-test-key",
		APISecret:  userID + "-test-secret",
		Passphrase: userID + "-test-pass",
	}, nil
}

// newTestSessionManager returns a SessionManager wired with a fake credential loader.
func newTestSessionManager() *gct.SessionManager {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	return gct.NewSessionManager(&fakeBotCredentialLoader{}, 0, log)
}

// connectedMockGCT returns a MockGCTClient whose IsConnected() always returns true.
// This satisfies the gate inside AITradingBot.StartWithContext.
func connectedMockGCT() *MockGCTClient {
	m := new(MockGCTClient)
	m.On("IsConnected").Return(true)
	return m
}

// newTestBotManager creates a BotManager with safe fakes for unit tests.
func newTestBotManager(maxBots int) *BotManager {
	return NewBotManager(
		newFakeConfigStore(),
		newTestSessionManager(),
		nil, // pairCoord: nil disables cross-component guard
		BotDependencies{
			GCTClient: connectedMockGCT(),
		},
		maxBots,
		createTestLogger(),
	)
}

// stopAll is a cleanup helper that stops all bots registered in the given manager.
func stopAll(bm *BotManager) {
	for _, uid := range bm.ListActive() {
		_ = bm.StopBot(uid)
	}
}

// ─── NewBotManager construction ───────────────────────────────────────────────

func TestNewBotManager_DefaultMaxBots(t *testing.T) {
	bm := newTestBotManager(0) // 0 → default 5
	defer bm.Shutdown()
	assert.Equal(t, 5, bm.maxBots)
}

func TestNewBotManager_PanicsOnNilCfgRepo(t *testing.T) {
	assert.Panics(t, func() {
		NewBotManager(nil, newTestSessionManager(), nil, BotDependencies{}, 5, createTestLogger())
	})
}

func TestNewBotManager_PanicsOnNilSessionMgr(t *testing.T) {
	assert.Panics(t, func() {
		NewBotManager(newFakeConfigStore(), nil, nil, BotDependencies{}, 5, createTestLogger())
	})
}

// ─── StartBot + ListActive + GetStatus ───────────────────────────────────────

func TestBotManager_StartBot_ListActive_GetStatus(t *testing.T) {
	bm := newTestBotManager(5)
	defer stopAll(bm)

	err := bm.StartBot(context.Background(), "alice")
	require.NoError(t, err, "StartBot should succeed for a valid dry-run config")

	active := bm.ListActive()
	require.Len(t, active, 1)
	assert.Equal(t, "alice", active[0])

	status := bm.GetStatus("alice")
	require.NotNil(t, status)
	assert.Equal(t, "alice", status["user_id"])
}

func TestBotManager_StartBot_Idempotent(t *testing.T) {
	bm := newTestBotManager(5)
	defer stopAll(bm)

	require.NoError(t, bm.StartBot(context.Background(), "alice"))
	// Second call for the same user must be a no-op, not an error.
	err := bm.StartBot(context.Background(), "alice")
	assert.NoError(t, err, "second StartBot for same user should be idempotent")

	// Only one entry in the active map.
	assert.Len(t, bm.ListActive(), 1)
}

func TestBotManager_GetStatus_ReturnsNilForUnknownUser(t *testing.T) {
	bm := newTestBotManager(5)
	defer bm.Shutdown()

	status := bm.GetStatus("nobody")
	assert.Nil(t, status)
}

// ─── maxBots enforcement ──────────────────────────────────────────────────────

func TestBotManager_StartBot_RejectsWhenCapReached(t *testing.T) {
	const cap = 2
	bm := newTestBotManager(cap)
	defer stopAll(bm)

	for i := 0; i < cap; i++ {
		uid := fmt.Sprintf("user-%d", i)
		require.NoError(t, bm.StartBot(context.Background(), uid),
			"should start bot for %s within cap", uid)
	}

	// One more should be rejected.
	err := bm.StartBot(context.Background(), "user-overflow")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max concurrent bots")
	assert.Len(t, bm.ListActive(), cap)
}

// ─── StopBot lifecycle ────────────────────────────────────────────────────────

func TestBotManager_StopBot_RemovesFromActiveSet(t *testing.T) {
	bm := newTestBotManager(5)

	require.NoError(t, bm.StartBot(context.Background(), "alice"))
	assert.Len(t, bm.ListActive(), 1)

	err := bm.StopBot("alice")
	assert.NoError(t, err)
	assert.Len(t, bm.ListActive(), 0)
	assert.Nil(t, bm.GetStatus("alice"))
}

func TestBotManager_StopBot_Idempotent(t *testing.T) {
	bm := newTestBotManager(5)

	require.NoError(t, bm.StartBot(context.Background(), "alice"))
	require.NoError(t, bm.StopBot("alice"))
	// Stopping again should be a no-op (not an error).
	assert.NoError(t, bm.StopBot("alice"))
}

func TestBotManager_StopBot_ReturnsErrorOnEmptyUserID(t *testing.T) {
	bm := newTestBotManager(5)
	defer bm.Shutdown()
	assert.Error(t, bm.StopBot(""))
}

func TestBotManager_EmergencyStop_RemovesFromActiveSet(t *testing.T) {
	bm := newTestBotManager(5)

	require.NoError(t, bm.StartBot(context.Background(), "alice"))
	assert.Len(t, bm.ListActive(), 1)

	// No open positions in the test bot, so the close pass is a no-op; the bot
	// must still be stopped and removed from the active set.
	err := bm.EmergencyStop(context.Background(), "alice")
	assert.NoError(t, err)
	assert.Len(t, bm.ListActive(), 0)
	assert.Nil(t, bm.GetStatus("alice"))
}

func TestBotManager_EmergencyStop_Idempotent(t *testing.T) {
	bm := newTestBotManager(5)

	require.NoError(t, bm.StartBot(context.Background(), "alice"))
	require.NoError(t, bm.EmergencyStop(context.Background(), "alice"))
	// Emergency-stopping again should be a no-op (not an error).
	assert.NoError(t, bm.EmergencyStop(context.Background(), "alice"))
}

func TestBotManager_EmergencyStop_ReturnsErrorOnEmptyUserID(t *testing.T) {
	bm := newTestBotManager(5)
	defer bm.Shutdown()
	assert.Error(t, bm.EmergencyStop(context.Background(), ""))
}

func TestBotManager_StartStop_StartAgain_NoLeak(t *testing.T) {
	bm := newTestBotManager(5)
	defer bm.Shutdown()

	require.NoError(t, bm.StartBot(context.Background(), "alice"))
	require.NoError(t, bm.StopBot("alice"))

	// Re-starting the same user after stop should succeed (no stale map entry).
	err := bm.StartBot(context.Background(), "alice")
	assert.NoError(t, err, "start → stop → start cycle must work without leaking map entries")
	_ = bm.StopBot("alice")
}

// ─── Multi-user coexistence ───────────────────────────────────────────────────

func TestBotManager_MultipleUserBots_CoexistIndependently(t *testing.T) {
	bm := newTestBotManager(5)
	defer stopAll(bm)

	users := []string{"alice", "bob", "charlie"}
	for _, uid := range users {
		require.NoError(t, bm.StartBot(context.Background(), uid))
	}

	active := bm.ListActive()
	sort.Strings(active)
	sort.Strings(users)
	assert.Equal(t, users, active)

	// Each user has a distinct status entry.
	for _, uid := range users {
		s := bm.GetStatus(uid)
		require.NotNil(t, s)
		assert.Equal(t, uid, s["user_id"])
	}
}

// ─── StartBot error paths ─────────────────────────────────────────────────────

func TestBotManager_StartBot_RejectsEmptyUserID(t *testing.T) {
	bm := newTestBotManager(5)
	defer bm.Shutdown()
	assert.Error(t, bm.StartBot(context.Background(), ""))
}

func TestBotManager_StartBot_PropagatesConfigStoreError(t *testing.T) {
	// Wire a broken config store so Get always fails.
	bm := NewBotManager(
		&erroringConfigStore{},
		newTestSessionManager(),
		nil,
		BotDependencies{GCTClient: connectedMockGCT()},
		5,
		createTestLogger(),
	)
	defer bm.Shutdown()

	err := bm.StartBot(context.Background(), "alice")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load config")
}

func TestBotManager_StartBot_RejectsInvalidConfig(t *testing.T) {
	store := newFakeConfigStore()
	// Insert a config that fails validateUserConfig (trade size above cap).
	badCfg := defaultTestUserCfg("alice", "kucoin")
	badCfg.TradeSizeUSD = MaxTradeSizeUSD + 1.0
	_ = store.Upsert(context.Background(), badCfg)

	bm := NewBotManager(
		store,
		newTestSessionManager(),
		nil,
		BotDependencies{GCTClient: connectedMockGCT()},
		5,
		createTestLogger(),
	)
	defer bm.Shutdown()

	err := bm.StartBot(context.Background(), "alice")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "config rejected")
}

// ─── validateUserConfig — pure risk-guard tests ────────────────────────────────

func TestValidateUserConfig_AcceptsValidConfig(t *testing.T) {
	cfg := defaultTestUserCfg("alice", "kucoin")
	assert.NoError(t, validateUserConfig(cfg))
}

func TestValidateUserConfig_RejectsNilConfig(t *testing.T) {
	assert.Error(t, validateUserConfig(nil))
}

func TestValidateUserConfig_RejectsTradeSizeTooLow(t *testing.T) {
	cfg := defaultTestUserCfg("alice", "kucoin")
	cfg.TradeSizeUSD = MinTradeSizeUSD - 0.01
	err := validateUserConfig(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "below minimum")
}

func TestValidateUserConfig_RejectsTradeSizeTooHigh(t *testing.T) {
	cfg := defaultTestUserCfg("alice", "kucoin")
	cfg.TradeSizeUSD = MaxTradeSizeUSD + 1.0
	err := validateUserConfig(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds server hard cap")
}

func TestValidateUserConfig_AcceptsTradeSizeAtBoundaries(t *testing.T) {
	for _, size := range []float64{MinTradeSizeUSD, MaxTradeSizeUSD} {
		cfg := defaultTestUserCfg("alice", "kucoin")
		cfg.TradeSizeUSD = size
		assert.NoError(t, validateUserConfig(cfg), "size %.2f should be accepted", size)
	}
}

func TestValidateUserConfig_RejectsDailyLossOverCap(t *testing.T) {
	cfg := defaultTestUserCfg("alice", "kucoin")
	cfg.MaxDailyLossUSD = MaxDailyLossCap + 0.01
	err := validateUserConfig(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max_daily_loss_usd")
}

func TestValidateUserConfig_AcceptsDailyLossAtCap(t *testing.T) {
	cfg := defaultTestUserCfg("alice", "kucoin")
	cfg.MaxDailyLossUSD = MaxDailyLossCap
	assert.NoError(t, validateUserConfig(cfg))
}

func TestValidateUserConfig_RejectsOpenPositionsBelowMin(t *testing.T) {
	// MinOpenPositions = 1: the only non-zero value below the minimum would be a
	// negative number. The production guard is `cfg.MaxOpenPositions != 0` so zero
	// is treated as "unset". We verify a negative value (unusual but possible via
	// direct API manipulation) triggers the error path.
	cfg := defaultTestUserCfg("alice", "kucoin")
	cfg.MaxOpenPositions = -1 // clearly below MinOpenPositions (1) and non-zero
	err := validateUserConfig(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max_open_positions")
}

func TestValidateUserConfig_RejectsOpenPositionsOverCap(t *testing.T) {
	cfg := defaultTestUserCfg("alice", "kucoin")
	cfg.MaxOpenPositions = MaxOpenPositionsCap + 1
	err := validateUserConfig(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max_open_positions")
}

func TestValidateUserConfig_RejectsLeverageBelowMin(t *testing.T) {
	// MinLeverage = 1: the production guard skips validation for zero (treated as
	// "unset / use default"). A negative leverage is the only non-zero value below
	// the minimum and should be caught.
	cfg := defaultTestUserCfg("alice", "kucoin")
	cfg.Leverage = -1 // non-zero and below MinLeverage (1)
	err := validateUserConfig(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "leverage")
}

func TestValidateUserConfig_RejectsLeverageOverCap(t *testing.T) {
	cfg := defaultTestUserCfg("alice", "kucoin")
	cfg.Leverage = MaxLeverageCap + 1
	err := validateUserConfig(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "leverage")
}

func TestValidateUserConfig_AcceptsLeverageAtBoundaries(t *testing.T) {
	for _, lev := range []int{MinLeverage, MaxLeverageCap} {
		cfg := defaultTestUserCfg("alice", "kucoin")
		cfg.Leverage = lev
		assert.NoError(t, validateUserConfig(cfg), "leverage %d should be accepted", lev)
	}
}

func TestValidateUserConfig_ZeroFieldsNotValidated(t *testing.T) {
	// Zero values for optional fields (trade size, leverage, max open positions)
	// are left for defaults and must not trigger validation errors.
	cfg := &UserBotCfg{
		UserID:           "alice",
		Exchange:         "kucoin",
		TradeSizeUSD:     0, // zero → not validated
		Leverage:         0, // zero → not validated
		MaxOpenPositions: 0, // zero → not validated
		MaxDailyLossUSD:  0, // zero → not validated
		DryRun:           true,
	}
	assert.NoError(t, validateUserConfig(cfg))
}

// ─── Circuit breaker: RecordFailure / ResetFailures / StartBot gate ───────────

func TestCircuitBreaker_RecordFailureIncrements(t *testing.T) {
	bm := newTestBotManager(5)
	defer bm.Shutdown()

	for i := 0; i < maxConsecutiveFailures-1; i++ {
		bm.RecordFailure("alice")
		assert.Equal(t, i+1, bm.failureCounts["alice"])
	}
}

func TestCircuitBreaker_StartsBlockedAfterThreshold(t *testing.T) {
	bm := newTestBotManager(5)
	defer bm.Shutdown()

	// Trip the circuit breaker manually (no running bot, so StopBot in RecordFailure is a no-op).
	for i := 0; i < maxConsecutiveFailures; i++ {
		bm.RecordFailure("alice")
	}

	// StartBot must now be rejected.
	err := bm.StartBot(context.Background(), "alice")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "circuit-broken")
}

func TestCircuitBreaker_ResetFailuresReopensCircuit(t *testing.T) {
	bm := newTestBotManager(5)
	defer stopAll(bm)

	// Trip the circuit breaker.
	for i := 0; i < maxConsecutiveFailures; i++ {
		bm.RecordFailure("alice")
	}

	// Verify it is blocked.
	err := bm.StartBot(context.Background(), "alice")
	require.Error(t, err, "circuit should be open before reset")

	// Reset: re-opens the circuit.
	bm.ResetFailures("alice")
	assert.Equal(t, 0, bm.failureCounts["alice"])

	// StartBot must now succeed.
	err = bm.StartBot(context.Background(), "alice")
	assert.NoError(t, err, "StartBot should succeed after ResetFailures re-opens the circuit")
}

func TestCircuitBreaker_RecordFailure_AutoStopsRunningBot(t *testing.T) {
	bm := newTestBotManager(5)
	defer bm.Shutdown()

	// Start a bot first.
	require.NoError(t, bm.StartBot(context.Background(), "alice"))
	assert.Len(t, bm.ListActive(), 1)

	// Exceed the threshold — the bot should be auto-stopped.
	for i := 0; i < maxConsecutiveFailures; i++ {
		bm.RecordFailure("alice")
	}

	// Bot should no longer be in the active set.
	assert.Len(t, bm.ListActive(), 0,
		"bot should be auto-stopped when circuit breaker trips")
}

func TestCircuitBreaker_ResetFailures_NoopOnUnknownUser(t *testing.T) {
	bm := newTestBotManager(5)
	defer bm.Shutdown()
	// Should not panic.
	bm.ResetFailures("nobody")
}

func TestCircuitBreaker_RecordFailure_NoopOnEmptyUserID(t *testing.T) {
	bm := newTestBotManager(5)
	defer bm.Shutdown()
	// Should not panic or record anything.
	bm.RecordFailure("")
	assert.Equal(t, 0, bm.failureCounts[""])
}

// ─── Shutdown ─────────────────────────────────────────────────────────────────

func TestBotManager_Shutdown_StopsAllBots(t *testing.T) {
	bm := newTestBotManager(5)

	for _, uid := range []string{"alice", "bob"} {
		require.NoError(t, bm.StartBot(context.Background(), uid))
	}
	assert.Len(t, bm.ListActive(), 2)

	bm.Shutdown()
	assert.Len(t, bm.ListActive(), 0)
}

// ─── UpdateConfig ─────────────────────────────────────────────────────────────

func TestBotManager_UpdateConfig_PersistsConfig(t *testing.T) {
	store := newFakeConfigStore()
	bm := NewBotManager(
		store,
		newTestSessionManager(),
		nil,
		BotDependencies{GCTClient: connectedMockGCT()},
		5,
		createTestLogger(),
	)
	defer bm.Shutdown()

	cfg := defaultTestUserCfg("alice", "kucoin")
	cfg.TradeSizeUSD = 5.0

	err := bm.UpdateConfig(context.Background(), "alice", cfg)
	require.NoError(t, err)

	stored, err := store.Get(context.Background(), "alice", "kucoin")
	require.NoError(t, err)
	assert.Equal(t, 5.0, stored.TradeSizeUSD)
}

// ensure MockGCTClient.IsConnected() expectation doesn't produce "unmet" warnings
// for mocks wired with connectedMockGCT() but never consumed (e.g. when StartBot
// fails early before reaching the bot creation step).
// We use mock.MatchedBy for any-call to avoid strict expectations.
var _ = mock.MatchedBy // ensure import is used
