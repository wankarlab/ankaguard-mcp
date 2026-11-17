'use client';

// XSEC basket + active alerts. Uses the exact 'xsec:status' SWR key (and the
// same fetcher/options) as the XSec screen so both share one cache entry.

import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { getXsecStatus, type XSecStatus } from '@/lib/xsec-api';
import { fmtOrDash } from '@/lib/home-logic';
import { HomeBlock, BlockMessage, Stat } from './HomeBlock';

export function HomeBasketBlock() {
  const t = useTranslations('Home');
  const { data, error, isLoading } = useSWR<XSecStatus>('xsec:status', getXsecStatus, {
    revalidateOnFocus: false,
    refreshInterval: 15_000,
  });

  const totals = data?.totals;
  const hasBasket = !!totals && (totals.n_long > 0 || totals.n_short > 0);
  const alerts = data?.active_alerts ?? [];
  const total = totals ? totals.n_long + totals.n_short : 0;
  // Imbalance is derived only from the real leg counts reported by the backend.
  const neutral = hasBasket ? totals.n_long === totals.n_short : null;

  return (
    <HomeBlock title={t('basket_title')} href="/xsec" linkLabel={t('link_detail')}>
      {isLoading && !data && <BlockMessage text={t('loading')} />}
      {error && !data && <BlockMessage text={t('unavailable')} tone="error" />}
      {data && !hasBasket && <BlockMessage text={t('basket_empty')} />}
      {data && hasBasket && totals && (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(130px, 1fr))', gap: 10 }}>
          <Stat label={t('basket_long')} value={fmtOrDash(totals.n_long, String)} />
          <Stat label={t('basket_short')} value={fmtOrDash(totals.n_short, String)} />
          <Stat
            label={t('basket_neutrality')}
            value={neutral ? t('basket_balanced') : t('basket_unbalanced', { long: totals.n_long, short: totals.n_short, total })}
            color={neutral ? 'var(--up)' : 'var(--warn)'}
          />
        </div>
      )}
      {data && (
        <div style={{ marginTop: 12 }}>
          <div className="mono" style={{ fontSize: 10, letterSpacing: '.06em', textTransform: 'uppercase', color: alerts.length ? 'var(--down)' : 'var(--tx-faint)', marginBottom: 6 }}>
            {t('alerts_title')} ({alerts.length})
          </div>
          {alerts.length === 0 && <BlockMessage text={t('alerts_none')} />}
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            {alerts.map((a) => (
              <div key={a.key} style={{ padding: '8px 12px', borderRadius: 'var(--r)', border: '1px solid var(--down)', background: 'rgba(233,90,90,.07)' }}>
                <div style={{ fontSize: 12, fontWeight: 700, color: 'var(--down)' }}>{a.title}</div>
                <div className="mono" style={{ fontSize: 11, color: 'var(--tx-dim)', marginTop: 2 }}>{a.body}</div>
              </div>
            ))}
          </div>
        </div>
      )}
    </HomeBlock>
  );
}
