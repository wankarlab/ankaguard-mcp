'use client';

import React from 'react';
import { Link, usePathname, useRouter } from '@/i18n/routing';
import { useTranslations } from 'next-intl';
import clsx from 'clsx';
import { useUIStore } from '@/stores/ui';
import { usePermissionsStore } from '@/stores/permissions';
import { canAccess, type RoleId } from '@/lib/rbac';
import { isPlatformMode, menuGroup } from '@/lib/consoleMode';
import { isProductEdition, productMenuGroup, type ProductGroup } from '@/lib/productMode';

// ── Icons (inline SVG to avoid extra deps) ──────────────────────────────────

function IconFlywheel() {
  return (
    <svg viewBox="0 0 17 17" fill="none" xmlns="http://www.w3.org/2000/svg">
      <circle cx="8.5" cy="8.5" r="5.5" stroke="currentColor" strokeWidth="1.4" />
      <circle cx="8.5" cy="8.5" r="2" fill="currentColor" />
      <path d="M8.5 3V1M8.5 16v-2M3 8.5H1M16 8.5h-2" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
    </svg>
  );
}

function IconGate() {
  return (
    <svg viewBox="0 0 17 17" fill="none" xmlns="http://www.w3.org/2000/svg">
      <rect x="2" y="2" width="13" height="13" rx="2" stroke="currentColor" strokeWidth="1.4" />
      <path d="M5 8.5h7M9 6l2.5 2.5L9 11" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

function IconSpec() {
  return (
    <svg viewBox="0 0 17 17" fill="none" xmlns="http://www.w3.org/2000/svg">
      <path d="M3 3h11M3 7h8M3 11h5" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
      <circle cx="13" cy="12" r="3" stroke="currentColor" strokeWidth="1.3" />
      <path d="M15.5 14.5l1.5 1.5" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
    </svg>
  );
}

function IconAcademia() {
  return (
    <svg viewBox="0 0 17 17" fill="none" xmlns="http://www.w3.org/2000/svg">
      <path d="M8.5 2L15 5.5v4L8.5 13 2 9.5v-4L8.5 2Z" stroke="currentColor" strokeWidth="1.3" />
      <path d="M5.5 7.5v4.5" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
    </svg>
  );
}

function IconGremio() {
  return (
    <svg viewBox="0 0 17 17" fill="none" xmlns="http://www.w3.org/2000/svg">
      <circle cx="6" cy="5.5" r="2.5" stroke="currentColor" strokeWidth="1.3" />
      <circle cx="11.5" cy="5.5" r="2.5" stroke="currentColor" strokeWidth="1.3" />
      <path d="M1.5 14c0-2.5 2-4 4.5-4s4.5 1.5 4.5 4" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
    </svg>
  );
}

function IconControl() {
  return (
    <svg viewBox="0 0 17 17" fill="none" xmlns="http://www.w3.org/2000/svg">
      <path d="M3 5h11M3 8.5h11M3 12h11" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
      <circle cx="6" cy="5" r="1.6" fill="var(--bg)" stroke="currentColor" strokeWidth="1.3" />
      <circle cx="11" cy="8.5" r="1.6" fill="var(--bg)" stroke="currentColor" strokeWidth="1.3" />
      <circle cx="7" cy="12" r="1.6" fill="var(--bg)" stroke="currentColor" strokeWidth="1.3" />
    </svg>
  );
}

function IconMotor() {
  return (
    <svg viewBox="0 0 17 17" fill="none" xmlns="http://www.w3.org/2000/svg">
      <circle cx="8.5" cy="8.5" r="3" stroke="currentColor" strokeWidth="1.4" />
      <path d="M8.5 1v2.5M8.5 13.5V16M1 8.5h2.5M13.5 8.5H16M3.4 3.4l1.8 1.8M11.8 11.8l1.8 1.8M13.6 3.4l-1.8 1.8M5.2 11.8l-1.8 1.8" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
    </svg>
  );
}

function IconXsec() {
  return (
    <svg viewBox="0 0 17 17" fill="none" xmlns="http://www.w3.org/2000/svg">
      <path d="M2 5h5M10 5h5M2 12h5M10 12h5" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
      <path d="M5.5 5v7M11.5 5v7" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeDasharray="1 2.4" />
      <circle cx="5.5" cy="5" r="1.6" fill="var(--bg)" stroke="currentColor" strokeWidth="1.3" />
      <circle cx="11.5" cy="12" r="1.6" fill="var(--bg)" stroke="currentColor" strokeWidth="1.3" />
    </svg>
  );
}

function IconLaunch() {
  return (
    <svg viewBox="0 0 17 17" fill="none" xmlns="http://www.w3.org/2000/svg">
      <path d="M8.5 2c2.6 1.8 3.8 4.4 3.5 7.5l-2 2h-3l-2-2C4.7 6.4 5.9 3.8 8.5 2Z" stroke="currentColor" strokeWidth="1.3" strokeLinejoin="round" />
      <circle cx="8.5" cy="6.5" r="1.2" stroke="currentColor" strokeWidth="1.2" />
      <path d="M6.5 11.5L5 15M10.5 11.5L12 15M8.5 12v3" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
    </svg>
  );
}

function IconPoliticas() {
  return (
    <svg viewBox="0 0 17 17" fill="none" xmlns="http://www.w3.org/2000/svg">
      <rect x="2" y="2" width="13" height="13" rx="2" stroke="currentColor" strokeWidth="1.3" />
      <path d="M5 6h7M5 9h5M5 12h3" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
    </svg>
  );
}

function IconArena() {
  return (
    <svg viewBox="0 0 17 17" fill="none" xmlns="http://www.w3.org/2000/svg">
      <path d="M8.5 2l1.8 4.9H15l-4 3.1 1.5 4.9-4-3-4 3 1.5-4.9-4-3.1h4.7L8.5 2Z" stroke="currentColor" strokeWidth="1.3" strokeLinejoin="round" />
    </svg>
  );
}

function IconAdversarial() {
  return (
    <svg viewBox="0 0 17 17" fill="none" xmlns="http://www.w3.org/2000/svg">
      <path d="M8.5 2l5.5 2.5v4c0 3.5-2.5 5.5-5.5 6.5C5.5 14 3 12 3 8.5v-4L8.5 2Z" stroke="currentColor" strokeWidth="1.3" strokeLinejoin="round" />
      <path d="M8.5 6v3M8.5 11h.01" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
    </svg>
  );
}

function IconTribunal() {
  return (
    <svg viewBox="0 0 17 17" fill="none" xmlns="http://www.w3.org/2000/svg">
      <path d="M8.5 2v13M4 6l-2 4h4l-2-4ZM13 6l-2 4h4l-2-4Z" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" />
      <path d="M3 15h11" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
    </svg>
  );
}

function IconPool() {
  return (
    <svg viewBox="0 0 17 17" fill="none" xmlns="http://www.w3.org/2000/svg">
      <path d="M2 11c1.5 0 1.5 1.5 3 1.5S6.5 11 8 11s1.5 1.5 3 1.5S12.5 11 14 11" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
      <path d="M2 7c1.5 0 1.5 1.5 3 1.5S6.5 7 8 7s1.5 1.5 3 1.5S12.5 7 14 7" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
      <circle cx="8.5" cy="3.5" r="1.5" stroke="currentColor" strokeWidth="1.3" />
    </svg>
  );
}

function IconCohortes() {
  return (
    <svg viewBox="0 0 17 17" fill="none" xmlns="http://www.w3.org/2000/svg">
      <circle cx="5" cy="6" r="2" stroke="currentColor" strokeWidth="1.3" />
      <circle cx="12" cy="6" r="2" stroke="currentColor" strokeWidth="1.3" />
      <path d="M2 14c0-2 1.5-3.5 3-3.5S8 12 8 14M9 14c0-2 1.5-3.5 3-3.5s3 1.5 3 3.5" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" />
    </svg>
  );
}

function IconLock() {
  return (
    <svg viewBox="0 0 17 17" fill="none" xmlns="http://www.w3.org/2000/svg" style={{ color: 'var(--micro)', opacity: 0.7 }}>
      <rect x="3.5" y="7.5" width="10" height="8" rx="1.5" stroke="currentColor" strokeWidth="1.3" />
      <path d="M5.5 7.5V5.5a3 3 0 1 1 6 0v2" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
      <circle cx="8.5" cy="11.5" r="1" fill="currentColor" />
    </svg>
  );
}

// ── Nav structure ────────────────────────────────────────────────────────────

// Icons by catalog slug; groups and membership come from src/lib/consoleMode.ts.
function IconHome() {
  return (
    <svg viewBox="0 0 17 17" fill="none" xmlns="http://www.w3.org/2000/svg">
      <path d="M2.5 8L8.5 2.5 14.5 8M4 7v7h3.5v-4h2v4H13V7" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

function IconAccount() {
  return (
    <svg viewBox="0 0 17 17" fill="none" xmlns="http://www.w3.org/2000/svg">
      <circle cx="8.5" cy="6" r="2.7" stroke="currentColor" strokeWidth="1.3" />
      <path d="M3 14.5c0-2.8 2.4-4.5 5.5-4.5s5.5 1.7 5.5 4.5" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
    </svg>
  );
}

const SLUG_ICONS: Record<string, () => React.ReactElement> = {
  home: IconHome,
  flywheel: IconFlywheel,
  gate: IconGate,
  'spec-studio': IconSpec,
  academia: IconAcademia,
  'control-panel': IconControl,
  motor: IconMotor,
  xsec: IconXsec,
  launch: IconLaunch,
  gremio: IconGremio,
  arena: IconArena,
  politicas: IconPoliticas,
  adversarial: IconAdversarial,
  tribunal: IconTribunal,
  cohortes: IconCohortes,
  pool: IconPool,
  'account-equity': IconAccount,
  'account-alerts': IconAccount,
  'account-keys': IconAccount,
  'system-health': IconGate,
  'agent-tokens': IconLock,
  license: IconPoliticas,
};

interface NavGroupDef {
  labelKey: string;
  /** Group id inside the catalog. */
  group: string;
}

// Group labels reuse existing Nav keys where they exist (operate/govern/...).
const OPERATOR_GROUPS: NavGroupDef[] = [
  { labelKey: 'group_start', group: 'start' },
  { labelKey: 'operate', group: 'operate' },
  { labelKey: 'group_validate', group: 'validate' },
  { labelKey: 'govern', group: 'govern' },
  { labelKey: 'group_account', group: 'account' },
];

const PLATFORM_GROUPS: NavGroupDef[] = [
  { labelKey: 'group_start', group: 'start' },
  { labelKey: 'organism', group: 'organism' },
  { labelKey: 'create', group: 'create' },
  { labelKey: 'learn', group: 'learn' },
  { labelKey: 'operate', group: 'operate' },
  { labelKey: 'capital', group: 'capital' },
  { labelKey: 'govern', group: 'govern' },
  { labelKey: 'group_account', group: 'account' },
];

// Product edition (NEXT_PUBLIC_EDITION=product): fixed menu from src/lib/productMode.ts.
const PRODUCT_GROUPS: NavGroupDef[] = [
  { labelKey: 'group_start', group: 'start' },
  { labelKey: 'operate', group: 'operate' },
  { labelKey: 'group_account', group: 'account' },
  { labelKey: 'group_access', group: 'access' },
];

// Screens whose lock state is governed by the backend permissions endpoint
// (me/permissions.locks[]) rather than the client-side rbac.ts hint. These are
// the GOBERNAR screens; the backend lock (required level) is the truth.
const PERMISSION_GOVERNED = new Set([
  'politicas',
  'adversarial',
  'tribunal',
  'cohortes',
  'pool',
]);

// ── Role badge colors ────────────────────────────────────────────────────────

const ROLE_BG: Record<RoleId, string> = {
  admin:    '#9d7cff',
  author:   '#46c8d8',
  operator: '#e9a23e',
  copiador: '#4f9bf5',
  viewer:   '#525b6c',
};

// ── Component ────────────────────────────────────────────────────────────────

export function NavRail() {
  const pathname  = usePathname();
  const router    = useRouter();
  const t         = useTranslations('Nav');
  const role      = useUIStore((s) => s.role);
  const kycLevel  = useUIStore((s) => s.kycLevel);
  const openKill  = useUIStore((s) => s.openKillSwitch);
  const isLoggedIn= useUIStore((s) => s.isLoggedIn);
  const logoutFn  = useUIStore((s) => s.logout);
  const hydrate   = useUIStore((s) => s.hydrate);

  // Backend permission truth (C1.4) — drives lock state for GOBERNAR screens.
  const loadPerms = usePermissionsStore((s) => s.load);
  const isLocked  = usePermissionsStore((s) => s.isLocked);
  const permsLoaded = usePermissionsStore((s) => s.loaded);

  // Re-derive auth state from the stored JWT on mount (handles reload + token
  // present without persisted role, and clears stale role when token is gone).
  React.useEffect(() => {
    hydrate();
  }, [hydrate]);

  // Load backend permissions once logged in so lock state reflects me/permissions.
  React.useEffect(() => {
    if (isLoggedIn) loadPerms();
  }, [isLoggedIn, loadPerms]);

  const productEdition = isProductEdition();
  const platformMode = !productEdition && isPlatformMode();
  const groups = productEdition ? PRODUCT_GROUPS : platformMode ? PLATFORM_GROUPS : OPERATOR_GROUPS;
  const roleLabel = t(`role_${role}`);
  const initial   = roleLabel[0]?.toUpperCase() ?? 'U';

  return (
    <nav className="rail" aria-label={t('aria_main')}>
      {/* Brand */}
      <div className="rail-brand">
        <div className="brand-mark">
          <svg width="30" height="30" viewBox="0 0 30 30" fill="none">
            <rect width="30" height="30" rx="7" fill="var(--brand-soft)" />
            <path
              d="M9 21L15 9l6 12M11.5 17h7"
              stroke="var(--brand)"
              strokeWidth="1.8"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
        </div>
        <div>
          <div className="brand-name">
            {productEdition ? <>Anka<b>Guard</b></> : <>Anka<b>Arena</b></>}
          </div>
          <div className="brand-sub">{productEdition ? t('product_brand_sub') : 'Trading · IA'}</div>
        </div>
      </div>

      {/* Nav groups */}
      <div className="nav">
        {groups.map((g) => {
          const items = productEdition
            ? productMenuGroup(g.group as ProductGroup)
            : menuGroup(g.group, platformMode);
          // Product edition lists the emergency stop under "operate".
          const showKill = productEdition
            ? g.group === 'operate' && isLoggedIn
            : !platformMode && g.group === 'govern' && isLoggedIn;
          if (items.length === 0 && !showKill) return null;
          return (
            <div key={g.group}>
              <div className="nav-group-label">{t(g.labelKey as any)}</div>
              {items.map((item) => {
                // GOBERNAR (C3) screens defer to the backend permissions endpoint;
                // everything else uses the rbac.ts UX hint. Never hide a locked
                // screen — render it with the lock affordance.
                // Product edition: every listed screen is for the operator session.
                const allowed = productEdition
                  ? true
                  : PERMISSION_GOVERNED.has(item.slug)
                    ? (permsLoaded ? !isLocked(item.slug) : false)
                    : canAccess(role, item.slug);
                const isActive = pathname.startsWith(item.href.split('?')[0]);
                const Icon = SLUG_ICONS[item.slug] ?? IconAccount;

                return (
                  <Link
                    key={item.href}
                    href={allowed ? item.href : '#'}
                    className={clsx(
                      'nav-item',
                      isActive && 'active',
                      !allowed && 'locked',
                    )}
                    // The label text is hidden on narrow screens (icon rail):
                    // keep the accessible name and a tooltip.
                    aria-label={t(item.labelKey as any)}
                    title={t(item.labelKey as any)}
                    aria-current={isActive ? 'page' : undefined}
                    aria-disabled={!allowed}
                    tabIndex={allowed ? 0 : -1}
                  >
                    <span className="ni-ic">
                      <Icon />
                    </span>
                    <span style={{ flex: 1 }}>{t(item.labelKey as any)}</span>
                    {!allowed && (
                      <span className="ni-ic" aria-label={t('locked')}>
                        <IconLock />
                      </span>
                    )}
                  </Link>
                );
              })}
              {showKill && (
                <button
                  type="button"
                  className="nav-item"
                  aria-label={t('kill_switch')}
                  title={t('kill_switch')}
                  onClick={openKill}
                  style={{ width: '100%', background: 'none', border: 'none', textAlign: 'left', cursor: 'pointer' }}
                >
                  <span className="ni-ic">
                    <IconAdversarial />
                  </span>
                  <span style={{ flex: 1 }}>{t('kill_switch')}</span>
                </button>
              )}
            </div>
          );
        })}
      </div>

      {/* Footer: kill switch + role badge */}
      <div className="rail-foot">
        {isLoggedIn && (
          <button className="kill-nav" onClick={openKill} type="button">
            <span className="pulse-dot" />
            {t('kill_switch')}
          </button>
        )}

        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            gap: '9px',
            marginTop: '10px',
            padding: '7px 8px',
            borderRadius: 'var(--r)',
            border: '1px solid var(--line)',
            background: 'var(--bg-inset)',
          }}
        >
          {isLoggedIn ? (
            <>
              <div style={{ display: 'flex', alignItems: 'center', gap: '9px' }}>
                <div className="role-av" style={{ background: ROLE_BG[role] }} aria-hidden>
                  {initial}
                </div>
                <div className="role-meta">
                  <div className="rn">{roleLabel}</div>
                  <div className="rr">
                    {/* KYC levels belong to the platform; the product has one operator. */}
                    {productEdition ? t('product_role_badge') : `${roleLabel} · KYC ${kycLevel}`}
                  </div>
                </div>
              </div>
              <button 
                onClick={() => logoutFn()}
                style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--tx-dim)', fontSize: 12, padding: 4 }}
                title={t('logout')}
                aria-label={t('logout')}
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" />
                  <polyline points="16 17 21 12 16 7" />
                  <line x1="21" y1="12" x2="9" y2="12" />
                </svg>
              </button>
            </>
          ) : (
            <button
              onClick={() => router.push('/login')}
              style={{
                width: '100%',
                background: 'transparent',
                color: 'var(--tx-hi)',
                border: '1px solid var(--line)',
                padding: '6px 0',
                borderRadius: 4,
                fontFamily: 'var(--sans)',
                fontSize: 11,
                fontWeight: 500,
                cursor: 'pointer',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                gap: 6,
                transition: 'background 0.2s, color 0.2s',
              }}
              onMouseEnter={(e) => {
                e.currentTarget.style.background = 'var(--tx-hi)';
                e.currentTarget.style.color = 'var(--bg)';
              }}
              onMouseLeave={(e) => {
                e.currentTarget.style.background = 'transparent';
                e.currentTarget.style.color = 'var(--tx-hi)';
              }}
            >
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <path d="M15 3h4a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2h-4" />
                <polyline points="10 17 15 12 10 7" />
                <line x1="15" y1="12" x2="3" y2="12" />
              </svg>
              {t('login')}
            </button>
          )}
        </div>
      </div>
    </nav>
  );
}
