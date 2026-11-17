import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { TEN_MINUTES_MS, findMatchingDryRun } from './xsec-live-gate';
import type { XSecJob } from './xsec-api';

// api.ts pulls the next-intl routing at import time; mock it with a minimal ApiError.
vi.mock('@/lib/api', () => {
  class ApiError extends Error {
    constructor(public status: number, public body: unknown) { super(`HTTP ${status}`); }
  }
  return { ApiError, apiFetch: vi.fn() };
});
vi.mock('@/lib/motor-api', () => ({ getXSecStatus: vi.fn() }));

const NOW = new Date('2026-10-04T12:00:00Z').getTime();

function dry(over: Partial<XSecJob> = {}, ageMs = 60_000): XSecJob {
  return {
    id: 1,
    mode: 'rebalance-dry',
    args: ['-k', '10', '-max-pos-usd', '2.5', '-lookback', '10', '-max-total-usd', '50'],
    status: 'done',
    output: '',
    created_by: 'u',
    started_at: new Date(NOW - ageMs - 5000).toISOString(),
    finished_at: new Date(NOW - ageMs).toISOString(),
    ...over,
  };
}

describe('findMatchingDryRun', () => {
  beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(NOW); });
  afterEach(() => vi.useRealTimers());

  it('exports a 10 minute window', () => {
    expect(TEN_MINUTES_MS).toBe(600_000);
  });

  it('matches a fresh done dry-run with identical args', () => {
    const j = dry();
    expect(findMatchingDryRun([j], 10, 2.5, 10, 50)).toBe(j);
  });

  it.each([
    ['k differs', () => findMatchingDryRun([dry()], 11, 2.5, 10, 50)],
    ['max_pos differs', () => findMatchingDryRun([dry()], 10, 2.6, 10, 50)],
    ['lookback differs', () => findMatchingDryRun([dry()], 10, 2.5, 14, 50)],
    ['max_total differs', () => findMatchingDryRun([dry()], 10, 2.5, 10, 60)],
    ['older than 10 min', () => findMatchingDryRun([dry({}, TEN_MINUTES_MS + 1000)], 10, 2.5, 10, 50)],
    ['still running', () => findMatchingDryRun([dry({ status: 'running', finished_at: null })], 10, 2.5, 10, 50)],
    ['errored', () => findMatchingDryRun([dry({ status: 'error' })], 10, 2.5, 10, 50)],
    ['wrong mode', () => findMatchingDryRun([dry({ mode: 'rebalance-live' })], 10, 2.5, 10, 50)],
    ['bad finished_at', () => findMatchingDryRun([dry({ finished_at: 'nope' })], 10, 2.5, 10, 50)],
  ])('returns null when %s', (_n, fn) => {
    expect(fn()).toBeNull();
  });

  it('skips non-matching jobs and returns the matching one', () => {
    const other = dry({ id: 2, args: ['-k', '5', '-max-pos-usd', '1', '-lookback', '10', '-max-total-usd', '10'] });
    const good = dry({ id: 3 });
    expect(findMatchingDryRun([other, good], 10, 2.5, 10, 50)?.id).toBe(3);
  });
});
