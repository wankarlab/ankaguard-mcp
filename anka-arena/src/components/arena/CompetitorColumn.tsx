'use client';

/**
 * CompetitorColumn — Per-AI competitor card.
 *
 * PnL display is isolated via usePnLSlice so rapid WS updates
 * do NOT re-render the parent (trade history, header, etc.).
 */

import { memo } from 'react';
import { useTranslations } from 'next-intl';
import { usePnLSlice } from '@/hooks/useArenaStream';
import { useExplain } from '@/lib/useExplain';
import type { Competitor, TradeHistoryItem } from '@/stores/arena';
import { color } from '@/design-system/tokens';

// ── Provider palette ──────────────────────────────────────────

const PROVIDER_COLORS: Record<string, string> = {
  gemini:    '#4f9bf5',
  openai:    '#36cd86',
  anthropic: '#9d7cff',
  openrouter:'#e9a23e',
  default:   '#46c8d8',
};

const PROVIDER_LABELS: Record<string, string> = {
  gemini:    'Gemini',
  openai:    'GPT-4',
  anthropic: 'Claude',
  openrouter:'OpenRouter',
};

function providerColor(provider: string): string {
  return PROVIDER_COLORS[provider.toLowerCase()] ?? PROVIDER_COLORS.default;
}

function providerLabel(provider: string): string {
  return PROVIDER_LABELS[provider.toLowerCase()] ?? provider;
}

// ── Avatar ────────────────────────────────────────────────────

function CompetitorAvatar({
  name,
  provider,
}: {
  name: string;
  provider: string;
}) {
  const bg    = providerColor(provider);
  const initials = name.slice(0, 2).toUpperCase();
  return (
    <div style={{ ...styles.avatar, background: `${bg}22`, border: `2px solid ${bg}` }}>
      <span style={{ ...styles.avatarText, color: bg }}>{initials}</span>
    </div>
  );
}

// ── PnL display — isolated from parent re-renders ────────────

const PnLDisplay = memo(function PnLDisplay({
  competitorId,
}: {
  competitorId: string;
}) {
  const { pnl_usd, pnl_pct } = usePnLSlice(competitorId);
  const isUp   = pnl_usd >= 0;
  const sign   = isUp ? '+' : '';
  const c      = isUp ? color.up : color.down;

  return (
    <div style={styles.pnlWrap}>
      <span style={{ ...styles.pnlMain, color: c }}>
        {sign}${Math.abs(pnl_usd).toFixed(2)}
      </span>
      <span style={{ ...styles.pnlPct, color: c }}>
        {sign}{pnl_pct.toFixed(2)}%
      </span>
    </div>
  );
});

// ── Win rate mini-bar ─────────────────────────────────────────

function WinRateBar({ winRate }: { winRate: number }) {
  const pct = Math.round(winRate * 100);
  return (
    <div style={styles.wrWrap}>
      <span style={styles.wrLabel}>WR</span>
      <div style={styles.wrTrack}>
        <div
          style={{
            ...styles.wrFill,
            width:      `${pct}%`,
            background: pct >= 55 ? color.up : pct >= 45 ? color.warn : color.down,
          }}
        />
      </div>
      <span style={styles.wrValue}>{pct}%</span>
    </div>
  );
}

// ── Trade history row ─────────────────────────────────────────

// Regla 1 — "el porqué a un toque": clicking a trade row deep-links into the
// shared ExplainDrawer singleton (C1.2) via openExplain(tradeId). No local fetch
// here — the drawer in the layout owns GET /api/v1/trades/:id/explain.
const TradeRow = memo(function TradeRow({ trade }: { trade: TradeHistoryItem }) {
  const t = useTranslations('PageArenaDetail');
  const { openExplain } = useExplain();
  const isUp = trade.pnl_usd >= 0;
  const sign = isUp ? '+' : '';
  const explainable = Boolean(trade.id);

  const onActivate = () => {
    if (explainable) openExplain(trade.id);
  };

  return (
    <div
      style={{ ...styles.tradeRow, ...(explainable ? styles.tradeRowClickable : null) }}
      role={explainable ? 'button' : undefined}
      tabIndex={explainable ? 0 : undefined}
      title={explainable ? t('trade_explain_title') : undefined}
      aria-label={
        explainable ? t('trade_explain_aria', { pair: trade.pair, side: trade.side }) : undefined
      }
      onClick={explainable ? onActivate : undefined}
      onKeyDown={
        explainable
          ? (e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault();
                onActivate();
              }
            }
          : undefined
      }
    >
      <span style={styles.tradePair}>{trade.pair}</span>
      <span
        style={{
          ...styles.tradeSide,
          color: trade.side === 'LONG' ? color.up : color.down,
        }}
      >
        {trade.side}
      </span>
      <span
        style={{
          ...styles.tradePnl,
          color: isUp ? color.up : color.down,
        }}
      >
        {sign}${trade.pnl_usd.toFixed(2)}
      </span>
    </div>
  );
});

