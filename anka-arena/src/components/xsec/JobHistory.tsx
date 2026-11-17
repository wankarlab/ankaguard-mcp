'use client';

// JobHistory — last N XSec jobs (mode/status/when/who), each row expandable to
// show its (possibly truncated by the server) output. Receives the shared
// `jobs` list from the parent page (already polled) — no fetch of its own.

import { useState } from 'react';
import { useTranslations } from 'next-intl';
import { Badge } from '@/components/ui/Badge';
import { formatJobArgs, type XSecJob, type XSecJobStatus } from '@/lib/xsec-api';
import { RebalanceWarnings } from '@/components/xsec/RebalanceWarnings';

function fmtTime(iso?: string | null): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (isNaN(d.getTime()) || d.getUTCFullYear() < 2000) return '—';
  return d.toLocaleString();
}

const STATUS_VARIANT: Record<XSecJobStatus, 'brand' | 'ok' | 'real'> = {
  running: 'brand',
  done: 'ok',
  error: 'real',
};

interface Props {
  jobs: XSecJob[];
  isLoading: boolean;
  // Optional comparison selection (Fase 3) — only offered for jobs where
  // selectable(job) is true (e.g. mode==='backtest', which has a single
  // parseable report; a sweep job has many and isn't a clean comparison unit).
  selectedIds?: Set<number>;
  onToggleSelect?: (jobId: number) => void;
  selectable?: (job: XSecJob) => boolean;
}

export function JobHistory({ jobs, isLoading, selectedIds, onToggleSelect, selectable }: Props) {
  const t = useTranslations('Xsec');
  const [expanded, setExpanded] = useState<number | null>(null);
  const showSelection = !!onToggleSelect;
  const gridCols = showSelection ? '28px 90px 130px 1fr 160px 120px' : '90px 130px 1fr 160px 120px';

  if (isLoading) {
    return <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)', padding: 16 }}>{t('loading')}</div>;
  }
  if (jobs.length === 0) {
    return <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)', padding: 16 }}>{t('no_jobs')}</div>;
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
      <div style={{ display: 'grid', gridTemplateColumns: gridCols, gap: 8, padding: '4px 8px', fontSize: 10, color: 'var(--tx-faint)', textTransform: 'uppercase' }}>
        {showSelection && <span />}
        <span>{t('col_job_id')}</span>
        <span>{t('col_mode')}</span>
        <span>{t('col_status')}</span>
        <span>{t('col_when')}</span>
        <span>{t('col_who')}</span>
      </div>
      {jobs.map((job) => {
        const isOpen = expanded === job.id;
        const canSelect = showSelection && (selectable ? selectable(job) : true);
        return (
          <div key={job.id} style={{ border: '1px solid var(--line)', borderRadius: 'var(--r)', overflow: 'hidden' }}>
            <div
              onClick={() => setExpanded(isOpen ? null : job.id)}
              style={{
                display: 'grid', gridTemplateColumns: gridCols, gap: 8,
                width: '100%', padding: '8px 8px', background: isOpen ? 'var(--bg-inset)' : 'transparent',
                cursor: 'pointer', textAlign: 'left', alignItems: 'center',
              }}
            >
              {showSelection && (
                <span onClick={(e) => e.stopPropagation()}>
                  <input
                    type="checkbox"
                    checked={selectedIds?.has(job.id) ?? false}
                    disabled={!canSelect} suppressHydrationWarning
                    onChange={() => onToggleSelect?.(job.id)}
                  />
                </span>
              )}
              <span className="mono" style={{ fontSize: 12, color: 'var(--tx-hi)' }}>#{job.id}</span>
              <span className="mono" style={{ fontSize: 12, color: 'var(--tx-dim)' }}>{job.mode}</span>
              <span><Badge variant={STATUS_VARIANT[job.status] ?? 'neutral'}>{t(`job_status_${job.status}`)}</Badge></span>
              <span className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{fmtTime(job.started_at)}</span>
              <span className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{job.created_by || '—'}</span>
            </div>
            {isOpen && (
              <div style={{ padding: '0 12px 12px' }}>
                <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', marginBottom: 6 }}>
                  {t('args_label')}: {formatJobArgs(job) || '—'} · {t('finished_at_label')}: {fmtTime(job.finished_at)}
                  {job.exit_code != null && ` · exit=${job.exit_code}`}
                </div>
                {job.status !== 'running' && (
                  <div style={{ marginBottom: 8 }}>
                    <RebalanceWarnings mode={job.mode} output={job.output} compact />
                  </div>
                )}
                <pre
                  className="mono"
                  style={{
                    margin: 0, fontSize: 11, color: 'var(--tx-hi)', background: 'var(--bg-void)',
                    border: '1px solid var(--line)', borderRadius: 4, padding: 10,
                    maxHeight: 260, overflow: 'auto', whiteSpace: 'pre-wrap',
                  }}
                >
                  {job.output || t('monitor_no_output')}
                </pre>
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}
