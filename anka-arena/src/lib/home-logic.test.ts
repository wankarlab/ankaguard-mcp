import { describe, it, expect } from 'vitest';
import { mergeLogbook, affectedComponents, fmtOrDash } from '@/lib/home-logic';

describe('mergeLogbook', () => {
  it('merges, sorts newest first and de-duplicates by id', () => {
    const out = mergeLogbook(
      [
        { id: 'a', kind: 'k', message: 'old', ts: '2026-10-03T10:00:00Z' },
        { id: 'b', kind: 'k', message: 'mid', ts: '2026-10-03T11:00:00Z' },
      ],
      [
        { id: 'b', kind: 'k', txt: 'mid-live', ts: Date.parse('2026-10-03T11:00:00Z') },
        { id: 'c', kind: 'k', txt: 'new', ts: Date.parse('2026-10-03T12:00:00Z') },
      ],
    );
    expect(out.map((e) => e.id)).toEqual(['c', 'b', 'a']);
    expect(out[1].text).toBe('mid-live');
  });
  it('caps and drops invalid timestamps; handles null input', () => {
    expect(mergeLogbook(null, null)).toEqual([]);
    const out = mergeLogbook([{ id: 'x', kind: 'k', message: 'm', ts: 'nope' }], [], 5);
    expect(out).toEqual([]);
    const many = Array.from({ length: 10 }, (_, i) => ({ id: `${i}`, kind: 'k', txt: 't', ts: i + 1 }));
    expect(mergeLogbook([], many, 3)).toHaveLength(3);
  });
});

describe('affectedComponents', () => {
  it('lists non-ok components with critical first', () => {
    const c = (name: string, critical: boolean, status: 'ok' | 'down') => ({ name, critical, status, latency_ms: 1, message: '' });
    const out = affectedComponents([c('ml', false, 'down'), c('gct', true, 'down'), c('db', true, 'ok')]);
    expect(out.map((x) => x.name)).toEqual(['gct', 'ml']);
    expect(affectedComponents(null)).toEqual([]);
  });
});

describe('fmtOrDash', () => {
  it('returns a dash for missing values', () => {
    expect(fmtOrDash(null, String)).toBe('—');
    expect(fmtOrDash(undefined, String)).toBe('—');
    expect(fmtOrDash(NaN, String)).toBe('—');
    expect(fmtOrDash(0, (n) => `${n}!`)).toBe('0!');
  });
});
