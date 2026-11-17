// Console mode catalog (OpenSpec focus-anka-arena-operator-mode, D1/D2).
//
// The mode is decided at build time via NEXT_PUBLIC_PLATFORM_MODE. NavRail, the
// root page, the post-login landing and the middleware all consult ONLY this
// catalog. The module is pure (no React, no next-intl) so it runs in the edge
// middleware and in vitest.

import { canAccess, type RoleId } from '@/lib/rbac';

export type ConsoleMode = 'operator' | 'platform' | 'both';

/** Menu groups. Operator mode uses `operatorGroup`, platform mode `platformGroup`. */
export type OperatorGroup = 'start' | 'operate' | 'validate' | 'govern' | 'account';
export type PlatformGroup =
  | 'start'
  | 'organism'
  | 'create'
  | 'learn'
  | 'operate'
  | 'capital'
  | 'govern'
  | 'account';

export interface ScreenDef {
  /** Slug used by rbac.ts / permissions store. */
  slug: string;
  /** Route without locale prefix. */
  href: string;
  /** Where the screen is available. Outside its mode it redirects to `/home`. */
  mode: ConsoleMode;
  /** Nav i18n key (namespace `Nav`). */
  labelKey: string;
  operatorGroup?: OperatorGroup;
  platformGroup?: PlatformGroup;
  /**
   * Merged screen: in EVERY mode the route redirects here (path may carry a
   * query string). The menu never links merged screens.
   */
  mergedInto?: string;
}

export const OPERATOR_HOME = '/home';
export const PLATFORM_HOME = '/flywheel';

/** Ordered catalog: menu order inside each group follows array order. */
export const SCREENS: readonly ScreenDef[] = [
  // Always available
  { slug: 'home', href: '/home', mode: 'both', labelKey: 'home', operatorGroup: 'start', platformGroup: 'start' },
  { slug: 'xsec', href: '/xsec', mode: 'both', labelKey: 'xsec', operatorGroup: 'operate', platformGroup: 'operate' },
  { slug: 'launch', href: '/launch', mode: 'both', labelKey: 'launch', operatorGroup: 'operate', platformGroup: 'operate' },
  { slug: 'motor', href: '/motor', mode: 'both', labelKey: 'motor', operatorGroup: 'operate', platformGroup: 'operate' },
  { slug: 'control-panel', href: '/control-panel', mode: 'both', labelKey: 'control_panel', operatorGroup: 'operate', platformGroup: 'operate' },
  { slug: 'gate', href: '/gate', mode: 'both', labelKey: 'gate', operatorGroup: 'validate', platformGroup: 'organism' },
  { slug: 'spec-studio', href: '/spec-studio', mode: 'both', labelKey: 'spec_studio', operatorGroup: 'validate', platformGroup: 'create' },
  { slug: 'politicas', href: '/politicas', mode: 'both', labelKey: 'policies', operatorGroup: 'govern', platformGroup: 'govern' },
  { slug: 'account-equity', href: '/account/equity', mode: 'both', labelKey: 'account_equity', operatorGroup: 'account', platformGroup: 'account' },
  { slug: 'account-alerts', href: '/account/alerts', mode: 'both', labelKey: 'account_alerts', operatorGroup: 'account', platformGroup: 'account' },
  { slug: 'account-keys', href: '/account/keys', mode: 'both', labelKey: 'account_keys', operatorGroup: 'account', platformGroup: 'account' },

  // Merged screens (redirect in both modes)
  { slug: 'scalping', href: '/scalping', mode: 'both', labelKey: 'scalping', mergedInto: '/control-panel?tab=scalping' },
  { slug: 'aprobaciones', href: '/aprobaciones', mode: 'both', labelKey: 'aprobaciones', mergedInto: '/gate' },

  // Platform-only screens (hidden in operator mode -> /home)
  { slug: 'flywheel', href: '/flywheel', mode: 'platform', labelKey: 'flywheel', platformGroup: 'organism' },
  { slug: 'academia', href: '/academia', mode: 'platform', labelKey: 'academy', platformGroup: 'learn' },
  { slug: 'gremio', href: '/gremio', mode: 'platform', labelKey: 'gremio', platformGroup: 'capital' },
  { slug: 'arena', href: '/arena', mode: 'platform', labelKey: 'arena', platformGroup: 'capital' },
  { slug: 'adversarial', href: '/adversarial', mode: 'platform', labelKey: 'adversarial', platformGroup: 'govern' },
  { slug: 'tribunal', href: '/tribunal', mode: 'platform', labelKey: 'tribunal', platformGroup: 'govern' },
  { slug: 'cohortes', href: '/cohortes', mode: 'platform', labelKey: 'cohortes', platformGroup: 'govern' },
  { slug: 'pool', href: '/pool', mode: 'platform', labelKey: 'pool', platformGroup: 'govern' },
];

