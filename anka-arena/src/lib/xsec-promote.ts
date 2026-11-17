// Pure logic behind PromoteToLivePanel ("run live with this backtest's
// configuration"): reading the backtest job's argv, deciding whether promotion
// is blocked, and creating the dry-run / live jobs with IDENTICAL arguments.
// Kept free of React so it is unit-testable in the node vitest environment.

import {
  createXsecJob,
  type BacktestReport,
  type CreateXsecJobRequest,
  type CreateXsecJobResult,
  type SuggestParams,
  type XSecJob,
} from '@/lib/xsec-api';
import { findMatchingDryRun, isLegBelowMin } from '@/lib/xsec-live-gate';

/** Backtest settings read back from the job's literal argv (cmd/xsec-sim flags). */
export interface BacktestJobConfig {
  k?: number;
  lookback?: number;
  /** Rebalance cadence in days (`-rebal`). */
  rebal?: number;
  band?: number;
  capital?: number;
  volWeights?: boolean;
  stopShort?: number;
  stopLong?: number;
  tpLiquidate?: number;
  tpLeg?: number;
  tpRebalance?: number;
  trailTp?: number;
}

const NUM_FLAGS: Record<string, keyof BacktestJobConfig> = {
  '-k': 'k',
  '-lb': 'lookback',
  '-rebal': 'rebal',
  '-band': 'band',
  '-capital': 'capital',
  '-stop-short': 'stopShort',
  '-stop-long': 'stopLong',
  '-tp-liquidate': 'tpLiquidate',
  '-tp-leg': 'tpLeg',
  '-tp-rebalance': 'tpRebalance',
  '-trail-tp': 'trailTp',
};

/**
 * Parse a backtest job's argv (`["-lb","10","-k","10",...,"-vol-weights=true"]`).
 * Handles both `-flag value` and `-flag=value`. Unknown flags are ignored.
 * Leverage / margin utilization are NOT backtest flags, so they never appear here.
 */
export function parseBacktestJobArgs(args: string[] | null | undefined): BacktestJobConfig {
  const out: BacktestJobConfig = {};
  const list = args ?? [];
  for (let i = 0; i < list.length; i++) {
    const raw = list[i];
    const eq = raw.indexOf('=');
    const flag = eq === -1 ? raw : raw.slice(0, eq);
    if (flag === '-vol-weights') {
      // Go bool flag: bare `-vol-weights` means true; `-vol-weights=false` is explicit.
      out.volWeights = eq === -1 ? true : raw.slice(eq + 1) === 'true';
      continue;
    }
    const key = NUM_FLAGS[flag];
    if (!key) continue;
    const valueText = eq === -1 ? list[i + 1] : raw.slice(eq + 1);
    if (valueText === undefined) continue;
    const n = Number(valueText);
    if (!isFinite(n)) continue;
    (out as Record<string, number>)[key] = n;
    if (eq === -1) i++;
  }
  return out;
}

/** One backtest parameter the live executor (cmd/xsec-live) does not apply. */
export interface NotAppliedLiveItem {
  key: 'rebal' | 'band' | 'vol_weights' | 'stop_short' | 'stop_long' | 'tp_liquidate' | 'tp_leg' | 'tp_rebalance' | 'trail_tp';
  value: string;
}

/**
 * Backtest parameters that live rebalancing ignores (xsec-live only takes
 * k / lookback / max-pos-usd / max-total-usd). Only parameters actually set in
 * the backtest are listed, with their backtest value.
 */
export function listNotAppliedLive(cfg: BacktestJobConfig): NotAppliedLiveItem[] {
  const items: NotAppliedLiveItem[] = [];
  if (cfg.rebal !== undefined) items.push({ key: 'rebal', value: `${cfg.rebal}d` });
  if (cfg.band !== undefined) items.push({ key: 'band', value: String(cfg.band) });
  if (cfg.volWeights === true) items.push({ key: 'vol_weights', value: 'on' });
  const optional: [NotAppliedLiveItem['key'], number | undefined][] = [
    ['stop_short', cfg.stopShort],
    ['stop_long', cfg.stopLong],
    ['tp_liquidate', cfg.tpLiquidate],
    ['tp_leg', cfg.tpLeg],
    ['tp_rebalance', cfg.tpRebalance],
    ['trail_tp', cfg.trailTp],
  ];
  for (const [key, v] of optional) {
    if (v !== undefined && v > 0) items.push({ key, value: String(v) });
  }
  return items;
}

