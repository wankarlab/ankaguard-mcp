// RBAC — UX HINT ONLY for instant render before the backend answers.
//
// The AUTHORITATIVE source of truth for screen access is the backend endpoint
// GET /api/v1/me/permissions (see usePermissionsStore + LockedScreen). That
// endpoint reads required levels from the dynamic config table (rbac.level.*).
// This module exists so the nav can render a sensible lock affordance on first
// paint; once permissions load, the store wins. Do NOT hardcode business
// thresholds here that contradict the backend — keep this list cosmetic.

export type RoleId = 'admin' | 'author' | 'operator' | 'copiador' | 'viewer';

export interface RolePermission {
  /** Minimum KYC level required for this role */
  kyc: number;
  /** List of screen slugs this role can access */
  allow: string[];
  /** Human-readable display name */
  displayName: string;
  /** Brand color for avatar */
  color: string;
}

/**
 * Screens (slugs) used throughout the app.
 * flywheel   — Flywheel Vivo (live engine dashboard)
 * gate       — Teatro del Gate (edge validation theater)
 * spec-studio — Spec Studio (strategy editor)
 * academia   — Academia + Replays
 * gremio     — Gremio + Fee Ledger
 * politicas  — Sala de Políticas
 * arena      — Arena · Torneo
 */
export const ALL_SCREENS = [
  'flywheel',
  'gate',
  'spec-studio',
  'academia',
  'gremio',
  'politicas',
  'scalping',
  'control-panel',
  'motor',
  'xsec',
  'launch',
  'arena',
  'account-equity',
  'account-alerts',
  'account-keys',
  'home',
] as const;

export type ScreenSlug = (typeof ALL_SCREENS)[number];

/** Roles ordered by ascending level (0..4). Each role's `allow` must include the previous one's. */
export const ROLE_ORDER: readonly RoleId[] = ['viewer', 'copiador', 'operator', 'author', 'admin'];

export const ROLE_PERMISSIONS: Record<RoleId, RolePermission> = {
  admin: {
    kyc: 0,
    displayName: 'Admin',
    color: '#9d7cff',
    allow: ['flywheel', 'gate', 'spec-studio', 'academia', 'gremio', 'politicas', 'scalping', 'control-panel', 'motor', 'xsec', 'launch', 'arena', 'account-equity', 'account-alerts', 'account-keys', 'home'],
  },
  author: {
    kyc: 2,
    displayName: 'Autor',
    color: '#46c8d8',
    allow: ['flywheel', 'gate', 'spec-studio', 'academia', 'gremio', 'scalping', 'control-panel', 'motor', 'xsec', 'launch', 'arena', 'account-equity', 'account-alerts', 'account-keys', 'home'],
  },
  operator: {
    kyc: 1,
    displayName: 'Operador',
    color: '#e9a23e',
    allow: ['flywheel', 'gate', 'academia', 'gremio', 'scalping', 'control-panel', 'motor', 'xsec', 'launch', 'arena', 'account-equity', 'account-alerts', 'account-keys', 'home'],
  },
  copiador: {
    kyc: 1,
    displayName: 'Copiador',
    color: '#4f9bf5',
    allow: ['flywheel', 'academia', 'launch', 'arena', 'account-equity', 'account-alerts', 'account-keys', 'home'],
  },
  viewer: {
    kyc: 0,
    displayName: 'Viewer',
    color: '#7e8799',
    allow: ['flywheel', 'academia', 'account-equity', 'account-alerts', 'account-keys', 'home'],
  },
};

/**
 * Check whether a role can access a given screen slug.
 */
export function canAccess(role: RoleId, screen: string): boolean {
  const perms = ROLE_PERMISSIONS[role];
  if (!perms) return false;
  return perms.allow.includes(screen);
}

/**
 * Return the minimum KYC level required across all roles that can access a screen.
 * Returns 0 if the screen is accessible by viewer (everyone).
 */
export function getMinKyc(screen: string): number {
  const levels = Object.values(ROLE_PERMISSIONS)
    .filter((p) => p.allow.includes(screen))
    .map((p) => p.kyc);
  if (levels.length === 0) return 99; // no role can access → require max KYC
  return Math.min(...levels);
}

/**
 * Get all screens accessible to a role.
 */
export function getAllowedScreens(role: RoleId): string[] {
  return ROLE_PERMISSIONS[role]?.allow ?? [];
}
