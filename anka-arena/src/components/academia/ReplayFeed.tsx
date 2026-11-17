'use client';

import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import clsx from 'clsx';
import { useUIStore } from '@/stores/ui';
import { fetchTradeHistory } from '@/lib/api';
import type { TradeHistoryRow } from '@/lib/adapters';
import { Spinner } from '@/components/ui/Spinner';
import { Badge } from '@/components/ui/Badge';
import { useState } from 'react';

// ── Types ─────────────────────────────────────────────────────────────────────

export type Regime = 'TRENDING' | 'RANGING' | 'BREAKOUT' | 'UNKNOWN';

// Regime filter is a subset of the full set; 'todos' = no regime filter.
type RegimeFilter = 'todos' | 'TRENDING' | 'RANGING' | 'BREAKOUT';

const REGIME_FILTERS: RegimeFilter[] = ['todos', 'TRENDING', 'RANGING', 'BREAKOUT'];

function normalizeRegime(r: string): Regime {
  const up = (r ?? '').toUpperCase();
  if (up === 'TRENDING' || up === 'RANGING' || up === 'BREAKOUT') return up;
  return 'UNKNOWN';
}

// ── Mini candle SVG ───────────────────────────────────────────────────────────

function MiniCandles({ win }: { win: boolean }) {
  const data = win
    ? [40, 42, 41, 44, 46, 45, 49, 52, 54, 58]
    : [54, 52, 53, 50, 48, 49, 46, 44, 45, 42];

  return (
    <svg viewBox="0 0 200 56" style={{ width: '100%', height: 56, display: 'block' }}>
      {data.map((c, i) => {
        const up = i > 0 && c >= data[i - 1];
        const x  = 8 + i * 20;
        const y  = 56 - (c - 36) - 6;
        const color = up ? 'var(--up)' : 'var(--down)';
        return (
          <g key={i}>
            <line x1={x} y1={y} x2={x} y2={y + 12} stroke={color} strokeWidth="1" />
            <rect x={x - 3.5} y={y + 3} width="7" height="7" fill={color} opacity=".8" />
          </g>
        );
      })}
    </svg>
  );
}

// ── Regime chip ───────────────────────────────────────────────────────────────

const REGIME_ICON: Record<Regime, string> = {
  TRENDING: '↗',
  RANGING:  '⇔',
  BREAKOUT: '⚡',
  UNKNOWN:  '?',
};

const REGIME_COLOR: Record<Regime, string> = {
  TRENDING: 'var(--up)',
  RANGING:  'var(--paper)',
  BREAKOUT: 'var(--micro)',
  UNKNOWN:  'var(--tx-faint)',
};

function RegimeChip({ regime, volTier }: { regime: Regime; volTier: string }) {
  return (
    <span className="chip">
      <span style={{ color: REGIME_COLOR[regime] }}>{REGIME_ICON[regime]}</span>
      <b className={`regime-${regime}`}>{regime}</b>
      {volTier && volTier !== 'normal' && (
        <span style={{ color: 'var(--tx-faint)', fontSize: 9 }}>{volTier}</span>
      )}
    </span>
  );
}

// ── Trade card ────────────────────────────────────────────────────────────────

function TradeCard({ trade }: { trade: TradeHistoryRow }) {
  const t = useTranslations('PageAcademy');
  const openExplain = useUIStore((s) => s.openExplain);
  const win         = trade.pnl >= 0;
  const regime      = normalizeRegime(trade.regime);
  const side        = (trade.side ?? '').toUpperCase();

  return (
    <div
      className="panel"
      style={{
        overflow: 'hidden',
        cursor:   'pointer',
        borderColor: win ? 'var(--line)' : 'rgba(240,86,79,0.22)',
        transition: 'border-color 0.15s, box-shadow 0.15s',
      }}
      onClick={() => openExplain(trade.tradeId, 'replay')}
      role="button"
      tabIndex={0}
      onKeyDown={(e) => e.key === 'Enter' && openExplain(trade.tradeId, 'replay')}
      aria-label={t('trade_aria', { pair: trade.pair })}
    >
      {/* Header */}
      <div style={{ padding: '12px 14px 6px' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <span className="mono hi" style={{ fontSize: 12.5, fontWeight: 600 }}>
            {trade.pair}
          </span>
          <span
            className={clsx('num', win ? 'up' : 'down')}
            style={{ fontWeight: 600, fontSize: 13 }}
          >
            {win ? '+' : ''}${Math.abs(trade.pnl).toFixed(2)}
            <span style={{ fontSize: 9.5, marginLeft: 4, opacity: 0.75 }}>
              ({win ? '+' : ''}{trade.pnlPct.toFixed(2)}%)
            </span>
          </span>
        </div>

        <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginTop: 6 }}>
          <Badge variant={win ? 'ok' : 'neutral'} style={{ fontSize: 9, color: side === 'LONG' ? 'var(--up)' : 'var(--down)' } as React.CSSProperties}>
            {side || '—'}
          </Badge>
          <RegimeChip regime={regime} volTier={trade.volTier} />
        </div>
      </div>

      {/* Mini chart */}
      <MiniCandles win={win} />

      {/* Footer */}
      <div style={{ padding: '6px 14px 12px' }}>
        {trade.note && (
          <div style={{ fontSize: 11.5, color: 'var(--tx-dim)', lineHeight: 1.5, minHeight: 30 }}>
            {trade.note}
          </div>
        )}
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginTop: 8 }}>
          <span className="mono faint" style={{ fontSize: 9.5 }}>#{trade.tradeId}</span>
          <span style={{ fontSize: 11, color: 'var(--brand)' }}>{t('see_why')}</span>
        </div>
      </div>
    </div>
  );
}

