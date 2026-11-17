'use client';

/**
 * ExplainDrawer — Panel deslizante desde la derecha.
 * Topbar: pair + side + tab switcher (Tree / Chain / Replay).
 * Carga datos via SWR GET /api/v1/trades/:id/explain cuando drawerTradeId cambia.
 * Feedback form con opciones: wrong_direction | wrong_regime | wrong_timing | ok.
 * Default export: ExplainDrawer (para layout.tsx).
 * Named exports: ExplainTree, ExplainChain, ExplainReplay (skins standalone).
 */

import { useState, useEffect, useCallback } from 'react';
import useSWR from 'swr';
import { useTranslations } from 'next-intl';

import { useUIStore } from '@/stores/ui';
import type { DrawerVariant } from '@/stores/ui';
import { apiFetch } from '@/lib/api';

import { ExplainTree }   from './ExplainTree';
import { ExplainChain }  from './ExplainChain';
import { ExplainReplay } from './ExplainReplay';
import type { GateStep, ExplainData } from './ExplainTree';
import type { Candle } from './ExplainReplay';

// ---- Re-export skin components so consumers can import from this barrel ----
export { ExplainTree, ExplainChain, ExplainReplay };
export type { GateStep, ExplainData, Candle };

// ---- API shape -------------------------------------------------------------

interface ExplainApiResponse {
  trade_id:     string;
  pair:         string;
  side:         'LONG' | 'SHORT';
  pnl?:         number;
  pnl_pct?:     number;
  entry_price?: number;
  close_price?: number;
  duration_min?: number;
  mfe?:         number;
  mae?:         number;
  size?:        number;
  sl?:          number;
  tp?:          number;
  regime?:      string;
  vol_tier?:    string;
  gates?: {
    name:   string;
    value:  string;
    score:  number;
    passed: boolean | 'soft';
    reason: string;
  }[];
  system_note?: string;
  fs_row?:      string;
  candles?:     Candle[];
  replay_text?: string;
  // Legacy fields from Academia agent (backward compat)
  narrative?:   string;
  chain?: {
    label:  string;
    result: string;
    ok:     boolean;
  }[];
}

interface MappedExplain {
  data:        ExplainData;
  candles:     Candle[];
  replayText?: string;
}

function mapResponse(raw: ExplainApiResponse): MappedExplain {
  // Map gates — handle both new shape and legacy
  const gates: GateStep[] = (raw.gates ?? []).map((g) => ({
    name:   g.name,
    value:  g.value,
    score:  g.score ?? 0.5,
    passed: g.passed,
    reason: g.reason,
  }));

  const data: ExplainData = {
    id:          raw.trade_id,
    pair:        raw.pair,
    side:        raw.side,
    pnl:         raw.pnl,
    pnlPct:      raw.pnl_pct,
    entryPrice:  raw.entry_price,
    closePrice:  raw.close_price,
    durationMin: raw.duration_min,
    mfe:         raw.mfe,
    mae:         raw.mae,
    size:        raw.size,
    sl:          raw.sl,
    tp:          raw.tp,
    regime:      raw.regime,
    volTier:     raw.vol_tier,
    systemNote:  raw.system_note ?? raw.narrative,
    fsRow:       raw.fs_row,
    gates,
  };

  return {
    data,
    candles:    raw.candles ?? [],
    replayText: raw.replay_text ?? raw.narrative,
  };
}

// ---- SWR fetcher -----------------------------------------------------------

const fetcher = (url: string) => apiFetch<ExplainApiResponse>(url);

// ---- Tabs ------------------------------------------------------------------

const TABS: { id: DrawerVariant; label: string }[] = [
  { id: 'tree',   label: 'Arbol' },
  { id: 'chain',  label: 'Pipeline' },
  { id: 'replay', label: 'Replay' },
];

// ---- Skeleton loading ------------------------------------------------------

function SkeletonLine({ h = 14, w = '100%' }: { h?: number; w?: string }) {
  return (
    <div
      style={{
        height: h,
        width: w,
        background: 'var(--bg-inset)',
        borderRadius: 'var(--r-sm)',
        border: '1px solid var(--line)',
        animation: 'livePulse 1.4s ease-in-out infinite',
      }}
    />
  );
}

