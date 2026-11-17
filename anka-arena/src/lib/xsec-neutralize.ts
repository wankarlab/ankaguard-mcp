// Shared market-neutral health logic for the /xsec panel. Used by both
// StatusCard (banner + row highlight + close buttons) and PerformanceOverview
// (actionable hint under the imbalance KPI) so the recommendation is computed
// in exactly one place.

import type { XSecPosition } from '@/lib/xsec-api';

// A market-neutral basket must hold |long − short| below this fraction of the
// larger side. Above it the book carries directional beta — the exact failure
// mode of the 2026-06-09 partial-fill incident (see project memory). Mirrors
// the imbalance KPI's red threshold and xsec-live's own NET-EXPOSURE warning.
export const NEUTRAL_THRESHOLD_PCT = 15;

export interface NeutralizeAdvice {
  imbalancePct: number;
  netExposure: number; // + long-heavy, − short-heavy
  heavySide: 'long' | 'short';
  suggested: XSecPosition[];
  resultingPct: number;
}

// Greedily pick the minimal set of heavy-side legs (worst live PnL first) whose
// closure drops the imbalance back under NEUTRAL_THRESHOLD_PCT — the exact
// manual reasoning applied on 2026-07-25 (net long $4.74 → close the two worst
// longs ALLO/ERA → imbalance 26% → 5.7%). Returns null when already neutral or
// there is nothing to act on. Legs with unknown PnL sort as 0 (after known
// losers), so we never recommend closing an enriched winner over a loser.
export function computeNeutralization(positions: XSecPosition[]): NeutralizeAdvice | null {
  let longUsd = positions.filter((p) => p.dir > 0).reduce((s, p) => s + p.notional, 0);
  let shortUsd = positions.filter((p) => p.dir < 0).reduce((s, p) => s + p.notional, 0);
  const gross = longUsd + shortUsd;
  if (gross <= 0) return null;

  const net = longUsd - shortUsd;
  const imbalancePct = (Math.abs(net) / Math.max(longUsd, shortUsd)) * 100;
  if (imbalancePct <= NEUTRAL_THRESHOLD_PCT) return null;

  const heavySide: 'long' | 'short' = net > 0 ? 'long' : 'short';
  const heavyLegs = positions
    .filter((p) => (heavySide === 'long' ? p.dir > 0 : p.dir < 0))
    .slice()
    .sort((a, b) => (a.unrealized_pnl ?? 0) - (b.unrealized_pnl ?? 0)); // worst first

  const suggested: XSecPosition[] = [];
  for (const leg of heavyLegs) {
    const curImb = (Math.abs(longUsd - shortUsd) / Math.max(longUsd, shortUsd)) * 100;
    if (curImb <= NEUTRAL_THRESHOLD_PCT) break;
    if (heavySide === 'long') longUsd -= leg.notional;
    else shortUsd -= leg.notional;
    suggested.push(leg);
    const newImb =
      Math.max(longUsd, shortUsd) > 0
        ? (Math.abs(longUsd - shortUsd) / Math.max(longUsd, shortUsd)) * 100
        : 0;
    if (newImb <= NEUTRAL_THRESHOLD_PCT) break;
  }
  if (suggested.length === 0) return null;

  const resultingPct =
    Math.max(longUsd, shortUsd) > 0
      ? (Math.abs(longUsd - shortUsd) / Math.max(longUsd, shortUsd)) * 100
      : 0;
  return { imbalancePct, netExposure: net, heavySide, suggested, resultingPct };
}
