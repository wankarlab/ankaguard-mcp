// auth.ts — JWT client-side handling + RantiPay Auth integration
//
// Tokens are minted by the RantiPay Auth service and reached through the
// trading-mcp public proxy at /api/v1/auth/* (same-origin via the Next proxy).
// Signature verification is Kong/trading-mcp's responsibility; this module only
// decodes claims for client-side routing and stores the tokens.

import type { RoleId } from '@/lib/rbac';

export interface UserClaims {
  user_id: string;
  role: RoleId;
  kyc: number;
  level: number;
  tenant_id: string;
  email?: string;
  exp?: number;
  iat?: number;
}

const TOKEN_KEY = 'anka_arena_token';
const REFRESH_KEY = 'anka_arena_refresh';

/**
 * Map the RantiPay numeric user_level (1–4) to an Anka Arena functional role.
 * RantiPay JWTs carry `user_level`, not a `role` string, so we derive it.
 * A literal `role` claim (if ever present) takes precedence.
 */
export function roleFromLevel(level: number): RoleId {
  if (level >= 4) return 'admin';
  if (level === 3) return 'author';
  if (level === 2) return 'operator';
  if (level === 1) return 'copiador';
  return 'viewer';
}

/**
 * Decode a JWT and return its claims, mapping RantiPay's claim shape
 * (`sub`/`user_id`, `user_level`) onto the Anka Arena model (`role`, `kyc`).
 * Returns null if the token is malformed or expired.
 */
export function decodeToken(token: string): UserClaims | null {
  try {
    const parts = token.split('.');
    if (parts.length !== 3) return null;

    const payload = parts[1];
    const padded = payload + '='.repeat((4 - (payload.length % 4)) % 4);
    const decoded = JSON.parse(atob(padded));

    // Expiration check
    if (decoded.exp && decoded.exp * 1000 < Date.now()) {
      return null;
    }

    const userId: string = decoded.user_id ?? decoded.sub ?? '';
    if (!userId) return null;

    // RantiPay carries `user_level`; tolerate legacy `level`.
    const level = Number(decoded.user_level ?? decoded.level ?? 0);
    const role: RoleId = (decoded.role as RoleId) ?? roleFromLevel(level);
    // No dedicated kyc claim today → derive from level (level 2 ⇒ kyc 2, etc.).
    const kyc = Number(decoded.kyc ?? level);

    return {
      user_id:   userId,
      role,
      kyc,
      level,
      tenant_id: decoded.tenant_id ?? '',
      email:     decoded.email,
      exp:       decoded.exp,
      iat:       decoded.iat,
    };
  } catch {
    return null;
  }
}

// ── Token storage ─────────────────────────────────────────────────────────────

export function getStoredToken(): string | null {
  if (typeof window === 'undefined') return null;
  return localStorage.getItem(TOKEN_KEY);
}

export function setStoredToken(token: string): void {
  if (typeof window === 'undefined') return;
  localStorage.setItem(TOKEN_KEY, token);
}

export function getRefreshToken(): string | null {
  if (typeof window === 'undefined') return null;
  return localStorage.getItem(REFRESH_KEY);
}

export function setRefreshToken(token: string): void {
  if (typeof window === 'undefined') return;
  localStorage.setItem(REFRESH_KEY, token);
}

export function clearToken(): void {
  if (typeof window === 'undefined') return;
  localStorage.removeItem(TOKEN_KEY);
  localStorage.removeItem(REFRESH_KEY);
}

export function getCurrentUser(): UserClaims | null {
  const token = getStoredToken();
  if (!token) return null;
  return decodeToken(token);
}

// ── RantiPay Auth flows (via /api/v1/auth/* proxy) ──────────────────────────────

interface AuthTokens {
  access_token: string;
  refresh_token: string;
  token_type?: string;
  expires_in?: number;
}

interface LoginResponse {
  success: boolean;
  tokens?: AuthTokens;
  message?: string;
  error?: string;
  requires_mfa?: boolean;
}

/**
 * Persist the tokens from an Auth-service response and return decoded claims.
 */
function persistTokens(tokens: AuthTokens): UserClaims | null {
  setStoredToken(tokens.access_token);
  if (tokens.refresh_token) setRefreshToken(tokens.refresh_token);
  return decodeToken(tokens.access_token);
}

