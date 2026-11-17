import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('@/lib/api', () => {
  class ApiError extends Error {
    constructor(public status: number, public body: unknown) { super(`HTTP ${status}`); }
  }
  return { ApiError, apiFetch: vi.fn(), api: { get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() } };
});
const stored: string[] = [];
vi.mock('@/lib/auth', async (orig) => ({
  ...(await orig<typeof import('@/lib/auth')>()),
  setStoredToken: (t: string) => { stored.push(t); },
}));

import { ApiError } from '@/lib/api';
import {
  LocalLoginError,
  blockedReason,
  describeApiError,
  expiresSoon,
  hasNoLicense,
  formatWait,
  isProLicense,
  isTokenActive,
  licenseRejectReason,
  liveModeOf,
  liveTokenBlocked,
  loginLocal,
  parseRetryAfter,
  reduceRevoke,
  validateTokenForm,
  type AgentToken,
  type LicenseStatus,
} from './product-api';

const lic = (over: Partial<LicenseStatus> = {}): LicenseStatus => ({
  edition: 'pro', state: 'valid', holder: 'ACME', license_id: 'L1', expires_at: '2027-01-01T00:00:00Z',
  days_remaining: 100, grace_days_remaining: 0, warning: null, ...over,
});

function jwt(payload: object): string {
  const b64 = (o: object) => btoa(JSON.stringify(o)).replace(/=+$/, '');
  return `${b64({ alg: 'HS256' })}.${b64(payload)}.sig`;
}

describe('login', () => {
  afterEach(() => { vi.unstubAllGlobals(); stored.length = 0; });

  it('stores the session token where the console reads it', async () => {
    const token = jwt({ sub: 'operator', user_level: 4, token_use: 'operator_session', exp: 4102444800 });
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ token, token_type: 'Bearer', expires_at: 'x' }), { status: 200 })));
    const claims = await loginLocal('pw');
    expect(stored).toEqual([token]);
    expect(claims.role).toBe('admin');
  });

  it('429 carries the Retry-After seconds', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ code: 'LOGIN_LOCKED' }), { status: 429, headers: { 'Retry-After': '125' } })));
    const err = await loginLocal('pw').catch((e) => e);
    expect(err).toBeInstanceOf(LocalLoginError);
    expect(err.code).toBe('LOGIN_LOCKED');
    expect(err.retryAfter).toBe(125);
    expect(stored).toEqual([]);
  });

  it('maps 401 and 409', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('{}', { status: 401 })));
    expect((await loginLocal('x').catch((e) => e)).code).toBe('INVALID_CREDENTIALS');
    vi.stubGlobal('fetch', vi.fn(async () => new Response('{}', { status: 409 })));
    expect((await loginLocal('x').catch((e) => e)).code).toBe('PASSWORD_NOT_SET');
  });

  it('parses Retry-After and formats the wait', () => {
    expect(parseRetryAfter('30')).toBe(30);
    expect(parseRetryAfter(null)).toBeNull();
    expect(parseRetryAfter('Wed, 21 Oct 2026 07:28:00 GMT')).toBeNull();
    expect(formatWait(45)).toBe('45 s');
    expect(formatWait(120)).toBe('2 min');
    expect(formatWait(125)).toBe('2 min 5 s');
  });
});