// ── Main component ────────────────────────────────────────────

interface CompetitorColumnProps {
  competitor: Competitor;
  rank:       number;
}

export const CompetitorColumn = memo(function CompetitorColumn({
  competitor,
  rank,
}: CompetitorColumnProps) {
  const t = useTranslations('PageArenaDetail');
  const pc = providerColor(competitor.provider);

  return (
    <div style={{ ...styles.col, '--pc': pc } as React.CSSProperties}>
      {/* Rank indicator */}
      <div style={{ ...styles.rankBadge, color: rank === 1 ? color.warn : color.txDim }}>
        #{rank}
      </div>

      {/* Header */}
      <div style={styles.header}>
        <CompetitorAvatar name={competitor.name} provider={competitor.provider} />
        <div style={styles.headerText}>
          <span style={styles.name}>{competitor.name}</span>
          <span style={{ ...styles.providerBadge, color: pc, borderColor: `${pc}44` }}>
            {providerLabel(competitor.provider)}
          </span>
        </div>
      </div>

      {/* PnL — isolated subtree */}
      <PnLDisplay competitorId={competitor.id} />

      {/* Current trade */}
      {competitor.current_trade && (
        <div style={styles.currentTrade}>
          <span style={styles.ctLabel}>{t('active_trade')}</span>
          <div style={styles.ctRow}>
            <span style={styles.ctPair}>{competitor.current_trade.pair}</span>
            <span
              style={{
                ...styles.ctSide,
                color:      competitor.current_trade.side === 'LONG' ? color.up : color.down,
                background: competitor.current_trade.side === 'LONG'
                  ? color.upSoft
                  : 'rgba(240,86,79,0.13)',
              }}
            >
              {competitor.current_trade.side}
            </span>
          </div>
          <div style={styles.ctEntry}>
            <span style={styles.ctEntryLabel}>{t('entry')}</span>
            <span style={styles.ctEntryVal}>
              ${competitor.current_trade.entry.toFixed(4)}
            </span>
          </div>
        </div>
      )}

      {/* Win rate */}
      <WinRateBar winRate={competitor.win_rate} />

      {/* Trade history */}
      <div style={styles.histHeader}>
        <span style={styles.histLabel}>{t('history')}</span>
        <span style={styles.histCount}>{t('ops', { count: competitor.trades.length })}</span>
      </div>
      <div style={styles.histList}>
        {competitor.trades.length === 0 ? (
          <span style={styles.histEmpty}>{t('no_trades_round')}</span>
        ) : (
          competitor.trades.slice(0, 8).map((t) => (
            <TradeRow key={t.id} trade={t} />
          ))
        )}
      </div>
    </div>
  );
});

// ── Styles ────────────────────────────────────────────────────

