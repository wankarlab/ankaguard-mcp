// killswitch-ring0.ts — Ring 0 (global stop) client: dry-run preview, live
// execution, reset, and pure helpers (result parsing, error mapping, matching
// of non-basket futures positions).

import { api, ApiError } from '@/lib/api';

export const RING0_CONFIRM_TEXT = 'CERRAR TODO';
const RING0_ENDPOINT = '/api/v1/admin/killswitch/ring/0';

export type Ring0PositionResult = 'closed' | 'failed' | 'would_close';

export interface Ring0Position {
  symbol: string;
  side: string;
  size: number;
  unrealized_pnl: number;
  result: Ring0PositionResult;
  error?: string;
}

export interface Ring0Result {
  dry_run: boolean;
  status: string;
  bots_stopped: number;
  bots_failed: number;
  engines_stopped: string[];
  engines_failed: Record<string, string>;
  orders_cancelled: number;
  orders_failed: number;
  positions: Ring0Position[];
  positions_closed: number;
  positions_failed: number;
  /** Set when the open positions could not be listed: their state is unknown. */
  positions_list_error: string;
  acted_by: string;
  acted_at: string;
}

const num = (v: unknown): number => (typeof v === 'number' && isFinite(v) ? v : 0);
const str = (v: unknown): string => (typeof v === 'string' ? v : '');

/** Defensive parse of the ring-0 response (missing/null fields become zero/empty). */
export function parseRing0Result(raw: unknown): Ring0Result {
  const r = (raw && typeof raw === 'object' ? raw : {}) as Record<string, unknown>;
  const parsed: Ring0Position[] = Array.isArray(r.positions)
    ? r.positions
        .filter((p): p is Record<string, unknown> => !!p && typeof p === 'object')
        .map((p) => {
          const result = p.result === 'closed' || p.result === 'failed' ? p.result : 'would_close';
          return {
            symbol: str(p.symbol),
            side: str(p.side),
            size: num(p.size),
            unrealized_pnl: num(p.unrealized_pnl),
            result,
            ...(typeof p.error === 'string' && p.error ? { error: p.error } : {}),
          } as Ring0Position;
        })
    : [];
  // Older servers reported a listing failure as a fake failed row with no
  // symbol ("list positions: ..."). It is not a position: move it out.
  const isLegacyListRow = (p: Ring0Position) =>
    !p.symbol && p.result === 'failed' && (p.error ?? '').startsWith('list positions');
  const legacy = parsed.find(isLegacyListRow);
  const positions = parsed.filter((p) => !isLegacyListRow(p));
  const listError =
    str(r.positions_list_error) || (legacy ? (legacy.error ?? '').replace(/^list positions:\s*/, '') : '');
  const failedMap: Record<string, string> = {};
  if (r.engines_failed && typeof r.engines_failed === 'object' && !Array.isArray(r.engines_failed)) {
    for (const [k, v] of Object.entries(r.engines_failed as Record<string, unknown>)) failedMap[k] = str(v);
  }
  return {
    dry_run: r.dry_run === true,
    status: str(r.status),
    bots_stopped: num(r.bots_stopped),
    bots_failed: num(r.bots_failed),
    engines_stopped: Array.isArray(r.engines_stopped) ? r.engines_stopped.filter((e): e is string => typeof e === 'string') : [],
    engines_failed: failedMap,
    orders_cancelled: num(r.orders_cancelled),
    orders_failed: num(r.orders_failed),
    positions,
    positions_closed: typeof r.positions_closed === 'number' ? r.positions_closed : positions.filter((p) => p.result === 'closed').length,
    positions_failed:
      typeof r.positions_failed === 'number'
        ? Math.max(0, r.positions_failed - (legacy ? 1 : 0))
        : positions.filter((p) => p.result === 'failed').length,
    positions_list_error: listError,
    acted_by: str(r.acted_by),
    acted_at: str(r.acted_at),
  };
}

/** True when the live run left something undone (needs a warning, not a success). */
export function ring0HasFailures(r: Ring0Result): boolean {
  return (
    r.positions_failed > 0 || r.bots_failed > 0 || r.orders_failed > 0 || r.positions_list_error !== '' ||
    Object.keys(r.engines_failed).length > 0
  );
}

export async function previewRing0(): Promise<Ring0Result> {
  return parseRing0Result(await api.post(RING0_ENDPOINT, { dry_run: true }));
}

export async function executeRing0(confirm: string): Promise<Ring0Result> {
  return parseRing0Result(await api.post(RING0_ENDPOINT, { confirm, dry_run: false }));
}

export async function resetRing0(): Promise<void> {
  await api.post(`${RING0_ENDPOINT}/reset`, {});
}

// ── Errors ────────────────────────────────────────────────────────────────────

interface ErrBody { error?: { code?: string; message?: string } | string; message?: string }

function bodyOf(e: ApiError): ErrBody {
  return e.body && typeof e.body === 'object' ? (e.body as ErrBody) : {};
}

/** True for HTTP 423 / code KILLSWITCH_ACTIVE (global stop blocks risk-increasing actions). */
export function isKillSwitchActiveError(e: unknown): boolean {
  if (!(e instanceof ApiError)) return false;
  if (e.status === 423) return true;
  const err = bodyOf(e).error;
  return typeof err === 'object' && err?.code === 'KILLSWITCH_ACTIVE';
}

/** Server-provided message from an error body, if any. */
export function apiErrorDetail(e: unknown): string {
  if (!(e instanceof ApiError)) return '';
  const b = bodyOf(e);
  if (typeof b.error === 'string') return b.error;
  if (b.error && typeof b.error === 'object' && b.error.message) return b.error.message;
  if (b.message) return b.message;
  return typeof e.body === 'string' ? e.body.slice(0, 200) : '';
}

// ── Non-basket futures positions ──────────────────────────────────────────────

/**
 * Canonical comparison key for a futures symbol. Maps "QNTUSDTM", "QNT-USDT",
 * "QNT-USDT-SWAP" and "XBTUSDTM" (KuCoin's BTC) to "QNTUSDT" / "BTCUSDT".
 */
export function normalizeSymbol(symbol: string): string {
  let s = symbol.toUpperCase().replace(/[-_/:]/g, '');
  s = s.replace(/SWAP$/, '');
  if (s.endsWith('USDTM')) s = s.slice(0, -1);
  if (s.startsWith('XBT')) s = 'BTC' + s.slice(3);
  return s;
}

export interface FuturesPositionLite {
  symbol: string;
  side: string;
  size: number;
  unrealized_pnl: number;
}

/** Positions on the account whose symbol is not a leg of the xsec basket. */
export function findNonBasketPositions<T extends { symbol: string }>(
  positions: T[] | null | undefined,
  basketPairs: string[] | null | undefined,
): T[] {
  const basket = new Set((basketPairs ?? []).map(normalizeSymbol));
  return (positions ?? []).filter((p) => p.symbol && !basket.has(normalizeSymbol(p.symbol)));
}
