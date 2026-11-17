import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import es from '../../messages/es.json';
import en from '../../messages/en.json';

function flatKeys(o: unknown, prefix = ''): string[] {
  if (o && typeof o === 'object') {
    return Object.entries(o as Record<string, unknown>).flatMap(([k, v]) => flatKeys(v, prefix ? `${prefix}.${k}` : k));
  }
  return [prefix];
}

const P = (m: unknown) => (m as { Product: Record<string, string> }).Product;
const N = (m: unknown) => (m as { Nav: Record<string, string> }).Nav;

describe('product i18n', () => {
  it('es and en have identical key sets over the whole files', () => {
    expect(flatKeys(en).sort()).toEqual(flatKeys(es).sort());
  });

  it('Product values are non-empty in both languages', () => {
    for (const m of [es, en]) for (const [k, v] of Object.entries(P(m))) expect(v, k).toBeTruthy();
  });

  it('nav keys of the product screens exist in both languages', () => {
    for (const k of ['system_health', 'sizing', 'agent_tokens', 'license', 'group_access', 'home', 'account_keys', 'account_equity', 'kill_switch']) {
      expect(N(es)[k], k).toBeTruthy();
      expect(N(en)[k], k).toBeTruthy();
    }
  });

  it('every literal and templated key the product components use exists', () => {
    const dir = join(__dirname, '../components/product');
    const keys = new Set(Object.keys(P(es)));
    const used = new Set<string>();
    for (const f of readdirSync(dir).filter((x) => x.endsWith('.tsx'))) {
      const src = readFileSync(join(dir, f), 'utf8');
      for (const m of src.matchAll(/\bt\('([a-z0-9_]+)'/g)) used.add(m[1]);
    }
    expect(used.size).toBeGreaterThan(40);
    for (const k of used) expect(keys.has(k), k).toBe(true);
    // dynamic suffixes
    const dyn: Record<string, string[]> = {
      mode_: ['live', 'off', 'unknown'],
      lic_state_: ['valid', 'grace', 'expired', 'invalid'],
      lic_reject_: ['bad_signature', 'malformed', 'wrong_key', 'expired'],
      tok_err_: ['name_required', 'name_too_long', 'profile_invalid', 'days_invalid'],
      tok_profile_: ['read', 'paper', 'live'],
      blocked_: ['none', 'expired', 'invalid', 'session', 'unknown'],
      health_state_: ['ok', 'degraded', 'down'],
    };
    for (const [prefix, list] of Object.entries(dyn)) for (const s of list) expect(keys.has(prefix + s), prefix + s).toBe(true);
    expect(keys.has('mode_live_hint') && keys.has('mode_off_hint') && keys.has('mode_unknown_hint')).toBe(true);
  });
});