// ── Email login (passwordless OTP, session orchestrator v2 — D7) ─────────────
//
// RantiPay Auth decided email is passwordless-first: /auth/login/email (password)
// no longer works for these accounts. The flow is the orchestrator's:
//   POST /auth/session → answer "identifier" (email) → answer "email_otp" (code).
// Same implementation as whatsapp-admin-ui (commits ec43c00fb, 39f17999b).

const DEVICE_ID_KEY = 'anka_arena_device_id';
const APP_VERSION = '1.0.0';

/**
 * crypto.randomUUID only exists in secure contexts (HTTPS or localhost). The
 * console is also opened over the LAN at http://<ip>:3002, where it is
 * undefined; crypto.getRandomValues works in every context.
 */
export function newDeviceId(): string {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID();
  const b = crypto.getRandomValues(new Uint8Array(16));
  b[6] = (b[6] & 0x0f) | 0x40; // version 4
  b[8] = (b[8] & 0x3f) | 0x80; // RFC 4122 variant
  const h = Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}

function getOrCreateDeviceId(): string {
  let id = localStorage.getItem(DEVICE_ID_KEY);
  if (!id) {
    id = newDeviceId();
    localStorage.setItem(DEVICE_ID_KEY, id);
  }
  return id;
}

/** Auth errors come as {error:{message}} or {error:"..."} or {message}. */
function errorMessage(data: unknown, fallback: string): string {
  const d = (data ?? {}) as { error?: unknown; message?: unknown };
  if (d.error && typeof d.error === 'object' && typeof (d.error as { message?: unknown }).message === 'string') {
    return (d.error as { message: string }).message;
  }
  if (typeof d.error === 'string') return d.error;
  return typeof d.message === 'string' && d.message ? d.message : fallback;
}

async function postJSON(path: string, body: unknown, sessionToken?: string) {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' };
  if (sessionToken) headers['X-Auth-Session-Token'] = sessionToken;
  const res = await fetch(path, { method: 'POST', headers, body: JSON.stringify(body) });
  const data = await res.json().catch(() => ({}));
  return { res, data };
}

export interface EmailOtpSession {
  sessionId: string;
  sessionToken: string;
  resendIn?: number;
}

/**
 * Step 1 — create the session and answer the identifier challenge with the
 * email, which sends the OTP. One call for the UI's single "send code" action.
 */
export async function loginEmailStart(email: string): Promise<EmailOtpSession> {
  const created = await postJSON('/api/v1/auth/session', {
    intent: 'login_or_register',
    client: { platform: 'web', device_id: getOrCreateDeviceId(), app_version: APP_VERSION },
    locale: 'es',
  });
  if (!created.res.ok || !created.data.session_id || !created.data.session_token) {
    // Auth builds without the D7 SkipPaths fix (rantipay ec43c00fb) reject the
    // anonymous session with MISSING_TENANT_CONTEXT: email OTP is not live there yet.
    if (created.data?.error?.code === 'MISSING_TENANT_CONTEXT') {
      throw new Error(
        'El login por email todavía no está habilitado en el servidor de autenticación. Entra con Teléfono o Google.',
      );
    }
    throw new Error(errorMessage(created.data, 'No se pudo iniciar la sesión de login.'));
  }
  const sessionId: string = created.data.session_id;
  const sessionToken: string = created.data.session_token;

  const answered = await postJSON(
    `/api/v1/auth/session/${encodeURIComponent(sessionId)}/answer`,
    { challenge: 'identifier', answer: { identifier: email, method: 'email', channel: 'email' } },
    sessionToken,
  );
  if (!answered.res.ok) {
    throw new Error(errorMessage(answered.data, 'Error al enviar el código al email.'));
  }
  if (answered.data.screen !== 'ENTER_OTP') {
    throw new Error(`Pantalla inesperada del servidor: ${answered.data.screen || 'desconocida'}`);
  }
  return { sessionId, sessionToken, resendIn: answered.data.resend_in };
}

