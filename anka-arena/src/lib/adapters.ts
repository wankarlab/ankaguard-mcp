// adapters.ts — translate backend response envelopes/shapes into FE types.
//
// The trading-mcp REST handlers wrap every payload in { success, data, timestamp }
// and use snake_case field names. The FE stores/components use flat camelCase
// types. These adapters are the single place that bridges the two contracts.

import type { EngineStats } from '@/stores/engine';

/** Backend envelope used by AnkaAI handlers. */
export interface Envelope<T> {
  success?: boolean;
  data?: T;
  error?: { code?: string; message?: string };
  timestamp?: string;
}

/**
 * Unwrap the { success, data } envelope. Returns `data` when present, otherwise
 * the value itself (some endpoints return bare objects/arrays).
 */
export function unwrap<T = unknown>(res: unknown): T {
  if (res && typeof res === 'object' && 'data' in (res as Record<string, unknown>)) {
    return (res as Envelope<T>).data as T;
  }
  return res as T;
}

// ── Engine stats (GET /api/v1/engine/stats) ─────────────────────────────────────

interface BackendEngineStats {
  feature_store_rows?: number;
  edges_live?: number;
  edges_total?: number;
  gate_pass_rate?: number; // already 0–100
  gbm_pf_oos?: number;
  aum_cert?: number;
  specs_certified_today?: number;
  specs_in_queue?: number;
}

/**
 * Map the engine/stats envelope to a Partial<EngineStats> the store understands.
 * Fields not provided by this endpoint (tier, capital, pnl, gbmAuc, regime, …)
 * are left untouched so WS pushes / other endpoints can fill them.
 */
export function toEngineStats(res: unknown): Partial<EngineStats> {
  const d = unwrap<BackendEngineStats>(res) ?? {};
  return {
    edgesTotal:     d.edges_total ?? 0,
    edgesCertified: d.edges_live ?? 0,
    gatePassPct:    d.gate_pass_rate ?? 0,
    gbmPf:          d.gbm_pf_oos ?? 0,
    aumCertUsd:     d.aum_cert ?? 0,
    featureStoreRows:    d.feature_store_rows ?? 0,
    specsInQueue:        d.specs_in_queue ?? 0,
    specsCertifiedToday: d.specs_certified_today ?? 0,
  };
}

// ── Exchanges (GET /api/v1/exchanges) ───────────────────────────────────────────

export interface ExchangeInfo {
  name:      string;
  enabled:   boolean;
  sandbox:   boolean;
  connected: boolean;
}

interface BackendExchange {
  name?: string;
  status?: string;          // "active" | "inactive" | ...
  enabled?: boolean;
  sandbox?: boolean;
  connected?: boolean;
}

/** Map the exchanges envelope ({data:{exchanges:[{name,status}]}}) to ExchangeInfo[]. */
export function toExchanges(res: unknown): ExchangeInfo[] {
  const d = unwrap<{ exchanges?: BackendExchange[] }>(res);
  const list = d?.exchanges ?? [];
  return list.map((e) => {
    const active = e.status ? e.status === 'active' : e.connected ?? false;
    return {
      name:      e.name ?? '—',
      enabled:   e.enabled ?? active,
      sandbox:   e.sandbox ?? false,
      connected: e.connected ?? active,
    };
  });
}

// ── Marketplace strategies (GET /api/v1/marketplace/strategies) ─────────────────

export interface GuildStrategy {
  id: string;
  author: string;
  kind: 'human' | 'ai';
  regime: 'TRENDING' | 'RANGING' | 'BREAKOUT' | 'UNKNOWN';
  pf_oos: number;
  consistency_pct: number;
  max_drawdown: number;      // C1.1 anti-survivorship: worst DD from validation
  days_active: number;
  copiers: number;
  retired: boolean;
  retired_at?: string;       // C1.1: when a withdrawn strategy was demoted
  status: 'certified' | 'retired' | 'shadow' | string;
  copiable: boolean;
}

interface BackendStrategy {
  spec_id?: string;
  author?: string;
  regime?: string;
  pf_oos?: number;
  consistency?: number;      // 0–1
  max_drawdown?: number;
  days_active?: number;
  copiers?: number;
  status?: string;           // certified | retired | shadow | ...
  retired?: boolean;
  retired_at?: string;
}

const VALID_REGIMES = ['TRENDING', 'RANGING', 'BREAKOUT'] as const;

