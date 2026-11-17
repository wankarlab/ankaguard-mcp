'use client';

import { useEffect, useState } from 'react';
import useSWR from 'swr';
import clsx from 'clsx';
import { useTranslations } from 'next-intl';

import { apiFetch }          from '@/lib/api';
import { toEngineStats }     from '@/lib/adapters';
import { useEngineStore, type EngineStats } from '@/stores/engine';
import { useEngineStream }   from '@/hooks/useEngineStream';

import { FlywheelDiagram }   from '@/components/flywheel/FlywheelDiagram';
import { NorthStar }         from '@/components/flywheel/NorthStar';
import { BitacoraFeed }      from '@/components/flywheel/BitacoraFeed';
import { ExchangeRail }      from '@/components/flywheel/ExchangeRail';

import { PanelHeader, PanelBody } from '@/components/ui/Panel';
import { Badge }             from '@/components/ui/Badge';

// ──────────────────────────────────────────────────────────────
//  SWR fetcher
// ──────────────────────────────────────────────────────────────

async function fetchEngineStats(): Promise<Partial<EngineStats>> {
  // Backend wraps in { success, data:{ snake_case } } — map to the store shape.
  return toEngineStats(await apiFetch<unknown>('/api/v1/engine/stats'));
}

// ──────────────────────────────────────────────────────────────
//  Organism stats grid (4 KPIs)
// ──────────────────────────────────────────────────────────────

// Same thresholds as the topbar Ticker: red <1.0, amber <1.3, green >=1.3.
function gbmPfColor(pf: number): string {
  return pf >= 1.3 ? 'var(--up)' : pf >= 1.0 ? 'var(--warn)' : 'var(--bad)';
}

function OrganismStats({ stats, loading }: { stats: EngineStats; loading: boolean }) {
  const t = useTranslations('Flywheel');
  const kpis: Array<[string, string, string]> = [
    [t('certified_specs'), loading ? '—' : String(stats.specsCertifiedToday),  'var(--up)'],
    [t('gate_pass_rate'),  loading ? '—' : `${stats.gatePassPct.toFixed(0)}%`, 'var(--micro)'],
    [t('live_edges'),            loading ? '—' : `${stats.edgesCertified} / ${stats.edgesTotal}`, 'var(--real)'],
    [t('gbm_pf'),      loading ? '—' : stats.gbmPf.toFixed(2),        gbmPfColor(stats.gbmPf)],
  ];

  return (
    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr' }}>
      {kpis.map(([label, value, color], i) => (
        <div
          key={label}
          style={{
            padding:     '13px 15px',
            borderRight: i % 2 === 0 ? '1px solid var(--line)' : 'none',
            borderTop:   i > 1 ? '1px solid var(--line)' : 'none',
            opacity:     loading ? 0.45 : 1,
            transition:  'opacity .3s',
          }}
        >
          <div
            style={{
              fontFamily:    'var(--mono)',
              fontSize:      9.5,
              letterSpacing: '.08em',
              textTransform: 'uppercase',
              color:         'var(--tx-faint)',
            }}
          >
            {label}
          </div>
          <div
            className="num"
            style={{ fontSize: 22, fontWeight: 600, color, marginTop: 4 }}
          >
            {value}
          </div>
        </div>
      ))}
    </div>
  );
}

// ──────────────────────────────────────────────────────────────
//  Capital ladder (simplified — tier rungs)
// ──────────────────────────────────────────────────────────────

const LADDER_TIERS = [
  { id: 'paper', label: 'PAPER',      descKey: 'ladder_paper_desc', color: 'var(--paper)' },
  { id: 'micro', label: 'MICRO <$50', descKey: 'ladder_micro_desc', color: 'var(--micro)' },
  { id: 'real',  label: 'REAL',       descKey: 'ladder_real_desc',  color: 'var(--real)'  },
];

function CapitalLadder({ current }: { current: 'paper' | 'micro' | 'real' }) {
  const t = useTranslations('UiFlywheel');
  const currentIdx = LADDER_TIERS.findIndex((tier) => tier.id === current);
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      {LADDER_TIERS.map((tier, i) => {
        const active  = tier.id === current;
        const reached = i <= currentIdx;
        return (
          <div
            key={tier.id}
            style={{
              display:      'flex',
              alignItems:   'center',
              gap:          12,
              padding:      '10px 13px',
              background:   active ? 'rgba(157,124,255,.08)' : 'var(--bg-inset)',
              border:       `1px solid ${active ? tier.color : 'var(--line)'}`,
              borderRadius: 8,
              opacity:      reached ? 1 : 0.45,
            }}
          >
            <span
              style={{
                width:        10,
                height:       10,
                borderRadius: '50%',
                background:   reached ? tier.color : 'var(--line-2)',
                boxShadow:    active ? `0 0 10px ${tier.color}` : 'none',
                flexShrink:   0,
              }}
            />
            <div style={{ flex: 1 }}>
              <div
                className="mono"
                style={{
                  fontSize:   11,
                  fontWeight: 600,
                  color:      reached ? tier.color : 'var(--tx-dim)',
                }}
              >
                {tier.label}
              </div>
              <div style={{ fontSize: 10, color: 'var(--tx-faint)', marginTop: 1 }}>
                {t(tier.descKey)}
              </div>
            </div>
            {active && (
              <span className="badge ok" style={{ fontSize: 10 }}>
                <span className="bd-dot" style={{ background: 'var(--up)' }} />
                {t('ladder_current')}
              </span>
            )}
          </div>
        );
      })}
      <p style={{ fontSize: 10.5, color: 'var(--tx-faint)', margin: '6px 0 0', lineHeight: 1.5 }}>
        {t('ladder_warning')}
      </p>
    </div>
  );
}

