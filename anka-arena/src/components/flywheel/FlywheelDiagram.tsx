'use client';

import { useMemo } from 'react';
import { useTranslations } from 'next-intl';
// next-intl router preserves the active locale prefix (as-needed) when navigating
// to a subsystem route. Using next/navigation here would drop the /en|/es segment.
import { useRouter } from '@/i18n/routing';
import clsx from 'clsx';
import type { EngineStats } from '@/stores/engine';

// ──────────────────────────────────────────────────────────────
//  Geometry
// ──────────────────────────────────────────────────────────────

const FW_CX = 450;
const FW_CY = 300;
const FW_RX = 322;
const FW_RY = 196;

function fwPoint(angleDeg: number): [number, number] {
  const a = (angleDeg * Math.PI) / 180;
  return [FW_CX + FW_RX * Math.cos(a), FW_CY + FW_RY * Math.sin(a)];
}

// Closed ellipse path used both for rendering and for animateMotion mpath
const FW_RING =
  `M ${FW_CX} ${FW_CY - FW_RY}` +
  ` A ${FW_RX} ${FW_RY} 0 1 1 ${FW_CX} ${FW_CY + FW_RY}` +
  ` A ${FW_RX} ${FW_RY} 0 1 1 ${FW_CX} ${FW_CY - FW_RY}`;

// ──────────────────────────────────────────────────────────────
//  Node definitions
// ──────────────────────────────────────────────────────────────

type Translator = ReturnType<typeof useTranslations>;

interface NodeDef {
  id:     string;
  angle:  number;
  labelKey: string;       // i18n key in the UiFlywheel namespace
  color:  string;         // CSS var reference
  route:  string;         // next/navigation target
  valve?: boolean;        // gate node — animated pulse
  metricKey: keyof EngineStats | null;
  metricFmt: (stats: EngineStats, t: Translator) => string;
}

const FW_NODES: NodeDef[] = [
  {
    id: 'edu',    angle: -90,
    labelKey: 'node_education',
    color: 'var(--micro)',
    route: '/academia',
    metricKey: null,
    metricFmt: (_s, t) => t('metric_students'),
  },
  {
    id: 'pers',   angle: -38.57,
    labelKey: 'node_people',
    color: 'var(--paper)',
    route: '/spec-studio',
    metricKey: null,
    metricFmt: (_s, t) => t('metric_authors'),
  },
  {
    id: 'gen',    angle: 12.86,
    labelKey: 'node_generation',
    color: 'var(--brand)',
    route: '/spec-studio',
    metricKey: 'specsInQueue',
    metricFmt: (s, t) => t('metric_specs_queued', { count: s.specsInQueue }),
  },
  {
    id: 'gate',   angle: 64.29,
    labelKey: 'node_gate',
    color: 'var(--up)',
    route: '/gate',
    valve: true,
    metricKey: 'gatePassPct',
    metricFmt: (s, t) => t('metric_pass_rate', { pct: s.gatePassPct.toFixed(0) }),
  },
  {
    id: 'shadow', angle: 115.71,
    labelKey: 'node_shadow',
    color: 'var(--paper)',
    route: '/gate',
    metricKey: 'activeBots',
    metricFmt: (s, t) => t('metric_paper_live', { count: s.activeBots }),
  },
  {
    id: 'real',   angle: 167.14,
    labelKey: 'node_real',
    color: 'var(--real)',
    route: '/gremio',
    metricKey: 'edgesCertified',
    metricFmt: (s, t) => t('metric_live_edges', { certified: s.edgesCertified, total: s.edgesTotal }),
  },
  {
    id: 'cap',    angle: 218.57,
    labelKey: 'node_training',
    color: 'var(--micro)',
    route: '/gremio',
    metricKey: 'aumCertUsd',
    metricFmt: (s, t) =>
      t('metric_aum_cert', { amount: (s.aumCertUsd / 1000).toFixed(1) }),
  },
];

