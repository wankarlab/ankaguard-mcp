package dynamic

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rantipay/trading-mcp/internal/config/dynamic/domain"
	"github.com/rantipay/trading-mcp/internal/config/dynamic/repository"
)

// ---- mockRepo ---------------------------------------------------------------

type mockRepo struct {
	registry map[string]*domain.ParameterRegistry
	values   map[string]*domain.ParameterValue // key: "key|scope_type|scope_id"
	history  []*domain.ParameterHistory
	presets  map[string]*domain.ParameterPreset
	mu       sync.Mutex
}

func newMockRepo() *mockRepo {
	return &mockRepo{
		registry: make(map[string]*domain.ParameterRegistry),
		values:   make(map[string]*domain.ParameterValue),
		presets:  make(map[string]*domain.ParameterPreset),
	}
}

func valueKey(key string, scope domain.ScopeType, scopeID string) string {
	return fmt.Sprintf("%s|%s|%s", key, string(scope), scopeID)
}

func (m *mockRepo) ListRegistry(_ context.Context, group string) ([]*domain.ParameterRegistry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*domain.ParameterRegistry, 0, len(m.registry))
	for _, r := range m.registry {
		if group == "" || r.GroupName == group {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *mockRepo) GetRegistry(_ context.Context, key string) (*domain.ParameterRegistry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.registry[key]
	if !ok {
		return nil, fmt.Errorf("not found: %s", key)
	}
	return r, nil
}

func (m *mockRepo) UpsertRegistry(_ context.Context, reg *domain.ParameterRegistry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.registry[reg.Key] = reg
	return nil
}

func (m *mockRepo) GetValue(_ context.Context, key string, scope domain.ScopeType, scopeID string) (*domain.ParameterValue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.values[valueKey(key, scope, scopeID)]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return v, nil
}

func (m *mockRepo) ListValues(_ context.Context, key string) ([]*domain.ParameterValue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*domain.ParameterValue
	for _, v := range m.values {
		if v.Key == key {
			out = append(out, v)
		}
	}
	return out, nil
}

func (m *mockRepo) ListAllValues(_ context.Context) ([]*domain.ParameterValue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*domain.ParameterValue, 0, len(m.values))
	for _, v := range m.values {
		out = append(out, v)
	}
	return out, nil
}

func (m *mockRepo) SetValue(_ context.Context, val *domain.ParameterValue, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := valueKey(val.Key, val.ScopeType, val.ScopeID)
	cp := *val
	cp.UpdatedAt = time.Now()
	m.values[k] = &cp
	return nil
}

func (m *mockRepo) DeleteValue(_ context.Context, key string, scope domain.ScopeType, scopeID string, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.values, valueKey(key, scope, scopeID))
	return nil
}

func (m *mockRepo) BulkSetValues(_ context.Context, vals []*domain.ParameterValue, _, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, v := range vals {
		k := valueKey(v.Key, v.ScopeType, v.ScopeID)
		cp := *v
		cp.UpdatedAt = time.Now()
		m.values[k] = &cp
	}
	return nil
}

func (m *mockRepo) ListHistory(_ context.Context, _ repository.HistoryFilter, _ int) ([]*domain.ParameterHistory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.history, nil
}

func (m *mockRepo) GetHistory(_ context.Context, id int64) (*domain.ParameterHistory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, h := range m.history {
		if h.ID == id {
			return h, nil
		}
	}
	return nil, fmt.Errorf("not found: %d", id)
}

func (m *mockRepo) ListPresets(_ context.Context) ([]*domain.ParameterPreset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*domain.ParameterPreset, 0, len(m.presets))
	for _, p := range m.presets {
		out = append(out, p)
	}
	return out, nil
}

func (m *mockRepo) GetPreset(_ context.Context, name string) (*domain.ParameterPreset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.presets[name]
	if !ok {
		return nil, fmt.Errorf("not found: %s", name)
	}
	return p, nil
}

func (m *mockRepo) CreatePreset(_ context.Context, preset *domain.ParameterPreset) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.presets[preset.Name] = preset
	return nil
}

func (m *mockRepo) ApplyPreset(_ context.Context, name, _, _ string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.presets[name]; !ok {
		return "", fmt.Errorf("preset not found: %s", name)
	}
	return "snap-mock-id", nil
}

func (m *mockRepo) ListPresetsByCategory(_ context.Context, category string) ([]*domain.ParameterPreset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*domain.ParameterPreset, 0, len(m.presets))
	for _, p := range m.presets {
		if p.Category == category {
			out = append(out, p)
		}
	}
	return out, nil
}

func (m *mockRepo) GetActiveMode(_ context.Context) (*domain.ParameterPreset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.presets {
		if p.IsActive && p.Category == "mode" {
			return p, nil
		}
	}
	return nil, fmt.Errorf("no active mode")
}

func (m *mockRepo) UpdatePreset(_ context.Context, name string, preset *domain.ParameterPreset) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.presets[name]; !ok {
		return fmt.Errorf("not found: %s", name)
	}
	m.presets[name] = preset
	return nil
}

func (m *mockRepo) DeletePreset(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.presets[name]
	if !ok || !p.Editable {
		return fmt.Errorf("not found or not editable: %s", name)
	}
	delete(m.presets, name)
	return nil
}

func (m *mockRepo) UpdatePresetPerformance(_ context.Context, name string, perf *domain.PresetPerformance) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.presets[name]
	if !ok {
		return fmt.Errorf("preset not found: %s", name)
	}
	p.PerformanceScore = perf
	return nil
}

// ---- helpers ----------------------------------------------------------------

func strPtr(s string) *string { return &s }

