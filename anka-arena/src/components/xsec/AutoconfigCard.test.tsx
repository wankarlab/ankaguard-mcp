import { describe, expect, it, vi } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import type { AutoconfigResult } from '@/lib/xsec-api';

// Echo key + params so assertions do not depend on locale files.
vi.mock('next-intl', () => ({
  useTranslations: () => (key: string, vals?: Record<string, unknown>) =>
    vals ? `${key}|${Object.values(vals).join(',')}` : key,
}));

import { AutoconfigCard } from './AutoconfigCard';

const base: AutoconfigResult = {
  feasible: true, reason: '', equity: 27.42, capital: 27.42, utilization: 0.8, leverage: 1,
  k: 4, lookback: 14, rank_buffer: 2, per_leg_usd: 2.74, max_pos_usd: 2.74, max_total_usd: 21.94,
  longs: ['A-USDT', 'B-USDT'], shorts: ['C-USDT', 'D-USDT'],
  tried: [{ k: 10, per_leg_usd: 1.1, longs: 6, shorts: 10 }, { k: 4, per_leg_usd: 2.74, longs: 4, shorts: 4 }],
  min_k: 6, best_balanced_k: 4, required_equity_min_k_usd: 150, required_equity_target_k_usd: 300,
};

describe('AutoconfigCard', () => {
  it('feasible: summary, pair lists and the tried table', () => {
    const html = renderToStaticMarkup(<AutoconfigCard result={base} />);
    expect(html).toContain('autoconfig_summary|4,$2.74,$21.94,$27.42,0.8');
    expect(html).toContain('A-USDT, B-USDT');
    expect(html).toContain('C-USDT, D-USDT');
    expect(html).toContain('data-testid="autoconfig-tried"');
    expect(html).toContain('6/10');
  });
  it('infeasible: translated reason, no summary', () => {
    const html = renderToStaticMarkup(
      <AutoconfigCard result={{ ...base, feasible: false, reason: 'no_balanced_k', longs: [], shorts: [] }} />,
    );
    expect(html).toContain('autoconfig_reason_no_balanced_k');
    expect(html).not.toContain('autoconfig_summary');
  });
  it('unknown reason falls back to the raw code', () => {
    const html = renderToStaticMarkup(<AutoconfigCard result={{ ...base, feasible: false, reason: 'weird', tried: [] }} />);
    expect(html).toContain('weird');
    expect(html).not.toContain('autoconfig-tried');
  });
  it('below_validated_k: warning with required equity, tried table kept, no summary', () => {
    const html = renderToStaticMarkup(
      <AutoconfigCard
        result={{
          ...base, feasible: false, reason: 'below_validated_k', k: 0, per_leg_usd: 0, max_pos_usd: 0, max_total_usd: 0,
          longs: [], shorts: [], best_balanced_k: 1, min_k: 6,
          tried: [{ k: 1, per_leg_usd: 10.97, longs: 1, shorts: 1 }],
        }}
      />,
    );
    expect(html).toContain('data-testid="autoconfig-below-min-k"');
    expect(html).toContain('autoconfig_below_validated_k|$27.42,1,6,$150.00,$300.00,12');
    expect(html).not.toContain('autoconfig_summary');
    expect(html).not.toContain('autoconfig-infeasible');
    expect(html).toContain('data-testid="autoconfig-tried"');
  });
});
