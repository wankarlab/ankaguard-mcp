import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  canConfirmLive,
  evaluatePromoteBlock,
  EQUITY_CAPACITY_TOLERANCE,
  PROMOTE_MAX_IMBALANCE_PCT,
  listNotAppliedLive,
  parseBacktestJobArgs,
  requestDryRun,
  requestLive,
  type PromoteBlockInput,
} from './xsec-promote';
import { XSEC_MIN_LEG_USD } from './xsec-live-gate';
import { computePlanBalance, type SuggestParams, type XSecJob } from './xsec-api';

// api.ts pulls the next-intl routing at import time; mock it with a minimal ApiError.
vi.mock('@/lib/api', () => {
  class ApiError extends Error {
    constructor(public status: number, public body: unknown) { super(`HTTP ${status}`); }
  }
  return { ApiError, apiFetch: vi.fn() };
});
vi.mock('@/lib/motor-api', () => ({ getXSecStatus: vi.fn() }));

const ARGS = ['-db', 'data/trading.db', '-lb', '10', '-k', '10', '-rebal', '7', '-band', '2', '-cost', '0.0012',
  '-funding', '0.0001', '-capital', '27.42', '-vol-weights=true', '-min-turnover-usd', '3000000', '-windows', '4',
  '-stop-short', '0.15', '-tp-leg', '0.3', '-print-equity'];

const suggest = (over: Partial<SuggestParams> = {}): SuggestParams => ({
  capital: 27.42, leverage: 1, utilization: 0.8, universe_size: 40, min_turnover_usd: 3e6,
  k: 10, lookback: 14, rebal: 5, band: 0, vol_weights: true, legs: 20, per_leg_usd: 3,
  max_pos_usd: 10.5, max_total_usd: 21, gross_exposure: 21, margin_used: 21,
  feasible: true, warnings: [], ...over,
});

const input = (over: Partial<PromoteBlockInput> = {}): PromoteBlockInput => ({
  report: { verdict: 'PROMISING', liquidated: false, windows: [{ index: 1, returnPct: 5, sharpe: 1, maxDrawdownPct: 5, avgUniverse: 38, liquidated: false }] },
  canWrite: true, anyRunning: false, backtestK: 10, backtestLookback: 10, suggest: suggest(),
  accountEquity: 27.42, leverage: 1, utilization: 0.8, ...over,
});

describe('parseBacktestJobArgs', () => {
  it('blocks when the live per-leg is below the minimum leg size', () => {
    expect(evaluatePromoteBlock(input({ suggest: suggest({ max_pos_usd: 1.05 }) }))).toBe('leg_below_min');
    expect(evaluatePromoteBlock(input({ suggest: suggest({ max_pos_usd: 9.99 }) }))).toBe('leg_below_min');
    expect(evaluatePromoteBlock(input({ suggest: suggest({ max_pos_usd: XSEC_MIN_LEG_USD }) }))).toBeNull();
  });
  it('leg_below_min comes after suggest_pending/k_clamped and before equity checks', () => {
    expect(evaluatePromoteBlock(input({ suggest: null }))).toBe('suggest_pending');
    expect(evaluatePromoteBlock(input({ suggest: suggest({ k: 8, max_pos_usd: 1 }) }))).toBe('k_clamped');
    expect(evaluatePromoteBlock(input({ suggest: suggest({ max_pos_usd: 1 }), accountEquity: undefined }))).toBe('leg_below_min');
  });
  it('reads values, bool flag with = and optional stops', () => {
    expect(parseBacktestJobArgs(ARGS)).toMatchObject({
      k: 10, lookback: 10, rebal: 7, band: 2, capital: 27.42, volWeights: true, stopShort: 0.15, tpLeg: 0.3,
    });
  });
  it('handles -flag=value and a trailing flag without value', () => {
    expect(parseBacktestJobArgs(['-k=12', '-lb'])).toEqual({ k: 12 });
  });
  it('vol-weights=false is explicit false', () => {
    expect(parseBacktestJobArgs(['-vol-weights=false']).volWeights).toBe(false);
  });
});

