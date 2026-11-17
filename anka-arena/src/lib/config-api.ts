// config-api.ts — Control surface for the FULL dynamic-config registry.
//
// Unlike scalping.ts (a curated subset of "gate" knobs), this client speaks to
// the generic dynamic-config API so the operator can browse/edit any of the
// ~400 params across the 35 groups, manage user-defined "modes" (presets with
// category="mode"), and govern manual-position adoption.
//
// Nothing here hardcodes a config value: the registry (min/max/enum/default/
// requires_restart) is fetched from the backend and the backend re-validates
// every write. All writes use the GLOBAL scope (scope_type=global, scope_id="*")
// per the project rule for bot-wide params.

import { api } from '@/lib/api';

// ── Param / group shapes ──────────────────────────────────────────────────────

export type ParamType =
  | 'float'
  | 'int'
  | 'bool'
  | 'string'
  | 'duration'
  | 'json'
  | 'enum';

/**
 * A single registry parameter as returned by GET /params. Field names mirror the
 * backend registry envelope. Optional fields are absent when the registry does
 * not constrain them (e.g. a free string has no min/max/enum).
 */
export interface Param {
  key: string;
  type: ParamType | string;
  group: string;
  description: string;
  default: string;
  current_effective_value: string;
  has_override: boolean;
  min?: number;
  max?: number;
  step?: number;
  enum_values?: string[];
  requires_restart?: boolean;
  unit?: string;
}

export interface Group {
  name: string;
  description?: string;
  param_count?: number;
}

// ── Preset / mode shapes ──────────────────────────────────────────────────────

/**
 * A preset/mode. category="mode" presets are the user-defined modes managed by
 * ModeManager; editable=true means the operator may edit/delete it (seeded/system
 * presets are editable=false). params maps config key -> string value.
 */
export interface Preset {
  name: string;
  description?: string;
  risk_label?: string;
  category?: string;
  editable?: boolean;
  is_active?: boolean;
  params: Record<string, string>;
  last_applied_at?: string | null;
}

export interface CreatePresetInput {
  name: string;
  description?: string;
  risk_label?: string;
  category?: string;
  editable?: boolean;
  params: Record<string, string>;
}

// ── Bot positions ─────────────────────────────────────────────────────────────

export interface BotPosition {
  symbol: string;
  side: string; // "long" | "short" | "buy" | "sell"
  size: number;
  entry_price?: number;
  mark_price?: number;
  notional_usd?: number;
  pnl: number;
  pnl_pct?: number;
  adopted: boolean;
  has_exchange_sl: boolean;
  source: string; // "bot" | "manual"
}

// Raw shape as returned by GET /api/v1/bot/positions (backend botPositionView).
// The backend names the PnL field `unrealized_pnl` and omits a percentage; the
// adapter below normalises it to the FE `pnl` / `pnl_pct` contract.
interface RawBotPosition {
  symbol: string;
  side: string;
  size: number;
  entry_price?: number;
  mark_price?: number;
  notional_usd?: number;
  unrealized_pnl?: number;
  pnl?: number;
  pnl_pct?: number;
  adopted?: boolean;
  has_exchange_sl?: boolean;
  source?: string;
}

// ── Helpers ───────────────────────────────────────────────────────────────────

const GLOBAL_SCOPE = { scope_type: 'global', scope_id: '*' } as const;

/** Coerce an unknown value to the string form the backend config store expects. */
function toStringValue(value: unknown): string {
  if (typeof value === 'string') return value;
  if (typeof value === 'boolean') return value ? 'true' : 'false';
  if (value == null) return '';
  if (typeof value === 'object') return JSON.stringify(value);
  return String(value);
}

// ── Groups ────────────────────────────────────────────────────────────────────

export async function getGroups(): Promise<Group[]> {
  // The backend /groups endpoint returns a plain array of group-name strings
  // (e.g. ["adopt","ai",...]), not objects. Normalize both shapes — bare strings
  // and {name,...} objects — into Group so callers can rely on `.name`.
  const d = await api.get<{ groups?: unknown[] } | unknown[]>('/api/v1/admin/config/groups');
  const list = Array.isArray(d) ? d : d?.groups ?? [];
  return list.map((g) =>
    typeof g === 'string' ? { name: g } : (g as Group),
  );
}

// ── Params ────────────────────────────────────────────────────────────────────

export async function getParams(options?: {
  group?: string;
  modified?: boolean;
}): Promise<Param[]> {
  const qs = new URLSearchParams();
  if (options?.group) qs.set('group', options.group);
  if (options?.modified) qs.set('modified', 'true');
  const suffix = qs.toString() ? `?${qs.toString()}` : '';
  const d = await api.get<{ params?: Param[] } | Param[]>(
    `/api/v1/admin/config/params${suffix}`,
  );
  if (Array.isArray(d)) return d;
  return d?.params ?? [];
}

// Raw envelope returned by GET /params/:key — DIFFERENT from the flat objects in
// the list endpoint: the single endpoint nests registry metadata under `registry`
// and names the resolved value `effective_value` (the list uses a flat shape with
// `current_effective_value`). normalizeParam folds either shape into `Param`.
interface RawParamEnvelope {
  effective_value?: string;
  current_effective_value?: string;
  has_override?: boolean;
  default?: string;
  type?: string;
  group?: string;
  description?: string;
  registry?: {
    default?: string;
    type?: string;
    group?: string;
    description?: string;
    min_value?: number | null;
    max_value?: number | null;
    enum_values?: string[] | null;
    requires_restart?: boolean;
  };
}

