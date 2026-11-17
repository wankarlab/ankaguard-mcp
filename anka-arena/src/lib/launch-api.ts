// launch-api.ts — typed client for AnkaLaunch phase 1 (`/api/v1/launch`).
// Contract: docs/bot/launch-api-phase1.md. apiFetch already unwraps the
// `{ success, data }` envelope, so fetchers return the bare payload.

import { apiFetch, ApiError } from '@/lib/api';

// ── Types ────────────────────────────────────────────────────────────────────

export type LaunchService = 'up' | 'down' | 'never_started';
export type GateVerdict = 'INSUFFICIENT_DATA' | 'NO_EDGE' | 'EDGE';
export type LaunchBand = 'IGNORE' | 'OBSERVE' | 'ALERT' | 'MANUAL_APPROVAL' | 'AUTO_CANDIDATE';
export type DecisionAction = 'approve' | 'reject';
export type DecisionCategory = 'narrative' | 'team' | 'community' | 'chart' | 'other';
export type EntryChannel = 'auto' | 'manual' | 'counterfactual';

export interface GateRequirements {
  min_trades: number;
  min_shadow_weeks: number;
  max_fraud_buys: number;
}

export interface GateEvaluation {
  id: number;
  strategy_version_id: number;
  mode: string;
  verdict: GateVerdict;
  trades: number;
  shadow_weeks: number;
  net_pnl_usd: number;
  mean_pnl_usd: number;
  ci_low: number;
  ci_high: number;
  ci_level: number;
  profit_factor: number | null;
  max_drawdown_usd: number;
  fraud_buys: number;
  requirements: GateRequirements;
  detail: string;
  evaluated_at: string;
}

export interface LaunchSource {
  name: string;
  role: string;
  connected: boolean;
  active: boolean;
  last_event_at: string | null;
  events_per_min: number;
  reconnects: number;
  rate_limited: number;
  quota_used_pct: number | null;
}

export interface LaunchStatus {
  service: LaunchService;
  last_heartbeat: string | null;
  instance: string | null;
  entries_paused: boolean;
  pause_reason: string | null;
  breaker: { state: 'running' | 'killed'; since?: string; actor?: string; reason?: string } | null;
  sources: LaunchSource[];
  network_state: Record<string, string>;
  gate: GateEvaluation | null;
  shadow: {
    open_positions: number;
    exposure_usd: number;
    closed_positions: number;
    net_pnl_usd: number;
    gross_pnl_usd: number;
  } | null;
  counts_24h: {
    events: number;
    opportunities: number;
    rejected: number;
    by_reason: Record<string, number>;
    by_band: Record<string, number>;
  } | null;
  real_mode: boolean;
}

export interface BreakdownFactor {
  factor: string;
  raw: number;
  normalized: number;
  weight: number;
  confidence: number;
  points: number;
}

export interface OpportunityDecision {
  action: DecisionAction | 'expire';
  category: string | null;
  comment: string | null;
  actor: string | null;
  created_at: string;
}

export interface Opportunity {
  id: number;
  mint: string;
  symbol: string | null;
  pool: string | null;
  dex: string | null;
  source_kind: 'amm' | 'bonding_curve';
  graduated: boolean;
  screen_result: 'PASSED' | 'REJECTED' | 'BLOCKED';
  reject_reason: string | null;
  score: number | null;
  band: LaunchBand | null;
  status: string;
  expires_at: string | null;
  created_at: string;
  breakdown: BreakdownFactor[];
  snapshot: Record<string, { value: number; source?: string; ts?: string; expires_at?: string; confidence?: number }>;
  thesis: { for: string[]; against: string[] } | null;
  decision: OpportunityDecision | null;
  position_id: number | null;
}

export interface PositionCosts {
  venue_fees_usd: number;
  network_fees_usd: number;
  rent_usd: number;
  slippage_usd: number;
  token_tax_usd: number;
  dropped_txs: number;
}

