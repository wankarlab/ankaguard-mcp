import { describe, expect, it } from 'vitest';
import { formatLastUsed } from './format-date';

describe('formatLastUsed', () => {
  it('returns a dash for empty, null, invalid and Go zero dates', () => {
    expect(formatLastUsed(undefined)).toBe('—');
    expect(formatLastUsed(null)).toBe('—');
    expect(formatLastUsed('')).toBe('—');
    expect(formatLastUsed('garbage')).toBe('—');
    expect(formatLastUsed('0001-01-01T00:00:00Z')).toBe('—');
  });
  it('formats a real date', () => {
    expect(formatLastUsed('2026-10-04T12:00:00Z')).not.toBe('—');
  });
});
