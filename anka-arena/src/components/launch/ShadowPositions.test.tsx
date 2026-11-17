import { describe, expect, it, vi } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import type { LaunchPosition } from '@/lib/launch-api';

let positions: LaunchPosition[] = [];

vi.mock('next-intl', () => ({
  useTranslations: () => (key: string, vals?: Record<string, unknown>) =>
    vals ? `${key}|${Object.values(vals).join(',')}` : key,
}));
vi.mock('swr', () => ({
  default: (key: string) => ({ data: key.startsWith('launch:positions') ? { items: positions } : undefined, error: undefined, isLoading: false }),
}));
vi.mock('@/lib/api', () => {
  class ApiError extends Error {
    constructor(public status: number, public body: unknown) { super(`HTTP ${status}`); }
  }
  return { ApiError, apiFetch: vi.fn() };
});

import { ShadowPositions, totalCosts } from './ShadowPositions';

const costs = { venue_fees_usd: 0.1, network_fees_usd: 0.05, rent_usd: 0.04, slippage_usd: 0.03, token_tax_usd: 0, dropped_txs: 0 };
const pos = (id: number, entry_channel: string): LaunchPosition => ({
  id, opportunity_id: id, symbol: 'MANDO', mint: 'Mint', entry_channel, state: 'OPEN', invested_usd: 5,
  gross_pnl_usd: null, net_pnl_usd: null, costs, exit_reason: null, size_reason: null, fraud_flag: false,
  opened_at: '2026-11-02T07:08:38Z', closed_at: null,
} as unknown as LaunchPosition);

describe('ShadowPositions', () => {
  it('labels manual and counterfactual entries and keeps the counterfactual note', () => {
    positions = [pos(1, 'manual'), pos(2, 'counterfactual')];
    const html = renderToStaticMarkup(<ShadowPositions />);
    expect(html).toContain('cf_note');
    expect(html).toContain('channel_manual');
    expect(html).toContain('channel_counterfactual');
    expect(html).toContain('col_trades');
  });

  it('sums every cost component', () => {
    expect(totalCosts(costs)).toBeCloseTo(0.22, 10);
  });

  it('shows the empty state without positions', () => {
    positions = [];
    expect(renderToStaticMarkup(<ShadowPositions />)).toContain('empty');
  });
});
