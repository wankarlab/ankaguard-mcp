// Product edition catalog (OpenSpec guarded-mcp-product, D15).
//
// NEXT_PUBLIC_EDITION=product narrows the console to the screens an operator
// needs to install, watch and govern the standalone server. This module is
// pure (no React, no next-intl) so the proxy (edge) and vitest can use it.
// Without the variable the console is exactly what it was before.

export const PRODUCT_HOME = '/home';

export type ProductGroup = 'start' | 'operate' | 'account' | 'access';

export interface ProductScreen {
  slug: string;
  /** Route without locale prefix (may carry a query string). */
  href: string;
  /** Nav i18n key (namespace `Nav`). */
  labelKey: string;
  group: ProductGroup;
}

/** Ordered product menu. The emergency stop is a nav button (global modal), not a route. */
export const PRODUCT_SCREENS: readonly ProductScreen[] = [
  { slug: 'home', href: '/home', labelKey: 'home', group: 'start' },
  { slug: 'system-health', href: '/system-health', labelKey: 'system_health', group: 'operate' },
  { slug: 'control-panel', href: '/control-panel?tab=sizing', labelKey: 'sizing', group: 'operate' },
  { slug: 'account-keys', href: '/account/keys', labelKey: 'account_keys', group: 'account' },
  { slug: 'account-equity', href: '/account/equity', labelKey: 'account_equity', group: 'account' },
  { slug: 'agent-tokens', href: '/agent-tokens', labelKey: 'agent_tokens', group: 'access' },
  { slug: 'license', href: '/license', labelKey: 'license', group: 'access' },
];

/** Routes served in product edition (sub-routes included). `/` redirects to home. */
export const PRODUCT_ROUTES: readonly string[] = ['/', '/login', ...PRODUCT_SCREENS.map((s) => s.href.split('?')[0])];

/** Routes that exist only in the product edition (not found elsewhere). */
export const PRODUCT_ONLY_ROUTES: readonly string[] = ['/system-health', '/agent-tokens', '/license'];

/**
 * Routes reachable without a valid Pro license: login, health and license.
 * The emergency stop is a global modal, so it is never behind a route.
 */
export const LICENSE_EXEMPT_ROUTES: readonly string[] = ['/', '/login', '/system-health', '/license'];

/** Exchanges the product supports (the server only trades on KuCoin). */
export const PRODUCT_EXCHANGES: readonly string[] = ['kucoin'];

/** Keeps the exchanges the product supports; a no-op outside the product. */
export function supportedExchangesFor<T extends { name: string }>(product: boolean, list: T[]): T[] {
  if (!product) return list;
  return list.filter((e) => PRODUCT_EXCHANGES.includes(e.name.toLowerCase()));
}

/** True when the build is the product edition. */
export function isProductEdition(): boolean {
  return process.env.NEXT_PUBLIC_EDITION === 'product';
}

function normalize(pathname: string): string {
  if (!pathname) return '/';
  const p = pathname.length > 1 ? pathname.replace(/\/+$/, '') : pathname;
  return p || '/';
}

function matches(list: readonly string[], pathname: string): boolean {
  const p = normalize(pathname);
  return list.some((r) => (r === '/' ? p === '/' : p === r || p.startsWith(`${r}/`)));
}

/**
 * Single decision point for edition routing (used by src/proxy.ts). `path` is
 * locale-less. False means the route must answer "not found" in that edition.
 */
export function isRouteAllowed(path: string, product: boolean): boolean {
  if (product) return matches(PRODUCT_ROUTES, path);
  return !matches(PRODUCT_ONLY_ROUTES, path);
}

/** True when the route stays usable without a valid Pro license. */
export function isLicenseExempt(path: string): boolean {
  return matches(LICENSE_EXEMPT_ROUTES, path);
}

export function productMenuGroup(group: ProductGroup): ProductScreen[] {
  return PRODUCT_SCREENS.filter((s) => s.group === group);
}