describe('listNotAppliedLive', () => {
  it('lists cadence, band, vol-weights and set stops/take-profits with values', () => {
    const items = listNotAppliedLive(parseBacktestJobArgs(ARGS));
    expect(items).toEqual([
      { key: 'rebal', value: '7d' },
      { key: 'band', value: '2' },
      { key: 'vol_weights', value: 'on' },
      { key: 'stop_short', value: '0.15' },
      { key: 'tp_leg', value: '0.3' },
    ]);
  });
  it('omits unset stops and vol-weights off', () => {
    expect(listNotAppliedLive({ rebal: 5, band: 0, volWeights: false }).map((i) => i.key)).toEqual(['rebal', 'band']);
  });
});

describe('evaluatePromoteBlock', () => {
  it('allows a PROMISING, feasible, level-2 config', () => {
    expect(evaluatePromoteBlock(input())).toBeNull();
  });
  it('blocks MARGINAL and NEGATIVE', () => {
    expect(evaluatePromoteBlock(input({ report: { ...input().report, verdict: 'MARGINAL' } }))).toBe('verdict');
    expect(evaluatePromoteBlock(input({ report: { ...input().report, verdict: null } }))).toBe('verdict');
  });
  it('blocks on run liquidation and on a liquidated window', () => {
    expect(evaluatePromoteBlock(input({ report: { ...input().report, liquidated: true } }))).toBe('liquidated');
    const w = { index: 3, returnPct: -100, sharpe: -1, maxDrawdownPct: 100, avgUniverse: 38, liquidated: true };
    expect(evaluatePromoteBlock(input({ report: { ...input().report, windows: [w] } }))).toBe('liquidated');
  });
  it('treats unknown liquidation (null, old jobs) as not liquidated', () => {
    expect(evaluatePromoteBlock(input({ report: { ...input().report, liquidated: null } }))).toBeNull();
  });
  it('blocks on level, running job, missing config and pending suggest', () => {
    expect(evaluatePromoteBlock(input({ canWrite: false }))).toBe('level');
    expect(evaluatePromoteBlock(input({ anyRunning: true }))).toBe('running');
    expect(evaluatePromoteBlock(input({ backtestK: undefined }))).toBe('config_missing');
    expect(evaluatePromoteBlock(input({ suggest: null }))).toBe('suggest_pending');
  });
  it('blocks on unknown universe, infeasible and clamped k; allows larger suggested k', () => {
    expect(evaluatePromoteBlock(input({ suggest: suggest({ universe_size: -1 }) }))).toBe('universe_unknown');
    expect(evaluatePromoteBlock(input({ suggest: suggest({ feasible: false }) }))).toBe('infeasible');
    expect(evaluatePromoteBlock(input({ suggest: suggest({ k: 8 }) }))).toBe('k_clamped');
    expect(evaluatePromoteBlock(input({ suggest: suggest({ k: 12 }) }))).toBeNull();
  });
});

describe('evaluatePromoteBlock equity capacity', () => {
  // capacity = 27.42 * 1 * 0.8 = 21.94 (x1.05 = 23.03); suggest total is 21.
  it('blocks when equity is unknown (undefined, NaN, <= 0)', () => {
    expect(evaluatePromoteBlock(input({ accountEquity: undefined }))).toBe('equity_unknown');
    expect(evaluatePromoteBlock(input({ accountEquity: NaN }))).toBe('equity_unknown');
    expect(evaluatePromoteBlock(input({ accountEquity: 0 }))).toBe('equity_unknown');
  });
  it('blocks when the live total exceeds capacity (the $367 vs $27.42 hole)', () => {
    const big = suggest({ max_total_usd: 367 });
    expect(evaluatePromoteBlock(input({ suggest: big }))).toBe('exceeds_equity');
  });
  it('allows a total within capacity and within the tolerance', () => {
    expect(evaluatePromoteBlock(input())).toBeNull();
    const edge = 27.42 * 1 * 0.8 * EQUITY_CAPACITY_TOLERANCE;
    expect(evaluatePromoteBlock(input({ suggest: suggest({ max_total_usd: edge - 0.01 }) }))).toBeNull();
    expect(evaluatePromoteBlock(input({ suggest: suggest({ max_total_usd: edge + 0.01 }) }))).toBe('exceeds_equity');
  });
  it('keeps earlier reasons first (suggest_pending before equity checks)', () => {
    expect(evaluatePromoteBlock(input({ suggest: null, accountEquity: undefined }))).toBe('suggest_pending');
  });
});

