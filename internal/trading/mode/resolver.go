// Package mode provides the shared seam for selectable trading-mode logic
// (spot / futures / margin). Phase 1 exposes the resolver; later waves extend it.
package mode

import (
	"context"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/config/dynamic"
	"github.com/rantipay/trading-mcp/internal/gct"
)

// Decision is the resolved trading-mode outcome for a single order.
type Decision struct {
	// MarketType is the effective market type to stamp on the order.
	// Empty string means "use legacy routing" (i.e. caller should not override).
	MarketType gct.MarketType
	// Leverage is the effective leverage to use for the order.
	Leverage float64
}

// TradingModeRow is the minimal view of a persisted trading mode row that the
// Resolver requires. Defined here to avoid an import cycle with the storage
// package (storage → master → services → trading/mode).
type TradingModeRow struct {
	Scope         string // "global" or "pair"
	Pair          string
	MarketType    string // spot | futures | margin
	ModeSelection string // auto | manual | hybrid
	Leverage      float64
}

// ModeRepository is the subset of storage.TradingModeRepository that the
// Resolver needs. Declared as an interface so tests can inject fakes and to
// avoid a direct import of the storage package.
type ModeRepository interface {
	GetGlobalRow(ctx context.Context) (*TradingModeRow, error)
	GetForPairRow(ctx context.Context, pair string) (*TradingModeRow, error)
}

// Resolver derives the effective trading mode and leverage for an order from
// the dynamic config store and (optionally) the ModeRepository.
// It is safe for concurrent use.
type Resolver struct {
	log  *logrus.Logger
	repo ModeRepository // nil → no per-pair DB overrides, behaviour unchanged
}

// ResolveWithContext is identical to Resolve but accepts an optional
// MarketContext (regime, ATR%, confidence) used by the Wave 3-A AUTO logic
// when bot.auto_mode_intelligence == true.
//
// When mc is the zero value (empty Regime) the AUTO intelligence path produces
// a legacy pass-through, so callers that cannot cheaply supply a MarketContext
// can safely pass MarketContext{} — behaviour is unchanged.
//
// Callers that can supply regime data should prefer this method so that the
// regime-driven mode selection takes effect once the flag is enabled.
func (r *Resolver) ResolveWithContext(ctx context.Context, pair string, legacyLeverage float64, mc MarketContext) Decision {
	// ── 1. Per-pair DB override ──────────────────────────────────────────────
	if r.repo != nil && pair != "" {
		override, err := r.repo.GetForPairRow(ctx, pair)
		if err != nil {
			r.log.WithError(err).WithField("pair", pair).Warn("mode.Resolver: failed to query per-pair override, continuing")
		} else if override != nil {
			return r.decisionFromRow(override, legacyLeverage)
		}
	}

	// ── 2. Global DB manual setting ──────────────────────────────────────────
	if r.repo != nil {
		global, err := r.repo.GetGlobalRow(ctx)
		if err != nil {
			r.log.WithError(err).Warn("mode.Resolver: failed to query global DB mode, continuing")
		} else if global != nil && global.ModeSelection == "manual" {
			return r.decisionFromRow(global, legacyLeverage)
		}
	}

	// ── 3. Dynamic config store ──────────────────────────────────────────────
	snap := dynamic.SnapshotFromContext(ctx)
	if snap == nil {
		return Decision{Leverage: legacyLeverage}
	}

	modeSelection := snap.String("bot.mode_selection")

	switch modeSelection {
	case "manual":
		return r.resolveManual(snap, pair, legacyLeverage)
	case "auto", "hybrid", "":
		// Wave 3-A: regime-driven AUTO when the flag is explicitly enabled.
		// Flag OFF (default false) → identical legacy pass-through as before.
		if snap.Bool("bot.auto_mode_intelligence") {
			minLev := snap.Int("bot.auto_min_leverage")
			maxLev := snap.Int("bot.auto_max_leverage")
			if minLev <= 0 {
				minLev = 1
			}
			if maxLev <= 0 || maxLev < minLev {
				maxLev = 5
			}
			return decideAutoMode(mc, minLev, maxLev, legacyLeverage)
		}
		return Decision{Leverage: legacyLeverage}
	default:
		r.log.WithField("mode_selection", modeSelection).Warn("mode.Resolver: unknown mode_selection, falling back to legacy")
		return Decision{Leverage: legacyLeverage}
	}
}

