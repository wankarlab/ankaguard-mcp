// xsec-api.ts — Typed wrappers for the XSec (cross-sectional market-neutral
// futures basket) control surface.
//
// GET /xsec/status is public and already wrapped by getXSecStatus in
// motor-api.ts; this module re-exports it under the naming convention used by
// the rest of this file and adds the async job control surface (JWT) that
// drives sync-book / rebalance-dry / rebalance-live / cancel-order.
//
// WIRE SHAPE mirrors internal/api/v1/handlers/xsec_jobs_handler.go exactly
// (verified against the real handler, not guessed):
//   - XSecJob.args is the literal subprocess argv (string[]), e.g.
//     ["-db","data/trading.db","-rebalance","-target-source","compute",
//      "-k","5","-max-pos-usd","3"]. parseJobArgs below extracts -k /
//     -max-pos-usd from that argv (see BuildArgv in xsec_job_runner.go).
//   - ListJobs returns newest-first (xsec.ListJobs, ORDER BY id DESC per the
//     job runner's insert order) with output truncated to 4KB; GetJob (single)
//     returns the untruncated row.
//   - Error bodies use models.APIResponse: `{ success:false, error:{code,
//     message}, timestamp }`. The 409 single-flight conflict additionally
//     carries a top-level `running_job_id`. See errText helpers in the
//     components — they read body.error.message, not body.error as a string.
//   - PutParam's success response is `{ key, value }`, NOT the full
//     XSecParam row (no updated_at) — GetParams must be re-fetched for that.

import { apiFetch } from '@/lib/api';
import { getXSecStatus, type XSecStatus, type XSecParam } from '@/lib/motor-api';

export type { XSecStatus, XSecEquityPoint, XSecPosition, XSecParam, XSecShadowBasket, XSecActiveAlert } from '@/lib/motor-api';

/** GET /api/v1/xsec/status — public read-only strategy snapshot. */
export async function getXsecStatus(): Promise<XSecStatus> {
  return getXSecStatus();
}

// ── Jobs ─────────────────────────────────────────────────────────────────────

export type XSecJobMode = 'sync-book' | 'rebalance-dry' | 'rebalance-live' | 'cancel-order' | 'close-leg' | 'backtest' | 'backtest-sweep' | 'shadow-record' | 'close-all' | 'relaunch' | 'neutralize' | 'autoconfig';
export type XSecJobStatus = 'running' | 'done' | 'error';

// XSecJob mirrors xsecJobResponse (snake_case json tags). exit_code and
// finished_at are `omitempty` on the wire (absent, not null, while running).
export interface XSecJob {
  id: number;
  mode: string;
  args: string[];
  status: XSecJobStatus;
  output: string;
  exit_code?: number | null;
  created_by: string;
  started_at: string;
  finished_at?: string | null;
}

// Backend error envelope for non-2xx xsec endpoints (models.APIResponse).
export interface XSecApiErrorBody {
  success: false;
  error?: { code: string; message: string; details?: string };
  running_job_id?: number; // only present on 409 single-flight conflicts
}

export interface CreateXsecJobRequest {
  mode: XSecJobMode;
  k?: number;
  max_pos_usd?: number;
  lookback?: number;
  max_total_usd?: number;
  order_id?: string;
  pair?: string;
  // Backtest-only overrides (cmd/xsec-sim flags via BuildArgv). k/lookback
  // above are reused as-is — same meaning (basket size per side / momentum
  // lookback in bars).
  rebal?: number;
  band?: number;
  cost?: number;
  funding?: number;
  capital?: number;
  // Autoconfig-only: margin utilization (0-1); capital omitted = real futures equity.
  utilization?: number;
  vol_weights?: boolean;
  min_turnover_usd?: number;
  windows?: number;
  // Backtest-sweep-only.
  sweep_param?: 'lb' | 'k' | 'rebal' | 'band';
  sweep_values?: number[];
  // Backtest-only, advanced (Fase 3).
  stop_short?: number;
  stop_long?: number;
  tp_liquidate?: number;
  tp_leg?: number;
  tp_rebalance?: number;
  trail_tp?: number;
  regime_floor?: number;
  regime_window?: number;
  maker_fill_prob?: number;
  taker_cost?: number;
  vol_target?: number;
  max_short_weight?: number;
  funding_tilt?: number;
  pairs?: string;
  start_frac?: number;
  end_frac?: number;
}

export interface CreateXsecJobResult {
  job_id: number;
}

/** GET /api/v1/xsec/jobs?limit=20 — recent jobs (output truncated server-side). */
export async function listXsecJobs(limit = 20): Promise<XSecJob[]> {
  const jobs = await apiFetch<XSecJob[] | null>(`/api/v1/xsec/jobs?limit=${limit}`);
  return jobs ?? [];
}

