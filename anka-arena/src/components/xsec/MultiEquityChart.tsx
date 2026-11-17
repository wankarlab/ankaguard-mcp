'use client';

// MultiEquityChart — overlays N equity curves for comparison (Fase 3:
// backtest history comparison). Each series is plotted by its own
// FRACTIONAL position (index / (length-1)), not by aligned real dates —
// runs being compared can span different date ranges or rebalance counts,
// and normalizing by position keeps "shape of the curve" comparable without
// a more involved date-union alignment. Same inline-SVG approach as
// EquityChart (no charting library), one <path> per series.

import { useMemo, useState } from 'react';
import type { EquityChartPoint } from '@/components/xsec/EquityChart';

export interface MultiEquitySeries {
  label: string;
  color: string;
  points: EquityChartPoint[];
}

interface Props {
  series: MultiEquitySeries[];
}

const WIDTH = 640;
const HEIGHT = 180;
const PAD_TOP = 14;
const PAD_BOTTOM = 24;
const PAD_LEFT = 8;
const PAD_RIGHT = 8;

export function MultiEquityChart({ series }: Props) {
  const [hoverSeries, setHoverSeries] = useState<number | null>(null);

  const { paths, min, max } = useMemo(() => {
    const usable = series.filter((s) => s.points.length >= 2);
    const allValues = usable.flatMap((s) => s.points.map((p) => p.value));
    const min = Math.min(...allValues);
    const max = Math.max(...allValues);
    const range = max - min || Math.abs(max) || 1;
    const innerW = WIDTH - PAD_LEFT - PAD_RIGHT;
    const innerH = HEIGHT - PAD_TOP - PAD_BOTTOM;

    const paths = usable.map((s) => {
      const n = s.points.length;
      const coords = s.points.map((p, i) => ({
        x: PAD_LEFT + (i / (n - 1)) * innerW,
        y: PAD_TOP + innerH - ((p.value - min) / range) * innerH,
      }));
      const d = coords.map((c, i) => `${i === 0 ? 'M' : 'L'}${c.x.toFixed(2)},${c.y.toFixed(2)}`).join(' ');
      return { label: s.label, color: s.color, d };
    });
    return { paths, min, max };
  }, [series]);

  if (paths.length === 0) return null;

  return (
    <div>
      <svg viewBox={`0 0 ${WIDTH} ${HEIGHT}`} style={{ width: '100%', height: HEIGHT, display: 'block' }}>
        <line x1={PAD_LEFT} x2={WIDTH - PAD_RIGHT} y1={PAD_TOP} y2={PAD_TOP} stroke="var(--line)" strokeDasharray="2,3" />
        <line x1={PAD_LEFT} x2={WIDTH - PAD_RIGHT} y1={HEIGHT - PAD_BOTTOM} y2={HEIGHT - PAD_BOTTOM} stroke="var(--line)" strokeDasharray="2,3" />
        <text x={WIDTH - PAD_RIGHT} y={PAD_TOP - 4} textAnchor="end" fontSize="9" fill="var(--tx-faint)" style={{ fontFamily: 'var(--mono)' }}>
          ${max.toFixed(2)}
        </text>
        <text x={WIDTH - PAD_RIGHT} y={HEIGHT - PAD_BOTTOM + 12} textAnchor="end" fontSize="9" fill="var(--tx-faint)" style={{ fontFamily: 'var(--mono)' }}>
          ${min.toFixed(2)}
        </text>
        {paths.map((p, i) => (
          <path
            key={p.label}
            d={p.d}
            fill="none"
            stroke={p.color}
            strokeWidth={hoverSeries === null || hoverSeries === i ? 2 : 1}
            strokeOpacity={hoverSeries === null || hoverSeries === i ? 1 : 0.25}
            strokeLinejoin="round"
            strokeLinecap="round"
          />
        ))}
      </svg>
      <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', marginTop: 6 }}>
        {paths.map((p, i) => (
          <span
            key={p.label}
            className="mono"
            onMouseEnter={() => setHoverSeries(i)}
            onMouseLeave={() => setHoverSeries(null)}
            style={{ display: 'flex', alignItems: 'center', gap: 5, fontSize: 10, color: 'var(--tx-dim)', cursor: 'default' }}
          >
            <span style={{ width: 10, height: 2, background: p.color, display: 'inline-block' }} />
            {p.label}
          </span>
        ))}
      </div>
    </div>
  );
}
