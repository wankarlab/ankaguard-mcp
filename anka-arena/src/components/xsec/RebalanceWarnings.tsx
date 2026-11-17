'use client';

// RebalanceWarnings — highlighted banner for the two critical partial-result
// markers a rebalance job's output can carry (parsed by parseJobWarnings):
// missing legs (min contract size > per-leg target) and the net-exposure
// alarm (basket not market-neutral). Shared by JobMonitor (full banner over
// the live log) and JobHistory (compact banner inside the expanded row) so
// the parsing and the action hints never drift between the two.

import { useTranslations } from 'next-intl';
import { parseJobWarnings } from '@/lib/xsec-api';

interface Props {
  mode: string;
  output: string;
  compact?: boolean;
}

export function RebalanceWarnings({ mode, output, compact = false }: Props) {
  const t = useTranslations('Xsec');

  if (mode !== 'rebalance-live' && mode !== 'rebalance-dry' && mode !== 'relaunch') return null;
  const { missingLegs, netExposureWarning } = parseJobWarnings(output);
  if (missingLegs === 0 && !netExposureWarning) return null;

  const rowStyle: React.CSSProperties = {
    display: 'flex', flexDirection: 'column', gap: 2,
    fontSize: compact ? 11 : 12,
  };

  return (
    <div
      style={{
        display: 'flex', flexDirection: 'column', gap: compact ? 6 : 8,
        padding: compact ? '8px 10px' : '10px 12px', borderRadius: 'var(--r)',
        border: '1px solid var(--warn)', background: 'rgba(233,162,62,.07)',
      }}
    >
      {missingLegs > 0 && (
        <div style={rowStyle}>
          <span style={{ color: 'var(--warn)', fontWeight: 600 }}>
            🚫 {t('warn_missing_legs', { count: missingLegs })}
          </span>
          <span style={{ color: 'var(--tx-dim)' }}>{t('warn_missing_legs_hint')}</span>
        </div>
      )}
      {netExposureWarning && (
        <div style={rowStyle}>
          <span style={{ color: 'var(--down)', fontWeight: 600 }}>
            🚨 {t('warn_net_exposure')}
          </span>
          <span style={{ color: 'var(--tx-dim)' }}>{t('warn_net_exposure_hint')}</span>
        </div>
      )}
    </div>
  );
}
