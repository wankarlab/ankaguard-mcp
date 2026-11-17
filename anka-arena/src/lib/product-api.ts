// product-api.ts — typed wrappers and pure helpers for the product edition
// (local auth, agent tokens, license, product status). Shapes follow
// docs/product/api-contract.md. Nothing here invents data: failures surface as
// errors and the screens show them.

import { ApiError, api, apiFetch } from '@/lib/api';
import { decodeToken, setStoredToken, type UserClaims } from '@/lib/auth';

// ── Types ─────────────────────────────────────────────────────────────────────

export type AuthMode = 'standalone' | 'external';

export interface LocalAuthMode {
  mode: AuthMode;
  password_set: boolean;
}

export type TokenProfile = 'read' | 'paper' | 'live';
export const TOKEN_PROFILES: readonly TokenProfile[] = ['read', 'paper', 'live'];

export interface AgentToken {
  id: string;
  name: string;
  profile: TokenProfile;
  created_at: string;
  expires_at: string | null;
  last_used_at: string | null;
  revoked_at: string | null;
}

export interface CreatedToken {
  token: string;
  token_type: string;
  agent_token: AgentToken;
}

export type LicenseState = 'valid' | 'grace' | 'expired' | 'invalid';

export interface LicenseStatus {
  edition: 'community' | 'pro';
  state: LicenseState;
  holder: string | null;
  license_id: string | null;
  expires_at: string | null;
  days_remaining: number | null;
  grace_days_remaining: number | null;
  warning: 'expiring_soon' | 'in_grace' | 'clock_skew' | null;
}

export type LicenseRejectReason = 'bad_signature' | 'malformed' | 'wrong_key' | 'expired';

export interface ProductStatus {
  edition: string;
  build: 'standard' | 'product' | 'product-pro';
  auth_mode: AuthMode;
  live_enabled: boolean;
  license: LicenseStatus;
}

/** Days left at or under which the license screen and home warn. */
export const EXPIRY_WARNING_DAYS = 14;
export const DEFAULT_TOKEN_DAYS = 90;
export const MAX_TOKEN_DAYS = 365;

// ── Local login ───────────────────────────────────────────────────────────────

export class LocalLoginError extends Error {
  constructor(
    public code: 'INVALID_CREDENTIALS' | 'PASSWORD_NOT_SET' | 'LOGIN_LOCKED' | 'UNKNOWN',
    public status: number,
    /** Seconds from the Retry-After header (LOGIN_LOCKED only). */
    public retryAfter: number | null = null,
  ) {
    super(code);
    this.name = 'LocalLoginError';
  }
}

/** Parse a Retry-After header holding delta-seconds; null when absent or invalid. */
export function parseRetryAfter(value: string | null | undefined): number | null {
  if (value == null || !/^\s*\d+\s*$/.test(value)) return null;
  return parseInt(value, 10);
}

/** "2 min 5 s" / "45 s" — unit symbols are the same in es and en. */
export function formatWait(seconds: number): string {
  const s = Math.max(0, Math.ceil(seconds));
  const m = Math.floor(s / 60);
  const r = s % 60;
  if (m === 0) return `${r} s`;
  return r === 0 ? `${m} min` : `${m} min ${r} s`;
}

export async function getLocalAuthMode(): Promise<LocalAuthMode> {
  // Raw fetch: the endpoint is public and must not trigger the 401 redirect flow.
  const res = await fetch('/api/v1/auth/local/mode');
  if (!res.ok) throw new ApiError(res.status, await res.json().catch(() => ({})));
  const d = (await res.json()) as Partial<LocalAuthMode>;
  if (d.mode !== 'standalone' && d.mode !== 'external') throw new ApiError(res.status, d);
  return { mode: d.mode, password_set: d.password_set === true };
}

/**
 * Password login for `standalone` mode. Stores the session JWT exactly where the
 * rest of the console reads it (getStoredToken), so apiFetch sends it.
 */
export async function loginLocal(password: string): Promise<UserClaims> {
  const res = await fetch('/api/v1/auth/local/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ password }),
  });
  const data = (await res.json().catch(() => ({}))) as { token?: string; code?: string };
  if (!res.ok) {
    if (res.status === 429) {
      throw new LocalLoginError('LOGIN_LOCKED', 429, parseRetryAfter(res.headers.get('Retry-After')));
    }
    if (res.status === 409) throw new LocalLoginError('PASSWORD_NOT_SET', 409);
    if (res.status === 401) throw new LocalLoginError('INVALID_CREDENTIALS', 401);
    throw new LocalLoginError('UNKNOWN', res.status);
  }
  if (!data.token) throw new LocalLoginError('UNKNOWN', res.status);
  const claims = decodeToken(data.token);
  if (!claims) throw new LocalLoginError('UNKNOWN', res.status);
  setStoredToken(data.token);
  return claims;
}

// ── Agent tokens ──────────────────────────────────────────────────────────────

export async function listAgentTokens(): Promise<AgentToken[]> {
  const d = await apiFetch<{ tokens?: AgentToken[] }>('/api/v1/auth/local/tokens');
  return d?.tokens ?? [];
}

export interface CreateTokenInput {
  name: string;
  profile: TokenProfile;
  expires_in_days: number;
}

export function createAgentToken(input: CreateTokenInput): Promise<CreatedToken> {
  return api.post<CreatedToken>('/api/v1/auth/local/tokens', input);
}

