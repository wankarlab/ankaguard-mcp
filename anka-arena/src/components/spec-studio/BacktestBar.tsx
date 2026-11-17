'use client';

import React, { useState } from 'react';
import { useRouter } from '@/i18n/routing';
import type { SpecState } from '@/lib/spec-studio';
import { api, apiFetch, ApiError } from '@/lib/api';

// ── Types ────────────────────────────────────────────────────────────────────

interface BacktestResult {
  profit_factor:    number;
  win_rate:         number;
  total_trades:     number;
  sample:           string;
}

interface BacktestResponse {
  result: BacktestResult;
}

interface CreateSpecResponse {
  id?:          string;
  spec_id?:     string;
  content_hash?: string;
  status:       string;
}

// ── PF color helper ──────────────────────────────────────────────────────────

function pfColor(pf: number): string {
  if (pf >= 1.5) return 'var(--up)';
  if (pf >= 1.0) return 'var(--micro)';
  return 'var(--down)';
}

// ── BacktestBar ───────────────────────────────────────────────────────────────

interface BacktestBarProps {
  spec:   SpecState;
  specId: string | null;
  /** Skin-in-the-game live-trading days from the dynamic policy (null when the
   *  backend does not expose the threshold — never hardcode it). */
  skinInGameDays: number | null;
  onSpecCreated: (id: string, contentHash?: string) => void;
}

