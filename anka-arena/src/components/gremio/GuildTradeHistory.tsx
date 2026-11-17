'use client';

// GuildTradeHistory (C1.1 + C1.2) — recent closed trades for the gremio surface.
//
// C1.1 anti-survivorship: the backend trade-history endpoint returns losers too
// (no win/loss filter). The "solo ganadores" toggle is a FE-only convenience
// and carries a permanent disclaimer; the default view mixes wins and losses.
//
// C1.2 explainability: every row deep-links into the shared ExplainDrawer via
// useExplain().openExplain(tradeId) — the "por qué a un toque" rule from any
// surface, including the gremio.

import { useMemo, useState } from 'react';
import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { ApiError, fetchTradeHistory } from '@/lib/api';
import type { TradeHistoryRow } from '@/lib/adapters';
import { useExplain } from '@/lib/useExplain';
import { Spinner } from '@/components/ui/Spinner';

function fmt(n: number): string {
  return n.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

export function GuildTradeHistory() {
  const t = useTranslations('UiGremio');
  const { openExplain } = useExplain();
  const [winnersOnly, setWinnersOnly] = useState(false);

  const { data, error, isLoading } = useSWR<TradeHistoryRow[]>(
    '/api/v1/trades/history',
    () => fetchTradeHistory(),
    { refreshInterval: 60_000 },
  );

  const rows = useMemo(() => {
    const all = data ?? [];
    return winnersOnly ? all.filter((r) => r.pnl > 0) : all;
  }, [data, winnersOnly]);

  return (
    <div className="panel" style={{ overflow: 'hidden', marginTop: 16 }}>
      <div className="panel-h">
        <div style={{ flex: 1 }}>
          <h3>{t('history_title')}</h3>
          <span className="sub">/api/v1/trades/history · {t('history_includes_losses')}</span>
        </div>
        <label
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 6,
            fontSize: 11,
            color: 'var(--tx-dim)',
            cursor: 'pointer',
          }}
        >
          <input
            type="checkbox"
            checked={winnersOnly}
            onChange={(e) => setWinnersOnly(e.target.checked)}
            style={{ accentColor: 'var(--brand)' }}
          />
          {t('winners_only')}
        </label>
      </div>

      {/* Anti-survivorship disclaimer (permanent when filtering winners) */}
      {winnersOnly && (
        <div className="disclaimer" style={{ margin: 0, borderRadius: 0, borderLeft: 0, borderRight: 0, borderTop: 0 }}>
          <span style={{ color: 'var(--real)', fontSize: 10.5 }}>
            <b>{t('survivorship_bias')}</b> {t('survivorship_body')}
          </span>
        </div>
      )}

      {isLoading && (
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', padding: '32px 0', gap: 8, color: 'var(--tx-dim)' }}>
          <Spinner size={16} />
          <span className="mono" style={{ fontSize: 11 }}>{t('loading_history')}</span>
        </div>
      )}

      {error && (
        <div className="disclaimer" style={{ margin: 12 }}>
          <span>
            {error instanceof ApiError
              ? t('history_http_error', { status: error.status })
              : t('history_network_error')}
          </span>
        </div>
      )}

      {!isLoading && !error && (
        <div style={{ overflowX: 'auto' }}>
          <table className="tbl">
            <thead>
              <tr>
                <th>{t('pair')}</th>
                <th>{t('side')}</th>
                <th className="num">PnL</th>
                <th className="num">PnL %</th>
                <th>{t('regime')}</th>
                <th>{t('closed')}</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {rows.length === 0 && (
                <tr>
                  <td colSpan={7} style={{ textAlign: 'center', color: 'var(--tx-faint)', padding: '20px 12px', fontSize: 11.5 }}>
                    {t('no_trades')}
                  </td>
                </tr>
              )}
              {rows.map((row) => {
                const win = row.pnl >= 0;
                return (
                  <tr key={row.tradeId}>
                    <td>
                      <span className="mono hi" style={{ fontSize: 11.5 }}>{row.pair}</span>
                    </td>
                    <td>
                      <span className="mono" style={{ fontSize: 11, color: 'var(--tx-dim)' }}>
                        {row.side?.toUpperCase()}
                      </span>
                    </td>
                    <td className="num">
                      <span style={{ color: win ? 'var(--up)' : 'var(--down)' }}>
                        {win ? '+' : ''}${fmt(row.pnl)}
                      </span>
                    </td>
                    <td className="num">
                      <span style={{ color: win ? 'var(--up)' : 'var(--down)' }}>
                        {row.pnlPct >= 0 ? '+' : ''}{row.pnlPct.toFixed(2)}%
                      </span>
                    </td>
                    <td>
                      <span className="mono dim" style={{ fontSize: 11 }}>{row.regime || '—'}</span>
                    </td>
                    <td>
                      <span className="mono" style={{ fontSize: 10.5, color: 'var(--tx-faint)' }}>
                        {row.closedAt ? row.closedAt.slice(0, 16).replace('T', ' ') : '—'}
                      </span>
                    </td>
                    <td>
                      <button
                        className="btn sm ghost"
                        onClick={() => openExplain(row.tradeId)}
                        disabled={!row.tradeId}
                        title={t('why_title')}
                        style={{ fontSize: 11 }}
                      >
                        {t('why')}
                      </button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
