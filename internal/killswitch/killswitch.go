// Package killswitch holds the process-wide ring-0 (global stop) state and the
// registry of in-process trading engines that ring 0 must be able to stop.
//
// It is intentionally nearly dependency-free (only the leaf package
// toolpolicy) so any layer (REST handlers, the MCP
// server, the xsec job runner, main) can consult it without import cycles.
package killswitch

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/toolpolicy"
)

// ErrActive is returned by risk-increasing paths while ring 0 is active.
var ErrActive = errors.New("global kill switch (ring 0) is active: risk-increasing actions are blocked")

// ErrorCode is the stable API error code used for HTTP 423 / tool errors.
const ErrorCode = "KILLSWITCH_ACTIVE"

var ring0 atomic.Bool

// Ring0Active reports whether the global stop is currently active.
func Ring0Active() bool { return ring0.Load() }

// SetRing0Active sets the process-wide ring-0 flag. The REST handler is the
// owner of this state (it persists it and restores it on boot).
func SetRing0Active(v bool) { ring0.Store(v) }

// Check returns ErrActive while ring 0 is active, nil otherwise.
func Check() error {
	if Ring0Active() {
		return ErrActive
	}
	return nil
}

// EngineStopper is an in-process trading engine that ring 0 can stop.
// Stop must be idempotent and treat "already stopped" as success.
type EngineStopper interface {
	Name() string
	Stop(ctx context.Context) error
}

var (
	enginesMu sync.RWMutex
	engines   []EngineStopper
)

// RegisterEngine adds an engine to the registry (nil is ignored). Engines can
// be registered after the REST router is built; ring 0 reads the registry at
// trigger time.
func RegisterEngine(e EngineStopper) {
	if e == nil {
		return
	}
	enginesMu.Lock()
	defer enginesMu.Unlock()
	engines = append(engines, e)
}

// Engines returns a snapshot of the registered engines.
func Engines() []EngineStopper {
	enginesMu.RLock()
	defer enginesMu.RUnlock()
	out := make([]EngineStopper, len(engines))
	copy(out, engines)
	return out
}

// FuncEngine adapts a plain stop function into an EngineStopper.
type FuncEngine struct {
	EngineName string
	StopFn     func(ctx context.Context) error
}

// Name implements EngineStopper.
func (f FuncEngine) Name() string { return f.EngineName }

// Stop implements EngineStopper; a nil StopFn is a no-op.
func (f FuncEngine) Stop(ctx context.Context) error {
	if f.StopFn == nil {
		return nil
	}
	return f.StopFn(ctx)
}

// WriteLocked writes the standard HTTP 423 / KILLSWITCH_ACTIVE JSON body for
// net/http handlers. Callers use: if killswitch.Ring0Active() { WriteLocked(w); return }.
func WriteLocked(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusLocked)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": false,
		"error":   map[string]string{"code": ErrorCode, "message": ErrActive.Error()},
	})
}

// RiskIncreasingTools is the set of MCP tools that can open or enlarge a
// position or (re)start a trading loop. Ring 0 refuses exactly these; cancels,
// closes and read-only tools stay available.
//
// It is derived from the tool classification (class trade_increase in
// internal/mcp-trading/toolpolicy), which is the single source of truth. A tool
// whose class depends on its arguments (submit_futures_order_direct) is listed
// here by its base class; callers must use toolpolicy.ClassForCall to apply the
// reduce-only exception.
var RiskIncreasingTools = toolpolicy.ToolsOfClass(toolpolicy.ClassTradeIncrease)

// BlockedByRing0 reports whether a call to tool with the given arguments must
// be refused while ring 0 is active: exactly the calls classified as
// trade_increase. Unclassified tools are refused too (fail closed).
func BlockedByRing0(tool string, args map[string]interface{}) bool {
	if !Ring0Active() {
		return false
	}
	class, ok := toolpolicy.ClassForCall(tool, args)
	if !ok {
		return true
	}
	return class == toolpolicy.ClassTradeIncrease
}
