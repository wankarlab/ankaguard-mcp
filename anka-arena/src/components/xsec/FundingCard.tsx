'use client';

// FundingCard — the live funding cash flow of the current basket, otherwise
// invisible on the dashboard. 13 perpetuals accrue funding every 8h; on a
// market-neutral book the NET can be a real drag or tailwind. Data is the
// GET /api/v1/xsec/funding endpoint (live GCT rates, funding_archive is a
// stale research table). Polled on its own 60s key — the endpoint fans out
// N gRPC calls, so it must not ride the 15s status poll.

import { useTranslations } from 'next-intl';
import useSWR from 'swr';
import { getXsecFunding, type XSecFunding } from '@/lib/xsec-api';

function fmtUsd(n: number, signed = false): string {
  if (!isFinite(n)) return '—';
  const neg = n < 0;
  const abs = Math.abs(n).toLocaleString('en-US', { minimumFractionDigits: 4, maximumFractionDigits: 4 });
  const sign = neg ? '−' : signed && n > 0 ? '+' : '';
  return `${sign}$${abs}`;
}

function fmtRatePct(r: number): string {
  if (!isFinite(r)) return '—';
  const sign = r > 0 ? '+' : '';
  return `${sign}${(r * 100).toFixed(4)}%`;
}

function fmtNextTime(iso?: string | null): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (isNaN(d.getTime())) return '—';
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

function tone(n: number): string {
  return n > 0 ? 'var(--up)' : n < 0 ? 'var(--down)' : 'var(--tx-hi)';
}

export function FundingCard() {
  const t = useTranslations('Xsec');
  const { data } = useSWR<XSecFunding>('xsec:funding', getXsecFunding, {
    revalidateOnFocus: false,
    refreshInterval: 60_000,
  });

  if (!data || data.total_legs === 0) return null;

  const known = data.legs.filter((l) => l.funding_rate != null);

  return (
    <div>
      <div className="mono" style={{ fontSize: 10, letterSpacing: '.06em', color: 'var(--tx-faint)', textTransform: 'uppercase', marginBottom: 8 }}>
        {t('funding_title')}
      </div>

      {known.length === 0 ? (
        <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{t('funding_empty')}</div>
      ) : (
        <>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(120px, 1fr))', gap: 10, marginBottom: 10 }}>
            <div style={{ padding: '8px 10px', borderRadius: 'var(--r)', border: '1px solid var(--line)', background: 'var(--bg-inset)' }}>
              <div className="mono" style={{ fontSize: 9, color: 'var(--tx-faint)', textTransform: 'uppercase', letterSpacing: '.04em', marginBottom: 3 }}>{t('funding_net')}</div>
              <div className="mono" style={{ fontSize: 14, fontWeight: 700, color: tone(data.net_next_funding_usd) }}>{fmtUsd(data.net_next_funding_usd, true)}</div>
            </div>
            <div style={{ padding: '8px 10px', borderRadius: 'var(--r)', border: '1px solid var(--line)', background: 'var(--bg-inset)' }}>
              <div className="mono" style={{ fontSize: 9, color: 'var(--tx-faint)', textTransform: 'uppercase', letterSpacing: '.04em', marginBottom: 3 }}>{t('funding_daily')}</div>
              <div className="mono" style={{ fontSize: 14, fontWeight: 700, color: tone(data.est_daily_usd) }}>{fmtUsd(data.est_daily_usd, true)}</div>
            </div>
            <div style={{ padding: '8px 10px', borderRadius: 'var(--r)', border: '1px solid var(--line)', background: 'var(--bg-inset)' }}>
              <div className="mono" style={{ fontSize: 9, color: 'var(--tx-faint)', textTransform: 'uppercase', letterSpacing: '.04em', marginBottom: 3 }}>{t('funding_next_time')}</div>
              <div className="mono" style={{ fontSize: 14, fontWeight: 700, color: 'var(--tx-hi)' }}>{fmtNextTime(data.next_funding_time)}</div>
            </div>
          </div>

          <div style={{ overflowX: 'auto' }}>
            <table className="mono" style={{ width: '100%', borderCollapse: 'collapse', fontSize: 11 }}>
              <thead>
                <tr style={{ color: 'var(--tx-faint)', textAlign: 'left', textTransform: 'uppercase', fontSize: 10 }}>
                  <th style={{ padding: '3px 8px' }}>{t('col_pair')}</th>
                  <th style={{ padding: '3px 8px' }}>{t('col_side')}</th>
                  <th style={{ padding: '3px 8px', textAlign: 'right' }}>{t('funding_col_rate')}</th>
                  <th style={{ padding: '3px 8px', textAlign: 'right' }}>{t('funding_col_flow')}</th>
                </tr>
              </thead>
              <tbody>
                {data.legs.map((l) => (
                  <tr key={l.pair} style={{ borderTop: '1px solid var(--line)' }}>
                    <td style={{ padding: '4px 8px', color: 'var(--tx-hi)' }}>{l.pair}</td>
                    <td style={{ padding: '4px 8px', color: l.dir > 0 ? 'var(--up)' : 'var(--down)' }}>
                      {l.dir > 0 ? t('side_long') : t('side_short')}
                    </td>
                    <td style={{ padding: '4px 8px', textAlign: 'right', color: l.funding_rate == null ? 'var(--tx-faint)' : tone(l.funding_rate) }}>
                      {l.funding_rate == null ? t('funding_unknown') : fmtRatePct(l.funding_rate)}
                    </td>
                    <td style={{ padding: '4px 8px', textAlign: 'right', color: l.next_funding_usd == null ? 'var(--tx-faint)' : tone(l.next_funding_usd) }}>
                      {l.next_funding_usd == null ? '—' : fmtUsd(l.next_funding_usd, true)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', marginTop: 6 }}>
            {t('funding_note')}
            {data.known_legs < data.total_legs && (
              <> · {t('funding_partial', { known: data.known_legs, total: data.total_legs })}</>
            )}
          </div>
        </>
      )}
    </div>
  );
}
