'use client';

/**
 * /arena/[id] — Main tournament arena screen.
 *
 * Layout:
 *   ┌─────────────────────────────────────────┬──────────────┐
 *   │  Topbar: name + RoundTimer + badge       │              │
 *   ├──────────┬──────────┬────────────────────│  Leaderboard │
 *   │ Column A │ Column B │ Column C           │  + ChatPane  │
 *   ├──────────┴──────────┴────────────────────│              │
 *   │            VoteBar                       │              │
 *   ├──────────────────────────────────────────┴──────────────┤
 *   │                  NarratorFeed                           │
 *   └─────────────────────────────────────────────────────────┘
 */

import { use } from 'react';
import { useTranslations } from 'next-intl';
import { useArenaStore } from '@/stores/arena';
import { useArenaStream } from '@/hooks/useArenaStream';
import { CompetitorColumn } from '@/components/arena/CompetitorColumn';
import { VoteBar }          from '@/components/arena/VoteBar';
import { RoundTimer }       from '@/components/arena/RoundTimer';
import { NarratorFeed }     from '@/components/arena/NarratorFeed';
import { Leaderboard }      from '@/components/arena/Leaderboard';
import { ChatPane }         from '@/components/arena/ChatPane';
import { Spinner }          from '@/components/ui/Spinner';
import { color }            from '@/design-system/tokens';

interface ArenaPageProps {
  params: Promise<{ id: string }>;
}

export default function ArenaPage({ params }: ArenaPageProps) {
  const { id } = use(params);
  return <ArenaScreen tournamentId={id} />;
}

// ── Inner client component ────────────────────────────────────

function ArenaScreen({ tournamentId }: { tournamentId: string }) {
  const t = useTranslations('PageArenaDetail');
  // Activate WS subscription + late-join snapshot
  useArenaStream(tournamentId);

  const round       = useArenaStore((s) => s.currentRound);
  const competitors = useArenaStore((s) => s.competitors);
  const phase       = useArenaStore((s) => s.phase);
  const seconds     = useArenaStore((s) => s.secondsRemaining);
  const loading     = useArenaStore((s) => s.loading);
  const error       = useArenaStore((s) => s.error);

  // ── Loading state ─────────────────────────────────────────
  if (loading) {
    return (
      <div style={styles.center}>
        <Spinner size={32} />
        <span style={styles.loadingText}>{t('connecting')}</span>
      </div>
    );
  }

  // ── Error state ───────────────────────────────────────────
  if (error) {
    return (
      <div style={styles.center}>
        <span style={{ ...styles.loadingText, color: color.bad }}>{error}</span>
      </div>
    );
  }

  // ── Determine total seconds per phase for RoundTimer arc ──
  const totalSeconds = round
    ? Math.round((round.endAt - round.startAt) / 1000)
    : 300;

  return (
    <div style={styles.root}>
      {/* ── Topbar ──────────────────────────────────────────── */}
      <div style={styles.topbar}>
        <div style={styles.topbarLeft}>
          <span style={styles.tournamentName}>
            {round?.name ?? 'Arena'}
          </span>
          {round && (
            <CapitalBadge prizePool={round.prizePool} currency={round.currency} />
          )}
        </div>
        <RoundTimer
          phase={phase}
          secondsRemaining={seconds}
          totalSeconds={totalSeconds}
        />
      </div>

      {/* ── Body (columns + sidebar) ─────────────────────────── */}
      <div style={styles.body}>
        {/* Competitor columns */}
        <div style={styles.columns}>
          {competitors.length === 0 ? (
            <div style={styles.noCompetitors}>
              <Spinner size={20} />
              <span style={{ color: color.txDim, fontSize: 13 }}>
                {t('waiting_competitors')}
              </span>
            </div>
          ) : (
            competitors.map((c, i) => (
              <CompetitorColumn
                key={c.id}
                competitor={c}
                rank={i + 1}
              />
            ))
          )}
        </div>

        {/* Right sidebar */}
        <div style={styles.sidebar}>
          <div style={styles.sideSection}>
            <Leaderboard tournamentId={tournamentId} />
          </div>
          <div style={{ ...styles.sideSection, flex: 1 }}>
            <ChatPane tournamentId={tournamentId} />
          </div>
        </div>
      </div>

      {/* ── VoteBar ──────────────────────────────────────────── */}
      <VoteBar tournamentId={tournamentId} phase={phase} />

      {/* ── NarratorFeed ─────────────────────────────────────── */}
      <NarratorFeed />
    </div>
  );
}

// ── Capital state badge ───────────────────────────────────────

function CapitalBadge({ prizePool, currency }: { prizePool: number; currency: string }) {
  return (
    <div style={styles.capitalBadge}>
      <span style={styles.capitalDot} />
      <span style={styles.capitalText}>
        {prizePool.toFixed(0)} {currency}
      </span>
    </div>
  );
}

// ── Styles ────────────────────────────────────────────────────

const styles = {
  root: {
    display:       'flex',
    flexDirection: 'column' as const,
    height:        'calc(100vh - 56px)',  // fill below global topbar
    overflow:      'hidden',
    background:    color.bgBase,
  },

  // Loading / error
  center: {
    display:        'flex',
    flexDirection:  'column' as const,
    alignItems:     'center',
    justifyContent: 'center',
    height:         'calc(100vh - 56px)',
    gap:            16,
  },
  loadingText: {
    fontSize:   15,
    color:      color.txDim,
    fontFamily: 'var(--sans)',
  },

  // Topbar (internal, inside arena screen)
  topbar: {
    display:        'flex',
    alignItems:     'center',
    justifyContent: 'space-between',
    padding:        '0 20px',
    height:         52,
    borderBottom:   `1px solid ${color.line}`,
    background:     color.bgPanel,
    flexShrink:     0,
  },
  topbarLeft: {
    display:    'flex',
    alignItems: 'center',
    gap:        12,
  },
  tournamentName: {
    fontSize:   16,
    fontWeight: 700,
    color:      color.txHi,
    fontFamily: 'var(--sans)',
  },
  capitalBadge: {
    display:      'flex',
    alignItems:   'center',
    gap:          6,
    background:   color.bgInset,
    border:       `1px solid ${color.line2}`,
    borderRadius: 6,
    padding:      '3px 10px',
  },
  capitalDot: {
    width:        6,
    height:       6,
    borderRadius: '50%',
    background:   color.up,
    flexShrink:   0,
  },
  capitalText: {
    fontSize:   11,
    fontFamily: 'var(--mono)',
    color:      color.tx,
  },

  // Body layout
  body: {
    display:   'flex',
    flex:      1,
    overflow:  'hidden',
    gap:       0,
  },

  // 3 competitor columns
  columns: {
    display:   'flex',
    flex:      1,
    gap:       12,
    padding:   16,
    overflowY: 'auto' as const,
    minWidth:  0,
  },
  noCompetitors: {
    display:        'flex',
    alignItems:     'center',
    justifyContent: 'center',
    flex:           1,
    gap:            12,
  },

  // Right sidebar
  sidebar: {
    width:         300,
    flexShrink:    0,
    display:       'flex',
    flexDirection: 'column' as const,
    borderLeft:    `1px solid ${color.line}`,
    background:    color.bgPanel,
    overflowY:     'auto' as const,
  },
  sideSection: {
    padding:      '14px 14px 10px',
    borderBottom: `1px solid ${color.line}`,
  },
} as const;
