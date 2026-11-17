import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { loginEmailStart, loginEmailVerify, newDeviceId } from './auth';

const UUID_V4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

// Minimal localStorage for the node test environment.
function stubStorage() {
  const store = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => void store.set(k, v),
    removeItem: (k: string) => void store.delete(k),
  });
  vi.stubGlobal('window', {});
  return store;
}

function jsonResponse(status: number, body: unknown) {
  return { ok: status >= 200 && status < 300, status, json: async () => body } as Response;
}

// A structurally valid unsigned JWT that expires in an hour.
function fakeJwt(claims: Record<string, unknown>): string {
  const enc = (o: unknown) => btoa(JSON.stringify(o)).replace(/=+$/, '');
  return `${enc({ alg: 'none' })}.${enc({ exp: Math.floor(Date.now() / 1000) + 3600, ...claims })}.sig`;
}

describe('newDeviceId', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('uses crypto.randomUUID in a secure context', () => {
    const randomUUID = vi.fn(() => '11111111-1111-4111-8111-111111111111');
    vi.stubGlobal('crypto', { randomUUID, getRandomValues: globalThis.crypto.getRandomValues.bind(globalThis.crypto) });
    expect(newDeviceId()).toBe('11111111-1111-4111-8111-111111111111');
  });

  it('falls back to getRandomValues over plain http (LAN IP)', () => {
    vi.stubGlobal('crypto', { getRandomValues: globalThis.crypto.getRandomValues.bind(globalThis.crypto) });
    const a = newDeviceId();
    expect(a).toMatch(UUID_V4);
    expect(newDeviceId()).not.toBe(a);
  });
});

describe('email OTP login', () => {
  let store: Map<string, string>;
  beforeEach(() => {
    store = stubStorage();
  });
  afterEach(() => vi.unstubAllGlobals());

  it('creates the session, sends the identifier and forwards the session token', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse(201, { session_id: 'sid-1', session_token: 'tok-1', screen: 'ENTER_IDENTIFIER' }))
      .mockResolvedValueOnce(jsonResponse(200, { screen: 'ENTER_OTP', resend_in: 60 }));
    vi.stubGlobal('fetch', fetchMock);

    const session = await loginEmailStart('op@anka.io');

    expect(session).toEqual({ sessionId: 'sid-1', sessionToken: 'tok-1', resendIn: 60 });
    expect(fetchMock.mock.calls[0][0]).toBe('/api/v1/auth/session');
    const [path, init] = fetchMock.mock.calls[1];
    expect(path).toBe('/api/v1/auth/session/sid-1/answer');
    expect(init.headers['X-Auth-Session-Token']).toBe('tok-1');
    expect(JSON.parse(init.body)).toEqual({
      challenge: 'identifier',
      answer: { identifier: 'op@anka.io', method: 'email', channel: 'email' },
    });
    expect(store.get('anka_arena_device_id')).toMatch(UUID_V4);
  });

  it('explains that email OTP is not live when auth lacks the session fix', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(jsonResponse(401, { error: { code: 'MISSING_TENANT_CONTEXT', message: 'tenant context not found' } })),
    );
    await expect(loginEmailStart('op@anka.io')).rejects.toThrow(/todavía no está habilitado.*Teléfono o Google/);
  });

  it('surfaces other auth error messages (object or string shape)', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(429, { error: { code: 'RATE_LIMITED', message: 'too many requests' } })));
    await expect(loginEmailStart('op@anka.io')).rejects.toThrow('too many requests');
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(400, { error: 'invalid email' })));
    await expect(loginEmailStart('op@anka.io')).rejects.toThrow('invalid email');
  });

  it('stores the tokens of an existing user', async () => {
    const access = fakeJwt({ user_id: 'u1', user_level: 3 });
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse(200, { completed: true, result: { access_token: access, refresh_token: 'r1', is_new_user: false } }),
      ),
    );
    const claims = await loginEmailVerify({ sessionId: 'sid-1', sessionToken: 'tok-1' }, '123456');
    expect(claims?.level).toBe(3);
    expect(store.get('anka_arena_token')).toBe(access);
    expect(store.get('anka_arena_refresh')).toBe('r1');
  });

  it('rejects an account that the login just created', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse(200, { completed: true, result: { access_token: fakeJwt({ user_id: 'new' }), is_new_user: true } }),
      ),
    );
    await expect(loginEmailVerify({ sessionId: 'sid-1', sessionToken: 'tok-1' }, '123456')).rejects.toThrow(
      /No existe una cuenta de operador/,
    );
  });
});