func newStore(t *testing.T, repo *mockRepo) *ConfigStore {
	t.Helper()
	log := logrus.New()
	log.SetOutput(os.Stderr)
	log.SetLevel(logrus.WarnLevel)
	store, err := NewConfigStore(repo, log)
	require.NoError(t, err)
	return store
}

// seedIntParam adds a basic int registry entry to the mock repo.
func seedIntParam(repo *mockRepo, key, group string, min, max *string, def string) {
	repo.registry[key] = &domain.ParameterRegistry{
		Key:          key,
		Type:         domain.ParamTypeInt,
		GroupName:    group,
		DefaultValue: def,
		MinValue:     min,
		MaxValue:     max,
	}
}

// ---- TC-9.1.1: Snapshot atomicity under concurrency -------------------------

// TestSnapshotConcurrency verifies that 10 goroutines reading Snapshot() and
// 5 goroutines calling Set() concurrently produce no data race. Run with -race.
func TestSnapshotConcurrency(t *testing.T) {
	repo := newMockRepo()
	// Register a param and an initial value.
	seedIntParam(repo, "foo.bar", "foo", strPtr("0"), strPtr("1000"), "5")
	repo.values[valueKey("foo.bar", domain.ScopeGlobal, "*")] = &domain.ParameterValue{
		Key: "foo.bar", ScopeType: domain.ScopeGlobal, ScopeID: "*", Value: "5",
	}
	store := newStore(t, repo)

	ctx := context.Background()
	var wg sync.WaitGroup

	readers := 10
	writers := 5
	iterations := 50

	// Start readers.
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				snap := store.Snapshot()
				// Just access the snapshot to ensure the pointer is valid.
				if snap == nil {
					t.Errorf("unexpected nil snapshot")
					return
				}
				_ = snap.Hash
			}
		}()
	}

	// Start writers.
	var writeErrs int64
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				val := &domain.ParameterValue{
					Key:       "foo.bar",
					ScopeType: domain.ScopeGlobal,
					ScopeID:   "*",
					Value:     fmt.Sprintf("%d", (id*iterations+j)%200),
				}
				if err := store.Set(ctx, val, "concurrency test"); err != nil {
					atomic.AddInt64(&writeErrs, 1)
				}
			}
		}(i)
	}

	wg.Wait()

	// No unexpected write errors (all values are in [0, 1000] so validation passes).
	assert.Zero(t, writeErrs, "unexpected validation errors during concurrent writes")

	// Snapshot must still be non-nil and have a valid hash after all concurrent ops.
	snap := store.Snapshot()
	require.NotNil(t, snap)
	assert.NotEmpty(t, snap.Hash)
}

// ---- TC-9.1.2: Scope resolver — 4 levels ------------------------------------

// TestScopeResolver verifies the most-specific-wins precedence:
// bot_instance > pair > exchange > global.
func TestScopeResolver(t *testing.T) {
	const key = "indicators.rsi.period"

	repo := newMockRepo()
	repo.registry[key] = &domain.ParameterRegistry{
		Key:          key,
		Type:         domain.ParamTypeInt,
		GroupName:    "indicators",
		DefaultValue: "0",
	}

	// Insert a value at every scope level for the same key.
	levels := []struct {
		scope   domain.ScopeType
		scopeID string
		value   string
	}{
		{domain.ScopeGlobal, "*", "10"},
		{domain.ScopeExchange, "kucoin", "20"},
		{domain.ScopePair, "BTC-USDT", "30"},
		{domain.ScopeBotInstance, "bot-1", "40"},
	}
	for _, l := range levels {
		repo.values[valueKey(key, l.scope, l.scopeID)] = &domain.ParameterValue{
			Key: key, ScopeType: l.scope, ScopeID: l.scopeID, Value: l.value,
		}
	}

	store := newStore(t, repo)

	// Full chain: bot_instance > pair > exchange > global.
	fullChain := BotScopeChain("bot-1", "BTC-USDT", "kucoin")
	ss := store.SnapshotForScope(fullChain)
	assert.Equal(t, 40, ss.Int(key), "bot_instance must win")

	// Without bot_instance: pair wins.
	chainNoBotInst := BotScopeChain("", "BTC-USDT", "kucoin")
	ss2 := store.SnapshotForScope(chainNoBotInst)
	assert.Equal(t, 30, ss2.Int(key), "pair must win when bot_instance absent")

	// Without bot_instance or pair: exchange wins.
	chainExch := BotScopeChain("", "", "kucoin")
	ss3 := store.SnapshotForScope(chainExch)
	assert.Equal(t, 20, ss3.Int(key), "exchange must win when pair absent")

	// Global only.
	chainGlobal := BotScopeChain("", "", "")
	ss4 := store.SnapshotForScope(chainGlobal)
	assert.Equal(t, 10, ss4.Int(key), "global value must be returned")

	// No values at all — fallback to registry default.
	repo2 := newMockRepo()
	repo2.registry[key] = &domain.ParameterRegistry{
		Key:          key,
		Type:         domain.ParamTypeInt,
		GroupName:    "indicators",
		DefaultValue: "7",
	}
	store2 := newStore(t, repo2)
	ss5 := store2.SnapshotForScope(BotScopeChain("bot-1", "BTC-USDT", "kucoin"))
	assert.Equal(t, 7, ss5.Int(key), "registry default must be used when no scoped value exists")
}

// ---- TC-9.1.3: Validators — integer with min/max ----------------------------

