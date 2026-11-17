'use client';

/**
 * ExplainReplay — Skin "Replay narrado" del Visor de Explicabilidad.
 * Muestra texto narrativo + mini SVG chart OHLC (ultimas 10 velas) + marcadores entry/exit.
 * Chart 100% SVG, sin libreria externa.
 */

import { useMemo } from 'react';
import type { GateStep, ExplainData } from './ExplainTree';

// ---- Types -----------------------------------------------------------------

export interface Candle {
  open: number;
  high: number;
  low:  number;
  close: number;
  ts?:  number;
}

interface ExplainReplayProps {
  gates: GateStep[];
  data: ExplainData;
  candles?: Candle[];       // backend may supply real OHLC; fallback to synthetic
  replayText?: string;      // narrative text from backend
  entryIndex?: number;      // candle index where entry happened (0-based)
  exitIndex?: number;       // candle index where exit happened
}

// ---- Synthetic candles for fallback ----------------------------------------

function makeSyntheticCandles(data: ExplainData, count = 10): Candle[] {
  // Build plausible-looking candles around the entry price
  const base = data.entryPrice ?? 100;
  const vol  = base * 0.003;
  const seed = base * 1000;

  const candles: Candle[] = [];
  let price = base * (1 - 0.008);

  for (let i = 0; i < count; i++) {
    const rng = ((seed * (i + 7) * 2654435761) & 0xffffffff) / 0xffffffff;
    const trend = data.side === 'LONG' ? 0.0005 : -0.0005;
    const move = (rng - 0.48) * vol * 2 + trend * base;
    const open  = price;
    const close = price + move;
    const high  = Math.max(open, close) + Math.abs(move) * (0.3 + rng * 0.5);
    const low   = Math.min(open, close) - Math.abs(move) * (0.3 + (1 - rng) * 0.5);
    candles.push({ open, high, low, close });
    price = close;
  }
  return candles;
}

// ---- Mini SVG OHLC chart ---------------------------------------------------

const SVG_W = 340;
const SVG_H = 90;
const PAD_L = 4;
const PAD_R = 4;
const PAD_T = 6;
const PAD_B = 6;

function OhlcChart({
  candles,
  entryIndex,
  exitIndex,
  entryPrice,
  closePrice,
}: {
  candles: Candle[];
  entryIndex: number;
  exitIndex: number;
  entryPrice?: number;
  closePrice?: number;
}) {
  const n = candles.length;
  if (n === 0) return null;

  const allHighs = candles.map((c) => c.high);
  const allLows  = candles.map((c) => c.low);
  const maxP = Math.max(...allHighs);
  const minP = Math.min(...allLows);
  const rangeP = maxP - minP || 1;

  const chartW = SVG_W - PAD_L - PAD_R;
  const chartH = SVG_H - PAD_T - PAD_B;
  const colW   = chartW / n;
  const bodyW  = Math.max(3, colW * 0.55);

  function toX(i: number) {
    return PAD_L + i * colW + colW / 2;
  }

  function toY(price: number) {
    return PAD_T + chartH - ((price - minP) / rangeP) * chartH;
  }

  return (
    <svg
      viewBox={`0 0 ${SVG_W} ${SVG_H}`}
      style={{ width: '100%', height: SVG_H, display: 'block' }}
      aria-label="Mini chart OHLC"
    >
      {/* Grid line at midpoint */}
      <line
        x1={PAD_L} y1={PAD_T + chartH / 2}
        x2={SVG_W - PAD_R} y2={PAD_T + chartH / 2}
        stroke="var(--line)"
        strokeWidth="1"
        strokeDasharray="3 3"
      />

      {/* Candles */}
      {candles.map((c, i) => {
        const isUp  = c.close >= c.open;
        const color = isUp ? 'var(--up)' : 'var(--down)';
        const cx    = toX(i);
        const bodyTop    = toY(Math.max(c.open, c.close));
        const bodyBottom = toY(Math.min(c.open, c.close));
        const bodyH = Math.max(1.5, bodyBottom - bodyTop);

        return (
          <g key={i}>
            {/* Wick */}
            <line
              x1={cx} y1={toY(c.high)}
              x2={cx} y2={toY(c.low)}
              stroke={color}
              strokeWidth="1"
              opacity="0.7"
            />
            {/* Body */}
            <rect
              x={cx - bodyW / 2}
              y={bodyTop}
              width={bodyW}
              height={bodyH}
              fill={color}
              opacity="0.85"
            />
          </g>
        );
      })}

      {/* Entry marker */}
      {entryIndex >= 0 && entryIndex < n && (
        <g>
          <circle
            cx={toX(entryIndex)}
            cy={toY(entryPrice ?? candles[entryIndex].close)}
            r="5"
            fill="var(--up)"
            stroke="var(--bg-panel)"
            strokeWidth="1.5"
          />
          <text
            x={toX(entryIndex) + 8}
            y={toY(entryPrice ?? candles[entryIndex].close) - 2}
            style={{ fontSize: 8, fill: 'var(--up)', fontFamily: 'var(--mono)' }}
          >
            ENTRY
          </text>
        </g>
      )}

      {/* Exit marker */}
      {exitIndex >= 0 && exitIndex < n && (
        <g>
          <circle
            cx={toX(exitIndex)}
            cy={toY(closePrice ?? candles[exitIndex].close)}
            r="5"
            fill="var(--micro)"
            stroke="var(--bg-panel)"
            strokeWidth="1.5"
          />
          <text
            x={Math.max(4, toX(exitIndex) - 36)}
            y={toY(closePrice ?? candles[exitIndex].close) - 2}
            style={{ fontSize: 8, fill: 'var(--micro)', fontFamily: 'var(--mono)' }}
          >
            EXIT
          </text>
        </g>
      )}
    </svg>
  );
}