export function BacktestBar({ spec, specId, skinInGameDays, onSpecCreated }: BacktestBarProps) {
  const router = useRouter();

  const [btLoading,  setBtLoading]  = useState(false);
  const [btResult,   setBtResult]   = useState<BacktestResult | null>(null);
  const [btError,    setBtError]    = useState<string | null>(null);

  const [gateLoading, setGateLoading] = useState(false);
  const [gateError,   setGateError]   = useState<string | null>(null);

  // ── Backtest rápido ──────────────────────────────────────────────────────

  async function handleBacktest() {
    setBtLoading(true);
    setBtError(null);
    setBtResult(null);

    const id = specId ?? 'draft';
    try {
      const data = await api.post<BacktestResponse>(
        `/api/v1/specs/${id}/backtest`,
        { spec },
      );
      setBtResult(data.result);
    } catch (err) {
      // Never fabricate a result — surface the real backend state.
      if (err instanceof ApiError && (err.status === 404 || err.status === 501)) {
        setBtError('El backtest aún no está disponible en el backend.');
      } else {
        setBtError('No se pudo ejecutar el backtest. Intenta más tarde.');
      }
    } finally {
      setBtLoading(false);
    }
  }

  // ── Enviar al Gate ────────────────────────────────────────────────────────

  async function handleSendToGate() {
    setGateLoading(true);
    setGateError(null);

    try {
      // Map the editor inputs to a valid StrategySpec (the backend parses the raw
      // YAML body and runs Validate() — schema must match internal/strategy/spec.go).
      const entryType =
        spec.regime === 'RANGING'  ? 'mean_reversion' :
        spec.regime === 'BREAKOUT' ? 'breakout' :
        spec.regime === 'TRENDING' ? 'momentum' : 'pullback';

      // The backend requires take_profit_pct > stop_loss_pct.
      const yamlPayload = [
        `apiVersion: v1`,
        `kind: StrategySpec`,
        `metadata:`,
        `  name: ${spec.name}`,
        `  description: Generado en Spec Studio`,
        `scope:`,
        `  pairs: [BTC-USDT]`,
        `  exchange: kucoin`,
        `  market_type: spot`,
        `  timeframes: [5m]`,
        `entry:`,
        `  type: ${entryType}`,
        `  confidence_threshold: 0.8`,
        `exit:`,
        `  take_profit_pct: ${spec.tp.toFixed(2)}`,
        `  stop_loss_pct: ${spec.sl.toFixed(2)}`,
        `risk_limits:`,
        `  max_position_size_usd: 2.0`,
        `  leverage: 0`,
        `indicators:`,
        `  rsi_period: 14`,
        `  rsi_oversold: ${spec.rsiMin}`,
        `  rsi_overbought: ${spec.rsiMax}`,
      ].join('\n');

      // Send the raw YAML as the request body (NOT wrapped in JSON).
      const data = await apiFetch<CreateSpecResponse>('/api/v1/specs', {
        method:  'POST',
        body:    yamlPayload,
        headers: { 'Content-Type': 'application/x-yaml' },
      });

      const newId = data.spec_id ?? data.id;
      if (!newId) {
        setGateError('El backend no devolvió un id de spec. Intenta más tarde.');
        setGateLoading(false);
        return;
      }
      // C2.2: the content_hash is computed by the backend; surface it as-is.
      onSpecCreated(newId, data.content_hash);
      router.push('/gate');
    } catch (err) {
      if (err instanceof ApiError && (err.status === 404 || err.status === 501)) {
        setGateError('El envío al gate aún no está disponible en el backend.');
        setGateLoading(false);
      } else if (err instanceof ApiError && err.status === 400) {
        // Surface the backend validation message (e.g. tp must be > sl).
        const detail =
          (err.body as any)?.error?.details ??
          (err.body as any)?.error?.message ??
          'revisa los parámetros (el take-profit debe ser mayor que el stop-loss)';
        setGateError(`Spec inválida: ${detail}`);
        setGateLoading(false);
      } else if (err instanceof ApiError && err.status === 409) {
        // Duplicate spec name.
        setGateError('Ya existe una spec con ese nombre. Cambia el nombre o edita la existente.');
        setGateLoading(false);
      } else {
        setGateError('Error al enviar al gate. Intenta más tarde.');
        setGateLoading(false);
      }
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 0 }}>

      {/* Legal disclaimer */}
      <div className="disclaimer legal" style={{ borderRadius: 'var(--r) var(--r) 0 0', borderBottom: 'none' }}>
        <svg width="15" height="15" viewBox="0 0 14 14" fill="none" style={{ flex: 'none', marginTop: 1 }}>
          <path d="M7 2L2 12h10L7 2Z" stroke="currentColor" strokeWidth="1.2" strokeLinejoin="round" />
          <path d="M7 6v3M7 10.5v.5" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" />
        </svg>
        <span style={{ fontSize: 11 }}>
          <b>Skin in the game:</b> deberás correrla{' '}
          {skinInGameDays != null ? <b className="num">{skinInGameDays} días</b> : 'el período que defina la política'}{' '}
          en TU cuenta con dinero real antes de que sea copiable. El autor siempre pierde primero.
        </span>
      </div>

      {/* Backtest result strip */}
      {btResult && (
        <div style={{
          display:        'flex',
          alignItems:     'center',
          gap:            20,
          padding:        '9px 14px',
          background:     'var(--bg-inset)',
          borderLeft:     '1px solid var(--line)',
          borderRight:    '1px solid var(--line)',
          fontFamily:     'var(--mono)',
          fontSize:       11.5,
          flexWrap:       'wrap',
        }}>
          <span style={{ color: 'var(--tx-faint)' }}>PF estimado</span>
          <b style={{ color: pfColor(btResult.profit_factor), fontSize: 15 }}>
            {btResult.profit_factor.toFixed(2)}
          </b>
          <span style={{ color: 'var(--tx-faint)' }}>WR</span>
          <b style={{ color: btResult.win_rate >= 0.5 ? 'var(--up)' : 'var(--down)' }}>
            {(btResult.win_rate * 100).toFixed(0)}%
          </b>
          <span style={{ color: 'var(--tx-faint)' }}>trades</span>
          <b style={{ color: 'var(--tx)' }}>{btResult.total_trades}</b>
          <span className="faint" style={{ fontSize: 10, marginLeft: 'auto' }}>{btResult.sample}</span>
        </div>
      )}

      {/* Error strips */}
      {btError && (
        <div style={{
          padding: '7px 14px', fontSize: 11,
          background: 'var(--real-soft)', color: 'var(--down)',
          borderLeft: '1px solid var(--line)', borderRight: '1px solid var(--line)',
        }}>
          {btError}
        </div>
      )}
      {gateError && (
        <div style={{
          padding: '7px 14px', fontSize: 11,
          background: 'var(--real-soft)', color: 'var(--down)',
          borderLeft: '1px solid var(--line)', borderRight: '1px solid var(--line)',
        }}>
          {gateError}
        </div>
      )}

      {/* Action buttons */}
      <div style={{
        display:        'flex',
        gap:            8,
        padding:        '12px 14px',
        borderTop:      '1px solid var(--line)',
      }}>
        <button
          className="btn"
          style={{ flex: 1 }}
          onClick={handleBacktest}
          disabled={btLoading}
        >
          {btLoading ? (
            <span style={{
              width: 12, height: 12,
              border: '2px solid rgba(255,255,255,0.3)',
              borderTopColor: 'var(--tx)',
              borderRadius: '50%',
              display: 'inline-block',
              animation: 'spin 0.7s linear infinite',
            }} />
          ) : (
            /* Play icon */
            <svg width="13" height="13" viewBox="0 0 14 14" fill="none">
              <path d="M4 2.5l8 4.5-8 4.5V2.5Z" stroke="currentColor" strokeWidth="1.2" strokeLinejoin="round" />
            </svg>
          )}
          Backtest rápido
        </button>

        <button
          className="btn primary"
          style={{ flex: 1 }}
          onClick={handleSendToGate}
          disabled={gateLoading}
        >
          {gateLoading ? (
            <span style={{
              width: 12, height: 12,
              border: '2px solid rgba(255,255,255,0.3)',
              borderTopColor: '#fff',
              borderRadius: '50%',
              display: 'inline-block',
              animation: 'spin 0.7s linear infinite',
            }} />
          ) : (
            /* Gate icon */
            <svg width="13" height="13" viewBox="0 0 14 14" fill="none">
              <rect x="1.5" y="1.5" width="11" height="11" rx="1.5" stroke="currentColor" strokeWidth="1.2" />
              <path d="M4 7h6M7.5 4.5L10 7l-2.5 2.5" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
          )}
          Enviar al Gate
        </button>
      </div>

      <style>{`@keyframes spin { to { transform: rotate(360deg); } }`}</style>
    </div>
  );
}