// TestValidatorIntMinMax verifies ValidateValue for integer parameters with bounds.
func TestValidatorIntMinMax(t *testing.T) {
	reg := &domain.ParameterRegistry{
		Key:          "test.int",
		Type:         domain.ParamTypeInt,
		DefaultValue: "50",
		MinValue:     strPtr("1"),
		MaxValue:     strPtr("100"),
	}

	t.Run("valid_value", func(t *testing.T) {
		err := ValidateValue(reg, "50")
		assert.NoError(t, err)
	})

	t.Run("below_min", func(t *testing.T) {
		err := ValidateValue(reg, "0")
		assert.ErrorIs(t, err, ErrBelowMin)
	})

	t.Run("at_min", func(t *testing.T) {
		err := ValidateValue(reg, "1")
		assert.NoError(t, err)
	})

	t.Run("at_max", func(t *testing.T) {
		err := ValidateValue(reg, "100")
		assert.NoError(t, err)
	})

	t.Run("above_max", func(t *testing.T) {
		err := ValidateValue(reg, "101")
		assert.ErrorIs(t, err, ErrAboveMax)
	})

	t.Run("not_an_integer", func(t *testing.T) {
		err := ValidateValue(reg, "abc")
		assert.ErrorIs(t, err, ErrInvalidInt)
	})

	t.Run("float_string_rejected", func(t *testing.T) {
		err := ValidateValue(reg, "3.14")
		assert.ErrorIs(t, err, ErrInvalidInt)
	})
}

// TestValidatorFloat verifies ValidateValue for float parameters with bounds.
func TestValidatorFloat(t *testing.T) {
	reg := &domain.ParameterRegistry{
		Key:          "test.float",
		Type:         domain.ParamTypeFloat,
		DefaultValue: "0.5",
		MinValue:     strPtr("0.0"),
		MaxValue:     strPtr("1.0"),
	}

	assert.NoError(t, ValidateValue(reg, "0.5"))
	assert.ErrorIs(t, ValidateValue(reg, "-0.1"), ErrBelowMin)
	assert.ErrorIs(t, ValidateValue(reg, "1.1"), ErrAboveMax)
	assert.ErrorIs(t, ValidateValue(reg, "abc"), ErrInvalidFloat)
}

// TestValidatorBool verifies ValidateValue for boolean parameters.
func TestValidatorBool(t *testing.T) {
	reg := &domain.ParameterRegistry{
		Key:          "test.bool",
		Type:         domain.ParamTypeBool,
		DefaultValue: "false",
	}

	assert.NoError(t, ValidateValue(reg, "true"))
	assert.NoError(t, ValidateValue(reg, "false"))
	assert.ErrorIs(t, ValidateValue(reg, "yes"), ErrInvalidBool)
	assert.ErrorIs(t, ValidateValue(reg, "1"), ErrInvalidBool)
	assert.ErrorIs(t, ValidateValue(reg, ""), ErrInvalidBool)
}

// TestValidatorDuration verifies ValidateValue for duration parameters.
func TestValidatorDuration(t *testing.T) {
	reg := &domain.ParameterRegistry{
		Key:          "test.duration",
		Type:         domain.ParamTypeDuration,
		DefaultValue: "30s",
	}

	assert.NoError(t, ValidateValue(reg, "1m30s"))
	assert.NoError(t, ValidateValue(reg, "500ms"))
	assert.ErrorIs(t, ValidateValue(reg, "banana"), ErrInvalidDuration)
}

// TestValidatorEnum verifies that enum validation is applied across all types.
func TestValidatorEnum(t *testing.T) {
	reg := &domain.ParameterRegistry{
		Key:          "test.enum",
		Type:         domain.ParamTypeString,
		DefaultValue: "low",
		EnumValues:   []string{"low", "medium", "high"},
	}

	assert.NoError(t, ValidateValue(reg, "low"))
	assert.NoError(t, ValidateValue(reg, "high"))
	assert.ErrorIs(t, ValidateValue(reg, "extreme"), ErrNotInEnum)
	assert.ErrorIs(t, ValidateValue(reg, ""), ErrNotInEnum)
}

// TestStoreSetRejectsUnknownKey verifies that Set returns an error for unknown keys.
func TestStoreSetRejectsUnknownKey(t *testing.T) {
	repo := newMockRepo()
	store := newStore(t, repo)

	err := store.Set(context.Background(), &domain.ParameterValue{
		Key:       "does.not.exist",
		ScopeType: domain.ScopeGlobal,
		ScopeID:   "*",
		Value:     "42",
	}, "test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown parameter key")
}

// TestStoreSetRejectsInvalidValue verifies that Set rejects values that fail validation.
func TestStoreSetRejectsInvalidValue(t *testing.T) {
	repo := newMockRepo()
	seedIntParam(repo, "foo.count", "foo", strPtr("1"), strPtr("100"), "50")
	store := newStore(t, repo)

	err := store.Set(context.Background(), &domain.ParameterValue{
		Key:       "foo.count",
		ScopeType: domain.ScopeGlobal,
		ScopeID:   "*",
		Value:     "not-a-number",
	}, "test")
	assert.Error(t, err)
}

// ---- TC-9.1.4: Subscribe callbacks ------------------------------------------