// ---- Narrative builder -----------------------------------------------------

function buildNarrative(data: ExplainData, gates: GateStep[]): string {
  const regime  = data.regime  ?? 'desconocido';
  const volTier = data.volTier ?? 'normal';

  const passedGates = gates.filter((g) => g.passed !== false);
  const failedGates = gates.filter((g) => g.passed === false);

  let text = `El sistema detecto regimen ${regime} (vol-tier ${volTier}).`;

  if (passedGates.length > 0) {
    const names = passedGates.map((g) => g.name).join(', ');
    text += ` Los gates [${names}] aprobaron la entrada.`;
  }

  if (failedGates.length > 0) {
    const names = failedGates.map((g) => g.name).join(', ');
    text += ` El gate [${names}] fallo — la entrada fue bloqueada.`;
  }

  if (data.size) {
    text += ` Entro con $${data.size.toFixed(2)}`;
    if (data.sl)  text += ` SL ${data.sl}%`;
    if (data.tp)  text += ` TP +${data.tp}%`;
    text += '.';
  }

  if (data.mfe !== undefined) {
    text += ` Llego a un maximo favorable de +${data.mfe}%`;
    if (data.mae !== undefined) text += ` y un retroceso maximo de ${data.mae}%`;
    text += '.';
  }

  if (data.pnl !== undefined) {
    const sign = data.pnl >= 0 ? '+' : '';
    text += ` Cerro en ${sign}$${data.pnl.toFixed(2)}`;
    if (data.pnlPct !== undefined) text += ` (${sign}${data.pnlPct.toFixed(2)}%)`;
    text += '.';
  }

  return text;
}

// ---- Disclaimer ------------------------------------------------------------

function AntiSurvivorshipNote() {
  return (
    <div
      style={{
        display: 'flex',
        gap: 9,
        alignItems: 'flex-start',
        fontSize: 11,
        color: 'var(--tx-dim)',
        background: 'var(--micro-soft)',
        border: '1px solid rgba(233,162,62,0.25)',
        borderRadius: 'var(--r)',
        padding: '9px 12px',
        marginTop: 12,
      }}
    >
      <span style={{ color: 'var(--micro)', flexShrink: 0, fontSize: 13 }}>!</span>
      <span>
        <strong>Anti-survivorship:</strong> este feed mezcla ganadores y perdedores.
        Ver solo trades exitosos distorsiona la percepcion. Rendimiento pasado no garantiza resultados futuros.
      </span>
    </div>
  );
}

