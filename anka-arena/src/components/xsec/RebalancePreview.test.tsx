import { describe, expect, it, vi } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import type { XSecJob } from '@/lib/xsec-api';

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

import { RebalancePreview } from './RebalancePreview';

// Only KEEP legs are needed: they feed the long/short balance of the plan.
const dryJob = (keepLines: string[]): XSecJob => ({
  id: 5, mode: 'rebalance-dry', status: 'done', created_by: 'u', args: [],
  started_at: '2026-10-04T11:00:00Z', finished_at: '2026-10-04T11:01:00Z',
  output: ['Target source: x (4 legs) | actual book: 0 positions | per-leg $3.00', '--- DIFF ---', ...keepLines].join('\n'),
});

const continueDisabled = (html: string) => /<button[^>]*disabled=""[^>]*>btn_preview_continue/.test(html);

describe('RebalancePreview continueDisabledReason', () => {
  const noop = () => {};
  const job = dryJob([]);

  it('is enabled by default and disabled with the reason shown when provided', () => {
    const ok = renderToStaticMarkup(<RebalancePreview job={job} onCancel={noop} onContinue={noop} />);
    expect(continueDisabled(ok)).toBe(false);
    expect(ok).not.toContain('preview-continue-disabled-reason');

    const blocked = renderToStaticMarkup(
      <RebalancePreview job={job} onCancel={noop} onContinue={noop} continueDisabledReason="imbalanced 40%" />,
    );
    expect(continueDisabled(blocked)).toBe(true);
    expect(blocked).toContain('imbalanced 40%');
  });
});
