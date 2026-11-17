'use client';

// AutoconfigCard — compact result of the read-only `autoconfig` job: the biggest
// k that fills k longs + k shorts at 1x on the real equity, the pairs, and the
// k values that were tried (so the operator sees why a bigger k was rejected).

import { useTranslations } from 'next-intl';
import type { AutoconfigResult } from '@/lib/xsec-api';

const usd = (n: number) => `$${n.toFixed(2)}`;

export function AutoconfigCard({ result }: { result: AutoconfigResult }) {
  const t = useTranslations('Xsec');
  const reasonKey =
    result.reason === 'capital_below_min_leg' || result.reason === 'below_validated_k' || result.reason === 'no_balanced_k' || result.reason === 'equity_unavailable'
      ? `autoconfig_reason_${result.reason}`
      : null;

  return (
    <div data-testid="autoconfig-card" style={{ marginTop: 10, display: 'flex', flexDirection: 'column', gap: 6 }}>
      {result.feasible ? (
        <>
          <div className="mono" data-testid="autoconfig-summary" style={{ fontSize: 11, color: 'var(--up)' }}>
            {t('autoconfig_summary', {
              k: result.k,
              per: usd(result.per_leg_usd),
              total: usd(result.max_total_usd),
              equity: usd(result.equity),
              util: result.utilization,
            })}
          </div>
          <div className="mono" style={{ fontSize: 10, color: 'var(--tx-dim)' }}>
            {t('autoconfig_longs')}: {result.longs.join(', ') || '—'}
          </div>
          <div className="mono" style={{ fontSize: 10, color: 'var(--tx-dim)' }}>
            {t('autoconfig_shorts')}: {result.shorts.join(', ') || '—'}
          </div>
        </>
      ) : result.reason === 'below_validated_k' ? (
        <div className="mono" data-testid="autoconfig-below-min-k" style={{ fontSize: 11, color: 'var(--warn)' }}>
          {t('autoconfig_below_validated_k', {
            equity: usd(result.equity),
            best: result.best_balanced_k,
            min: result.min_k,
            reqMin: usd(result.required_equity_min_k_usd),
            reqTarget: usd(result.required_equity_target_k_usd),
            target: result.required_equity_min_k_usd > 0 ? Math.round((result.required_equity_target_k_usd / result.required_equity_min_k_usd) * result.min_k) : result.min_k,
          })}
        </div>
      ) : (
        <div className="mono" data-testid="autoconfig-infeasible" style={{ fontSize: 11, color: 'var(--down)' }}>
          {t('autoconfig_infeasible')}: {reasonKey ? t(reasonKey) : result.reason}
        </div>
      )}
      {result.tried.length > 0 && (
        <div style={{ overflowX: 'auto' }}>
          <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', marginBottom: 2 }}>{t('autoconfig_tried_title')}</div>
          <table className="mono" data-testid="autoconfig-tried" style={{ fontSize: 10, color: 'var(--tx-dim)', borderCollapse: 'collapse' }}>
            <thead>
              <tr>
                <th style={cell}>k</th>
                <th style={cell}>{t('autoconfig_tried_per_leg')}</th>
                <th style={cell}>{t('autoconfig_tried_longs')}</th>
                <th style={cell}>{t('autoconfig_tried_shorts')}</th>
              </tr>
            </thead>
            <tbody>
              {result.tried.map((r) => (
                <tr key={r.k}>
                  <td style={cell}>{r.k}</td>
                  <td style={cell}>{usd(r.per_leg_usd)}</td>
                  <td style={cell}>{r.longs}/{r.k}</td>
                  <td style={cell}>{r.shorts}/{r.k}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

const cell: React.CSSProperties = { padding: '2px 10px 2px 0', textAlign: 'left', fontWeight: 400 };