// SVG icon paths (minimal, inline — avoids external icon dependency)
const ICON_PATHS: Record<string, string> = {
  academy:     'M10 2L2 7l8 5 8-5-8-5zM2 17l8 5 8-5M2 12l8 5 8-5',
  brain:       'M9.5 2A2.5 2.5 0 0 0 7 4.5v.5H5a3 3 0 0 0-3 3v3a3 3 0 0 0 3 3h.5v.5A2.5 2.5 0 0 0 8 17h8a2.5 2.5 0 0 0 2.5-2.5v-10A2.5 2.5 0 0 0 16 2H9.5z',
  spec:        'M9 3H5a2 2 0 0 0-2 2v4m6-6h10a2 2 0 0 1 2 2v4M9 3v18m0 0h10a2 2 0 0 0 2-2V9M9 21H5a2 2 0 0 1-2-2V9m0 0h18',
  gate:        'M9 12l2 2 4-4m6 2a9 9 0 1 1-18 0 9 9 0 0 1 18 0z',
  ghost:       'M9 3a6 6 0 0 1 12 0v13l-3-2-3 2-3-2-3 2V3z',
  bolt:        'M13 2L3 14h9l-1 8 10-12h-9l1-8z',
  fee:         'M12 8c-1.657 0-3 .895-3 2s1.343 2 3 2 3 .895 3 2-1.343 2-3 2m0-8c1.11 0 2.08.402 2.599 1M12 8V7m0 1v8m0 0v1m0-1c-1.11 0-2.08-.402-2.599-1M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0z',
  data:        'M4 7v10c0 2 1 3 3 3h10c2 0 3-1 3-3V7M4 7c0-2 1-3 3-3h10c2 0 3 1 3 3M4 7h16M9 11h6M9 15h6',
};

function NodeIcon({ name, x, y, color }: { name: string; x: number; y: number; color: string }) {
  const d = ICON_PATHS[name] ?? ICON_PATHS.data;
  return (
    <g transform={`translate(${x - 9},${y - 9})`}>
      <svg
        width={18}
        height={18}
        viewBox="0 0 24 24"
        fill="none"
        stroke={color}
        strokeWidth={1.8}
        strokeLinecap="round"
        strokeLinejoin="round"
      >
        <path d={d} />
      </svg>
    </g>
  );
}

// Icon lookup per node id
const NODE_ICONS: Record<string, string> = {
  edu: 'academy', pers: 'brain', gen: 'spec',
  gate: 'gate', shadow: 'ghost', real: 'bolt', cap: 'fee',
};

// ──────────────────────────────────────────────────────────────
//  Props
// ──────────────────────────────────────────────────────────────

export interface FlywheelDiagramProps {
  engineStats: EngineStats;
  gatePulse:   boolean;
  className?:  string;
}

// ──────────────────────────────────────────────────────────────
//  Component
// ──────────────────────────────────────────────────────────────