/** Step 2 — answer the email_otp challenge and store the issued tokens. */
export async function loginEmailVerify(session: EmailOtpSession, code: string): Promise<UserClaims> {
  const { res, data } = await postJSON(
    `/api/v1/auth/session/${encodeURIComponent(session.sessionId)}/answer`,
    { challenge: 'email_otp', answer: { code } },
    session.sessionToken,
  );
  if (!res.ok) throw new Error(errorMessage(data, 'Código inválido o expirado.'));
  const result = data.result;
  if (!data.completed || !result?.access_token) {
    throw new Error(`Pantalla inesperada del servidor: ${data.screen || 'desconocida'}`);
  }
  if (result.is_new_user) {
    // Restricted operator console: typing an unknown email must not let a
    // freshly created account in.
    throw new Error('No existe una cuenta de operador con ese email. Solicita acceso a un administrador.');
  }
  const claims = persistTokens({ access_token: result.access_token, refresh_token: result.refresh_token ?? '' });
  if (!claims) throw new Error('Token recibido no es válido.');
  return claims;
}

/** Resend the email OTP on the current session. */
export async function loginEmailResend(session: EmailOtpSession): Promise<{ resendIn?: number }> {
  const { res, data } = await postJSON(
    `/api/v1/auth/session/${encodeURIComponent(session.sessionId)}/resend`,
    { channel: 'email' },
    session.sessionToken,
  );
  if (!res.ok) throw new Error(errorMessage(data, 'No se pudo reenviar el código.'));
  return { resendIn: data.resend_in };
}

// ── Phone login (2-step OTP) ────────────────────────────────────────────────────

export type PhoneMode = 'login' | 'register';

export interface OtpSent {
  channel: 'sms' | 'whatsapp';
  expiresIn?: number;
  mode: PhoneMode;       // which verify endpoint to call next
  message?: string;
}

/**
 * Step 1 — request an OTP. Smart: tries login (existing account) first; if the
 * number isn't registered it falls back to registration (which also sends an OTP).
 * The returned `mode` tells step 2 which verify endpoint to use.
 *
 * NOTE: RantiPay currently delivers the code via WhatsApp, not SMS.
 */
export async function loginPhoneStart(
  phoneNumber: string,
  channel: 'sms' | 'whatsapp' = 'whatsapp',
): Promise<OtpSent> {
  // 1) Existing account → login OTP.
  const r1 = await fetch('/api/v1/auth/login/phone', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ phone_number: phoneNumber, preferred_channel: channel, remember_me: true }),
  });
  const d1 = await r1.json().catch(() => ({} as any));
  if (r1.ok && d1.success !== false) {
    return { mode: 'login', channel: d1.channel ?? channel, expiresIn: d1.expires_in, message: d1.message };
  }

  // 2) Number not registered → start registration (also sends an OTP).
  const r2 = await fetch('/api/v1/auth/register/phone', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      phone_number: phoneNumber,
      preferred_channel: channel,
      accept_terms: true,
      accept_privacy: true,
      marketing_consent: false,
    }),
  });
  const d2 = await r2.json().catch(() => ({} as any));
  if (r2.ok && d2.success !== false) {
    return { mode: 'register', channel: d2.channel ?? channel, expiresIn: d2.expires_in, message: d2.message };
  }

  throw new Error(d2.message || d2.error || d1.message || 'No se pudo enviar el código.');
}

/**
 * Step 2 — verify the 6-digit OTP and obtain JWT tokens. `mode` selects the
 * login or registration verify endpoint (both return tokens).
 */
export async function loginPhoneVerify(
  phoneNumber: string,
  otpCode: string,
  mode: PhoneMode = 'login',
  firstName?: string,
): Promise<UserClaims> {
  const path =
    mode === 'register'
      ? '/api/v1/auth/register/phone/verify'
      : '/api/v1/auth/login/phone/verify';
  const body: Record<string, unknown> =
    mode === 'register'
      ? { phone_number: phoneNumber, otp_code: otpCode, first_name: firstName || 'Usuario' }
      : { phone_number: phoneNumber, otp_code: otpCode, remember_me: true };

  const res = await fetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  const data: LoginResponse = await res.json().catch(() => ({ success: false }));
  if (!res.ok || !data.success || !data.tokens) {
    throw new Error(data.message || data.error || 'Código inválido.');
  }
  const claims = persistTokens(data.tokens);
  if (!claims) throw new Error('Token recibido no es válido.');
  return claims;
}

/**
 * Persist tokens received out-of-band (e.g. the OAuth callback redirect) and
 * return decoded claims.
 */
