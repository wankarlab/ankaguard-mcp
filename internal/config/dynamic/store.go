package dynamic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/config/dynamic/domain"
	"github.com/rantipay/trading-mcp/internal/config/dynamic/repository"
)

// scopeKey is the composite key for looking up a scoped value in the snapshot.
type scopeKey struct {
	Key       string
	ScopeType domain.ScopeType
	ScopeID   string
}

// ScopeRef describes one level of the scope resolution chain.
type ScopeRef struct {
	Type domain.ScopeType
	ID   string
}

// ScopedSnapshot binds an immutable Snapshot to a specific scope resolution chain.
// All accessor methods (Int, Float, Bool, …) walk that chain for lookups.
type ScopedSnapshot struct {
	*Snapshot
	chain []ScopeRef // ordered: most-specific → least-specific
}

// Snapshot is an immutable, point-in-time view of the entire configuration state.
// It is replaced atomically; existing holders keep the old copy until they drop the reference.
type Snapshot struct {
	Hash     string    // SHA-256 of canonical key=value lines for reproducibility
	BuiltAt  time.Time
	registry map[string]*domain.ParameterRegistry // key → registry entry
	values   map[scopeKey]string                  // (key, scope_type, scope_id) → string value
}

// ConfigStore is the single in-memory authority for dynamic configuration.
// Reads are lock-free via atomic.Pointer; writes are serialised by muWrite.
type ConfigStore struct {
	repo repository.ConfigRepository
	log  *logrus.Logger

	snap    atomic.Pointer[Snapshot] // lock-free read path
	muWrite sync.Mutex               // serialises snapshot rebuilds

	subscribers map[string][]Subscriber // group → callbacks
	muSubs      sync.RWMutex

	validators map[string]Validator // key → custom per-key validator (extensible)
}

// NewConfigStore creates a ConfigStore, performs an initial Load, and returns it.
func NewConfigStore(repo repository.ConfigRepository, log *logrus.Logger) (*ConfigStore, error) {
	s := &ConfigStore{
		repo:        repo,
		log:         log,
		subscribers: make(map[string][]Subscriber),
		validators:  make(map[string]Validator),
	}
	if err := s.Load(context.Background()); err != nil {
		return nil, fmt.Errorf("config store initial load: %w", err)
	}
	return s, nil
}

// Load performs a full reload from the repository and atomically replaces the snapshot.
// Safe to call concurrently — the muWrite lock serialises the rebuild.
func (s *ConfigStore) Load(ctx context.Context) error {
	regs, err := s.repo.ListRegistry(ctx, "")
	if err != nil {
		return fmt.Errorf("list registry: %w", err)
	}
	vals, err := s.repo.ListAllValues(ctx)
	if err != nil {
		return fmt.Errorf("list all values: %w", err)
	}

	snap := buildSnapshot(regs, vals)

	s.muWrite.Lock()
	s.snap.Store(snap)
	s.muWrite.Unlock()

	s.log.WithFields(logrus.Fields{
		"hash":      snap.Hash,
		"built_at":  snap.BuiltAt,
		"params":    len(snap.registry),
		"values":    len(snap.values),
	}).Info("config snapshot loaded")
	return nil
}

// Snapshot returns the current immutable snapshot (lock-free).
func (s *ConfigStore) Snapshot() *Snapshot {
	return s.snap.Load()
}

// SnapshotForScope returns a ScopedSnapshot bound to the given scope chain.
// Intended to be called once per runAnalysis cycle so the cycle sees a consistent view.
func (s *ConfigStore) SnapshotForScope(chain []ScopeRef) *ScopedSnapshot {
	return &ScopedSnapshot{Snapshot: s.Snapshot(), chain: chain}
}

// Repo exposes the underlying ConfigRepository for callers that need direct access.
func (s *ConfigStore) Repo() repository.ConfigRepository {
	return s.repo
}

// Set validates, persists, and hot-reloads a single parameter value.
func (s *ConfigStore) Set(ctx context.Context, val *domain.ParameterValue, reason string) error {
	snap := s.Snapshot()
	reg, ok := snap.registry[val.Key]
	if !ok {
		return fmt.Errorf("unknown parameter key: %s", val.Key)
	}

	if err := ValidateValue(reg, val.Value); err != nil {
		return fmt.Errorf("validate %s: %w", val.Key, err)
	}
	// Run any custom per-key validator registered on the store.
	if v, ok := s.validators[val.Key]; ok {
		if err := v(reg, val.Value); err != nil {
			return fmt.Errorf("custom validate %s: %w", val.Key, err)
		}
	}

	// Read old value for change notification.
	old := ""
	if ov, ok := snap.values[scopeKey{Key: val.Key, ScopeType: val.ScopeType, ScopeID: val.ScopeID}]; ok {
		old = ov
	}

	if err := s.repo.SetValue(ctx, val, reason); err != nil {
		return fmt.Errorf("repo set value: %w", err)
	}

	if err := s.reload(ctx); err != nil {
		s.log.WithError(err).Warn("config store reload after Set failed; in-memory state may be stale")
	}

	s.notify([]Change{{
		Key:       val.Key,
		ScopeType: val.ScopeType,
		ScopeID:   val.ScopeID,
		OldValue:  old,
		NewValue:  val.Value,
	}})
	return nil
}

