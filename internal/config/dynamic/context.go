package dynamic

import (
	"context"

	"github.com/rantipay/trading-mcp/internal/config/dynamic/domain"
)

// ctxKey is an unexported type used as the context key for ScopedSnapshot values.
// Using a private type prevents collisions with keys defined in other packages.
type ctxKey struct{}

// SnapshotContextKey is the context key under which a *ScopedSnapshot is stored.
// External packages use dynamic.WithSnapshot / dynamic.SnapshotFromContext rather than
// accessing this key directly.
var SnapshotContextKey = ctxKey{}

// defaultStore is the global fallback store used by SnapshotFromContext when the context
// carries no snapshot. Set once at application startup via SetDefaultStore.
var defaultStore *ConfigStore

// SetDefaultStore registers the application-wide ConfigStore used as a fallback by
// SnapshotFromContext. Call this once during initialisation before serving traffic.
func SetDefaultStore(s *ConfigStore) {
	defaultStore = s
}

// WithSnapshot returns a new context that carries snap.
// Pass the returned context through the entire analysis cycle so all subsystems see
// the same immutable configuration view.
func WithSnapshot(ctx context.Context, snap *ScopedSnapshot) context.Context {
	return context.WithValue(ctx, SnapshotContextKey, snap)
}

// SnapshotFromContext extracts the *ScopedSnapshot from ctx.
// If none is present it falls back to defaultStore (global scope), which is useful for
// subsystems that are called outside of a bot analysis cycle.
// Returns nil if neither the context nor defaultStore has a snapshot — callers must guard
// against nil when using the strict API.
func SnapshotFromContext(ctx context.Context) *ScopedSnapshot {
	if s, ok := ctx.Value(SnapshotContextKey).(*ScopedSnapshot); ok {
		return s
	}
	if defaultStore != nil {
		return defaultStore.SnapshotForScope([]ScopeRef{{Type: domain.ScopeGlobal, ID: "*"}})
	}
	return nil
}
