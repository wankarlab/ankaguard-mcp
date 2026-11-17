'use client';

import React, { useState } from 'react';
import type { SpecState } from '@/lib/spec-studio';
import { api, ApiError } from '@/lib/api';

// ── Types ────────────────────────────────────────────────────────────────────

interface CopilotSuggestion {
  text:     string;
  mutation: Partial<SpecState>;
  source:   string;
}

interface CopilotResponse {
  suggestion: CopilotSuggestion;
}

// ── Diff renderer — highlights changed fields in the suggestion text ──────────

function DiffText({ text }: { text: string }) {
  // Bold sequences like **word** are highlighted as diffs
  const parts = text.split(/(\*\*[^*]+\*\*)/g);
  return (
    <span>
      {parts.map((p, i) => {
        if (p.startsWith('**') && p.endsWith('**')) {
          return (
            <b key={i} style={{ color: 'var(--tx-hi)', background: 'rgba(157,124,255,0.15)', borderRadius: 3, padding: '0 3px' }}>
              {p.slice(2, -2)}
            </b>
          );
        }
        return <span key={i}>{p}</span>;
      })}
    </span>
  );
}

// ── CopilotPanel ─────────────────────────────────────────────────────────────

interface CopilotPanelProps {
  spec:    SpecState;
  specId:  string | null;
  onApply: (mutation: Partial<SpecState>) => void;
}

export function CopilotPanel({ spec, specId, onApply }: CopilotPanelProps) {
  const [loading,    setLoading]    = useState(false);
  const [suggestion, setSuggestion] = useState<CopilotSuggestion | null>(null);
  const [error,      setError]      = useState<string | null>(null);
  const [applied,    setApplied]    = useState(false);

  async function handleAnalyze() {
    setLoading(true);
    setError(null);
    setApplied(false);

    try {
      const id = specId ?? 'draft';
      // Pass the spec as a query param body via POST body; use GET with spec payload
      const data = await api.get<CopilotResponse>(
        `/api/v1/specs/${id}/copilot?spec=${encodeURIComponent(JSON.stringify(spec))}`,
      );
      setSuggestion(data.suggestion);
    } catch (err) {
      if (err instanceof ApiError) {
        // Never fabricate a suggestion — surface the real backend state.
        if (err.status === 404 || err.status === 501) {
          setError('El copiloto aún no está disponible en el backend.');
        } else {
          setError(`Error ${err.status}: no se pudo obtener sugerencia del copiloto.`);
        }
      } else {
        setError('Error de red al contactar el copiloto.');
      }
    } finally {
      setLoading(false);
    }
  }

  function handleApply() {
    if (!suggestion || applied) return;
    onApply(suggestion.mutation);
    setApplied(true);
  }

  return (
    <div style={{ padding: 14, display: 'flex', flexDirection: 'column', gap: 12 }}>

      {/* Analyze button */}
      <button
        className={`btn ${loading ? '' : 'primary'} sm`}
        style={{ width: '100%', gap: 7 }}
        onClick={handleAnalyze}
        disabled={loading}
      >
        {loading ? (
          <>
            <span style={{
              width: 12, height: 12, border: '2px solid rgba(255,255,255,0.3)',
              borderTopColor: '#fff', borderRadius: '50%',
              display: 'inline-block', animation: 'spin 0.7s linear infinite',
            }} />
            Analizando…
          </>
        ) : (
          <>
            {/* Spark icon */}
            <svg width="13" height="13" viewBox="0 0 14 14" fill="none">
              <path d="M7 1L8.5 5.5H13L9.5 8.5L11 13L7 10.3L3 13L4.5 8.5L1 5.5H5.5L7 1Z"
                stroke="currentColor" strokeWidth="1.2" strokeLinejoin="round" />
            </svg>
            Analizar con IA
          </>
        )}
      </button>

      {/* Error state */}
      {error && (
        <div style={{
          background: 'var(--real-soft)',
          border:     '1px solid rgba(240,86,79,0.3)',
          borderRadius: 'var(--r)',
          padding:    '9px 12px',
          fontSize:   11.5,
          color:      'var(--down)',
        }}>
          {error}
        </div>
      )}

      {/* Suggestion card */}
      {suggestion && (
        <div className="panel-inset" style={{ padding: 12, borderColor: 'rgba(157,124,255,.3)' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 7, marginBottom: 8 }}>
            <svg width="14" height="14" viewBox="0 0 14 14" fill="none">
              <path d="M7 1L8.5 5.5H13L9.5 8.5L11 13L7 10.3L3 13L4.5 8.5L1 5.5H5.5L7 1Z"
                stroke="var(--brand)" strokeWidth="1.2" strokeLinejoin="round" />
            </svg>
            <span className="eyebrow" style={{ color: 'var(--brand)' }}>
              sugerencia · evidencia de drift
            </span>
          </div>

          <div style={{ fontSize: 12, lineHeight: 1.6, color: 'var(--tx)' }}>
            <DiffText text={suggestion.text} />
          </div>

          <div className="mono faint" style={{ fontSize: 9.5, margin: '8px 0' }}>
            fuente: {suggestion.source}
          </div>

          <button
            className={`btn sm ${applied ? '' : 'primary'}`}
            style={{ width: '100%' }}
            onClick={handleApply}
            disabled={applied}
          >
            {applied ? '✓ aplicada' : 'Aplicar mutación'}
          </button>
        </div>
      )}

      {/* Disclaimer */}
      <div className="disclaimer" style={{
        background:   'var(--brand-soft)',
        borderColor:  'rgba(157,124,255,.25)',
        color:        'var(--tx-dim)',
      }}>
        <svg width="14" height="14" viewBox="0 0 14 14" fill="none" style={{ flex: 'none', color: 'var(--brand)', marginTop: 1 }}>
          <path d="M7 2L2 12h10L7 2Z" stroke="currentColor" strokeWidth="1.2" strokeLinejoin="round" />
          <path d="M7 6v3M7 10.5v.5" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" />
        </svg>
        <span style={{ fontSize: 11 }}>
          El copiloto propone, tú envías al gate.{' '}
          <b style={{ color: 'var(--tx)' }}>La IA jamás califica su propia Spec</b>{' '}
          (principio #1).
        </span>
      </div>

      {/* CSS for spinner animation */}
      <style>{`@keyframes spin { to { transform: rotate(360deg); } }`}</style>
    </div>
  );
}