/** True when the build is configured as the multi-tenant platform console. */
export function isPlatformMode(): boolean {
  const v = process.env.NEXT_PUBLIC_PLATFORM_MODE;
  return v === '1' || v === 'true';
}

function normalize(pathname: string): string {
  if (!pathname) return '/';
  const p = pathname.length > 1 ? pathname.replace(/\/+$/, '') : pathname;
  return p || '/';
}

/** Find the catalog entry owning a locale-less pathname (exact or sub-route). */
export function findScreen(pathname: string): ScreenDef | undefined {
  const p = normalize(pathname);
  return SCREENS.find((s) => p === s.href || p.startsWith(`${s.href}/`));
}

/** Screen shown in the menu for the given mode (merged screens are never listed). */
export function isScreenAvailable(s: ScreenDef, platformMode: boolean): boolean {
  if (s.mergedInto) return false;
  return s.mode === 'both' || (platformMode ? s.mode === 'platform' : s.mode === 'operator');
}

/**
 * Resolve the redirect target for a locale-less pathname, or null when the
 * route is served as is. The target may include a query (e.g. `?tab=scalping`).
 */
export function resolveRedirect(pathname: string, platformMode: boolean): string | null {
  const screen = findScreen(pathname);
  if (!screen) return null;
  if (screen.mergedInto) return screen.mergedInto;
  if (screen.mode === 'platform' && !platformMode) return OPERATOR_HOME;
  if (screen.mode === 'operator' && platformMode) return PLATFORM_HOME;
  return null;
}

/** Remove a leading `/<locale>` segment. Returns the locale (if any) and the rest. */
export function splitLocale(
  pathname: string,
  locales: readonly string[],
): { locale: string | null; path: string } {
  for (const l of locales) {
    if (pathname === `/${l}`) return { locale: l, path: '/' };
    if (pathname.startsWith(`/${l}/`)) return { locale: l, path: pathname.slice(l.length + 1) };
  }
  return { locale: null, path: pathname };
}

/** Menu items of one group for the mode, in catalog order. */
export function menuGroup(group: string, platformMode: boolean): ScreenDef[] {
  return SCREENS.filter(
    (s) =>
      isScreenAvailable(s, platformMode) &&
      (platformMode ? s.platformGroup : s.operatorGroup) === group,
  );
}

/** Root page destination for the active mode. */
export function rootPath(platformMode: boolean): string {
  return platformMode ? PLATFORM_HOME : OPERATOR_HOME;
}

/**
 * Post-login landing: the first screen the role may open within the mode.
 * Platform mode keeps the legacy preference for the arena.
 */
export function landingPath(role: RoleId, platformMode: boolean = isPlatformMode()): string {
  const candidates = SCREENS.filter((s) => isScreenAvailable(s, platformMode));
  if (platformMode) {
    for (const slug of ['arena', 'flywheel']) {
      const pref = candidates.find((s) => s.slug === slug);
      if (pref && canAccess(role, pref.slug)) return pref.href;
    }
  }
  const first = candidates.find((s) => canAccess(role, s.slug));
  return first?.href ?? rootPath(platformMode);
}
