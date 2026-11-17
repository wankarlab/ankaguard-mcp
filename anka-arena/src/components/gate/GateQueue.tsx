// GateQueue.tsx — Lista de specs en cola del gate
// Clic selecciona la spec. Muestra id, author, regime chip, resultado.

import { useTranslations } from 'next-intl';
import type { GateSpec } from './VerdictCard';

const REGIME_COLOR: Record<string, string> = {
  TRENDING: 'var(--up)',
  RANGING:  'var(--paper)',
  BREAKOUT: 'var(--micro)',
  UNKNOWN:  'var(--tx-faint)',
};

interface GateQueueProps {
  specs: GateSpec[];
  selectedId: string | null;
  onSelect: (spec: GateSpec) => void;
}

export function GateQueue({ specs, selectedId, onSelect }: GateQueueProps) {
  const t = useTranslations('UiGate');
  return (
    <div className="panel" style={{ overflow: 'hidden' }}>
      <div className="panel-h">
        <h3 style={{ flex: 1 }}>{t('queue_title')}</h3>
        <span className="mono faint" style={{ fontSize: 9.5 }}>
          {specs.length}
        </span>
      </div>

      {specs.length === 0 && (
        <div
          style={{
            padding: '24px 16px',
            textAlign: 'center',
            color: 'var(--tx-faint)',
            fontSize: 11.5,
          }}
        >
          {t('queue_empty')}
        </div>
      )}

      {specs.map((s) => {
        const isSelected = s.id === selectedId;
        return (
          <div
            key={s.id}
            onClick={() => onSelect(s)}
            style={{
              padding: '11px 14px',
              borderBottom: '1px solid var(--line)',
              cursor: 'pointer',
              background: isSelected ? 'var(--brand-soft)' : 'transparent',
              borderLeft: isSelected
                ? '2px solid var(--brand)'
                : '2px solid transparent',
              transition: 'background 0.12s',
            }}
          >
            {/* Spec ID */}
            <div
              className="mono hi"
              style={{
                fontSize: 11,
                fontWeight: 600,
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                whiteSpace: 'nowrap',
              }}
              title={s.id}
            >
              {s.id}
            </div>

            {/* Author chip */}
            <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginTop: 5 }}>
              <span
                className="chip"
                style={{ padding: '1px 6px', fontSize: 9.5 }}
              >
                {s.kind === 'IA' ? '⊛' : '⊙'} {s.author}
              </span>
            </div>

            {/* Regime + verdict */}
            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                marginTop: 6,
              }}
            >
              <span
                className="mono"
                style={{
                  fontSize: 9.5,
                  color: REGIME_COLOR[s.regime] ?? 'var(--tx-faint)',
                }}
              >
                {s.regime}
              </span>

              {s.verdict === 'certified' && (
                <span style={{ color: 'var(--up)', fontSize: 11 }}>✓</span>
              )}
              {s.verdict === 'rejected' && (
                <span style={{ color: 'var(--real)', fontSize: 11 }}>✕</span>
              )}
              {s.verdict === 'queued' && (
                <span style={{ color: 'var(--tx-faint)', fontSize: 11 }}>···</span>
              )}
              {s.verdict === 'validating' && (
                <span style={{ color: 'var(--micro)', fontSize: 11 }}>⟳</span>
              )}
            </div>
          </div>
        );
      })}
    </div>
  );
}