/** GET /api/v1/xsec/jobs/:id — single job with full (non-truncated) output. */
export async function getXsecJob(id: number): Promise<XSecJob> {
  return apiFetch<XSecJob>(`/api/v1/xsec/jobs/${id}`);
}

// ── Funding ──────────────────────────────────────────────────────────────────

// One basket leg's live funding read. funding_rate is the decimal per-8h rate
// (0.0001 = 0.01%); next_funding_usd is the leg cashflow at the next
// settlement (+ = basket receives, − = pays). Both absent when the live rate
// could not be fetched for that leg.
export interface XSecFundingLeg {
  pair: string;
  dir: number;
  notional: number;
  funding_rate?: number;
  next_funding_usd?: number;
}

// Basket-level funding cash flow. net_next_funding_usd / est_daily_usd are
// POSITIVE when the book earns funding, negative when it pays.
export interface XSecFunding {
  legs: XSecFundingLeg[];
  net_next_funding_usd: number;
  est_daily_usd: number;
  next_funding_time?: string | null;
  known_legs: number;
  total_legs: number;
  updated_at: string;
}

/** GET /api/v1/xsec/funding — live funding cash flow of the current basket. */
export async function getXsecFunding(): Promise<XSecFunding> {
  return apiFetch<XSecFunding>('/api/v1/xsec/funding');
}

// ---- Assisted parametrization ------------------------------------------------

// SuggestParams mirrors handlers.SuggestParamsResponse (xsec_suggest.go). Given
// a capital amount + leverage it returns the best FEASIBLE basket config: the
// OOS-robust config (lb14/k12/rebal5/band0) with k clamped by the live universe
// and by a per-leg min-notional floor. `warnings` are stable codes translated
// in the UI (see WARN_CODE handling in BacktestPanel). universe_size is -1 when
// the live KuCoin fetch failed.
export interface SuggestParams {
  capital: number;
  leverage: number;
  utilization: number;
  universe_size: number;
  min_turnover_usd: number;
  k: number;
  lookback: number;
  rebal: number;
  band: number;
  vol_weights: boolean;
  legs: number;
  per_leg_usd: number;
  max_pos_usd: number;
  max_total_usd: number;
  gross_exposure: number;
  margin_used: number;
  feasible: boolean;
  warnings: string[];
}

/** GET /api/v1/xsec/suggest-params — public, no money at risk. */
export async function getXsecSuggestParams(
  capital: number,
  leverage: number,
  utilization: number,
): Promise<SuggestParams> {
  const q = new URLSearchParams({
    capital: String(capital),
    leverage: String(leverage),
    utilization: String(utilization),
  });
  return apiFetch<SuggestParams>(`/api/v1/xsec/suggest-params?${q.toString()}`);
}

// ---- Forward paper/shadow A/B ------------------------------------------------

export interface ShadowEquityPoint {
  ts: string;
  equity: number;
  period_ret: number;
  n_long: number;
  n_short: number;
}

// One scorer's realized, net-of-cost, market-neutral forward paper equity.
export interface ShadowEquity {
  scorer: string;
  curve: ShadowEquityPoint[];
  total_return: number;
  gross_return: number;
  n_periods: number;
  avg_period_ret: number;
  last_ts: string;
}

export interface ShadowComparison {
  scorers: ShadowEquity[];
  cost_frac: number;
}

/** GET /api/v1/xsec/shadow — forward paper equity of every recorded scorer. */
export async function getXsecShadow(): Promise<ShadowComparison> {
  const r = await apiFetch<ShadowComparison | null>('/api/v1/xsec/shadow');
  return r ?? { scorers: [], cost_frac: 0 };
}

/** DELETE /api/v1/xsec/shadow/:scorer — clear a scorer's records (JWT, Level 2). */
export async function resetXsecShadow(scorer: string): Promise<{ scorer: string; deleted: number }> {
  return apiFetch<{ scorer: string; deleted: number }>(`/api/v1/xsec/shadow/${encodeURIComponent(scorer)}`, {
    method: 'DELETE',
  });
}

/**
 * POST /api/v1/xsec/jobs — enqueue an async job. Throws ApiError on:
 *  - 409 when another job is already running
 *  - 412 when mode=rebalance-live has no matching recent successful dry-run
 *  - 400 on validation errors
 */