// TestSubscribeGroupCallback verifies that a subscriber for a named group receives
// changes only for parameters in that group, and that a "*" subscriber receives all.
func TestSubscribeGroupCallback(t *testing.T) {
	repo := newMockRepo()
	const key = "indicators.rsi.period"
	repo.registry[key] = &domain.ParameterRegistry{
		Key:          key,
		Type:         domain.ParamTypeInt,
		GroupName:    "indicators",
		DefaultValue: "14",
	}
	store := newStore(t, repo)

	var (
		indicatorsReceived int64
		wildcardReceived   int64
	)

	// Subscriber for "indicators" group.
	unsubInd := store.Subscribe("indicators", func(changes []Change) {
		atomic.AddInt64(&indicatorsReceived, int64(len(changes)))
	})
	defer unsubInd()

	// Wildcard subscriber.
	unsubAll := store.Subscribe("*", func(changes []Change) {
		atomic.AddInt64(&wildcardReceived, int64(len(changes)))
	})
	defer unsubAll()

	err := store.Set(context.Background(), &domain.ParameterValue{
		Key:       key,
		ScopeType: domain.ScopeGlobal,
		ScopeID:   "*",
		Value:     "21",
	}, "subscribe test")
	require.NoError(t, err)

	// Notifications are dispatched asynchronously; wait for them.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&indicatorsReceived) > 0 && atomic.LoadInt64(&wildcardReceived) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	assert.GreaterOrEqual(t, atomic.LoadInt64(&indicatorsReceived), int64(1),
		"indicators subscriber must receive change")
	assert.GreaterOrEqual(t, atomic.LoadInt64(&wildcardReceived), int64(1),
		"wildcard subscriber must receive change")
}

// TestSubscribeUnsubscribe verifies that calling the returned unsubscribe function
// prevents subsequent callbacks from being invoked.
func TestSubscribeUnsubscribe(t *testing.T) {
	repo := newMockRepo()
	const key = "foo.param"
	seedIntParam(repo, key, "foo", nil, nil, "10")
	store := newStore(t, repo)

	var received int64
	unsub := store.Subscribe("foo", func(_ []Change) {
		atomic.AddInt64(&received, 1)
	})

	// First Set — subscriber is active.
	require.NoError(t, store.Set(context.Background(), &domain.ParameterValue{
		Key: key, ScopeType: domain.ScopeGlobal, ScopeID: "*", Value: "20",
	}, "first"))
	time.Sleep(30 * time.Millisecond)
	firstCount := atomic.LoadInt64(&received)
	assert.GreaterOrEqual(t, firstCount, int64(1))

	// Unsubscribe.
	unsub()

	// Second Set — subscriber is gone.
	require.NoError(t, store.Set(context.Background(), &domain.ParameterValue{
		Key: key, ScopeType: domain.ScopeGlobal, ScopeID: "*", Value: "30",
	}, "second"))
	time.Sleep(30 * time.Millisecond)
	assert.Equal(t, firstCount, atomic.LoadInt64(&received), "no new callbacks after unsubscribe")
}

// TestSubscribeChangeFields verifies the Change struct fields passed to the callback.
func TestSubscribeChangeFields(t *testing.T) {
	repo := newMockRepo()
	const key = "indicators.macd.fast"
	repo.registry[key] = &domain.ParameterRegistry{
		Key:          key,
		Type:         domain.ParamTypeInt,
		GroupName:    "indicators",
		DefaultValue: "12",
	}
	// Pre-load an existing value so OldValue is non-empty.
	repo.values[valueKey(key, domain.ScopeGlobal, "*")] = &domain.ParameterValue{
		Key: key, ScopeType: domain.ScopeGlobal, ScopeID: "*", Value: "12",
	}
	store := newStore(t, repo)

	var captured []Change
	var mu sync.Mutex
	unsub := store.Subscribe("indicators", func(changes []Change) {
		mu.Lock()
		defer mu.Unlock()
		captured = append(captured, changes...)
	})
	defer unsub()

	require.NoError(t, store.Set(context.Background(), &domain.ParameterValue{
		Key:       key,
		ScopeType: domain.ScopeGlobal,
		ScopeID:   "*",
		Value:     "26",
	}, "change-fields test"))

	// Wait for the async dispatch.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(captured)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, captured, 1)
	c := captured[0]
	assert.Equal(t, key, c.Key)
	assert.Equal(t, domain.ScopeGlobal, c.ScopeType)
	assert.Equal(t, "*", c.ScopeID)
	assert.Equal(t, "12", c.OldValue)
	assert.Equal(t, "26", c.NewValue)
}

// ---- TC-9.1.5: Backup file format -------------------------------------------

// TestWriteBackupFormat calls writeBackup to a temp dir, parses the JSON, and
// verifies the expected structure and field values.
func TestWriteBackupFormat(t *testing.T) {
	repo := newMockRepo()
	repo.registry["risk.stop_loss"] = &domain.ParameterRegistry{
		Key:          "risk.stop_loss",
		Type:         domain.ParamTypeFloat,
		GroupName:    "risk",
		DefaultValue: "0.02",
	}
	now := time.Now()
	repo.values[valueKey("risk.stop_loss", domain.ScopeGlobal, "*")] = &domain.ParameterValue{
		Key:       "risk.stop_loss",
		ScopeType: domain.ScopeGlobal,
		ScopeID:   "*",
		Value:     "0.015",
		UpdatedBy: "system",
		UpdatedAt: now,
	}

	store := newStore(t, repo)

	dir := t.TempDir()
	err := store.writeBackup(context.Background(), dir)
	require.NoError(t, err)

	// Find the written file.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "exactly one backup file must be created")

	data, err := os.ReadFile(dir + "/" + entries[0].Name())
	require.NoError(t, err)

	var backup []BackupEntry
	require.NoError(t, json.Unmarshal(data, &backup))
	require.Len(t, backup, 1)

	entry := backup[0]
	assert.Equal(t, "risk.stop_loss", entry.Key)
	assert.Equal(t, "global", entry.ScopeType)
	assert.Equal(t, "*", entry.ScopeID)
	assert.Equal(t, "0.015", entry.Value)
	assert.Equal(t, "system", entry.UpdatedBy)
	assert.False(t, entry.UpdatedAt.IsZero(), "UpdatedAt must be set")
}

