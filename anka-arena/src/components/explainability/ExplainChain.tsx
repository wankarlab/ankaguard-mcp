'use client';

/**
 * ExplainChain — Skin "Pipeline horizontal" del Visor de Explicabilidad.
 * Muestra chain steps del explain API con flechas, resaltando el paso donde fallo.
 * En pantallas pequeñas se colapsa a layout vertical.
 */

import { useTranslations } from 'next-intl';
import type { GateStep, ExplainData } from './ExplainTree';

// ---- Helpers ---------------------------------------------------------------

function usd(v: number) {
  return '$' + Math.abs(v).toFixed(2);
}

function statusLabel(passed: boolean | 'soft') {
  if (passed === true)   return 'PASS';
  if (passed === 'soft') return 'SOFT';
  return 'FAIL';
}

function statusColor(passed: boolean | 'soft') {
  if (passed === true)   return 'var(--up)';
  if (passed === 'soft') return 'var(--micro)';
  return 'var(--down)';
}

function statusIcon(passed: boolean | 'soft') {
  if (passed === true)   return '✓';
  if (passed === 'soft') return '◐';
  return '✕';
}

// ---- Meter strip -----------------------------------------------------------

function MiniMeter({ value, color }: { value: number; color: string }) {
  return (
    <div
      style={{
        height: 4,
        borderRadius: 4,
        background: 'var(--bg-inset)',
        overflow: 'hidden',
        border: '1px solid var(--line)',
        marginTop: 6,
      }}
    >
      <div
        style={{
          height: '100%',
          width: `${Math.round(value * 100)}%`,
          background: color,
          borderRadius: 4,
          transition: 'width 0.4s',
        }}
      />
    </div>
  );
}

// ---- Step card -------------------------------------------------------------

function StepCard({ gate, isFail }: { gate: GateStep; isFail: boolean }) {
  const t = useTranslations('Explain');
  const col = statusColor(gate.passed);

  return (
    <div
      style={{
        flex: '1 0 130px',
        minWidth: 120,
        maxWidth: 200,
        padding: '11px 12px',
        background: isFail ? 'rgba(240,86,79,0.06)' : 'var(--bg-inset)',
        border: `1px solid ${isFail ? 'rgba(240,86,79,0.45)' : col + '44'}`,
        borderRadius: 'var(--r)',
        boxShadow: isFail ? '0 0 0 1px rgba(240,86,79,0.15), 0 4px 16px rgba(240,86,79,0.08)' : 'none',
      }}
    >
      {/* Status row */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 6 }}>
        <span
          style={{
            fontFamily: 'var(--mono)',
            fontSize: 9,
            letterSpacing: '0.14em',
            textTransform: 'uppercase',
            color: col,
            fontWeight: 600,
          }}
        >
          {statusLabel(gate.passed)}
        </span>
        <span style={{ color: col, fontSize: 14, lineHeight: 1 }}>{statusIcon(gate.passed)}</span>
      </div>

      {/* Gate name */}
      <div
        style={{
          fontFamily: 'var(--mono)',
          fontSize: 11,
          fontWeight: 600,
          color: 'var(--tx-hi)',
          marginBottom: 4,
          lineHeight: 1.3,
        }}
      >
        {gate.name}
      </div>

      {/* Gate value */}
      <div
        style={{
          fontSize: 10.5,
          color: 'var(--tx-dim)',
          minHeight: 28,
          lineHeight: 1.4,
          marginBottom: 6,
        }}
      >
        {gate.value}
      </div>

      {/* Confidence meter */}
      <MiniMeter value={gate.score} color={col} />
      <div
        style={{
          fontFamily: 'var(--mono)',
          fontSize: 9.5,
          color: 'var(--tx-faint)',
          marginTop: 3,
        }}
      >
        {t('conf', { score: gate.score.toFixed(2) })}
      </div>

      {/* Reason hint */}
      <div
        style={{
          fontSize: 10,
          color: 'var(--tx-faint)',
          marginTop: 6,
          lineHeight: 1.4,
          borderTop: '1px solid var(--line)',
          paddingTop: 6,
        }}
      >
        {gate.reason}
      </div>

      {/* Fail banner */}
      {isFail && (
        <div
          style={{
            marginTop: 8,
            background: 'rgba(240,86,79,0.15)',
            border: '1px solid rgba(240,86,79,0.4)',
            borderRadius: 'var(--r-sm)',
            padding: '3px 7px',
            fontFamily: 'var(--mono)',
            fontSize: 9,
            letterSpacing: '0.1em',
            textTransform: 'uppercase',
            color: 'var(--down)',
            textAlign: 'center',
          }}
        >
          {t('fail_point')}
        </div>
      )}
    </div>
  );
}

// ---- Arrow connector -------------------------------------------------------

function Arrow() {
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        color: 'var(--tx-faint)',
        fontSize: 16,
        flexShrink: 0,
        width: 20,
        userSelect: 'none',
      }}
    >
      →
    </div>
  );
}

// ---- Result card -----------------------------------------------------------