export interface LaunchPosition {
  id: number;
  opportunity_id: number;
  mint: string;
  symbol: string | null;
  mode: string;
  entry_channel: EntryChannel;
  state: 'OPEN' | 'CLOSED' | 'DROPPED';
  invested_usd: number;
  entry_price_usd: number;
  remaining_tokens: number;
  current_value_usd: number | null;
  gross_pnl_usd: number | null;
  net_pnl_usd: number | null;
  costs: PositionCosts;
  exit_reason: string | null;
  size_reason: string | null;
  fraud_flag: boolean;
  strategy_version_id: number;
  opened_at: string;
  closed_at: string | null;
}

export interface ChannelStats {
  trades: number;
  net_pnl_usd: number;
  win_rate: number;
  profit_factor: number | null;
}

export interface HumanLiftReportData {
  status: 'INSUFFICIENT_DATA' | 'OK';
  min_trades: number;
  channels: { manual: ChannelStats; counterfactual: ChannelStats; auto: ChannelStats };
  by_category: { category: string; manual: ChannelStats; counterfactual: ChannelStats }[];
  manual_minus_counterfactual_net_usd: number | null;
}

export interface Paged<T> {
  items: T[];
  next_before_id: number | null;
}

export interface ApprovalsResponse {
  items: Opportunity[];
  can_decide: boolean;
}

export interface DecisionResult {
  decision_id: number;
  opportunity_id: number;
  action: DecisionAction;
}

export interface StrategyVersion {
  id: number;
  version: number;
  params: Record<string, unknown>;
  author: string;
  reason: string;
  created_at: string;
}

export interface LaunchStrategy {
  id: number;
  name: string;
  status: string;
  versions: StrategyVersion[];
}

// ── Fetchers ─────────────────────────────────────────────────────────────────

const BASE = '/api/v1/launch';

function qs(params: Record<string, string | number | null | undefined>): string {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== '') p.set(k, String(v));
  }
  const s = p.toString();
  return s ? `?${s}` : '';
}

export const getLaunchStatus = () => apiFetch<LaunchStatus>(`${BASE}/status`);

export const listOpportunities = (opts: { band?: string; status?: string; limit?: number; beforeId?: number } = {}) =>
  apiFetch<Paged<Opportunity>>(
    `${BASE}/opportunities${qs({ band: opts.band, status: opts.status, limit: opts.limit ?? 50, before_id: opts.beforeId })}`,
  );

export const getOpportunity = (id: number) => apiFetch<Opportunity>(`${BASE}/opportunities/${id}`);

export const listApprovals = () => apiFetch<ApprovalsResponse>(`${BASE}/approvals`);

export const decideOpportunity = (id: number, body: { action: DecisionAction; category: DecisionCategory; comment?: string }) =>
  apiFetch<DecisionResult>(`${BASE}/opportunities/${id}/decision`, { method: 'POST', body: JSON.stringify(body) });

export const listPositions = (opts: { channel?: string; state?: string; limit?: number; beforeId?: number } = {}) =>
  apiFetch<Paged<LaunchPosition>>(
    `${BASE}/positions${qs({ channel: opts.channel, state: opts.state, limit: opts.limit ?? 50, before_id: opts.beforeId })}`,
  );

export const getPosition = (id: number) =>
  apiFetch<LaunchPosition & { orders: unknown[] }>(`${BASE}/positions/${id}`);

export const listStrategies = () => apiFetch<{ items: LaunchStrategy[] }>(`${BASE}/strategies`);

export const getGate = (limit = 20) =>
  apiFetch<{ latest: GateEvaluation | null; history: GateEvaluation[] }>(`${BASE}/gate${qs({ limit })}`);

export const getHumanLift = () => apiFetch<HumanLiftReportData>(`${BASE}/reports/human-lift`);

export const launchKillSwitch = (reason: string) =>
  apiFetch<{ command_id: number }>(`${BASE}/kill-switch`, { method: 'POST', body: JSON.stringify({ reason }) });

