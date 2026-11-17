import { describe, expect, it } from 'vitest';
import {
  SCREENS,
  landingPath,
  menuGroup,
  resolveRedirect,
  rootPath,
  splitLocale,
} from './consoleMode';

describe('resolveRedirect', () => {
  it.each(['/arena', '/arena/abc', '/adversarial', '/tribunal', '/pool', '/cohortes', '/gremio', '/academia', '/flywheel'])(
    'hides %s in operator mode',
    (p) => expect(resolveRedirect(p, false)).toBe('/home'),
  );

  it('serves platform screens in platform mode', () => {
    expect(resolveRedirect('/tribunal', true)).toBeNull();
    expect(resolveRedirect('/arena/abc', true)).toBeNull();
    expect(resolveRedirect('/flywheel', true)).toBeNull();
  });

  it('merges scalping and aprobaciones in both modes', () => {
    for (const mode of [false, true]) {
      expect(resolveRedirect('/scalping', mode)).toBe('/control-panel?tab=scalping');
      expect(resolveRedirect('/aprobaciones', mode)).toBe('/gate');
    }
  });

  it('leaves shared and unknown routes alone', () => {
    expect(resolveRedirect('/home', false)).toBeNull();
    expect(resolveRedirect('/xsec', false)).toBeNull();
    expect(resolveRedirect('/account/equity', false)).toBeNull();
    expect(resolveRedirect('/login', false)).toBeNull();
    expect(resolveRedirect('/arenas', false)).toBeNull();
    expect(resolveRedirect('/', false)).toBeNull();
  });

  it('tolerates trailing slashes', () => {
    expect(resolveRedirect('/tribunal/', false)).toBe('/home');
  });
});

describe('splitLocale', () => {
  const locales = ['es', 'en'];
  it('strips a locale prefix', () => {
    expect(splitLocale('/en/tribunal', locales)).toEqual({ locale: 'en', path: '/tribunal' });
    expect(splitLocale('/es', locales)).toEqual({ locale: 'es', path: '/' });
  });
  it('keeps unprefixed paths and lookalikes', () => {
    expect(splitLocale('/tribunal', locales)).toEqual({ locale: null, path: '/tribunal' });
    expect(splitLocale('/english', locales)).toEqual({ locale: null, path: '/english' });
  });
});

describe('menu and landing', () => {
  it('operator menu has the five groups and no hidden or merged routes', () => {
    const groups = ['start', 'operate', 'validate', 'govern', 'account'];
    expect(groups.map((g) => menuGroup(g, false).map((s) => s.slug))).toEqual([
      ['home'],
      ['xsec', 'launch', 'motor', 'control-panel'],
      ['gate', 'spec-studio'],
      ['politicas'],
      ['account-equity', 'account-alerts', 'account-keys'],
    ]);
  });

  it('never lists a route that redirects', () => {
    for (const platform of [false, true]) {
      const groups = ['start', 'organism', 'create', 'learn', 'operate', 'capital', 'govern', 'account', 'validate'];
      for (const g of groups) {
        for (const s of menuGroup(g, platform)) expect(resolveRedirect(s.href, platform)).toBeNull();
      }
    }
  });

  it('platform menu includes the platform screens', () => {
    expect(menuGroup('govern', true).map((s) => s.slug)).toEqual(['politicas', 'adversarial', 'tribunal', 'cohortes', 'pool']);
  });

  it('catalog slugs and hrefs are unique', () => {
    expect(new Set(SCREENS.map((s) => s.slug)).size).toBe(SCREENS.length);
    expect(new Set(SCREENS.map((s) => s.href)).size).toBe(SCREENS.length);
  });

  it('root and landing depend on the mode', () => {
    expect(rootPath(false)).toBe('/home');
    expect(rootPath(true)).toBe('/flywheel');
    expect(landingPath('operator', false)).toBe('/home');
    expect(landingPath('viewer', false)).toBe('/home');
    expect(landingPath('author', true)).toBe('/arena');
    expect(landingPath('viewer', true)).toBe('/flywheel');
  });
});
