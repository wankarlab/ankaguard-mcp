'use client';

/**
 * ExplainTree — Skin "Arbol de decision" del Visor de Explicabilidad.
 * Acordeon de 3 secciones: Por que entro / Que paso / Que aprendio.
 */

import { useState } from 'react';
import { useTranslations } from 'next-intl';

// ---- Types ----------------------------------------------------------------

export interface GateStep {
  name: string;
  value: string;
  score: number;         // 0-1 confidence
  passed: boolean | 'soft';
  reason: string;
}

export interface ExplainData {
  id: string;
  pair: string;
  side: 'LONG' | 'SHORT';
  pnl?: number;
  pnlPct?: number;
  entryPrice?: number;
  closePrice?: number;
  durationMin?: number;
  mfe?: number;          // max favorable excursion %
  mae?: number;          // max adverse excursion %
  size?: number;
  sl?: number;
  tp?: number;
  regime?: string;
  volTier?: string;
  gates: GateStep[];
  systemNote?: string;   // "Que aprendio" free text from backend
  fsRow?: string;        // feature_store row id
}

// ---- Helpers ---------------------------------------------------------------

function usd(v: number) {
  return '$' + Math.abs(v).toFixed(2);
}

// ---- Sub-components --------------------------------------------------------

function ChevronIcon({ open }: { open: boolean }) {
  return (
    <svg
      width="12"
      height="12"
      viewBox="0 0 12 12"
      fill="none"
      style={{
        transform: open ? 'rotate(90deg)' : 'none',
        transition: 'transform 0.15s',
        flexShrink: 0,
        color: 'var(--tx-dim)',
      }}
    >
      <path d="M4 2l4 4-4 4" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

function GateStatus({ passed }: { passed: boolean | 'soft' }) {
  if (passed === true)   return <span style={{ color: 'var(--up)',    fontFamily: 'var(--mono)', fontSize: 13, lineHeight: 1 }}>✓</span>;
  if (passed === 'soft') return <span style={{ color: 'var(--micro)', fontFamily: 'var(--mono)', fontSize: 13, lineHeight: 1 }}>◐</span>;
  return                        <span style={{ color: 'var(--down)',  fontFamily: 'var(--mono)', fontSize: 13, lineHeight: 1 }}>✕</span>;
}

function SectionHeader({
  open,
  onToggle,
  label,
  style,
}: {
  open: boolean;
  onToggle: () => void;
  label: string;
  style?: React.CSSProperties;
}) {
  return (
    <button
      onClick={onToggle}
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 8,
        width: '100%',
        background: 'none',
        border: 'none',
        cursor: 'pointer',
        padding: '7px 0',
        color: 'var(--tx-hi)',
        fontFamily: 'var(--sans)',
        fontSize: 12.5,
        fontWeight: 600,
        textAlign: 'left',
        ...style,
      }}
    >
      <ChevronIcon open={open} />
      {label}
    </button>
  );
}

function Branch({ children }: { children: React.ReactNode }) {
  return (
    <div
      style={{
        borderLeft: '1px solid var(--line-2)',
        marginLeft: 5,
        paddingLeft: 14,
        display: 'flex',
        flexDirection: 'column',
        gap: 6,
        paddingBottom: 4,
      }}
    >
      {children}
    </div>
  );
}

function TreeRow({ icon, children }: { icon: React.ReactNode; children: React.ReactNode }) {
  return (
    <div style={{ display: 'flex', alignItems: 'flex-start', gap: 8 }}>
      <div style={{ width: 16, display: 'flex', justifyContent: 'center', paddingTop: 1, flexShrink: 0 }}>
        {icon}
      </div>
      <div style={{ flex: 1, fontSize: 12.5, color: 'var(--tx)', lineHeight: 1.5 }}>
        {children}
      </div>
    </div>
  );
}

// ---- Main component --------------------------------------------------------

interface ExplainTreeProps {
  gates: GateStep[];
  data: ExplainData;
}