describe('tokens', () => {
  it('validates the form', () => {
    const ok = { name: 'bot', profile: 'paper', days: '90' };
    expect(validateTokenForm(ok)).toBeNull();
    expect(validateTokenForm({ ...ok, name: '  ' })).toBe('name_required');
    expect(validateTokenForm({ ...ok, name: 'x'.repeat(65) })).toBe('name_too_long');
    expect(validateTokenForm({ ...ok, profile: 'admin' })).toBe('profile_invalid');
    expect(validateTokenForm({ ...ok, days: '0' })).toBe('days_invalid');
    expect(validateTokenForm({ ...ok, days: '366' })).toBe('days_invalid');
    expect(validateTokenForm({ ...ok, days: '365' })).toBeNull();
    expect(validateTokenForm({ ...ok, days: '9.5' })).toBe('days_invalid');
  });

  it('a live token is blocked only when the license is known and not Pro', () => {
    expect(liveTokenBlocked('live', lic({ edition: 'community' }))).toBe(true);
    expect(liveTokenBlocked('live', lic({ state: 'expired', edition: 'community' }))).toBe(true);
    expect(liveTokenBlocked('live', lic())).toBe(false);
    expect(liveTokenBlocked('live', undefined)).toBe(false);
    expect(liveTokenBlocked('paper', lic({ edition: 'community' }))).toBe(false);
  });

  it('active means neither revoked nor expired', () => {
    const base: AgentToken = { id: '1', name: 'a', profile: 'read', created_at: '2026-01-01T00:00:00Z', expires_at: '2027-01-01T00:00:00Z', last_used_at: null, revoked_at: null };
    const now = Date.parse('2026-06-01T00:00:00Z');
    expect(isTokenActive(base, now)).toBe(true);
    expect(isTokenActive({ ...base, revoked_at: '2026-02-01T00:00:00Z' }, now)).toBe(false);
    expect(isTokenActive({ ...base, expires_at: '2026-05-01T00:00:00Z' }, now)).toBe(false);
  });

  it('revoke flow: ask opens one confirmation, cancel and done close it', () => {
    let s = reduceRevoke({ pendingId: null }, { type: 'ask', id: 'a' });
    expect(s.pendingId).toBe('a');
    s = reduceRevoke(s, { type: 'ask', id: 'b' });
    expect(s.pendingId).toBe('b');
    expect(reduceRevoke(s, { type: 'cancel' }).pendingId).toBeNull();
    expect(reduceRevoke(s, { type: 'done' }).pendingId).toBeNull();
  });
});

describe('license helpers', () => {
  it('Pro means effective pro with valid or grace', () => {
    expect(isProLicense(lic())).toBe(true);
    expect(isProLicense(lic({ state: 'grace' }))).toBe(true);
    expect(isProLicense(lic({ state: 'expired' }))).toBe(false);
    expect(isProLicense(lic({ edition: 'community' }))).toBe(false);
    expect(isProLicense(undefined)).toBe(false);
  });

  it('warns at 14 days or fewer and not before', () => {
    expect(expiresSoon(lic({ days_remaining: 14 }))).toBe(true);
    expect(expiresSoon(lic({ days_remaining: 0 }))).toBe(true);
    expect(expiresSoon(lic({ days_remaining: 15 }))).toBe(false);
    expect(expiresSoon(lic({ days_remaining: null }))).toBe(false);
    expect(expiresSoon(lic({ state: 'expired', edition: 'community', days_remaining: 3 }))).toBe(false);
  });

  it('explains why a screen is blocked', () => {
    expect(blockedReason(lic())).toBeNull();
    expect(blockedReason(lic({ edition: 'community', state: 'invalid', holder: null }))).toBe('invalid');
    expect(blockedReason(lic({ edition: 'community', state: 'invalid', holder: null, license_id: null }))).toBe('none');
    expect(hasNoLicense(lic({ state: 'invalid', license_id: null }))).toBe(true);
    expect(hasNoLicense(lic({ state: 'invalid' }))).toBe(false);
    expect(blockedReason(lic({ edition: 'community', state: 'expired' }))).toBe('expired');
    expect(blockedReason(lic({ edition: 'community', state: 'valid' }))).toBe('none');
    expect(blockedReason(undefined)).toBeNull();
  });

  it('reads the rejection reason from a 400 body', () => {
    expect(licenseRejectReason(new ApiError(400, { code: 'LICENSE_INVALID', reason: 'bad_signature' }))).toBe('bad_signature');
    expect(licenseRejectReason(new ApiError(400, { reason: 'nope' }))).toBe('unknown');
    expect(licenseRejectReason(new ApiError(500, { reason: 'expired' }))).toBe('unknown');
    expect(licenseRejectReason(new Error('x'))).toBe('unknown');
  });
});

describe('live mode and errors', () => {
  it('only a boolean live_enabled yields a known mode', () => {
    const s = (v: unknown) => ({ live_enabled: v } as never);
    expect(liveModeOf(s(true))).toBe('live');
    expect(liveModeOf(s(false))).toBe('off');
    expect(liveModeOf(s(undefined))).toBe('unknown');
    expect(liveModeOf(undefined)).toBe('unknown');
    expect(liveModeOf(null)).toBe('unknown');
  });

  it('describes API errors without inventing text', () => {
    expect(describeApiError(new ApiError(403, { code: 'OPERATOR_REQUIRED', error: 'nope' }), 'fb')).toBe('403 · OPERATOR_REQUIRED · nope');
    expect(describeApiError(new ApiError(404, 'plain'), 'fb')).toBe('404');
    expect(describeApiError(new Error('x'), 'fb')).toBe('fb');
  });
});
