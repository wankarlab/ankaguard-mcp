// scalping.ts — Control surface for the live scalping gate stack.
//
// Every VALUE shown/edited here is read live from the backend dynamic-config API
// (GET /api/v1/admin/config/params?group=bot → current_effective_value) and written
// back via PUT /params/:key or POST /params/bulk. Nothing here hardcodes a config
// value: the constants below are only UI affordances (slider bounds, labels) that
// mirror the manifest (internal/config/dynamic/manifest/manifest.go). The backend
// re-validates every write against the registry min/max/enum, so a stale bound
// here can never push an out-of-range value past the server.
//
// All writes use the GLOBAL scope (scope_type=global, scope_id="*") — the project
// rule for bot-wide params. confidence_threshold also supports a per-pair scope.

import { api } from '@/lib/api';

export type KnobType = 'float' | 'int' | 'bool' | 'enum' | 'string';

export interface Knob {
  key: string;
  type: KnobType;
  /** i18n sub-key under the Scalping namespace, e.g. 'k_confidence' */
  labelKey: string;
  min?: number;
  max?: number;
  step?: number;
  enum?: string[];
}

// The gate stack, in the order the bot evaluates it. These bounds mirror the
// manifest exactly (confirmed against manifest.go). Order matters for the UI:
// confidence_threshold is the dominant filter, so it leads.
export const KNOBS: Knob[] = [
  { key: 'bot.confidence_threshold',          type: 'float', labelKey: 'k_confidence',   min: 0.5,  max: 0.95, step: 0.01 },
  { key: 'bot.validation_gate.enabled',       type: 'bool',  labelKey: 'k_valgate' },
  { key: 'bot.validation.min_profit_factor',  type: 'float', labelKey: 'k_min_pf',       min: 1.0,  max: 3.0,  step: 0.01 },
  { key: 'bot.validation.min_consistency',    type: 'float', labelKey: 'k_min_consist',  min: 0.0,  max: 1.0,  step: 0.05 },
  { key: 'bot.validation.windows',            type: 'int',   labelKey: 'k_windows',      min: 2,    max: 16,   step: 1 },
  { key: 'bot.validation.min_trades_total',   type: 'int',   labelKey: 'k_min_trades',   min: 5,    max: 1000, step: 5 },
  { key: 'bot.validation.max_drawdown_pct',   type: 'float', labelKey: 'k_max_dd',       min: 1.0,  max: 100.0, step: 0.5 },
  { key: 'bot.validation.backfill_days',      type: 'int',   labelKey: 'k_backfill',     min: 3,    max: 90,   step: 1 },
  { key: 'bot.entry.regime_gate.enabled',     type: 'bool',  labelKey: 'k_regime_on' },
  { key: 'bot.entry.regime_gate.mode',        type: 'enum',  labelKey: 'k_regime_mode',  enum: ['trending', 'ranging', 'off'] },
  { key: 'bot.entry.ranging.pair_allowlist',  type: 'string', labelKey: 'k_allowlist' },
  { key: 'regime.ranging.min_confidence',     type: 'float', labelKey: 'k_ranging_minc', min: 0.0,  max: 1.0,  step: 0.05 },
  { key: 'regime.unknown.min_confidence',     type: 'float', labelKey: 'k_unknown_minc', min: 0.0,  max: 1.0,  step: 0.05 },
  { key: 'bot.max_open_positions',            type: 'int',   labelKey: 'k_max_pos',      min: 1,    max: 6,    step: 1 },
];

export const KNOB_GROUPS = ['bot', 'regime'] as const;

// ── Presets ──────────────────────────────────────────────────────────────────
// Each preset is a coherent move of the WHOLE stack — never a single knob — so
// the operator can't "open the gate" yet leave the bot flat behind a high
// confidence floor. Applied atomically via POST /params/bulk.

export interface PresetChange {
  key: string;
  value: string;
}
export interface ScalpingPreset {
  id: 'flat' | 'conservative' | 'aggressive';
  /** i18n sub-key under Scalping, e.g. 'preset_flat' */
  labelKey: string;
  tone: 'safe' | 'neutral' | 'hot';
  changes: PresetChange[];
}

