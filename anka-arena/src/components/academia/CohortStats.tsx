'use client';

import useSWR from 'swr';
import { useLocale, useTranslations } from 'next-intl';
import { apiFetch } from '@/lib/api';
import { Panel, PanelHeader, PanelBody } from '@/components/ui/Panel';
import { Spinner } from '@/components/ui/Spinner';

// ── Types ─────────────────────────────────────────────────────────────────────

interface CohortData {
  name:              string;
  students:          number;
  authors:           number;
  certified:         number;
  conversionPct:     number;
  aumGenerated:      number;
  updatedAt?:        string;
}

// ── Component ─────────────────────────────────────────────────────────────────

const fetcher = (url: string) => apiFetch<CohortData>(url);

export function CohortStats() {
  const t = useTranslations('PageAcademy');
  const locale = useLocale();
  const { data, isLoading } = useSWR<CohortData>(
    '/api/v1/academy/cohorts/current',
    fetcher,
    {
      refreshInterval: 60_000,          // poll every 60s for live updates
      onErrorRetry: (err, _key, _cfg, revalidate, { retryCount }) => {
        if (err?.status === 404 || err?.status === 401) return;
        if (retryCount >= 2) return;
        setTimeout(() => revalidate({ retryCount }), 8000);
      },
    },
  );

  // Only render real backend data — NO mock fallback.
  const cohort = data ?? null;

  return (
    <Panel>
      <PanelHeader title={t('educator_title')} sub="VISION §8.6" />
      <PanelBody>
        {isLoading && !cohort && (
          <div style={{ display: 'flex', justifyContent: 'center', padding: 12 }}>
            <Spinner size={18} />
          </div>
        )}

        {!isLoading && !cohort && (
          <div style={{ fontSize: 11, color: 'var(--tx-dim)', padding: '4px 0' }}>
            {t('no_cohort')}
          </div>
        )}

        {cohort && (
          <>
            <div className="mono" style={{ fontSize: 10.5, color: 'var(--tx-dim)', marginBottom: 10 }}>
              {t('cohort_name', { name: cohort.name })}
            </div>

            {/* Funnel: alumnos → autores → certificadas */}
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, fontFamily: 'var(--mono)', fontSize: 14, marginBottom: 8 }}>
              <span className="hi" style={{ fontWeight: 700 }}>{cohort.students ?? 0}</span>
              <span className="faint" style={{ fontSize: 13 }}>→</span>
              <span className="hi" style={{ fontWeight: 700 }}>{cohort.authors ?? 0}</span>
              <span className="faint" style={{ fontSize: 13 }}>→</span>
              <span className="up" style={{ fontWeight: 700 }}>{cohort.certified ?? 0}</span>
            </div>

            <div style={{ display: 'flex', gap: 0, fontSize: 9.5, color: 'var(--tx-faint)', fontFamily: 'var(--mono)' }}>
              <span style={{ flex: 1 }}>{t('students')}</span>
              <span style={{ flex: 1, textAlign: 'center' }}>{t('authors')}</span>
              <span style={{ flex: 1, textAlign: 'right' }}>{t('certified')}</span>
            </div>

            <div style={{ height: 1, background: 'var(--line)', margin: '12px 0' }} />

            {/* KV stats */}
            <div style={{ display: 'grid', gridTemplateColumns: '1fr auto', gap: '6px 12px', fontSize: 11.5 }}>
              <span style={{ color: 'var(--tx-dim)' }}>{t('conversion')}</span>
              <span className="up mono" style={{ fontWeight: 600 }}>{(cohort.conversionPct ?? (cohort as any).conversion_pct ?? 0).toFixed(1)}%</span>

              <span style={{ color: 'var(--tx-dim)' }}>{t('aum')}</span>
              <span className="hi mono" style={{ fontWeight: 600 }}>${(cohort.aumGenerated ?? (cohort as any).aum_generated ?? 0).toLocaleString()}</span>
            </div>

            {cohort.updatedAt && (
              <div style={{ marginTop: 12, fontSize: 9.5, color: 'var(--tx-faint)', fontFamily: 'var(--mono)' }}>
                {t('updated', {
                  time: new Date(cohort.updatedAt).toLocaleTimeString(locale === 'es' ? 'es-EC' : locale, { hour: '2-digit', minute: '2-digit' }),
                })}
              </div>
            )}
          </>
        )}
      </PanelBody>
    </Panel>
  );
}