// NewResolver creates a Resolver.
//
// repo may be nil — when nil the resolver skips all DB lookups and behaviour
// is identical to the pre-Wave-2 implementation (legacy pass-through).
// The logger is required; the config store is read via the context's embedded
// ScopedSnapshot (dynamic.SnapshotFromContext).
func NewResolver(log *logrus.Logger, repo ModeRepository) *Resolver {
	return &Resolver{log: log, repo: repo}
}

// Resolve returns the effective mode Decision for the given pair, falling back
// to legacyLeverage when the mode_selection is "auto" or "hybrid".
//
// Precedence (highest first):
//  1. Per-pair DB override (when repo != nil and a row exists for pair).
//  2. Global DB manual setting (when repo != nil and global mode_selection == "manual").
//  3. Dynamic config store (bot.mode_selection / bot.market_type from context).
//  4. Legacy pass-through: return legacyLeverage unchanged.
//
// Callers that have regime/ATR/confidence available should prefer
// ResolveWithContext so that Wave 3-A AUTO intelligence can be used when the
// bot.auto_mode_intelligence flag is enabled. Resolve passes an empty
// MarketContext which causes the intelligence path to produce a legacy
// pass-through — so flag OFF behaviour is byte-identical to pre-Wave-3.
func (r *Resolver) Resolve(ctx context.Context, pair string, legacyLeverage float64) Decision {
	return r.ResolveWithContext(ctx, pair, legacyLeverage, MarketContext{})
}

// decisionFromRow converts a TradingModeRow to a Decision.
func (r *Resolver) decisionFromRow(m *TradingModeRow, legacyLeverage float64) Decision {
	switch gct.MarketType(m.MarketType) {
	case gct.MarketTypeSpot:
		lv := m.Leverage
		if lv == 0 {
			lv = 1
		}
		return Decision{MarketType: gct.MarketTypeSpot, Leverage: lv}
	case gct.MarketTypeFutures:
		lv := m.Leverage
		if lv == 0 {
			lv = legacyLeverage
		}
		return Decision{MarketType: gct.MarketTypeFutures, Leverage: lv}
	case gct.MarketTypeMargin:
		// Wave 4 implements margin; until then treat as futures passthrough.
		r.log.WithField("scope", m.Scope).Warn("mode.Resolver: margin mode not yet supported, routing as futures")
		lv := m.Leverage
		if lv == 0 {
			lv = legacyLeverage
		}
		return Decision{MarketType: gct.MarketTypeFutures, Leverage: lv}
	default:
		r.log.WithField("market_type", m.MarketType).Warn("mode.Resolver: unknown market_type in DB row, falling back to legacy")
		return Decision{Leverage: legacyLeverage}
	}
}

// resolveManual applies the explicit bot.market_type from the dynamic config snapshot.
func (r *Resolver) resolveManual(snap *dynamic.ScopedSnapshot, pair string, legacyLeverage float64) Decision {
	// Wave 2: per-pair DB override lookup already handled above before reaching here.
	marketTypeStr := snap.String("bot.market_type")

	switch gct.MarketType(marketTypeStr) {
	case gct.MarketTypeSpot:
		return Decision{
			MarketType: gct.MarketTypeSpot,
			Leverage:   1,
		}
	case gct.MarketTypeFutures:
		return Decision{
			MarketType: gct.MarketTypeFutures,
			Leverage:   legacyLeverage,
		}
	case gct.MarketTypeMargin:
		// Wave 4 implements margin; until then treat as futures passthrough.
		r.log.WithField("pair", pair).Warn("mode.Resolver: margin mode not yet supported, routing as futures")
		return Decision{
			MarketType: gct.MarketTypeFutures,
			Leverage:   legacyLeverage,
		}
	default:
		// Unknown or empty market_type — fall back to legacy.
		r.log.WithField("market_type", marketTypeStr).Warn("mode.Resolver: unknown market_type, falling back to legacy")
		return Decision{Leverage: legacyLeverage}
	}
}