// ──────────────────────────────────────────────────────────────
//  Page
// ──────────────────────────────────────────────────────────────

export default function FlywheelPage() {
  const t = useTranslations('Flywheel');
  const tu = useTranslations('UiFlywheel');
  // SWR: poll every 30 s
  const { data: swrData, isLoading: swrLoading } = useSWR(
    '/api/v1/engine/stats',
    fetchEngineStats,
    { refreshInterval: 30_000 },
  );

  // Zustand engine store
  const { stats, loading: storeLoading, setStats } = useEngineStore();

  // Push SWR data into store when it arrives
  useEffect(() => {
    if (swrData) setStats(swrData);
  }, [swrData, setStats]);

  const loading = swrLoading && storeLoading;

  // WS events + connection status
  const { events, connected } = useEngineStream();

  // Gate pulse: fires every 9s, lasts 2.4s
  const [gatePulse, setGatePulse] = useState(false);
  useEffect(() => {
    const iv = setInterval(() => {
      setGatePulse(true);
      setTimeout(() => setGatePulse(false), 2400);
    }, 9_000);
    return () => clearInterval(iv);
  }, []);

  return (
    <div className={clsx('content-pad', 'enter')}>
      <div className="maxw">

        {/* ── North Star ── */}
        <NorthStar
          engineStats={stats}
          loading={loading}
          style={{ marginBottom: 18 }}
        />

        {/* ── Main grid: flywheel + side panel ── */}
        <div
          style={{
            display:             'grid',
            gridTemplateColumns: 'minmax(0,1.7fr) minmax(280px,1fr)',
            gap:                 16,
            alignItems:          'stretch',
          }}
        >
          {/* Flywheel SVG panel */}
          <div className="panel" style={{ overflow: 'hidden' }}>
            <PanelHeader
              title={t('organism_live')}
              sub={t('vision_5')}
            >
              <Badge
                variant={connected ? 'ok' : 'neutral'}
                dot
                dotColor={connected ? 'var(--up)' : 'var(--tx-faint)'}
              >
                {connected ? t('spinning') : t('offline')}
              </Badge>
            </PanelHeader>
            <div style={{ position: 'relative', minHeight: 420 }}>
              <FlywheelDiagram
                engineStats={stats}
                gatePulse={gatePulse}
              />
              <div
                style={{
                  position:       'absolute',
                  bottom:         12,
                  left:           16,
                  right:          16,
                  display:        'flex',
                  justifyContent: 'space-between',
                  flexWrap:       'wrap',
                  gap:            8,
                }}
              >
                <span className="dim mono" style={{ fontSize: 10 }}>
                  {tu('flow_1')}{' '}
                  <span style={{ color: 'var(--up)' }}>gate</span> {tu('flow_2')}
                </span>
                <span className="dim mono" style={{ fontSize: 10 }}>
                  {tu('pulse_1')} <span style={{ color: 'var(--up)' }}>gate</span> {tu('pulse_2')}{' '}
                  <span style={{ color: 'var(--real)' }}>{tu('pulse_red')}</span> {tu('pulse_3')}
                </span>
              </div>
            </div>
          </div>

          {/* Side column */}
          <div
            style={{
              display:       'flex',
              flexDirection: 'column',
              gap:           16,
              minWidth:      0,
            }}
          >
            {/* Organism KPIs */}
            <div className="panel" style={{ overflow: 'hidden' }}>
              <PanelHeader title={t('organism_state')} />
              <OrganismStats stats={stats} loading={loading} />
            </div>

            {/* Bitácora */}
            <div
              className="panel"
              style={{
                flex:          1,
                minHeight:     220,
                display:       'flex',
                flexDirection: 'column',
                overflow:      'hidden',
              }}
            >
              <PanelHeader title={t('logbook')} sub={t('append_only')} />
              <BitacoraFeed events={events} />
            </div>
          </div>
        </div>

        {/* ── Bottom grid: capital ladder + exchange rail ── */}
        <div
          style={{
            display:             'grid',
            gridTemplateColumns: 'minmax(0,1.7fr) minmax(280px,1fr)',
            gap:                 16,
            marginTop:           16,
          }}
        >
          <div className="panel">
            <PanelHeader
              title={t('capital_ladder')}
              sub={t('vision_12')}
            />
            <PanelBody>
              <CapitalLadder current={stats.tier} />
            </PanelBody>
          </div>

          <div className="panel">
            <PanelHeader title={t('multi_exchange')} sub={t('honest')} />
            <PanelBody>
              <ExchangeRail />
            </PanelBody>
          </div>
        </div>

      </div>
    </div>
  );
}
