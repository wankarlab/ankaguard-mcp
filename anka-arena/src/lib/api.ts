// api.ts — Typed fetch client with Bearer JWT injection

import { routing } from '@/i18n/routing';
import { getStoredToken, refreshSession } from '@/lib/auth';
import {
  toFeeLedger,
  toFeePeriodDetail,
  toSpecTemplates,
  toGateSpecs,
  toGuildStrategies,
  toPermissions,
  toTradeHistory,
  toConsentTemplate,
  toConsentStatus,
  toAdversarialQueue,
  toAppeals,
  type FeeLedger,
  type FeePeriod,
  type SpecTemplate,
  type GateSpec,
  type GuildStrategy,
  type Permissions,
  type TradeHistoryRow,
  type ConsentTemplate,
  type ConsentStatus,
  type AdversarialItem,
  type Appeal,
} from '@/lib/adapters';

// In development Next.js rewrites /api/* → http://localhost:10600/api/* (see next.config.js).
// Set NEXT_PUBLIC_API_URL only when bypassing the Next.js proxy (e.g. mobile/native clients).
const BASE_URL = (process.env.NEXT_PUBLIC_API_URL ?? '').replace(/\/$/, '');

export class ApiError extends Error {
  constructor(
    public status: number,
    public body: unknown,
    message?: string,
  ) {
    super(message ?? `HTTP ${status}`);
    this.name = 'ApiError';
  }
}

/**
 * Fetch a JSON endpoint from the trading-mcp backend.
 * Automatically attaches the stored Bearer token if available.
 *
 * @param path  - Path starting with `/`, e.g. `/api/v1/engine/stats`
 * @param init  - Optional RequestInit overrides
 * @returns     - Parsed JSON as T
 * @throws      - ApiError for non-2xx responses
 */
export async function apiFetch<T>(path: string, init?: RequestInit, _retried = false): Promise<T> {
  const token = getStoredToken();

  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(init?.headers as Record<string, string> | undefined),
  };

  if (token) {
    headers['Authorization'] = `Bearer ${token}`;
  }

  const res = await fetch(`${BASE_URL}${path}`, {
    ...init,
    headers,
  });

  // Auto-refresh once on 401: the access token likely expired. If a fresh token
  // is obtained, retry the original request transparently. If the refresh also
  // fails (session fully expired), the stored token is cleared and the user is
  // sent to /login instead of being stranded on 401s.
  if (res.status === 401 && !_retried && token) {
    const claims = await refreshSession();
    if (claims) {
      return apiFetch<T>(path, init, true);
    }
    if (typeof window !== 'undefined' && !window.location.pathname.endsWith('/login')) {
      // Preserve the active locale prefix (as-needed: default locale has none).
      const seg = window.location.pathname.split('/')[1];
      const prefix = (routing.locales as readonly string[]).includes(seg) && seg !== routing.defaultLocale ? `/${seg}` : '';
      window.location.href = `${prefix}/login`;
    }
  }

  if (!res.ok) {
    let body: unknown;
    try {
      body = await res.json();
    } catch {
      body = await res.text();
    }
    throw new ApiError(res.status, body);
  }

  const json = await res.json();

  // AnkaAI REST handlers wrap payloads in { success, data, timestamp }. Unwrap to
  // `data` so callers receive the bare payload. Endpoints that return a raw object
  // or array (no `data` field) pass through untouched. Auth responses use raw
  // fetch (not apiFetch), so this never strips their { tokens } envelope.
  if (
    json && typeof json === 'object' && !Array.isArray(json) &&
    'success' in json && 'data' in json
  ) {
    return (json as { data: T }).data;
  }

  return json as T;
}

/**
 * Convenience wrappers for common HTTP verbs.
 */
export const api = {
  get:    <T>(path: string, init?: Omit<RequestInit, 'method'>) =>
    apiFetch<T>(path, { ...init, method: 'GET' }),

  post:   <T>(path: string, body: unknown, init?: Omit<RequestInit, 'method' | 'body'>) =>
    apiFetch<T>(path, { ...init, method: 'POST', body: JSON.stringify(body) }),

  put:    <T>(path: string, body: unknown, init?: Omit<RequestInit, 'method' | 'body'>) =>
    apiFetch<T>(path, { ...init, method: 'PUT', body: JSON.stringify(body) }),

  delete: <T>(path: string, init?: Omit<RequestInit, 'method'>) =>
    apiFetch<T>(path, { ...init, method: 'DELETE' }),
};

