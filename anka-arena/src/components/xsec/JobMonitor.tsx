'use client';

// JobMonitor — while a job is `running`, polls GET /xsec/jobs/:id every 2s and
// renders the live output in a scrolling monospace log. Once the job settles
// (done/error) the final state stays visible until a new job is focused.
//
// The parent (page.tsx) owns the shared `jobs` list poll; this component just
// picks the currently-running job from it (or a just-created `focusJobId` as a
// fallback before the list has caught up) and does its own tighter 2s poll for
// that single job's output.

import { useEffect, useRef } from 'react';
import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { Badge } from '@/components/ui/Badge';
import { getXsecJob, parseRebalanceResult, type XSecJob } from '@/lib/xsec-api';
import { RebalanceWarnings } from '@/components/xsec/RebalanceWarnings';
import { RebalanceResultReport } from '@/components/xsec/RebalanceResultReport';
import { BacktestReport } from '@/components/xsec/BacktestReport';
import { BacktestSweepReport } from '@/components/xsec/BacktestSweepReport';

interface Props {
  jobs: XSecJob[];
  focusJobId: number | null;
  onSettled: () => void;
}

export function JobMonitor({ jobs, focusJobId, onSettled }: Props) {
  const t = useTranslations('Xsec');

  const runningInList = jobs.find((j) => j.status === 'running');
  const targetId = runningInList?.id ?? focusJobId ?? null;

  const { data: job } = useSWR<XSecJob>(
    targetId != null ? `xsec:job:${targetId}` : null,
    () => getXsecJob(targetId as number),
    {
      refreshInterval: (latest) => (latest && latest.status !== 'running' ? 0 : 2_000),
      revalidateOnFocus: false,
    },
  );

  const wasRunning = useRef(false);
  useEffect(() => {
    if (job?.status === 'running') {
      wasRunning.current = true;
    } else if (wasRunning.current && job) {
      wasRunning.current = false;
      onSettled();
    }
  }, [job, onSettled]);

  if (targetId == null) {
    return (
      <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)', padding: 16 }}>
        {t('monitor_idle')}
      </div>
    );
  }

  const status = job?.status ?? 'running';
  const variant = status === 'running' ? 'brand' : status === 'done' ? 'ok' : 'real';

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
        <Badge variant={variant} dot>{t(`job_status_${status}`)}</Badge>
        <span className="mono" style={{ fontSize: 11, color: 'var(--tx-dim)' }}>
          {t('monitor_job')} #{targetId} · {job?.mode ?? '—'}
        </span>
        {job?.exit_code != null && (
          <span className="mono" style={{ fontSize: 11, color: job.exit_code === 0 ? 'var(--up)' : 'var(--down)' }}>
            exit={job.exit_code}
          </span>
        )}
      </div>
      {job && job.status !== 'running' && (
        <RebalanceWarnings mode={job.mode} output={job.output} />
      )}
      {(() => {
        // Structured execution result for live deploys (rebalance-live /
        // relaunch): read-only here — the actionable "complete" lives in
        // ActionsPanel, next to the deploy controls.
        if (!job || job.status === 'running') return null;
        if (job.mode !== 'rebalance-live' && job.mode !== 'relaunch') return null;
        const result = parseRebalanceResult(job.output);
        return result ? <RebalanceResultReport result={result} /> : null;
      })()}
      {job && job.status !== 'running' && job.mode === 'backtest' ? (
        <BacktestReport job={job} />
      ) : job && job.mode === 'backtest-sweep' && job.output.includes('### SWEEP') ? (
        // Sweep progress is genuinely useful to watch live — each run flushes
        // its own block as soon as it finishes (see executeSweep), so this
        // renders whatever runs have completed so far even while status is
        // still 'running', not just once the whole sweep settles.
        <BacktestSweepReport job={job} />
      ) : (
        <pre
          className="mono"
          style={{
            margin: 0, fontSize: 11, lineHeight: 1.5, color: 'var(--tx-hi)',
            background: 'var(--bg-void)', border: '1px solid var(--line)', borderRadius: 'var(--r)',
            padding: 12, maxHeight: 320, overflow: 'auto', whiteSpace: 'pre-wrap',
          }}
        >
          {job?.output || t('monitor_no_output')}
        </pre>
      )}
    </div>
  );
}
