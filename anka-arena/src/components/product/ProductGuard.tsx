'use client';

// ProductGuard — license gate for the product edition. Without a valid Pro
// license only login, system health and the license screen render; the rest
// shows a blocked state with the reason. The emergency stop is the global modal
// in the layout, outside this guard, so it always works.

import { useEffect, useSyncExternalStore, type ReactNode } from 'react';
import { Link, usePathname, useRouter } from '@/i18n/routing';
import { getStoredToken } from '@/lib/auth';
import { useTranslations } from 'next-intl';
import { ApiError } from '@/lib/api';
import { blockedReason, describeApiError } from '@/lib/product-api';
import { isLicenseExempt, isProductEdition } from '@/lib/productMode';
import { Spinner } from '@/components/ui/Spinner';
import { useLicense } from './hooks';
import { Notice } from './shared';

export type BlockedKind = 'none' | 'expired' | 'invalid' | 'session' | 'unknown';

export function LicenseBlocked({ kind, detail }: { kind: BlockedKind; detail?: string }) {
  const t = useTranslations('Product');
  return (
    <div className="content-pad" style={{ maxWidth: 640, margin: '0 auto' }} data-blocked={kind}>
      <Notice tone={kind === 'session' ? 'info' : 'warn'} title={t('blocked_title')}>
        <div>{t(`blocked_${kind}`)}</div>
        {detail && <div className="mono" style={{ fontSize: 11, marginTop: 6, color: 'var(--tx-dim)' }}>{detail}</div>}
        <div style={{ marginTop: 10, display: 'flex', gap: 14, flexWrap: 'wrap' }}>
          {kind === 'session' ? (
            <Link href="/login" style={{ color: 'var(--brand)', fontWeight: 600 }}>{t('blocked_login')}</Link>
          ) : (
            <Link href="/license" style={{ color: 'var(--brand)', fontWeight: 600 }}>{t('blocked_go_license')}</Link>
          )}
          <span className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{t('blocked_estop')}</span>
        </div>
      </Notice>
    </div>
  );
}

function Gate({ children }: { children: ReactNode }) {
  const { data, error, isLoading } = useLicense();
  if (isLoading && !data) {
    return <div style={{ display: 'flex', justifyContent: 'center', padding: 40 }}><Spinner /></div>;
  }
  if (!data) {
    if (error instanceof ApiError && error.status === 401) return <LicenseBlocked kind="session" />;
    return <LicenseBlocked kind="unknown" detail={describeApiError(error, '')} />;
  }
  const reason = blockedReason(data);
  if (reason) return <LicenseBlocked kind={reason} />;
  return <>{children}</>;
}

/**
 * True when the screen needs an operator session: in the product every screen
 * but the login does (health and license are license-exempt, not public).
 */
export function needsSession(path: string, hasToken: boolean): boolean {
  return !hasToken && !/(^|\/)login\/?$/.test(path);
}

// The stored token is read through useSyncExternalStore: the server snapshot
// assumes a session, so hydration matches and the client re-renders at once.
const noSubscribe = () => () => {};
const hasStoredToken = () => !!getStoredToken();
const assumeSession = () => true;

function SessionRequired() {
  const router = useRouter();
  useEffect(() => { router.replace('/login'); }, [router]);
  return <LicenseBlocked kind="session" />;
}

export function ProductGuard({ children }: { children: ReactNode }) {
  const pathname = usePathname();
  const hasToken = useSyncExternalStore(noSubscribe, hasStoredToken, assumeSession);
  if (!isProductEdition()) return <>{children}</>;
  // Without a session there is nothing to show: the data calls would only
  // answer 401 (a token that expired is handled by apiFetch's redirect).
  if (needsSession(pathname, hasToken)) return <SessionRequired />;
  if (isLicenseExempt(pathname)) return <>{children}</>;
  return <Gate>{children}</Gate>;
}
