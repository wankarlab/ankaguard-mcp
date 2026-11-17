'use client';

// FeeLedger (C0.4) — ALL economics come from the backend.
//
// The fee rate, author/treasury splits, per-period fee amount, and the per-row
// author/treasury split amounts are computed by the backend (dynamic config
// table billing.*). This component ONLY formats numbers. There is NO 20% /
// 70-30 / fee*0.3 anywhere in the client.

import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { ApiError } from '@/lib/api';
import { fetchFeeLedger } from '@/lib/api';
import type { FeeLedger as FeeLedgerData } from '@/lib/adapters';
import { Badge } from '@/components/ui/Badge';
import { Spinner } from '@/components/ui/Spinner';

// ---- Helpers ---------------------------------------------------------------

function fmt(n: number): string {
  return n.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

/** Render a fraction (0–1) as a whole-number percent, backend-sourced. */
function pct(fraction: number): string {
  return `${Math.round(fraction * 100)}%`;
}

// ---- Component ------------------------------------------------------------

interface FeeLedgerProps {
  userId: string;
}

export function FeeLedger({ userId }: FeeLedgerProps) {
  const t = useTranslations('UiGremio');
  const { data, error, isLoading } = useSWR<FeeLedgerData>(
    userId ? `/api/v1/billing/fees/${userId}` : null,
    () => fetchFeeLedger(userId),
    { refreshInterval: 120_000 },
  );

  const economy   = data?.economy;
  const periods   = data?.periods ?? [];
  const aggregate = data?.aggregate;

  // Economy labels are entirely backend-driven; render nothing concrete until loaded.
  const feeRateLabel  = economy ? pct(economy.feeRate) : '—';
  const splitLabel    = economy ? t('split_label', { author: pct(economy.authorSplit), treasury: pct(economy.treasurySplit) }) : '—';

  return (
    <div className="panel" style={{ overflow: 'hidden' }}>
      {/* Panel header */}
      <div className="panel-h">
        <div style={{ flex: 1 }}>
          <h3>{t('fee_ledger')}</h3>
          <span className="sub">
            HWM · {economy ? t('rate_on_new_pnl', { rate: feeRateLabel }) : t('loading_economy')}
          </span>
        </div>
        <Badge variant="brand" style={{ fontSize: 9 }}>
          {t('open_period')}
        </Badge>
      </div>

      {/* Body */}
      {isLoading && (
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            padding: '32px 0',
            gap: 8,
            color: 'var(--tx-dim)',
          }}
        >
          <Spinner size={16} />
          <span className="mono" style={{ fontSize: 11 }}>{t('loading_ledger')}</span>
        </div>
      )}

      {error && (
        <div className="disclaimer" style={{ margin: 12 }}>
          <span>
            {error instanceof ApiError
              ? t('ledger_http_error', { status: error.status })
              : t('ledger_network_error')}
          </span>
        </div>
      )}

      {!isLoading && !error && (
        <>
          <div style={{ overflowX: 'auto' }}>
            <table className="tbl">
              <thead>
                <tr>
                  <th>{t('period')}</th>
                  <th className="num">{t('new_pnl')}</th>
                  <th className="num">HWM</th>
                  <th className="num">{t('fee_col', { rate: feeRateLabel })}</th>
                  <th>{t('status')}</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {periods.length === 0 && (
                  <tr>
                    <td
                      colSpan={6}
                      style={{ textAlign: 'center', color: 'var(--tx-faint)', padding: '20px 12px', fontSize: 11.5 }}
                    >
                      {t('no_periods')}
                    </td>
                  </tr>
                )}
                {periods.map((p) => {
                  const periodLabel = p.periodStart
                    ? p.periodStart.slice(0, 7) // YYYY-MM
                    : p.id;
                  const isPending = p.status === 'pending';

                  return (
                    <tr key={p.id || p.periodStart}>
                      {/* Período */}
                      <td>
                        <span className="mono" style={{ fontSize: 12, color: 'var(--tx-hi)' }}>
                          {periodLabel}
                        </span>
                      </td>

                      {/* PnL nuevo (period net PnL sum) */}
                      <td className="num">
                        <span style={{ color: p.periodNetPnlSum >= 0 ? 'var(--up)' : 'var(--down)' }}>
                          {p.periodNetPnlSum >= 0 ? '+' : ''}${fmt(p.periodNetPnlSum)}
                        </span>
                      </td>

                      {/* HWM */}
                      <td className="num">
                        <span className="hi">${fmt(p.highWaterMark)}</span>
                      </td>

                      {/* Fee — amounts are backend-computed (authorAmount/treasuryAmount) */}
                      <td className="num">
                        <div style={{ lineHeight: 1.3 }}>
                          <div className="hi">${fmt(p.feeUsd)}</div>
                          <div style={{ fontSize: 9.5, color: 'var(--tx-faint)', fontFamily: 'var(--mono)' }}>
                            {t('row_split', { author: fmt(p.authorAmount), treasury: fmt(p.treasuryAmount) })}
                          </div>
                        </div>
                      </td>

                      {/* Estado */}
                      <td>
                        {isPending ? (
                          <Badge variant="micro" style={{ fontSize: 9 }}>
                            {p.status}
                          </Badge>
                        ) : (
                          <Badge variant="ok" style={{ fontSize: 9 }}>
                            {p.status || 'collected'}
                          </Badge>
                        )}
                      </td>

                      {/* Cobro — legal-gated */}
                      <td>
                        {isPending ? (
                          <button
                            className="btn sm"
                            disabled
                            title={t('legal_gated_title')}
                            style={{ opacity: 0.45, cursor: 'not-allowed', fontSize: 10.5 }}
                          >
                            <Badge variant="micro" style={{ fontSize: 8.5, marginRight: 4 }}>
                              legal-gated
                            </Badge>
                            {t('charge')}
                          </button>
                        ) : (
                          <span style={{ color: 'var(--tx-faint)', fontSize: 11 }}>—</span>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>

          {/* Aggregate totals — all backend-computed */}
          {aggregate && periods.length > 0 && (
            <div
              style={{
                borderTop: '1px solid var(--line)',
                padding: '9px 14px',
                display: 'flex',
                justifyContent: 'space-between',
                flexWrap: 'wrap',
                gap: 8,
                fontSize: 11,
                color: 'var(--tx-dim)',
              }}
            >
              <span className="mono">{t('total_fee')} <b className="hi">${fmt(aggregate.totalFeeUsd)}</b></span>
              <span className="mono">
                {t('aggregate_split', { author: fmt(aggregate.totalAuthorAmount), treasury: fmt(aggregate.totalTreasuryAmount) })}
              </span>
            </div>
          )}

          {/* Footer note — labels reflect backend economy */}
          <div
            style={{
              borderTop: '1px solid var(--line)',
              padding: '9px 14px',
              fontSize: 10.5,
              color: 'var(--tx-faint)',
              fontFamily: 'var(--mono)',
              display: 'flex',
              justifyContent: 'space-between',
              flexWrap: 'wrap',
              gap: 4,
            }}
          >
            <span>{splitLabel}</span>
            <span>{t('hwm_note')}</span>
          </div>

          {data?.note && (
            <div style={{ padding: '0 14px 10px', fontSize: 10.5, color: 'var(--tx-faint)' }}>
              {data.note}
            </div>
          )}
        </>
      )}
    </div>
  );
}