// ── Typed endpoint fetchers (auto-adapted snake_case → camelCase) ───────────────
// Every fetcher consumes a REAL backend endpoint and runs the corresponding
// adapter. There are NO mocks: endpoints that have no rows yet return empty
// lists. Business values (fee rate, splits, required levels, legal text) are all
// backend-sourced — never hardcode them in the client.

// C0.4 — Fee ledger (per-user, high-water-mark periods). auth: own | level>=3.
export async function fetchFeeLedger(userId: string): Promise<FeeLedger> {
  return toFeeLedger(await apiFetch(`/api/v1/billing/fees/${encodeURIComponent(userId)}`));
}

export async function fetchFeePeriod(userId: string, periodId: string): Promise<FeePeriod> {
  return toFeePeriodDetail(
    await apiFetch(`/api/v1/billing/fees/${encodeURIComponent(userId)}/${encodeURIComponent(periodId)}`),
  );
}

// C0.5 — Spec Studio templates (seeded real StrategySpec docs). public.
export async function fetchSpecTemplates(): Promise<SpecTemplate[]> {
  return toSpecTemplates(await apiFetch('/api/v1/specs/templates'));
}

// Gate specs. When `mine` is true, calls the JWT-authenticated /specs/mine route
// (the public /specs route has no JWT middleware and 401s on ?mine, which would
// bounce the user to /login). Otherwise the public cross-user community Gate.
export async function fetchSpecs(opts?: { mine?: boolean; status?: string }): Promise<GateSpec[]> {
  const params = new URLSearchParams();
  if (opts?.status) params.set('status', opts.status);
  const qs = params.toString();
  const path = opts?.mine ? '/api/v1/specs/mine' : '/api/v1/specs';
  return toGateSpecs(await apiFetch(`${path}${qs ? `?${qs}` : ''}`));
}

// C1.1 — Marketplace strategies (anti-survivorship: includes retired + certified).
export async function fetchMarketplaceStrategies(): Promise<GuildStrategy[]> {
  return toGuildStrategies(await apiFetch('/api/v1/marketplace/strategies'));
}

// C1.5 — Promote a spec to live. Backend returns 403 SELF_APPROVAL_FORBIDDEN on
// self-approval (unless spec.allow_self_promotion). Throws ApiError on failure.
export interface PromoteResult {
  spec_id:  string;
  status:   string;
  approver: string;
}
export async function promoteSpec(specId: string): Promise<PromoteResult> {
  return apiFetch<PromoteResult>(`/api/v1/specs/${encodeURIComponent(specId)}/promote`, {
    method: 'POST',
    body: JSON.stringify({}),
  });
}

// C0.2 — Demote (retire) a live spec. Real endpoint (replaces the phantom
// /archive). Backend returns 404 NOT_FOUND or 409 CONFLICT (only live specs can
// be demoted). Throws ApiError on failure.
export interface DemoteResult {
  spec_id: string;
  status:  string;
  reason:  string;
}
export async function demoteSpec(specId: string, reason?: string): Promise<DemoteResult> {
  return apiFetch<DemoteResult>(`/api/v1/specs/${encodeURIComponent(specId)}/demote`, {
    method: 'POST',
    body: JSON.stringify(reason ? { reason } : {}),
  });
}

// C1.4 — Permissions (truth for locked screens + required levels + kyc status).
export async function fetchPermissions(): Promise<Permissions> {
  return toPermissions(await apiFetch('/api/v1/me/permissions'));
}

// C1.1 — Trade history (VERIFIED: includes losers; FE adds the disclaimer).
export async function fetchTradeHistory(): Promise<TradeHistoryRow[]> {
  return toTradeHistory(await apiFetch('/api/v1/trades/history'));
}

// C1.3 — Consent: versioned legal text + per-user acceptance audit trail.
export async function fetchConsentTemplate(kind: string): Promise<ConsentTemplate> {
  return toConsentTemplate(await apiFetch(`/api/v1/consent/template/${encodeURIComponent(kind)}`));
}

export async function fetchConsentStatus(kind: string): Promise<ConsentStatus> {
  return toConsentStatus(await apiFetch(`/api/v1/consent/status?kind=${encodeURIComponent(kind)}`));
}

export interface ConsentAccepted {
  kind:             string;
  accepted:         boolean;
  accepted_version: number;
}
/** Idempotent: backend UNIQUE(user_id,kind,template_version). */
export async function acceptConsent(kind: string, templateVersion: number): Promise<ConsentAccepted> {
  return apiFetch<ConsentAccepted>('/api/v1/consent', {
    method: 'POST',
    body: JSON.stringify({ kind, template_version: templateVersion }),
  });
}