/** Map the strategies envelope to GuildStrategy[] (consistency 0–1 → percent). */
export function toGuildStrategies(res: unknown): GuildStrategy[] {
  const d = unwrap<{ strategies?: BackendStrategy[] }>(res);
  const list = d?.strategies ?? [];
  return list.map((s) => {
    const regime = (s.regime ?? '').toUpperCase();
    return {
      id:              s.spec_id ?? '',
      author:          s.author ?? 'desconocido',
      kind:            (s.author ?? '').toLowerCase().includes('ai') ? 'ai' : 'human',
      regime:          (VALID_REGIMES as readonly string[]).includes(regime)
                         ? (regime as GuildStrategy['regime'])
                         : 'UNKNOWN',
      pf_oos:          s.pf_oos ?? 0,
      consistency_pct: Math.round((s.consistency ?? 0) * 100),
      max_drawdown:    s.max_drawdown ?? 0,
      days_active:     s.days_active ?? 0,
      copiers:         s.copiers ?? 0,
      retired:         s.retired ?? s.status === 'retired',
      retired_at:      s.retired_at,
      status:          (s.status as GuildStrategy['status']) ?? 'shadow',
      copiable:        s.status === 'certified',
    };
  });
}

// ── Billing fee ledger (GET /api/v1/billing/fees/:user_id) ──────────────────────
// C0.4: ALL economy values (fee rate, splits, per-row amounts, aggregate totals)
// come from the backend / dynamic config. The FE must NOT hardcode 20% / 70-30.

/** Economy parameters resolved from the dynamic config table (billing.*). */
export interface FeeEconomy {
  feeRate:       number; // e.g. 0.20
  authorSplit:   number; // e.g. 0.70
  treasurySplit: number; // e.g. 0.30
}

/** A single high-water-mark fee period (backend-computed amounts). */
export interface FeePeriod {
  id:               string;
  userId:           string;
  periodStart:      string;
  periodEnd:        string;
  highWaterMark:    number;
  periodNetPnlSum:  number;
  feeUsd:           number;
  rate:             number;
  status:           string;
  authorAmount:     number;
  treasuryAmount:   number;
  economy?:         FeeEconomy; // present on the single-period endpoint
}

/** Backend-computed aggregate totals across all periods. */
export interface FeeAggregate {
  totalFeeUsd:         number;
  totalAuthorAmount:   number;
  totalTreasuryAmount: number;
}

export interface FeeLedger {
  userId:    string;
  periods:   FeePeriod[];
  economy:   FeeEconomy;
  aggregate: FeeAggregate;
  note?:     string;
}

interface BackendFeeEconomy {
  fee_rate?:      number;
  author_split?:  number;
  treasury_split?: number;
}

interface BackendFeePeriod {
  id?:                 string;
  user_id?:            string;
  period_start?:       string;
  period_end?:         string;
  high_water_mark?:    number;
  period_net_pnl_sum?: number;
  fee_usd?:            number;
  rate?:               number;
  status?:             string;
  author_amount?:      number;
  treasury_amount?:    number;
  economy?:            BackendFeeEconomy;
}

function toFeeEconomy(d?: BackendFeeEconomy): FeeEconomy {
  return {
    feeRate:       d?.fee_rate ?? 0,
    authorSplit:   d?.author_split ?? 0,
    treasurySplit: d?.treasury_split ?? 0,
  };
}

function toFeePeriod(d: BackendFeePeriod): FeePeriod {
  return {
    id:              d.id ?? '',
    userId:          d.user_id ?? '',
    periodStart:     d.period_start ?? '',
    periodEnd:       d.period_end ?? '',
    highWaterMark:   d.high_water_mark ?? 0,
    periodNetPnlSum: d.period_net_pnl_sum ?? 0,
    feeUsd:          d.fee_usd ?? 0,
    rate:            d.rate ?? 0,
    status:          d.status ?? '',
    authorAmount:    d.author_amount ?? 0,
    treasuryAmount:  d.treasury_amount ?? 0,
    economy:         d.economy ? toFeeEconomy(d.economy) : undefined,
  };
}

interface BackendFeeLedger {
  user_id?: string;
  periods?: BackendFeePeriod[];
  economy?: BackendFeeEconomy;
  aggregate?: {
    total_fee_usd?:        number;
    total_author_amount?:  number;
    total_treasury_amount?: number;
  };
  note?: string;
}

/** Map the fee-ledger envelope to FeeLedger (all amounts are backend-computed). */
export function toFeeLedger(res: unknown): FeeLedger {
  const d = unwrap<BackendFeeLedger>(res) ?? {};
  return {
    userId:  d.user_id ?? '',
    periods: (d.periods ?? []).map(toFeePeriod),
    economy: toFeeEconomy(d.economy),
    aggregate: {
      totalFeeUsd:         d.aggregate?.total_fee_usd ?? 0,
      totalAuthorAmount:   d.aggregate?.total_author_amount ?? 0,
      totalTreasuryAmount: d.aggregate?.total_treasury_amount ?? 0,
    },
    note: d.note,
  };
}

