import { describe, expect, it, vi } from 'vitest';
// xsec-api pulls the next-intl routing at import time; stub the api module.
vi.mock('@/lib/api', () => ({ ApiError: class extends Error {}, apiFetch: vi.fn() }));

import { isLegBelowMin, XSEC_MIN_LEG_USD } from './xsec-live-gate';

describe('isLegBelowMin', () => {
  it('uses a $10 default minimum', () => {
    expect(XSEC_MIN_LEG_USD).toBe(10);
  });
  it.each([
    [1.37, true],
    [9.99, true],
    [10, false],
    [12.5, false],
    [0, false],
    [NaN, false],
    [-3, false],
  ])('maxPos %s -> %s', (v, want) => {
    expect(isLegBelowMin(v)).toBe(want);
  });
  it('honors a custom minimum', () => {
    expect(isLegBelowMin(15, 20)).toBe(true);
  });
});