export type PromoteBlockReason =
  | 'verdict'
  | 'liquidated'
  | 'level'
  | 'running'
  | 'config_missing'
  | 'suggest_pending'
  | 'infeasible'
  | 'k_clamped'
  | 'leg_below_min'
  | 'universe_unknown'
  | 'equity_unknown'
  | 'exceeds_equity';

/** Tolerance over the account capacity (equity x leverage x utilization) before blocking. */
export const EQUITY_CAPACITY_TOLERANCE = 1.05;
/** Max long/short imbalance (%) of a dry-run plan that may still be executed live. */
export const PROMOTE_MAX_IMBALANCE_PCT = 15;

/** Max live gross total the real account supports, tolerance included. */
export function equityCapacityUsd(equity: number, leverage: number, utilization: number): number {
  return equity * leverage * utilization * EQUITY_CAPACITY_TOLERANCE;
}

export interface PromoteBlockInput {
  report: Pick<BacktestReport, 'verdict' | 'liquidated' | 'windows'>;
  canWrite: boolean;
  anyRunning: boolean;
  /** k and lookback from the backtest job args. */
  backtestK: number | undefined;
  backtestLookback: number | undefined;
  suggest: SuggestParams | null;
  /** Real futures account equity (USD); undefined/NaN = unknown, which blocks (fail closed). */
  accountEquity?: number;
  /** Leverage and margin utilization the panel sized with. */
  leverage?: number;
  utilization?: number;
}

/** First blocking reason, or null when promotion may proceed to the preview. */
export function evaluatePromoteBlock(i: PromoteBlockInput): PromoteBlockReason | null {
  if (i.report.verdict !== 'PROMISING') return 'verdict';
  if (i.report.liquidated === true || i.report.windows.some((w) => w.liquidated)) return 'liquidated';
  if (!i.canWrite) return 'level';
  if (i.anyRunning) return 'running';
  if (i.backtestK === undefined || i.backtestLookback === undefined) return 'config_missing';
  if (!i.suggest) return 'suggest_pending';
  if (i.suggest.universe_size === -1) return 'universe_unknown';
  if (!i.suggest.feasible) return 'infeasible';
  if (i.suggest.k < i.backtestK) return 'k_clamped';
  // Live per-leg below the exchange minimum contract: legs would be dropped and
  // the basket left imbalanced (mirrors the server's -min-leg-usd guard).
  if (isLegBelowMin(i.suggest.max_pos_usd)) return 'leg_below_min';
  const eq = i.accountEquity;
  if (eq === undefined || !isFinite(eq) || eq <= 0) return 'equity_unknown';
  if (i.leverage === undefined || i.utilization === undefined) return 'equity_unknown';
  if (i.suggest.max_total_usd > equityCapacityUsd(eq, i.leverage, i.utilization)) return 'exceeds_equity';
  return null;
}

/** Arguments shared verbatim by the dry-run and the live job. */
export interface PromoteJobArgs {
  k: number;
  lookback: number;
  maxPosUsd: number;
  maxTotalUsd: number;
}

type CreateJob = (req: CreateXsecJobRequest) => Promise<CreateXsecJobResult>;

function toRequest(mode: 'rebalance-dry' | 'rebalance-live', a: PromoteJobArgs): CreateXsecJobRequest {
  return { mode, k: a.k, max_pos_usd: a.maxPosUsd, lookback: a.lookback, max_total_usd: a.maxTotalUsd };
}

/** Step 1: create the rebalance-dry job against the real book. */
export function requestDryRun(a: PromoteJobArgs, create: CreateJob = createXsecJob): Promise<CreateXsecJobResult> {
  return create(toRequest('rebalance-dry', a));
}

/** True when `dryJob` satisfies the shared dry-run-first gate for these args. */
export function canConfirmLive(dryJob: XSecJob | null | undefined, a: PromoteJobArgs): boolean {
  if (!dryJob) return false;
  return findMatchingDryRun([dryJob], a.k, a.maxPosUsd, a.lookback, a.maxTotalUsd) != null;
}

/**
 * Step 2: create the rebalance-live job. Re-checks the gate right before
 * sending (the server re-checks too and answers 412) and returns null
 * without creating anything if the dry-run is missing, mismatched or older than 10 minutes.
 */
export async function requestLive(
  dryJob: XSecJob | null | undefined,
  a: PromoteJobArgs,
  create: CreateJob = createXsecJob,
): Promise<CreateXsecJobResult | null> {
  if (!canConfirmLive(dryJob, a)) return null;
  return create(toRequest('rebalance-live', a));
}