function normalizeParam(key: string, raw: RawParamEnvelope): Param {
  const reg = raw.registry;
  return {
    key,
    type: reg?.type ?? raw.type ?? 'string',
    group: reg?.group ?? raw.group ?? '',
    description: reg?.description ?? raw.description ?? '',
    default: reg?.default ?? raw.default ?? '',
    // Single endpoint: `effective_value`; list endpoint: `current_effective_value`.
    current_effective_value: raw.effective_value ?? raw.current_effective_value ?? reg?.default ?? raw.default ?? '',
    has_override: Boolean(raw.has_override),
    min: reg?.min_value ?? undefined,
    max: reg?.max_value ?? undefined,
    enum_values: reg?.enum_values ?? undefined,
    requires_restart: reg?.requires_restart,
  };
}

export async function getParam(key: string): Promise<Param> {
  const d = await api.get<{ param?: RawParamEnvelope } | RawParamEnvelope>(
    `/api/v1/admin/config/params/${encodeURIComponent(key)}`,
  );
  const raw = (d && typeof d === 'object' && 'param' in d && d.param ? d.param : d) as RawParamEnvelope;
  return normalizeParam(key, raw);
}

export async function updateParam(
  key: string,
  value: unknown,
  reason: string,
): Promise<void> {
  await api.put(`/api/v1/admin/config/params/${encodeURIComponent(key)}`, {
    value: toStringValue(value),
    reason,
    ...GLOBAL_SCOPE,
  });
}

// ── Presets / modes ───────────────────────────────────────────────────────────

export async function getPresets(category?: string): Promise<Preset[]> {
  const suffix = category ? `?category=${encodeURIComponent(category)}` : '';
  const d = await api.get<{ presets?: Preset[] } | Preset[]>(
    `/api/v1/admin/config/presets${suffix}`,
  );
  const list = Array.isArray(d) ? d : d?.presets ?? [];
  // Normalize: ensure params is always an object so callers can count keys safely.
  return list.map((p) => ({ ...p, params: p.params ?? {} }));
}

export async function getActiveMode(): Promise<Preset | null> {
  try {
    const d = await api.get<{ preset?: Preset | null } | Preset | null>(
      '/api/v1/admin/config/presets/active',
    );
    if (!d) return null;
    if (typeof d === 'object' && 'preset' in d) return d.preset ?? null;
    const p = d as Preset;
    return { ...p, params: p.params ?? {} };
  } catch {
    // No active mode (404) or endpoint not wired yet — treat as "none".
    return null;
  }
}

export async function createPreset(data: CreatePresetInput): Promise<Preset> {
  const d = await api.post<{ preset?: Preset } | Preset>(
    '/api/v1/admin/config/presets',
    {
      category: 'mode',
      editable: true,
      ...data,
      params: data.params ?? {},
    },
  );
  if (d && typeof d === 'object' && 'preset' in d && d.preset) return d.preset;
  return d as Preset;
}

export async function updatePreset(
  name: string,
  data: Partial<CreatePresetInput>,
): Promise<Preset> {
  const d = await api.put<{ preset?: Preset } | Preset>(
    `/api/v1/admin/config/presets/${encodeURIComponent(name)}`,
    data,
  );
  if (d && typeof d === 'object' && 'preset' in d && d.preset) return d.preset;
  return d as Preset;
}

export async function deletePreset(name: string): Promise<void> {
  await api.delete(`/api/v1/admin/config/presets/${encodeURIComponent(name)}`);
}

export async function applyPreset(name: string, reason: string): Promise<void> {
  await api.post(
    `/api/v1/admin/config/presets/${encodeURIComponent(name)}/apply`,
    { reason, ...GLOBAL_SCOPE },
  );
}

export async function rollbackPreset(name: string): Promise<void> {
  await api.post(
    `/api/v1/admin/config/presets/${encodeURIComponent(name)}/rollback`,
    {},
  );
}

// ── Bot positions ─────────────────────────────────────────────────────────────

export async function getBotPositions(): Promise<BotPosition[]> {
  const d = await api.get<{ positions?: RawBotPosition[] } | RawBotPosition[]>(
    '/api/v1/bot/positions',
  );
  const list = Array.isArray(d) ? d : d?.positions ?? [];
  return list.map((p) => {
    // Backend emits `unrealized_pnl`; tolerate an already-mapped `pnl` too.
    const pnl = p.pnl ?? p.unrealized_pnl ?? 0;
    // Backend omits a percentage — derive an unleveraged price-move % from
    // entry/mark prices when both are present (sign flipped for shorts).
    let pnlPct = p.pnl_pct;
    if (pnlPct == null && p.entry_price && p.mark_price && p.entry_price !== 0) {
      const move = ((p.mark_price - p.entry_price) / p.entry_price) * 100;
      pnlPct = /short|sell/i.test(p.side) ? -move : move;
    }
    return {
      symbol: p.symbol,
      side: p.side,
      size: p.size,
      entry_price: p.entry_price,
      mark_price: p.mark_price,
      notional_usd: p.notional_usd,
      pnl,
      pnl_pct: pnlPct,
      adopted: Boolean(p.adopted),
      has_exchange_sl: Boolean(p.has_exchange_sl),
      source: p.source ?? 'manual',
    };
  });
}

// ── Manual-protection toggle ──────────────────────────────────────────────────
// The master switch is just a bool param; expose dedicated helpers so the
// component reads the same key the backend governs.

export const MANUAL_PROTECTION_KEY = 'bot.adopt.all_pairs.enabled';

export async function getManualProtection(): Promise<boolean> {
  const p = await getParam(MANUAL_PROTECTION_KEY);
  return (p.current_effective_value ?? p.default) === 'true';
}

export async function setManualProtection(enabled: boolean): Promise<void> {
  await updateParam(MANUAL_PROTECTION_KEY, enabled, 'manual protection toggle');
}
