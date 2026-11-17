import { describe, expect, it, vi } from 'vitest';

vi.mock('@/lib/api', () => ({ ApiError: class extends Error {}, apiFetch: vi.fn() }));

import es from '../../messages/es.json';
import en from '../../messages/en.json';
import {
  BANDS, CHANNELS, DECISION_CATEGORIES, EXIT_REASONS, OPPORTUNITY_STATUSES, REASON_CODES, bandKey, exitReasonKey,
  reasonKey, statusKey,
} from './launch-api';

function flatKeys(o: unknown, prefix = ''): string[] {
  if (o && typeof o === 'object') {
    return Object.entries(o as Record<string, unknown>).flatMap(([k, v]) => flatKeys(v, prefix ? `${prefix}.${k}` : k));
  }
  return [prefix];
}

const L = (m: unknown) => (m as { Launch: Record<string, string> }).Launch;

describe('Launch i18n', () => {
  it('es and en have identical key sets (whole files)', () => {
    expect(flatKeys(en).sort()).toEqual(flatKeys(es).sort());
  });
  it('Launch namespace and Nav.launch exist in both languages with no empty values', () => {
    expect(Object.keys(L(es)).sort()).toEqual(Object.keys(L(en)).sort());
    for (const m of [es, en]) {
      expect((m as { Nav: Record<string, string> }).Nav.launch).toBeTruthy();
      for (const [k, v] of Object.entries(L(m))) expect(v, k).toBeTruthy();
    }
  });
  it('has a translation for every code the UI can render', () => {
    const keys = new Set(Object.keys(L(es)));
    const need = [
      ...REASON_CODES.map(reasonKey), ...EXIT_REASONS.map(exitReasonKey),
      ...BANDS.map(bandKey), ...OPPORTUNITY_STATUSES.map(statusKey),
      ...DECISION_CATEGORIES.map((c) => `cat_${c}`), ...CHANNELS.map((c) => `channel_${c}`),
      ...['EDGE', 'NO_EDGE', 'INSUFFICIENT_DATA'].map((v) => `verdict_${v}`),
      ...['up', 'down', 'never_started'].map((s) => `service_${s}`),
      ...['OPEN', 'CLOSED', 'DROPPED'].map((s) => `state_${s}`),
      ...['approve', 'reject', 'expire'].map((a) => `action_${a}`),
      'err_expired', 'err_already_decided', 'err_forbidden',
      'sim_banner',
    ];
    expect(need.filter((k) => !keys.has(k as string))).toEqual([]);
  });
});
