// MetricsGrid.tsx — Grid 2×2 con métricas de validación walk-forward OOS

import { useTranslations } from 'next-intl';
import { Meter } from '@/components/ui/Meter';

export interface SpecMetrics {
  pf: number;
  cons: number;      // 0–100 (%)
  robust: number;    // 0–1
  greenWindows: number;
  totalWindows: number;
}

interface MetricsGridProps {
  metrics: SpecMetrics;
}

interface MetricCellProps {
  label: string;
  value: string;
  pass: boolean;
  note: string;
  meterValue: number;
  meterMax: number;
  extra?: React.ReactNode;
}

function MetricCell({
  label,
  value,
  pass,
  note,
  meterValue,
  meterMax,
  extra,
}: MetricCellProps) {
  const color = pass ? 'var(--up)' : 'var(--real)';
  // normalize 0–100 for the Meter component
  const meterPct = Math.min(100, (meterValue / meterMax) * 100);

  return (
    <div className="panel-inset" style={{ padding: '12px 14px' }}>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          marginBottom: 4,
        }}
      >
        <span className="eyebrow" style={{ fontSize: 9.5 }}>
          {label}
        </span>
        <span style={{ color, fontSize: 11 }}>{pass ? '✓' : '✕'}</span>
      </div>
      <div
        className="num"
        style={{
          fontSize: 24,
          fontWeight: 600,
          color: pass ? 'var(--tx-hi)' : 'var(--real)',
          margin: '4px 0 8px',
        }}
      >
        {value}
      </div>
      <Meter value={meterPct} color={color} aria-label={label} />
      {extra}
      <div className="mono faint" style={{ fontSize: 9.5, marginTop: 5 }}>
        {note}
      </div>
    </div>
  );
}

export function MetricsGrid({ metrics }: MetricsGridProps) {
  const t = useTranslations('UiGate');
  const { pf, cons, robust, greenWindows, totalWindows } = metrics;

  return (
    <div
      style={{
        display: 'grid',
        gridTemplateColumns: '1fr 1fr',
        gap: 12,
      }}
    >
      {/* PF OOS */}
      <MetricCell
        label="PF OOS"
        value={pf.toFixed(2)}
        pass={pf >= 1.3}
        note={t('threshold_pf')}
        meterValue={pf}
        meterMax={3.2}
      />

      {/* Consistencia */}
      <MetricCell
        label={t('consistency')}
        value={cons.toFixed(0) + '%'}
        pass={cons >= 60}
        note={t('threshold_cons')}
        meterValue={cons}
        meterMax={100}
      />

      {/* Robustness */}
      <div className="panel-inset" style={{ padding: '12px 14px' }}>
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            marginBottom: 4,
          }}
        >
          <span className="eyebrow" style={{ fontSize: 9.5 }}>
            Robustness score
          </span>
          <a style={{ fontSize: 10 }}>{t('weights_v1')}</a>
        </div>
        <div
          className="num"
          style={{
            fontSize: 24,
            fontWeight: 600,
            color: 'var(--tx-hi)',
            margin: '4px 0 8px',
          }}
        >
          {robust.toFixed(2)}
        </div>
        <Meter
          value={Math.min(100, robust * 100)}
          color="var(--brand)"
          aria-label="Robustness score"
        />
        <div className="mono faint" style={{ fontSize: 9.5, marginTop: 5 }}>
          {t('governed_in_db')}
        </div>
      </div>

      {/* Cost model */}
      <div className="panel-inset" style={{ padding: '12px 14px' }}>
        <div className="eyebrow" style={{ fontSize: 9.5, marginBottom: 8 }}>
          Cost model
        </div>
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 7,
            marginBottom: 8,
          }}
        >
          <span style={{ color: 'var(--up)' }}>✓</span>
          <span className="hi" style={{ fontSize: 12 }}>
            {t('real_fees_applied')}
          </span>
        </div>
        <div className="mono faint" style={{ fontSize: 9.5 }}>
          {t('green_windows', { green: greenWindows, total: totalWindows })}
        </div>
      </div>
    </div>
  );
}