const styles = {
  col: {
    display:       'flex',
    flexDirection: 'column' as const,
    gap:           12,
    background:    color.bgPanel,
    border:        `1px solid ${color.line}`,
    borderRadius:  11,
    padding:       '16px 14px',
    minWidth:      0,
    flex:          1,
    position:      'relative' as const,
  },
  rankBadge: {
    position:   'absolute' as const,
    top:        12,
    right:      12,
    fontSize:   12,
    fontFamily: 'var(--mono)',
    fontWeight: 700,
  },
  header: {
    display:    'flex',
    alignItems: 'center',
    gap:        10,
  },
  avatar: {
    width:          40,
    height:         40,
    borderRadius:   '50%',
    display:        'flex',
    alignItems:     'center',
    justifyContent: 'center',
    flexShrink:     0,
  },
  avatarText: {
    fontSize:   14,
    fontWeight: 700,
    fontFamily: 'var(--sans)',
  },
  headerText: {
    display:       'flex',
    flexDirection: 'column' as const,
    gap:           3,
    minWidth:      0,
  },
  name: {
    fontSize:     14,
    fontWeight:   600,
    color:        color.txHi,
    fontFamily:   'var(--sans)',
    overflow:     'hidden',
    textOverflow: 'ellipsis',
    whiteSpace:   'nowrap' as const,
  },
  providerBadge: {
    fontSize:      10,
    fontFamily:    'var(--mono)',
    letterSpacing: '0.08em',
    textTransform: 'uppercase' as const,
    border:        '1px solid',
    borderRadius:  4,
    padding:       '1px 5px',
    alignSelf:     'flex-start' as const,
  },

  // PnL
  pnlWrap: {
    display:       'flex',
    flexDirection: 'column' as const,
    gap:           2,
    padding:       '10px 0',
    borderTop:     `1px solid ${color.line}`,
    borderBottom:  `1px solid ${color.line}`,
  },
  pnlMain: {
    fontSize:   28,
    fontWeight: 700,
    fontFamily: 'var(--mono)',
    lineHeight: 1,
  },
  pnlPct: {
    fontSize:   13,
    fontFamily: 'var(--mono)',
    opacity:    0.8,
  },

  // Current trade
  currentTrade: {
    background:   color.bgInset,
    borderRadius: 7,
    padding:      '8px 10px',
    display:      'flex',
    flexDirection:'column' as const,
    gap:          6,
  },
  ctLabel: {
    fontSize:      9,
    letterSpacing: '0.1em',
    textTransform: 'uppercase' as const,
    color:         color.txFaint,
    fontFamily:    'var(--mono)',
  },
  ctRow: {
    display:    'flex',
    alignItems: 'center',
    gap:        8,
  },
  ctPair: {
    fontSize:   13,
    fontWeight: 600,
    color:      color.txHi,
    fontFamily: 'var(--mono)',
  },
  ctSide: {
    fontSize:      10,
    fontWeight:    700,
    fontFamily:    'var(--mono)',
    letterSpacing: '0.06em',
    padding:       '2px 6px',
    borderRadius:  4,
  },
  ctEntry: {
    display:    'flex',
    alignItems: 'center',
    gap:        6,
  },
  ctEntryLabel: {
    fontSize:   10,
    color:      color.txFaint,
    fontFamily: 'var(--sans)',
  },
  ctEntryVal: {
    fontSize:   12,
    color:      color.tx,
    fontFamily: 'var(--mono)',
  },

  // Win rate
  wrWrap: {
    display:    'flex',
    alignItems: 'center',
    gap:        8,
  },
  wrLabel: {
    fontSize:      9,
    letterSpacing: '0.08em',
    textTransform: 'uppercase' as const,
    color:         color.txFaint,
    fontFamily:    'var(--mono)',
    width:         18,
    flexShrink:    0,
  },
  wrTrack: {
    flex:         1,
    height:       4,
    background:   color.line2,
    borderRadius: 2,
    overflow:     'hidden',
  },
  wrFill: {
    height:       '100%',
    borderRadius: 2,
    transition:   'width 0.6s ease',
  },
  wrValue: {
    fontSize:   11,
    color:      color.txDim,
    fontFamily: 'var(--mono)',
    width:      30,
    textAlign:  'right' as const,
  },

  // History
  histHeader: {
    display:        'flex',
    justifyContent: 'space-between',
    alignItems:     'center',
    marginTop:      4,
  },
  histLabel: {
    fontSize:      9,
    letterSpacing: '0.1em',
    textTransform: 'uppercase' as const,
    color:         color.txFaint,
    fontFamily:    'var(--mono)',
  },
  histCount: {
    fontSize:   10,
    color:      color.txFaint,
    fontFamily: 'var(--mono)',
  },
  histList: {
    display:       'flex',
    flexDirection: 'column' as const,
    gap:           3,
    maxHeight:     200,
    overflowY:     'auto' as const,
  },
  histEmpty: {
    fontSize:   12,
    color:      color.txFaint,
    fontFamily: 'var(--sans)',
    padding:    '8px 0',
    textAlign:  'center' as const,
  },
  tradeRow: {
    display:         'flex',
    alignItems:      'center',
    justifyContent:  'space-between',
    padding:         '4px 6px',
    background:      color.bgInset,
    borderRadius:    5,
  },
  tradeRowClickable: {
    cursor:     'pointer',
    transition: 'background 0.12s ease',
  },
  tradePair: {
    fontSize:   11,
    fontFamily: 'var(--mono)',
    color:      color.tx,
    flex:       1,
  },
  tradeSide: {
    fontSize:      9,
    fontFamily:    'var(--mono)',
    fontWeight:    700,
    letterSpacing: '0.06em',
    width:         36,
    textAlign:     'center' as const,
  },
  tradePnl: {
    fontSize:   11,
    fontFamily: 'var(--mono)',
    fontWeight: 600,
    textAlign:  'right' as const,
    width:      56,
  },
} as const;