// ---- Main component --------------------------------------------------------

export function ExplainReplay({
  gates,
  data,
  candles: externalCandles,
  replayText,
  entryIndex,
  exitIndex,
}: ExplainReplayProps) {
  const candles = useMemo<Candle[]>(() => {
    if (externalCandles && externalCandles.length > 0) {
      return externalCandles.slice(-10);
    }
    return makeSyntheticCandles(data, 10);
  }, [externalCandles, data]);

  const resolvedEntryIndex = entryIndex ?? Math.floor(candles.length * 0.5);
  const resolvedExitIndex  = exitIndex  ?? candles.length - 1;

  const narrative = replayText ?? buildNarrative(data, gates);

  return (
    <div>
      {/* Chart panel */}
      <div
        style={{
          background: 'var(--bg-inset)',
          border: '1px solid var(--line)',
          borderRadius: 'var(--r)',
          padding: '12px 14px',
          marginBottom: 14,
        }}
      >
        {/* Panel header */}
        <div
          style={{
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
            marginBottom: 10,
          }}
        >
          <span
            style={{
              fontFamily: 'var(--mono)',
              fontSize: 10,
              letterSpacing: '0.14em',
              textTransform: 'uppercase',
              color: 'var(--tx-faint)',
            }}
          >
            REPLAY · {data.pair}
          </span>

          {data.regime && (
            <span
              style={{
                fontFamily: 'var(--mono)',
                fontSize: 10,
                padding: '2px 8px',
                borderRadius: 5,
                border: '1px solid var(--line-2)',
                background: 'var(--bg-panel)',
                color: data.regime === 'TRENDING' ? 'var(--up)' : data.regime === 'RANGING' ? 'var(--paper)' : 'var(--micro)',
              }}
            >
              {data.regime}
              {data.volTier && ` · ${data.volTier}`}
            </span>
          )}
        </div>

        {/* OHLC SVG chart */}
        <OhlcChart
          candles={candles}
          entryIndex={resolvedEntryIndex}
          exitIndex={resolvedExitIndex}
          entryPrice={data.entryPrice}
          closePrice={data.closePrice}
        />

        {/* Legend */}
        <div
          style={{
            display: 'flex',
            gap: 14,
            marginTop: 8,
            fontFamily: 'var(--mono)',
            fontSize: 9.5,
            color: 'var(--tx-faint)',
          }}
        >
          <span><span style={{ color: 'var(--up)' }}>●</span> Entry</span>
          <span><span style={{ color: 'var(--micro)' }}>●</span> Exit</span>
          <span><span style={{ color: 'var(--up)' }}>■</span> Alcista</span>
          <span><span style={{ color: 'var(--down)' }}>■</span> Bajista</span>
        </div>
      </div>

      {/* Narrative text */}
      <div
        style={{
          fontSize: 13.5,
          lineHeight: 1.7,
          color: 'var(--tx)',
        }}
      >
        {narrative}
      </div>

      {/* Gate quick-summary */}
      {gates.length > 0 && (
        <div
          style={{
            display: 'flex',
            gap: 6,
            flexWrap: 'wrap',
            marginTop: 12,
          }}
        >
          {gates.map((g, i) => {
            const col =
              g.passed === true
                ? 'var(--up)'
                : g.passed === 'soft'
                ? 'var(--micro)'
                : 'var(--down)';
            return (
              <div
                key={i}
                style={{
                  display: 'inline-flex',
                  alignItems: 'center',
                  gap: 5,
                  padding: '2px 8px',
                  borderRadius: 5,
                  border: `1px solid ${col}44`,
                  background: `${col}11`,
                  fontFamily: 'var(--mono)',
                  fontSize: 10,
                  color: col,
                }}
              >
                {g.passed === true ? '✓' : g.passed === 'soft' ? '◐' : '✕'}
                {' '}{g.name}
              </div>
            );
          })}
        </div>
      )}

      {/* Anti-survivorship disclaimer */}
      <AntiSurvivorshipNote />
    </div>
  );
}