// ── Main component ────────────────────────────────────────────────────────────

export function ReplayFeed() {
  const t = useTranslations('PageAcademy');
  const [regimeFilter, setRegimeFilter] = useState<RegimeFilter>('todos');
  // Anti-survivorship (C1.1): the feed defaults to MIXED (losers included). The
  // "solo ganadores" toggle is opt-in and surfaces a permanent disclaimer while on.
  const [onlyWins, setOnlyWins] = useState(false);

  const { data, error, isLoading } = useSWR<TradeHistoryRow[]>(
    'trades/history',
    fetchTradeHistory,
    {
      refreshInterval: 0,
      onErrorRetry: (err: any, _key, _cfg, revalidate, { retryCount }) => {
        if (err?.status === 404 || err?.status === 401) return;
        if (retryCount >= 2) return;
        setTimeout(() => revalidate({ retryCount }), 5000);
      },
    },
  );

  const trades = data ?? [];

  const filtered = trades.filter((t) => {
    if (onlyWins && t.pnl < 0) return false;
    if (regimeFilter === 'todos') return true;
    return normalizeRegime(t.regime) === regimeFilter;
  });

  return (
    <div>
      {/* Toolbar */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 12, flexWrap: 'wrap', gap: 10 }}>
        <div>
          <div className="eyebrow" style={{ color: 'var(--tx-faint)' }}>{t('feed_eyebrow')}</div>
          <h2 style={{ margin: '4px 0 0', fontSize: 17, color: 'var(--tx-hi)' }}>{t('feed_title')}</h2>
        </div>

        <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap', alignItems: 'center' }}>
          {/* Regime segmented filter */}
          <div style={{ display: 'flex', gap: 4, background: 'var(--bg-inset)', padding: 3, borderRadius: 'var(--r)', border: '1px solid var(--line)' }}>
            {REGIME_FILTERS.map((f) => (
              <button
                key={f}
                onClick={() => setRegimeFilter(f)}
                style={{
                  padding: '4px 10px',
                  borderRadius: 'var(--r-sm)',
                  border: 'none',
                  cursor: 'pointer',
                  fontFamily: 'var(--mono)',
                  fontSize: 10.5,
                  fontWeight: 600,
                  letterSpacing: '0.04em',
                  textTransform: 'uppercase',
                  background:  regimeFilter === f ? 'var(--bg-panel-2)' : 'transparent',
                  color:       regimeFilter === f ? 'var(--tx-hi)'      : 'var(--tx-dim)',
                  borderColor: regimeFilter === f ? 'var(--line-2)'     : 'transparent',
                  transition:  'all 0.12s',
                }}
              >
                {f === 'todos' ? t('filter_all') : f}
              </button>
            ))}
          </div>

          {/* Anti-survivorship toggle: "solo ganadores" (opt-in, default OFF). */}
          <button
            onClick={() => setOnlyWins((v) => !v)}
            aria-pressed={onlyWins}
            style={{
              padding: '5px 11px',
              borderRadius: 'var(--r-sm)',
              cursor: 'pointer',
              fontFamily: 'var(--mono)',
              fontSize: 10.5,
              fontWeight: 600,
              letterSpacing: '0.04em',
              textTransform: 'uppercase',
              background:  onlyWins ? 'rgba(233,162,62,0.12)' : 'var(--bg-inset)',
              color:       onlyWins ? 'var(--warn)' : 'var(--tx-dim)',
              border:      `1px solid ${onlyWins ? 'rgba(233,162,62,0.35)' : 'var(--line)'}`,
              transition:  'all 0.12s',
            }}
          >
            {onlyWins ? '✓ ' : ''}{t('only_wins')}
          </button>
        </div>
      </div>

      {/* Anti-survivorship disclaimer — permanent while "solo ganadores" is active */}
      {onlyWins && (
        <div
          style={{
            display: 'flex',
            alignItems: 'flex-start',
            gap: 10,
            padding: '10px 14px',
            marginBottom: 12,
            background: 'rgba(233,162,62,0.10)',
            border: '1px solid rgba(233,162,62,0.35)',
            borderRadius: 'var(--r)',
            fontSize: 12,
            color: 'var(--tx)',
          }}
          role="alert"
        >
          <span style={{ fontSize: 15, color: 'var(--warn)', marginTop: -1 }}>⚠</span>
          <span>
            <b style={{ color: 'var(--warn)' }}>{t('bias_title')}</b>{' '}
            {t.rich('bias_body', { b: (chunks) => <b>{chunks}</b> })}
          </span>
        </div>
      )}

      {/* States */}
      {isLoading && (
        <div style={{ display: 'flex', justifyContent: 'center', padding: 40 }}>
          <Spinner />
        </div>
      )}

      {error && (
        <div style={{ padding: '14px', background: 'var(--real-soft)', border: '1px solid rgba(240,86,79,0.3)', borderRadius: 'var(--r)', color: 'var(--real)', fontSize: 12 }}>
          {t('history_error', { message: (error as any)?.message ?? t('unknown') })}
        </div>
      )}

      {!isLoading && !error && filtered.length === 0 && (
        <div style={{ textAlign: 'center', padding: 40, color: 'var(--tx-dim)' }}>
          {onlyWins
            ? t('no_winners')
            : t('no_trades')}
        </div>
      )}

      {/* Grid */}
      {!isLoading && !error && filtered.length > 0 && (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))', gap: 12 }}>
          {filtered.map((t) => (
            <TradeCard key={t.tradeId} trade={t} />
          ))}
        </div>
      )}
    </div>
  );
}