export async function createXsecJob(req: CreateXsecJobRequest): Promise<CreateXsecJobResult> {
  return apiFetch<CreateXsecJobResult>('/api/v1/xsec/jobs', {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

export interface ParsedJobArgs {
  k?: number;
  max_pos_usd?: number;
  lookback?: number;
  max_total_usd?: number;
  order_id?: string;
  pair?: string;
}

/**
 * Extract the override values from XSecJob.args, the literal argv built
 * server-side by XSecJobRunner.BuildArgv (e.g. `-k 10 -max-pos-usd 2.5
 * -lookback 10 -max-total-usd 50`, `-cancel-order 123...` or `-close-leg
 * AAVE-USDT`). Used to match a rebalance-dry job against the current form
 * inputs for the LIVE dry-run-first gate — every rebalance override the form
 * sends must be compared here, mirroring the server's exact-argv check.
 */
export function parseJobArgs(job: XSecJob): ParsedJobArgs {
  const args = job.args ?? [];
  const result: ParsedJobArgs = {};
  const numFlags: Record<string, keyof Pick<ParsedJobArgs, 'k' | 'max_pos_usd' | 'lookback' | 'max_total_usd'>> = {
    '-k': 'k',
    '-max-pos-usd': 'max_pos_usd',
    '-lookback': 'lookback',
    '-max-total-usd': 'max_total_usd',
  };
  for (let i = 0; i < args.length; i++) {
    const numKey = numFlags[args[i]];
    if (numKey && args[i + 1] !== undefined) {
      const n = Number(args[i + 1]);
      if (isFinite(n)) result[numKey] = n;
    } else if (args[i] === '-cancel-order' && args[i + 1] !== undefined) {
      result.order_id = args[i + 1];
    } else if (args[i] === '-close-leg' && args[i + 1] !== undefined) {
      result.pair = args[i + 1];
    }
  }
  return result;
}

// ── Rebalance output warnings ────────────────────────────────────────────────

export interface JobWarnings {
  /** Legs the reconciliation reported as MISSING (open failed/unfilled). */
  missingLegs: number;
  /** True when the output carries the net-exposure (non-market-neutral) alarm. */
  netExposureWarning: boolean;
}

/**
 * Scan a rebalance job's output for the two critical partial-result markers
 * xsec-live prints in its reconciliation section: `🚫 MISSING` (one per leg
 * that failed to open — e.g. the pair's minimum contract size exceeds the
 * per-leg target) and `🚨 NET-EXPOSURE WARNING` (|long−short| imbalance above
 * the safety threshold). Pure text scan — no state, safe on truncated output.
 */
export function parseJobWarnings(output: string): JobWarnings {
  const missingLegs = (output.match(/🚫 MISSING/g) ?? []).length;
  const netExposureWarning = output.includes('🚨 NET-EXPOSURE WARNING');
  return { missingLegs, netExposureWarning };
}

/** Renders argv as a shell-like string for display (history/monitor panels). */
export function formatJobArgs(job: XSecJob): string {
  return (job.args ?? []).join(' ');
}

// ── Rebalance EXECUTION result (why a live deploy entered partial) ────────────

export interface RebalanceFailedLeg {
  pair: string;
  side: 'buy' | 'sell';
  /** The per-leg USD target that was attempted. */
  targetUsd: number;
  /** Notional of ONE contract of this pair (the reject reason). */
  contractUsd: number;
  /** contractUsd / targetUsd — how oversized 1 contract is vs the target. */
  ratio: number;
  /** Free-text reason when it wasn't the oversize/min-contract guard. */
  reason?: string;
}

export interface RebalanceReconRow {
  pair: string;
  target: string; // 'long' | 'short'
  status: 'ok' | 'drift' | 'missing';
  notionalUsd: number | null;
}

export interface RebalanceResult {
  okCount: number;
  driftCount: number;
  missingCount: number;
  failedLegs: RebalanceFailedLeg[];
  recon: RebalanceReconRow[];
  netLongUsd: number;
  netShortUsd: number;
  imbalancePct: number;
  netWarning: boolean;
  /** Smallest per-leg USD that would fit EVERY min-contract-blocked leg (max
   * contract notional / 2, since the guard rejects at >2x target). Infinity if
   * none — used to size the "resize and complete" recommendation. */
  minPerLegToFitUsd: number;
}

const FAILED_OVERSIZE_RE = /❌\s+(buy|sell)\s+(\S+)\s+\$([\d.]+).*?1 contract = \$([\d.]+).*?which is ([\d.]+)×/;
const FAILED_GENERIC_RE = /❌\s+(buy|sell)\s+(\S+)\s+\$([\d.]+)\s+\([^)]*\):\s*(.+)$/;
const NET_EXPOSURE_RE = /NET EXPOSURE:\s*long \$([\d.]+) vs short \$([\d.]+)\s*\|\s*imbalance ([\d.]+)%/;

/**
 * Parse a LIVE rebalance/relaunch job's execution output — the `RECONCILIATION`
 * table plus the `❌ ... notional safety check FAILED` lines — into a structured
 * result explaining WHY the basket entered partial. Returns null when the
 * output has no reconciliation section (dry-run, backtest, still running, or a
 * non-rebalance mode) so callers fall back to the raw log, never hide it.
 */
export function parseRebalanceResult(output: string): RebalanceResult | null {
  if (!output || !output.includes('RECONCILIATION')) return null;

  const lines = output.split('\n');
  const failedLegs: RebalanceFailedLeg[] = [];
  const recon: RebalanceReconRow[] = [];
  let netLongUsd = 0, netShortUsd = 0, imbalancePct = 0;

  for (const line of lines) {
    const ov = FAILED_OVERSIZE_RE.exec(line);
    if (ov) {
      failedLegs.push({
        side: ov[1] as 'buy' | 'sell',
        pair: ov[2],
        targetUsd: Number(ov[3]),
        contractUsd: Number(ov[4]),
        ratio: Number(ov[5]),
      });
      continue;
    }
    const gen = FAILED_GENERIC_RE.exec(line);
    if (gen && !line.includes('1 contract =')) {
      failedLegs.push({
        side: gen[1] as 'buy' | 'sell',
        pair: gen[2],
        targetUsd: Number(gen[3]),
        contractUsd: 0,
        ratio: 0,
        reason: gen[4].trim(),
      });
      continue;
    }

    // Reconciliation rows carry a status emoji and a -USDT pair.
    if (/-USDT\b/.test(line) && (line.includes('✅') || line.includes('⚠️') || line.includes('🚫'))) {
      const pairM = line.match(/(\S+-USDT)/);
      const targetM = line.match(/\b(long|short)\b/);
      const notM = line.match(/\$([\d.]+)/);
      if (pairM) {
        recon.push({
          pair: pairM[1],
          target: targetM ? targetM[1] : '',
          status: line.includes('🚫') ? 'missing' : line.includes('⚠️') ? 'drift' : 'ok',
          notionalUsd: notM ? Number(notM[1]) : null,
        });
      }
      continue;
    }

    const ne = NET_EXPOSURE_RE.exec(line);
    if (ne) {
      netLongUsd = Number(ne[1]);
      netShortUsd = Number(ne[2]);
      imbalancePct = Number(ne[3]);
    }
  }

  const okCount = recon.filter((r) => r.status === 'ok').length;
  const driftCount = recon.filter((r) => r.status === 'drift').length;
  const missingCount = recon.filter((r) => r.status === 'missing').length;
  const oversize = failedLegs.filter((l) => l.contractUsd > 0);
  const minPerLegToFitUsd = oversize.length > 0
    ? Math.max(...oversize.map((l) => l.contractUsd)) / 2
    : Infinity;

  return {
    okCount, driftCount, missingCount, failedLegs, recon,
    netLongUsd, netShortUsd, imbalancePct,
    netWarning: output.includes('🚨 NET-EXPOSURE WARNING'),
    minPerLegToFitUsd,
  };
}

// ── Rebalance plan (structured preview) ─────────────────────────────────────

export interface RebalancePlanHeader {
  targetSource: string;
  targetLegs: number;
  actualPositions: number;
  perLegUsd: number;
}

export interface RebalancePlanLeg {
  pair: string;
  side: 'buy' | 'sell';
  notionalUsd: number;
  /** Absent on CLOSE/RESIZE rows — the binary only prints price for OPEN. */
  price?: number;
  reason: string;
}

export interface RebalancePlan {
  header: RebalancePlanHeader | null;
  keep: { pair: string; dir: 1 | -1; notionalUsd: number }[];
  skip: { pair: string; dir: 1 | -1; reason: string }[];
  opens: RebalancePlanLeg[];
  closes: RebalancePlanLeg[];
  resizes: RebalancePlanLeg[];
  safetyOk: boolean;
  safetyProblems: string[];
}

/** Long/short notional of the basket a dry-run plan would leave (opens + kept legs). */
export interface PlanBalance {
  longUsd: number;
  shortUsd: number;
  gross: number;
  /** |long - short| / max(long, short) in percent; 0 when the plan is empty. */
  imbalancePct: number;
}

export function computePlanBalance(plan: Pick<RebalancePlan, 'opens' | 'keep'>): PlanBalance {
  const longUsd = plan.opens.filter((o) => o.side === 'buy').reduce((s, o) => s + o.notionalUsd, 0)
    + plan.keep.filter((k) => k.dir > 0).reduce((s, k) => s + k.notionalUsd, 0);
  const shortUsd = plan.opens.filter((o) => o.side === 'sell').reduce((s, o) => s + o.notionalUsd, 0)
    + plan.keep.filter((k) => k.dir < 0).reduce((s, k) => s + k.notionalUsd, 0);
  const gross = longUsd + shortUsd;
  const imbalancePct = gross > 0 ? (Math.abs(longUsd - shortUsd) / Math.max(longUsd, shortUsd)) * 100 : 0;
  return { longUsd, shortUsd, gross, imbalancePct };
}

const HEADER_RE = /^Target source: (\S+) \((\d+) legs\) \| actual book: (\d+) positions \| per-leg \$([\d.]+)/;
const SECTION_RE = /^\s{2}(KEEP|SKIP|CLOSE|RESIZE|OPEN)\s+\((\d+)\):(.*)$/;
// OPEN rows carry a price ("@ 0.078340"); CLOSE/RESIZE rows never do.
const LEG_WITH_PRICE_RE = /^\s+(\S+)\s+(buy|sell)\s+\$([\d.]+)\s+@\s+([\d.]+)\s+\((\S+)\)/;
const LEG_NO_PRICE_RE = /^\s+(\S+)\s+(buy|sell)\s+\$([\d.]+)\s+\((\S+)\)/;
// Inline KEEP/SKIP tokens: "ENA-USDT(+1, $2.50)" or "ZEC-USDT(+1, no live price)".
const TOKEN_RE = /(\S+?)\(([+-]\d+),\s*([^)]+)\)/g;

/**
 * Parse the `--- DIFF ---` block xsec-live's `runRebalance` prints (dry-run
 * AND live, before/instead of executing — cmd/xsec-live/main.go:853-886) into
 * a structured plan for the preview table. Returns null if the output
 * doesn't match this shape (job not settled yet, or a non-rebalance mode) —
 * callers must fall back to the raw output, never hide it.
 */
export function parseRebalancePlan(output: string): RebalancePlan | null {
  if (!output || !output.includes('--- DIFF ---')) return null;

  const lines = output.split('\n');
  let header: RebalancePlanHeader | null = null;
  const keep: RebalancePlan['keep'] = [];
  const skip: RebalancePlan['skip'] = [];
  const opens: RebalancePlanLeg[] = [];
  const closes: RebalancePlanLeg[] = [];
  const resizes: RebalancePlanLeg[] = [];
  let safetyOk = true;
  const safetyProblems: string[] = [];

  let section: 'KEEP' | 'SKIP' | 'CLOSE' | 'RESIZE' | 'OPEN' | 'SAFETY' | null = null;

  for (const line of lines) {
    const h = HEADER_RE.exec(line);
    if (h) {
      header = {
        targetSource: h[1],
        targetLegs: Number(h[2]),
        actualPositions: Number(h[3]),
        perLegUsd: Number(h[4]),
      };
      continue;
    }

    const s = SECTION_RE.exec(line);
    if (s) {
      section = s[1] as 'KEEP' | 'SKIP' | 'CLOSE' | 'RESIZE' | 'OPEN';
      const inline = s[3] ?? '';
      if (s[1] === 'KEEP' || s[1] === 'SKIP') {
        let m: RegExpExecArray | null;
        TOKEN_RE.lastIndex = 0;
        while ((m = TOKEN_RE.exec(inline)) !== null) {
          const dir = m[2].startsWith('-') ? -1 : 1;
          if (s[1] === 'KEEP') {
            const notional = parseFloat(m[3].replace(/^\$/, ''));
            keep.push({ pair: m[1], dir, notionalUsd: isFinite(notional) ? notional : 0 });
          } else {
            skip.push({ pair: m[1], dir, reason: m[3] });
          }
        }
      }
      continue;
    }

    if (line.includes('SAFETY:')) {
      section = 'SAFETY';
      safetyOk = line.includes('✅');
      continue;
    }
    if (section === 'SAFETY') {
      const p = /^ {3}- (.+)$/.exec(line);
      if (p) {
        safetyProblems.push(p[1]);
        continue;
      }
      if (line.trim() === '') continue;
      section = null; // first non-problem line ends the safety block
    }

    if (section === 'OPEN' || section === 'CLOSE' || section === 'RESIZE') {
      const withPrice = LEG_WITH_PRICE_RE.exec(line);
      const leg: RebalancePlanLeg | null = withPrice
        ? { pair: withPrice[1], side: withPrice[2] as 'buy' | 'sell', notionalUsd: parseFloat(withPrice[3]), price: parseFloat(withPrice[4]), reason: withPrice[5] }
        : (() => {
            const noPrice = LEG_NO_PRICE_RE.exec(line);
            return noPrice
              ? { pair: noPrice[1], side: noPrice[2] as 'buy' | 'sell', notionalUsd: parseFloat(noPrice[3]), reason: noPrice[4] }
              : null;
          })();
      if (leg) {
        if (section === 'OPEN') opens.push(leg);
        else if (section === 'CLOSE') closes.push(leg);
        else resizes.push(leg);
      }
    }
  }

  if (!header && opens.length === 0 && closes.length === 0 && resizes.length === 0) return null;
  // Any listed problem (imbalance, total over equity capacity, ...) means NOT ok.
  if (safetyProblems.length > 0) safetyOk = false;
  return { header, keep, skip, opens, closes, resizes, safetyOk, safetyProblems };
}

// ── Backtest report (structured) ────────────────────────────────────────────

export interface BacktestWindow {
  index: number;
  returnPct: number;
  sharpe: number;
  maxDrawdownPct: number;
  avgUniverse: number;
  /** True when cmd/xsec-sim flagged the window with ` *LIQUIDATED` (account wiped out). */
  liquidated: boolean;
}

export interface BacktestReport {
  timeframe?: string;
  pairs?: number;
  years?: number;
  finalEquity: number;
  startCapital: number;
  totalReturnPct: number;
  cagrPct: number;
  sharpe: number;
  maxDrawdownPct: number;
  rebalances: number;
  avgTurnoverPct: number;
  fundingPaid: number;
  fundingReceived: number;
  fundingNet: number;
  avgUniverseEligible: number;
  /**
   * Full-run liquidation: true/false from the `Liquidated:` summary line, or
   * null for older job outputs that predate the line (unknown — no badge).
   */
  liquidated: boolean | null;
  /** YYYY-MM-DD of the liquidation when `liquidated` is true, else null. */
  liquidatedAt: string | null;
  windows: BacktestWindow[];
  verdict: 'PROMISING' | 'MARGINAL' | 'NEGATIVE' | null;
  verdictDetail: string;
}

// Regexes matched against cmd/xsec-sim's actual stdout (verified by running
// the binary directly, not guessed from the Printf calls) — whitespace is
// intentionally loose (`\s+`) since column widths are cosmetic alignment,
// not a wire contract.
const BT_CONFIG_RE = /^#\s+(\S+),\s+(\d+)\s+pairs,\s+([\d.]+)\s+years/;
const BT_FINAL_EQUITY_RE = /Final equity:\s+\$([\d,.-]+)\s+\(start\s+\$([\d,.-]+)\)/;
const BT_TOTAL_RETURN_RE = /Total return:\s+([+-][\d.]+)%/;
const BT_CAGR_RE = /CAGR:\s+([+-][\d.]+)%\/yr/;
const BT_SHARPE_RE = /Sharpe \(ann\.\):\s+([+-]?[\d.]+)/;
const BT_MAX_DD_RE = /Max drawdown:\s+-?([\d.]+)%/;
const BT_REBALANCES_RE = /Rebalances:\s+(\d+)\s+avg turnover\s+([\d.]+)%\/rebal/;
const BT_FUNDING_RE = /Funding P&L:\s+paid\s+\$([\d,.-]+),\s+received\s+\$([\d,.-]+),\s+net\s+\$([+-][\d,.-]+)/;
const BT_UNIVERSE_RE = /Universe:\s+avg\s+([\d.]+)\s+eligible\/rebalance/;
const BT_LIQUIDATED_RE = /Liquidated:\s+(YES|no)\b(?:\s*\(([^)]*)\))?/;
// Suffix on a W<n> line; kept out of BT_WINDOW_RE's capture groups on purpose.
const BT_WINDOW_LIQUIDATED_RE = /\*LIQUIDATED/;
const BT_WINDOW_RE = /W(\d+):\s+([+-][\d.]+)%\s+\(Sharpe\s+([+-]?[\d.]+),\s+DD\s+-([\d.]+)%,\s+univ\s+([\d.]+)/;
const BT_VERDICT_RE = /^#\s*VERDICT:\s*(PROMISING|MARGINAL|NEGATIVE)\s*[—-]*\s*(.*)$/;

/**
 * Parse cmd/xsec-sim's stdout report into a structured shape for the
 * backtest UI. Returns null if the output doesn't match this shape (job not
 * settled yet, subprocess crashed before printing a report, or a non-backtest
 * mode) — callers must fall back to the raw output, never hide it.
 */
export function parseBacktestReport(output: string): BacktestReport | null {
  if (!output || !output.includes('Final equity:')) return null;

  const lines = output.split('\n');
  const report: BacktestReport = {
    finalEquity: 0, startCapital: 0, totalReturnPct: 0, cagrPct: 0, sharpe: 0,
    maxDrawdownPct: 0, rebalances: 0, avgTurnoverPct: 0, fundingPaid: 0,
    fundingReceived: 0, fundingNet: 0, avgUniverseEligible: 0,
    liquidated: null, liquidatedAt: null, windows: [],
    verdict: null, verdictDetail: '',
  };
  let found = false;

  for (const line of lines) {
    let m: RegExpExecArray | null;

    if ((m = BT_CONFIG_RE.exec(line))) {
      report.timeframe = m[1];
      report.pairs = Number(m[2]);
      report.years = parseFloat(m[3]);
      continue;
    }
    if ((m = BT_FINAL_EQUITY_RE.exec(line))) {
      report.finalEquity = parseFloat(m[1].replace(/,/g, ''));
      report.startCapital = parseFloat(m[2].replace(/,/g, ''));
      found = true;
      continue;
    }
    if ((m = BT_TOTAL_RETURN_RE.exec(line))) { report.totalReturnPct = parseFloat(m[1]); continue; }
    if ((m = BT_CAGR_RE.exec(line))) { report.cagrPct = parseFloat(m[1]); continue; }
    if ((m = BT_SHARPE_RE.exec(line))) { report.sharpe = parseFloat(m[1]); continue; }
    if ((m = BT_MAX_DD_RE.exec(line))) { report.maxDrawdownPct = parseFloat(m[1]); continue; }
    if ((m = BT_REBALANCES_RE.exec(line))) {
      report.rebalances = Number(m[1]);
      report.avgTurnoverPct = parseFloat(m[2]);
      continue;
    }
    if ((m = BT_FUNDING_RE.exec(line))) {
      report.fundingPaid = parseFloat(m[1].replace(/,/g, ''));
      report.fundingReceived = parseFloat(m[2].replace(/,/g, ''));
      report.fundingNet = parseFloat(m[3].replace(/,/g, ''));
      continue;
    }
    if ((m = BT_LIQUIDATED_RE.exec(line))) {
      report.liquidated = m[1] === 'YES';
      report.liquidatedAt = report.liquidated ? (m[2]?.trim() || null) : null;
      continue;
    }
    if ((m = BT_UNIVERSE_RE.exec(line))) { report.avgUniverseEligible = parseFloat(m[1]); continue; }
    if ((m = BT_WINDOW_RE.exec(line))) {
      report.windows.push({
        index: Number(m[1]),
        returnPct: parseFloat(m[2]),
        sharpe: parseFloat(m[3]),
        maxDrawdownPct: parseFloat(m[4]),
        avgUniverse: parseFloat(m[5]),
        liquidated: BT_WINDOW_LIQUIDATED_RE.test(line),
      });
      continue;
    }
    if ((m = BT_VERDICT_RE.exec(line))) {
      report.verdict = m[1] as BacktestReport['verdict'];
      report.verdictDetail = m[2].trim();
      continue;
    }
  }

  return found ? report : null;
}

export interface BacktestEquityPoint {
  ts: string;
  value: number;
}

/**
 * Extract the `## EQUITY_CSV_BEGIN` / `## EQUITY_CSV_END` fenced block
 * cmd/xsec-sim emits with `-print-equity` (always sent for mode=backtest —
 * see BuildArgv) into equity-curve points for EquityChart. Returns null if
 * the block isn't present (older job predating this feature, or fewer than
 * 2 rows — nothing to chart) so the caller can show an empty-state message
 * instead of a broken chart.
 */
export function parseBacktestEquityCurve(output: string): BacktestEquityPoint[] | null {
  const begin = output.indexOf('## EQUITY_CSV_BEGIN');
  const end = output.indexOf('## EQUITY_CSV_END');
  if (begin === -1 || end === -1 || end < begin) return null;

  const block = output.slice(begin, end).split('\n').slice(1); // drop the BEGIN marker line
  const points: BacktestEquityPoint[] = [];
  for (const line of block) {
    const cols = line.split(',');
    if (cols[0] === 'date' || cols.length < 2) continue; // header or blank
    const value = parseFloat(cols[1]);
    if (!cols[0] || !isFinite(value)) continue;
    points.push({ ts: cols[0], value });
  }
  return points.length >= 2 ? points : null;
}

// ── Backtest sweep (structured) ──────────────────────────────────────────────

export interface BacktestSweepRun {
  index: number;
  /** The swept dimension's name, e.g. "k" — same for every run in one sweep. */
  param: string;
  paramValue: number;
  report: BacktestReport | null;
  /** Raw text for this run — shown when `report` is null (that one run's report didn't parse). */
  rawOutput: string;
}

// Matches internal/api/v1/handlers/xsec_job_runner.go executeSweep's exact
// header: `### SWEEP i=<n> <param>=<value> ###`.
const BT_SWEEP_HEADER_RE = /^### SWEEP i=(\d+) (\S+)=([\d.-]+) ###$/;

/**
 * Split a backtest-sweep job's output into one parsed BacktestReport per
 * run (each run's own stdout, cmd/xsec-sim, real historical data). Returns
 * null if no `### SWEEP` header is found (job not settled yet, or wrong
 * mode) — same fallback contract as parseBacktestReport.
 */
export function parseBacktestSweep(output: string): BacktestSweepRun[] | null {
  if (!output || !output.includes('### SWEEP')) return null;

  const lines = output.split('\n');
  const runs: BacktestSweepRun[] = [];
  let current: { index: number; param: string; paramValue: number; lines: string[] } | null = null;

  function flush() {
    if (!current) return;
    const rawOutput = current.lines.join('\n');
    runs.push({
      index: current.index,
      param: current.param,
      paramValue: current.paramValue,
      report: parseBacktestReport(rawOutput),
      rawOutput,
    });
  }

  for (const line of lines) {
    const h = BT_SWEEP_HEADER_RE.exec(line);
    if (h) {
      flush();
      current = { index: Number(h[1]), param: h[2], paramValue: parseFloat(h[3]), lines: [] };
      continue;
    }
    if (current) current.lines.push(line);
  }
  flush();

  return runs.length > 0 ? runs : null;
}

// ── Params ───────────────────────────────────────────────────────────────────

/** GET /api/v1/xsec/params — xsec_params key/value rows (JWT). */
export async function getXsecParams(): Promise<XSecParam[]> {
  const params = await apiFetch<XSecParam[] | null>('/api/v1/xsec/params');
  return params ?? [];
}

/**
 * PUT /api/v1/xsec/params — update one key/value row (JWT, Level 2). The
 * success body is only `{ key, value }` (no updated_at) — callers re-fetch
 * getXsecParams to refresh the full row (including its new updated_at).
 */
export async function putXsecParam(key: string, value: string): Promise<{ key: string; value: string }> {
  return apiFetch<{ key: string; value: string }>('/api/v1/xsec/params', {
    method: 'PUT',
    body: JSON.stringify({ key, value }),
  });
}

// ── Autoconfig (read-only job: biggest balanced k at 1x on the real equity) ──

export type AutoconfigReason = 'capital_below_min_leg' | 'no_balanced_k' | 'equity_unavailable' | 'below_validated_k';

export interface AutoconfigTried {
  k: number;
  per_leg_usd: number;
  longs: number;
  shorts: number;
}

/** The `AUTOCONFIG_RESULT {json}` line printed by the backend autoconfig job. */
export interface AutoconfigResult {
  feasible: boolean;
  reason: string;
  equity: number;
  capital: number;
  utilization: number;
  leverage: number;
  k: number;
  lookback: number;
  rank_buffer: number;
  per_leg_usd: number;
  max_pos_usd: number;
  max_total_usd: number;
  longs: string[];
  shorts: string[];
  tried: AutoconfigTried[];
  /** Newer backends only; 0 when absent in older outputs. */
  min_k: number;
  best_balanced_k: number;
  required_equity_min_k_usd: number;
  required_equity_target_k_usd: number;
}

const AUTOCONFIG_PREFIX = 'AUTOCONFIG_RESULT ';

/** Parse the last AUTOCONFIG_RESULT line of a job output; null if absent/malformed. */
export function parseAutoconfigResult(output: string): AutoconfigResult | null {
  if (!output) return null;
  const lines = output.split('\n');
  for (let i = lines.length - 1; i >= 0; i--) {
    const line = lines[i].trim();
    if (!line.startsWith(AUTOCONFIG_PREFIX)) continue;
    try {
      const raw = JSON.parse(line.slice(AUTOCONFIG_PREFIX.length)) as Partial<AutoconfigResult> | null;
      if (!raw || typeof raw !== 'object' || typeof raw.feasible !== 'boolean') return null;
      const n = (v: unknown) => (typeof v === 'number' && isFinite(v) ? v : 0);
      const arr = (v: unknown): string[] => (Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : []);
      return {
        feasible: raw.feasible,
        reason: typeof raw.reason === 'string' ? raw.reason : '',
        equity: n(raw.equity),
        capital: n(raw.capital),
        utilization: n(raw.utilization),
        leverage: n(raw.leverage) || 1,
        k: n(raw.k),
        lookback: n(raw.lookback),
        rank_buffer: n(raw.rank_buffer),
        per_leg_usd: n(raw.per_leg_usd),
        max_pos_usd: n(raw.max_pos_usd),
        max_total_usd: n(raw.max_total_usd),
        longs: arr(raw.longs),
        shorts: arr(raw.shorts),
        tried: Array.isArray(raw.tried)
          ? raw.tried.map((t) => ({ k: n(t?.k), per_leg_usd: n(t?.per_leg_usd), longs: n(t?.longs), shorts: n(t?.shorts) }))
          : [],
        min_k: n(raw.min_k),
        best_balanced_k: n(raw.best_balanced_k),
        required_equity_min_k_usd: n(raw.required_equity_min_k_usd),
        required_equity_target_k_usd: n(raw.required_equity_target_k_usd),
      };
    } catch {
      return null;
    }
  }
  return null;
}