describe('computePlanBalance', () => {
  it('computes long/short and imbalance from opens + keep', () => {
    const b = computePlanBalance({
      opens: [{ pair: 'A', side: 'buy', notionalUsd: 30, reason: '' }, { pair: 'B', side: 'sell', notionalUsd: 20, reason: '' }],
      keep: [{ pair: 'C', dir: 1, notionalUsd: 10 }, { pair: 'D', dir: -1, notionalUsd: 10 }],
    });
    expect(b).toMatchObject({ longUsd: 40, shortUsd: 30, gross: 70 });
    expect(b.imbalancePct).toBeCloseTo(25);
    expect(b.imbalancePct > PROMOTE_MAX_IMBALANCE_PCT).toBe(true);
  });
  it('is 0 for an empty plan and for a balanced one', () => {
    expect(computePlanBalance({ opens: [], keep: [] }).imbalancePct).toBe(0);
    expect(computePlanBalance({ opens: [{ pair: 'A', side: 'buy', notionalUsd: 5, reason: '' }, { pair: 'B', side: 'sell', notionalUsd: 5, reason: '' }], keep: [] }).imbalancePct).toBe(0);
  });
});

describe('promote flow (createXsecJob mocked)', () => {
  const NOW = new Date('2026-10-04T12:00:00Z').getTime();
  const a = { k: 10, lookback: 10, maxPosUsd: 3.29, maxTotalUsd: 65.8 };
  const dryJob = (ageMs: number): XSecJob => ({
    id: 7, mode: 'rebalance-dry', status: 'done', output: '', created_by: 'u',
    args: ['-k', '10', '-max-pos-usd', '3.29', '-lookback', '10', '-max-total-usd', '65.8'],
    started_at: new Date(NOW - ageMs - 3000).toISOString(),
    finished_at: new Date(NOW - ageMs).toISOString(),
  });
  beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(NOW); });
  afterEach(() => vi.useRealTimers());

  it('happy path: dry then live with identical args', async () => {
    const create = vi.fn().mockResolvedValueOnce({ job_id: 7 }).mockResolvedValueOnce({ job_id: 8 });
    await requestDryRun(a, create);
    const live = await requestLive(dryJob(30_000), a, create);
    expect(live).toEqual({ job_id: 8 });
    expect(create).toHaveBeenCalledTimes(2);
    const [dryReq] = create.mock.calls[0];
    const [liveReq] = create.mock.calls[1];
    expect(dryReq).toEqual({ mode: 'rebalance-dry', k: 10, max_pos_usd: 3.29, lookback: 10, max_total_usd: 65.8 });
    expect({ ...liveReq, mode: 'x' }).toEqual({ ...dryReq, mode: 'x' });
    expect(liveReq.mode).toBe('rebalance-live');
  });

  it('stale dry (>10 min) disables confirm and creates no live job', async () => {
    const create = vi.fn();
    expect(canConfirmLive(dryJob(11 * 60_000), a)).toBe(false);
    expect(await requestLive(dryJob(11 * 60_000), a, create)).toBeNull();
    expect(create).not.toHaveBeenCalled();
  });

  it('confirm is disabled with no dry, or a dry for different args', async () => {
    const create = vi.fn();
    expect(canConfirmLive(null, a)).toBe(false);
    expect(canConfirmLive(dryJob(1000), { ...a, maxTotalUsd: 70 })).toBe(false);
    expect(await requestLive(undefined, a, create)).toBeNull();
    expect(create).not.toHaveBeenCalled();
  });

  it('cancel after preview creates no further job (only the dry-run exists)', async () => {
    const create = vi.fn().mockResolvedValue({ job_id: 7 });
    await requestDryRun(a, create);
    // operator opens the confirm step then cancels: nothing is called.
    expect(canConfirmLive(dryJob(1000), a)).toBe(true);
    expect(create).toHaveBeenCalledTimes(1);
    expect(create.mock.calls[0][0].mode).toBe('rebalance-dry');
  });
});
