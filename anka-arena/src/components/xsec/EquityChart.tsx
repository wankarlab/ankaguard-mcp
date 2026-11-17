'use client';

// EquityChart — lightweight inline-SVG area/line chart. No charting library:
// the dataset is small (≤168 hourly points from GET /xsec/status) and a
// hand-rolled SVG keeps the bundle small and the styling trivially consistent
// with the rest of the design system (CSS vars). Hover shows a crosshair +
// tooltip with the exact date/value at that point. Generic over any
// {ts, value} series — the caller decides what's real.

import { useMemo, useRef, useState } from 'react';

export interface EquityChartPoint {
  ts: string;
  value: number;
}

interface Props {
  points: EquityChartPoint[];
}

const WIDTH = 640;
const HEIGHT = 160;
const PAD_TOP = 14;
const PAD_BOTTOM = 20;
const PAD_LEFT = 8;
const PAD_RIGHT = 8;

function fmtShortDateTime(iso: string): string {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return '—';
  return d.toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
}

export function EquityChart({ points }: Props) {
  const svgRef = useRef<SVGSVGElement>(null);
  const [hoverIdx, setHoverIdx] = useState<number | null>(null);

  const { path, areaPath, min, max, coords, up } = useMemo(() => {
    const values = points.map((p) => p.value);
    const min = Math.min(...values);
    const max = Math.max(...values);
    const range = max - min || Math.abs(max) || 1;
    const innerW = WIDTH - PAD_LEFT - PAD_RIGHT;
    const innerH = HEIGHT - PAD_TOP - PAD_BOTTOM;
    const coords = points.map((p, i) => ({
      x: PAD_LEFT + (points.length === 1 ? innerW / 2 : (i / (points.length - 1)) * innerW),
      y: PAD_TOP + innerH - ((p.value - min) / range) * innerH,
      p,
    }));
    const path = coords.map((c, i) => `${i === 0 ? 'M' : 'L'}${c.x.toFixed(2)},${c.y.toFixed(2)}`).join(' ');
    const floorY = PAD_TOP + innerH;
    const areaPath = `${path} L${coords[coords.length - 1].x.toFixed(2)},${floorY} L${coords[0].x.toFixed(2)},${floorY} Z`;
    const up = points[points.length - 1].value >= points[0].value;
    return { path, areaPath, min, max, coords, up };
  }, [points]);

  const color = up ? 'var(--up)' : 'var(--down)';
  const gradientId = 'equity-chart-gradient';

  function handleMove(e: React.MouseEvent<SVGSVGElement>) {
    const svg = svgRef.current;
    if (!svg) return;
    const rect = svg.getBoundingClientRect();
    const relX = ((e.clientX - rect.left) / rect.width) * WIDTH;
    let nearest = 0;
    let best = Infinity;
    coords.forEach((c, i) => {
      const d = Math.abs(c.x - relX);
      if (d < best) { best = d; nearest = i; }
    });
    setHoverIdx(nearest);
  }

  const hovered = hoverIdx != null ? coords[hoverIdx] : null;
  const tooltipLeftPct = hovered ? Math.min(Math.max((hovered.x / WIDTH) * 100, 10), 90) : 0;

  return (
    <div style={{ position: 'relative' }}>
      <svg
        ref={svgRef}
        viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
        style={{ width: '100%', height: HEIGHT, display: 'block', cursor: 'crosshair' }}
        onMouseMove={handleMove}
        onMouseLeave={() => setHoverIdx(null)}
      >
        <defs>
          <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={color} stopOpacity="0.28" />
            <stop offset="100%" stopColor={color} stopOpacity="0" />
          </linearGradient>
        </defs>

        <line x1={PAD_LEFT} x2={WIDTH - PAD_RIGHT} y1={PAD_TOP} y2={PAD_TOP} stroke="var(--line)" strokeDasharray="2,3" />
        <line x1={PAD_LEFT} x2={WIDTH - PAD_RIGHT} y1={HEIGHT - PAD_BOTTOM} y2={HEIGHT - PAD_BOTTOM} stroke="var(--line)" strokeDasharray="2,3" />
        <text x={WIDTH - PAD_RIGHT} y={PAD_TOP - 4} textAnchor="end" fontSize="9" fill="var(--tx-faint)" style={{ fontFamily: 'var(--mono)' }}>
          ${max.toFixed(2)}
        </text>
        <text x={WIDTH - PAD_RIGHT} y={HEIGHT - PAD_BOTTOM + 12} textAnchor="end" fontSize="9" fill="var(--tx-faint)" style={{ fontFamily: 'var(--mono)' }}>
          ${min.toFixed(2)}
        </text>

        <path d={areaPath} fill={`url(#${gradientId})`} />
        <path d={path} fill="none" stroke={color} strokeWidth="2" strokeLinejoin="round" strokeLinecap="round" />

        {hovered && (
          <>
            <line x1={hovered.x} x2={hovered.x} y1={PAD_TOP} y2={HEIGHT - PAD_BOTTOM} stroke="var(--tx-faint)" strokeWidth="1" />
            <circle cx={hovered.x} cy={hovered.y} r="4" fill={color} stroke="var(--bg-void)" strokeWidth="1.5" />
          </>
        )}
      </svg>

      <div className="mono" style={{ display: 'flex', justifyContent: 'space-between', fontSize: 10, color: 'var(--tx-faint)', marginTop: 2 }}>
        <span>{fmtShortDateTime(points[0].ts)}</span>
        <span>{fmtShortDateTime(points[points.length - 1].ts)}</span>
      </div>

      {hovered && (
        <div
          className="mono"
          style={{
            position: 'absolute', top: 4, left: `${tooltipLeftPct}%`, transform: 'translateX(-50%)',
            fontSize: 11, padding: '4px 8px', borderRadius: 4, whiteSpace: 'nowrap', pointerEvents: 'none',
            background: 'var(--bg-panel-2)', border: '1px solid var(--line)', color: 'var(--tx-hi)',
          }}
        >
          {fmtShortDateTime(hovered.p.ts)} · ${hovered.p.value.toFixed(2)}
        </div>
      )}
    </div>
  );
}
