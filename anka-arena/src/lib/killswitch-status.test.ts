import { describe, it, expect, vi } from 'vitest';
import { isStatusForbidden } from '@/lib/killswitch-status';

// api.ts pulls the next-intl routing at import time; mock it with a minimal ApiError.
vi.mock('@/lib/api', () => {
  class ApiError extends Error {
    constructor(public status: number, public body: unknown) { super(`HTTP ${status}`); }
  }
  return { ApiError, apiFetch: vi.fn() };
});
import { ApiError } from '@/lib/api';


describe('isStatusForbidden', () => {
  it('is true for 401 and 403 only', () => {
    expect(isStatusForbidden(new ApiError(401, {}))).toBe(true);
    expect(isStatusForbidden(new ApiError(403, {}))).toBe(true);
    expect(isStatusForbidden(new ApiError(500, {}))).toBe(false);
    expect(isStatusForbidden(new Error('x'))).toBe(false);
  });
});
