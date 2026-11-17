// Dry-run-first gate for LIVE rebalances, shared by ActionsPanel (Live tab)
// and PromoteToLivePanel (Backtesting tab) so both apply one single criterion:
// a finished rebalance-dry job with identical overrides, finished < 10 min ago.
// Client-side mirror of the server's exact-argv 412 gate.

import { parseJobArgs, type XSecJob } from '@/lib/xsec-api';

export const TEN_MINUTES_MS = 10 * 60 * 1000;

export function findMatchingDryRun(
  jobs: XSecJob[],
  k: number,
  maxPosUsd: number,
  lookback: number,
  maxTotalUsd: number,
): XSecJob | null {
  const now = Date.now();
  for (const job of jobs) {
    if (job.mode !== 'rebalance-dry' || job.status !== 'done' || !job.finished_at) continue;
    const finishedMs = new Date(job.finished_at).getTime();
    if (isNaN(finishedMs) || now - finishedMs > TEN_MINUTES_MS) continue;
    const args = parseJobArgs(job);
    if (
      args.k === k &&
      args.max_pos_usd === maxPosUsd &&
      args.lookback === lookback &&
      args.max_total_usd === maxTotalUsd
    ) return job;
  }
  return null;
}

/**
 * Minimum per-leg notional (USD). Legs below it fall under the exchange's
 * minimum contract, get dropped at execution and leave the basket imbalanced.
 * Mirror of the server guard's -min-leg-usd default (cmd/xsec-live).
 */
export const XSEC_MIN_LEG_USD = 10;

/** True when a finite, positive per-leg size is below the minimum leg size. */
export function isLegBelowMin(maxPosUsd: number, minLegUsd: number = XSEC_MIN_LEG_USD): boolean {
  return isFinite(maxPosUsd) && maxPosUsd > 0 && maxPosUsd < minLegUsd - 1e-9;
}
