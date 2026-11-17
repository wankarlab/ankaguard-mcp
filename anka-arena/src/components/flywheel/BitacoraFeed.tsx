'use client';

import { useEffect, useRef } from 'react';
import clsx from 'clsx';
import { useTranslations } from 'next-intl';
import { useUIStore } from '@/stores/ui';
import type { FlywheelEvent } from '@/hooks/useEngineStream';

// ──────────────────────────────────────────────────────────────
//  Tone → color
// ──────────────────────────────────────────────────────────────

const TONE_COLOR: Record<string, string> = {
  up:      'var(--up)',
  down:    'var(--real)',
  bad:     'var(--real)',
  paper:   'var(--paper)',
  brand:   'var(--brand)',
  micro:   'var(--micro)',
  neutral: 'var(--tx-faint)',
};

// ──────────────────────────────────────────────────────────────
//  Kind → icon label (text-based, no dep)
// ──────────────────────────────────────────────────────────────

const KIND_ICON: Record<string, string> = {
  gate_decision: '⛩',
  trade_close:   '💹',
  spec_promoted: '🏆',
  retrain:       '🔄',
  cohort:        '🎓',
};

// ──────────────────────────────────────────────────────────────
//  Props
// ──────────────────────────────────────────────────────────────

interface BitacoraFeedProps {
  events:    FlywheelEvent[];
  className?: string;
}

// ──────────────────────────────────────────────────────────────
//  Component
// ──────────────────────────────────────────────────────────────

export function BitacoraFeed({ events, className }: BitacoraFeedProps) {
  const t = useTranslations('UiFlywheel');
  const openExplain = useUIStore((s) => s.openExplain);
  const bottomRef   = useRef<HTMLDivElement>(null);

  // Scroll to bottom when new events arrive
  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [events.length]);

  function formatTime(ts: number): string {
    const d = new Date(ts);
    return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}:${String(d.getSeconds()).padStart(2, '0')}`;
  }

  return (
    <div
      className={clsx(className)}
      style={{
        overflowY: 'auto',
        padding:   '6px 0',
        flex:      1,
        minHeight: 0,
        // Thin scrollbar
        scrollbarWidth:   'thin',
        scrollbarColor:   'var(--line-2) transparent',
      }}
    >
      {events.length === 0 && (
        <div
          style={{
            padding:    '24px 16px',
            textAlign:  'center',
            color:      'var(--tx-faint)',
            fontFamily: 'var(--mono)',
            fontSize:   11,
          }}
        >
          {t('awaiting_events')}
        </div>
      )}

      {/* Render newest-last (events array is newest-first from hook) */}
      {[...events].reverse().map((e) => {
        const isReal = e.kind === 'trade_close' && e.real;
        const dotColor = TONE_COLOR[e.tone] ?? 'var(--tx-faint)';
        const icon = KIND_ICON[e.kind] ?? '·';

        return (
          <div
            key={e.id}
            role={isReal ? 'button' : undefined}
            tabIndex={isReal ? 0 : undefined}
            aria-label={isReal ? t('view_trade_detail') : undefined}
            onClick={() => {
              if (isReal && e.tradeId) {
                openExplain(e.tradeId, 'tree');
              }
            }}
            onKeyDown={(ev) => {
              if (isReal && e.tradeId && ev.key === 'Enter') {
                openExplain(e.tradeId, 'tree');
              }
            }}
            style={{
              display:    'flex',
              gap:        10,
              padding:    '8px 15px',
              alignItems: 'flex-start',
              cursor:     isReal ? 'pointer' : 'default',
              transition: 'background .15s',
            }}
            className={clsx(isReal && 'fw-event-real')}
            onMouseEnter={(ev) => {
              if (isReal) (ev.currentTarget as HTMLElement).style.background = 'var(--bg-hover)';
            }}
            onMouseLeave={(ev) => {
              if (isReal) (ev.currentTarget as HTMLElement).style.background = '';
            }}
          >
            {/* Timestamp */}
            <span
              className="mono"
              style={{
                fontSize:   9.5,
                color:      'var(--tx-faint)',
                flex:       'none',
                marginTop:  1,
                minWidth:   56,
              }}
            >
              {formatTime(e.ts)}
            </span>

            {/* Kind icon (emoji) */}
            <span
              style={{ fontSize: 12, flex: 'none', marginTop: 0, lineHeight: 1.5 }}
              aria-hidden="true"
            >
              {icon}
            </span>

            {/* Dot */}
            <span
              style={{
                width:        6,
                height:       6,
                borderRadius: '50%',
                marginTop:    6,
                flex:         'none',
                background:   dotColor,
                flexShrink:   0,
              }}
            />

            {/* Text */}
            <span style={{ fontSize: 11.5, color: 'var(--tx)', lineHeight: 1.45 }}>
              {e.txt}
              {isReal && (
                <span className="dim" style={{ fontSize: 10, color: 'var(--tx-faint)' }}>
                  {' '}· {t('see_why')}
                </span>
              )}
            </span>
          </div>
        );
      })}

      {/* Scroll anchor */}
      <div ref={bottomRef} />
    </div>
  );
}