/** Map a single fee-period envelope (economy embedded inline). */
export function toFeePeriodDetail(res: unknown): FeePeriod {
  const d = unwrap<BackendFeePeriod>(res) ?? {};
  return toFeePeriod(d);
}

// ── Spec templates (GET /api/v1/specs/templates) ────────────────────────────────
// C0.5: Spec Studio opens from a seeded real StrategySpec, never DEFAULT_SPEC.

/** A spec template wrapping a full StrategySpec doc. `spec` is opaque JSON. */
export interface SpecTemplate {
  templateId:  string;
  name:        string;
  description: string;
  regime:      string;
  kind:        string;
  spec:        Record<string, unknown>;
}

interface BackendSpecTemplate {
  template_id?: string;
  name?:        string;
  description?: string;
  regime?:      string;
  kind?:        string;
  spec?:        Record<string, unknown>;
}

/** Map the templates envelope to SpecTemplate[]. */
export function toSpecTemplates(res: unknown): SpecTemplate[] {
  const d = unwrap<{ templates?: BackendSpecTemplate[] }>(res);
  return (d?.templates ?? []).map((t) => ({
    templateId:  t.template_id ?? '',
    name:        t.name ?? '',
    description: t.description ?? '',
    regime:      t.regime ?? '',
    kind:        t.kind ?? '',
    spec:        t.spec ?? {},
  }));
}

// ── Gate specs (GET /api/v1/specs and /api/v1/specs?mine=true) ───────────────────
// C1.1 / C1.5: now includes author_id, user_id, max_drawdown, raw status, retired.

export interface GateSpec {
  id:          string;
  name:        string;
  author:      string;
  authorId:    string;
  userId:      string;
  kind:        string;
  regime:      string;
  pf:          number;
  cons:        number;
  robust:      number;
  maxDrawdown: number;
  status:      string;  // raw lifecycle: draft|validating|validated|rejected|live|demoted
  retired:     boolean;
  windows:     number;
  verdict:     string;
}

interface BackendGateSpec {
  id?:           string;
  name?:         string;
  author?:       string;
  author_id?:    string;
  user_id?:      string;
  kind?:         string;
  regime?:       string;
  pf?:           number;
  cons?:         number;
  robust?:       number;
  max_drawdown?: number;
  status?:       string;
  retired?:      boolean;
  windows?:      number;
  verdict?:      string;
}

function toGateSpec(d: BackendGateSpec): GateSpec {
  return {
    id:          d.id ?? '',
    name:        d.name ?? '',
    author:      d.author ?? '',
    authorId:    d.author_id ?? '',
    userId:      d.user_id ?? '',
    kind:        d.kind ?? '',
    regime:      d.regime ?? '',
    pf:          d.pf ?? 0,
    cons:        d.cons ?? 0,
    robust:      d.robust ?? 0,
    maxDrawdown: d.max_drawdown ?? 0,
    status:      d.status ?? '',
    retired:     d.retired ?? false,
    windows:     d.windows ?? 0,
    verdict:     d.verdict ?? '',
  };
}

/** Map the specs envelope to GateSpec[]. */
export function toGateSpecs(res: unknown): GateSpec[] {
  const d = unwrap<{ specs?: BackendGateSpec[] }>(res);
  return (d?.specs ?? []).map(toGateSpec);
}

// ── Permissions (GET /api/v1/me/permissions) ────────────────────────────────────
// C1.4: truth for locked screens + required levels. lib/rbac.ts is UX hint only.

export interface ScreenLock {
  screen:        string;
  locked:        boolean;
  requiredLevel: number;
  howToUnlock:   string;
}

export interface PermissionRequiredLevels {
  politicas:    number;
  aprobaciones: number;
  tribunal:     number;
  pool:         number;
}

export interface Permissions {
  userLevel:      number;
  requiredLevels: PermissionRequiredLevels;
  kycStatus:      string; // "unknown" | "verified" | ...
  locks:          ScreenLock[];
}

interface BackendPermissions {
  user_level?: number;
  required_levels?: {
    politicas?:    number;
    aprobaciones?: number;
    tribunal?:     number;
    pool?:         number;
  };
  kyc_status?: string;
  locks?: {
    screen?:         string;
    locked?:         boolean;
    required_level?: number;
    how_to_unlock?:  string;
  }[];
}

/** Map the permissions envelope to Permissions. */
export function toPermissions(res: unknown): Permissions {
  const d = unwrap<BackendPermissions>(res) ?? {};
  return {
    userLevel: d.user_level ?? 0,
    requiredLevels: {
      politicas:    d.required_levels?.politicas ?? 0,
      aprobaciones: d.required_levels?.aprobaciones ?? 0,
      tribunal:     d.required_levels?.tribunal ?? 0,
      pool:         d.required_levels?.pool ?? 0,
    },
    kycStatus: d.kyc_status ?? 'unknown',
    locks: (d.locks ?? []).map((l) => ({
      screen:        l.screen ?? '',
      locked:        l.locked ?? false,
      requiredLevel: l.required_level ?? 0,
      howToUnlock:   l.how_to_unlock ?? '',
    })),
  };
}

