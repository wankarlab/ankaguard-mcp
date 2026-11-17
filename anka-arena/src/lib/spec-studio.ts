// spec-studio.ts — shared types + backend mappers for the Spec Studio screen.
//
// SpecState is the editor's simplified, UI-bound view of a StrategySpec. It is
// imported by the page and every spec-studio subcomponent (this is the single
// source of truth for the type — components must NOT import it from the page
// module, which breaks under [locale] routing).
//
// NON-NEGOTIABLE: the editor never seeds from a client constant. Initial state is
// derived from a backend template (GET /api/v1/specs/templates) or an existing
// own spec (GET /api/v1/specs?author=me) via specStateFromTemplate / the loaded
// StrategySpec. The skin-in-the-game threshold is read from the dynamic policies
// endpoint, never hardcoded.

import { apiFetch } from '@/lib/api';
import type { SpecTemplate } from '@/lib/adapters';

// ── Editor state ──────────────────────────────────────────────────────────────

export type SpecRegime = 'TRENDING' | 'RANGING' | 'BREAKOUT' | 'ANY';

export interface SpecState {
  name:    string;
  regime:  SpecRegime;
  rsiMin:  number;
  rsiMax:  number;
  tp:      number;   // percent, e.g. 0.9
  sl:      number;   // percent, e.g. 1.2
  trail:   boolean;
  maxHold: number;   // minutes
}

// ── Backend StrategySpec → SpecState mapping ────────────────────────────────────
// The backend `spec` object is the full StrategySpec doc (opaque JSON). We pull
// only the fields the block editor exposes; everything else is round-tripped on
// send by BacktestBar's YAML builder. No defaults are invented as business
// values — when a field is absent we fall back to a neutral editor placeholder so
// the sliders have a valid position, NOT a recommended value.

function num(v: unknown, fallback: number): number {
  return typeof v === 'number' && Number.isFinite(v) ? v : fallback;
}

function asRegime(v: unknown): SpecRegime {
  const s = typeof v === 'string' ? v.toUpperCase() : '';
  if (s === 'TRENDING' || s === 'RANGING' || s === 'BREAKOUT' || s === 'ANY') return s;
  return 'ANY';
}

/** Derive editor state from a full StrategySpec doc loaded from the backend. */
export function specStateFromSpecDoc(doc: Record<string, unknown>): SpecState {
  const metadata   = (doc.metadata    as Record<string, unknown>) ?? {};
  const exit       = (doc.exit        as Record<string, unknown>) ?? {};
  const indicators = (doc.indicators  as Record<string, unknown>) ?? {};
  const regime     = (doc.regime      as Record<string, unknown>) ?? {};
  const execution  = (doc.execution   as Record<string, unknown>) ?? {};
  const trailing   = (exit.trailing   as Record<string, unknown>) ?? {};

  const allowed = Array.isArray(regime.allowed_regimes)
    ? (regime.allowed_regimes as unknown[])
    : [];
  const firstRegime = allowed.length > 0 ? allowed[0] : undefined;

  // max_hold is expressed in minutes; the backend stores it in execution or omits
  // it — fall back to a neutral editor slider position (mid-range), not advice.
  const maxHold = num(execution.max_hold_min, 240);

  return {
    name:    typeof metadata.name === 'string' ? (metadata.name as string) : 'untitled-spec',
    regime:  asRegime(firstRegime),
    rsiMin:  num(indicators.rsi_oversold, 30),
    rsiMax:  num(indicators.rsi_overbought, 70),
    tp:      num(exit.take_profit_pct, 0.9),
    sl:      num(exit.stop_loss_pct, 1.2),
    trail:   trailing.enabled === true,
    maxHold,
  };
}

/** Derive editor state from a backend spec template. */
export function specStateFromTemplate(t: SpecTemplate): SpecState {
  const state = specStateFromSpecDoc(t.spec ?? {});
  // Prefer the template's own name/regime metadata when present.
  return {
    ...state,
    name:   state.name && state.name !== 'untitled-spec' ? state.name : (t.name || 'untitled-spec'),
    regime: state.regime !== 'ANY' ? state.regime : asRegime(t.regime),
  };
}

// ── Skin-in-the-game threshold (dynamic, read from backend policies) ────────────
// C2.2: the copyable threshold is NOT a client constant. It is read from the
// dynamic policies endpoint. If the backend does not (yet) expose a day count,
// `days` is null and the FE renders the requirement WITHOUT inventing a number.

export interface SkinInGamePolicy {
  /** Live-trading days an author must run a spec before it becomes copyable. */
  days: number | null;
}

export async function fetchSkinInGamePolicy(): Promise<SkinInGamePolicy> {
  try {
    const d = await apiFetch<Record<string, unknown>>('/api/v1/admin/policies');
    // Accept any of the field names the backend may expose for this threshold.
    const raw =
      (d?.skin_in_game_days as unknown) ??
      (d?.min_live_days as unknown) ??
      (d?.copyable_after_days as unknown) ??
      (d?.live_days_required as unknown);
    const days = typeof raw === 'number' && Number.isFinite(raw) ? raw : null;
    return { days };
  } catch {
    // Endpoint gated/unavailable — do not invent a number.
    return { days: null };
  }
}