function LoadingSkeleton() {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
      <SkeletonLine h={42} />
      <SkeletonLine h={18} w="55%" />
      <div style={{ height: 16 }} />
      {[0, 1, 2].map((i) => (
        <SkeletonLine key={i} h={64} />
      ))}
    </div>
  );
}

// ---- Feedback form ---------------------------------------------------------

type FeedbackType = 'wrong_direction' | 'wrong_regime' | 'wrong_timing' | 'ok';

const FEEDBACK_OPTIONS: { id: FeedbackType; label: string }[] = [
  { id: 'wrong_direction', label: 'Direccion incorrecta' },
  { id: 'wrong_regime',    label: 'Regimen mal detectado' },
  { id: 'wrong_timing',    label: 'Timing equivocado' },
  { id: 'ok',              label: 'Parece correcto' },
];

function FeedbackForm({ tradeId, onClose }: { tradeId: string; onClose: () => void }) {
  const t = useTranslations('Explain');
  const [selected, setSelected] = useState<FeedbackType | null>(null);
  const [sending,  setSending]  = useState(false);
  const [sent,     setSent]     = useState(false);

  const submit = useCallback(async () => {
    if (!selected || sending) return;
    setSending(true);
    try {
      await apiFetch(`/api/v1/trades/${tradeId}/feedback`, {
        method: 'POST',
        body: JSON.stringify({ type: selected }),
      });
    } catch {
      // Feedback is best-effort — silently continue
    } finally {
      setSending(false);
      setSent(true);
      setTimeout(onClose, 1800);
    }
  }, [tradeId, selected, sending, onClose]);

  if (sent) {
    return (
      <div
        style={{
          padding: '12px 14px',
          background: 'var(--up-soft)',
          border: '1px solid rgba(54,205,134,0.3)',
          borderRadius: 'var(--r)',
          fontFamily: 'var(--mono)',
          fontSize: 12,
          color: 'var(--up)',
          textAlign: 'center',
        }}
      >
        {t('feedback_sent')}
      </div>
    );
  }

  return (
    <div
      style={{
        background: 'var(--bg-inset)',
        border: '1px solid var(--line-2)',
        borderRadius: 'var(--r)',
        padding: 14,
      }}
    >
      <div
        style={{
          fontFamily: 'var(--mono)',
          fontSize: 10,
          letterSpacing: '0.12em',
          textTransform: 'uppercase',
          color: 'var(--tx-faint)',
          marginBottom: 10,
        }}
      >
        {t('feedback_type')}
      </div>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
        {FEEDBACK_OPTIONS.map((opt) => {
          const isSelected = selected === opt.id;
          return (
            <button
              key={opt.id}
              onClick={() => setSelected(opt.id)}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 9,
                padding: '8px 12px',
                borderRadius: 'var(--r)',
                border: `1px solid ${isSelected ? 'var(--brand)' : 'var(--line)'}`,
                background: isSelected ? 'var(--brand-soft)' : 'transparent',
                color: isSelected ? 'var(--tx-hi)' : 'var(--tx)',
                cursor: 'pointer',
                fontSize: 12.5,
                textAlign: 'left',
                transition: 'background 0.12s, border-color 0.12s',
              }}
            >
              <span
                style={{
                  width: 14,
                  height: 14,
                  borderRadius: '50%',
                  border: `2px solid ${isSelected ? 'var(--brand)' : 'var(--line-2)'}`,
                  background: isSelected ? 'var(--brand)' : 'transparent',
                  flexShrink: 0,
                }}
              />
              {t(`fb_${opt.id}`)}
            </button>
          );
        })}
      </div>

      <div style={{ display: 'flex', gap: 8, marginTop: 12 }}>
        <button
          onClick={onClose}
          style={{
            flex: 1,
            padding: '8px',
            borderRadius: 'var(--r)',
            border: '1px solid var(--line)',
            background: 'transparent',
            color: 'var(--tx-dim)',
            cursor: 'pointer',
            fontFamily: 'var(--mono)',
            fontSize: 11.5,
          }}
        >
          {t('cancel')}
        </button>
        <button
          onClick={submit}
          disabled={!selected || sending}
          style={{
            flex: 2,
            padding: '8px',
            borderRadius: 'var(--r)',
            border: 'none',
            background: selected
              ? 'linear-gradient(180deg, var(--brand), var(--brand-2))'
              : 'var(--bg-panel-2)',
            color: selected ? '#fff' : 'var(--tx-faint)',
            cursor: selected ? 'pointer' : 'default',
            fontFamily: 'var(--mono)',
            fontSize: 11.5,
            fontWeight: 600,
            opacity: sending ? 0.7 : 1,
          }}
        >
          {sending ? t('sending') : t('send')}
        </button>
      </div>
    </div>
  );
}