// ── Trade history (GET /api/v1/trades/history) ──────────────────────────────────
// C1.1: VERIFIED includes losers (no win/loss filter). FE adds the disclaimer.

export interface TradeHistoryRow {
  tradeId:  string;
  pair:     string;
  side:     string;
  pnl:      number;
  pnlPct:   number;
  regime:   string;
  volTier:  string;
  note:     string;
  openedAt: string;
  closedAt: string;
}

interface BackendTradeHistoryRow {
  trade_id?:  string;
  pair?:      string;
  side?:      string;
  pnl?:       number;
  pnl_pct?:   number;
  regime?:    string;
  vol_tier?:  string;
  note?:      string;
  opened_at?: string;
  closed_at?: string;
}

/** Map the trade-history envelope to TradeHistoryRow[] (losers included). */
export function toTradeHistory(res: unknown): TradeHistoryRow[] {
  const d = unwrap<{ trades?: BackendTradeHistoryRow[] } | BackendTradeHistoryRow[]>(res);
  const list = Array.isArray(d) ? d : (d?.trades ?? []);
  return list.map((r) => ({
    tradeId:  r.trade_id ?? '',
    pair:     r.pair ?? '',
    side:     r.side ?? '',
    pnl:      r.pnl ?? 0,
    pnlPct:   r.pnl_pct ?? 0,
    regime:   r.regime ?? '',
    volTier:  r.vol_tier ?? '',
    note:     r.note ?? '',
    openedAt: r.opened_at ?? '',
    closedAt: r.closed_at ?? '',
  }));
}

// ── Consent (C1.3) ──────────────────────────────────────────────────────────────

export interface ConsentTemplate {
  kind:    string;
  version: number;
  bodyMd:  string;
}

export interface ConsentStatus {
  kind:            string;
  accepted:        boolean;
  acceptedVersion: number;
}

interface BackendConsentTemplate {
  kind?:    string;
  version?: number;
  body_md?: string;
}

interface BackendConsentStatus {
  kind?:             string;
  accepted?:         boolean;
  accepted_version?: number;
}

/** Map the consent-template envelope to ConsentTemplate. */
export function toConsentTemplate(res: unknown): ConsentTemplate {
  const d = unwrap<BackendConsentTemplate>(res) ?? {};
  return {
    kind:    d.kind ?? '',
    version: d.version ?? 0,
    bodyMd:  d.body_md ?? '',
  };
}

/** Map the consent-status envelope to ConsentStatus. */
export function toConsentStatus(res: unknown): ConsentStatus {
  const d = unwrap<BackendConsentStatus>(res) ?? {};
  return {
    kind:            d.kind ?? '',
    accepted:        d.accepted ?? false,
    acceptedVersion: d.accepted_version ?? 0,
  };
}

// ── Adversarial queue (C3.2) ─────────────────────────────────────────────────────

export interface AdversarialItem {
  id:        string;
  specId:    string;
  status:    string;
  createdAt: string;
}

interface BackendAdversarialItem {
  id?:         string;
  spec_id?:    string;
  status?:     string;
  created_at?: string;
}

/** Map the adversarial-queue envelope to AdversarialItem[]. */
export function toAdversarialQueue(res: unknown): AdversarialItem[] {
  const d = unwrap<{ items?: BackendAdversarialItem[] }>(res);
  return (d?.items ?? []).map((i) => ({
    id:        i.id ?? '',
    specId:    i.spec_id ?? '',
    status:    i.status ?? '',
    createdAt: i.created_at ?? '',
  }));
}

// ── Appeals (C3.3) ───────────────────────────────────────────────────────────────

export interface Appeal {
  id:           string;
  specId:       string;
  appellantId:  string;
  status:       string;
  reason:       string;
  createdAt:    string;
}

interface BackendAppeal {
  id?:           string;
  spec_id?:      string;
  appellant_id?: string;
  status?:       string;
  reason?:       string;
  created_at?:   string;
}

/** Map the appeals envelope to Appeal[]. */
export function toAppeals(res: unknown): Appeal[] {
  const d = unwrap<{ appeals?: BackendAppeal[] }>(res);
  return (d?.appeals ?? []).map((a) => ({
    id:          a.id ?? '',
    specId:      a.spec_id ?? '',
    appellantId: a.appellant_id ?? '',
    status:      a.status ?? '',
    reason:      a.reason ?? '',
    createdAt:   a.created_at ?? '',
  }));
}