export const launchResume = (reason: string) =>
  apiFetch<{ command_id: number }>(`${BASE}/resume`, { method: 'POST', body: JSON.stringify({ reason }) });

// ── Errors ───────────────────────────────────────────────────────────────────

export interface LaunchErrorInfo {
  status: number | null;
  code: string | null;
  message: string | null;
}

/** Read `{ error: { code, message } }` from an ApiError body (xsec convention). */
export function launchErrorInfo(e: unknown): LaunchErrorInfo {
  if (e instanceof ApiError) {
    const body = e.body as { error?: { code?: string; message?: string } } | undefined;
    return { status: e.status, code: body?.error?.code ?? null, message: body?.error?.message ?? null };
  }
  return { status: null, code: null, message: null };
}

const DECISION_ERROR_KEYS: Record<string, string> = {
  OPPORTUNITY_EXPIRED: 'err_expired',
  ALREADY_DECIDED: 'err_already_decided',
  NOT_PENDING: 'err_not_pending',
  INVALID_CATEGORY: 'err_invalid_category',
  INVALID_ACTION: 'err_invalid_action',
  FEATURE_ACCESS_DENIED: 'err_forbidden',
  LAUNCH_UNAVAILABLE: 'err_unavailable',
};

/** i18n key (namespace Launch) for an API error; falls back to a generic one. */
export function launchErrorKey(info: LaunchErrorInfo): string {
  if (info.code && DECISION_ERROR_KEYS[info.code]) return DECISION_ERROR_KEYS[info.code];
  if (info.status === 403) return 'err_forbidden';
  if (info.status === 503) return 'err_unavailable';
  return 'err_generic';
}

/** Errors after which the approval queue is stale and must be refetched. */
export function shouldRefreshQueue(info: LaunchErrorInfo): boolean {
  return info.code === 'OPPORTUNITY_EXPIRED' || info.code === 'ALREADY_DECIDED' || info.code === 'NOT_PENDING';
}

// ── Pure helpers ─────────────────────────────────────────────────────────────

export const DECISION_CATEGORIES: readonly DecisionCategory[] = ['narrative', 'team', 'community', 'chart', 'other'];
export const BANDS: readonly LaunchBand[] = ['IGNORE', 'OBSERVE', 'ALERT', 'MANUAL_APPROVAL', 'AUTO_CANDIDATE'];
// Mirrors internal/launch/core/screening.go OppStatus* constants.
export const OPPORTUNITY_STATUSES = ['REJECTED', 'BLOCKED_STALE', 'DATA_CONFLICT', 'SCORED', 'PENDING_APPROVAL', 'APPROVED', 'REJECTED_MANUAL', 'EXPIRED', 'ENTERED', 'ENTRY_BLOCKED'] as const;
export const CHANNELS: readonly EntryChannel[] = ['auto', 'manual', 'counterfactual'];

/** Minimum user level (backend `launch_operate`) to approve/reject/kill/resume. */
export const OPERATE_MIN_LEVEL = 3;

export interface TimeRemaining {
  expired: boolean;
  totalSeconds: number;
  /** `m:ss` (or `h:mm:ss`); `0:00` when expired. */
  label: string;
}

export function timeRemaining(expiresAt: string | null | undefined, nowMs: number): TimeRemaining {
  const exp = expiresAt ? Date.parse(expiresAt) : NaN;
  if (!isFinite(exp)) return { expired: true, totalSeconds: 0, label: '0:00' };
  const total = Math.max(0, Math.ceil((exp - nowMs) / 1000));
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  const ss = String(s).padStart(2, '0');
  const label = h > 0 ? `${h}:${String(m).padStart(2, '0')}:${ss}` : `${m}:${ss}`;
  return { expired: total === 0, totalSeconds: total, label };
}

export type BadgeTone = 'ok' | 'real' | 'micro' | 'neutral';

