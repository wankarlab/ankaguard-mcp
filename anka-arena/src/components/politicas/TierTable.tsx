'use client';

import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { apiFetch } from '@/lib/api';

interface Tier {
  name: string;
  price_usd: number;
  max_bots: number | string;
  max_exchanges: number | string;
  max_order_usd: string;
  real_trading: boolean;
}

// Raw tier item as returned by the backend (all fields optional).
interface TierPayload {
  name?: string;
  price_usd?: number;
  price_monthly_usd?: number;
  max_bots?: number | string;
  max_exchanges?: number | string;
  max_pairs?: number | string;
  max_order_usd?: string;
  max_order_size_usd?: number | string;
  real_trading?: boolean;
}

// Backend: { data: { tiers: [{name,price_monthly_usd,max_bots,max_pairs,...}] } }.
const fetcher = async (path: string): Promise<Tier[]> => {
  const d = await apiFetch<TierPayload[] | { tiers?: TierPayload[] } | null>(path);
  const list: TierPayload[] = Array.isArray(d) ? d : (d?.tiers ?? []);
  return list.map((tier) => ({
    name:          tier.name ?? '—',
    price_usd:     tier.price_usd ?? tier.price_monthly_usd ?? 0,
    max_bots:      tier.max_bots ?? '—',
    max_exchanges: tier.max_exchanges ?? tier.max_pairs ?? '—',
    max_order_usd: tier.max_order_usd ?? (tier.max_order_size_usd ? `$${tier.max_order_size_usd}` : '—'),
    real_trading:  tier.real_trading ?? !String(tier.name ?? '').toLowerCase().includes('free'),
  }));
};

export function TierTable() {
  const t = useTranslations('UiPolicies');
  const { data, error, isLoading } = useSWR<Tier[]>(
    '/api/v1/admin/tiers',
    fetcher,
    { revalidateOnFocus: false },
  );

  if (isLoading) {
    return (
      <div style={{ padding: 16 }}>
        <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}>
          {t('loading_tiers')}
        </div>
      </div>
    );
  }

  // Anti-hardcode rule: never fabricate tier prices in the client. On error we
  // surface the real backend failure; the FE only renders real data.
  if (error) {
    return (
      <div style={{ padding: 16 }}>
        <div className="mono" style={{ fontSize: 11, color: '#f0564f' }}>
          {t('tiers_error')}
        </div>
      </div>
    );
  }

  const tiers = data ?? [];

  if (tiers.length === 0) {
    return (
      <div style={{ padding: 16 }}>
        <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}>
          {t('tiers_empty')}
        </div>
      </div>
    );
  }

  return (
    <div style={{ overflowX: 'auto' }}>
      <table
        className="tbl"
        style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}
      >
        <thead>
          <tr>
            {[
              { id: 'tier', label: 'Tier' },
              { id: 'price', label: t('per_month') },
              { id: 'bots', label: 'MaxBots' },
              { id: 'exchanges', label: 'Exchanges' },
              { id: 'order', label: 'MaxOrder' },
              { id: 'real', label: 'Real trading' },
            ].map((h) => (
              <th
                key={h.id}
                style={{
                  padding: '8px 14px',
                  textAlign: h.id === 'tier' ? 'left' : 'right',
                  fontSize: 10,
                  fontWeight: 600,
                  color: 'var(--tx-faint)',
                  textTransform: 'uppercase',
                  letterSpacing: '.05em',
                  borderBottom: '1px solid var(--line)',
                  whiteSpace: 'nowrap',
                }}
              >
                {h.label}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {tiers.map((tier, i) => (
            <tr
              key={tier.name}
              style={{
                background: i % 2 === 0 ? 'transparent' : 'var(--bg-void)',
              }}
            >
              <td
                style={{
                  padding: '9px 14px',
                  fontWeight: 600,
                  color: 'var(--tx-hi)',
                }}
              >
                {tier.name}
              </td>
              <td className="mono" style={{ padding: '9px 14px', textAlign: 'right', color: 'var(--tx-hi)' }}>
                ${tier.price_usd}
              </td>
              <td className="mono" style={{ padding: '9px 14px', textAlign: 'right', color: 'var(--tx)' }}>
                {tier.max_bots}
              </td>
              <td className="mono" style={{ padding: '9px 14px', textAlign: 'right', color: 'var(--tx)' }}>
                {tier.max_exchanges}
              </td>
              <td className="mono" style={{ padding: '9px 14px', textAlign: 'right', fontSize: 11, color: 'var(--tx)' }}>
                {tier.max_order_usd}
              </td>
              <td style={{ padding: '9px 14px', textAlign: 'right' }}>
                {tier.real_trading ? (
                  <span style={{ color: '#4adb84', fontWeight: 600 }}>✓</span>
                ) : (
                  <span style={{ color: 'var(--tx-faint)' }}>✕</span>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