// TestWriteBackupEmpty verifies that writeBackup creates a valid empty JSON array
// when no values are stored.
func TestWriteBackupEmpty(t *testing.T) {
	repo := newMockRepo()
	store := newStore(t, repo)

	dir := t.TempDir()
	err := store.writeBackup(context.Background(), dir)
	require.NoError(t, err)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	data, err := os.ReadFile(dir + "/" + entries[0].Name())
	require.NoError(t, err)

	var backup []BackupEntry
	require.NoError(t, json.Unmarshal(data, &backup))
	assert.Empty(t, backup)
}

// ---- Additional coverage: BulkSet, SnapshotWithOverrides, Repo ---------------

// TestBulkSet verifies that BulkSet applies all values and updates the snapshot.
func TestBulkSet(t *testing.T) {
	repo := newMockRepo()
	seedIntParam(repo, "scalping.tp1", "scalping", strPtr("1"), strPtr("500"), "100")
	seedIntParam(repo, "scalping.tp2", "scalping", strPtr("1"), strPtr("500"), "200")
	store := newStore(t, repo)

	vals := []*domain.ParameterValue{
		{Key: "scalping.tp1", ScopeType: domain.ScopeGlobal, ScopeID: "*", Value: "150"},
		{Key: "scalping.tp2", ScopeType: domain.ScopeGlobal, ScopeID: "*", Value: "250"},
	}
	err := store.BulkSet(context.Background(), vals, "test-user", "bulk test")
	require.NoError(t, err)

	snap := store.Snapshot()
	chain := BotScopeChain("", "", "")
	ss := &ScopedSnapshot{Snapshot: snap, chain: chain}
	assert.Equal(t, 150, ss.Int("scalping.tp1"))
	assert.Equal(t, 250, ss.Int("scalping.tp2"))
}

// TestBulkSetUnknownKeyFails verifies that BulkSet fails if any key is unknown.
func TestBulkSetUnknownKeyFails(t *testing.T) {
	repo := newMockRepo()
	store := newStore(t, repo)

	err := store.BulkSet(context.Background(), []*domain.ParameterValue{
		{Key: "ghost.param", ScopeType: domain.ScopeGlobal, ScopeID: "*", Value: "1"},
	}, "u", "r")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown parameter key")
}

// TestSnapshotWithOverrides verifies virtual snapshot override without modifying the store.
func TestSnapshotWithOverrides(t *testing.T) {
	repo := newMockRepo()
	seedIntParam(repo, "indicators.rsi.period", "indicators", nil, nil, "14")
	repo.values[valueKey("indicators.rsi.period", domain.ScopeGlobal, "*")] = &domain.ParameterValue{
		Key: "indicators.rsi.period", ScopeType: domain.ScopeGlobal, ScopeID: "*", Value: "14",
	}
	store := newStore(t, repo)

	virtual := store.SnapshotWithOverrides("current", map[string]string{
		"indicators.rsi.period": "21",
	})
	require.NotNil(t, virtual)

	// Virtual snapshot sees overridden value.
	ss := virtual.ToScopedSnapshot()
	assert.Equal(t, 21, ss.Int("indicators.rsi.period"))

	// Live snapshot is unchanged.
	live := store.SnapshotForScope(BotScopeChain("", "", ""))
	assert.Equal(t, 14, live.Int("indicators.rsi.period"))
}

// TestRepo exposes the underlying repository.
func TestRepo(t *testing.T) {
	repo := newMockRepo()
	store := newStore(t, repo)
	assert.Equal(t, repo, store.Repo())
}

// TestSnapshotHash verifies that the snapshot hash changes when values change.
func TestSnapshotHash(t *testing.T) {
	repo := newMockRepo()
	seedIntParam(repo, "hash.test", "test", nil, nil, "1")
	store := newStore(t, repo)

	snap1 := store.Snapshot()
	hash1 := snap1.Hash
	ts1 := snap1.BuiltAt

	err := store.Set(context.Background(), &domain.ParameterValue{
		Key:       "hash.test",
		ScopeType: domain.ScopeGlobal,
		ScopeID:   "*",
		Value:     "99",
	}, "hash test")
	require.NoError(t, err)

	snap2 := store.Snapshot()
	assert.NotEqual(t, hash1, snap2.Hash, "hash must change after value update")
	assert.True(t, !snap2.BuiltAt.Before(ts1), "BuiltAt must not regress")
}

// ---- TC-9.1 extra: ScopedSnapshot type accessors ----------------------------

