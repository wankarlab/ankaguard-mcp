'use client';

// C3.4 — Cohortes (educador): real cohort/funnel metrics for the educator.
//
// Reads GET /api/v1/academy/cohorts/current (aggregate counts across the
// academy_progress table; period = current month). The educator is a flywheel
// motor: alumnos → autores → certificadas. Every number here is backend
// computed — NO mocks, NO hardcoded funnel. When there are no students yet the
// counts are simply zero (honest empty funnel), not invented.

import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { apiFetch } from '@/lib/api';
import { LockedScreen } from '@/components/common/LockedScreen';
import { Panel, PanelHeader, PanelBody } from '@/components/ui/Panel';
import { Badge } from '@/components/ui/Badge';
import { Spinner } from '@/components/ui/Spinner';

// Backend shape (envelope unwrapped by apiFetch; fields stay snake_case — the
// adapter layer is what camelCases, and there is no dedicated adapter for this
// endpoint, so we read the raw snake_case keys here).
interface BackendCohortStats {
  total_alumnos?: number;
  total_autores?: number;
  total_certified?: number;
  period?: string;
}

interface CohortMetrics {
  alumnos: number;
  autores: number;
  certified: number;
  period: string;
}

function fetchCohort(url: string): Promise<CohortMetrics> {
  return apiFetch<BackendCohortStats>(url).then((d) => ({
    alumnos: d.total_alumnos ?? 0,
    autores: d.total_autores ?? 0,
    certified: d.total_certified ?? 0,
    period: d.period ?? '',
  }));
}

export default function CohortesPage() {
  const t = useTranslations('PageCohorts');
  const { data, error, isLoading, mutate } = useSWR<CohortMetrics>(
    '/api/v1/academy/cohorts/current',
    fetchCohort,
    { refreshInterval: 60_000 },
  );

  // Conversion rates derived from the real counts (no business constants).
  const alumnos = data?.alumnos ?? 0;
  const autores = data?.autores ?? 0;
  const certified = data?.certified ?? 0;
  const authorRate = alumnos > 0 ? (autores / alumnos) * 100 : 0;
  const certRate = autores > 0 ? (certified / autores) * 100 : 0;

  return (
    <div className="content-pad enter" style={{ maxWidth: 900, margin: '0 auto' }}>
      <PageHeader period={data?.period} />

      <LockedScreen screen="cohortes" bannerOnly>
        <Panel>
          <PanelHeader
            title={t('panel_title')}
            sub={t('panel_sub')}
          >
            <Badge variant="brand">VISION §8.6</Badge>
          </PanelHeader>
          <PanelBody>
            {isLoading && (
              <div style={{ display: 'flex', justifyContent: 'center', padding: 24 }}>
                <Spinner size={20} />
              </div>
            )}

            {error && !isLoading && <ErrorState onRetry={() => mutate()} />}

            {!isLoading && !error && data && (
              <>
                {/* Funnel */}
                <div
                  style={{
                    display: 'grid',
                    gridTemplateColumns: 'repeat(3, minmax(0, 1fr))',
                    gap: 12,
                    marginBottom: 16,
                  }}
                >
                  <FunnelCell label={t('students')} value={alumnos} color="var(--tx-hi)" />
                  <FunnelCell label={t('authors')} value={autores} color="var(--brand)" />
                  <FunnelCell label={t('certified')} value={certified} color="var(--up)" />
                </div>

                {/* Conversion rates */}
                <div
                  style={{
                    display: 'flex',
                    gap: 24,
                    fontFamily: 'var(--mono)',
                    fontSize: 11.5,
                    color: 'var(--tx-dim)',
                    flexWrap: 'wrap',
                  }}
                >
                  <ConvStat label={t('student_to_author')} value={`${authorRate.toFixed(1)}%`} />
                  <ConvStat label={t('author_to_cert')} value={`${certRate.toFixed(1)}%`} />
                </div>

                {alumnos === 0 && (
                  <div style={{ marginTop: 14, fontSize: 12, color: 'var(--tx-dim)' }}>
                    {t('empty_cohort')}
                  </div>
                )}
              </>
            )}
          </PanelBody>
        </Panel>
      </LockedScreen>
    </div>
  );
}

function PageHeader({ period }: { period?: string }) {
  const t = useTranslations('PageCohorts');
  return (
    <div style={{ marginBottom: 18 }}>
      <div
        className="mono"
        style={{
          fontSize: 10,
          letterSpacing: '.08em',
          color: 'var(--brand)',
          textTransform: 'uppercase',
          marginBottom: 4,
        }}
      >
        {period ? t('eyebrow_period', { period }) : t('eyebrow')}
      </div>
      <h2 style={{ margin: 0, fontSize: 20, color: 'var(--tx-hi)', fontWeight: 700 }}>
        {t('title')}
      </h2>
      <p style={{ margin: '6px 0 0', fontSize: 12.5, color: 'var(--tx-dim)', maxWidth: 640 }}>
        {t('intro')}
      </p>
    </div>
  );
}

function FunnelCell({ label, value, color }: { label: string; value: number; color: string }) {
  return (
    <div className="panel-inset" style={{ padding: '14px 12px', textAlign: 'center' }}>
      <div style={{ fontFamily: 'var(--mono)', fontSize: 26, fontWeight: 700, color }}>
        {value}
      </div>
      <div
        style={{
          fontSize: 10,
          textTransform: 'uppercase',
          letterSpacing: '.06em',
          color: 'var(--tx-faint)',
          marginTop: 4,
        }}
      >
        {label}
      </div>
    </div>
  );
}

function ConvStat({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <span style={{ color: 'var(--tx-hi)', fontWeight: 600 }}>{value}</span>
      <span style={{ marginLeft: 6 }}>{label}</span>
    </div>
  );
}

function ErrorState({ onRetry }: { onRetry: () => void }) {
  const t = useTranslations('PageCohorts');
  return (
    <div style={{ textAlign: 'center', padding: '24px 12px' }}>
      <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--down, var(--tx-hi))' }}>
        {t('error_title')}
      </div>
      <div style={{ fontSize: 12, marginTop: 6, color: 'var(--tx-dim)' }}>
        {t('error_body')}
      </div>
      <button
        type="button"
        onClick={onRetry}
        style={{
          fontFamily: 'var(--sans)',
          fontSize: 12,
          fontWeight: 600,
          padding: '7px 12px',
          borderRadius: 'var(--r)',
          border: '1px solid var(--line-2)',
          background: 'transparent',
          color: 'var(--tx)',
          cursor: 'pointer',
          marginTop: 12,
        }}
      >
        {t('retry')}
      </button>
    </div>
  );
}
