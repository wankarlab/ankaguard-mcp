// Capa0Panel — read-only, no client state needed

import { useTranslations } from 'next-intl';

export function Capa0Panel() {
  const t = useTranslations('UiPolicies');
  const immutablePaths: string[] = [
    'internal/risk/',
    'internal/backtest/validator.go',
    'risk_limits (config)',
    t('gates_pipeline'),
  ];
  return (
    <div style={{ padding: 16 }}>
      {/* Disclaimer */}
      <div
        style={{
          display: 'flex',
          gap: 8,
          padding: '10px 12px',
          background: 'rgba(240,86,79,.06)',
          border: '1px solid rgba(240,86,79,.25)',
          borderRadius: 8,
          marginBottom: 14,
          fontSize: 11,
          color: 'var(--tx)',
        }}
      >
        <svg
          width="15"
          height="15"
          viewBox="0 0 24 24"
          fill="none"
          stroke="#4adb84"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          style={{ flexShrink: 0, marginTop: 1 }}
        >
          <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" />
        </svg>
        <span>
          {t('agent_cannot_touch')}
        </span>
      </div>

      {/* Immutable paths list */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 7 }}>
        {immutablePaths.map((path) => (
          <div
            key={path}
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 9,
              padding: '8px 11px',
              background: 'var(--bg-void)',
              border: '1px solid var(--line)',
              borderRadius: 8,
            }}
          >
            {/* Lock icon */}
            <svg
              width="14"
              height="14"
              viewBox="0 0 24 24"
              fill="none"
              stroke="#f0564f"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
              style={{ flexShrink: 0 }}
            >
              <rect x="3" y="11" width="18" height="11" rx="2" ry="2" />
              <path d="M7 11V7a5 5 0 0 1 10 0v4" />
            </svg>

            <span className="mono" style={{ fontSize: 11, color: 'var(--tx)', flex: 1 }}>
              {path}
            </span>

            {/* INMUTABLE badge */}
            <span
              className="badge"
              style={{
                marginLeft: 'auto',
                fontSize: 8.5,
                padding: '2px 6px',
                borderRadius: 4,
                background: 'rgba(240,86,79,.15)',
                color: '#f0564f',
                fontFamily: 'var(--mono)',
                fontWeight: 700,
                letterSpacing: '.05em',
                whiteSpace: 'nowrap',
              }}
            >
              {t('immutable')}
            </span>
          </div>
        ))}
      </div>

      <div
        className="mono"
        style={{ fontSize: 9.5, color: 'var(--tx-faint)', marginTop: 10 }}
      >
        ADR-0005 · .ai-immutable-paths
      </div>
    </div>
  );
}
