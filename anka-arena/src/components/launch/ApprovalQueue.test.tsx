import { beforeEach, describe, expect, it, vi } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import type { Opportunity } from '@/lib/launch-api';

// Node environment without DOM: the queue is rendered to static markup and the
// permission hook is mocked per case (level 1 read-only vs level 3 operator).
let perm = { ready: true, level: 1, allowed: false };
let approvals: { can_decide: boolean; items: Opportunity[] } | undefined;

vi.mock('next-intl', () => ({
  useTranslations: () => (key: string, vals?: Record<string, unknown>) =>
    vals ? `${key}|${Object.values(vals).join(',')}` : key,
}));
vi.mock('swr', () => ({
  default: (key: string) => ({ data: key === 'launch:approvals' ? approvals : undefined, error: undefined, isLoading: false, mutate: vi.fn() }),
  useSWRConfig: () => ({ mutate: vi.fn() }),
}));
vi.mock('@/lib/api', () => {
  class ApiError extends Error {
    constructor(public status: number, public body: unknown) { super(`HTTP ${status}`); }
  }
  return { ApiError, apiFetch: vi.fn() };
});
vi.mock('@/components/launch/shared', async (orig) => ({
  ...(await orig<typeof import('@/components/launch/shared')>()),
  useOperatePermission: () => perm,
  useNow: () => Date.now(),
}));

import { ApprovalQueue } from './ApprovalQueue';

const opp = (over: Partial<Opportunity> = {}): Opportunity => ({
  id: 7, mint: 'MintAAAAAAAA', symbol: 'MANDO', pool: 'Pool', dex: 'pumpswap', source_kind: 'amm', graduated: true,
  screen_result: 'PASSED', reject_reason: null, score: 82, band: 'MANUAL_APPROVAL', status: 'PENDING_APPROVAL',
  expires_at: new Date(Date.now() + 120_000).toISOString(), created_at: new Date().toISOString(),
  ...over,
} as Opportunity);

const buttons = (html: string) =>
  [...html.matchAll(/<button[^>]*?(disabled="")?[^>]*>([^<]*)<\/button>/g)].map((m) => ({ label: m[2], disabled: m[0].includes('disabled=""') }));

describe('ApprovalQueue', () => {
  beforeEach(() => {
    approvals = { can_decide: true, items: [opp()] };
  });

  it('level 1 sees the queue read-only with the reason', () => {
    perm = { ready: true, level: 1, allowed: false };
    const html = renderToStaticMarkup(<ApprovalQueue />);
    expect(html).toContain('MANDO');
    const b = buttons(html).filter((x) => x.label === 'approve' || x.label === 'reject');
    expect(b).toHaveLength(2);
    expect(b.every((x) => x.disabled)).toBe(true);
    expect(html).toContain('appr_readonly|no_permission');
  });

  it('level 3 with backend permission can decide', () => {
    perm = { ready: true, level: 3, allowed: true };
    const html = renderToStaticMarkup(<ApprovalQueue />);
    const b = buttons(html).filter((x) => x.label === 'approve' || x.label === 'reject');
    expect(b.every((x) => !x.disabled)).toBe(true);
    expect(html).not.toContain('appr_readonly');
  });

  it('level 3 stays read-only when the backend denies the feature', () => {
    perm = { ready: true, level: 3, allowed: true };
    approvals = { can_decide: false, items: [opp()] };
    const html = renderToStaticMarkup(<ApprovalQueue />);
    expect(buttons(html).filter((x) => x.label === 'approve').every((x) => x.disabled)).toBe(true);
  });

  it('expired items cannot be decided', () => {
    perm = { ready: true, level: 3, allowed: true };
    approvals = { can_decide: true, items: [opp({ expires_at: new Date(Date.now() - 1000).toISOString() })] };
    const html = renderToStaticMarkup(<ApprovalQueue />);
    expect(buttons(html).filter((x) => x.label === 'approve').every((x) => x.disabled)).toBe(true);
  });

  it('empty queue shows the empty state', () => {
    approvals = { can_decide: true, items: [] };
    expect(renderToStaticMarkup(<ApprovalQueue />)).toContain('appr_empty');
  });
});
