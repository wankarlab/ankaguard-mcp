import { describe, expect, it } from 'vitest';
import { splitLocale } from './consoleMode';
import { PRODUCT_SCREENS, isLicenseExempt, isRouteAllowed, productMenuGroup, supportedExchangesFor } from './productMode';

describe('product edition routes', () => {
  it.each([
    '/', '/home', '/login', '/system-health', '/control-panel', '/account/keys', '/account/equity',
    '/agent-tokens', '/license', '/license/', '/account/keys/',
  ])('serves %s', (p) => expect(isRouteAllowed(p, true)).toBe(true));

  it.each([
    '/flywheel', '/gate', '/spec-studio', '/academia', '/gremio', '/arena', '/arena/12', '/politicas',
    '/adversarial', '/tribunal', '/cohortes', '/pool', '/xsec', '/launch', '/motor', '/scalping',
    '/aprobaciones', '/account/alerts', '/licensing', '/auth/callback', '/homepage',
  ])('answers not found for %s', (p) => expect(isRouteAllowed(p, true)).toBe(false));

  it('the nav lists exactly the allowed screens', () => {
    for (const s of PRODUCT_SCREENS) expect(isRouteAllowed(s.href.split('?')[0], true)).toBe(true);
    expect(PRODUCT_SCREENS.map((s) => s.slug)).toEqual([
      'home', 'system-health', 'control-panel', 'account-keys', 'account-equity', 'agent-tokens', 'license',
    ]);
    expect(productMenuGroup('operate').map((s) => s.slug)).toEqual(['system-health', 'control-panel']);
  });

  it('works with the locale prefix removed by splitLocale', () => {
    expect(isRouteAllowed(splitLocale('/en/flywheel', ['es', 'en']).path, true)).toBe(false);
    expect(isRouteAllowed(splitLocale('/en/license', ['es', 'en']).path, true)).toBe(true);
  });

  it('without the edition the product-only screens are not found and the rest is untouched', () => {
    for (const p of ['/system-health', '/agent-tokens', '/license']) expect(isRouteAllowed(p, false)).toBe(false);
    for (const p of ['/home', '/flywheel', '/xsec', '/control-panel', '/account/keys', '/login', '/arena/3'])
      expect(isRouteAllowed(p, false)).toBe(true);
  });

  it('only login, health and license are exempt from the license gate', () => {
    for (const p of ['/login', '/system-health', '/license']) expect(isLicenseExempt(p)).toBe(true);
    for (const p of ['/home', '/control-panel', '/account/keys', '/account/equity', '/agent-tokens'])
      expect(isLicenseExempt(p)).toBe(false);
  });
});

describe('supportedExchangesFor', () => {
  const all = [{ name: 'binance' }, { name: 'KuCoin' }, { name: 'okx' }];
  it('keeps only KuCoin in the product', () => {
    expect(supportedExchangesFor(true, all)).toEqual([{ name: 'KuCoin' }]);
  });
  it('changes nothing outside the product', () => {
    expect(supportedExchangesFor(false, all)).toBe(all);
  });
});
