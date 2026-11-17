// sizing.ts — Control surface for capital-relative sizing (OpenSpec change
// capital-relative-sizing).
//
// Every value is read live from the dynamic-config API and written back with an
// atomic POST /params/bulk. The bounds below only drive the inputs; they mirror
// internal/config/dynamic/manifest/{sizing,manifest}.go and the backend
// re-validates every write against the registry, so a stale bound here can never
// push an out-of-range value through.
//
// previewSizing() mirrors the Go resolver (internal/services/ai_trading_bot_sizing.go)
// so the operator sees the USD effect of a pending edit BEFORE saving it. The
// authoritative numbers are the ones the bot reports in /ai-bot/status.

import { api } from '@/lib/api';
import type { ParamItem, BulkResult } from '@/lib/scalping';

export type FieldType = 'float' | 'int' | 'bool' | 'enum';

export interface SizingField {
  key: string;
  type: FieldType;
  /** i18n sub-key under the Sizing namespace */
  labelKey: string;
  /** i18n sub-key with a one-line explanation */
  hintKey: string;
  /** unit shown next to the input */
  unit?: '%' | 'USD' | '×' | 'R';
  min?: number;
  max?: number;
  step?: number;
  enum?: string[];
}

export interface SizingGroup {
  id: 'size' | 'caps' | 'validation' | 'sentinel';
  titleKey: string;
  subKey: string;
  fields: SizingField[];
}

export const SIZING_GROUPS: SizingGroup[] = [
  {
    id: 'size',
    titleKey: 'g_size',
    subKey: 'g_size_sub',
    fields: [
      { key: 'bot.sizing.mode', type: 'enum', labelKey: 'f_mode', hintKey: 'h_mode', enum: ['capital_pct', 'absolute'] },
      { key: 'bot.sizing.base_pct', type: 'float', labelKey: 'f_base_pct', hintKey: 'h_base_pct', unit: '%', min: 0.1, max: 100, step: 0.5 },
      { key: 'bot.sizing.min_notional_usd', type: 'float', labelKey: 'f_min_notional', hintKey: 'h_min_notional', unit: 'USD', min: 0.5, max: 1000, step: 0.5 },
      { key: 'bot.sizing.max_notional_usd', type: 'float', labelKey: 'f_max_notional', hintKey: 'h_max_notional', unit: 'USD', min: 1, max: 100000, step: 1 },
    ],
  },
  {
    id: 'caps',
    titleKey: 'g_caps',
    subKey: 'g_caps_sub',
    fields: [
      { key: 'bot.max_daily_loss_percent', type: 'float', labelKey: 'f_daily_pct', hintKey: 'h_daily_pct', unit: '%', min: 0.5, max: 50, step: 0.5 },
      { key: 'bot.max_daily_loss_usd', type: 'float', labelKey: 'f_daily_usd', hintKey: 'h_daily_usd', unit: 'USD', min: 0, max: 100000, step: 0.5 },
      { key: 'bot.pair_pause.net_loss_pct', type: 'float', labelKey: 'f_pair_pause', hintKey: 'h_pair_pause', unit: '%', min: 0.01, max: 20, step: 0.05 },
      { key: 'bot.drawdown.use_account_capital', type: 'bool', labelKey: 'f_dd_capital', hintKey: 'h_dd_capital' },
      { key: 'bot.drawdown.size_reduce_threshold_pct', type: 'float', labelKey: 'f_dd_reduce', hintKey: 'h_dd_reduce', unit: '%', min: 5, max: 50, step: 1 },
      { key: 'bot.drawdown.size_reduce_factor', type: 'float', labelKey: 'f_dd_factor', hintKey: 'h_dd_factor', unit: '×', min: 0.1, max: 1, step: 0.05 },
    ],
  },
  {
    id: 'validation',
    titleKey: 'g_validation',
    subKey: 'g_validation_sub',
    fields: [
      { key: 'validation.max_risk_per_trade_pct', type: 'float', labelKey: 'f_v_risk', hintKey: 'h_v_risk', unit: '%', min: 0.1, max: 10, step: 0.1 },
      { key: 'validation.max_daily_loss_pct', type: 'float', labelKey: 'f_v_daily', hintKey: 'h_v_daily', unit: '%', min: 0.5, max: 50, step: 0.5 },
      { key: 'validation.max_drawdown_pct', type: 'float', labelKey: 'f_v_dd', hintKey: 'h_v_dd', unit: '%', min: 1, max: 80, step: 1 },
      { key: 'validation.max_stop_loss_pct', type: 'float', labelKey: 'f_v_sl', hintKey: 'h_v_sl', unit: '%', min: 0.5, max: 50, step: 0.5 },
      { key: 'validation.min_risk_reward', type: 'float', labelKey: 'f_v_rr', hintKey: 'h_v_rr', unit: 'R', min: 1, max: 10, step: 0.1 },
      { key: 'validation.min_confluence', type: 'int', labelKey: 'f_v_conf', hintKey: 'h_v_conf', min: 0, max: 10, step: 1 },
    ],
  },
  {
    id: 'sentinel',
    titleKey: 'g_sentinel',
    subKey: 'g_sentinel_sub',
    fields: [
      { key: 'risk.max_order_size_usd', type: 'float', labelKey: 'f_s_usd', hintKey: 'h_s_usd', unit: 'USD', min: 0, max: 100000, step: 0.5 },
      { key: 'risk.max_order_size_pct', type: 'float', labelKey: 'f_s_pct', hintKey: 'h_s_pct', unit: '%', min: 0, max: 100, step: 1 },
      { key: 'risk.max_notional_factor', type: 'float', labelKey: 'f_s_factor', hintKey: 'h_s_factor', unit: '×', min: 1, max: 100, step: 0.5 },
      { key: 'risk.enforce_cap_on_market_orders', type: 'bool', labelKey: 'f_s_market', hintKey: 'h_s_market' },
    ],
  },
];