// C3.2 — Adversarial review queue + flag.
export async function fetchAdversarialQueue(): Promise<AdversarialItem[]> {
  return toAdversarialQueue(await apiFetch('/api/v1/adversarial/queue'));
}

export interface AdversarialFlagResult {
  flag_id:     string;
  item_id:     string;
  reviewer_id: string;
}
export async function flagAdversarial(itemId: string, reason: string): Promise<AdversarialFlagResult> {
  return apiFetch<AdversarialFlagResult>(`/api/v1/adversarial/${encodeURIComponent(itemId)}/flag`, {
    method: 'POST',
    body: JSON.stringify({ reason }),
  });
}

// C3.3 — Appeals (tribunal).
export async function fetchAppeals(): Promise<Appeal[]> {
  return toAppeals(await apiFetch('/api/v1/appeals'));
}

export interface AppealCreated {
  id:      string;
  spec_id: string;
  appellant_id: string;
  status:  string;
}
export async function createAppeal(specId: string, reason: string): Promise<AppealCreated> {
  return apiFetch<AppealCreated>('/api/v1/appeals', {
    method: 'POST',
    body: JSON.stringify({ spec_id: specId, reason }),
  });
}

// C1.7 — Exchange credentials (non-custodial BYO-keys). Real backend endpoints
// under /api/v1/credentials. The permissions array returned by /test is the
// source of truth for the "solo-trade / no-withdraw" banner — never hardcoded.

export interface SupportedExchange {
  name:                string;
  display_name:        string;
  requires_passphrase: boolean;
  features:            string[];
}
/** Public — list of exchanges the platform can connect to (BYO-keys). */
export async function fetchSupportedExchanges(): Promise<SupportedExchange[]> {
  const d = await apiFetch<{ exchanges?: SupportedExchange[] }>('/api/v1/credentials/exchanges');
  return d?.exchanges ?? [];
}

export interface ExchangeCredential {
  exchange:     string;
  label:        string;
  api_key_hint: string;
  sandbox:      boolean;
  status:       string;
  created_at?:  string;
  last_used_at?: string;
}
/** Credentials the caller has already connected (own keys, never the secret). */
export async function fetchCredentials(): Promise<ExchangeCredential[]> {
  const d = await apiFetch<{ credentials?: ExchangeCredential[] }>('/api/v1/credentials');
  return d?.credentials ?? [];
}

export interface AddCredentialInput {
  exchange:   string;
  apiKey:     string;
  apiSecret:  string;
  passphrase?: string;
  label?:     string;
}
/** Adds the caller's own API key (solo-trade keys; we never store withdraw access). */
export async function addCredential(input: AddCredentialInput): Promise<ExchangeCredential> {
  return apiFetch<ExchangeCredential>('/api/v1/credentials', {
    method: 'POST',
    body: JSON.stringify({
      exchange:   input.exchange,
      api_key:    input.apiKey,
      api_secret: input.apiSecret,
      passphrase: input.passphrase ?? '',
      label:      input.label ?? '',
    }),
  });
}

/** Deletes the caller's stored credential for an exchange. */
export async function deleteCredential(exchange: string): Promise<void> {
  await apiFetch<unknown>(`/api/v1/credentials/${encodeURIComponent(exchange)}`, { method: 'DELETE' });
}

export interface CredentialTestResult {
  exchange:    string;
  valid:       boolean;
  status:      string;
  message:     string;
  permissions: string[];
}
/**
 * Tests a connected credential against the exchange. The `permissions` array
 * (e.g. ["spot","futures"] / includes "withdraw" when the key can withdraw) is
 * the REAL verification used for the non-custodial banner.
 */
export async function testCredential(exchange: string): Promise<CredentialTestResult> {
  const d = await apiFetch<{
    exchange?: string;
    valid?: boolean;
    status?: string;
    message?: string;
    permissions?: string[];
  }>(`/api/v1/credentials/${encodeURIComponent(exchange)}/test`, {
    method: 'POST',
    body: JSON.stringify({}),
  });
  return {
    exchange:    d?.exchange ?? exchange,
    valid:       d?.valid ?? false,
    status:      d?.status ?? 'invalid',
    message:     d?.message ?? '',
    permissions: d?.permissions ?? [],
  };
}
