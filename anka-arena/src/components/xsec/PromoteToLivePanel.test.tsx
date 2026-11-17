import { beforeEach, describe, expect, it, vi } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import type { SuggestParams, XSecJob } from '@/lib/xsec-api';

// The node vitest environment has no DOM, so the panel is rendered to static
// markup (initial "idle" state). Interactive transitions are covered against
// the same createXsecJob-driven helpers in lib/xsec-promote.test.ts.
const createXsecJob = vi.fn();
let suggestData: SuggestParams | undefined;

vi.mock('@/lib/api', () => {
  class ApiError extends Error {
    constructor(public status: number, public body: unknown) { super(`HTTP ${status}`); }
  }
  return { ApiError, apiFetch: vi.fn() };
});
vi.mock('@/lib/motor-api', () => ({ getXSecStatus: vi.fn() }));
vi.mock('next-intl', () => ({
  useTranslations: () => (key: string, vals?: Record<string, unknown>) =>
    vals ? `${key}|${Object.values(vals).join(',')}` : key,
}));
vi.mock('swr', () => ({
  default: (key: string | null) => ({
    data: key && key.startsWith('xsec:promote-suggest') ? suggestData : undefined,
    error: undefined,
  }),
}));
vi.mock('@/lib/xsec-api', async (orig) => ({
  ...(await orig<typeof import('@/lib/xsec-api')>()),
  createXsecJob: (...a: unknown[]) => createXsecJob(...a),
}));

import { PromoteToLivePanel } from './PromoteToLivePanel';

const suggest = (over: Partial<SuggestParams> = {}): SuggestParams => ({
  capital: 27.42, leverage: 1, utilization: 0.8, universe_size: 40, min_turnover_usd: 3e6,
  k: 10, lookback: 14, rebal: 5, band: 0, vol_weights: true, legs: 20, per_leg_usd: 3,
  max_pos_usd: 10.5, max_total_usd: 21, gross_exposure: 21, margin_used: 21,
  feasible: true, warnings: [], ...over,
});

function output(verdict: string, liq: string, w3 = '  W3:  +4.0%  (Sharpe 0.90, DD -6%, univ 38)'): string {
  return [
    '# 4h, 38 pairs, 2.1 years',
    '  Final equity:   $40.00 (start $27.42)',
    '  Total return:   +45.9%', '  CAGR:           +20.0%/yr', '  Sharpe (ann.):  1.20',
    '  Max drawdown:   -12.0%', liq,
    '  Rebalances:     120   avg turnover 31.0%/rebal',
    '  W1:  +12.0%  (Sharpe 1.10, DD -8%, univ 38)', w3,
    `# VERDICT: ${verdict} — detail`,
  ].join('\n');
}

const job = (out: string): XSecJob => ({
  id: 91, mode: 'backtest', status: 'done', output: out, created_by: 'u',
  args: ['-lb', '10', '-k', '10', '-rebal', '7', '-band', '2', '-capital', '27.42', '-vol-weights=true', '-stop-short', '0.15'],
  started_at: '2026-10-04T11:00:00Z', finished_at: '2026-10-04T11:01:00Z',
});

function render(j: XSecJob, over: { canWrite?: boolean; anyRunning?: boolean; accountEquity?: number | null } = {}): string {
  return renderToStaticMarkup(
    <PromoteToLivePanel job={j} canWrite={over.canWrite ?? true} anyRunning={over.anyRunning ?? false}
      accountEquity={over.accountEquity === null ? undefined : (over.accountEquity ?? 27.42)}
      onJobsChanged={() => {}} onConflict={() => {}} />,
  );
}

const previewDisabled = (html: string) => /data-testid="promote-preview-btn"[^>]*disabled=""/.test(html);

describe('PromoteToLivePanel (initial render)', () => {
  beforeEach(() => { createXsecJob.mockReset(); suggestData = suggest(); });

  it('PROMISING + level 2 + feasible: preview enabled, live values and not-applied list shown', () => {
    const html = render(job(output('PROMISING', '  Liquidated:     no')));
    expect(previewDisabled(html)).toBe(false);
    expect(html).not.toContain('promote-block-reason');
    expect(html).toContain('k=10');
    expect(html).toContain('promote_na_rebal');
    expect(html).toContain('promote_na_stop_short');
    expect(html).toContain('promote_na_vol_weights');
  });

  it('MARGINAL: disabled with the verdict reason', () => {
    const html = render(job(output('MARGINAL', '  Liquidated:     no')));
    expect(previewDisabled(html)).toBe(true);
    expect(html).toContain('promote_block_verdict');
  });

  it('liquidated window: disabled with the liquidation reason', () => {
    const html = render(job(output('PROMISING', '  Liquidated:     no',
      '  W3:  -100.0%  (Sharpe -1.62, DD -100%, univ 38) *NEG *LIQUIDATED')));
    expect(previewDisabled(html)).toBe(true);
    expect(html).toContain('promote_block_liquidated');
  });

  it('liquidated run: disabled', () => {
    const html = render(job(output('PROMISING', '  Liquidated:     YES (2025-03-14)')));
    expect(previewDisabled(html)).toBe(true);
    expect(html).toContain('promote_block_liquidated');
  });

  it('level < 2: disabled with the level reason', () => {
    const html = render(job(output('PROMISING', '  Liquidated:     no')), { canWrite: false });
    expect(previewDisabled(html)).toBe(true);
    expect(html).toContain('promote_block_level');
  });

  it('another job running: disabled', () => {
    const html = render(job(output('PROMISING', '  Liquidated:     no')), { anyRunning: true });
    expect(previewDisabled(html)).toBe(true);
    expect(html).toContain('promote_block_running');
  });

  it('suggested k below backtest k: disabled showing both values', () => {
    suggestData = suggest({ k: 8 });
    const html = render(job(output('PROMISING', '  Liquidated:     no')));
    expect(previewDisabled(html)).toBe(true);
    expect(html).toContain('promote_block_k_clamped|10,8');
  });

  it('infeasible sizing: disabled', () => {
    suggestData = suggest({ feasible: false });
    const html = render(job(output('PROMISING', '  Liquidated:     no')));
    expect(previewDisabled(html)).toBe(true);
    expect(html).toContain('promote_block_infeasible');
  });

  it('equity unknown: disabled with the equity_unknown reason', () => {
    const html = render(job(output('PROMISING', '  Liquidated:     no')), { accountEquity: null });
    expect(previewDisabled(html)).toBe(true);
    expect(html).toContain('promote_block_equity_unknown');
  });

  it('total above real account capacity: disabled showing the capacity numbers', () => {
    suggestData = suggest({ max_total_usd: 367 });
    const html = render(job(output('PROMISING', '  Liquidated:     no')));
    expect(previewDisabled(html)).toBe(true);
    expect(html).toContain('promote_block_exceeds_equity|$367.00,$21.94,$27.42,1,0.8');
  });

  it('never creates a job just by rendering', () => {
    render(job(output('PROMISING', '  Liquidated:     no')));
    expect(createXsecJob).not.toHaveBeenCalled();
  });
});