// TestScopedSnapshotAccessors exercises Bool, Float, Duration, String, JSON,
// and the strict variants so that types.go reaches high coverage.
func TestScopedSnapshotAccessors(t *testing.T) {
	repo := newMockRepo()

	// Register params of each type.
	repo.registry["p.bool"] = &domain.ParameterRegistry{
		Key: "p.bool", Type: domain.ParamTypeBool, DefaultValue: "false",
	}
	repo.registry["p.float"] = &domain.ParameterRegistry{
		Key: "p.float", Type: domain.ParamTypeFloat, DefaultValue: "0.0",
	}
	repo.registry["p.duration"] = &domain.ParameterRegistry{
		Key: "p.duration", Type: domain.ParamTypeDuration, DefaultValue: "1s",
	}
	repo.registry["p.string"] = &domain.ParameterRegistry{
		Key: "p.string", Type: domain.ParamTypeString, DefaultValue: "hello",
	}
	repo.registry["p.json"] = &domain.ParameterRegistry{
		Key: "p.json", Type: domain.ParamTypeJSON, DefaultValue: `{"x":1}`,
	}
	repo.registry["p.int"] = &domain.ParameterRegistry{
		Key: "p.int", Type: domain.ParamTypeInt, DefaultValue: "7",
	}

	// Set concrete values.
	setVal := func(key string, scope domain.ScopeType, scopeID, val string) {
		repo.values[valueKey(key, scope, scopeID)] = &domain.ParameterValue{
			Key: key, ScopeType: scope, ScopeID: scopeID, Value: val,
		}
	}
	setVal("p.bool", domain.ScopeGlobal, "*", "true")
	setVal("p.float", domain.ScopeGlobal, "*", "3.14")
	setVal("p.duration", domain.ScopeGlobal, "*", "500ms")
	setVal("p.string", domain.ScopeGlobal, "*", "world")
	setVal("p.json", domain.ScopeGlobal, "*", `{"x":42}`)
	setVal("p.int", domain.ScopeGlobal, "*", "99")

	store := newStore(t, repo)
	ss := store.SnapshotForScope(BotScopeChain("", "", ""))

	// Bool
	assert.True(t, ss.Bool("p.bool"))
	bv, ok := ss.BoolStrict("p.bool")
	assert.True(t, bv)
	assert.True(t, ok)

	// Float
	assert.InDelta(t, 3.14, ss.Float("p.float"), 1e-9)
	fv, ok := ss.FloatStrict("p.float")
	assert.InDelta(t, 3.14, fv, 1e-9)
	assert.True(t, ok)

	// Duration
	assert.Equal(t, 500*time.Millisecond, ss.Duration("p.duration"))
	dv, ok := ss.DurationStrict("p.duration")
	assert.Equal(t, 500*time.Millisecond, dv)
	assert.True(t, ok)

	// String
	assert.Equal(t, "world", ss.String("p.string"))

	// JSON
	var out map[string]int
	err := ss.JSON("p.json", &out)
	assert.NoError(t, err)
	assert.Equal(t, 42, out["x"])

	// IntStrict
	iv, ok := ss.IntStrict("p.int")
	assert.Equal(t, 99, iv)
	assert.True(t, ok)

	// Unknown key — strict variants return (zero, false).
	_, okF := ss.FloatStrict("nonexistent")
	assert.False(t, okF)
	_, okB := ss.BoolStrict("nonexistent")
	assert.False(t, okB)
	_, okD := ss.DurationStrict("nonexistent")
	assert.False(t, okD)
	_, okI := ss.IntStrict("nonexistent")
	assert.False(t, okI)
}

// TestScopedSnapshotDefaultFallback verifies that when a value is missing from
// the snapshot the accessor falls back to the registry DefaultValue.
func TestScopedSnapshotDefaultFallback(t *testing.T) {
	repo := newMockRepo()
	repo.registry["defaults.int"] = &domain.ParameterRegistry{
		Key: "defaults.int", Type: domain.ParamTypeInt, DefaultValue: "42",
	}
	repo.registry["defaults.float"] = &domain.ParameterRegistry{
		Key: "defaults.float", Type: domain.ParamTypeFloat, DefaultValue: "1.5",
	}
	repo.registry["defaults.bool"] = &domain.ParameterRegistry{
		Key: "defaults.bool", Type: domain.ParamTypeBool, DefaultValue: "true",
	}
	repo.registry["defaults.duration"] = &domain.ParameterRegistry{
		Key: "defaults.duration", Type: domain.ParamTypeDuration, DefaultValue: "2m",
	}

	store := newStore(t, repo)
	ss := store.SnapshotForScope(BotScopeChain("", "", ""))

	assert.Equal(t, 42, ss.Int("defaults.int"))
	assert.InDelta(t, 1.5, ss.Float("defaults.float"), 1e-9)
	assert.True(t, ss.Bool("defaults.bool"))
	assert.Equal(t, 2*time.Minute, ss.Duration("defaults.duration"))
}

// ---- TC-9.1 extra: context helpers ------------------------------------------

// TestContextHelpers verifies WithSnapshot, SnapshotFromContext, and SetDefaultStore.
func TestContextHelpers(t *testing.T) {
	repo := newMockRepo()
	seedIntParam(repo, "ctx.param", "ctx", nil, nil, "55")
	repo.values[valueKey("ctx.param", domain.ScopeGlobal, "*")] = &domain.ParameterValue{
		Key: "ctx.param", ScopeType: domain.ScopeGlobal, ScopeID: "*", Value: "55",
	}
	store := newStore(t, repo)

	chain := BotScopeChain("", "", "")
	ss := store.SnapshotForScope(chain)

	// WithSnapshot / SnapshotFromContext round-trip.
	ctx := WithSnapshot(context.Background(), ss)
	got := SnapshotFromContext(ctx)
	require.NotNil(t, got)
	assert.Equal(t, 55, got.Int("ctx.param"))

	// Context without a snapshot → fallback to defaultStore.
	SetDefaultStore(store)
	t.Cleanup(func() { SetDefaultStore(nil) }) // restore global state
	got2 := SnapshotFromContext(context.Background())
	require.NotNil(t, got2)
	assert.Equal(t, 55, got2.Int("ctx.param"))

	// Context without a snapshot and no defaultStore → nil.
	SetDefaultStore(nil)
	got3 := SnapshotFromContext(context.Background())
	assert.Nil(t, got3)
}

// ---- TC-9.1 extra: cross-param validators ------------------------------------