export function FlywheelDiagram({ engineStats, gatePulse, className }: FlywheelDiagramProps) {
  const router = useRouter();
  const t = useTranslations('UiFlywheel');

  const particles = useMemo(
    () =>
      Array.from({ length: 9 }, (_, i) => ({
        begin:  parseFloat((-(i * (13 / 9))).toFixed(2)),
        r:      i % 3 === 0 ? 3.4 : 2.4,
        color:
          i % 3 === 0
            ? 'var(--brand)'
            : i % 3 === 1
            ? 'var(--cyan)'
            : 'var(--paper)',
      })),
    [],
  );

  // Real value from the backend; '—' while no data has arrived.
  const featureRows = engineStats.featureStoreRows > 0
    ? engineStats.featureStoreRows.toLocaleString()
    : '—';

  return (
    <svg
      viewBox="0 0 900 600"
      className={clsx('fw-diagram', className)}
      style={{ width: '100%', height: '100%', display: 'block' }}
      aria-label={t('diagram_aria')}
    >
      <defs>
        <radialGradient id="fw-core-g" cx="50%" cy="50%" r="50%">
          <stop offset="0"   stopColor="#1a2030" />
          <stop offset="0.7" stopColor="#10141c" />
          <stop offset="1"   stopColor="#0c0f15" />
        </radialGradient>
        <filter id="fw-soft">
          <feGaussianBlur stdDeviation="3" />
        </filter>
      </defs>

      {/* ── Spokes (center ↔ node) with moving dot ── */}
      {FW_NODES.map((n) => {
        const [x, y] = fwPoint(n.angle);
        const inbound = ['real', 'gate', 'shadow'].includes(n.id);
        const dotPath = inbound
          ? `M${x} ${y} L${FW_CX} ${FW_CY}`
          : `M${FW_CX} ${FW_CY} L${x} ${y}`;
        return (
          <g key={`sp-${n.id}`}>
            <line
              x1={x} y1={y} x2={FW_CX} y2={FW_CY}
              stroke="var(--line-2)"
              strokeWidth={1}
              strokeDasharray="2 5"
              opacity={0.55}
            />
            <circle r={2.2} fill={inbound ? 'var(--up)' : 'var(--brand)'} opacity={0.85}>
              <animateMotion
                dur="3.4s"
                repeatCount="indefinite"
                path={dotPath}
                begin={`${parseFloat((n.angle % 3).toFixed(2))}s`}
              />
            </circle>
          </g>
        );
      })}

      {/* ── Ring ── */}
      <path d={FW_RING} fill="none" stroke="var(--line-2)" strokeWidth={1.4} />
      <path d={FW_RING} fill="none" stroke="var(--brand)"  strokeWidth={1.4} opacity={0.22} />

      {/* ── Orbiting particles ── */}
      {particles.map((p, i) => (
        <circle key={i} r={p.r} fill={p.color}>
          <animateMotion dur="13s" repeatCount="indefinite" begin={`${p.begin}s`} rotate="auto">
            <mpath href="#fw-ring-path" />
          </animateMotion>
        </circle>
      ))}
      {/* Hidden path for mpath reference */}
      <path id="fw-ring-path" d={FW_RING} fill="none" stroke="none" />

      {/* ── Core: feature_store ── */}
      <g>
        <circle
          cx={FW_CX} cy={FW_CY} r={74}
          fill="url(#fw-core-g)"
          stroke="var(--line-2)"
          strokeWidth={1.2}
        />
        {/* Breathing ring */}
        <circle cx={FW_CX} cy={FW_CY} r={74} fill="none" stroke="var(--brand)" strokeWidth={1} opacity={0.3}>
          <animate attributeName="r"       values="74;80;74" dur="4s" repeatCount="indefinite" />
          <animate attributeName="opacity" values=".3;.05;.3" dur="4s" repeatCount="indefinite" />
        </circle>
        {/* Data icon */}
        <NodeIcon name="data" x={FW_CX} y={FW_CY - 22} color="var(--brand)" />
        {/* Metric */}
        <text
          x={FW_CX} y={FW_CY + 8}
          textAnchor="middle"
          style={{ fontFamily: 'var(--mono)', fontSize: 18, fontWeight: 600, fill: 'var(--tx-hi)' }}
        >
          {featureRows}
        </text>
        <text
          x={FW_CX} y={FW_CY + 24}
          textAnchor="middle"
          style={{ fontFamily: 'var(--mono)', fontSize: 9.5, letterSpacing: '.12em', fill: 'var(--tx-faint)' }}
        >
          FEATURE_STORE
        </text>
        <text
          x={FW_CX} y={FW_CY + 40}
          textAnchor="middle"
          style={{ fontFamily: 'var(--mono)', fontSize: 9, fill: 'var(--tx-faint)' }}
        >
          {t('living_memory')}
        </text>
      </g>

      {/* ── Nodes ── */}
      {FW_NODES.map((n) => {
        const [x, y] = fwPoint(n.angle);
        const anchor = x < FW_CX - 30 ? 'end' : x > FW_CX + 30 ? 'start' : 'middle';
        const lx = anchor === 'end' ? x - 44 : anchor === 'start' ? x + 44 : x;
        const ly = y < FW_CY ? y - 50 : y + 52;
        const isGate = !!n.valve;
        const metric = n.metricFmt(engineStats, t);
        const label = t(n.labelKey);

        return (
          <g
            key={n.id}
            style={{ cursor: 'pointer' }}
            role="button"
            tabIndex={0}
            aria-label={t('navigate_to', { label })}
            onClick={() => router.push(n.route)}
            onKeyDown={(e) => e.key === 'Enter' && router.push(n.route)}
          >
            {/* Gate pulse ring */}
            {isGate && (
              <circle cx={x} cy={y} r={30}
                fill="none"
                stroke={gatePulse ? 'var(--real)' : 'var(--up)'}
                strokeWidth={2}
              >
                <animate attributeName="r"       values="30;46;30" dur={gatePulse ? '0.9s' : '2.6s'} repeatCount="indefinite" />
                <animate attributeName="opacity" values=".7;0;.7"  dur={gatePulse ? '0.9s' : '2.6s'} repeatCount="indefinite" />
              </circle>
            )}
            {/* Node circle */}
            <circle
              cx={x} cy={y} r={30}
              fill="var(--bg-panel-2)"
              stroke={n.color}
              strokeWidth={1.6}
              style={{ filter: 'drop-shadow(0 4px 12px rgba(0,0,0,.5))' }}
            />
            {/* Icon */}
            <NodeIcon name={NODE_ICONS[n.id] ?? 'data'} x={x} y={y} color={n.color} />
            {/* Label */}
            <text
              x={lx} y={ly}
              textAnchor={anchor}
              style={{ fontFamily: 'var(--mono)', fontSize: 11, fontWeight: 600, letterSpacing: '.04em', fill: 'var(--tx-hi)' }}
            >
              {label}
            </text>
            {/* Metric */}
            <text
              x={lx} y={ly + 14}
              textAnchor={anchor}
              style={{ fontFamily: 'var(--mono)', fontSize: 10, fill: n.color }}
            >
              {metric}
            </text>
          </g>
        );
      })}
    </svg>
  );
}
