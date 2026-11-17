import { describe, expect, it, vi } from 'vitest';

vi.mock('@/lib/api', () => {
  class ApiError extends Error {
    constructor(public status: number, public body: unknown) { super(`HTTP ${status}`); }
  }
  return { ApiError, apiFetch: vi.fn() };
});

import { ApiError } from '@/lib/api';
import {
  EXIT_REASONS, REASON_CODES, canDecide, canOperate, exitReasonKey, isDegraded, launchErrorInfo,
  launchErrorKey, reasonKey, shouldRefreshQueue, sortByExpiry, timeRemaining, verdictTone,
} from './launch-api';

const NOW = Date.parse('2026-11-02T10:00:00Z');

describe('timeRemaining', () => {
  it('formats m:ss', () => {
    const r = timeRemaining('2026-11-02T10:05:07Z', NOW);
    expect(r).toEqual({ expired: false, totalSeconds: 307, label: '5:07' });
  });
  it('formats h:mm:ss', () => {
    expect(timeRemaining('2026-11-02T11:01:01Z', NOW).label).toBe('1:01:01');
  });
  it('is expired at and after the deadline or when missing', () => {
    expect(timeRemaining('2026-11-02T10:00:00Z', NOW).expired).toBe(true);
    expect(timeRemaining('2026-11-02T09:00:00Z', NOW).label).toBe('0:00');
    expect(timeRemaining(null, NOW).expired).toBe(true);
    expect(timeRemaining('garbage', NOW).expired).toBe(true);
  });
});

describe('verdictTone', () => {
  it('maps verdicts', () => {
    expect(verdictTone('EDGE')).toBe('ok');
    expect(verdictTone('NO_EDGE')).toBe('real');
    expect(verdictTone('INSUFFICIENT_DATA')).toBe('micro');
    expect(verdictTone(undefined)).toBe('neutral');
  });
});

describe('reason keys', () => {
  it('maps every known reason and exit reason', () => {
    for (const c of REASON_CODES) expect(reasonKey(c)).toBe(`reason_${c}`);
    expect(exitReasonKey('TAKE_PROFIT')).toBe('exit_TAKE_PROFIT');
    expect(exitReasonKey('EMERGENCY_EXIT:liquidity_drop')).toBe('exit_EMERGENCY_EXIT_liquidity_drop');
    expect(EXIT_REASONS).toHaveLength(8);
  });
  it('returns null for unknown or empty', () => {
    expect(reasonKey('WHATEVER')).toBeNull();
    expect(reasonKey(null)).toBeNull();
    expect(exitReasonKey('X')).toBeNull();
  });
});

describe('canDecide', () => {
  const base = { status: 'PENDING_APPROVAL', expires_at: '2026-11-02T10:05:00Z', decision: null };
  it('allows a live pending opportunity for an operator', () => {
    expect(canDecide(base, true, NOW)).toBe(true);
  });
  it('denies without permission, after expiry, when decided or not pending', () => {
    expect(canDecide(base, false, NOW)).toBe(false);
    expect(canDecide(base, true, Date.parse('2026-11-02T10:05:00Z'))).toBe(false);
    expect(canDecide({ ...base, status: 'EXPIRED' }, true, NOW)).toBe(false);
    expect(canDecide({ ...base, decision: { action: 'approve', category: 'team', comment: null, actor: 'a', created_at: '' } }, true, NOW)).toBe(false);
  });
});

describe('canOperate', () => {
  it('prefers the backend flag, else level >= 3', () => {
    expect(canOperate(1, true)).toBe(true);
    expect(canOperate(4, false)).toBe(false);
    expect(canOperate(3)).toBe(true);
    expect(canOperate(2)).toBe(false);
    expect(canOperate(undefined)).toBe(false);
  });
});

describe('errors', () => {
  it('reads the error envelope', () => {
    const e = new ApiError(409, { success: false, error: { code: 'OPPORTUNITY_EXPIRED', message: 'expired' } });
    const info = launchErrorInfo(e);
    expect(info).toEqual({ status: 409, code: 'OPPORTUNITY_EXPIRED', message: 'expired' });
    expect(launchErrorKey(info)).toBe('err_expired');
    expect(shouldRefreshQueue(info)).toBe(true);
  });
  it('maps other codes and statuses', () => {
    expect(launchErrorKey(launchErrorInfo(new ApiError(409, { error: { code: 'ALREADY_DECIDED' } })))).toBe('err_already_decided');
    expect(launchErrorKey(launchErrorInfo(new ApiError(403, {})))).toBe('err_forbidden');
    expect(launchErrorKey(launchErrorInfo(new ApiError(503, 'x')))).toBe('err_unavailable');
    expect(launchErrorKey(launchErrorInfo(new Error('boom')))).toBe('err_generic');
    expect(shouldRefreshQueue(launchErrorInfo(new ApiError(400, { error: { code: 'INVALID_CATEGORY' } })))).toBe(false);
  });
});

describe('misc helpers', () => {
  it('sorts by expiry ascending, nulls last', () => {
    const out = sortByExpiry([
      { id: 1, expires_at: '2026-11-02T10:09:00Z' },
      { id: 2, expires_at: null },
      { id: 3, expires_at: '2026-11-02T10:01:00Z' },
    ]);
    expect(out.map((x) => x.id)).toEqual([3, 1, 2]);
  });
  it('detects degraded service', () => {
    expect(isDegraded({ service: 'up' })).toBe(false);
    expect(isDegraded({ service: 'down' })).toBe(true);
    expect(isDegraded({ service: 'never_started' })).toBe(true);
    expect(isDegraded(undefined)).toBe(false);
  });
});

describe('screenKey', () => {
  it('maps every backend screening result, including BLOCKED', async () => {
    const { screenKey } = await import('./launch-api');
    expect(screenKey('PASSED')).toBe('screen_PASSED');
    expect(screenKey('REJECTED')).toBe('screen_REJECTED');
    expect(screenKey('BLOCKED')).toBe('screen_BLOCKED');
    expect(screenKey('SOMETHING_NEW')).toBeNull();
  });
});