export const TIERS_KEY = 'dynamic_sizing.tiers';
const PARAM_GROUPS = ['bot', 'validation', 'risk', 'dynamic_sizing'] as const;

export interface SizingTier {
  name: string;
  min_confidence: number;
  max_confidence: number;
  size_pct?: number;
  size_usd?: number;
  take_profit_pct?: number;
  stop_loss_pct?: number;
}

// ── Live data ────────────────────────────────────────────────────────────────

export async function fetchSizingParams(): Promise<Record<string, ParamItem>> {
  const out: Record<string, ParamItem> = {};
  for (const group of PARAM_GROUPS) {
    const d = await api.get<{ params: ParamItem[] }>(
      `/api/v1/admin/config/params?group=${encodeURIComponent(group)}`,
    );
    for (const p of d?.params ?? []) out[p.key] = p;
  }
  return out;
}

/** Effective sizing as resolved by the running bot (GET /ai-bot/status → metadata.sizing). */
export interface BotSizing {
  mode: string;
  fallback: boolean;
  capital_usd: number;
  capital_at: string;
  day_start_capital_usd: number;
  base_notional_usd: number;
  min_notional_usd: number;
  max_notional_usd: number;
  daily_loss_limit_usd: number;
  daily_loss_usd: number;
  pair_pause_threshold_usd: number;
  drawdown_size_factor: number;
}

export interface BotStatusLite {
  running: boolean;
  sizing: BotSizing | null;
  tradeSizeUSD: number;
  maxDailyLossUSD: number;
}

export async function fetchBotSizing(): Promise<BotStatusLite> {
  const d = await api.get<{
    is_running?: boolean;
    metadata?: { sizing?: BotSizing; config?: { trade_size_usd?: number; max_daily_loss_usd?: number } };
  }>('/api/v1/ai-bot/status');
  return {
    running: !!d?.is_running,
    sizing: d?.metadata?.sizing ?? null,
    tradeSizeUSD: d?.metadata?.config?.trade_size_usd ?? 0,
    maxDailyLossUSD: d?.metadata?.config?.max_daily_loss_usd ?? 0,
  };
}

/** Apply every pending change atomically (one history entry per key, one rollback token). */
export async function saveSizing(changes: Record<string, string>, reason: string): Promise<BulkResult> {
  return api.post<BulkResult>('/api/v1/admin/config/params/bulk', {
    reason,
    changes: Object.entries(changes).map(([key, value]) => ({
      key,
      value,
      scope_type: 'global',
      scope_id: '*',
    })),
  });
}

