import { describe, it, expect, vi } from 'vitest';
import { isServiceUnavailable } from '@/lib/motor-availability';

// api.ts pulls the next-intl routing at import time; mock it with a minimal ApiError.
vi.mock('@/lib/api', () => {
  class ApiError extends Error {
    constructor(public status: number, public body: unknown) { super(`HTTP ${status}`); }
  }
  return { ApiError, apiFetch: vi.fn() };
});
import { ApiError } from '@/lib/api';


describe('isServiceUnavailable', () => {
  it('is true only for ApiError 503', () => {
    expect(isServiceUnavailable(new ApiError(503, {}))).toBe(true);
    expect(isServiceUnavailable(new ApiError(500, {}))).toBe(false);
    expect(isServiceUnavailable(new ApiError(403, {}))).toBe(false);
  });
  it('is false for non-ApiError values', () => {
    expect(isServiceUnavailable(new Error('503'))).toBe(false);
    expect(isServiceUnavailable(undefined)).toBe(false);
    expect(isServiceUnavailable(null)).toBe(false);
  });
});