// buildSnapForCross is a helper that creates a minimal Snapshot with the
// provided key→value pairs at global scope, all typed as float/string.
func buildSnapForCross(regs []*domain.ParameterRegistry, kvs map[string]string) *Snapshot {
	values := make(map[scopeKey]string, len(kvs))
	for k, v := range kvs {
		values[scopeKey{Key: k, ScopeType: domain.ScopeGlobal, ScopeID: "*"}] = v
	}
	registry := make(map[string]*domain.ParameterRegistry, len(regs))
	for _, r := range regs {
		registry[r.Key] = r
	}
	return &Snapshot{registry: registry, values: values}
}

func globalChain() []ScopeRef {
	return []ScopeRef{{Type: domain.ScopeGlobal, ID: "*"}}
}

// TestCrossValidatorMACD verifies that fast_period < slow_period is enforced.
func TestCrossValidatorMACD(t *testing.T) {
	regs := []*domain.ParameterRegistry{
		{Key: "indicators.macd.fast_period", Type: domain.ParamTypeInt, DefaultValue: "0"},
		{Key: "indicators.macd.slow_period", Type: domain.ParamTypeInt, DefaultValue: "0"},
	}

	t.Run("valid", func(t *testing.T) {
		snap := buildSnapForCross(regs, map[string]string{
			"indicators.macd.fast_period": "12",
			"indicators.macd.slow_period": "26",
		})
		assert.NoError(t, runCrossValidators(snap, globalChain()))
	})

	t.Run("equal_periods", func(t *testing.T) {
		snap := buildSnapForCross(regs, map[string]string{
			"indicators.macd.fast_period": "14",
			"indicators.macd.slow_period": "14",
		})
		assert.Error(t, runCrossValidators(snap, globalChain()))
	})

	t.Run("fast_greater_than_slow", func(t *testing.T) {
		snap := buildSnapForCross(regs, map[string]string{
			"indicators.macd.fast_period": "26",
			"indicators.macd.slow_period": "12",
		})
		assert.Error(t, runCrossValidators(snap, globalChain()))
	})

	t.Run("both_zero_skipped", func(t *testing.T) {
		snap := buildSnapForCross(regs, map[string]string{})
		assert.NoError(t, runCrossValidators(snap, globalChain()))
	})
}

// TestCrossValidatorRegimeThresholds verifies trend < chaos is enforced.
func TestCrossValidatorRegimeThresholds(t *testing.T) {
	regs := []*domain.ParameterRegistry{
		{Key: "regime.adx.trend_threshold", Type: domain.ParamTypeFloat, DefaultValue: "0"},
		{Key: "regime.adx.chaos_threshold", Type: domain.ParamTypeFloat, DefaultValue: "0"},
	}

	t.Run("valid", func(t *testing.T) {
		snap := buildSnapForCross(regs, map[string]string{
			"regime.adx.trend_threshold": "25.0",
			"regime.adx.chaos_threshold": "40.0",
		})
		assert.NoError(t, runCrossValidators(snap, globalChain()))
	})

	t.Run("trend_equals_chaos", func(t *testing.T) {
		snap := buildSnapForCross(regs, map[string]string{
			"regime.adx.trend_threshold": "30.0",
			"regime.adx.chaos_threshold": "30.0",
		})
		assert.Error(t, runCrossValidators(snap, globalChain()))
	})
}

// TestCrossValidatorBreakeven verifies 0 <= buffer < trigger is enforced.
func TestCrossValidatorBreakeven(t *testing.T) {
	regs := []*domain.ParameterRegistry{
		{Key: "scalping.breakeven.trigger_pct", Type: domain.ParamTypeFloat, DefaultValue: "0"},
		{Key: "scalping.breakeven.buffer_pct", Type: domain.ParamTypeFloat, DefaultValue: "0"},
	}

	t.Run("valid", func(t *testing.T) {
		snap := buildSnapForCross(regs, map[string]string{
			"scalping.breakeven.trigger_pct": "0.5",
			"scalping.breakeven.buffer_pct":  "0.1",
		})
		assert.NoError(t, runCrossValidators(snap, globalChain()))
	})

	t.Run("buffer_equals_trigger", func(t *testing.T) {
		snap := buildSnapForCross(regs, map[string]string{
			"scalping.breakeven.trigger_pct": "0.3",
			"scalping.breakeven.buffer_pct":  "0.3",
		})
		assert.Error(t, runCrossValidators(snap, globalChain()))
	})

	t.Run("negative_buffer", func(t *testing.T) {
		snap := buildSnapForCross(regs, map[string]string{
			"scalping.breakeven.trigger_pct": "0.5",
			"scalping.breakeven.buffer_pct":  "-0.1",
		})
		assert.Error(t, runCrossValidators(snap, globalChain()))
	})
}

