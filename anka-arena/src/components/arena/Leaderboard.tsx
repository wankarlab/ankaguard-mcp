'use client';

/**
 * Leaderboard — Real-time tournament leaderboard.
 *
 * - SWR polling every 5s for full refresh.
 * - WS updates via arenaStore (handled upstream by useArenaStream).
 * - Animates rank changes with a highlight pulse.
 */

import { useEffect, useRef, useState } from 'react';
import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { useArenaStore, type TournamentParticipant } from '@/stores/arena';
import { api } from '@/lib/api';
import { color } from '@/design-system/tokens';
import { Spinner } from '@/components/ui/Spinner';

interface LeaderboardProps {
  tournamentId: string;
}

interface LeaderboardResponse {
  participants: TournamentParticipant[];
}

export function Leaderboard({ tournamentId }: LeaderboardProps) {
  const t = useTranslations('PageArenaDetail');
  const storeParticipants = useArenaStore((s) => s.leaderboard);
  const setLeaderboard    = useArenaStore((s) => s.setLeaderboard);

  // SWR polling — server-of-record every 5s
  const { isLoading } = useSWR<LeaderboardResponse>(
    `/api/v1/tournament/${tournamentId}/leaderboard`,
    (path: string) => api.get<LeaderboardResponse>(path),
    {
      refreshInterval: 5_000,
      onSuccess: (data) => {
        if (data?.participants) setLeaderboard(data.participants);
      },
    },
  );

  // Track previous ranks for highlight animation
  const prevRanksRef = useRef<Map<string, number>>(new Map());
  const [highlighted, setHighlighted] = useState<Set<string>>(new Set());

  useEffect(() => {
    const prev     = prevRanksRef.current;
    const changed  = new Set<string>();

    storeParticipants.forEach((p) => {
      const oldRank = prev.get(p.userId);
      if (oldRank !== undefined && oldRank !== p.rank) {
        changed.add(p.userId);
      }
    });

    if (changed.size > 0) {
      setHighlighted(changed);
      const timer = setTimeout(() => setHighlighted(new Set()), 1200);

      // Update prev ranks
      storeParticipants.forEach((p) => prev.set(p.userId, p.rank));
      return () => clearTimeout(timer);
    }

    storeParticipants.forEach((p) => prev.set(p.userId, p.rank));
  }, [storeParticipants]);

  return (
    <div style={styles.wrap}>
      <div style={styles.header}>
        <span style={styles.title}>{t('board_title')}</span>
        {isLoading && <Spinner size={12} />}
      </div>

      <div style={styles.table}>
        {/* Column headers */}
        <div style={styles.headRow}>
          <span style={{ ...styles.cell, width: 24 }}>#</span>
          <span style={{ ...styles.cell, flex: 1 }}>{t('board_player')}</span>
          <span style={{ ...styles.cell, width: 60, textAlign: 'right' }}>PnL</span>
          <span style={{ ...styles.cell, width: 40, textAlign: 'right' }}>WR</span>
        </div>

        {/* Rows */}
        {storeParticipants.length === 0 ? (
          <div style={styles.empty}>{t('board_empty')}</div>
        ) : (
          storeParticipants.map((p) => (
            <LeaderboardRow
              key={p.userId}
              participant={p}
              highlighted={highlighted.has(p.userId)}
            />
          ))
        )}
      </div>
    </div>
  );
}

// ── Row ───────────────────────────────────────────────────────

function LeaderboardRow({
  participant: p,
  highlighted,
}: {
  participant: TournamentParticipant;
  highlighted: boolean;
}) {
  const t = useTranslations('PageArenaDetail');
  const isUp    = p.pnlUsd >= 0;
  const sign    = isUp ? '+' : '';
  const pnlColor = isUp ? color.up : color.down;
  const rankIcon = p.rank === 1 ? '🥇' : p.rank === 2 ? '🥈' : p.rank === 3 ? '🥉' : null;

  return (
    <div
      style={{
        ...styles.row,
        background: highlighted
          ? `${color.brand}18`
          : p.isMe
          ? `${color.paper}10`
          : 'transparent',
        borderLeft: p.isMe
          ? `2px solid ${color.paper}`
          : '2px solid transparent',
        transition: 'background 0.4s ease',
      }}
    >
      <span style={{ ...styles.cell, width: 24, color: color.txDim, fontFamily: 'var(--mono)', fontSize: 11 }}>
        {rankIcon ?? `${p.rank}`}
      </span>
      <div style={{ ...styles.cell, flex: 1, flexDirection: 'column', alignItems: 'flex-start', gap: 1 }}>
        <span style={styles.username}>
          {p.username}
          {p.isMe && <span style={styles.meTag}>{t('board_you')}</span>}
        </span>
        <TierDot tier={p.tier} />
      </div>
      <span style={{ ...styles.cell, width: 60, textAlign: 'right', color: pnlColor, fontFamily: 'var(--mono)', fontSize: 11 }}>
        {sign}${Math.abs(p.pnlUsd).toFixed(2)}
      </span>
      <span style={{ ...styles.cell, width: 40, textAlign: 'right', color: color.txDim, fontFamily: 'var(--mono)', fontSize: 10 }}>
        {Math.round(p.winRate * 100)}%
      </span>
    </div>
  );
}

function TierDot({ tier }: { tier: 'paper' | 'micro' | 'real' }) {
  const c = tier === 'paper' ? color.paper : tier === 'micro' ? color.micro : color.real;
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
      <span style={{ width: 5, height: 5, borderRadius: '50%', background: c, display: 'inline-block' }} />
      <span style={{ fontSize: 9, color: c, fontFamily: 'var(--mono)', letterSpacing: '0.06em', textTransform: 'uppercase' }}>
        {tier}
      </span>
    </div>
  );
}

// ── Styles ────────────────────────────────────────────────────

const styles = {
  wrap: {
    display:       'flex',
    flexDirection: 'column' as const,
    gap:           8,
  },
  header: {
    display:        'flex',
    alignItems:     'center',
    justifyContent: 'space-between',
    padding:        '0 2px',
  },
  title: {
    fontSize:      10,
    letterSpacing: '0.1em',
    textTransform: 'uppercase' as const,
    color:         color.txFaint,
    fontFamily:    'var(--mono)',
    fontWeight:    600,
  },
  table: {
    display:       'flex',
    flexDirection: 'column' as const,
    gap:           2,
  },
  headRow: {
    display:        'flex',
    alignItems:     'center',
    padding:        '4px 8px',
    borderBottom:   `1px solid ${color.line}`,
    marginBottom:   4,
  },
  row: {
    display:      'flex',
    alignItems:   'center',
    padding:      '6px 8px',
    borderRadius: 6,
    gap:          4,
    borderLeft:   '2px solid transparent',
  },
  cell: {
    display:    'flex',
    alignItems: 'center',
    fontSize:   11,
    color:      color.txDim,
    fontFamily: 'var(--sans)',
  },
  username: {
    fontSize:   12,
    fontWeight: 500,
    color:      color.tx,
    fontFamily: 'var(--sans)',
    display:    'flex',
    alignItems: 'center',
    gap:        6,
  },
  meTag: {
    fontSize:      8,
    letterSpacing: '0.08em',
    color:         color.paper,
    fontFamily:    'var(--mono)',
    border:        `1px solid ${color.paper}55`,
    borderRadius:  3,
    padding:       '0 4px',
  },
  empty: {
    fontSize:  12,
    color:     color.txFaint,
    padding:   '12px 8px',
    textAlign: 'center' as const,
  },
} as const;