// ---- Main ExplainDrawer ----------------------------------------------------

export default function ExplainDrawer() {
  const t = useTranslations('Explain');
  const drawerOpen    = useUIStore((s) => s.drawerOpen);
  const drawerTradeId = useUIStore((s) => s.drawerTradeId);
  const drawerVariant = useUIStore((s) => s.drawerVariant);
  const closeDrawer   = useUIStore((s) => s.closeDrawer);
  const openExplain   = useUIStore((s) => s.openExplain);

  const [showFeedback, setShowFeedback] = useState(false);

  // Reset feedback form when drawer opens/closes or trade changes
  useEffect(() => {
    setShowFeedback(false);
  }, [drawerTradeId, drawerOpen]);

  // Escape key
  const handleKey = useCallback(
    (e: KeyboardEvent) => {
      if (e.key === 'Escape' && drawerOpen) closeDrawer();
    },
    [drawerOpen, closeDrawer],
  );

  useEffect(() => {
    window.addEventListener('keydown', handleKey);
    return () => window.removeEventListener('keydown', handleKey);
  }, [handleKey]);

  // Lock body scroll
  useEffect(() => {
    document.body.style.overflow = drawerOpen ? 'hidden' : '';
    return () => { document.body.style.overflow = ''; };
  }, [drawerOpen]);

  // SWR — key is null when closed so it doesn't fetch
  const swrKey = drawerOpen && drawerTradeId
    ? `/api/v1/trades/${drawerTradeId}/explain`
    : null;

  const { data: rawData, isLoading, error } = useSWR<ExplainApiResponse>(swrKey, fetcher, {
    revalidateOnFocus: false,
    dedupingInterval:  30_000,
    onErrorRetry: (err, _k, _cfg, retry, { retryCount }) => {
      if ((err as { status?: number })?.status === 404) return; // don't retry 404
      if (retryCount >= 1) return;
      setTimeout(() => retry({ retryCount }), 3000);
    },
  });

  const mapped: MappedExplain | null = rawData ? mapResponse(rawData) : null;

  // ---- Render ----
  return (
    <>
      {/* Overlay scrim */}
      <div
        onClick={closeDrawer}
        aria-hidden
        style={{
          position:   'fixed',
          inset:       0,
          background:  'rgba(6,8,12,0.55)',
          backdropFilter: 'blur(3px)',
          zIndex:      200,
          pointerEvents: drawerOpen ? 'auto' : 'none',
          opacity:     drawerOpen ? 1 : 0,
          transition:  'opacity 0.2s',
        }}
      />

      {/* Drawer panel — slides in from right */}
      <div
        role="dialog"
        aria-modal="true"
        aria-label={t('viewer_title')}
        style={{
          position:    'fixed',
          top:          0,
          right:        0,
          bottom:       0,
          width:        480,
          maxWidth:    '96vw',
          background:  'var(--bg-panel)',
          borderLeft:  '1px solid var(--line-2)',
          boxShadow:   '-20px 0 60px rgba(0,0,0,0.5)',
          zIndex:       201,
          display:     'flex',
          flexDirection: 'column',
          transform:   drawerOpen ? 'translateX(0)' : 'translateX(100%)',
          transition:  'transform 0.24s cubic-bezier(0.22, 1, 0.36, 1)',
        }}
      >
        {/* ---- Header: pair + side + close ---- */}
        <div
          style={{
            display:      'flex',
            alignItems:   'center',
            gap:           12,
            padding:      '14px 18px',
            borderBottom: '1px solid var(--line)',
            flexShrink:    0,
          }}
        >
          <div style={{ flex: 1, minWidth: 0 }}>
            {mapped ? (
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                {/* Pair */}
                <span
                  style={{
                    fontFamily:     'var(--mono)',
                    fontSize:        15,
                    fontWeight:      700,
                    color:          'var(--tx-hi)',
                    letterSpacing:  '0.02em',
                  }}
                >
                  {mapped.data.pair}
                </span>

                {/* Side badge */}
                <span
                  style={{
                    fontFamily:  'var(--mono)',
                    fontSize:     10,
                    fontWeight:   600,
                    padding:     '2px 8px',
                    borderRadius: 5,
                    border:      '1px solid transparent',
                    background:
                      mapped.data.side === 'LONG'
                        ? 'rgba(54,205,134,0.15)'
                        : 'rgba(240,86,79,0.15)',
                    color:
                      mapped.data.side === 'LONG' ? 'var(--up)' : 'var(--down)',
                    borderColor:
                      mapped.data.side === 'LONG'
                        ? 'rgba(54,205,134,0.35)'
                        : 'rgba(240,86,79,0.35)',
                  }}
                >
                  {mapped.data.side}
                </span>

                {/* PnL */}
                {mapped.data.pnl !== undefined && (
                  <span
                    style={{
                      fontFamily: 'var(--mono)',
                      fontSize:    13,
                      fontWeight:  600,
                      color:
                        mapped.data.pnl >= 0 ? 'var(--up)' : 'var(--down)',
                    }}
                  >
                    {mapped.data.pnl >= 0 ? '+' : ''}
                    ${Math.abs(mapped.data.pnl).toFixed(2)}
                  </span>
                )}

                {/* Trade ID */}
                <span
                  style={{
                    fontFamily: 'var(--mono)',
                    fontSize:    10,
                    color:      'var(--tx-faint)',
                  }}
                >
                  #{drawerTradeId}
                </span>
              </div>
            ) : (
              <div
                style={{
                  fontFamily: 'var(--mono)',
                  fontSize:    13,
                  color:      'var(--tx-dim)',
                }}
              >
                {isLoading ? t('loading') : t('viewer_title')}
              </div>
            )}
          </div>

          {/* Close button */}
          <button
            onClick={closeDrawer}
            aria-label={t('close_drawer')}
            style={{
              width:          30,
              height:         30,
              borderRadius:  'var(--r)',
              border:        '1px solid var(--line)',
              background:    'var(--bg-inset)',
              color:         'var(--tx-dim)',
              fontSize:       18,
              cursor:        'pointer',
              display:       'flex',
              alignItems:    'center',
              justifyContent: 'center',
              flexShrink:     0,
              lineHeight:     1,
            }}
          >
            ×
          </button>
        </div>

        {/* ---- Tab switcher: Tree / Pipeline / Replay ---- */}
        <div
          style={{
            display:      'flex',
            padding:      '0 18px',
            borderBottom: '1px solid var(--line)',
            flexShrink:    0,
            background:   'var(--bg-inset)',
          }}
        >
          {TABS.map((tab) => (
            <button
              key={tab.id}
              onClick={() =>
                drawerTradeId && openExplain(drawerTradeId, tab.id)
              }
              style={{
                padding:       '9px 14px',
                fontFamily:   'var(--mono)',
                fontSize:      11.5,
                color:
                  drawerVariant === tab.id
                    ? 'var(--tx-hi)'
                    : 'var(--tx-dim)',
                background:   'none',
                border:       'none',
                borderBottom:
                  drawerVariant === tab.id
                    ? '2px solid var(--brand)'
                    : '2px solid transparent',
                cursor:        'pointer',
                letterSpacing: '0.03em',
                transition:   'color 0.12s',
                marginBottom:  -1,
              }}
            >
              {t(`tab_${tab.id}`)}
            </button>
          ))}
        </div>

        {/* ---- Scrollable body ---- */}
        <div
          style={{
            flex:      1,
            overflowY: 'auto',
            padding:   '18px',
          }}
        >
          {/* Loading state */}
          {isLoading && <LoadingSkeleton />}

          {/* Error state */}
          {!isLoading && error && (
            <div
              style={{
                padding:      '18px',
                background:   'var(--real-soft)',
                border:       '1px solid rgba(240,86,79,0.3)',
                borderRadius: 'var(--r)',
                color:        'var(--down)',
                fontFamily:   'var(--mono)',
                fontSize:      12,
                textAlign:    'center',
              }}
            >
              {t('load_error')}
              <br />
              <span style={{ color: 'var(--tx-faint)', fontSize: 10.5 }}>
                {(error as Error).message ?? t('unknown_error')}
              </span>
            </div>
          )}

          {/* Empty state */}
          {!isLoading && !error && !mapped && (
            <div
              style={{
                textAlign: 'center',
                padding:    32,
                color:     'var(--tx-dim)',
                fontFamily: 'var(--mono)',
                fontSize:   12,
              }}
            >
              {t('select_trade')}
            </div>
          )}

          {/* Content */}
          {!isLoading && !error && mapped && (
            <>
              {/* Skin: Tree */}
              {drawerVariant === 'tree' && (
                <ExplainTree
                  gates={mapped.data.gates}
                  data={mapped.data}
                />
              )}

              {/* Skin: Chain */}
              {drawerVariant === 'chain' && (
                <ExplainChain
                  gates={mapped.data.gates}
                  data={mapped.data}
                />
              )}

              {/* Skin: Replay */}
              {drawerVariant === 'replay' && (
                <ExplainReplay
                  gates={mapped.data.gates}
                  data={mapped.data}
                  candles={mapped.candles}
                  replayText={mapped.replayText}
                />
              )}

              {/* Divider */}
              <hr
                style={{
                  height:     1,
                  background: 'var(--line)',
                  border:     0,
                  margin:    '20px 0 14px',
                }}
              />

              {/* Feedback section */}
              {!showFeedback ? (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                  <button
                    onClick={() => setShowFeedback(true)}
                    style={{
                      width:          '100%',
                      display:       'flex',
                      alignItems:    'center',
                      justifyContent: 'center',
                      gap:             8,
                      padding:        '8px 15px',
                      borderRadius:  'var(--r)',
                      border:        '1px solid var(--line-2)',
                      background:    'transparent',
                      color:         'var(--tx)',
                      cursor:        'pointer',
                      fontFamily:    'var(--sans)',
                      fontSize:       12.5,
                      fontWeight:     550,
                      transition:    'background 0.12s',
                    }}
                    onMouseOver={(e) =>
                      ((e.currentTarget as HTMLButtonElement).style.background =
                        'var(--bg-hover)')
                    }
                    onMouseOut={(e) =>
                      ((e.currentTarget as HTMLButtonElement).style.background =
                        'transparent')
                    }
                  >
                    {/* Alert icon */}
                    <svg
                      width="15"
                      height="15"
                      viewBox="0 0 15 15"
                      fill="none"
                      style={{ flexShrink: 0 }}
                    >
                      <path
                        d="M7.5 1.5a6 6 0 100 12 6 6 0 000-12zM7.5 5v3.5M7.5 10.5h.01"
                        stroke="currentColor"
                        strokeWidth="1.4"
                        strokeLinecap="round"
                      />
                    </svg>
                    {t('wrong_here')}
                  </button>

                  <div
                    style={{
                      fontFamily:  'var(--mono)',
                      fontSize:     10,
                      textAlign:   'center',
                      color:       'var(--tx-faint)',
                    }}
                  >
                    {t('report_feeds')}
                  </div>
                </div>
              ) : (
                <FeedbackForm
                  tradeId={drawerTradeId!}
                  onClose={() => setShowFeedback(false)}
                />
              )}
            </>
          )}
        </div>

        {/* ---- Footer hint ---- */}
        <div
          style={{
            padding:    '8px 18px',
            borderTop:  '1px solid var(--line)',
            flexShrink:  0,
            background: 'var(--bg-inset)',
            fontFamily: 'var(--mono)',
            fontSize:    10,
            color:      'var(--tx-faint)',
          }}
        >
          {t('esc_close')}
        </div>
      </div>
    </>
  );
}
