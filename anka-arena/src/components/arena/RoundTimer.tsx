'use client';

/**
 * RoundTimer — Circular progress timer for arena rounds.
 *
 * - Phase-aware: LOBBY/ACTIVE/VOTING/SETTLE/RECAP
 * - Receives phase + secondsRemaining from WS via props.
 * - Local countdown interpolates between WS ticks (1s client timer).
 */

import { useEffect, useRef, useState } from 'react';
import { useTranslations } from 'next-intl';
import { color } from '@/design-system/tokens';
import type { ArenaPhase } from '@/stores/arena';

interface RoundTimerProps {
  phase:            ArenaPhase;
  secondsRemaining: number;
  totalSeconds?:    number;  // for computing arc; defaults to 300 (5min)
}

const PHASE_COLORS: Record<ArenaPhase, string> = {
  LOBBY:  color.txFaint,
  ACTIVE: color.up,
  VOTING: color.brand,
  SETTLE: color.warn,
  RECAP:  color.info,
};

const PHASE_LABEL_KEYS = {
  LOBBY:  'phase_lobby',
  ACTIVE: 'phase_active',
  VOTING: 'phase_voting',
  SETTLE: 'phase_settle',
  RECAP:  'phase_recap',
} as const satisfies Record<ArenaPhase, string>;

const SIZE  = 64;
const R     = 26;
const CX    = SIZE / 2;
const CY    = SIZE / 2;
const CIRC  = 2 * Math.PI * R;

function formatSeconds(s: number): string {
  const m = Math.floor(s / 60);
  const sec = s % 60;
  return `${m}:${String(sec).padStart(2, '0')}`;
}

export function RoundTimer({
  phase,
  secondsRemaining,
  totalSeconds = 300,
}: RoundTimerProps) {
  const t = useTranslations('PageArenaDetail');
  const [local, setLocal] = useState(secondsRemaining);
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null);

  // Sync to server ticks
  useEffect(() => {
    setLocal(secondsRemaining);
  }, [secondsRemaining]);

  // Client-side countdown between WS ticks
  useEffect(() => {
    if (intervalRef.current) clearInterval(intervalRef.current);
    if (phase === 'LOBBY' || phase === 'RECAP') return;

    intervalRef.current = setInterval(() => {
      setLocal((prev) => (prev > 0 ? prev - 1 : 0));
    }, 1000);

    return () => {
      if (intervalRef.current) clearInterval(intervalRef.current);
    };
  }, [phase, secondsRemaining]);

  const fraction = totalSeconds > 0
    ? Math.max(0, Math.min(1, local / totalSeconds))
    : 0;

  const dashOffset = CIRC * (1 - fraction);
  const phaseColor  = PHASE_COLORS[phase];
  const phaseLabel  = t(PHASE_LABEL_KEYS[phase]);
  const isUrgent    = local <= 10 && phase !== 'LOBBY' && phase !== 'RECAP';

  return (
    <div style={styles.wrap}>
      <svg
        width={SIZE}
        height={SIZE}
        viewBox={`0 0 ${SIZE} ${SIZE}`}
        style={{ display: 'block' }}
        aria-label={t('timer_aria', { phase: phaseLabel, seconds: local })}
      >
        {/* Background track */}
        <circle
          cx={CX} cy={CY} r={R}
          fill="none"
          stroke={color.line2}
          strokeWidth="3"
        />
        {/* Progress arc */}
        <circle
          cx={CX} cy={CY} r={R}
          fill="none"
          stroke={phaseColor}
          strokeWidth="3"
          strokeLinecap="round"
          strokeDasharray={CIRC}
          strokeDashoffset={dashOffset}
          transform={`rotate(-90 ${CX} ${CY})`}
          style={{
            transition:   'stroke-dashoffset 0.9s linear, stroke 0.3s',
            filter:       isUrgent ? `drop-shadow(0 0 6px ${phaseColor})` : undefined,
          }}
        />
        {/* Center text */}
        <text
          x={CX} y={CY - 4}
          textAnchor="middle"
          dominantBaseline="middle"
          style={{
            fontSize:   10,
            fontFamily: 'var(--mono)',
            fill:       phaseColor,
            fontWeight: 600,
          }}
        >
          {formatSeconds(local)}
        </text>
        <text
          x={CX} y={CY + 9}
          textAnchor="middle"
          dominantBaseline="middle"
          style={{
            fontSize:   7,
            fontFamily: 'var(--sans)',
            fill:       color.txFaint,
            letterSpacing: '0.04em',
          }}
        >
          {phaseLabel}
        </text>
      </svg>
    </div>
  );
}

const styles = {
  wrap: {
    display:     'flex',
    alignItems:  'center',
    justifyContent: 'center',
    flexShrink:  0,
  },
} as const;
