import { describe, expect, it } from 'vitest';
import { withToken } from './ws';

describe('withToken', () => {
  it('leaves the URL unchanged without a token', () => {
    expect(withToken('ws://h/ws', null)).toBe('ws://h/ws');
  });
  it('appends the token as a query parameter', () => {
    expect(withToken('ws://h/ws', 'a.b c')).toBe('ws://h/ws?token=a.b%20c');
  });
  it('keeps existing query parameters', () => {
    expect(withToken('ws://h/ws?x=1', 't')).toBe('ws://h/ws?x=1&token=t');
  });
});