export async function revokeAgentToken(id: string): Promise<void> {
  await api.delete<{ revoked: boolean }>(`/api/v1/auth/local/tokens/${encodeURIComponent(id)}`);
}

/** Error code carried in an ApiError body ({"code": "..."}), if any. */
export function apiErrorCode(e: unknown): string | null {
  if (!(e instanceof ApiError)) return null;
  const b = e.body as { code?: unknown } | null | undefined;
  return b && typeof b === 'object' && typeof b.code === 'string' ? b.code : null;
}

export type TokenFormError = 'name_required' | 'name_too_long' | 'profile_invalid' | 'days_invalid';

/** Client-side validation; the server stays the authority (400 INVALID_TOKEN_REQUEST). */
export function validateTokenForm(f: { name: string; profile: string; days: string }): TokenFormError | null {
  const name = f.name.trim();
  if (!name) return 'name_required';
  if (name.length > 64) return 'name_too_long';
  if (!(TOKEN_PROFILES as readonly string[]).includes(f.profile)) return 'profile_invalid';
  if (!/^\d+$/.test(f.days.trim())) return 'days_invalid';
  const d = parseInt(f.days, 10);
  if (d < 1 || d > MAX_TOKEN_DAYS) return 'days_invalid';
  return null;
}

/** A token is active when it is neither revoked nor expired. */
export function isTokenActive(t: AgentToken, now: number = Date.now()): boolean {
  if (t.revoked_at) return false;
  if (t.expires_at && new Date(t.expires_at).getTime() <= now) return false;
  return true;
}

/** Revocation flow state: one pending confirmation at a time, cancel sends nothing. */
export type RevokeState = { pendingId: string | null };

export function reduceRevoke(
  s: RevokeState,
  a: { type: 'ask'; id: string } | { type: 'cancel' } | { type: 'done' },
): RevokeState {
  if (a.type === 'ask') return { pendingId: a.id };
  return { pendingId: null };
}

// ── License ───────────────────────────────────────────────────────────────────

export function getLicense(): Promise<LicenseStatus> {
  return apiFetch<LicenseStatus>('/api/v1/license');
}

export function installLicense(document: string): Promise<LicenseStatus> {
  return api.put<LicenseStatus>('/api/v1/license', { license: document });
}

/** Rejection reasons the server can report; anything else maps to 'unknown'. */
export function licenseRejectReason(e: unknown): LicenseRejectReason | 'unknown' {
  if (e instanceof ApiError && e.status === 400) {
    const r = (e.body as { reason?: unknown } | null | undefined)?.reason;
    if (r === 'bad_signature' || r === 'malformed' || r === 'wrong_key' || r === 'expired') return r;
  }
  return 'unknown';
}

/** A valid Pro license: effective edition pro and state valid or grace. */
export function isProLicense(l: LicenseStatus | undefined | null): boolean {
  return !!l && l.edition === 'pro' && (l.state === 'valid' || l.state === 'grace');
}

/**
 * True when a `live` token must not be requested: the license is known and not
 * a valid Pro. An unknown license (fetch failed) lets the server decide (402).
 */
export function liveTokenBlocked(profile: string, l: LicenseStatus | undefined | null): boolean {
  return profile === 'live' && !!l && !isProLicense(l);
}

/** True when the expiry warning must show (<= 14 days left, license not already invalid). */
export function expiresSoon(l: LicenseStatus | undefined | null): boolean {
  if (!l || l.days_remaining == null) return false;
  if (l.state !== 'valid' && l.state !== 'grace') return false;
  return l.days_remaining <= EXPIRY_WARNING_DAYS;
}

/** True when no license document is loaded at all (the server reports it as "invalid" without an id). */
export function hasNoLicense(l: LicenseStatus | undefined | null): boolean {
  return !!l && l.state === 'invalid' && !l.license_id;
}

/** i18n key suffix describing why a screen is blocked; null when it is not. */
export function blockedReason(l: LicenseStatus | undefined | null): 'none' | 'expired' | 'invalid' | null {
  if (!l) return null;
  if (isProLicense(l)) return null;
  if (l.state === 'expired') return 'expired';
  if (hasNoLicense(l)) return 'none';
  if (l.state === 'invalid') return 'invalid';
  if (l.edition === 'community') return 'none';
  return 'invalid';
}

// ── Product status ────────────────────────────────────────────────────────────

export type LiveMode = 'live' | 'off' | 'unknown';

/** Live-money indicator state. Missing data (error, 404, malformed) is "unknown", never a guess. */
export function liveModeOf(s: ProductStatus | undefined | null): LiveMode {
  if (!s || typeof s.live_enabled !== 'boolean') return 'unknown';
  return s.live_enabled ? 'live' : 'off';
}

export function getProductStatus(): Promise<ProductStatus> {
  return apiFetch<ProductStatus>('/api/v1/product/status');
}

// ── Errors ────────────────────────────────────────────────────────────────────

/** "403 · OPERATOR_REQUIRED · text" from an ApiError; the fallback for anything else. */
export function describeApiError(e: unknown, fallback: string): string {
  if (!(e instanceof ApiError)) return fallback;
  const b = e.body as { code?: unknown; error?: unknown } | string | null | undefined;
  const parts: string[] = [String(e.status)];
  if (b && typeof b === 'object') {
    if (typeof b.code === 'string') parts.push(b.code);
    if (typeof b.error === 'string') parts.push(b.error);
  }
  return parts.join(' · ');
}
