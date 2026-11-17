'use client';

// Presentational pieces of the license screen and home notice. They take the
// status as a prop so they can be rendered without a backend.

import { useTranslations } from 'next-intl';
import { Link } from '@/i18n/routing';
import { expiresSoon, hasNoLicense, type LicenseStatus } from '@/lib/product-api';
import { Notice } from './shared';
import { useLicense } from './hooks';

function Field({ label, value, color }: { label: string; value: string; color?: string }) {
  return (
    <div className="panel-inset" style={{ padding: '10px 12px' }}>
      <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', textTransform: 'uppercase', letterSpacing: '.06em' }}>{label}</div>
      <div className="mono" style={{ fontSize: 15, fontWeight: 700, color: color ?? 'var(--tx-hi)', marginTop: 4, overflowWrap: 'anywhere' }}>{value}</div>
    </div>
  );
}

const STATE_COLOR: Record<LicenseStatus['state'], string> = {
  valid: 'var(--up)',
  grace: 'var(--warn)',
  expired: 'var(--down)',
  invalid: 'var(--down)',
};

/** Warning that the license expires in 14 days or less (renders nothing otherwise). */
export function LicenseExpiryWarning({ status, withLink }: { status: LicenseStatus | undefined; withLink?: boolean }) {
  const t = useTranslations('Product');
  if (!expiresSoon(status) || !status || status.days_remaining == null) return null;
  return (
    <Notice tone="warn" title={t('expiry_title')}>
      {t('expiry_text', { days: status.days_remaining })}
      {withLink && (
        <>
          {' '}
          <Link href="/license" style={{ color: 'var(--brand)' }}>{t('expiry_link')}</Link>
        </>
      )}
    </Notice>
  );
}

/** Edition, holder, expiry, days left and state. Missing values render as a dash, never as examples. */
export function LicenseCard({ status }: { status: LicenseStatus }) {
  const t = useTranslations('Product');
  const dash = '—';
  const expires = status.expires_at ? status.expires_at.slice(0, 10) : dash;
  // Nothing installed yet reads as "no license", not as a broken one.
  const none = hasNoLicense(status);
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 200px), 1fr))', gap: 10 }}>
        <Field label={t('lic_edition')} value={status.edition === 'pro' ? t('lic_edition_pro') : t('lic_edition_community')} />
        <Field
          label={t('lic_state')}
          value={none ? t('lic_state_none') : t(`lic_state_${status.state}`)}
          color={none ? 'var(--tx-dim)' : STATE_COLOR[status.state]}
        />
        <Field label={t('lic_holder')} value={status.holder ?? dash} />
        <Field label={t('lic_expires')} value={expires} />
        <Field label={t('lic_days')} value={status.days_remaining == null ? dash : String(status.days_remaining)} />
      </div>
      {status.warning === 'in_grace' && (
        <Notice tone="warn">{t('lic_warn_grace', { days: status.grace_days_remaining ?? 0 })}</Notice>
      )}
      {status.warning === 'clock_skew' && <Notice tone="warn">{t('lic_warn_clock')}</Notice>}
    </div>
  );
}

/** Home notice: fetches the license itself and stays silent when there is nothing to warn about. */
export function HomeLicenseNotice() {
  const { data } = useLicense();
  return <LicenseExpiryWarning status={data} withLink />;
}