// BulkSet validates all values individually, runs cross-param validators against a virtual
// snapshot, and only commits to the repository when all checks pass.
func (s *ConfigStore) BulkSet(ctx context.Context, vals []*domain.ParameterValue, by, reason string) error {
	snap := s.Snapshot()

	// 1. Per-value validation.
	for _, v := range vals {
		reg, ok := snap.registry[v.Key]
		if !ok {
			return fmt.Errorf("unknown parameter key: %s", v.Key)
		}
		if err := ValidateValue(reg, v.Value); err != nil {
			return fmt.Errorf("validate %s: %w", v.Key, err)
		}
	}

	// 2. Build virtual snapshot (overlay incoming changes on current snapshot).
	virtual := cloneSnapshot(snap)
	for _, v := range vals {
		virtual.values[scopeKey{Key: v.Key, ScopeType: v.ScopeType, ScopeID: v.ScopeID}] = v.Value
	}
	virtual.Hash = computeHash(virtual.values)
	virtual.BuiltAt = time.Now()

	// 3. Cross-param validation against the virtual snapshot.
	// Build a generic global scope chain for cross-validators.
	scope := []ScopeRef{{Type: domain.ScopeGlobal, ID: "*"}}
	if err := runCrossValidators(virtual, scope); err != nil {
		return fmt.Errorf("cross-param validation failed: %w", err)
	}

	// 4. Collect old values for notifications.
	changes := make([]Change, 0, len(vals))
	for _, v := range vals {
		old := ""
		if ov, ok := snap.values[scopeKey{Key: v.Key, ScopeType: v.ScopeType, ScopeID: v.ScopeID}]; ok {
			old = ov
		}
		changes = append(changes, Change{
			Key:       v.Key,
			ScopeType: v.ScopeType,
			ScopeID:   v.ScopeID,
			OldValue:  old,
			NewValue:  v.Value,
		})
	}

	// 5. Commit to DB.
	if err := s.repo.BulkSetValues(ctx, vals, by, reason); err != nil {
		return fmt.Errorf("repo bulk set: %w", err)
	}

	// 6. Atomically swap in the virtual snapshot (already validated).
	s.muWrite.Lock()
	s.snap.Store(virtual)
	s.muWrite.Unlock()

	s.notify(changes)
	return nil
}

// ---- internal helpers ----

// reload fetches the latest state from the repository and rebuilds the snapshot.
// Must NOT be called while holding muWrite.
func (s *ConfigStore) reload(ctx context.Context) error {
	regs, err := s.repo.ListRegistry(ctx, "")
	if err != nil {
		return err
	}
	vals, err := s.repo.ListAllValues(ctx)
	if err != nil {
		return err
	}
	snap := buildSnapshot(regs, vals)
	s.muWrite.Lock()
	s.snap.Store(snap)
	s.muWrite.Unlock()
	return nil
}

// buildSnapshot constructs a Snapshot from raw registry and value slices.
func buildSnapshot(regs []*domain.ParameterRegistry, vals []*domain.ParameterValue) *Snapshot {
	registry := make(map[string]*domain.ParameterRegistry, len(regs))
	for _, r := range regs {
		registry[r.Key] = r
	}
	values := make(map[scopeKey]string, len(vals))
	for _, v := range vals {
		values[scopeKey{Key: v.Key, ScopeType: v.ScopeType, ScopeID: v.ScopeID}] = v.Value
	}
	return &Snapshot{
		Hash:     computeHash(values),
		BuiltAt:  time.Now(),
		registry: registry,
		values:   values,
	}
}

// cloneSnapshot performs a shallow clone suitable for virtual modifications.
func cloneSnapshot(src *Snapshot) *Snapshot {
	registry := make(map[string]*domain.ParameterRegistry, len(src.registry))
	for k, v := range src.registry {
		registry[k] = v
	}
	values := make(map[scopeKey]string, len(src.values))
	for k, v := range src.values {
		values[k] = v
	}
	return &Snapshot{
		Hash:     src.Hash,
		BuiltAt:  src.BuiltAt,
		registry: registry,
		values:   values,
	}
}

// ToScopedSnapshot wraps an existing Snapshot in a ScopedSnapshot using the global scope.
// This is used by the backtest engine to inject a virtual snapshot into a context.
func (snap *Snapshot) ToScopedSnapshot() *ScopedSnapshot {
	return &ScopedSnapshot{
		Snapshot: snap,
		chain:    []ScopeRef{{Type: domain.ScopeGlobal, ID: "*"}},
	}
}

// SnapshotWithOverrides builds an in-memory virtual snapshot by cloning the current
// (or named preset) snapshot and overlaying the supplied key→value overrides.
// The resulting Snapshot is NOT stored in the ConfigStore and does NOT touch the DB;
// it exists only for the duration of a backtest run.
//
// baselinePreset interpretation:
//   - "" or "current" → use the live snapshot
//   - any other value  → future: load preset (not yet implemented, falls back to current)
func (s *ConfigStore) SnapshotWithOverrides(baselinePreset string, overrides map[string]string) *Snapshot {
	base := s.Snapshot()
	virtual := cloneSnapshot(base)

	// Apply each override using the global scope key so it is visible to all scope chains.
	for key, val := range overrides {
		virtual.values[scopeKey{Key: key, ScopeType: domain.ScopeGlobal, ScopeID: "*"}] = val
	}

	virtual.Hash = computeHash(virtual.values)
	virtual.BuiltAt = time.Now()
	return virtual
}

// computeHash returns a SHA-256 fingerprint of all key=value pairs (canonically sorted).
func computeHash(values map[scopeKey]string) string {
	lines := make([]string, 0, len(values))
	for k, v := range values {
		lines = append(lines, fmt.Sprintf("%s|%s|%s=%s", k.Key, string(k.ScopeType), k.ScopeID, v))
	}
	sort.Strings(lines)

	h := sha256.New()
	for _, l := range lines {
		h.Write([]byte(l + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}
