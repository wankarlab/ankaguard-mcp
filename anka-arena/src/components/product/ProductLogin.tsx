'use client';

// ProductLogin — login screen of the product edition. Asks the server for its
// auth mode: `standalone` shows the operator password form (or the set-password
// instructions), `external` renders the existing login untouched.

import { useEffect, useState, type ReactNode } from 'react';
import useSWR, { useSWRConfig } from 'swr';
import { useRouter } from '@/i18n/routing';
import { useTranslations } from 'next-intl';
import { Spinner } from '@/components/ui/Spinner';
import { useUIStore } from '@/stores/ui';
import { PRODUCT_HOME } from '@/lib/productMode';
import { formatWait, getLocalAuthMode, loginLocal, LocalLoginError, describeApiError } from '@/lib/product-api';
import { Notice, inputStyle, labelStyle } from './shared';

export const SET_PASSWORD_COMMAND = 'trading-mcp-server operator set-password';

/** Instructions shown instead of the form while the operator password is not set. */
export function PasswordNotSet() {
  const t = useTranslations('Product');
  return (
    <Notice tone="info" title={t('login_not_set_title')}>
      <div>{t('login_not_set_text')}</div>
      <code
        className="mono"
        style={{ display: 'block', margin: '8px 0', padding: '8px 10px', background: 'var(--bg)', border: '1px solid var(--line-2)', borderRadius: 'var(--r)', fontSize: 12, overflowWrap: 'anywhere' }}
      >
        {SET_PASSWORD_COMMAND}
      </code>
      <div>{t('login_not_set_after')}</div>
    </Notice>
  );
}

/** Lock message with the remaining wait. `seconds` null means the server sent no Retry-After. */
export function LoginLocked({ seconds }: { seconds: number | null }) {
  const t = useTranslations('Product');
  return (
    <Notice tone="warn" title={t('login_locked_title')}>
      {seconds == null ? t('login_locked_unknown') : t('login_locked_wait', { time: formatWait(seconds) })}
    </Notice>
  );
}

function Card({ children }: { children: ReactNode }) {
  const tl = useTranslations('Login');
  return (
    <div className="login-page" style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', minHeight: 'calc(100vh - 100px)', padding: 20 }}>
      {/* Same trick as the existing login: hide rail and topbar on this page. */}
      <style>{`
        .rail { display: none !important; }
        .topbar { display: none !important; }
        .app { grid-template-columns: 1fr !important; }
        .main { width: 100vw; height: 100vh; }
      `}</style>
      <div style={{ width: 380, maxWidth: 'calc(100vw - 40px)', background: 'var(--bg-card)', border: '1px solid var(--line)', borderRadius: 16, padding: '32px 28px', margin: 'auto' }}>
        <h1 style={{ fontSize: 22, color: 'var(--tx-hi)', margin: '0 0 6px', fontWeight: 600, textAlign: 'center' }}>{tl('product_title')}</h1>
        <div style={{ marginTop: 22 }}>{children}</div>
      </div>
    </div>
  );
}

function PasswordForm() {
  const t = useTranslations('Product');
  const router = useRouter();
  const loginFn = useUIStore((s) => s.login);
  const { mutate } = useSWRConfig();
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [lockedUntil, setLockedUntil] = useState<number | null>(null);
  const [lockUnknown, setLockUnknown] = useState(false);
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    if (lockedUntil == null) return;
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [lockedUntil]);

  const remaining = lockedUntil == null ? null : Math.max(0, Math.ceil((lockedUntil - now) / 1000));
  const locked = lockUnknown || (remaining != null && remaining > 0);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (busy || locked || !password) return;
    setBusy(true);
    setError(null);
    try {
      const claims = await loginLocal(password);
      setPassword('');
      loginFn(claims.role, claims.kyc);
      // A new session invalidates everything fetched before it: a 401 cached
      // from an earlier or expired session (no retry on error) would otherwise
      // keep showing "unknown mode" or a locked screen until a reload.
      void mutate(() => true);
      router.push(PRODUCT_HOME);
    } catch (err) {
      if (err instanceof LocalLoginError) {
        if (err.code === 'LOGIN_LOCKED') {
          const t0 = Date.now();
          setNow(t0);
          setLockedUntil(err.retryAfter == null ? null : t0 + err.retryAfter * 1000);
          setLockUnknown(err.retryAfter == null);
        } else if (err.code === 'INVALID_CREDENTIALS') setError(t('login_invalid'));
        else if (err.code === 'PASSWORD_NOT_SET') setError(t('login_not_set_title'));
        else setError(t('login_error', { status: err.status }));
      } else {
        setError(describeApiError(err, t('login_network')));
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
      <label>
        <span className="mono" style={labelStyle}>{t('login_password')}</span>
        <input
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          autoComplete="current-password"
          autoFocus
          style={inputStyle}
        />
      </label>
      <button type="submit" className="btn primary" disabled={busy || locked || !password} style={{ justifyContent: 'center' }}>
        {busy ? <Spinner size={14} color="#fff" /> : t('login_submit')}
      </button>
      {locked && <LoginLocked seconds={lockUnknown ? null : remaining} />}
      {error && !locked && <Notice tone="error">{error}</Notice>}
    </form>
  );
}

export function ProductLogin({ external }: { external: ReactNode }) {
  const t = useTranslations('Product');
  const { data, error, isLoading, mutate } = useSWR('product:auth-mode', getLocalAuthMode, {
    revalidateOnFocus: false,
    shouldRetryOnError: false,
  });

  if (data?.mode === 'external') return <>{external}</>;

  return (
    <Card>
      {isLoading && !data && (
        <div style={{ display: 'flex', justifyContent: 'center' }}><Spinner /></div>
      )}
      {error && !data && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          <Notice tone="error">{describeApiError(error, t('login_mode_error'))}</Notice>
          <button type="button" className="btn sm" onClick={() => mutate()} style={{ alignSelf: 'flex-start' }}>
            {t('login_retry')}
          </button>
        </div>
      )}
      {data?.mode === 'standalone' && (data.password_set ? <PasswordForm /> : <PasswordNotSet />)}
    </Card>
  );
}
