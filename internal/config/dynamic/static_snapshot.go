package dynamic

import (
	"time"

	"github.com/rantipay/trading-mcp/internal/config/dynamic/domain"
)

// NewStaticSnapshot builds a global-scope snapshot holding only the given raw
// key→value pairs, with no registry defaults. Unset keys resolve as "not
// configured", so callers see the same behaviour as a key missing from the DB.
// Intended for tests and offline tools that need a snapshot without a store.
func NewStaticSnapshot(values map[string]string) *ScopedSnapshot {
	snap := &Snapshot{
		BuiltAt:  time.Now(),
		registry: map[string]*domain.ParameterRegistry{},
		values:   make(map[scopeKey]string, len(values)),
	}
	for k, v := range values {
		snap.values[scopeKey{Key: k, ScopeType: domain.ScopeGlobal, ScopeID: "*"}] = v
	}
	return snap.ToScopedSnapshot()
}
