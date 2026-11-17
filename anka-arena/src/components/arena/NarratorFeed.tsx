'use client';

/**
 * NarratorFeed — Horizontal scrolling ticker with typewriter effect.
 *
 * - Receives narrator.speech messages via the arenaStore.
 * - Each new message is typed character-by-character at 30ms/char.
 * - After typing completes, scrolls left and queues the next narration.
 */

import { useEffect, useRef, useState } from 'react';
import { useTranslations } from 'next-intl';
import { useArenaStore } from '@/stores/arena';
import { color } from '@/design-system/tokens';

const CHAR_DELAY_MS  = 30;
const SEPARATOR      = '   ·   ';

export function NarratorFeed() {
  const t = useTranslations('PageArenaDetail');
  const narrations = useArenaStore((s) => s.narrations);

  // Current displayed text (typewriter buffer)
  const [displayed, setDisplayed] = useState('');
  const [queueIdx, setQueueIdx]   = useState(0);

  const timerRef     = useRef<ReturnType<typeof setTimeout> | null>(null);
  const charIdxRef   = useRef(0);
  const narrationRef = useRef(narrations);
  narrationRef.current = narrations;

  // Start typing the current narration
  useEffect(() => {
    const current = narrations[queueIdx];
    if (!current) return;

    charIdxRef.current = 0;
    setDisplayed('');

    function typeNext() {
      const text = narrationRef.current[queueIdx] ?? '';
      if (charIdxRef.current >= text.length) {
        // Finished typing — pause then advance to next narration
        timerRef.current = setTimeout(() => {
          setQueueIdx((i) => {
            const next = i + 1;
            return next >= narrationRef.current.length ? 0 : next;
          });
        }, 3000);
        return;
      }
      setDisplayed(text.slice(0, charIdxRef.current + 1));
      charIdxRef.current += 1;
      timerRef.current = setTimeout(typeNext, CHAR_DELAY_MS);
    }

    timerRef.current = setTimeout(typeNext, CHAR_DELAY_MS);

    return () => {
      if (timerRef.current) clearTimeout(timerRef.current);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [queueIdx]);

  // When new narration arrives at front, reset to index 0
  useEffect(() => {
    if (narrations.length > 0 && queueIdx >= narrations.length) {
      setQueueIdx(0);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [narrations.length]);

  if (narrations.length === 0) {
    return (
      <div style={styles.bar}>
        <span style={styles.label}>{t('narrator_label')}</span>
        <span style={{ ...styles.text, color: color.txFaint }}>
          {t('narrator_waiting')}
        </span>
      </div>
    );
  }

  return (
    <div style={styles.bar} aria-live="polite" aria-label={t('narrator_aria')}>
      <span style={styles.label}>{t('narrator_label')}</span>
      <div style={styles.track}>
        <span style={styles.text}>
          {displayed}
          <span style={styles.cursor} aria-hidden="true" />
        </span>
        {/* Decorative trailing separator when text is long */}
        {displayed.length > 60 && (
          <span style={styles.sep}>{SEPARATOR}</span>
        )}
      </div>
    </div>
  );
}

const styles = {
  bar: {
    display:     'flex',
    alignItems:  'center',
    gap:         12,
    padding:     '0 16px',
    height:      36,
    background:  color.bgInset,
    borderTop:   `1px solid ${color.line}`,
    overflow:    'hidden',
    flexShrink:  0,
  },
  label: {
    fontSize:      10,
    fontFamily:    'var(--mono)',
    letterSpacing: '0.1em',
    color:         color.brand,
    textTransform: 'uppercase' as const,
    flexShrink:    0,
    fontWeight:    600,
  },
  track: {
    flex:       1,
    overflow:   'hidden',
    display:    'flex',
    alignItems: 'center',
  },
  text: {
    fontSize:      13,
    fontFamily:    'var(--sans)',
    color:         color.tx,
    whiteSpace:    'nowrap' as const,
    overflow:      'hidden',
    textOverflow:  'ellipsis',
  },
  sep: {
    fontSize:   13,
    color:      color.txFaint,
    flexShrink: 0,
  },
  cursor: {
    display:          'inline-block',
    width:            2,
    height:           12,
    background:       color.brand,
    marginLeft:       2,
    verticalAlign:    'middle',
    animation:        'cursor-blink 0.7s step-end infinite',
    // The @keyframes is injected via globals.css or inline below
  },
} as const;