/** EDGE → ok, NO_EDGE → real (red), INSUFFICIENT_DATA → micro (amber). */
export function verdictTone(verdict: string | null | undefined): BadgeTone {
  if (verdict === 'EDGE') return 'ok';
  if (verdict === 'NO_EDGE') return 'real';
  if (verdict === 'INSUFFICIENT_DATA') return 'micro';
  return 'neutral';
}

export const REASON_CODES = [
  'HONEYPOT', 'NO_EXIT_ROUTE', 'AUTHORITY_ACTIVE', 'TOKEN22_EXTENSION', 'TAX_TOO_HIGH', 'LP_UNLOCKED',
  'RANGE', 'CONCENTRATION', 'BLOCKED_STALE', 'DATA_CONFLICT', 'INVALID_PRICE', 'RPC_BUDGET_EXCEEDED',
  'KNOWN_RUGGER', 'BLACKLISTED', 'KILL_SWITCH', 'PAUSE_ENTRIES', 'LAUNCH_DISABLED', 'MAX_OPEN_POSITIONS',
  'MAX_EXPOSURE', 'MAX_PER_TOKEN', 'STRATEGY_FILTER', 'NO_STRATEGY', 'NO_EXIT_PLAN', 'MAX_SLIPPAGE',
] as const;

export const EXIT_REASONS = [
  'TAKE_PROFIT', 'STOP_LOSS', 'TRAILING_STOP', 'MAX_HOLD',
  'EMERGENCY_EXIT:liquidity_drop', 'EMERGENCY_EXIT:liquidity_removed', 'EMERGENCY_EXIT:freeze', 'NO_EXIT_ROUTE',
] as const;

/** i18n key (namespace Launch) for a reject/pause reason code, or null when unknown. */
export function reasonKey(code: string | null | undefined): string | null {
  return code && (REASON_CODES as readonly string[]).includes(code) ? `reason_${code}` : null;
}

/** i18n key for an exit reason (`EMERGENCY_EXIT:freeze` → `exit_EMERGENCY_EXIT_freeze`), or null. */
export function exitReasonKey(code: string | null | undefined): string | null {
  return code && (EXIT_REASONS as readonly string[]).includes(code) ? `exit_${code.replace(/:/g, '_')}` : null;
}

export function bandKey(band: string | null | undefined): string | null {
  return band && (BANDS as readonly string[]).includes(band) ? `band_${band}` : null;
}

export const SCREEN_RESULTS = ['PASSED', 'REJECTED', 'BLOCKED'] as const;

/** i18n key for a screening result (BLOCKED = data problem, not a security verdict), or null. */
export function screenKey(result: string | null | undefined): string | null {
  return result && (SCREEN_RESULTS as readonly string[]).includes(result) ? `screen_${result}` : null;
}

export function statusKey(status: string | null | undefined): string | null {
  return status && (OPPORTUNITY_STATUSES as readonly string[]).includes(status) ? `status_${status}` : null;
}

/** True when the user may act on the opportunity right now. */
export function canDecide(
  opp: Pick<Opportunity, 'status' | 'expires_at' | 'decision'>,
  canOperate: boolean,
  nowMs: number,
): boolean {
  return (
    canOperate &&
    opp.status === 'PENDING_APPROVAL' &&
    opp.decision == null &&
    !timeRemaining(opp.expires_at, nowMs).expired
  );
}

/** Operate permission: backend flag when known, else user level >= 3. */
export function canOperate(userLevel: number | null | undefined, backendFlag?: boolean): boolean {
  if (backendFlag !== undefined) return backendFlag;
  return (userLevel ?? 0) >= OPERATE_MIN_LEVEL;
}

/** Queue sorted by expiry ascending; opportunities without expiry go last. */
export function sortByExpiry<T extends { expires_at: string | null }>(items: T[]): T[] {
  const t = (x: T) => (x.expires_at ? Date.parse(x.expires_at) : Infinity);
  return [...items].sort((a, b) => t(a) - t(b));
}

/** Service is degraded when launch-bot is not running. */
export function isDegraded(status: Pick<LaunchStatus, 'service'> | undefined): boolean {
  return !!status && status.service !== 'up';
}
