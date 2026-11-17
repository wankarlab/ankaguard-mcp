'use client';

import React from 'react';
import type { SpecState } from '@/lib/spec-studio';

// ── Regime radio chips ───────────────────────────────────────────────────────

const REGIMES = ['TRENDING', 'RANGING', 'BREAKOUT', 'ANY'] as const;

const REGIME_COLOR: Record<string, string> = {
  TRENDING: 'var(--up)',
  RANGING:  'var(--paper)',
  BREAKOUT: 'var(--micro)',
  ANY:      'var(--tx-dim)',
};

// ── BlockEditor ──────────────────────────────────────────────────────────────

interface BlockEditorProps {
  spec:   SpecState;
  onChange: <K extends keyof SpecState>(key: K, value: SpecState[K]) => void;
}

export function BlockEditor({ spec, onChange }: BlockEditorProps) {
  return (
    <div style={{ padding: 14, display: 'flex', flexDirection: 'column', gap: 16 }}>

      {/* ── Nombre ── */}
      <div>
        <div className="eyebrow" style={{ marginBottom: 6 }}>nombre</div>
        <input
          value={spec.name}
          onChange={(e) => onChange('name', e.target.value)}
          style={{
            width: '100%',
            background: 'var(--bg-void)',
            border: '1px solid var(--line)',
            borderRadius: 6,
            padding: '8px 10px',
            color: 'var(--tx-hi)',
            fontFamily: 'var(--mono)',
            fontSize: 12,
          }}
        />
      </div>

      {/* ── Entrada ── */}
      <div className="panel-inset" style={{ padding: 12, borderColor: 'rgba(79,155,245,.3)' }}>
        <div className="eyebrow" style={{ color: 'var(--paper)', marginBottom: 10 }}>▸ entrada</div>

        {/* Régimen — radio chips */}
        <div style={{ marginBottom: 12 }}>
          <div style={{ fontSize: 11.5, color: 'var(--tx-dim)', marginBottom: 6 }}>régimen</div>
          <div style={{ display: 'flex', gap: 5, flexWrap: 'wrap' }}>
            {REGIMES.map((r) => {
              const active = spec.regime === r;
              const col    = REGIME_COLOR[r];
              return (
                <button
                  key={r}
                  onClick={() => onChange('regime', r)}
                  style={{
                    fontFamily:    'var(--mono)',
                    fontSize:      10.5,
                    letterSpacing: '0.06em',
                    padding:       '4px 10px',
                    borderRadius:  5,
                    border:        `1px solid ${active ? col : 'var(--line-2)'}`,
                    background:    active ? `color-mix(in srgb, ${col} 18%, transparent)` : 'var(--bg-inset)',
                    color:         active ? col : 'var(--tx-dim)',
                    cursor:        'pointer',
                    transition:    'all 0.12s',
                  }}
                >
                  {r}
                </button>
              );
            })}
          </div>
        </div>

        {/* RSI range slider doble punta */}
        <div>
          <div style={{ fontSize: 11.5, marginBottom: 6 }}>
            RSI&nbsp;
            <b className="num" style={{ color: 'var(--paper)' }}>{spec.rsiMin}</b>
            <span style={{ color: 'var(--tx-faint)' }}> – </span>
            <b className="num" style={{ color: 'var(--paper)' }}>{spec.rsiMax}</b>
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 11 }}>
              <span style={{ width: 28, color: 'var(--tx-faint)', fontFamily: 'var(--mono)' }}>min</span>
              <input
                type="range"
                min={20}
                max={spec.rsiMax - 1}
                value={spec.rsiMin}
                onChange={(e) => onChange('rsiMin', +e.target.value)}
                style={{ flex: 1, accentColor: 'var(--paper)' }}
              />
              <span className="num" style={{ width: 24, textAlign: 'right', fontSize: 11 }}>{spec.rsiMin}</span>
            </label>
            <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 11 }}>
              <span style={{ width: 28, color: 'var(--tx-faint)', fontFamily: 'var(--mono)' }}>max</span>
              <input
                type="range"
                min={spec.rsiMin + 1}
                max={80}
                value={spec.rsiMax}
                onChange={(e) => onChange('rsiMax', +e.target.value)}
                style={{ flex: 1, accentColor: 'var(--paper)' }}
              />
              <span className="num" style={{ width: 24, textAlign: 'right', fontSize: 11 }}>{spec.rsiMax}</span>
            </label>
          </div>
          <div className="mono faint" style={{ fontSize: 9.5, marginTop: 6 }}>
            + trend_filter: EMA50 1h
          </div>
        </div>
      </div>

      {/* ── Salida ── */}
      <div className="panel-inset" style={{ padding: 12, borderColor: 'rgba(54,205,134,.3)' }}>
        <div className="eyebrow" style={{ color: 'var(--up)', marginBottom: 10 }}>▸ salida</div>

        {/* TP slider */}
        <div style={{ marginBottom: 10 }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 5, fontSize: 11.5 }}>
            <span>Take Profit</span>
            <b className="up num">+{spec.tp.toFixed(1)}%</b>
          </div>
          <input
            type="range"
            min={0.3}
            max={5.0}
            step={0.1}
            value={spec.tp}
            onChange={(e) => onChange('tp', +e.target.value)}
            style={{ width: '100%', accentColor: 'var(--up)' }}
          />
          <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 2 }}>
            <span className="faint" style={{ fontSize: 9.5, fontFamily: 'var(--mono)' }}>0.3%</span>
            <span className="faint" style={{ fontSize: 9.5, fontFamily: 'var(--mono)' }}>5.0%</span>
          </div>
        </div>

        {/* SL slider */}
        <div style={{ marginBottom: 10 }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 5, fontSize: 11.5 }}>
            <span>Stop Loss</span>
            <b className="down num">-{spec.sl.toFixed(1)}%</b>
          </div>
          <input
            type="range"
            min={0.2}
            max={3.0}
            step={0.1}
            value={spec.sl}
            onChange={(e) => onChange('sl', +e.target.value)}
            style={{ width: '100%', accentColor: 'var(--down)' }}
          />
          <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 2 }}>
            <span className="faint" style={{ fontSize: 9.5, fontFamily: 'var(--mono)' }}>0.2%</span>
            <span className="faint" style={{ fontSize: 9.5, fontFamily: 'var(--mono)' }}>3.0%</span>
          </div>
        </div>

        {/* Trailing checkbox */}
        <label style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer', fontSize: 12 }}>
          <input
            type="checkbox"
            checked={spec.trail}
            onChange={(e) => onChange('trail', e.target.checked)}
            style={{ accentColor: 'var(--brand)', width: 14, height: 14 }}
          />
          <span>trailing stop</span>
          {spec.trail && (
            <span className="badge ok" style={{ marginLeft: 4 }}>ON</span>
          )}
        </label>
      </div>

      {/* Max hold */}
      <div>
        <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 5, fontSize: 11.5 }}>
          <span style={{ color: 'var(--tx-dim)' }}>max_hold</span>
          <b className="num" style={{ color: 'var(--tx-hi)' }}>{spec.maxHold} min</b>
        </div>
        <input
          type="range"
          min={15}
          max={1440}
          step={15}
          value={spec.maxHold}
          onChange={(e) => onChange('maxHold', +e.target.value)}
          style={{ width: '100%', accentColor: 'var(--brand)' }}
        />
      </div>

    </div>
  );
}
