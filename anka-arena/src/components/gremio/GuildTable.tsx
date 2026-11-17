'use client';

import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { ApiError, fetchMarketplaceStrategies } from '@/lib/api';
import type { GuildStrategy } from '@/lib/adapters';
import { Badge } from '@/components/ui/Badge';
import { Spinner } from '@/components/ui/Spinner';

export type { GuildStrategy };

// ---- Fetcher — shared, backend-sourced (anti-survivorship: includes retired) --

const fetchStrategies = () => fetchMarketplaceStrategies();

// ---- Regime label ---------------------------------------------------------

const REGIME_CLASS: Record<GuildStrategy['regime'], string> = {
  TRENDING: 'regime-TRENDING',
  RANGING:  'regime-RANGING',
  BREAKOUT: 'regime-BREAKOUT',
  UNKNOWN:  'dim',
};

// ---- Component ------------------------------------------------------------

interface GuildTableProps {
  onCopy: (strategy: GuildStrategy) => void;
}

export function GuildTable({ onCopy }: GuildTableProps) {
  const t = useTranslations('UiGremio');
  const { data, error, isLoading } = useSWR<GuildStrategy[]>(
    '/api/v1/marketplace/strategies',
    fetchStrategies,
    { refreshInterval: 60_000 },
  );

  if (isLoading) {
    return (
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          padding: '40px 0',
          gap: 10,
          color: 'var(--tx-dim)',
        }}
      >
        <Spinner size={18} />
        <span className="mono" style={{ fontSize: 12 }}>
          {t('loading_strategies')}
        </span>
      </div>
    );
  }

  if (error) {
    const msg =
      error instanceof ApiError ? `HTTP ${error.status}` : t('network_error');
    return (
      <div className="disclaimer" style={{ margin: 12 }}>
        <span>{t('load_failed', { msg })}</span>
      </div>
    );
  }

  const strategies = data ?? [];

  return (
    <>
      {/* Anti-survivorship note */}
      <div className="disclaimer" style={{ margin: '0 0 0 0', borderRadius: 0, borderLeft: 0, borderRight: 0, borderTop: 0 }}>
        <span style={{ color: 'var(--tx-faint)', fontSize: 10.5 }}>
          <b style={{ color: 'var(--tx-dim)' }}>Anti-survivorship:</b> {t('anti_survivorship_1')}{' '}
          <b className="up">certified</b> {t('anti_survivorship_2')}
        </span>
      </div>

      <div style={{ overflowX: 'auto' }}>
        <table className="tbl">
          <thead>
            <tr>
              <th>Spec ID</th>
              <th>{t('author')}</th>
              <th>{t('regime')}</th>
              <th className="num">PF OOS</th>
              <th className="num">{t('consistency')}</th>
              <th className="num">Max DD</th>
              <th className="num">{t('days_active')}</th>
              <th className="num">{t('copiers')}</th>
              <th>{t('status')}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {strategies.map((g) => (
              <tr
                key={g.id}
                style={{
                  opacity: g.retired ? 0.6 : 1,
                  transition: 'opacity 0.15s',
                }}
              >
                {/* Spec ID */}
                <td>
                  <span
                    className="mono hi"
                    style={{
                      fontSize: 11.5,
                      fontWeight: 600,
                      textDecoration: g.retired ? 'line-through' : undefined,
                      color: g.retired ? 'var(--tx-dim)' : undefined,
                    }}
                  >
                    {g.id}
                  </span>
                </td>

                {/* Author */}
                <td>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                    {/* kind icon */}
                    <span
                      style={{
                        fontSize: 10,
                        fontFamily: 'var(--mono)',
                        color: g.kind === 'ai' ? 'var(--brand)' : 'var(--tx-dim)',
                        padding: '1px 5px',
                        border: '1px solid var(--line)',
                        borderRadius: 4,
                        background: 'var(--bg-inset)',
                        letterSpacing: '0.05em',
                      }}
                    >
                      {g.kind === 'ai' ? t('kind_ai') : t('kind_human')}
                    </span>
                    <span style={{ color: 'var(--tx-dim)', fontSize: 12 }}>{g.author}</span>
                  </div>
                </td>

                {/* Regime */}
                <td>
                  <span
                    className={`mono ${REGIME_CLASS[g.regime] ?? 'dim'}`}
                    style={{ fontSize: 11 }}
                  >
                    {g.regime}
                  </span>
                </td>

                {/* PF OOS */}
                <td
                  className="num"
                  style={{ color: g.pf_oos >= 1.3 ? 'var(--up)' : 'var(--real)' }}
                >
                  {g.pf_oos.toFixed(2)}
                </td>

                {/* Consistency */}
                <td className="num">{g.consistency_pct}%</td>

                {/* Max drawdown — anti-survivorship: always shown, never hidden */}
                <td className="num">
                  <span style={{ color: g.max_drawdown < 0 ? 'var(--down)' : 'var(--tx-dim)' }}>
                    {g.max_drawdown !== 0 ? `${g.max_drawdown.toFixed(1)}%` : '—'}
                  </span>
                </td>

                {/* Days active */}
                <td className="num">{g.days_active}</td>

                {/* Copiers */}
                <td className="num">{g.copiers}</td>

                {/* Estado */}
                <td>
                  {g.retired ? (
                    <span title={g.retired_at ? t('retired_on', { date: g.retired_at.slice(0, 10) }) : t('retired_degraded')}>
                      <Badge variant="neutral" className="real" style={{ fontSize: 8.5 }}>
                        {t('retired_badge')}
                      </Badge>
                    </span>
                  ) : g.copiable ? (
                    <Badge variant="ok" style={{ fontSize: 8.5 }}>
                      {t('active')}
                    </Badge>
                  ) : (
                    <Badge variant="micro" style={{ fontSize: 8.5 }}>
                      {t('pending')}
                    </Badge>
                  )}
                </td>

                {/* Copiar */}
                <td>
                  {g.copiable && !g.retired ? (
                    <button
                      className="btn sm"
                      onClick={() => onCopy(g)}
                      style={{ fontSize: 11 }}
                    >
                      {t('copy')}
                    </button>
                  ) : (
                    <span style={{ color: 'var(--tx-faint)', fontSize: 11 }}>—</span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}