export function ExplainTree({ gates, data }: ExplainTreeProps) {
  const t = useTranslations('Explain');
  const [open, setOpen] = useState({ entry: true, what: true, learned: false });

  const toggleEntry   = () => setOpen((s) => ({ ...s, entry:   !s.entry }));
  const toggleWhat    = () => setOpen((s) => ({ ...s, what:    !s.what }));
  const toggleLearned = () => setOpen((s) => ({ ...s, learned: !s.learned }));

  const hasPnl = data.pnl !== undefined;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>

      {/* ---- SECCION 1: Por que entro ---- */}
      <SectionHeader open={open.entry} onToggle={toggleEntry} label={t('tree_why_entered')} />

      {open.entry && (
        <Branch>
          {gates.map((gate, i) => (
            <TreeRow key={i} icon={<GateStatus passed={gate.passed} />}>
              <div>
                <span style={{ color: 'var(--tx-hi)', fontWeight: 550 }}>{gate.name}:</span>
                {' '}
                <span
                  style={{
                    color:
                      gate.passed === true
                        ? 'var(--tx)'
                        : gate.passed === 'soft'
                        ? 'var(--micro)'
                        : 'var(--down)',
                  }}
                >
                  {gate.value}
                </span>
                {' '}
                <span style={{ color: 'var(--tx-faint)', fontSize: 11 }}>
                  ({t('conf', { score: gate.score.toFixed(2) })})
                </span>
              </div>
              <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginTop: 2 }}>
                {gate.reason}
              </div>
            </TreeRow>
          ))}

          {/* Sizing summary */}
          {(data.size || data.sl || data.tp) && (
            <TreeRow icon={<span style={{ color: 'var(--tx-faint)', fontFamily: 'var(--mono)' }}>·</span>}>
              <span style={{ color: 'var(--tx-dim)', fontSize: 11.5 }}>
                {data.size && <><span style={{ color: 'var(--tx-hi)' }}>{usd(data.size)}</span> {t('size')}</>}
                {data.sl   && <> · SL <span style={{ color: 'var(--down)' }}>{data.sl}%</span></>}
                {data.tp   && <> · TP <span style={{ color: 'var(--up)' }}>+{data.tp}%</span></>}
              </span>
            </TreeRow>
          )}
        </Branch>
      )}

      {/* ---- SECCION 2: Que paso ---- */}
      <SectionHeader open={open.what} onToggle={toggleWhat} label={t('tree_what_happened')} style={{ marginTop: 4 }} />

      {open.what && (
        <Branch>
          {hasPnl && (
            <TreeRow
              icon={
                <span style={{ color: (data.pnl ?? 0) >= 0 ? 'var(--up)' : 'var(--down)', fontFamily: 'var(--mono)' }}>
                  {(data.pnl ?? 0) >= 0 ? '▲' : '▼'}
                </span>
              }
            >
              <span style={{ color: 'var(--tx-hi)', fontWeight: 600 }}>
                {(data.pnl ?? 0) >= 0 ? '+' : ''}{data.pnl?.toFixed(2)} USD
              </span>
              {data.pnlPct !== undefined && (
                <span style={{ color: 'var(--tx-dim)', fontSize: 11, marginLeft: 6 }}>
                  ({data.pnlPct >= 0 ? '+' : ''}{data.pnlPct.toFixed(2)}%)
                </span>
              )}
            </TreeRow>
          )}

          {(data.entryPrice || data.closePrice) && (
            <TreeRow icon={<span style={{ color: 'var(--tx-faint)', fontFamily: 'var(--mono)' }}>→</span>}>
              <span style={{ color: 'var(--tx-dim)', fontSize: 11.5 }}>
                {data.entryPrice && <>Entry <span style={{ color: 'var(--tx-hi)' }}>${data.entryPrice.toFixed(4)}</span></>}
                {data.closePrice && <> → Close <span style={{ color: 'var(--tx-hi)' }}>${data.closePrice.toFixed(4)}</span></>}
                {data.durationMin && (
                  <span style={{ color: 'var(--tx-faint)', fontSize: 11 }}>
                    {' '}({data.durationMin}m)
                  </span>
                )}
              </span>
            </TreeRow>
          )}

          {(data.mfe !== undefined || data.mae !== undefined) && (
            <TreeRow icon={<span style={{ color: 'var(--tx-faint)', fontFamily: 'var(--mono)' }}>~</span>}>
              <span style={{ fontSize: 11.5 }}>
                {data.mfe !== undefined && (
                  <>MFE <span style={{ color: 'var(--up)' }}>+{data.mfe}%</span></>
                )}
                {data.mae !== undefined && (
                  <> · MAE <span style={{ color: 'var(--down)' }}>{data.mae}%</span></>
                )}
              </span>
            </TreeRow>
          )}

          {(!hasPnl && data.mfe === undefined && !data.entryPrice) && (
            <TreeRow icon={<span style={{ color: 'var(--tx-faint)' }}>·</span>}>
              <span style={{ color: 'var(--tx-dim)', fontSize: 11.5 }}>{t('no_result_data')}</span>
            </TreeRow>
          )}
        </Branch>
      )}

      {/* ---- SECCION 3: Que aprendio el sistema ---- */}
      <SectionHeader open={open.learned} onToggle={toggleLearned} label={t('tree_what_learned')} style={{ marginTop: 4 }} />

      {open.learned && (
        <Branch>
          {data.systemNote ? (
            <TreeRow icon={<span style={{ color: 'var(--brand)', fontFamily: 'var(--mono)' }}>→</span>}>
              <span style={{ fontSize: 12, lineHeight: 1.6 }}>{data.systemNote}</span>
            </TreeRow>
          ) : (
            <>
              {data.fsRow && (
                <TreeRow icon={<span style={{ color: 'var(--brand)', fontFamily: 'var(--mono)' }}>→</span>}>
                  <span style={{ fontSize: 11.5 }}>
                    {t('feature_store_row')}{' '}
                    <span style={{ color: 'var(--tx-hi)', fontFamily: 'var(--mono)' }}>#{data.fsRow}</span>
                    {` · ${t('feeds_retrain')}`}
                  </span>
                </TreeRow>
              )}
              {!data.fsRow && (
                <TreeRow icon={<span style={{ color: 'var(--tx-faint)' }}>·</span>}>
                  <span style={{ color: 'var(--tx-dim)', fontSize: 11.5 }}>
                    {t('no_system_note')}
                  </span>
                </TreeRow>
              )}
            </>
          )}
        </Branch>
      )}
    </div>
  );
}