export const PRESETS: ScalpingPreset[] = [
  {
    id: 'flat',
    labelKey: 'preset_flat',
    tone: 'safe',
    // Guarantees the bot opens NOTHING: the validation gate (0 certified pairs)
    // plus a 0.90 confidence floor block every new entry. Exits still run.
    changes: [
      { key: 'bot.validation_gate.enabled', value: 'true' },
      { key: 'bot.confidence_threshold',    value: '0.90' },
    ],
  },
  {
    id: 'conservative',
    labelKey: 'preset_conservative',
    tone: 'neutral',
    // The historical safe defaults: selective, trend-following, strict edge.
    changes: [
      { key: 'bot.validation_gate.enabled',      value: 'false' },
      { key: 'bot.confidence_threshold',         value: '0.72' },
      { key: 'bot.entry.regime_gate.enabled',    value: 'true' },
      { key: 'bot.entry.regime_gate.mode',       value: 'trending' },
      { key: 'bot.validation.min_profit_factor', value: '1.30' },
    ],
  },
  {
    id: 'aggressive',
    labelKey: 'preset_aggressive',
    tone: 'hot',
    // Opens the full stack for 5/15/30m scalping: drops the confidence wall
    // (the real blocker), removes regime filtering, and loosens certification
    // to let thin edges through for live measurement. Real money — use within
    // the configured risk_limits.
    changes: [
      { key: 'bot.validation_gate.enabled',      value: 'false' },
      { key: 'bot.confidence_threshold',         value: '0.60' },
      { key: 'bot.entry.regime_gate.enabled',    value: 'false' },
      { key: 'bot.entry.regime_gate.mode',       value: 'off' },
      { key: 'bot.validation.min_profit_factor', value: '1.08' },
      { key: 'bot.validation.backfill_days',     value: '12' },
    ],
  },
];

// ── API shapes ───────────────────────────────────────────────────────────────

export interface ParamItem {
  key: string;
  type: string;
  group: string;
  description: string;
  default: string;
  current_effective_value: string;
  has_override: boolean;
}

export interface HistoryRow {
  id: number;
  key: string;
  scope_type: string;
  scope_id: string;
  old_value: string | null;
  new_value: string | null;
  changed_at: string;
  changed_by: string;
  change_reason: string;
}

export interface BulkResult {
  applied: number;
  changes: { key: string; old: string; new: string; history_id: number }[];
  rollback_token: string;
}

// Fetch current effective values for the gate knobs (groups bot + regime).
export async function fetchGateParams(): Promise<Record<string, ParamItem>> {
  const out: Record<string, ParamItem> = {};
  for (const group of KNOB_GROUPS) {
    const d = await api.get<{ params: ParamItem[] }>(
      `/api/v1/admin/config/params?group=${encodeURIComponent(group)}`,
    );
    for (const p of d?.params ?? []) out[p.key] = p;
  }
  return out;
}

// Apply a single knob globally. Backend validates against the registry.
export async function setKnob(key: string, value: string, reason: string): Promise<void> {
  await api.put(`/api/v1/admin/config/params/${encodeURIComponent(key)}`, {
    value,
    scope_type: 'global',
    scope_id: '*',
    reason,
  });
}

// Apply a whole preset atomically.
export async function applyPreset(p: ScalpingPreset, reason: string): Promise<BulkResult> {
  return api.post<BulkResult>('/api/v1/admin/config/params/bulk', {
    reason,
    changes: p.changes.map((c) => ({
      key: c.key,
      value: c.value,
      scope_type: 'global',
      scope_id: '*',
    })),
  });
}

export async function fetchHistory(limit = 30): Promise<HistoryRow[]> {
  const d = await api.get<{ history: HistoryRow[] }>(
    `/api/v1/admin/config/history?limit=${limit}`,
  );
  return d?.history ?? [];
}

export async function rollback(historyId: number): Promise<void> {
  await api.post(`/api/v1/admin/config/rollback/${historyId}`, {});
}

// ── Derived gate state ───────────────────────────────────────────────────────
// Pure, UI-side reasoning over the live values: which gate would block a new
// entry right now. This mirrors the bot's evaluation order so the operator sees
// WHY the bot is flat, not just that it is.

export interface GateBlocker {
  key: string;
  reason: string; // i18n sub-key under Scalping.block_*
}

export function deriveBlockers(p: Record<string, ParamItem>): GateBlocker[] {
  const blockers: GateBlocker[] = [];
  const val = (k: string) => p[k]?.current_effective_value;

  if (val('bot.validation_gate.enabled') === 'true') {
    blockers.push({ key: 'bot.validation_gate.enabled', reason: 'block_valgate' });
  }
  const conf = parseFloat(val('bot.confidence_threshold') ?? '0');
  if (conf >= 0.85) {
    blockers.push({ key: 'bot.confidence_threshold', reason: 'block_confidence' });
  }
  if (
    val('bot.entry.regime_gate.enabled') === 'true' &&
    val('bot.entry.regime_gate.mode') === 'trending'
  ) {
    blockers.push({ key: 'bot.entry.regime_gate.mode', reason: 'block_regime' });
  }
  return blockers;
}
