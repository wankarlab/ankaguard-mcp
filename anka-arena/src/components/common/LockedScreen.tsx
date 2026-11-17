'use client';

/**
 * LockedScreen — reusable read-only / locked banner (C1.4).
 *
 * Truth comes from GET /api/v1/me/permissions (usePermissionsStore), NOT from
 * the client-side rbac.ts hints. Locked screens are NEVER hidden — they render
 * with this banner showing the required level and how to unlock.
 *
 * Two usage modes:
 *  1. <LockedScreen screen="tribunal">{children}</LockedScreen>
 *     Renders children when unlocked; renders the locked banner otherwise.
 *  2. <LockedScreen screen="tribunal" bannerOnly />
 *     Always renders just the banner (e.g. above a read-only view).
 */

import React from 'react';
import { useTranslations } from 'next-intl';
import { usePermissionsStore } from '@/stores/permissions';

interface LockedScreenProps {
  /** Screen slug as known by the backend permissions.locks[].screen. */
  screen: string;
  /** Content to render when the screen is unlocked. */
  children?: React.ReactNode;
  /** Render only the banner regardless of lock state (for read-only headers). */
  bannerOnly?: boolean;
  /** Optional title override for the banner. */
  title?: string;
}

function IconLockLarge() {
  return (
    <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
      <rect x="4" y="11" width="16" height="10" rx="2" />
      <path d="M8 11V7a4 4 0 0 1 8 0v4" />
      <circle cx="12" cy="16" r="1.2" fill="currentColor" stroke="none" />
    </svg>
  );
}

export function LockedScreen({ screen, children, bannerOnly = false, title }: LockedScreenProps) {
  const t = useTranslations('Locked');
  const load     = usePermissionsStore((s) => s.load);
  const loaded   = usePermissionsStore((s) => s.loaded);
  const lockFor  = usePermissionsStore((s) => s.lockFor);

  React.useEffect(() => {
    load();
  }, [load]);

  const lock = lockFor(screen);
  const locked = lock?.locked ?? false;

  // Unlocked (or no lock descriptor) and not banner-only → render children.
  if (!bannerOnly && !locked) {
    return <>{children}</>;
  }

  const banner = (
    <div
      role="status"
      style={{
        display: 'flex',
        alignItems: 'flex-start',
        gap: 14,
        padding: '16px 18px',
        borderRadius: 'var(--r)',
        border: '1px solid var(--line-2)',
        background: 'var(--bg-inset)',
        color: 'var(--tx)',
      }}
    >
      <span style={{ color: 'var(--micro, var(--tx-dim))', flexShrink: 0, marginTop: 2 }}>
        <IconLockLarge />
      </span>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div
          style={{
            fontFamily: 'var(--sans)',
            fontSize: 14,
            fontWeight: 600,
            color: 'var(--tx-hi)',
            marginBottom: 4,
          }}
        >
          {title ?? t('title')}
        </div>
        {lock?.requiredLevel ? (
          <div
            style={{
              fontFamily: 'var(--mono)',
              fontSize: 11,
              letterSpacing: '0.04em',
              color: 'var(--tx-faint)',
              marginBottom: 6,
            }}
          >
            {t('required_level', { n: lock.requiredLevel })}
          </div>
        ) : null}
        <div style={{ fontFamily: 'var(--sans)', fontSize: 12.5, color: 'var(--tx-dim)' }}>
          {lock?.howToUnlock || t('no_permission')}
        </div>
      </div>
    </div>
  );

  // While permissions are loading we still show the banner skeleton-free; once
  // loaded the lock descriptor drives the copy. Render children below the banner
  // only in banner-only mode (read-only header above content).
  if (bannerOnly) {
    return (
      <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
        {banner}
        {children}
      </div>
    );
  }

  // Avoid flashing a locked banner before permissions resolve.
  if (!loaded) return null;

  return banner;
}

export default LockedScreen;
