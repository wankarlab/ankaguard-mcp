import { describe, expect, it, vi } from 'vitest';
import { parseAutoconfigResult, parseBacktestReport, parseRebalancePlan } from './xsec-api';

// api.ts pulls the next-intl routing at import time; mock it with a minimal ApiError.
vi.mock('@/lib/api', () => {
  class ApiError extends Error {
    constructor(public status: number, public body: unknown) { super(`HTTP ${status}`); }
  }
  return { ApiError, apiFetch: vi.fn() };
});
vi.mock('@/lib/motor-api', () => ({ getXSecStatus: vi.fn() }));

const BASE = [
  '# 4h, 38 pairs, 2.1 years',
  '  Final equity:   $27.42 (start $27.42)',
  '  Total return:   +0.0%',
  '  CAGR:           +0.0%/yr',
  '  Sharpe (ann.):  0.00',
  '  Max drawdown:   -23.1%',
  '__LIQ__',
  '  Rebalances:     120   avg turnover 31.0%/rebal',
  '  Funding P&L:    paid $1.00, received $2.00, net $+1.00',
  '  Universe:       avg 38.0 eligible/rebalance',
  '  W1:  +12.0%  (Sharpe 1.10, DD -8%, univ 38)',
  '  W2:  +3.0%  (Sharpe 0.40, DD -5%, univ 38)',
  '__W3__',
  '# VERDICT: MARGINAL — one window negative',
];

function sample(liq: string | null, w3: string): string {
  return BASE.filter((l) => l !== '__LIQ__' || liq !== null)
    .map((l) => (l === '__LIQ__' ? liq : l === '__W3__' ? w3 : l))
    .join('\n');
}

describe('parseBacktestReport liquidation', () => {
  it('old output without a Liquidated line parses as unknown (null)', () => {
    const r = parseBacktestReport(sample(null, '  W3:  -5.0%  (Sharpe -0.20, DD -9%, univ 38) *NEG'))!;
    expect(r.liquidated).toBeNull();
    expect(r.liquidatedAt).toBeNull();
    expect(r.windows).toHaveLength(3);
    expect(r.windows.every((w) => w.liquidated === false)).toBe(true);
    expect(r.verdict).toBe('MARGINAL');
  });

  it('Liquidated: no -> false', () => {
    const r = parseBacktestReport(sample('  Liquidated:     no', '  W3:  -5.0%  (Sharpe -0.20, DD -9%, univ 38) *NEG'))!;
    expect(r.liquidated).toBe(false);
    expect(r.liquidatedAt).toBeNull();
  });

  it('Liquidated: YES (date) -> true with the date', () => {
    const r = parseBacktestReport(sample('  Liquidated:     YES (2025-03-14)', '  W3:  -100.0%  (Sharpe -1.62, DD -100%, univ 38) *NEG *LIQUIDATED'))!;
    expect(r.liquidated).toBe(true);
    expect(r.liquidatedAt).toBe('2025-03-14');
    // Max drawdown line still parsed independently of the new line.
    expect(r.maxDrawdownPct).toBeCloseTo(23.1);
  });

  it('flags only the *LIQUIDATED window, without altering its parsed numbers', () => {
    const r = parseBacktestReport(sample('  Liquidated:     no', '  W3:  -100.0%  (Sharpe -1.62, DD -100%, univ 38) *NEG *LIQUIDATED'))!;
    expect(r.windows.map((w) => w.liquidated)).toEqual([false, false, true]);
    expect(r.windows[2]).toMatchObject({ index: 3, returnPct: -100, sharpe: -1.62, maxDrawdownPct: 100, avgUniverse: 38 });
  });
});

describe('parseAutoconfigResult', () => {
  const good = {
    feasible: true, reason: '', equity: 27.42, capital: 27.42, utilization: 0.8, leverage: 1,
    k: 4, lookback: 14, rank_buffer: 2, per_leg_usd: 2.74, max_pos_usd: 2.74, max_total_usd: 21.94,
    longs: ['A-USDT', 'B-USDT'], shorts: ['C-USDT', 'D-USDT'],
    tried: [{ k: 10, per_leg_usd: 1.1, longs: 6, shorts: 10 }, { k: 4, per_leg_usd: 2.74, longs: 4, shorts: 4 }],
  };
  it('parses the line amid other output', () => {
    const r = parseAutoconfigResult(`log line\nAUTOCONFIG_RESULT ${JSON.stringify(good)}\ndone`);
    expect(r?.feasible).toBe(true);
    expect(r?.k).toBe(4);
    expect(r?.tried).toHaveLength(2);
    expect(r?.longs).toEqual(['A-USDT', 'B-USDT']);
  });
  it('uses the last matching line', () => {
    const out = `AUTOCONFIG_RESULT ${JSON.stringify({ ...good, k: 2 })}\nAUTOCONFIG_RESULT ${JSON.stringify(good)}`;
    expect(parseAutoconfigResult(out)?.k).toBe(4);
  });
  it('parses infeasible results with a reason', () => {
    const r = parseAutoconfigResult(`AUTOCONFIG_RESULT ${JSON.stringify({ feasible: false, reason: 'no_balanced_k', tried: [] })}`);
    expect(r?.feasible).toBe(false);
    expect(r?.reason).toBe('no_balanced_k');
    expect(r?.longs).toEqual([]);
  });
  it('parses the min-k fields and tolerates their absence in older outputs', () => {
    const r = parseAutoconfigResult(`AUTOCONFIG_RESULT ${JSON.stringify({ feasible: false, reason: 'below_validated_k', min_k: 6, best_balanced_k: 1, required_equity_min_k_usd: 150, required_equity_target_k_usd: 300, tried: [] })}`);
    expect(r?.reason).toBe('below_validated_k');
    expect([r?.min_k, r?.best_balanced_k, r?.required_equity_min_k_usd, r?.required_equity_target_k_usd]).toEqual([6, 1, 150, 300]);
    const old = parseAutoconfigResult(`AUTOCONFIG_RESULT ${JSON.stringify(good)}`);
    expect([old?.min_k, old?.best_balanced_k, old?.required_equity_min_k_usd]).toEqual([0, 0, 0]);
  });
  it('returns null for missing, malformed or wrong-shape lines', () => {
    expect(parseAutoconfigResult('')).toBeNull();
    expect(parseAutoconfigResult('nothing here')).toBeNull();
    expect(parseAutoconfigResult('AUTOCONFIG_RESULT {not json')).toBeNull();
    expect(parseAutoconfigResult('AUTOCONFIG_RESULT {"k":3}')).toBeNull();
  });
});

describe('parseRebalancePlan safety problems', () => {
  it('treats any listed problem as not ok even with a check mark', () => {
    const out = ['--- DIFF ---', 'Target source: db (4 legs) | actual book: 0 positions | per-leg $2.50', 'SAFETY: ✅', '   - imbalance 40.0% > max 15.0%'].join('\n');
    const p = parseRebalancePlan(out);
    expect(p?.safetyOk).toBe(false);
    expect(p?.safetyProblems).toEqual(['imbalance 40.0% > max 15.0%']);
  });
});