// TestCrossValidatorDynamicSizingTiers covers the JSON tiers validator.
func TestCrossValidatorDynamicSizingTiers(t *testing.T) {
	regs := []*domain.ParameterRegistry{
		{Key: "scalping.dynamic_sizing.tiers", Type: domain.ParamTypeJSON, DefaultValue: ""},
	}

	t.Run("valid_tiers", func(t *testing.T) {
		snap := buildSnapForCross(regs, map[string]string{
			"scalping.dynamic_sizing.tiers": `[{"min_score":0.65,"max_score":0.80,"size_pct":50},{"min_score":0.80,"max_score":1.0,"size_pct":100}]`,
		})
		assert.NoError(t, runCrossValidators(snap, globalChain()))
	})

	t.Run("gap_between_tiers", func(t *testing.T) {
		snap := buildSnapForCross(regs, map[string]string{
			"scalping.dynamic_sizing.tiers": `[{"min_score":0.65,"max_score":0.75,"size_pct":50},{"min_score":0.85,"max_score":1.0,"size_pct":100}]`,
		})
		assert.Error(t, runCrossValidators(snap, globalChain()))
	})

	t.Run("overlap_between_tiers", func(t *testing.T) {
		snap := buildSnapForCross(regs, map[string]string{
			"scalping.dynamic_sizing.tiers": `[{"min_score":0.65,"max_score":0.85,"size_pct":50},{"min_score":0.80,"max_score":1.0,"size_pct":100}]`,
		})
		assert.Error(t, runCrossValidators(snap, globalChain()))
	})

	t.Run("does_not_reach_1.0", func(t *testing.T) {
		snap := buildSnapForCross(regs, map[string]string{
			"scalping.dynamic_sizing.tiers": `[{"min_score":0.65,"max_score":0.95,"size_pct":50}]`,
		})
		assert.Error(t, runCrossValidators(snap, globalChain()))
	})

	t.Run("starts_after_0.65", func(t *testing.T) {
		snap := buildSnapForCross(regs, map[string]string{
			"scalping.dynamic_sizing.tiers": `[{"min_score":0.70,"max_score":1.0,"size_pct":100}]`,
		})
		assert.Error(t, runCrossValidators(snap, globalChain()))
	})

	t.Run("empty_json_array_ok", func(t *testing.T) {
		snap := buildSnapForCross(regs, map[string]string{
			"scalping.dynamic_sizing.tiers": `[]`,
		})
		assert.NoError(t, runCrossValidators(snap, globalChain()))
	})

	t.Run("missing_key_ok", func(t *testing.T) {
		snap := buildSnapForCross(regs, map[string]string{})
		assert.NoError(t, runCrossValidators(snap, globalChain()))
	})
}

// TestCrossValidatorPartialTPLevels covers the partial TP levels validator.
func TestCrossValidatorPartialTPLevels(t *testing.T) {
	regs := []*domain.ParameterRegistry{
		{Key: "scalping.partial_tp.levels", Type: domain.ParamTypeJSON, DefaultValue: ""},
	}

	t.Run("valid_levels", func(t *testing.T) {
		snap := buildSnapForCross(regs, map[string]string{
			"scalping.partial_tp.levels": `[{"tp_pct":0.5,"close_pct":30},{"tp_pct":1.0,"close_pct":40}]`,
		})
		assert.NoError(t, runCrossValidators(snap, globalChain()))
	})

	t.Run("non_increasing_tp_pct", func(t *testing.T) {
		snap := buildSnapForCross(regs, map[string]string{
			"scalping.partial_tp.levels": `[{"tp_pct":1.0,"close_pct":30},{"tp_pct":0.5,"close_pct":40}]`,
		})
		assert.Error(t, runCrossValidators(snap, globalChain()))
	})

	t.Run("close_pct_sum_exceeds_100", func(t *testing.T) {
		snap := buildSnapForCross(regs, map[string]string{
			"scalping.partial_tp.levels": `[{"tp_pct":0.5,"close_pct":60},{"tp_pct":1.0,"close_pct":60}]`,
		})
		assert.Error(t, runCrossValidators(snap, globalChain()))
	})

	t.Run("missing_key_ok", func(t *testing.T) {
		snap := buildSnapForCross(regs, map[string]string{})
		assert.NoError(t, runCrossValidators(snap, globalChain()))
	})
}

// ---- TC-9.1 extra: cleanupOldBackups ----------------------------------------

// TestCleanupOldBackupsRemovesOldFiles verifies that files > 30 days are deleted,
// the 7–30 day window keeps only one per day, and recent files are untouched.
func TestCleanupOldBackupsRemovesOldFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()

	writeFile := func(ts time.Time) {
		t.Helper()
		name := fmt.Sprintf("config_%s.json", ts.Format("20060102_15"))
		err := os.WriteFile(dir+"/"+name, []byte("[]"), 0o644)
		require.NoError(t, err)
	}

	// File from 35 days ago (should be deleted).
	writeFile(now.AddDate(0, 0, -35))

	// Two files from 10 days ago (different hours — only earliest should survive).
	base10 := now.AddDate(0, 0, -10)
	day10 := time.Date(base10.Year(), base10.Month(), base10.Day(), 8, 0, 0, 0, time.UTC)
	writeFile(day10)
	writeFile(day10.Add(1 * time.Hour))

	// File from 3 days ago (recent — should be kept).
	writeFile(now.AddDate(0, 0, -3))

	err := cleanupOldBackups(dir)
	require.NoError(t, err)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}

	// 35-day-old file must be gone.
	oldName := fmt.Sprintf("config_%s.json", now.AddDate(0, 0, -35).Format("20060102_15"))
	assert.NotContains(t, names, oldName, "file older than 30 days must be deleted")

	// 10-day window: only the hour-08 file survives, hour-09 is removed.
	earlyName := fmt.Sprintf("config_%s.json", day10.Format("20060102_15"))
	lateName := fmt.Sprintf("config_%s.json", day10.Add(1*time.Hour).Format("20060102_15"))
	assert.Contains(t, names, earlyName, "earliest file in 7-30 day window must be kept")
	assert.NotContains(t, names, lateName, "duplicate file in 7-30 day window must be removed")

	// Recent file must still be present.
	recentName := fmt.Sprintf("config_%s.json", now.AddDate(0, 0, -3).Format("20060102_15"))
	assert.Contains(t, names, recentName, "recent file must be kept")
}

// TestCleanupOldBackupsNonExistentDirOK verifies that a missing dir returns nil.
func TestCleanupOldBackupsNonExistentDirOK(t *testing.T) {
	err := cleanupOldBackups("/tmp/this_dir_does_not_exist_trading_mcp_test")
	assert.NoError(t, err)
}
