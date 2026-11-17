'use client';

// Read-only kill switch state from the backend (ring 0, frozen specs, isolated
// tenants, last action). Only fetched while mounted (i.e. while the modal is open).

import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { isProductEdition } from '@/lib/productMode';
import { Ring0ActiveBanner } from './Ring0Panels';
import { getKillSwitchStatus, isStatusForbidden, type KillSwitchStatus } from '@/lib/killswitch-status';

export function KillSwitchStatusPanel() {
  const t = useTranslations('KillSwitch');
  const { data, error, isLoading } = useSWR<KillSwitchStatus>(
    'killswitch:status',
    getKillSwitchStatus,
    { revalidateOnFocus: false, shouldRetryOnError: false, refreshInterval: 10_000 },
  );

  const box: React.CSSProperties = {
    marginTop: 14, padding: '10px 12px', borderRadius: 8,
    border: '1px solid var(--line)', background: 'var(--bg-void)', fontSize: 11,
  };

  if (isStatusForbidden(error)) {
    return <div style={{ ...box, color: 'var(--tx-faint)' }}>{t('status_forbidden')}</div>;
  }
  if (error) {
    return <div style={{ ...box, color: 'var(--down)' }}>{t('status_error')}</div>;
  }
  if (isLoading || !data) {
    return <div style={{ ...box, color: 'var(--tx-faint)' }}>{t('status_loading')}</div>;
  }

  const specs = data.ring1_specs ?? [];
  const tenants = data.ring2_tenants ?? [];
  const product = isProductEdition();
  const when = data.acted_at ? new Date(data.acted_at) : null;
  const whenText = when && !isNaN(when.getTime()) ? when.toLocaleString() : null;

  return (
    <div style={{ ...box, borderColor: data.ring0_active ? '#f0564f' : 'var(--line)' }}>
      <div className="eyebrow" style={{ fontSize: 10, letterSpacing: '.08em', color: 'var(--tx-faint)', textTransform: 'uppercase', marginBottom: 6 }}>
        {t('status_title')}
      </div>
      {data.ring0_active ? (
        <Ring0ActiveBanner />
      ) : (
        <div style={{ color: 'var(--tx)', fontWeight: 600 }}>
          {t(product ? 'product_status_inactive' : 'status_ring0_inactive')}
        </div>
      )}
      {/* Specs and tenants are platform concepts: not shown in the product. */}
      {!product && (
        <>
          <div className="mono" style={{ marginTop: 4, color: 'var(--tx)' }}>
            {t('status_specs', { count: specs.length })}{specs.length > 0 ? `: ${specs.join(', ')}` : ''}
          </div>
          <div className="mono" style={{ marginTop: 2, color: 'var(--tx)' }}>
            {t('status_tenants', { count: tenants.length })}{tenants.length > 0 ? `: ${tenants.join(', ')}` : ''}
          </div>
        </>
      )}
      <div className="mono" style={{ marginTop: 4, color: 'var(--tx-faint)' }}>
        {data.last_action
          ? t('status_last_action', {
              action: data.last_action,
              by: data.acted_by || '—',
              at: whenText ?? '—',
            })
          : t('status_no_action')}
      </div>
    </div>
  );
}