function ResultCard({ data, allPassed }: { data: ExplainData; allPassed: boolean }) {
  const t = useTranslations('Explain');
  const hasPnl = data.pnl !== undefined;
  const positive = (data.pnl ?? 0) >= 0;

  if (!allPassed) {
    // Rejected — show rejection card
    return (
      <div
        style={{
          flex: '1 0 120px',
          minWidth: 110,
          padding: '11px 12px',
          background: 'var(--real-soft)',
          border: '1px solid rgba(240,86,79,0.4)',
          borderRadius: 'var(--r)',
        }}
      >
        <div style={{ fontFamily: 'var(--mono)', fontSize: 9, letterSpacing: '0.14em', textTransform: 'uppercase', color: 'var(--down)', marginBottom: 6 }}>
          {t('rejected')}
        </div>
        <div style={{ fontSize: 16, fontWeight: 700, color: 'var(--down)', fontFamily: 'var(--mono)', marginTop: 4 }}>✕</div>
        <div style={{ fontSize: 10.5, color: 'var(--tx-dim)', marginTop: 6 }}>
          {t('rejected_desc')}
        </div>
      </div>
    );
  }

  return (
    <div
      style={{
        flex: '1 0 120px',
        minWidth: 110,
        padding: '11px 12px',
        background: hasPnl
          ? positive
            ? 'rgba(54,205,134,0.08)'
            : 'rgba(240,86,79,0.06)'
          : 'var(--bg-inset)',
        border: `1px solid ${hasPnl ? (positive ? 'rgba(54,205,134,0.35)' : 'rgba(240,86,79,0.35)') : 'var(--line)'}`,
        borderRadius: 'var(--r)',
      }}
    >
      <div style={{ fontFamily: 'var(--mono)', fontSize: 9, letterSpacing: '0.14em', textTransform: 'uppercase', color: hasPnl ? (positive ? 'var(--up)' : 'var(--down)') : 'var(--tx-dim)', marginBottom: 6 }}>
        {t('execution')}
      </div>

      {hasPnl ? (
        <>
          <div style={{ fontSize: 18, fontWeight: 700, color: positive ? 'var(--up)' : 'var(--down)', fontFamily: 'var(--mono)', letterSpacing: '-0.01em', marginTop: 4 }}>
            {positive ? '+' : ''}{usd(data.pnl!)}
          </div>
          {data.pnlPct !== undefined && (
            <div style={{ fontFamily: 'var(--mono)', fontSize: 10, color: 'var(--tx-dim)', marginTop: 3 }}>
              {data.pnlPct >= 0 ? '+' : ''}{data.pnlPct.toFixed(2)}%
            </div>
          )}
        </>
      ) : (
        <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 8 }}>{t('active_trade')}</div>
      )}

      <div style={{ fontFamily: 'var(--mono)', fontSize: 10, color: 'var(--tx-dim)', marginTop: 6 }}>
        {data.side}
        {data.size && ` · ${usd(data.size)}`}
      </div>
    </div>
  );
}

// ---- Stats row -------------------------------------------------------------

function StatsRow({ data }: { data: ExplainData }) {
  const t = useTranslations('Explain');
  const items: [string, string, string][] = [];
  if (data.mfe !== undefined) items.push(['MFE', `+${data.mfe}%`, 'var(--up)']);
  if (data.mae !== undefined) items.push(['MAE', `${data.mae}%`, 'var(--down)']);
  if (data.entryPrice)        items.push(['Entry', `$${data.entryPrice.toFixed(4)}`, 'var(--tx-hi)']);
  if (data.closePrice)        items.push(['Close', `$${data.closePrice.toFixed(4)}`, 'var(--tx-hi)']);
  if (data.durationMin)       items.push([t('duration'), `${data.durationMin}m`, 'var(--tx-dim)']);

  if (items.length === 0) return null;

  return (
    <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap', marginTop: 12 }}>
      {items.map(([k, v, c]) => (
        <div
          key={k}
          style={{
            padding: '8px 12px',
            background: 'var(--bg-inset)',
            border: '1px solid var(--line)',
            borderRadius: 'var(--r)',
            flex: '1 0 80px',
          }}
        >
          <div style={{ fontFamily: 'var(--mono)', fontSize: 9, letterSpacing: '0.1em', textTransform: 'uppercase', color: 'var(--tx-faint)' }}>{k}</div>
          <div style={{ fontFamily: 'var(--mono)', fontSize: 15, fontWeight: 600, color: c, marginTop: 3 }}>{v}</div>
        </div>
      ))}
    </div>
  );
}

// ---- Main component --------------------------------------------------------

interface ExplainChainProps {
  gates: GateStep[];
  data: ExplainData;
}

export function ExplainChain({ gates, data }: ExplainChainProps) {
  // Detect the first failing gate index
  const failIndex = gates.findIndex((g) => g.passed === false);
  const allPassed = failIndex === -1;

  return (
    <div>
      {/* Horizontal pipeline — wraps to vertical on small screens via flex-wrap */}
      <div
        style={{
          display: 'flex',
          alignItems: 'stretch',
          gap: 0,
          overflowX: 'auto',
          padding: '4px 0 10px',
          flexWrap: 'wrap',
          rowGap: 8,
        }}
      >
        {gates.map((gate, i) => (
          <div
            key={i}
            style={{ display: 'flex', alignItems: 'center', gap: 0, minWidth: 0 }}
          >
            <StepCard gate={gate} isFail={i === failIndex} />
            <Arrow />
          </div>
        ))}

        {/* Final result block */}
        <ResultCard data={data} allPassed={allPassed} />
      </div>

      {/* Stats row below */}
      <StatsRow data={data} />
    </div>
  );
}
