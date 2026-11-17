'use client';

/**
 * VoteBar — Horizontal voting bar with 3 competitor buttons.
 *
 * - 1 vote per round (disabled after casting until next round).
 * - After vote: shows animated fill bars with live vote counts.
 * - Receives vote.update via WS (handled in useArenaStream → store).
 * - rAF-coalescing is done upstream in useArenaStream.
 */

import { useCallback } from 'react';
import { useTranslations } from 'next-intl';
import { useArenaStore } from '@/stores/arena';
import type { Competitor, ArenaPhase } from '@/stores/arena';
import { api } from '@/lib/api';
import { color } from '@/design-system/tokens';

interface VoteBarProps {
  tournamentId: string;
  phase:        ArenaPhase;
}

export function VoteBar({ tournamentId, phase }: VoteBarProps) {
  const t = useTranslations('PageArenaDetail');
  const competitors = useArenaStore((s) => s.competitors);
  const votes       = useArenaStore((s) => s.votes);
  const myVote      = useArenaStore((s) => s.myVote);
  const castVote    = useArenaStore((s) => s.castVote);

  const canVote     = phase === 'VOTING' && myVote === null;
  const hasVoted    = myVote !== null;

  const totalVotes  = Object.values(votes).reduce((a, b) => a + b, 0);

  const handleVote  = useCallback(async (competitorId: string) => {
    if (!canVote) return;
    castVote(competitorId);
    try {
      await api.post(`/api/v1/tournament/${tournamentId}/vote`, {
        competitor_id: competitorId,
      });
    } catch {
      // Vote recorded locally regardless — server may reject but UX stays positive
    }
  }, [canVote, castVote, tournamentId]);

  if (competitors.length === 0) return null;

  return (
    <div style={styles.wrap} role="group" aria-label={t('vote_group_aria')}>
      {/* Phase label */}
      <span style={styles.phaseLabel}>
        {phase === 'VOTING'
          ? hasVoted ? t('vote_voted') : t('vote_pick')
          : phase === 'ACTIVE' ? t('vote_active')
          : phase === 'SETTLE' ? t('vote_settle')
          : phase === 'RECAP'  ? t('vote_recap')
          : t('vote_waiting')}
      </span>

      {/* Vote buttons */}
      <div style={styles.buttons}>
        {competitors.map((c) => (
          <VoteButton
            key={c.id}
            competitor={c}
            votes={votes[c.id] ?? 0}
            totalVotes={totalVotes}
            isMyVote={myVote === c.id}
            hasVoted={hasVoted}
            canVote={canVote}
            onVote={handleVote}
          />
        ))}
      </div>

      {/* Rate-limit hint */}
      {hasVoted && (
        <span style={styles.hint}>
          {t('vote_registered')}
        </span>
      )}
      {phase !== 'VOTING' && !hasVoted && (
        <span style={styles.hint}>
          {t('vote_opens')}
        </span>
      )}
    </div>
  );
}

// ── VoteButton ────────────────────────────────────────────────

interface VoteButtonProps {
  competitor:  Competitor;
  votes:       number;
  totalVotes:  number;
  isMyVote:    boolean;
  hasVoted:    boolean;
  canVote:     boolean;
  onVote:      (id: string) => void;
}

function VoteButton({
  competitor,
  votes,
  totalVotes,
  isMyVote,
  hasVoted,
  canVote,
  onVote,
}: VoteButtonProps) {
  const t = useTranslations('PageArenaDetail');
  const pct = totalVotes > 0 ? Math.round((votes / totalVotes) * 100) : 0;

  const bg = isMyVote
    ? color.brandSoft
    : hasVoted
    ? color.bgInset
    : color.bgPanel2;

  const borderColor = isMyVote
    ? color.brand
    : hasVoted
    ? color.line
    : color.line2;

  const textColor = canVote
    ? color.txHi
    : isMyVote
    ? color.brand
    : color.txDim;

  return (
    <button
      onClick={() => onVote(competitor.id)}
      disabled={!canVote}
      aria-label={t('vote_for_aria', { name: competitor.name })}
      aria-pressed={isMyVote}
      style={{
        ...styles.btn,
        background:   bg,
        border:       `1px solid ${borderColor}`,
        color:        textColor,
        cursor:       canVote ? 'pointer' : 'default',
        opacity:      !canVote && !isMyVote ? 0.7 : 1,
      }}
    >
      {/* Fill bar (shown after voting) */}
      {hasVoted && (
        <div
          style={{
            ...styles.fill,
            width:      `${pct}%`,
            background: isMyVote
              ? `${color.brand}33`
              : `${color.txFaint}22`,
          }}
          aria-hidden="true"
        />
      )}

      {/* Content */}
      <div style={styles.btnContent}>
        <span style={styles.btnName}>{competitor.name}</span>
        {hasVoted && (
          <span style={{ ...styles.btnPct, color: isMyVote ? color.brand : color.txDim }}>
            {pct}%
          </span>
        )}
        {!hasVoted && (
          <span style={styles.btnProvider}>{competitor.provider.toUpperCase()}</span>
        )}
      </div>

      {isMyVote && (
        <span style={styles.checkmark} aria-hidden="true">✓</span>
      )}
    </button>
  );
}

// ── Styles ────────────────────────────────────────────────────

const styles = {
  wrap: {
    display:       'flex',
    flexDirection: 'column' as const,
    gap:           10,
    padding:       '12px 16px',
    background:    color.bgPanel,
    borderTop:     `1px solid ${color.line}`,
    flexShrink:    0,
  },
  phaseLabel: {
    fontSize:      10,
    letterSpacing: '0.1em',
    textTransform: 'uppercase' as const,
    color:         color.txFaint,
    fontFamily:    'var(--mono)',
    fontWeight:    600,
  },
  buttons: {
    display: 'flex',
    gap:     10,
  },
  btn: {
    flex:         1,
    position:     'relative' as const,
    overflow:     'hidden',
    borderRadius: 8,
    padding:      '10px 14px',
    textAlign:    'left' as const,
    transition:   'border-color 0.2s, background 0.2s',
    minHeight:    48,
    outline:      'none',
    fontFamily:   'var(--sans)',
  },
  fill: {
    position:   'absolute' as const,
    left:       0,
    top:        0,
    bottom:     0,
    transition: 'width 0.5s cubic-bezier(0.4,0,0.2,1)',
    borderRadius:8,
  },
  btnContent: {
    position:       'relative' as const,
    zIndex:         1,
    display:        'flex',
    justifyContent: 'space-between',
    alignItems:     'center',
    gap:            8,
  },
  btnName: {
    fontSize:   13,
    fontWeight: 600,
  },
  btnPct: {
    fontSize:   14,
    fontWeight: 700,
    fontFamily: 'var(--mono)',
  },
  btnProvider: {
    fontSize:      9,
    letterSpacing: '0.08em',
    color:         color.txFaint,
    fontFamily:    'var(--mono)',
  },
  checkmark: {
    position:   'absolute' as const,
    top:        6,
    right:      10,
    fontSize:   12,
    color:      color.brand,
    fontWeight: 700,
  },
  hint: {
    fontSize:   11,
    color:      color.txFaint,
    fontFamily: 'var(--sans)',
  },
} as const;
