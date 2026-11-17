'use client';

import type React from 'react';
import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import clsx from 'clsx';

import { apiFetch } from '@/lib/api';
import { unwrap } from '@/lib/adapters';
import type { EngineStats } from '@/stores/engine';

interface NorthStarProps {
  engineStats: EngineStats;
  loading?:    boolean;
  className?:  string;
  style?:      React.CSSProperties;
}

// ── Cohort metrics (GET /api/v1/academy/cohorts/current) ─────────────────────
//
// The North Star decomposition (educadores × alumnos × conversión) reads REAL
// aggregate counts straight from the academy_progress table. Every sub-metric
// below maps to a backend field; if a field is absent we OMIT the rung rather
// than invent a number. No hardcoded business values.

interface BackendCohortStats {
  total_alumnos?:   number;
  total_autores?:   number;
  total_certified?: number;
  period?:          string;
}

interface CohortMetrics {
  alumnos:   number | null;
  autores:   number | null;  // educadores/authors that reached step 3
  certified: number | null;  // step ≥ 2 (candidato+)
  period:    string | null;
}

function toCohortMetrics(res: unknown): CohortMetrics {
  const d = unwrap<BackendCohortStats>(res) ?? {};
  return {
    alumnos:   typeof d.total_alumnos   === 'number' ? d.total_alumnos   : null,
    autores:   typeof d.total_autores   === 'number' ? d.total_autores   : null,
    certified: typeof d.total_certified === 'number' ? d.total_certified : null,
    period:    typeof d.period          === 'string' ? d.period          : null,
  };
}

async function fetchCohortMetrics(): Promise<CohortMetrics> {
  return toCohortMetrics(await apiFetch<unknown>('/api/v1/academy/cohorts/current'));
}

interface ChainItem {
  labelKey: string; // i18n key in the UiFlywheel namespace
  value: string;
}

/**
 * Build the acquisition chain ONLY from sub-metrics the backend exposes.
 * Rungs whose source field is null are dropped (never faked). The chain is the
 * funnel from people → authors → certified edges → certified AUM.
 */
function buildChain(cohort: CohortMetrics, engine: EngineStats): ChainItem[] {
  const chain: ChainItem[] = [];

  if (cohort.alumnos !== null) {
    chain.push({ labelKey: 'chain_students', value: String(cohort.alumnos) });
  }
  if (cohort.autores !== null) {
    chain.push({ labelKey: 'chain_authors', value: String(cohort.autores) });
  }
  // Conversión alumno → autor: only derivable when both counts are present
  // and there is at least one alumno (avoid divide-by-zero / fabricated %).
  if (cohort.alumnos !== null && cohort.autores !== null && cohort.alumnos > 0) {
    const conv = (cohort.autores / cohort.alumnos) * 100;
    chain.push({ labelKey: 'chain_conversion', value: `${conv.toFixed(1)}%` });
  }
  if (cohort.certified !== null) {
    chain.push({ labelKey: 'chain_certified', value: String(cohort.certified) });
  }
  // Live edges come from the engine stats already loaded by the page.
  chain.push({ labelKey: 'chain_live_edges', value: `${engine.edgesCertified} / ${engine.edgesTotal}` });

  return chain;
}

export function NorthStar({ engineStats, loading, className, style }: NorthStarProps) {
  const t = useTranslations('UiFlywheel');
  const aum = engineStats.aumCertUsd;

  // Real cohort funnel — polled alongside the engine stats (same cadence).
  const { data: cohort } = useSWR('/api/v1/academy/cohorts/current', fetchCohortMetrics, {
    refreshInterval: 30_000,
  });

  const chain = cohort ? buildChain(cohort, engineStats) : [];

  return (
    <div
      className={clsx('panel', className)}
      style={{
        padding:    '18px 22px',
        background: 'linear-gradient(110deg, rgba(157,124,255,.08), transparent 55%)',
        ...style,
      }}
    >
      <div
        style={{
          display:    'flex',
          alignItems: 'flex-start',
          flexWrap:   'wrap',
          gap:        18,
          justifyContent: 'space-between',
        }}
      >
        {/* AUM Certified metric */}
        <div>
          <div
            className="eyebrow"
            style={{ color: 'var(--brand)', fontFamily: 'var(--mono)', fontSize: 10, letterSpacing: '.08em', textTransform: 'uppercase' }}
          >
            {t('north_star_label')}
          </div>
          <div
            className="num"
            style={{
              fontSize:   40,
              fontWeight: 600,
              color:      'var(--tx-hi)',
              lineHeight: 1.1,
              marginTop:  6,
              opacity:    loading ? 0.4 : 1,
              transition: 'opacity .3s',
            }}
          >
            {loading
              ? '—'
              : `$${aum.toLocaleString('en-US', { maximumFractionDigits: 0 })}`}
          </div>
          <div
            className="dim"
            style={{ fontSize: 11.5, marginTop: 2, color: 'var(--tx-dim)' }}
          >
            {t('aum_caption')}
          </div>
        </div>

        {/* Acquisition chain — real cohort funnel */}
        <div style={{ flex: '1 1 420px', maxWidth: 560 }}>
          <div
            className="eyebrow"
            style={{
              fontFamily:    'var(--mono)',
              fontSize:      10,
              letterSpacing: '.08em',
              textTransform: 'uppercase',
              color:         'var(--tx-faint)',
              marginBottom:  8,
            }}
          >
            {t('acquisition_chain')}{cohort?.period ? ` · ${cohort.period}` : ''}
          </div>
          {chain.length === 0 ? (
            <div
              className="dim mono"
              style={{ fontSize: 11, color: 'var(--tx-faint)' }}
            >
              {t('no_cohort_data')}
            </div>
          ) : (
            <div style={{ display: 'flex', gap: 0, alignItems: 'stretch', flexWrap: 'wrap' }}>
              {chain.map((item, i) => (
                <div key={item.labelKey} style={{ display: 'flex', alignItems: 'stretch' }}>
                  <div
                    className="panel-inset"
                    style={{ padding: '8px 12px', flex: 1, minWidth: 96 }}
                  >
                    <div
                      className="num"
                      style={{ fontSize: 16, color: 'var(--tx-hi)', fontWeight: 600 }}
                    >
                      {item.value}
                    </div>
                    <div
                      style={{
                        fontSize:      9.5,
                        color:         'var(--tx-faint)',
                        fontFamily:    'var(--mono)',
                        marginTop:     2,
                      }}
                    >
                      {t(item.labelKey)}
                    </div>
                  </div>
                  {i < chain.length - 1 && (
                    <div
                      style={{
                        width:        18,
                        display:      'grid',
                        placeItems:   'center',
                        color:        'var(--tx-faint)',
                        fontSize:     12,
                      }}
                    >
                      ·
                    </div>
                  )}
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
