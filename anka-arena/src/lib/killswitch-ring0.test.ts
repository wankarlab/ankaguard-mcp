import { describe, it, expect, vi } from 'vitest';

vi.mock('@/lib/api', () => {
  class ApiError extends Error {
    constructor(public status: number, public body: unknown) { super(`HTTP ${status}`); }
  }
  return { ApiError, api: { post: vi.fn() } };
});
import { ApiError } from '@/lib/api';
import {
  parseRing0Result, ring0HasFailures, isKillSwitchActiveError, apiErrorDetail,
  normalizeSymbol, findNonBasketPositions,
} from '@/lib/killswitch-ring0';

describe('parseRing0Result', () => {
  it('parses a full payload', () => {
    const r = parseRing0Result({
      dry_run: false, status: 'global_stop_active', bots_stopped: 2, bots_failed: 0,
      engines_stopped: ['xsec'], engines_failed: { scalping: 'boom' },
      orders_cancelled: 3, orders_failed: 0,
      positions: [
        { symbol: 'QNTUSDTM', side: 'long', size: 2, unrealized_pnl: -0.1, result: 'closed' },
        { symbol: 'ETHUSDTM', side: 'short', size: 1, unrealized_pnl: 0.2, result: 'failed', error: 'x' },
      ],
      positions_closed: 1, positions_failed: 1, acted_by: 'u', acted_at: 't',
    });
    expect(r.positions[1].error).toBe('x');
    expect(r.positions_failed).toBe(1);
    expect(ring0HasFailures(r)).toBe(true);
  });
  it('tolerates null/missing fields', () => {
    const r = parseRing0Result({ dry_run: true, positions: null, engines_stopped: null });
    expect(r.positions).toEqual([]);
    expect(r.engines_failed).toEqual({});
    expect(ring0HasFailures(r)).toBe(false);
    expect(parseRing0Result(null).bots_stopped).toBe(0);
  });
  it('derives counts when absent', () => {
    const r = parseRing0Result({ positions: [{ symbol: 'A', result: 'failed' }, { symbol: 'B', result: 'closed' }] });
    expect(r.positions_failed).toBe(1);
    expect(r.positions_closed).toBe(1);
  });
  it('reports a listing failure apart from the positions', () => {
    const r = parseRing0Result({ dry_run: true, positions: [], positions_list_error: 'timeout' });
    expect(r.positions).toEqual([]);
    expect(r.positions_list_error).toBe('timeout');
    expect(ring0HasFailures(r)).toBe(true);
  });
  it('turns the legacy fake row into a listing failure', () => {
    const r = parseRing0Result({
      dry_run: true, positions_failed: 1,
      positions: [{ symbol: '', side: '', size: 0, unrealized_pnl: 0, result: 'failed', error: 'list positions: timeout' }],
    });
    expect(r.positions).toEqual([]);
    expect(r.positions_failed).toBe(0);
    expect(r.positions_list_error).toBe('timeout');
  });
});

describe('errors', () => {
  it('detects 423 and KILLSWITCH_ACTIVE', () => {
    expect(isKillSwitchActiveError(new ApiError(423, {}))).toBe(true);
    expect(isKillSwitchActiveError(new ApiError(400, { error: { code: 'KILLSWITCH_ACTIVE' } }))).toBe(true);
    expect(isKillSwitchActiveError(new ApiError(409, {}))).toBe(false);
    expect(isKillSwitchActiveError(new Error('x'))).toBe(false);
  });
  it('extracts details', () => {
    expect(apiErrorDetail(new ApiError(500, { error: { message: 'm' } }))).toBe('m');
    expect(apiErrorDetail(new ApiError(500, 'plain'))).toBe('plain');
    expect(apiErrorDetail(new Error('x'))).toBe('');
  });
});

describe('symbol matching', () => {
  it('normalizes both formats', () => {
    expect(normalizeSymbol('QNTUSDTM')).toBe('QNTUSDT');
    expect(normalizeSymbol('QNT-USDT')).toBe('QNTUSDT');
    expect(normalizeSymbol('XBTUSDTM')).toBe(normalizeSymbol('BTC-USDT'));
  });
  it('finds positions outside the basket', () => {
    const pos = [{ symbol: 'QNTUSDTM' }, { symbol: 'ETHUSDTM' }, { symbol: 'XBTUSDTM' }];
    const out = findNonBasketPositions(pos, ['QNT-USDT', 'BTC-USDT']);
    expect(out.map((p) => p.symbol)).toEqual(['ETHUSDTM']);
    expect(findNonBasketPositions(null, ['A'])).toEqual([]);
    expect(findNonBasketPositions(pos, null)).toHaveLength(3);
  });
});
