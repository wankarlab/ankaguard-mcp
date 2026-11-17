'use client';

/**
 * /auth/callback — OAuth (Google) redirect landing.
 *
 * After the user authorizes with Google, RantiPay Auth (api.rantipay.com) redirects
 * back here with the freshly minted tokens. We accept them from either the query
 * string or the URL hash (servers differ), persist them, and continue to the role's landing screen.
 *
 * Accepted params: access_token + refresh_token, or a single `token`.
 * The auth server's configured frontend redirect URI must point to this route.
 */

import { useEffect, useState } from 'react';
import { useRouter } from '@/i18n/routing';
import { useTranslations } from 'next-intl';
import { type RoleId } from '@/lib/rbac';
import { landingPath } from '@/lib/consoleMode';
import { Spinner } from '@/components/ui/Spinner';
import { adoptTokens } from '@/lib/auth';
import { useUIStore } from '@/stores/ui';

export default function OAuthCallbackPage() {
  const router = useRouter();
  const t = useTranslations('LoginFlow');
  const loginFn = useUIStore((s) => s.login);
  const [error, setError] = useState('');

  useEffect(() => {
    function readParams(): URLSearchParams {
      const search = new URLSearchParams(window.location.search);
      // Some providers return tokens in the hash fragment (#access_token=…).
      const hash = window.location.hash.startsWith('#')
        ? new URLSearchParams(window.location.hash.slice(1))
        : new URLSearchParams();
      hash.forEach((v, k) => { if (!search.has(k)) search.set(k, v); });
      return search;
    }

    const p = readParams();
    const err = p.get('error') || p.get('error_description');
    if (err) {
      setError(decodeURIComponent(err));
      return;
    }

    const access = p.get('access_token') || p.get('token');
    const refresh = p.get('refresh_token') || undefined;

    if (!access) {
      setError(t('cb_no_token'));
      return;
    }

    const claims = adoptTokens(access, refresh);
    if (!claims) {
      setError(t('cb_invalid_token'));
      return;
    }

    loginFn(claims.role, claims.kyc);
    router.replace(landingPath(claims.role as RoleId));
  }, [router, loginFn, t]);

  return (
    <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', minHeight: 'calc(100vh - 100px)', gap: 14, padding: 20 }}>
      <style>{`.rail{display:none!important}.topbar{display:none!important}.app{grid-template-columns:1fr!important}`}</style>
      {error ? (
        <>
          <span style={{ color: 'var(--real)', fontSize: 13, fontWeight: 600 }}>{t('cb_failed')}</span>
          <span className="mono" style={{ fontSize: 11, color: 'var(--tx-dim)', maxWidth: 360, textAlign: 'center' }}>{error}</span>
          <button className="btn sm" onClick={() => router.replace('/login')} style={{ marginTop: 8 }}>
            {t('cb_back')}
          </button>
        </>
      ) : (
        <>
          <Spinner size={28} />
          <span className="mono" style={{ fontSize: 12, color: 'var(--tx-dim)' }}>{t('cb_completing')}</span>
        </>
      )}
    </div>
  );
}