export function parseTiers(raw: string | undefined): SizingTier[] {
  if (!raw) return [];
  try {
    const v = JSON.parse(raw);
    return Array.isArray(v) ? (v as SizingTier[]) : [];
  } catch {
    return [];
  }
}

// ── Preview (mirror of the Go resolver) ─────────────────────────────────────

export interface SizingPreview {
  mode: 'capital_pct' | 'absolute';
  capital: number;
  dayStart: number;
  base: number;
  tiers: { name: string; range: string; pct: number; usd: number }[];
  botMax: number;
  dailyCap: number;
  pairPause: number;
  sentinelCap: number; // 0 = no cap
  /** risk of one base trade with the default 1.5 % stop, as % of capital */
  baseRiskPct: number;
  incoherent: boolean;
  floorApplied: boolean;
  ceilingApplied: boolean;
}

const num = (v: string | undefined, fallback = 0) => {
  const n = parseFloat(v ?? '');
  return Number.isFinite(n) ? n : fallback;
};

export function previewSizing(
  values: (key: string) => string,
  tiers: SizingTier[],
  bot: BotStatusLite | undefined,
): SizingPreview | null {
  const s = bot?.sizing;
  const capital = s?.capital_usd ?? 0;
  if (!capital) return null;
  const dayStart = s?.day_start_capital_usd || capital;
  const mode = values('bot.sizing.mode') === 'absolute' ? 'absolute' : 'capital_pct';

  let min = num(values('bot.sizing.min_notional_usd'), 2);
  if (min <= 0) min = 2;
  let max = num(values('bot.sizing.max_notional_usd'), 25);
  if (max <= 0) max = 25;
  if (max < min) max = min;
  const clamp = (x: number) => Math.min(Math.max(x, min), max);
  const factor = s?.drawdown_size_factor && s.drawdown_size_factor > 0 ? s.drawdown_size_factor : 1;

  let basePct = num(values('bot.sizing.base_pct'), 10);
  if (basePct <= 0) basePct = 10;
  const rawBase = capital * basePct / 100 * factor;

  let base: number;
  let tierRows: SizingPreview['tiers'];
  if (mode === 'absolute') {
    base = (bot?.tradeSizeUSD ?? 0) * factor;
    tierRows = tiers.map((t) => ({
      name: t.name,
      range: `${Math.round(t.min_confidence * 100)}–${Math.round(t.max_confidence * 100)} %`,
      pct: t.size_usd ? (t.size_usd / capital) * 100 : 0,
      usd: (t.size_usd ?? base) * factor,
    }));
  } else {
    base = clamp(rawBase);
    tierRows = tiers.map((t) => {
      const pct = t.size_pct && t.size_pct > 0 ? t.size_pct : t.size_usd ? (t.size_usd / capital) * 100 : basePct;
      return {
        name: t.name,
        range: `${Math.round(t.min_confidence * 100)}–${Math.round(t.max_confidence * 100)} %`,
        pct,
        usd: clamp(capital * pct / 100 * factor),
      };
    });
  }
  const botMax = Math.max(base, ...tierRows.map((r) => r.usd));

  const dailyUSD = num(values('bot.max_daily_loss_usd'));
  let dailyCap = mode === 'absolute'
    ? bot?.maxDailyLossUSD ?? 0
    : dayStart * num(values('bot.max_daily_loss_percent'), 3) / 100;
  if (dailyUSD > 0 && (dailyCap <= 0 || dailyUSD < dailyCap)) dailyCap = dailyUSD;

  const pairPause = mode === 'absolute'
    ? 0.15
    : dayStart * num(values('bot.pair_pause.net_loss_pct'), 0.5) / 100;

  let sentinelCap = num(values('risk.max_order_size_usd'));
  const sPct = num(values('risk.max_order_size_pct'));
  if (sPct > 0) {
    const rel = capital * sPct / 100;
    if (sentinelCap <= 0 || rel < sentinelCap) sentinelCap = rel;
  }

  return {
    mode,
    capital,
    dayStart,
    base,
    tiers: tierRows,
    botMax,
    dailyCap,
    pairPause,
    sentinelCap,
    baseRiskPct: (base * 0.015 / capital) * 100,
    incoherent: sentinelCap > 0 && sentinelCap < botMax,
    floorApplied: mode === 'capital_pct' && rawBase < min,
    ceilingApplied: mode === 'capital_pct' && rawBase > max,
  };
}
