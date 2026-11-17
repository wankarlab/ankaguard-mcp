'use client';

// Kill switch status. The endpoint needs level 4: 401/403 is shown as
// "no permission", not as an error. The block opens the global modal.

import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { useUIStore } from '@/stores/ui';
import { isProductEdition } from '@/lib/productMode';
import { getKillSwitchStatus, isStatusForbidden, type KillSwitchStatus } from '@/lib/killswitch-status';
import { Ring0ActiveBanner } from '@/components/killswitch/Ring0Panels';
import { HomeBlock, BlockMessage } from './HomeBlock';

export function HomeKillSwitchBlock() {
  const t = useTranslations('Home');
  const openKill = useUIStore((s) => s.openKillSwitch);
  const { data, error, isLoading } = useSWR<KillSwitchStatus>('home:killswitch', getKillSwitchStatus, {
    revalidateOnFocus: false,
    refreshInterval: 30_000,
    shouldRetryOnError: false,
  });

  const forbidden = !!error && isStatusForbidden(error);
  const specs = data?.ring1_specs?.length ?? 0;
  const tenants = data?.ring2_tenants?.length ?? 0;
  // Specs and tenants (rings 1 and 2) do not exist in the product.
  const product = isProductEdition();

  return (
    <HomeBlock title={t('kill_title')}>
      {isLoading && !data && <BlockMessage text={t('loading')} />}
      {forbidden && <BlockMessage text={t('kill_forbidden')} />}
      {error && !data && !forbidden && <BlockMessage text={t('unavailable')} tone="error" />}
      {data?.ring0_active && <Ring0ActiveBanner compact />}
      {data && (
        <div className="mono" style={{ fontSize: 12, display: 'flex', flexDirection: 'column', gap: 4 }}>
          {!data.ring0_active && (
            <span style={{ color: 'var(--up)', fontWeight: 700 }}>{t(product ? 'product_kill_inactive' : 'kill_ring0_inactive')}</span>
          )}
          {!product && <span style={{ color: 'var(--tx-dim)' }}>{t('kill_rings', { specs, tenants })}</span>}
        </div>
      )}
      {!forbidden && (
        <button
          type="button"
          onClick={openKill}
          className="mono"
          style={{ marginTop: 10, background: 'none', border: '1px solid var(--line)', borderRadius: 'var(--r)', padding: '5px 10px', cursor: 'pointer', fontSize: 11, color: 'var(--brand)' }}
        >
          {t(product ? 'product_kill_open' : 'kill_open')} →
        </button>
      )}
    </HomeBlock>
  );
}