export function adoptTokens(accessToken: string, refreshToken?: string): UserClaims | null {
  return persistTokens({ access_token: accessToken, refresh_token: refreshToken ?? '' });
}

/**
 * Exchange a refresh token for a fresh access token. Returns the new claims,
 * or null if the refresh failed (caller should redirect to login).
 */
export async function refreshSession(): Promise<UserClaims | null> {
  const refresh = getRefreshToken();
  if (!refresh) return null;

  try {
    const res = await fetch('/api/v1/auth/refresh', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ refresh_token: refresh }),
    });
    const data: LoginResponse = await res.json();
    if (!res.ok || !data.success || !data.tokens) {
      clearToken();
      return null;
    }
    return persistTokens(data.tokens);
  } catch {
    return null;
  }
}

// ── Google login (Google Identity Services → native token exchange) ─────────────
//
// The RantiPay gateway only exposes the OAuth *native* endpoint (the web
// authorize/callback flow is not routed). So on web we use Google Identity
// Services to obtain an ID token in the browser and exchange it for RantiPay
// tokens via POST /api/v1/auth/oauth/google/native. The server verifies the ID
// token; the access_token field is required-but-unverified, so we send the same
// credential. The client_id must be an authorized JS origin in Google Console.

const GOOGLE_CLIENT_ID =
  process.env.NEXT_PUBLIC_GOOGLE_CLIENT_ID ??
  '163218053731-lvuqkabco8hevnd29a6hbnabr6qvmc2s.apps.googleusercontent.com';

declare global {
  interface Window {
    google?: any;
  }
}

function loadGoogleIdentity(): Promise<void> {
  return new Promise((resolve, reject) => {
    if (typeof window === 'undefined') return reject(new Error('no window'));
    if (window.google?.accounts?.id) return resolve();
    const existing = document.getElementById('gis-script');
    if (existing) {
      existing.addEventListener('load', () => resolve());
      existing.addEventListener('error', () => reject(new Error('No se pudo cargar Google.')));
      return;
    }
    const s = document.createElement('script');
    s.id = 'gis-script';
    s.src = 'https://accounts.google.com/gsi/client';
    s.async = true;
    s.defer = true;
    s.onload = () => resolve();
    s.onerror = () => reject(new Error('No se pudo cargar Google.'));
    document.head.appendChild(s);
  });
}

interface GooglePayload {
  email?: string;
  name?: string;
  sub?: string;
}

/**
 * Sign in with Google via One Tap / GIS. Resolves to user claims on success.
 * Rejects if the prompt is suppressed or the exchange fails.
 */
export async function loginGoogle(): Promise<UserClaims> {
  await loadGoogleIdentity();

  const credential = await new Promise<string>((resolve, reject) => {
    window.google.accounts.id.initialize({
      client_id: GOOGLE_CLIENT_ID,
      callback: (resp: { credential?: string }) =>
        resp?.credential ? resolve(resp.credential) : reject(new Error('Google no devolvió credencial.')),
    });
    window.google.accounts.id.prompt((n: any) => {
      if (n?.isNotDisplayed?.() || n?.isSkippedMoment?.() || n?.isDismissedMoment?.()) {
        reject(new Error('No se pudo mostrar el login de Google. Verifica el origen autorizado.'));
      }
    });
  });

  // Decode the ID token for the user_info the native endpoint requires.
  const payload = (decodeJwtRaw(credential) ?? {}) as GooglePayload;

  const res = await fetch('/api/v1/auth/oauth/google/native', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      access_token: credential, // required-but-unverified by the server
      id_token: credential,
      user_info: { email: payload.email ?? '', name: payload.name ?? '', id: payload.sub ?? '' },
    }),
  });
  const data: LoginResponse = await res.json().catch(() => ({ success: false }));
  if (!res.ok || !data.success || !data.tokens) {
    throw new Error(data.message || data.error || 'No se pudo completar el login con Google.');
  }
  const claims = persistTokens(data.tokens);
  if (!claims) throw new Error('Token de Google no válido.');
  return claims;
}

/** Decode a JWT payload without validation (for reading Google ID token claims). */
function decodeJwtRaw(token: string): Record<string, unknown> | null {
  try {
    const part = token.split('.')[1];
    const padded = part + '='.repeat((4 - (part.length % 4)) % 4);
    return JSON.parse(atob(padded));
  } catch {
    return null;
  }
}
